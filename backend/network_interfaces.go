package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- Data Structures ----------

type InterfaceDHCPSettings struct {
	Enabled     bool     `json:"enabled"`
	Start       string   `json:"start"`
	Limit       string   `json:"limit"`
	LeaseTime   string   `json:"leasetime"`
	DynamicDHCP bool     `json:"dynamic_dhcp"`
	Force       bool     `json:"force"`
	DHCPOptions []string `json:"dhcp_options"`
}

type InterfaceStats struct {
	ID           string                 `json:"id"`
	Name         string                 `json:"name"`
	Device       string                 `json:"device"`
	Protocol     string                 `json:"protocol"`
	Uptime       int                    `json:"uptime"`
	MacAddr      string                 `json:"macaddr"`
	RxBytes      int64                  `json:"rx_bytes"`
	RxPkts       int64                  `json:"rx_pkts"`
	TxBytes      int64                  `json:"tx_bytes"`
	TxPkts       int64                  `json:"tx_pkts"`
	IPv4         string                 `json:"ipv4"`
	IPAddr       string                 `json:"ipaddr"`
	Netmask      string                 `json:"netmask"`
	Gateway      string                 `json:"gateway"`
	DNS          string                 `json:"dns"`
	FirewallZone string                 `json:"firewall_zone"`
	DHCP         *InterfaceDHCPSettings `json:"dhcp,omitempty"`
	Auto         bool                   `json:"auto"`
	Up           bool                   `json:"up"`
	Editable     bool                   `json:"editable"`
	CanRestart   bool                   `json:"can_restart"`
	CanStop      bool                   `json:"can_stop"`
	CanDelete    bool                   `json:"can_delete"`
}

type APManagementInfo struct {
	Name         string `json:"name"`
	AntennaIndex int    `json:"antenna_index,omitempty"`
	IP           string `json:"ip"`
	Online       bool   `json:"online"`
	IPAddr       string `json:"ipaddr"`
	Netmask      string `json:"netmask"`
	Gateway      string `json:"gateway"`
	DNS          string `json:"dns"`
	Error        string `json:"error,omitempty"`
}

type InterfacesResponse struct {
	ACName       string             `json:"ac_name"`
	ACIP         string             `json:"ac_ip"`
	Interfaces   []InterfaceStats   `json:"interfaces"`
	APManagement []APManagementInfo `json:"ap_management"`
}

type InterfaceDHCPReq struct {
	Enabled     bool     `json:"enabled"`
	Start       string   `json:"start"`
	Limit       string   `json:"limit"`
	LeaseTime   string   `json:"leasetime"`
	DynamicDHCP bool     `json:"dynamic_dhcp"`
	Force       bool     `json:"force"`
	DHCPOptions []string `json:"dhcp_options"`
}

type InterfaceActionReq struct {
	Action    string            `json:"action"`
	Interface string            `json:"interface"`
	Name      string            `json:"name"`
	IPAddr    string            `json:"ipaddr"`
	Netmask   string            `json:"netmask"`
	Gateway   string            `json:"gateway"`
	DNS       string            `json:"dns"`
	DHCP      *InterfaceDHCPReq `json:"dhcp,omitempty"`
	Auto      *bool             `json:"auto"`
}

type InterfaceActionResponse struct {
	OK               bool `json:"ok"`
	Changed          bool `json:"changed"`
	LANRestarting    bool `json:"lan_restarting,omitempty"`
	EstimatedSeconds int  `json:"estimated_seconds,omitempty"`
}

var ifaceNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

var (
	errIfaceLANIPFixed             = errors.New("LAN IPv4 address is fixed")
	errIfaceLANNetmaskIncompatible = errors.New("subnet mask is incompatible with current network configuration")
	errIfaceDHCPStartTooLow        = errors.New("DHCP start address offset must be 100 or greater")
	errIfaceDHCPPoolIncompatible   = errors.New("DHCP address pool is incompatible with current network configuration")
)

// ---------- HTTP Handler ----------

func interfacesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	sid := strings.TrimSpace(auth[len("Bearer "):])
	if sid == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		handleGetInterfaces(w, sid)

	case http.MethodPost:
		handleInterfaceAction(w, r, sid)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ---------- GET ----------

func handleGetInterfaces(w http.ResponseWriter, localSid string) {
	acName := ifaceLocalHostname(localSid)
	if acName == "" {
		acName = "Main Module"
	}

	acIP := ifaceLocalLanIP(localSid)

	interfaces, err := ifaceGetLocalInterfaces(localSid)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"get AC interfaces failed: %v"}`, err), http.StatusInternalServerError)
		return
	}

	resp := InterfacesResponse{
		ACName:       acName,
		ACIP:         acIP,
		Interfaces:   interfaces,
		APManagement: ifaceGetAPManagementList(),
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func ifaceGetLocalInterfaces(localSid string) ([]InterfaceStats, error) {
	uciSections, err := ifaceGetNetworkInterfaceSections(localSid)
	if err != nil {
		return nil, err
	}

	firewallZones := ifaceGetFirewallZoneByNetwork(localSid)
	dhcpSections := ifaceGetDHCPSectionsByName(localSid)

	dumpByID := map[string]map[string]any{}

	dump, err := ubusCallJSONLocal(localSid, "network.interface", "dump", nil)
	if err == nil {
		if ifacesRaw, ok := dump["interface"].([]any); ok {
			for _, ifaceRaw := range ifacesRaw {
				iface, ok := ifaceRaw.(map[string]any)
				if !ok {
					continue
				}

				id, _ := iface["interface"].(string)
				if id != "" {
					dumpByID[id] = iface
				}
			}
		}
	}

	results := make([]InterfaceStats, 0)

	for id, uciValues := range uciSections {
		if id == "" || id == "loopback" {
			continue
		}

		proto := ifaceValueToString(uciValues["proto"])
		device := ifaceValueToString(uciValues["device"])
		if device == "" {
			device = ifaceValueToString(uciValues["ifname"])
		}

		ipaddr := ifaceValueToString(uciValues["ipaddr"])
		netmask := ifaceValueToString(uciValues["netmask"])
		gateway := ifaceValueToString(uciValues["gateway"])
		dns := ifaceValueToString(uciValues["dns"])
		auto := ifaceAutoValue(uciValues["auto"])

		stat := InterfaceStats{
			ID:           id,
			Name:         strings.ToUpper(id),
			Device:       device,
			Protocol:     ifaceProtocolLabel(proto),
			IPAddr:       ipaddr,
			Netmask:      netmask,
			Gateway:      gateway,
			DNS:          dns,
			FirewallZone: firewallZones[id],
			Auto:         auto,
		}

		if ipaddr != "" && netmask != "" {
			stat.IPv4 = fmt.Sprintf("%s/%d", ipaddr, ifaceMaskToCIDR(netmask))
		}

		if id == "lan" {
			// Read the explicitly named dhcp.lan section. Multiple DHCP pools
			// can intentionally share interface=lan, so indexing by interface
			// would allow an internal pool to overwrite this value.
			stat.DHCP = ifaceDHCPSettingsFromValues(dhcpSections["lan"])
		}

		if dumpIface, ok := dumpByID[id]; ok {
			stat.Up = ifaceBool(dumpIface["up"])

			if uptime, ok := dumpIface["uptime"].(float64); ok && stat.Up {
				stat.Uptime = int(uptime)
			}

			if dumpDevice := ifaceDeviceName(dumpIface); dumpDevice != "" {
				stat.Device = dumpDevice
			}

			if ipv4Addrs, ok := dumpIface["ipv4-address"].([]any); ok && len(ipv4Addrs) > 0 {
				ipStrings := make([]string, 0)

				for _, addrRaw := range ipv4Addrs {
					addrMap, ok := addrRaw.(map[string]any)
					if !ok {
						continue
					}

					ip, _ := addrMap["address"].(string)
					mask, _ := addrMap["mask"].(float64)

					if ip != "" {
						ipStrings = append(ipStrings, fmt.Sprintf("%s/%d", ip, int(mask)))
					}
				}

				if len(ipStrings) > 0 {
					stat.IPv4 = strings.Join(ipStrings, ", ")
				}
			}
		}

		if stat.Device != "" && !strings.HasPrefix(stat.Device, "@") {
			devStatus, _ := ubusCallJSONLocal(localSid, "network.device", "status", map[string]any{
				"name": stat.Device,
			})

			if devStatus != nil {
				if mac, ok := devStatus["macaddr"].(string); ok {
					stat.MacAddr = strings.ToUpper(mac)
				}

				if stats, ok := devStatus["statistics"].(map[string]any); ok {
					if rx, ok := stats["rx_bytes"].(float64); ok {
						stat.RxBytes = int64(rx)
					}
					if rp, ok := stats["rx_packets"].(float64); ok {
						stat.RxPkts = int64(rp)
					}
					if tx, ok := stats["tx_bytes"].(float64); ok {
						stat.TxBytes = int64(tx)
					}
					if tp, ok := stats["tx_packets"].(float64); ok {
						stat.TxPkts = int64(tp)
					}
				}
			}
		}

		if stat.MacAddr == "" && stat.Device == "@lan" {
			if lanDev := ifaceValueToString(uciSections["lan"]["device"]); lanDev != "" {
				devStatus, _ := ubusCallJSONLocal(localSid, "network.device", "status", map[string]any{
					"name": lanDev,
				})
				if mac, ok := devStatus["macaddr"].(string); ok {
					stat.MacAddr = strings.ToUpper(mac)
				}
			}
		}

		stat.Editable = ifaceIsEditable(id, proto, device)
		stat.CanRestart = stat.Editable
		stat.CanStop = stat.Editable && id != "lan"
		stat.CanDelete = stat.Editable && id != "lan"

		results = append(results, stat)
	}

	ifaceSortInterfaces(results)

	return results, nil
}

func ifaceGetNetworkInterfaceSections(localSid string) (map[string]map[string]any, error) {
	res, err := ubusCallJSONLocal(localSid, "uci", "get", map[string]any{
		"config": "network",
		"type":   "interface",
	})
	if err != nil {
		return nil, err
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return map[string]map[string]any{}, nil
	}

	out := map[string]map[string]any{}

	for sectionName, raw := range values {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		out[sectionName] = m
	}

	return out, nil
}

func ifaceIsEditable(id string, proto string, device string) bool {
	id = strings.TrimSpace(id)
	proto = strings.TrimSpace(proto)
	device = strings.TrimSpace(device)

	if id == "" || id == "loopback" || id == "wan" || id == "wan6" {
		return false
	}

	if id == "lan" {
		return proto == "static"
	}

	if proto != "static" {
		return false
	}

	if device == "@lan" || device == "br-lan" || device == "" {
		return true
	}

	return false
}

func ifaceSortInterfaces(items []InterfaceStats) {
	weight := func(id string) int {
		switch id {
		case "lan":
			return 0
		case "lan_alias":
			return 10
		case "wan":
			return 90
		case "wan6":
			return 91
		default:
			if strings.Contains(id, "alias") {
				return 20
			}
			return 50
		}
	}

	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			wi := weight(items[i].ID)
			wj := weight(items[j].ID)

			if wj < wi || (wj == wi && items[j].ID < items[i].ID) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func ifaceDeviceName(iface map[string]any) string {
	if dev, ok := iface["device"].(string); ok && dev != "" {
		return dev
	}

	if dev, ok := iface["l3_device"].(string); ok && dev != "" {
		return dev
	}

	if devices, ok := iface["devices"].([]any); ok && len(devices) > 0 {
		if dev, ok := devices[0].(string); ok && dev != "" {
			return dev
		}
	}

	return ""
}

func ifaceProtocolLabel(proto string) string {
	switch proto {
	case "static":
		return "Static address"
	case "dhcp":
		return "DHCP client"
	case "none":
		return "Unmanaged"
	default:
		if proto == "" {
			return "-"
		}
		return proto
	}
}

func ifaceBool(v any) bool {
	b, _ := v.(bool)
	return b
}

func ifaceAutoValue(v any) bool {
	if v == nil {
		return true
	}

	switch t := v.(type) {
	case string:
		return t != "0"
	case float64:
		return int(t) != 0
	case bool:
		return t
	default:
		return true
	}
}

func ifaceMaskToCIDR(mask string) int {
	ip := strings.Split(mask, ".")
	if len(ip) != 4 {
		return 0
	}

	total := 0

	for _, part := range ip {
		n, err := strconv.Atoi(part)
		if err != nil {
			return 0
		}

		for i := 7; i >= 0; i-- {
			if n&(1<<i) != 0 {
				total++
			}
		}
	}

	return total
}

// ---------- Firewall zone and DHCP helpers ----------

func ifaceGetFirewallZoneByNetwork(localSid string) map[string]string {
	out := map[string]string{}

	res, err := ubusCallJSONLocal(localSid, "uci", "get", map[string]any{
		"config": "firewall",
		"type":   "zone",
	})
	if err != nil {
		return out
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return out
	}

	for _, raw := range values {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		zoneName := ifaceValueToString(m["name"])
		for _, network := range ifaceValueToList(m["network"]) {
			if network != "" && zoneName != "" {
				out[network] = zoneName
			}
		}
	}

	return out
}

func ifaceGetDHCPSectionsByName(localSid string) map[string]map[string]any {
	out := map[string]map[string]any{}

	res, err := ubusCallJSONLocal(localSid, "uci", "get", map[string]any{
		"config": "dhcp",
		"type":   "dhcp",
	})
	if err != nil {
		return out
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return out
	}

	for sectionName, raw := range values {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		out[sectionName] = m
	}

	return out
}

func ifaceDHCPSettingsFromValues(values map[string]any) *InterfaceDHCPSettings {
	if values == nil {
		return &InterfaceDHCPSettings{
			Enabled:     false,
			Start:       "100",
			Limit:       "150",
			LeaseTime:   "12h",
			DynamicDHCP: true,
			Force:       false,
			DHCPOptions: []string{},
		}
	}

	enabled := !ifaceBoolUCI(values["ignore"], false)
	if _, exists := values["lrap_client_enabled"]; exists {
		enabled = ifaceBoolUCI(values["lrap_client_enabled"], enabled)
	}

	dynamicDHCP := ifaceBoolUCI(values["dynamicdhcp"], true)
	if _, exists := values["lrap_client_dynamicdhcp"]; exists {
		dynamicDHCP = ifaceBoolUCI(values["lrap_client_dynamicdhcp"], dynamicDHCP)
	}

	return &InterfaceDHCPSettings{
		Enabled:     enabled,
		Start:       ifaceStringDefault(values["start"], "100"),
		Limit:       ifaceStringDefault(values["limit"], "150"),
		LeaseTime:   ifaceStringDefault(values["leasetime"], "12h"),
		DynamicDHCP: dynamicDHCP,
		Force:       ifaceBoolUCI(values["force"], false),
		DHCPOptions: ifaceValueToList(values["dhcp_option"]),
	}
}

func ifaceEnsureLanDHCPSection(localSid string) error {
	sections := ifaceGetDHCPSectionsByName(localSid)
	if _, ok := sections["lan"]; ok {
		return nil
	}

	_, err := ubusCallJSONLocal(localSid, "uci", "add", map[string]any{
		"config": "dhcp",
		"type":   "dhcp",
		"name":   "lan",
		"values": map[string]string{
			"interface": "lan",
		},
	})
	if err != nil {
		return fmt.Errorf("uci add dhcp.lan failed: %v", err)
	}

	return nil
}

func ifaceUpdateLanDHCP(localSid string, netmask string, req *InterfaceDHCPReq) error {
	if req == nil {
		return nil
	}

	if err := ifaceValidateLanDHCPPool(netmask, req); err != nil {
		return err
	}

	if err := ifaceEnsureLanDHCPSection(localSid); err != nil {
		return err
	}

	start := strings.TrimSpace(req.Start)
	limit := strings.TrimSpace(req.Limit)
	leasetime := strings.TrimSpace(req.LeaseTime)

	if start == "" || limit == "" || leasetime == "" {
		return fmt.Errorf("dhcp start, limit and lease time are required")
	}

	_, err := ubusCallJSONLocal(localSid, "uci", "set", map[string]any{
		"config":  "dhcp",
		"section": "lan",
		"values": map[string]string{
			"interface":               "lan",
			"ignore":                  "0",
			"start":                   start,
			"limit":                   limit,
			"leasetime":               leasetime,
			"lrap_client_enabled":     ifaceBoolToUCI(req.Enabled),
			"lrap_client_dynamicdhcp": ifaceBoolToUCI(req.DynamicDHCP),
			"dynamicdhcp":             ifaceBoolToUCI(req.Enabled && req.DynamicDHCP),
			"force":                   ifaceBoolToUCI(req.Force),
		},
	})
	if err != nil {
		return fmt.Errorf("uci set dhcp.lan failed: %v", err)
	}

	cmdParts := []string{
		"uci -q delete dhcp.lan.dhcp_option >/dev/null 2>&1 || true",
	}

	for _, opt := range req.DHCPOptions {
		opt = strings.TrimSpace(opt)
		if opt == "" {
			continue
		}

		cmdParts = append(
			cmdParts,
			fmt.Sprintf("uci add_list dhcp.lan.dhcp_option=%s", ifaceShellQuote(opt)),
		)
	}

	if err := exec.Command("sh", "-c", strings.Join(cmdParts, "; ")).Run(); err != nil {
		return fmt.Errorf("uci set dhcp options failed: %v", err)
	}

	// Keep the ordinary client pool and the internal Antenna pool mutually
	// exclusive. OpenWrt requires range tags to be UCI list values; writing
	// "tag" in the ubus values map above would turn it into a scalar option and
	// dnsmasq would silently expose the ordinary pool to Antennas.
	if _, err := ensureProtectedDHCPConfig(false, false); err != nil {
		return fmt.Errorf("protect internal Antenna DHCP pool failed: %v", err)
	}

	return nil
}

func ifaceCommitDHCP(localSid string) error {
	_, err := ubusCallJSONLocal(localSid, "uci", "commit", map[string]any{
		"config": "dhcp",
	})
	if err != nil {
		return fmt.Errorf("uci commit dhcp failed: %v", err)
	}

	return nil
}

func ifaceAsyncRestartDNSMasq() {
	go func() {
		time.Sleep(1 * time.Second)
		_ = exec.Command("/etc/init.d/dnsmasq", "restart").Run()
	}()
}

func ifaceDeleteNetworkOption(localSid string, section string, option string) {
	_, _ = ubusCallJSONLocal(localSid, "uci", "delete", map[string]any{
		"config":  "network",
		"section": section,
		"option":  option,
	})
}

func ifaceStringDefault(v any, fallback string) string {
	s := ifaceValueToString(v)
	if s == "" {
		return fallback
	}
	return s
}

func ifaceBoolUCI(v any, fallback bool) bool {
	s := strings.ToLower(strings.TrimSpace(ifaceValueToString(v)))
	if s == "" {
		return fallback
	}
	if s == "1" || s == "true" || s == "yes" || s == "on" {
		return true
	}
	if s == "0" || s == "false" || s == "no" || s == "off" {
		return false
	}
	return fallback
}

func ifaceBoolToUCI(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func ifaceShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// ---------- POST actions ----------

func handleInterfaceAction(w http.ResponseWriter, r *http.Request, localSid string) {
	var req InterfaceActionReq

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"bad request"}`, http.StatusBadRequest)
		return
	}

	req.Action = strings.TrimSpace(req.Action)
	req.Interface = strings.TrimSpace(req.Interface)
	req.Name = strings.TrimSpace(req.Name)
	req.IPAddr = strings.TrimSpace(req.IPAddr)
	req.Netmask = strings.TrimSpace(req.Netmask)

	var err error
	result := InterfaceActionResponse{OK: true, Changed: true}

	switch req.Action {
	case "create":
		err = ifaceCreateAlias(localSid, req)

	case "edit":
		result, err = ifaceEdit(localSid, req)

	case "restart":
		err = ifaceRestart(localSid, req.Interface)
		if err == nil && req.Interface == "lan" {
			result.LANRestarting = true
			result.EstimatedSeconds = 15
		}

	case "stop":
		err = ifaceStop(localSid, req.Interface)

	case "delete":
		err = ifaceDelete(localSid, req.Interface)

	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}

	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, errIfaceLANIPFixed) ||
			errors.Is(err, errIfaceLANNetmaskIncompatible) ||
			errors.Is(err, errIfaceDHCPStartTooLow) ||
			errors.Is(err, errIfaceDHCPPoolIncompatible) {
			status = http.StatusBadRequest
		}
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), status)
		return
	}

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}

func ifaceCreateAlias(localSid string, req InterfaceActionReq) error {
	if req.Name == "" {
		return fmt.Errorf("interface name is required")
	}

	if !ifaceNameRe.MatchString(req.Name) {
		return fmt.Errorf("interface name can only contain letters, numbers and underscore")
	}

	if req.Name == "lan" || req.Name == "wan" || req.Name == "wan6" || req.Name == "loopback" {
		return fmt.Errorf("reserved interface name")
	}

	if req.IPAddr == "" || req.Netmask == "" {
		return fmt.Errorf("ipaddr and netmask are required")
	}

	if !ifaceIsValidNetmask(req.Netmask) {
		return fmt.Errorf("invalid netmask")
	}

	ifaceSections, err := ifaceGetNetworkInterfaceSections(localSid)
	if err != nil {
		return err
	}

	if _, exists := ifaceSections[req.Name]; exists {
		return fmt.Errorf("interface already exists")
	}

	auto := "1"
	if req.Auto != nil && !*req.Auto {
		auto = "0"
	}

	_, err = ubusCallJSONLocal(localSid, "uci", "add", map[string]any{
		"config": "network",
		"type":   "interface",
		"name":   req.Name,
		"values": map[string]string{
			"proto":   "static",
			"device":  "@lan",
			"ipaddr":  req.IPAddr,
			"netmask": req.Netmask,
			"auto":    auto,
		},
	})
	if err != nil {
		return fmt.Errorf("uci add interface failed: %v", err)
	}

	if err := ifaceCommitNetwork(localSid); err != nil {
		return err
	}

	if err := ifaceAddToLanFirewallZone(req.Name); err != nil {
		fmt.Printf("[Interfaces] warning: add firewall zone failed: %v\n", err)
	}

	ifaceAsyncIfup(req.Name)

	return nil
}

func ifaceEdit(localSid string, req InterfaceActionReq) (InterfaceActionResponse, error) {
	result := InterfaceActionResponse{OK: true}

	if req.Interface == "" {
		return result, fmt.Errorf("missing interface")
	}

	if req.IPAddr == "" || req.Netmask == "" {
		return result, fmt.Errorf("ipaddr and netmask are required")
	}

	if !ifaceIsValidNetmask(req.Netmask) {
		return result, fmt.Errorf("invalid netmask")
	}

	if req.Interface == "lan" {
		if req.IPAddr != ACManagementIP {
			return result, errIfaceLANIPFixed
		}
		if !ifaceNetmaskSupportsRequiredAddresses(req.Netmask) {
			return result, errIfaceLANNetmaskIncompatible
		}
		if err := ifaceValidateLanDHCPPool(req.Netmask, req.DHCP); err != nil {
			return result, err
		}
	}

	ifaceSections, err := ifaceGetNetworkInterfaceSections(localSid)
	if err != nil {
		return result, err
	}

	values, exists := ifaceSections[req.Interface]
	if !exists {
		return result, fmt.Errorf("interface not found")
	}

	proto := ifaceValueToString(values["proto"])
	device := ifaceValueToString(values["device"])
	if device == "" {
		device = ifaceValueToString(values["ifname"])
	}

	if !ifaceIsEditable(req.Interface, proto, device) {
		return result, fmt.Errorf("interface is read-only")
	}

	currentIP := strings.TrimSpace(ifaceValueToString(values["ipaddr"]))
	currentNetmask := strings.TrimSpace(ifaceValueToString(values["netmask"]))
	currentGateway := strings.TrimSpace(ifaceValueToString(values["gateway"]))
	currentDNS := ifaceValueToList(values["dns"])
	currentAuto := ifaceAutoValue(values["auto"])

	requestedGateway := strings.TrimSpace(req.Gateway)
	requestedDNS := ifaceSplitList(req.DNS)
	requestedAuto := currentAuto
	if req.Auto != nil {
		requestedAuto = *req.Auto
	}

	ipChanged := currentIP != req.IPAddr
	netmaskChanged := currentNetmask != req.Netmask
	gatewayChanged := currentGateway != requestedGateway
	dnsChanged := !ifaceStringListsEqual(currentDNS, requestedDNS)
	autoChanged := currentAuto != requestedAuto
	networkChanged := ipChanged || netmaskChanged || gatewayChanged || dnsChanged || autoChanged

	dhcpChanged := false
	if req.Interface == "lan" && req.DHCP != nil {
		dhcpSections := ifaceGetDHCPSectionsByName(localSid)
		currentDHCP := ifaceDHCPSettingsFromValues(dhcpSections["lan"])
		dhcpChanged = !ifaceDHCPRequestMatchesSettings(req.DHCP, currentDHCP)
	}

	result.Changed = networkChanged || dhcpChanged
	if !result.Changed {
		return result, nil
	}

	if networkChanged {
		auto := "0"
		if requestedAuto {
			auto = "1"
		}

		networkValues := map[string]any{
			"proto":   "static",
			"ipaddr":  req.IPAddr,
			"netmask": req.Netmask,
			"auto":    auto,
		}

		if requestedGateway != "" {
			networkValues["gateway"] = requestedGateway
		} else if gatewayChanged {
			ifaceDeleteNetworkOption(localSid, req.Interface, "gateway")
		}

		if len(requestedDNS) > 0 {
			networkValues["dns"] = requestedDNS
		} else if dnsChanged {
			ifaceDeleteNetworkOption(localSid, req.Interface, "dns")
		}

		_, err = ubusCallJSONLocal(localSid, "uci", "set", map[string]any{
			"config":  "network",
			"section": req.Interface,
			"values":  networkValues,
		})
		if err != nil {
			return result, fmt.Errorf("uci set failed: %v", err)
		}

		if req.Interface != "lan" {
			_, _ = ubusCallJSONLocal(localSid, "uci", "set", map[string]any{
				"config":  "network",
				"section": req.Interface,
				"values": map[string]string{
					"device": "@lan",
				},
			})
		}
	}

	if dhcpChanged {
		if err := ifaceUpdateLanDHCP(localSid, req.Netmask, req.DHCP); err != nil {
			return result, err
		}
	}

	if networkChanged {
		if err := ifaceCommitNetwork(localSid); err != nil {
			return result, err
		}
	}

	if dhcpChanged {
		if err := ifaceCommitDHCP(localSid); err != nil {
			return result, err
		}
	}

	if req.Interface == "lan" {
		if netmaskChanged {
			result.LANRestarting = true
			result.EstimatedSeconds = 15
			ifaceAsyncRestart(req.Interface)
		} else if gatewayChanged {
			ifaceAsyncApplyGateway(device, currentGateway, requestedGateway)
		}

		if dhcpChanged || dnsChanged || netmaskChanged {
			ifaceAsyncRestartDNSMasq()
		}
	} else if networkChanged {
		ifaceAsyncRestart(req.Interface)
	}

	return result, nil
}

func ifaceDHCPRequestMatchesSettings(req *InterfaceDHCPReq, current *InterfaceDHCPSettings) bool {
	if req == nil || current == nil {
		return req == nil && current == nil
	}

	return req.Enabled == current.Enabled &&
		strings.TrimSpace(req.Start) == strings.TrimSpace(current.Start) &&
		strings.TrimSpace(req.Limit) == strings.TrimSpace(current.Limit) &&
		strings.TrimSpace(req.LeaseTime) == strings.TrimSpace(current.LeaseTime) &&
		req.DynamicDHCP == current.DynamicDHCP &&
		req.Force == current.Force &&
		ifaceStringListsEqual(req.DHCPOptions, current.DHCPOptions)
}

func ifaceStringListsEqual(left []string, right []string) bool {
	leftNormalized := make([]string, 0, len(left))
	for _, value := range left {
		if value = strings.TrimSpace(value); value != "" {
			leftNormalized = append(leftNormalized, value)
		}
	}

	rightNormalized := make([]string, 0, len(right))
	for _, value := range right {
		if value = strings.TrimSpace(value); value != "" {
			rightNormalized = append(rightNormalized, value)
		}
	}

	if len(leftNormalized) != len(rightNormalized) {
		return false
	}
	for i := range leftNormalized {
		if leftNormalized[i] != rightNormalized[i] {
			return false
		}
	}
	return true
}

func ifaceAsyncApplyGateway(device string, oldGateway string, newGateway string) {
	device = strings.TrimSpace(device)
	oldGateway = strings.TrimSpace(oldGateway)
	newGateway = strings.TrimSpace(newGateway)
	if device == "" {
		device = "br-lan"
	}

	go func() {
		time.Sleep(1 * time.Second)

		if oldGateway != "" && oldGateway != newGateway {
			_ = exec.Command("/sbin/ip", "route", "del", "default", "via", oldGateway, "dev", device).Run()
		}
		if newGateway != "" {
			_ = exec.Command("/sbin/ip", "route", "replace", "default", "via", newGateway, "dev", device).Run()
		}
	}()
}

func ifaceRestart(localSid string, name string) error {
	if err := ifaceAssertActionAllowed(localSid, name, "restart"); err != nil {
		return err
	}

	ifaceAsyncRestart(name)

	return nil
}

func ifaceStop(localSid string, name string) error {
	if err := ifaceAssertActionAllowed(localSid, name, "stop"); err != nil {
		return err
	}

	ifaceAsyncIfdown(name)

	return nil
}

func ifaceDelete(localSid string, name string) error {
	if err := ifaceAssertActionAllowed(localSid, name, "delete"); err != nil {
		return err
	}

	ifaceAsyncIfdown(name)

	time.Sleep(300 * time.Millisecond)

	_, err := ubusCallJSONLocal(localSid, "uci", "delete", map[string]any{
		"config":  "network",
		"section": name,
	})
	if err != nil {
		return fmt.Errorf("uci delete failed: %v", err)
	}

	if err := ifaceCommitNetwork(localSid); err != nil {
		return err
	}

	if err := ifaceRemoveFromLanFirewallZone(name); err != nil {
		fmt.Printf("[Interfaces] warning: remove firewall zone failed: %v\n", err)
	}

	return nil
}

func ifaceAssertActionAllowed(localSid string, name string, action string) error {
	if name == "" {
		return fmt.Errorf("missing interface")
	}

	ifaceSections, err := ifaceGetNetworkInterfaceSections(localSid)
	if err != nil {
		return err
	}

	values, exists := ifaceSections[name]
	if !exists {
		return fmt.Errorf("interface not found")
	}

	proto := ifaceValueToString(values["proto"])
	device := ifaceValueToString(values["device"])
	if device == "" {
		device = ifaceValueToString(values["ifname"])
	}

	if !ifaceIsEditable(name, proto, device) {
		return fmt.Errorf("interface is read-only")
	}

	if name == "lan" && (action == "stop" || action == "delete") {
		return fmt.Errorf("lan cannot be stopped or deleted")
	}

	return nil
}

func ifaceCommitNetwork(localSid string) error {
	_, err := ubusCallJSONLocal(localSid, "uci", "commit", map[string]any{
		"config": "network",
	})
	if err != nil {
		return fmt.Errorf("uci commit network failed: %v", err)
	}

	return nil
}

// ---------- Apply helpers ----------

func ifaceAsyncRestart(name string) {
	go func() {
		time.Sleep(1 * time.Second)
		cmd := exec.Command("sh", "-c", fmt.Sprintf(
			"/sbin/ifdown '%s' >/dev/null 2>&1; sleep 1; /sbin/ifup '%s' >/dev/null 2>&1",
			ifaceShellQuoteSafe(name),
			ifaceShellQuoteSafe(name),
		))
		_ = cmd.Run()
	}()
}

func ifaceAsyncIfup(name string) {
	go func() {
		time.Sleep(1 * time.Second)
		cmd := exec.Command("sh", "-c", fmt.Sprintf(
			"/sbin/ifup '%s' >/dev/null 2>&1",
			ifaceShellQuoteSafe(name),
		))
		_ = cmd.Run()
	}()
}

func ifaceAsyncIfdown(name string) {
	go func() {
		time.Sleep(1 * time.Second)
		cmd := exec.Command("sh", "-c", fmt.Sprintf(
			"/sbin/ifdown '%s' >/dev/null 2>&1",
			ifaceShellQuoteSafe(name),
		))
		_ = cmd.Run()
	}()
}

func ifaceShellQuoteSafe(s string) string {
	return strings.ReplaceAll(s, `'`, `'\''`)
}

// ---------- Firewall zone helpers ----------

func ifaceAddToLanFirewallZone(name string) error {
	cmd := fmt.Sprintf(`
zone="$(uci show firewall | sed -n "s/^\(firewall\.@zone\[[0-9]\+\]\)\.name='lan'$/\1/p" | head -n1)"
[ -n "$zone" ] || exit 0
uci add_list "$zone.network=%s" 2>/dev/null || true
uci commit firewall
/etc/init.d/firewall reload >/dev/null 2>&1 || true
`, ifaceShellQuoteSafe(name))

	return exec.Command("sh", "-c", cmd).Run()
}

func ifaceRemoveFromLanFirewallZone(name string) error {
	cmd := fmt.Sprintf(`
zone="$(uci show firewall | sed -n "s/^\(firewall\.@zone\[[0-9]\+\]\)\.name='lan'$/\1/p" | head -n1)"
[ -n "$zone" ] || exit 0
uci del_list "$zone.network=%s" 2>/dev/null || true
uci commit firewall
/etc/init.d/firewall reload >/dev/null 2>&1 || true
`, ifaceShellQuoteSafe(name))

	return exec.Command("sh", "-c", cmd).Run()
}

// ---------- AP Management Read-only ----------

func ifaceGetAPManagementList() []APManagementInfo {
	registry := discoverManagedAPs(true)
	// 按物理口给稳定显示名(Antenna<N>),而不是暴露 AP 真实 hostname。
	// portByIP 在起 goroutine 前算好,循环里只读。
	modulesByPort := managedAntennaModulesByPort(registry)
	activePorts := currentManagedAntennaPortIndexes(registry)
	results := make([]APManagementInfo, len(activePorts))

	var wg sync.WaitGroup
	for index, portIndex := range activePorts {
		index := index
		portIndex := portIndex
		module, resolved := modulesByPort[portIndex]
		if !resolved {
			results[index] = APManagementInfo{
				Name:         apDisplayName("Antenna", portIndex),
				AntennaIndex: portIndex,
				Online:       false,
				Error:        "identification pending",
			}
			continue
		}

		wg.Add(1)

		go func() {
			defer wg.Done()

			results[index] = ifaceGetSingleAPManagement(module.IP, portIndex)
		}()
	}

	wg.Wait()

	ifaceSortAPManagement(results)

	return results
}

func ifaceGetSingleAPManagement(ip string, portIndex int) APManagementInfo {
	// portIndex>0 时 apDisplayName 直接返回 Antenna<N>(忽略 hostname 参数);
	// 解析不出口时才回退到括号里的名字(离线用 "AP "+ip,在线用真实 hostname)。
	info := APManagementInfo{
		Name:         apDisplayName("AP "+ip, portIndex),
		AntennaIndex: portIndex,
		IP:           ip,
		Online:       false,
	}

	if !ifacePingOnce(ip) {
		info.Error = "unreachable"
		return info
	}

	info.Online = true

	if hostname := ifaceRemoteHostname(ip); hostname != "" {
		info.Name = apDisplayName(hostname, portIndex)
	}

	values, err := ifaceRemoteUCISection(ip, "network", "lan")
	if err != nil {
		info.Error = err.Error()
		return info
	}

	info.IPAddr = ifaceValueToString(values["ipaddr"])
	info.Netmask = ifaceValueToString(values["netmask"])
	info.Gateway = ifaceValueToString(values["gateway"])
	info.DNS = ifaceValueToString(values["dns"])

	return info
}

func ifaceRemoteUCISection(ip string, config string, section string) (map[string]any, error) {
	res, err := ubusCallJSONAt(ip, AnonSID, "uci", "get", map[string]any{
		"config":  config,
		"section": section,
	})
	if err != nil {
		return nil, err
	}

	if values, ok := res["values"].(map[string]any); ok {
		return values, nil
	}

	return map[string]any{}, nil
}

func ifaceRemoteHostname(ip string) string {
	res, err := ubusCallJSONAt(ip, AnonSID, "uci", "get", map[string]any{
		"config":  "system",
		"section": "@system[0]",
	})
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hostname, ok := values["hostname"].(string); ok && hostname != "" {
				return hostname
			}
		}
	}

	board, err := ubusCallJSONAt(ip, AnonSID, "system", "board", nil)
	if err == nil {
		if hostname, ok := board["hostname"].(string); ok && hostname != "" {
			return hostname
		}
	}

	return ""
}

func ifacePingOnce(ip string) bool {
	cmd := exec.Command("ping", "-c", "1", "-W", "1", ip)
	return cmd.Run() == nil
}

func ifaceSortAPManagement(items []APManagementInfo) {
	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			left := items[i].AntennaIndex
			right := items[j].AntennaIndex
			if left <= 0 {
				left = 999
			}
			if right <= 0 {
				right = 999
			}
			if right < left {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func ifaceIsValidNetmask(mask string) bool {
	value, ok := ifaceIPv4ToUint32(mask)
	if !ok {
		return false
	}

	// Reject /0 and /32 for LAN-style editable interfaces.
	// /32 would make normal LAN DHCP/routing behavior invalid for this product UI.
	if value == 0 || value == 0xFFFFFFFF {
		return false
	}

	seenZero := false
	for i := 31; i >= 0; i-- {
		bit := (value >> uint(i)) & 1
		if bit == 0 {
			seenZero = true
		} else if seenZero {
			return false
		}
	}

	return true
}

// ifaceNetmaskSupportsRequiredAddresses ensures that changing the user-facing
// LAN netmask cannot separate or reserve any address needed by the integrated
// device. Keep the public error generic: implementation topology is internal.
func ifaceNetmaskSupportsRequiredAddresses(mask string) bool {
	maskValue, ok := ifaceIPv4ToUint32(mask)
	if !ok || !ifaceIsValidNetmask(mask) {
		return false
	}

	acValue, ok := ifaceIPv4ToUint32(ACManagementIP)
	if !ok {
		return false
	}

	network := acValue & maskValue
	broadcast := network | ^maskValue
	requiredAddresses := append([]string{ACManagementIP}, APManagementIPs()...)

	for _, address := range requiredAddresses {
		value, valid := ifaceIPv4ToUint32(address)
		if !valid || value&maskValue != network || value == network || value == broadcast {
			return false
		}
	}

	return true
}

func ifaceValidateLanDHCPPool(netmask string, req *InterfaceDHCPReq) error {
	if req == nil {
		return nil
	}

	start := strings.TrimSpace(req.Start)
	limit := strings.TrimSpace(req.Limit)
	leasetime := strings.TrimSpace(req.LeaseTime)
	if start == "" || limit == "" || leasetime == "" {
		return fmt.Errorf("dhcp start, limit and lease time are required")
	}

	startNum, err := strconv.ParseUint(start, 10, 32)
	if err != nil || startNum > 65535 {
		return fmt.Errorf("invalid dhcp start")
	}
	if startNum < 100 {
		return errIfaceDHCPStartTooLow
	}

	limitNum, err := strconv.ParseUint(limit, 10, 32)
	if err != nil || limitNum < 1 || limitNum > 65535 {
		return fmt.Errorf("invalid dhcp limit")
	}

	maskValue, ok := ifaceIPv4ToUint32(netmask)
	if !ok || !ifaceIsValidNetmask(netmask) {
		return fmt.Errorf("invalid netmask")
	}

	hostMask := uint64(^maskValue)
	endOffset := startNum + limitNum - 1
	// hostMask is the broadcast offset; the last usable host is one below it.
	if hostMask < 2 || endOffset >= hostMask {
		return errIfaceDHCPPoolIncompatible
	}

	acValue, ok := ifaceIPv4ToUint32(ACManagementIP)
	if !ok {
		return errIfaceDHCPPoolIncompatible
	}
	network := uint64(acValue & maskValue)
	requiredAddresses := append([]string{ACManagementIP}, APManagementIPs()...)
	for _, address := range requiredAddresses {
		value, valid := ifaceIPv4ToUint32(address)
		if !valid || uint64(value) < network {
			return errIfaceDHCPPoolIncompatible
		}
		offset := uint64(value) - network
		if startNum <= offset && offset <= endOffset {
			return errIfaceDHCPPoolIncompatible
		}
	}

	return nil
}

func ifaceIPv4ToUint32(ip string) (uint32, bool) {
	parts := strings.Split(strings.TrimSpace(ip), ".")
	if len(parts) != 4 {
		return 0, false
	}

	var value uint32
	for _, part := range parts {
		if part == "" {
			return 0, false
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 255 {
			return 0, false
		}
		value = (value << 8) | uint32(n)
	}

	return value, true
}

// ---------- Value Helpers ----------

func ifaceSplitList(raw string) []string {
	parts := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})

	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func ifaceValueToList(v any) []string {
	switch t := v.(type) {
	case string:
		return ifaceSplitList(t)
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	default:
		return []string{}
	}
}

func ifaceValueToString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	case float64:
		return strconv.Itoa(int(t))
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return ""
	}
}

// ---------- AC Info Helpers ----------

func ifaceLocalHostname(localSid string) string {
	res, err := ubusCallJSONLocal(localSid, "uci", "get", map[string]any{
		"config":  "system",
		"section": "@system[0]",
	})
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hostname, ok := values["hostname"].(string); ok && hostname != "" {
				return hostname
			}
		}
	}

	board, err := ubusCallJSONLocal(localSid, "system", "board", nil)
	if err == nil {
		if hostname, ok := board["hostname"].(string); ok && hostname != "" {
			return hostname
		}
	}

	return ""
}

func ifaceLocalLanIP(localSid string) string {
	st, err := ubusCallJSONLocal(localSid, "network.interface.lan", "status", nil)
	if err != nil {
		return ""
	}

	if arr, ok := st["ipv4-address"].([]any); ok && len(arr) > 0 {
		if m, ok := arr[0].(map[string]any); ok {
			if addr, ok := m["address"].(string); ok {
				return addr
			}
		}
	}

	return ""
}
