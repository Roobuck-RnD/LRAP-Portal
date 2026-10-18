package main

import (
	"bufio"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// The data structure returned to the frontend
// Keeps port/mac/ip/hostname for compatibility with the current frontend
// Adds type/online; the frontend should use type to tell the main module from sub-modules from now on
type LanClient struct {
	ModuleID string `json:"module_id"`
	Port     string `json:"port"`
	MAC      string `json:"mac"`
	IP       string `json:"ip"`
	Hostname string `json:"hostname"`

	Type   string `json:"type,omitempty"`   // "main" / "ap"
	Online bool   `json:"online,omitempty"` // whether the main module/AP is online
}

// ------- ubus helpers -------

func pickLanIPv4(sid string) string {
	st, err := ubusCallJSONLocal(sid, "network.interface.lan", "status", nil)
	if err != nil {
		log.Printf("lanClients: pickLanIPv4 ubus error: %v", err)
		return ""
	}

	if arr, ok := st["ipv4-address"].([]any); ok && len(arr) > 0 {
		if m, ok2 := arr[0].(map[string]any); ok2 {
			if a, ok3 := m["address"].(string); ok3 && a != "" {
				return a
			}
		}
	}

	return ""
}

func getBridgeMAC(sid string) string {
	st, err := ubusCallJSONLocal(sid, "network.device", "status", map[string]any{
		"name": "br-lan",
	})
	if err != nil {
		log.Printf("lanClients: getBridgeMAC ubus error: %v", err)
		return ""
	}

	if mac, ok := st["macaddr"].(string); ok && mac != "" {
		return strings.ToUpper(mac)
	}

	return ""
}

// Main module hostname: read UCI first, fall back to system board
func hostnameFromLocal(sid string) string {
	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONLocal(sid, "uci", "get", params)
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hn, ok2 := values["hostname"].(string); ok2 && hn != "" {
				return hn
			}
		}
	}

	b, err := ubusCallJSONLocal(sid, "system", "board", nil)
	if err == nil {
		if hn, ok := b["hostname"].(string); ok && hn != "" {
			return hn
		}
	}

	return ""
}

// Sub-module hostname: still the AnonSID approach
// Requires the sub-module's ACL to allow the corresponding ubus calls
func resolveRemoteHostname(ip string) string {

	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONAt(ip, AnonSID, "uci", "get", params)
	if err != nil {
		return ""
	}

	if values, ok := res["values"].(map[string]any); ok {
		if hn, ok2 := values["hostname"].(string); ok2 && hn != "" {
			return hn
		}
	}

	return ""
}

// ------- probing and parsing local tables -------

// Ping concurrently, but wait for every ping to finish before reading ARP
// Fixes the earlier race of reading ARP right after starting the pings
func probeStaticIPs(ips []string) {
	var wg sync.WaitGroup

	for _, ip := range ips {
		ip := ip
		wg.Add(1)

		go func() {
			defer wg.Done()

			// -c 1: send 1 packet
			// -W 1: wait at most 1 second
			// OpenWrt busybox ping supports this form
			if err := exec.Command("ping", "-c", "1", "-W", "1", ip).Run(); err != nil {
				// Being offline is normal; no error log needed
				return
			}
		}()
	}

	wg.Wait()
}

// Only valid ARP entries are returned:
// flags == 0x2
// mac != 00:00:00:00:00:00
// device == the active Antenna management bridge
//
// Returns: ip -> mac
func parseARPByIP() map[string]string {
	f, err := os.Open("/proc/net/arp")
	if err != nil {
		log.Printf("lanClients: open /proc/net/arp error: %v", err)
		return map[string]string{}
	}
	defer f.Close()

	out := make(map[string]string)

	sc := bufio.NewScanner(f)
	first := true

	for sc.Scan() {
		ln := strings.TrimSpace(sc.Text())
		if ln == "" {
			continue
		}

		if first {
			first = false
			continue
		}

		fs := strings.Fields(ln)
		if len(fs) < 6 {
			continue
		}

		ip := fs[0]
		flags := strings.ToLower(fs[2])
		mac := strings.ToUpper(fs[3])
		dev := fs[5]

		if flags != "0x2" {
			continue
		}
		if mac == "" || mac == "00:00:00:00:00:00" {
			continue
		}
		expectedDevice := "br-lan"
		if antennaManagementIsIsolated() {
			expectedDevice = AntennaManagementBridge
		}
		if dev != expectedDevice {
			continue
		}

		out[ip] = mac
	}

	if err := sc.Err(); err != nil {
		log.Printf("lanClients: scan /proc/net/arp error: %v", err)
	}

	return out
}

// Read the bridge fdb and build a mac -> port map
// Looser than the earlier match on lan1-lan4 only
func listLanEdgePortsByMAC() map[string]string {
	out := make(map[string]string)
	bridgeName := "br-lan"
	if antennaManagementIsIsolated() {
		bridgeName = AntennaManagementBridge
	}

	raw, err := exec.Command("bridge", "fdb", "show", "br", bridgeName).Output()
	if err != nil {
		log.Printf("lanClients: bridge fdb error on %s: %v", bridgeName, err)
		return out
	}

	// Typical lines:
	// 52:53:29:c5:88:a4 dev lan1 master br-lan
	// 52:53:29:c5:88:a4 dev wan master br-lan
	// 52:53:29:c5:88:a4 dev eth0 master br-lan
	reLine := regexp.MustCompile(`(?i)^([0-9a-f:]{17})\s+dev\s+([^\s]+)\b`)

	lines := strings.Split(string(raw), "\n")

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}

		lower := strings.ToLower(ln)

		// Skip the bridge's own entries, permanent entries and vlan entries
		if strings.Contains(lower, " self ") ||
			strings.Contains(lower, " permanent") ||
			strings.Contains(lower, " vlan ") {
			continue
		}

		m := reLine.FindStringSubmatch(ln)
		if len(m) != 3 {
			continue
		}

		mac := strings.ToUpper(m[1])
		port := strings.TrimSuffix(m[2], ".200")

		// Skip br-lan itself
		if port == bridgeName {
			continue
		}

		// If the same MAC appears more than once, keep the first physical port
		if _, exists := out[mac]; !exists {
			out[mac] = port
		}
	}

	return out
}

// ------- HTTP Handler -------

func lanClientsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	// 1) Get the sid
	auth := r.Header.Get("Authorization")
	sid := ""

	if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
		sid = auth[7:]
	}

	// The sid comes from the request Bearer (withAuth guarantees it is there).

	// 2) Probe the static AP IPs and wait for the pings to finish
	registry := discoverManagedAPs(true)

	// 3) The main module itself
	main := LanClient{
		ModuleID: managedMainModuleID,
		Port:     "br-lan",
		MAC:      getBridgeMAC(sid),
		IP:       pickLanIPv4(sid),
		Hostname: hostnameFromLocal(sid),
		Type:     "main",
		Online:   true,
	}

	if main.Hostname == "" {
		main.Hostname = "(unknown)"
	}
	if main.IP == "" {
		main.IP = "(unknown)"
	}
	if main.MAC == "" {
		main.MAC = "(unknown)"
	}

	// 4) Read ARP and FDB

	// 5) Assemble the response
	finalOut := make([]LanClient, 0, 1+len(registry))
	finalOut = append(finalOut, main)

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, module := range registry {
		if !ifacePingOnce(module.IP) {
			// Offline APs are not returned, as requested
			continue
		}

		client := LanClient{
			ModuleID: module.ModuleID,
			Port:     module.Port,
			MAC:      module.MAC,
			IP:       module.IP,
			// Display names are numbered by physical port (RoobuckAP1..4), stable whatever the IP; the device's real hostname is unchanged.
			Hostname: apDisplayName("RoobuckAP", module.PortIndex),
			Type:     "ap",
			Online:   true,
		}

		idx := len(finalOut)
		finalOut = append(finalOut, client)

		// Read the real hostnames concurrently, then number them by physical port
		wg.Add(1)
		go func(targetIdx, pN int, targetIP string) {
			defer wg.Done()

			realName := resolveRemoteHostname(targetIP)
			if realName == "" {
				return
			}

			mu.Lock()
			finalOut[targetIdx].Hostname = apDisplayName(realName, pN)
			mu.Unlock()
		}(idx, module.PortIndex, module.IP)
	}

	wg.Wait()

	// Product order follows physical identity, never the transient management IP:
	// Router, Antenna1/lan1, ..., Antenna4/lan4. Unknown identities are kept at
	// the end with IP as a deterministic compatibility fallback.
	sort.SliceStable(finalOut, func(i, j int) bool {
		left := managedModuleDisplaySortKey(finalOut[i].ModuleID, finalOut[i].Type, finalOut[i].Port, finalOut[i].IP)
		right := managedModuleDisplaySortKey(finalOut[j].ModuleID, finalOut[j].Type, finalOut[j].Port, finalOut[j].IP)
		if left != right {
			return left < right
		}
		return finalOut[i].IP < finalOut[j].IP
	})

	_ = json.NewEncoder(w).Encode(finalOut)
}
