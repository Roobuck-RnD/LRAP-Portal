package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- Constants ----------

const (
	ccMtk24GPath    = "/etc/wireless/mediatek/mt7981.dbdc.b0.dat"
	ccMtk5GPath     = "/etc/wireless/mediatek/mt7981.dbdc.b1.dat"
	ccStaHelperPath = "/usr/bin/mtk_sta_info"

	ccWirelessIF24G = "ra0"
	ccWirelessIF5G  = "rax0"
)

// ---------- Response Types ----------

type ConnectedClient struct {
	Hostname      string `json:"hostname,omitempty"`
	IP            string `json:"ip,omitempty"`
	MAC           string `json:"mac"`
	SSID          string `json:"ssid,omitempty"`
	Band          string `json:"band,omitempty"`
	Interface     string `json:"interface,omitempty"`
	RSSI          int    `json:"rssi,omitempty"`
	Signal        string `json:"signal,omitempty"`
	ConnectedTime string `json:"connected_time,omitempty"`

	// 内部使用，不返回给前端。
	ConnectedSeconds int `json:"-"`
}

type ConnectedClientModule struct {
	Name     string            `json:"name"`
	Type     string            `json:"type"` // main / ap
	IP       string            `json:"ip,omitempty"`
	MAC      string            `json:"mac,omitempty"` // 兼容旧前端，等于 br-lan MAC
	BrLanMAC string            `json:"br_lan_mac,omitempty"`
	Ra0MAC   string            `json:"ra0_mac,omitempty"`
	Rax0MAC  string            `json:"rax0_mac,omitempty"`
	Online   bool              `json:"online"`
	Clients  []ConnectedClient `json:"clients"`
}

type ConnectedClientsResponse struct {
	Modules []ConnectedClientModule `json:"modules"`
}

// ---------- Internal Types ----------

type ccDhcpLease struct {
	Hostname string
	IP       string
	MAC      string
}

type ccArpEntry struct {
	IP     string
	MAC    string
	Device string
}

type ccFDBEntry struct {
	MAC  string
	Port string
}

type ccWirelessStation struct {
	MAC              string
	Interface        string
	SSID             string
	Band             string
	RSSI             int
	Signal           string
	ConnectedSeconds int
	ConnectedTime    string
}

type ccMtkStaInfo struct {
	MAC              string `json:"mac"`
	Interface        string `json:"interface"`
	RSSI             int    `json:"rssi"`
	Signal           string `json:"signal"`
	ConnectedSeconds int    `json:"connected_seconds"`
	ConnectedTime    string `json:"connected_time"`

	// helper 可能仍然输出这些字段，主后端忽略即可。
	TXRateRaw string `json:"tx_rate_raw"`
	TXRate    string `json:"tx_rate"`
	RXRateRaw string `json:"rx_rate_raw"`
	RXRate    string `json:"rx_rate"`
}

type ccMtkStaError struct {
	Error string `json:"error"`
}

// ---------- Candidate AP IPs ----------

func ccManagedAPIPs() []string {
	return []string{
		"10.10.18.2",
		"10.10.18.3",
		"10.10.18.4",
		"10.10.18.5",
	}
}

// ---------- HTTP Handler ----------

func connectedClientsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if r.Header.Get("Authorization") == "" {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	leases := ccParseLocalDHCPLeases()
	arpByMAC := ccParseLocalARPByMAC()

	// Band/source is decided from each module's own bridge FDB.
	// AC FDB cannot tell whether a client behind an AP is on AP ra0 or rax0,
	// so every AP is queried for its own FDB and station helper output.
	mainModule := ccBuildMainModule(leases, arpByMAC)
	apModules := ccBuildAPModules(leases, arpByMAC)

	modules := make([]ConnectedClientModule, 0, 1+len(apModules))
	modules = append(modules, mainModule)
	modules = append(modules, apModules...)

	// A client MAC can briefly appear on more than one AP while roaming or when
	// an AP still has stale station/FDB data. Only keep one confirmed row per MAC
	// in the final online list. The best row is chosen by valid signal first, then
	// stronger RSSI.
	ccDedupeClientsAcrossModules(modules)

	_ = json.NewEncoder(w).Encode(ConnectedClientsResponse{
		Modules: modules,
	})
}

// ---------- Main Module ----------

func ccBuildMainModule(leases map[string]ccDhcpLease, arpByMAC map[string]ccArpEntry) ConnectedClientModule {
	sid := ccGetLocalSID()

	name := ccLocalHostname(sid)
	if name == "" {
		name = "Main Module"
	}

	ip := ccLocalLanIP(sid)
	if ip == "" {
		ip = ccFallbackLocalLanIP()
	}

	brLanMAC := ccLocalDeviceMAC(sid, "br-lan")
	ra0MAC := ccLocalDeviceMAC(sid, ccWirelessIF24G)
	rax0MAC := ccLocalDeviceMAC(sid, ccWirelessIF5G)
	ssid24 := ccGetLocalSSID24()
	ssid5 := ccGetLocalSSID5()

	stations, err := ccGetLocalWirelessStations(ssid24, ssid5)
	if err != nil {
		log.Printf("connectedClients: local helper failed: %v", err)
	}

	moduleFDB := ccParseBridgeFDB()

	mod := ConnectedClientModule{
		Name:     name,
		Type:     "main",
		IP:       ip,
		MAC:      brLanMAC,
		BrLanMAC: brLanMAC,
		Ra0MAC:   ra0MAC,
		Rax0MAC:  rax0MAC,
		Online:   true,
		Clients:  nil,
	}

	mod.Clients = ccBuildClientsFromModuleFDBAndStations(&mod, moduleFDB, stations, leases, arpByMAC, ssid24, ssid5)
	return mod
}

// ---------- AP Modules ----------

func ccBuildAPModules(leases map[string]ccDhcpLease, arpByMAC map[string]ccArpEntry) []ConnectedClientModule {
	ips := ccManagedAPIPs()
	out := make([]ConnectedClientModule, 0, len(ips))

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range ips {
		ip := ip

		wg.Add(1)

		go func() {
			defer wg.Done()

			if !ccPingOnce(ip) {
				return
			}

			mod := ccBuildSingleAPModule(ip, leases, arpByMAC)

			mu.Lock()
			out = append(out, mod)
			mu.Unlock()
		}()
	}

	wg.Wait()

	sort.Slice(out, func(i, j int) bool {
		return ccIPSortKey(out[i].IP) < ccIPSortKey(out[j].IP)
	})

	return out
}

func ccBuildSingleAPModule(ip string, leases map[string]ccDhcpLease, arpByMAC map[string]ccArpEntry) ConnectedClientModule {
	name := ccRemoteHostname(ip)
	if name == "" {
		name = "RoobuckAP"
	}

	brLanMAC := ccRemoteDeviceMAC(ip, "br-lan")
	ra0MAC := ccRemoteDeviceMAC(ip, ccWirelessIF24G)
	rax0MAC := ccRemoteDeviceMAC(ip, ccWirelessIF5G)
	ssid24 := ccGetRemoteSSID24(ip)
	ssid5 := ccGetRemoteSSID5(ip)

	stations, err := ccGetRemoteWirelessStations(ip, ssid24, ssid5)
	if err != nil {
		log.Printf("connectedClients: remote helper failed ip=%s err=%v", ip, err)
	}

	moduleFDB := ccGetRemoteBridgeFDB(ip)

	mod := ConnectedClientModule{
		Name:     name,
		Type:     "ap",
		IP:       ip,
		MAC:      brLanMAC,
		BrLanMAC: brLanMAC,
		Ra0MAC:   ra0MAC,
		Rax0MAC:  rax0MAC,
		Online:   true,
		Clients:  nil,
	}

	mod.Clients = ccBuildClientsFromModuleFDBAndStations(&mod, moduleFDB, stations, leases, arpByMAC, ssid24, ssid5)
	return mod
}

// ---------- Station Helper ----------

func ccGetLocalWirelessStations(ssid24 string, ssid5 string) ([]ccWirelessStation, error) {
	stations := make([]ccWirelessStation, 0)

	raw24, err24 := ccRunLocalStaHelper(ccWirelessIF24G)
	if err24 == nil {
		stations = append(stations, ccParseStaHelperJSON(raw24, ssid24, ccWirelessIF24G, "2.4G")...)
	} else {
		log.Printf("connectedClients: local 2.4G helper failed: %v", err24)
	}

	raw5, err5 := ccRunLocalStaHelper(ccWirelessIF5G)
	if err5 == nil {
		stations = append(stations, ccParseStaHelperJSON(raw5, ssid5, ccWirelessIF5G, "5G")...)
	} else {
		log.Printf("connectedClients: local 5G helper failed: %v", err5)
	}

	if err24 != nil && err5 != nil {
		return nil, fmt.Errorf("both local station helpers failed: 2g=%v 5g=%v", err24, err5)
	}

	return stations, nil
}

func ccGetRemoteWirelessStations(ip string, ssid24 string, ssid5 string) ([]ccWirelessStation, error) {
	stations := make([]ccWirelessStation, 0)

	raw24, err24 := ccRunRemoteStaHelper(ip, ccWirelessIF24G)
	if err24 == nil {
		stations = append(stations, ccParseStaHelperJSON(raw24, ssid24, ccWirelessIF24G, "2.4G")...)
	} else {
		log.Printf("connectedClients: remote 2.4G helper failed ip=%s err=%v", ip, err24)
	}

	raw5, err5 := ccRunRemoteStaHelper(ip, ccWirelessIF5G)
	if err5 == nil {
		stations = append(stations, ccParseStaHelperJSON(raw5, ssid5, ccWirelessIF5G, "5G")...)
	} else {
		log.Printf("connectedClients: remote 5G helper failed ip=%s err=%v", ip, err5)
	}

	if err24 != nil && err5 != nil {
		return nil, fmt.Errorf("both remote station helpers failed for %s: 2g=%v 5g=%v", ip, err24, err5)
	}

	return stations, nil
}

func ccRunLocalStaHelper(iface string) (string, error) {
	cmd := exec.Command(ccStaHelperPath, iface)

	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("exec %s %s failed: %v output=%s", ccStaHelperPath, iface, err, string(out))
	}

	return string(out), nil
}

func ccRunRemoteStaHelper(ip string, iface string) (string, error) {
	const zeroSID = "00000000000000000000000000000000"

	res, err := ubusCallJSONAt(ip, zeroSID, "file", "exec", map[string]any{
		"command": ccStaHelperPath,
		"params":  []string{iface},
	})
	if err != nil {
		return "", err
	}

	if stdout, ok := res["stdout"].(string); ok {
		return stdout, nil
	}

	if data, ok := res["data"].(string); ok {
		return data, nil
	}

	return "", fmt.Errorf("remote helper returned no stdout")
}

func ccParseStaHelperJSON(raw string, ssid string, iface string, band string) []ccWirelessStation {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	var errPayload ccMtkStaError
	if err := json.Unmarshal([]byte(raw), &errPayload); err == nil && errPayload.Error != "" {
		log.Printf("connectedClients: helper json error iface=%s band=%s: %s", iface, band, errPayload.Error)
		return nil
	}

	var arr []ccMtkStaInfo
	if err := json.Unmarshal([]byte(raw), &arr); err != nil {
		log.Printf("connectedClients: parse helper json failed iface=%s band=%s: %v raw=%s", iface, band, err, raw)
		return nil
	}

	out := make([]ccWirelessStation, 0, len(arr))

	for _, item := range arr {
		mac := strings.ToUpper(strings.TrimSpace(item.MAC))
		if mac == "" {
			continue
		}

		actualIface := strings.TrimSpace(item.Interface)
		if actualIface == "" {
			actualIface = iface
		}

		signal := item.Signal
		if signal == "" {
			signal = ccSignalText(item.RSSI)
		}

		connectedTime := item.ConnectedTime
		if connectedTime == "" && item.ConnectedSeconds > 0 {
			connectedTime = ccFormatDuration(item.ConnectedSeconds)
		}

		out = append(out, ccWirelessStation{
			MAC:              mac,
			Interface:        actualIface,
			SSID:             ssid,
			Band:             band,
			RSSI:             item.RSSI,
			Signal:           signal,
			ConnectedSeconds: item.ConnectedSeconds,
			ConnectedTime:    connectedTime,
		})
	}

	return out
}

// ---------- Module-local FDB + station merge ----------
//
// Important: On this MTK firmware, the helper output can sometimes contain the
// same MAC when called on both ra0 and rax0. Therefore the source of truth for
// band/interface is the module's own bridge FDB port:
//   ra0  -> 2.4G
//   rax0 -> 5G
// The helper only supplies RSSI and connected time. DHCP/ARP only supplies name/IP.

func ccBuildClientsFromModuleFDBAndStations(
	mod *ConnectedClientModule,
	moduleFDB []ccFDBEntry,
	stations []ccWirelessStation,
	leases map[string]ccDhcpLease,
	arpByMAC map[string]ccArpEntry,
	ssid24 string,
	ssid5 string,
) []ConnectedClient {
	if mod == nil {
		return nil
	}

	wirelessPortByMAC := ccWirelessFDBPortByMAC(moduleFDB)
	stationCandidates := ccStationCandidatesByMAC(stations)
	excludedMACs := ccModuleOwnMACSet(mod)

	macSet := make(map[string]bool)
	for mac := range wirelessPortByMAC {
		macSet[mac] = true
	}
	for mac := range stationCandidates {
		macSet[mac] = true
	}

	clients := make([]ConnectedClient, 0, len(macSet))

	for mac := range macSet {
		mac = strings.ToUpper(strings.TrimSpace(mac))
		if !ccIsUnicastMAC(mac) {
			continue
		}
		if excludedMACs[mac] {
			continue
		}

		port := wirelessPortByMAC[mac]
		candidates := stationCandidates[mac]
		station, hasStation := ccPickStationCandidate(candidates, port)

		// Only the module-local wireless FDB proves the client is currently attached
		// to this module. Helper-only station entries can be stale after roaming, so
		// do not show them as confirmed online clients.
		if port == "" {
			continue
		}

		client := ConnectedClient{
			MAC:      mac,
			Hostname: "(unknown)",
			Signal:   "Unknown",
		}

		if hasStation {
			client.SSID = station.SSID
			client.Band = station.Band
			client.RSSI = station.RSSI
			client.Signal = station.Signal
			client.ConnectedTime = station.ConnectedTime
			client.ConnectedSeconds = station.ConnectedSeconds
			client.Interface = station.Interface
		}

		// Override band/SSID with the module-local bridge FDB port when available.
		// This fixes false 5G display when mtk_sta_info returns the same MAC from rax0.
		switch port {
		case ccWirelessIF24G:
			client.Band = "2.4G"
			client.SSID = ssid24
			client.Interface = ccWirelessIF24G
		case ccWirelessIF5G:
			client.Band = "5G"
			client.SSID = ssid5
			client.Interface = ccWirelessIF5G
		}

		if lease, ok := leases[mac]; ok {
			if strings.TrimSpace(lease.Hostname) != "" {
				client.Hostname = lease.Hostname
			}
			if strings.TrimSpace(lease.IP) != "" {
				client.IP = lease.IP
			}
		}

		if arp, ok := arpByMAC[mac]; ok && strings.TrimSpace(arp.IP) != "" {
			client.IP = arp.IP
		}

		if client.Band == "" {
			client.Band = "Unknown"
		}
		if client.SSID == "" {
			client.SSID = ""
		}
		if client.Signal == "" {
			client.Signal = ccSignalText(client.RSSI)
		}

		// Only show confirmed online wireless clients. Rows with Unknown signal are
		// usually stale FDB/DHCP leftovers or helper misses, so do not include them
		// in the confirmed connected-client list.
		if !ccClientHasKnownSignal(client) {
			continue
		}

		clients = append(clients, client)
	}

	ccSortClients(clients)
	return clients
}

func ccWirelessFDBPortByMAC(entries []ccFDBEntry) map[string]string {
	out := make(map[string]string)

	for _, entry := range entries {
		mac := strings.ToUpper(strings.TrimSpace(entry.MAC))
		port := strings.TrimSpace(entry.Port)

		if mac == "" || port == "" {
			continue
		}

		if port != ccWirelessIF24G && port != ccWirelessIF5G {
			continue
		}

		if !ccIsUnicastMAC(mac) {
			continue
		}

		// Prefer an existing wireless port if present; usually there is only one.
		if _, exists := out[mac]; !exists {
			out[mac] = port
		}
	}

	return out
}

func ccStationCandidatesByMAC(stations []ccWirelessStation) map[string][]ccWirelessStation {
	out := make(map[string][]ccWirelessStation)

	for _, station := range stations {
		mac := strings.ToUpper(strings.TrimSpace(station.MAC))
		if mac == "" || !ccIsUnicastMAC(mac) {
			continue
		}

		out[mac] = append(out[mac], station)
	}

	return out
}

func ccClientHasKnownSignal(client ConnectedClient) bool {
	signal := strings.ToLower(strings.TrimSpace(client.Signal))

	if signal == "" || signal == "unknown" {
		return false
	}

	// RSSI=0 is what the helper uses when it did not return a real station RSSI.
	if client.RSSI == 0 {
		return false
	}

	return true
}

func ccDedupeClientsAcrossModules(modules []ConnectedClientModule) {
	type bestClientLocation struct {
		ModuleIndex int
		Client      ConnectedClient
	}

	bestByMAC := make(map[string]bestClientLocation)

	for moduleIndex := range modules {
		for _, client := range modules[moduleIndex].Clients {
			mac := strings.ToUpper(strings.TrimSpace(client.MAC))
			if mac == "" || !ccIsUnicastMAC(mac) {
				continue
			}

			current := bestClientLocation{
				ModuleIndex: moduleIndex,
				Client:      client,
			}

			previous, exists := bestByMAC[mac]
			if !exists || ccClientIsBetter(current.Client, previous.Client) {
				bestByMAC[mac] = current
			}
		}
	}

	grouped := make([][]ConnectedClient, len(modules))
	for _, best := range bestByMAC {
		grouped[best.ModuleIndex] = append(grouped[best.ModuleIndex], best.Client)
	}

	for moduleIndex := range modules {
		modules[moduleIndex].Clients = grouped[moduleIndex]
		ccSortModuleClients(&modules[moduleIndex])
	}
}

func ccClientIsBetter(a ConnectedClient, b ConnectedClient) bool {
	aKnown := ccClientHasKnownSignal(a)
	bKnown := ccClientHasKnownSignal(b)

	if aKnown != bKnown {
		return aKnown
	}

	// RSSI values are negative; a larger number is stronger, e.g. -39 is better than -72.
	if a.RSSI != b.RSSI {
		return a.RSSI > b.RSSI
	}

	// Prefer a row with station time, then richer DHCP/ARP metadata.
	if (a.ConnectedSeconds > 0) != (b.ConnectedSeconds > 0) {
		return a.ConnectedSeconds > 0
	}
	if (strings.TrimSpace(a.IP) != "") != (strings.TrimSpace(b.IP) != "") {
		return strings.TrimSpace(a.IP) != ""
	}
	if ccHostnameKnown(a.Hostname) != ccHostnameKnown(b.Hostname) {
		return ccHostnameKnown(a.Hostname)
	}

	return false
}

func ccHostnameKnown(hostname string) bool {
	hostname = strings.TrimSpace(hostname)
	return hostname != "" && hostname != "(unknown)" && hostname != "*" && hostname != "?"
}

func ccPickStationCandidate(candidates []ccWirelessStation, desiredPort string) (ccWirelessStation, bool) {
	if len(candidates) == 0 {
		return ccWirelessStation{}, false
	}

	desiredPort = strings.TrimSpace(desiredPort)
	if desiredPort != "" {
		for _, candidate := range candidates {
			if strings.EqualFold(candidate.Interface, desiredPort) {
				return candidate, true
			}
		}
	}

	// Prefer a candidate with actual RSSI/time over an empty one.
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if best.RSSI == 0 && candidate.RSSI != 0 {
			best = candidate
			continue
		}
		if best.ConnectedSeconds == 0 && candidate.ConnectedSeconds > 0 {
			best = candidate
		}
	}

	return best, true
}

func ccModuleOwnMACSet(mod *ConnectedClientModule) map[string]bool {
	out := make(map[string]bool)
	if mod == nil {
		return out
	}

	for _, mac := range []string{mod.MAC, mod.BrLanMAC, mod.Ra0MAC, mod.Rax0MAC} {
		mac = strings.ToUpper(strings.TrimSpace(mac))
		if mac != "" {
			out[mac] = true
		}
	}

	return out
}

func ccGetRemoteBridgeFDB(ip string) []ccFDBEntry {
	const zeroSID = "00000000000000000000000000000000"

	// Do not call "bridge" directly here. On many OpenWrt images ubus file.exec
	// does not get the same PATH as an interactive shell, so "bridge" can be
	// missing even though /usr/sbin/bridge exists. If this fails, the UI falls
	// back to helper data and duplicate MACs can be shown on the wrong band.
	cmd := `
PATH=/usr/sbin:/usr/bin:/sbin:/bin
if command -v bridge >/dev/null 2>&1; then
	bridge fdb show br br-lan
elif [ -x /usr/sbin/bridge ]; then
	/usr/sbin/bridge fdb show br br-lan
elif [ -x /sbin/bridge ]; then
	/sbin/bridge fdb show br br-lan
else
	exit 127
fi
`

	res, err := ubusCallJSONAt(ip, zeroSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", cmd},
	})
	if err != nil {
		log.Printf("connectedClients: remote bridge fdb failed ip=%s err=%v", ip, err)
		return nil
	}

	out := ""
	if stdout, ok := res["stdout"].(string); ok {
		out = stdout
	} else if data, ok := res["data"].(string); ok {
		out = data
	}

	entries := ccParseBridgeFDBText(out)
	if len(entries) == 0 {
		log.Printf("connectedClients: remote bridge fdb empty ip=%s raw=%q", ip, strings.TrimSpace(out))
	}

	return entries
}

func ccParseBridgeFDBText(raw string) []ccFDBEntry {
	lines := strings.Split(string(raw), "\n")
	reLine := regexp.MustCompile(`(?i)^([0-9a-f:]{17})\s+dev\s+([^\s]+)\b`)

	out := make([]ccFDBEntry, 0)
	seen := make(map[string]bool)

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		lower := strings.ToLower(line)

		if strings.Contains(lower, " self ") ||
			strings.Contains(lower, " permanent") ||
			strings.Contains(lower, " vlan ") {
			continue
		}

		m := reLine.FindStringSubmatch(line)
		if len(m) != 3 {
			continue
		}

		mac := strings.ToUpper(m[1])
		port := m[2]

		if port == "br-lan" {
			continue
		}

		key := mac + "|" + port
		if seen[key] {
			continue
		}

		seen[key] = true
		out = append(out, ccFDBEntry{MAC: mac, Port: port})
	}

	return out
}

// ---------- Convert Stations to Clients ----------

func ccStationsToClients(stations []ccWirelessStation, leases map[string]ccDhcpLease) []ConnectedClient {
	clients := make([]ConnectedClient, 0, len(stations))

	for _, st := range stations {
		mac := strings.ToUpper(st.MAC)

		hostname := "(unknown)"
		ip := ""

		if lease, ok := leases[mac]; ok {
			if lease.Hostname != "" {
				hostname = lease.Hostname
			}
			ip = lease.IP
		}

		signal := st.Signal
		if signal == "" {
			signal = ccSignalText(st.RSSI)
		}

		clients = append(clients, ConnectedClient{
			Hostname:         hostname,
			IP:               ip,
			MAC:              mac,
			SSID:             st.SSID,
			Band:             st.Band,
			Interface:        st.Interface,
			RSSI:             st.RSSI,
			Signal:           signal,
			ConnectedTime:    st.ConnectedTime,
			ConnectedSeconds: st.ConnectedSeconds,
		})
	}

	ccSortClients(clients)

	return clients
}

// ---------- FDB-driven Rebuild ----------

func ccRebuildClientsFromFDB(
	mainModule *ConnectedClientModule,
	apModules []ConnectedClientModule,
	leases map[string]ccDhcpLease,
	arpByMAC map[string]ccArpEntry,
	fdbEntries []ccFDBEntry,
) {
	if mainModule == nil {
		return
	}

	modules := make([]*ConnectedClientModule, 0, 1+len(apModules))
	modules = append(modules, mainModule)

	for i := range apModules {
		modules = append(modules, &apModules[i])
	}

	// 收集 helper 返回的无线详情。
	// 注意：这些详情可能有残留，所以不作为“在线”的判断依据。
	helperByModule := make(map[string]map[string]ConnectedClient)

	for _, mod := range modules {
		key := ccModuleKey(mod)

		if helperByModule[key] == nil {
			helperByModule[key] = make(map[string]ConnectedClient)
		}

		for _, c := range mod.Clients {
			mac := strings.ToUpper(c.MAC)
			if mac == "" {
				continue
			}

			helperByModule[key][mac] = c
		}
	}

	apByIP := make(map[string]*ConnectedClientModule)
	apMacSet := make(map[string]bool)
	apIPSet := make(map[string]bool)

	for i := range apModules {
		ap := &apModules[i]

		if ap.IP != "" {
			apByIP[ap.IP] = ap
			apIPSet[ap.IP] = true
		}

		if ap.BrLanMAC != "" {
			apMacSet[strings.ToUpper(ap.BrLanMAC)] = true
		}

		if ap.MAC != "" {
			apMacSet[strings.ToUpper(ap.MAC)] = true
		}
	}

	// 如果某个 AP 的 br-lan MAC 没通过 ubus 拿到，用主模块 ARP 表补。
	for mac, arp := range arpByMAC {
		if ap, ok := apByIP[arp.IP]; ok {
			if ap.MAC == "" {
				ap.MAC = strings.ToUpper(mac)
			}
			if ap.BrLanMAC == "" {
				ap.BrLanMAC = strings.ToUpper(mac)
			}
			apMacSet[strings.ToUpper(mac)] = true
		}
	}

	// port -> AP
	// 通过 AP 自己的 br-lan MAC 所在端口建立映射。
	// 不假设 lan1/lan2/lan3/lan4 和 AP 编号有固定关系。
	portToAP := make(map[string]*ConnectedClientModule)

	for _, f := range fdbEntries {
		fmac := strings.ToUpper(f.MAC)

		if !apMacSet[fmac] {
			continue
		}

		// 优先通过 AP br-lan MAC 匹配。
		for i := range apModules {
			ap := &apModules[i]

			if ap.BrLanMAC != "" && strings.EqualFold(ap.BrLanMAC, fmac) {
				portToAP[f.Port] = ap
				break
			}

			if ap.MAC != "" && strings.EqualFold(ap.MAC, fmac) {
				portToAP[f.Port] = ap
				break
			}
		}

		// 如果 MAC 没匹配上，用 ARP 的 IP 反查 AP。
		if _, exists := portToAP[f.Port]; !exists {
			if arp, ok := arpByMAC[fmac]; ok {
				if ap, ok2 := apByIP[arp.IP]; ok2 {
					portToAP[f.Port] = ap
				}
			}
		}
	}

	// 清空所有 module clients，稍后只根据 FDB 重新放入。
	for _, mod := range modules {
		mod.Clients = nil
	}

	fallbackSSID := ccGetLocalSSID24()
	seenClientMAC := make(map[string]bool)

	for _, f := range fdbEntries {
		mac := strings.ToUpper(f.MAC)

		if !ccIsUnicastMAC(mac) {
			continue
		}

		if seenClientMAC[mac] {
			continue
		}

		// 跳过主模块自己的 MAC。
		if mainModule.BrLanMAC != "" && strings.EqualFold(mainModule.BrLanMAC, mac) {
			continue
		}
		if mainModule.MAC != "" && strings.EqualFold(mainModule.MAC, mac) {
			continue
		}
		if mainModule.Ra0MAC != "" && strings.EqualFold(mainModule.Ra0MAC, mac) {
			continue
		}
		if mainModule.Rax0MAC != "" && strings.EqualFold(mainModule.Rax0MAC, mac) {
			continue
		}

		// 跳过 AP 自己的 br-lan MAC。
		if apMacSet[mac] {
			continue
		}

		owner := ccOwnerFromFDBPort(f.Port, mainModule, portToAP)
		if owner == nil {
			continue
		}

		lease, hasLease := leases[mac]

		// 跳过 AP 管理 IP。
		if hasLease && apIPSet[lease.IP] {
			continue
		}

		ownerKey := ccModuleKey(owner)
		helperClient, hasHelper := helperByModule[ownerKey][mac]

		// 如果没有 DHCP，也没有 owner 模块的 helper 详情，则不显示。
		if !hasLease && !hasHelper {
			continue
		}

		client := ConnectedClient{
			MAC:      mac,
			Hostname: "(unknown)",
			Signal:   "Unknown",
			SSID:     fallbackSSID,
			Band:     "2.4G",
		}

		if hasHelper {
			client = helperClient
			client.MAC = mac

			if client.Signal == "" {
				client.Signal = ccSignalText(client.RSSI)
			}
		}

		if hasLease {
			if lease.Hostname != "" {
				client.Hostname = lease.Hostname
			}
			if lease.IP != "" {
				client.IP = lease.IP
			}
		}

		if arp, ok := arpByMAC[mac]; ok && arp.IP != "" {
			client.IP = arp.IP
		}

		if client.SSID == "" {
			client.SSID = fallbackSSID
		}

		if client.Band == "" {
			client.Band = "2.4G"
		}

		if client.Signal == "" {
			client.Signal = ccSignalText(client.RSSI)
		}

		owner.Clients = append(owner.Clients, client)
		seenClientMAC[mac] = true
	}

	for _, mod := range modules {
		ccSortModuleClients(mod)
	}
}

func ccOwnerFromFDBPort(
	port string,
	mainModule *ConnectedClientModule,
	portToAP map[string]*ConnectedClientModule,
) *ConnectedClientModule {
	if port == "" {
		return nil
	}

	if port == ccWirelessIF24G || port == ccWirelessIF5G {
		return mainModule
	}

	if ap, ok := portToAP[port]; ok {
		return ap
	}

	return nil
}

func ccModuleKey(mod *ConnectedClientModule) string {
	if mod == nil {
		return ""
	}

	if mod.Type == "main" {
		return "main"
	}

	if mod.IP != "" {
		return "ap:" + mod.IP
	}

	if mod.BrLanMAC != "" {
		return "ap-mac:" + strings.ToUpper(mod.BrLanMAC)
	}

	if mod.MAC != "" {
		return "ap-mac:" + strings.ToUpper(mod.MAC)
	}

	return "module:" + mod.Name
}

func ccIsUnicastMAC(mac string) bool {
	mac = strings.ToUpper(strings.TrimSpace(mac))

	if mac == "" || mac == "00:00:00:00:00:00" || mac == "FF:FF:FF:FF:FF:FF" {
		return false
	}

	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return false
	}

	first, err := strconv.ParseUint(parts[0], 16, 8)
	if err != nil {
		return false
	}

	if first&1 == 1 {
		return false
	}

	return true
}

// ---------- Sort ----------

func ccSortModuleClients(mod *ConnectedClientModule) {
	ccSortClients(mod.Clients)
}

func ccSortClients(clients []ConnectedClient) {
	sort.Slice(clients, func(i, j int) bool {
		if clients[i].Hostname == clients[j].Hostname {
			return clients[i].MAC < clients[j].MAC
		}

		return clients[i].Hostname < clients[j].Hostname
	})
}

// ---------- DHCP / ARP / FDB ----------

func ccParseLocalDHCPLeases() map[string]ccDhcpLease {
	f, err := os.Open("/tmp/dhcp.leases")
	if err != nil {
		return map[string]ccDhcpLease{}
	}
	defer f.Close()

	out := make(map[string]ccDhcpLease)

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 4 {
			continue
		}

		mac := strings.ToUpper(fields[1])
		ip := fields[2]
		hostname := fields[3]

		if hostname == "" || hostname == "*" || hostname == "?" {
			hostname = "(unknown)"
		}

		out[mac] = ccDhcpLease{
			Hostname: hostname,
			IP:       ip,
			MAC:      mac,
		}
	}

	return out
}

func ccParseLocalARPByMAC() map[string]ccArpEntry {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		return map[string]ccArpEntry{}
	}
	defer f.Close()

	out := make(map[string]ccArpEntry)

	sc := bufio.NewScanner(f)
	first := true

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		if first {
			first = false
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 6 {
			continue
		}

		ip := fields[0]
		flags := strings.ToLower(fields[2])
		mac := strings.ToUpper(fields[3])
		dev := fields[5]

		if flags != "0x2" {
			continue
		}

		if mac == "" || mac == "00:00:00:00:00:00" {
			continue
		}

		out[mac] = ccArpEntry{
			IP:     ip,
			MAC:    mac,
			Device: dev,
		}
	}

	return out
}

func ccParseBridgeFDB() []ccFDBEntry {
	cmd := `
PATH=/usr/sbin:/usr/bin:/sbin:/bin
if command -v bridge >/dev/null 2>&1; then
	bridge fdb show br br-lan
elif [ -x /usr/sbin/bridge ]; then
	/usr/sbin/bridge fdb show br br-lan
elif [ -x /sbin/bridge ]; then
	/sbin/bridge fdb show br br-lan
else
	exit 127
fi
`

	raw, err := exec.Command("sh", "-c", cmd).Output()
	if err != nil {
		log.Printf("connectedClients: bridge fdb error: %v", err)
		return nil
	}

	entries := ccParseBridgeFDBText(string(raw))
	if len(entries) == 0 {
		log.Printf("connectedClients: local bridge fdb empty raw=%q", strings.TrimSpace(string(raw)))
	}

	return entries
}

// ---------- SSID ----------

func ccGetLocalSSID24() string {
	return ccGetLocalSSIDFromPath(ccMtk24GPath)
}

func ccGetLocalSSID5() string {
	return ccGetLocalSSIDFromPath(ccMtk5GPath)
}

func ccGetLocalSSIDFromPath(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return ccParseSSIDFromDat(string(b))
}

func ccGetRemoteSSID24(ip string) string {
	return ccGetRemoteSSIDFromPath(ip, ccMtk24GPath, ccGetLocalSSID24())
}

func ccGetRemoteSSID5(ip string) string {
	return ccGetRemoteSSIDFromPath(ip, ccMtk5GPath, ccGetLocalSSID5())
}

func ccGetRemoteSSIDFromPath(ip string, path string, fallback string) string {
	const zeroSID = "00000000000000000000000000000000"

	res, err := ubusCallJSONAt(ip, zeroSID, "file", "read", map[string]any{
		"path": path,
	})
	if err != nil {
		return fallback
	}

	if data, ok := res["data"].(string); ok {
		ssid := ccParseSSIDFromDat(data)
		if ssid != "" {
			return ssid
		}
	}

	return fallback
}

func ccParseSSIDFromDat(content string) string {
	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "SSID1=") {
			return strings.TrimPrefix(line, "SSID1=")
		}
	}

	return ""
}

// ---------- Module Info Helpers ----------

func ccGetLocalSID() string {
	user := envOr("RPC_USER", "root")
	pass := envOr("RPC_PASS", "")

	sid, _, err := ubusLoginLocal(user, pass)
	if err != nil || sid == "" {
		log.Printf("connectedClients: local ubus login failed: %v", err)
		return ""
	}

	return sid
}

func ccLocalHostname(sid string) string {
	if sid == "" {
		return ""
	}

	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONLocal(sid, "uci", "get", params)
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hostname, ok2 := values["hostname"].(string); ok2 && hostname != "" {
				return hostname
			}
		}
	}

	board, err := ubusCallJSONLocal(sid, "system", "board", nil)
	if err == nil {
		if hostname, ok := board["hostname"].(string); ok && hostname != "" {
			return hostname
		}
	}

	return ""
}

func ccLocalLanIP(sid string) string {
	if sid == "" {
		return ""
	}

	st, err := ubusCallJSONLocal(sid, "network.interface.lan", "status", nil)
	if err != nil {
		return ""
	}

	if arr, ok := st["ipv4-address"].([]any); ok && len(arr) > 0 {
		if m, ok2 := arr[0].(map[string]any); ok2 {
			if addr, ok3 := m["address"].(string); ok3 {
				return addr
			}
		}
	}

	return ""
}

func ccFallbackLocalLanIP() string {
	out, err := exec.Command(
		"sh",
		"-c",
		"ip -4 addr show br-lan 2>/dev/null | awk '/inet /{print $2}' | cut -d/ -f1 | head -n1",
	).Output()

	if err == nil {
		ip := strings.TrimSpace(string(out))
		if ip != "" {
			return ip
		}
	}

	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	for _, iface := range ifaces {
		if iface.Name != "br-lan" {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			return ""
		}

		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}

			ip4 := ipnet.IP.To4()
			if ip4 != nil {
				return ip4.String()
			}
		}
	}

	return ""
}

func ccLocalDeviceMAC(sid string, dev string) string {
	if dev == "" {
		return ""
	}

	// 1. 优先通过 ubus 读取
	if sid != "" {
		st, err := ubusCallJSONLocal(sid, "network.device", "status", map[string]any{
			"name": dev,
		})
		if err == nil {
			if mac, ok := st["macaddr"].(string); ok && ccValidMAC(mac) {
				return strings.ToUpper(mac)
			}
		}
	}

	// 2. 本机 fallback：直接读 Linux netdev address
	return ccReadSysfsMAC(dev)
}

func ccReadSysfsMAC(dev string) string {
	if dev == "" {
		return ""
	}

	b, err := os.ReadFile("/sys/class/net/" + dev + "/address")
	if err != nil {
		return ""
	}

	mac := strings.TrimSpace(string(b))
	if !ccValidMAC(mac) {
		return ""
	}

	return strings.ToUpper(mac)
}

func ccRemoteDeviceMAC(ip string, dev string) string {
	const zeroSID = "00000000000000000000000000000000"

	if ip == "" || dev == "" {
		return ""
	}

	// 1. 优先通过 ubus network.device status 读取
	st, err := ubusCallJSONAt(ip, zeroSID, "network.device", "status", map[string]any{
		"name": dev,
	})
	if err == nil {
		if mac, ok := st["macaddr"].(string); ok && ccValidMAC(mac) {
			return strings.ToUpper(mac)
		}
	}

	// 2. fallback：远程读 /sys/class/net/<dev>/address
	if mac := ccRemoteReadSysfsMAC(ip, dev); mac != "" {
		return mac
	}

	// 3. fallback：远程执行 ifconfig <dev> 并解析 HWaddr
	if mac := ccRemoteReadIfconfigMAC(ip, dev); mac != "" {
		return mac
	}

	return ""
}

func ccRemoteReadSysfsMAC(ip string, dev string) string {
	const zeroSID = "00000000000000000000000000000000"

	if ip == "" || dev == "" {
		return ""
	}

	res, err := ubusCallJSONAt(ip, zeroSID, "file", "read", map[string]any{
		"path": "/sys/class/net/" + dev + "/address",
	})
	if err != nil {
		return ""
	}

	if data, ok := res["data"].(string); ok {
		mac := strings.TrimSpace(data)
		if ccValidMAC(mac) {
			return strings.ToUpper(mac)
		}
	}

	return ""
}

func ccRemoteReadIfconfigMAC(ip string, dev string) string {
	const zeroSID = "00000000000000000000000000000000"

	if ip == "" || dev == "" {
		return ""
	}

	res, err := ubusCallJSONAt(ip, zeroSID, "file", "exec", map[string]any{
		"command": "ifconfig",
		"params":  []string{dev},
	})
	if err != nil {
		return ""
	}

	out := ""

	if stdout, ok := res["stdout"].(string); ok {
		out = stdout
	} else if data, ok := res["data"].(string); ok {
		out = data
	}

	if out == "" {
		return ""
	}

	re := regexp.MustCompile(`(?i)(?:HWaddr|ether)\s+([0-9a-f:]{17})`)
	m := re.FindStringSubmatch(out)

	if len(m) >= 2 && ccValidMAC(m[1]) {
		return strings.ToUpper(m[1])
	}

	return ""
}

func ccValidMAC(mac string) bool {
	mac = strings.ToUpper(strings.TrimSpace(mac))

	if mac == "" ||
		mac == "00:00:00:00:00:00" ||
		mac == "FF:FF:FF:FF:FF:FF" {
		return false
	}

	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return false
	}

	for _, p := range parts {
		if len(p) != 2 {
			return false
		}

		if _, err := strconv.ParseUint(p, 16, 8); err != nil {
			return false
		}
	}

	return true
}

func ccRemoteHostname(ip string) string {
	const zeroSID = "00000000000000000000000000000000"

	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONAt(ip, zeroSID, "uci", "get", params)
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hostname, ok2 := values["hostname"].(string); ok2 && hostname != "" {
				return hostname
			}
		}
	}

	board, err := ubusCallJSONAt(ip, zeroSID, "system", "board", nil)
	if err == nil {
		if hostname, ok := board["hostname"].(string); ok && hostname != "" {
			return hostname
		}
	}

	return ""
}

// ---------- Small Helpers ----------

func ccPingOnce(ip string) bool {
	cmd := exec.Command("ping", "-c", "1", "-W", "1", ip)
	return cmd.Run() == nil
}

func ccSignalText(rssi int) string {
	if rssi == 0 {
		return "Unknown"
	}
	if rssi >= -55 {
		return "Excellent"
	}
	if rssi >= -67 {
		return "Good"
	}
	if rssi >= -75 {
		return "Fair"
	}
	return "Weak"
}

func ccFormatDuration(seconds int) string {
	if seconds <= 0 {
		return ""
	}

	d := time.Duration(seconds) * time.Second

	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60

	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}

	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}

	return fmt.Sprintf("%ds", s)
}

func ccIPSortKey(ip string) int {
	parts := strings.Split(ip, ".")
	if len(parts) != 4 {
		return 0
	}

	last, err := strconv.Atoi(parts[3])
	if err != nil {
		return 0
	}

	return last
}
