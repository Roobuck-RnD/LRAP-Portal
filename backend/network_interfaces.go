package main

import (
	"encoding/json"
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
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Online  bool   `json:"online"`
	IPAddr  string `json:"ipaddr"`
	Netmask string `json:"netmask"`
	Gateway string `json:"gateway"`
	DNS     string `json:"dns"`
	Error   string `json:"error,omitempty"`
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

const ifaceZeroSID = "00000000000000000000000000000000"

var ifaceNameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

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
	dhcpSections := ifaceGetDHCPInterfaceSections(localSid)

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
			stat.DHCP = ifaceDHCPSettingsFromValues(dhcpSections[id])
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

func ifaceGetDHCPInterfaceSections(localSid string) map[string]map[string]any {
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

		iface := ifaceValueToString(m["interface"])
		if iface == "" {
			iface = sectionName
		}
		out[iface] = m
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

	return &InterfaceDHCPSettings{
		Enabled:     !ifaceBoolUCI(values["ignore"], false),
		Start:       ifaceStringDefault(values["start"], "100"),
		Limit:       ifaceStringDefault(values["limit"], "150"),
		LeaseTime:   ifaceStringDefault(values["leasetime"], "12h"),
		DynamicDHCP: ifaceBoolUCI(values["dynamicdhcp"], true),
		Force:       ifaceBoolUCI(values["force"], false),
		DHCPOptions: ifaceValueToList(values["dhcp_option"]),
	}
}

func ifaceEnsureLanDHCPSection(localSid string) error {
	sections := ifaceGetDHCPInterfaceSections(localSid)
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

func ifaceUpdateLanDHCP(localSid string, req *InterfaceDHCPReq) error {
	if req == nil {
		return nil
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

	startNum, err := strconv.Atoi(start)
	if err != nil || startNum < 1 || startNum > 65535 {
		return fmt.Errorf("invalid dhcp start")
	}

	limitNum, err := strconv.Atoi(limit)
	if err != nil || limitNum < 1 || limitNum > 65535 {
		return fmt.Errorf("invalid dhcp limit")
	}

	ignore := "0"
	if !req.Enabled {
		ignore = "1"
	}

	_, err = ubusCallJSONLocal(localSid, "uci", "set", map[string]any{
		"config":  "dhcp",
		"section": "lan",
		"values": map[string]string{
			"interface":   "lan",
			"ignore":      ignore,
			"start":       start,
			"limit":       limit,
			"leasetime":   leasetime,
			"dynamicdhcp": ifaceBoolToUCI(req.DynamicDHCP),
			"force":       ifaceBoolToUCI(req.Force),
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

	switch req.Action {
	case "create":
		err = ifaceCreateAlias(localSid, req)

	case "edit":
		err = ifaceEdit(localSid, req)

	case "restart":
		err = ifaceRestart(localSid, req.Interface)

	case "stop":
		err = ifaceStop(localSid, req.Interface)

	case "delete":
		err = ifaceDelete(localSid, req.Interface)

	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}

	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"ok":true}`))
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

func ifaceEdit(localSid string, req InterfaceActionReq) error {
	if req.Interface == "" {
		return fmt.Errorf("missing interface")
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

	values, exists := ifaceSections[req.Interface]
	if !exists {
		return fmt.Errorf("interface not found")
	}

	proto := ifaceValueToString(values["proto"])
	device := ifaceValueToString(values["device"])
	if device == "" {
		device = ifaceValueToString(values["ifname"])
	}

	if !ifaceIsEditable(req.Interface, proto, device) {
		return fmt.Errorf("interface is read-only")
	}

	auto := "1"
	if req.Auto != nil && !*req.Auto {
		auto = "0"
	}

	networkValues := map[string]any{
		"proto":   "static",
		"ipaddr":  req.IPAddr,
		"netmask": req.Netmask,
		"auto":    auto,
	}

	if strings.TrimSpace(req.Gateway) != "" {
		networkValues["gateway"] = strings.TrimSpace(req.Gateway)
	} else {
		ifaceDeleteNetworkOption(localSid, req.Interface, "gateway")
	}

	dnsList := ifaceSplitList(req.DNS)
	if len(dnsList) > 0 {
		networkValues["dns"] = dnsList
	} else {
		ifaceDeleteNetworkOption(localSid, req.Interface, "dns")
	}

	_, err = ubusCallJSONLocal(localSid, "uci", "set", map[string]any{
		"config":  "network",
		"section": req.Interface,
		"values":  networkValues,
	})
	if err != nil {
		return fmt.Errorf("uci set failed: %v", err)
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

	dhcpChanged := false
	if req.Interface == "lan" && req.DHCP != nil {
		if err := ifaceUpdateLanDHCP(localSid, req.DHCP); err != nil {
			return err
		}
		dhcpChanged = true
	}

	if err := ifaceCommitNetwork(localSid); err != nil {
		return err
	}

	if dhcpChanged {
		if err := ifaceCommitDHCP(localSid); err != nil {
			return err
		}
		ifaceAsyncRestartDNSMasq()
	}

	ifaceAsyncRestart(req.Interface)

	return nil
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

func ifaceManagedAPIPs() []string {
	return []string{
		"10.10.18.2",
		"10.10.18.3",
		"10.10.18.4",
		"10.10.18.5",
	}
}

func ifaceGetAPManagementList() []APManagementInfo {
	ips := ifaceManagedAPIPs()
	results := make([]APManagementInfo, 0, len(ips))

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range ips {
		ip := ip

		wg.Add(1)

		go func() {
			defer wg.Done()

			info := ifaceGetSingleAPManagement(ip)

			mu.Lock()
			results = append(results, info)
			mu.Unlock()
		}()
	}

	wg.Wait()

	ifaceSortAPManagement(results)

	return results
}

func ifaceGetSingleAPManagement(ip string) APManagementInfo {
	info := APManagementInfo{
		Name:   "AP " + ip,
		IP:     ip,
		Online: false,
	}

	if !ifacePingOnce(ip) {
		info.Error = "unreachable"
		return info
	}

	info.Online = true

	if hostname := ifaceRemoteHostname(ip); hostname != "" {
		info.Name = hostname
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
	res, err := ubusCallJSONAt(ip, ifaceZeroSID, "uci", "get", map[string]any{
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
	res, err := ubusCallJSONAt(ip, ifaceZeroSID, "uci", "get", map[string]any{
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

	board, err := ubusCallJSONAt(ip, ifaceZeroSID, "system", "board", nil)
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
			if ifaceIPSortKey(items[j].IP) < ifaceIPSortKey(items[i].IP) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}

func ifaceIPSortKey(ip string) int {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return 999
	}

	last, err := strconv.Atoi(parts[3])
	if err != nil {
		return 999
	}

	return last
}


func ifaceIsValidNetmask(mask string) bool {
	parts := strings.Split(strings.TrimSpace(mask), ".")
	if len(parts) != 4 {
		return false
	}

	var value uint32
	for _, part := range parts {
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 255 {
			return false
		}
		value = (value << 8) | uint32(n)
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
