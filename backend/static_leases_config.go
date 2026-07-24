package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---------- Struct Definitions ----------

type StaticLeaseConfig struct {
	Section  string `json:"section,omitempty"`
	Hostname string `json:"hostname"`
	MAC      string `json:"mac"`
	IPAddr   string `json:"ipaddr"`
}

type staticLeaseValidationError struct {
	message string
}

func (e staticLeaseValidationError) Error() string {
	return e.message
}

var staticLeaseMACPattern = regexp.MustCompile(`^([0-9A-F]{2}:){5}[0-9A-F]{2}$`)

// ---------- SID Helper ----------

func staticLeaseSIDFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	sid := strings.TrimPrefix(authHeader, "Bearer ")

	if sid == authHeader {
		return ""
	}

	return strings.TrimSpace(sid)
}

// ---------- UCI Helpers: AC-only ----------

func getUciStaticLeasesLocal(sid string) ([]StaticLeaseConfig, error) {
	useSid := resolveSid("", sid)

	res, err := ubusCallJSONLocal(useSid, "uci", "get", map[string]any{
		"config": "dhcp",
	})
	if err != nil {
		return nil, err
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return []StaticLeaseConfig{}, nil
	}

	list := make([]StaticLeaseConfig, 0)

	for sectionName, v := range values {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}

		if item[".type"] != "host" {
			continue
		}

		entry := StaticLeaseConfig{
			Section:  sectionName,
			Hostname: getString(item, "name"),
			MAC:      strings.ToUpper(getString(item, "mac")),
			IPAddr:   getString(item, "ip"),
		}

		if entry.MAC == "" || entry.IPAddr == "" {
			continue
		}

		list = append(list, entry)
	}

	return list, nil
}

func applyLocalUciLeaseChange(sid string, action func(currentSid string) error) error {
	useSid := resolveSid("", sid)

	if err := action(useSid); err != nil {
		return err
	}

	if _, err := ubusCallJSONLocal(useSid, "uci", "commit", map[string]any{
		"config": "dhcp",
	}); err != nil {
		return fmt.Errorf("commit failed: %v", err)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)

		_, _ = ubusCallJSONLocal(useSid, "service", "event", map[string]any{
			"type": "config.change",
			"data": map[string]any{
				"package": "dhcp",
			},
		})
	}()

	return nil
}

// ---------- Shared static-lease service (used by both /api/lan/static-lease*
// and /api/lan/static-leases-config so the UCI + reload logic lives once) ----------

// staticLeaseUpsert adds or updates the dhcp host reservation matched by MAC.
// Returns the affected section and whether it was an update (vs a fresh add).
func staticLeaseUpsert(sid, mac, ip, name string) (string, bool, error) {
	mac = staticLeaseNormalizeMAC(mac)
	ip = strings.TrimSpace(ip)

	existing, err := getUciStaticLeasesLocal(sid)
	if err != nil {
		return "", false, err
	}

	networkSections, err := ifaceGetNetworkInterfaceSections(resolveSid("", sid))
	if err != nil {
		return "", false, fmt.Errorf("read LAN configuration failed: %v", err)
	}
	lanValues, ok := networkSections["lan"]
	if !ok {
		return "", false, fmt.Errorf("LAN configuration not found")
	}
	lanIP := strings.TrimSpace(ifaceValueToString(lanValues["ipaddr"]))
	netmask := strings.TrimSpace(ifaceValueToString(lanValues["netmask"]))
	if err := staticLeaseValidateAssignment(existing, mac, ip, lanIP, netmask); err != nil {
		return "", false, err
	}

	targetSection := ""
	for _, lease := range existing {
		if strings.EqualFold(lease.MAC, mac) {
			targetSection = lease.Section
			break
		}
	}
	updated := targetSection != ""

	resultSection := targetSection
	err = applyLocalUciLeaseChange(sid, func(currSid string) error {
		values := map[string]string{"mac": mac, "ip": ip}
		if name != "" {
			values["name"] = name
		}

		if targetSection != "" {
			_, callErr := ubusCallJSONLocal(currSid, "uci", "set", map[string]any{
				"config":  "dhcp",
				"section": targetSection,
				"values":  values,
			})
			return callErr
		}

		res, callErr := ubusCallJSONLocal(currSid, "uci", "add", map[string]any{
			"config": "dhcp",
			"type":   "host",
			"values": values,
		})
		if callErr != nil {
			return callErr
		}
		resultSection, _ = res["section"].(string)
		return nil
	})
	if err != nil {
		return "", updated, err
	}
	return resultSection, updated, nil
}

// staticLeaseDeleteByMAC removes the dhcp host reservation matched by MAC.
// Returns the removed section and whether anything was removed.
func staticLeaseDeleteByMAC(sid, mac string) (string, bool, error) {
	mac = staticLeaseNormalizeMAC(mac)

	existing, err := getUciStaticLeasesLocal(sid)
	if err != nil {
		return "", false, err
	}
	targetSection := ""
	for _, lease := range existing {
		if strings.EqualFold(lease.MAC, mac) {
			targetSection = lease.Section
			break
		}
	}
	if targetSection == "" {
		return "", false, nil
	}
	if err := staticLeaseDeleteBySection(sid, targetSection); err != nil {
		return "", false, err
	}
	return targetSection, true, nil
}

// staticLeaseDeleteBySection removes the dhcp host reservation by UCI section.
func staticLeaseDeleteBySection(sid, section string) error {
	return applyLocalUciLeaseChange(sid, func(currSid string) error {
		_, callErr := ubusCallJSONLocal(currSid, "uci", "delete", map[string]any{
			"config":  "dhcp",
			"section": section,
		})
		return callErr
	})
}

func staticLeaseNormalizeMAC(mac string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(mac), "-", ":"))
}

func staticLeaseValidateAssignment(
	existing []StaticLeaseConfig,
	mac string,
	ip string,
	lanIP string,
	netmask string,
) error {
	if !staticLeaseMACPattern.MatchString(staticLeaseNormalizeMAC(mac)) {
		return staticLeaseValidationError{message: "invalid MAC address"}
	}

	ipValue, valid := ifaceIPv4ToUint32(ip)
	if !valid {
		return staticLeaseValidationError{message: "invalid IPv4 address"}
	}
	lanValue, valid := ifaceIPv4ToUint32(lanIP)
	if !valid {
		return fmt.Errorf("invalid LAN IPv4 configuration")
	}
	maskValue, valid := ifaceIPv4ToUint32(netmask)
	if !valid || !ifaceIsValidNetmask(netmask) {
		return fmt.Errorf("invalid LAN netmask configuration")
	}

	network := lanValue & maskValue
	broadcast := network | ^maskValue
	if ipValue&maskValue != network || ipValue == network || ipValue == broadcast {
		return staticLeaseValidationError{message: "IPv4 address is outside the current LAN subnet"}
	}

	lanParts := strings.Split(lanIP, ".")
	ipParts := strings.Split(ip, ".")
	if len(lanParts) != 4 || len(ipParts) != 4 {
		return staticLeaseValidationError{message: "invalid IPv4 address"}
	}
	lastOctet, err := strconv.Atoi(ipParts[3])
	samePrefix := lanParts[0] == ipParts[0] &&
		lanParts[1] == ipParts[1] &&
		lanParts[2] == ipParts[2]
	if err != nil || !samePrefix || lastOctet < 6 || lastOctet > 99 {
		prefix := strings.Join(lanParts[:3], ".")
		return staticLeaseValidationError{
			message: fmt.Sprintf("IPv4 address must be between %s.6 and %s.99", prefix, prefix),
		}
	}

	normalizedMAC := staticLeaseNormalizeMAC(mac)
	for _, lease := range existing {
		if strings.TrimSpace(lease.IPAddr) == ip &&
			staticLeaseNormalizeMAC(lease.MAC) != normalizedMAC {
			return staticLeaseValidationError{message: "IPv4 address is already assigned to another device"}
		}
	}

	return nil
}

func staticLeaseErrorStatus(err error) int {
	if _, ok := err.(staticLeaseValidationError); ok {
		return http.StatusBadRequest
	}
	return http.StatusInternalServerError
}

// ---------- HTTP Handler: AC-only ----------

func staticLeaseConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sid := staticLeaseSIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		list, err := getUciStaticLeasesLocal(sid)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(list)

	case http.MethodPost:
		var req StaticLeaseConfig

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		req.Hostname = strings.TrimSpace(req.Hostname)
		req.MAC = staticLeaseNormalizeMAC(req.MAC)
		req.IPAddr = strings.TrimSpace(req.IPAddr)

		if req.MAC == "" || req.IPAddr == "" {
			http.Error(w, `{"error":"mac and ipaddr are required"}`, http.StatusBadRequest)
			return
		}

		if _, _, err := staticLeaseUpsert(sid, req.MAC, req.IPAddr, req.Hostname); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), staticLeaseErrorStatus(err))
			return
		}

		_, _ = w.Write([]byte(`{"status":"ok"}`))

	case http.MethodDelete:
		section := strings.TrimSpace(r.URL.Query().Get("section"))
		if section == "" {
			http.Error(w, `{"error":"missing section param"}`, http.StatusBadRequest)
			return
		}

		if err := staticLeaseDeleteBySection(sid, section); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(`{"status":"ok"}`))

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
