package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type NetworkInfo struct {
	Protocol  string `json:"protocol"`
	Address   string `json:"address"`
	Device    string `json:"device"`
	Gateway   string `json:"gateway"`
	DNS       string `json:"dns"`
	Connected string `json:"connected"` // e.g. "1 d, 02 h, 03 min"
	MAC       string `json:"mac"`
}

// GET /api/status/overview/network[?ip=10.10.18.X]
func networkOverviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	ni, err := getNetworkViaUbus(r)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(ni)
}

func getNetworkViaUbus(r *http.Request) (NetworkInfo, error) {
	q := r.URL.Query()
	ip := strings.TrimSpace(q.Get("ip"))

	// ------- this device -------
	if ip == "" {
		auth := r.Header.Get("Authorization")
		sid := ""
		if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
			sid = auth[7:]
		}
		if sid == "" {
			return NetworkInfo{}, fmt.Errorf("unauthorized: missing session token")
		}
		return readNetworkVia(sid,
			func(object, method string, params map[string]any) (map[string]any, error) {
				return ubusCallJSONLocal(sid, object, method, params)
			},
		)
	}

	// ------- remote (sub-module) -------
	return readNetworkVia(AnonSID,
		func(object, method string, params map[string]any) (map[string]any, error) {
			return ubusCallJSONAt(ip, AnonSID, object, method, params)
		},
	)
}

type ubusGetter func(object, method string, params map[string]any) (map[string]any, error)

func readNetworkVia(sid string, get ubusGetter) (NetworkInfo, error) {
	// 1) Logical interface status: try WAN first (main module)
	ifStatus, err := get("network.interface", "status", map[string]any{"interface": "wan"})
	
	// 🟢 The fix: if reading WAN fails (e.g. result:[4], no such interface), fall back to LAN (a pure AP sub-module)
	if err != nil {
		var errFallback error
		ifStatus, errFallback = get("network.interface", "status", map[string]any{"interface": "lan"})
		if errFallback != nil {
			// If even LAN fails, something underneath really is wrong
			return NetworkInfo{}, fmt.Errorf("network query failed (wan and lan): %v", errFallback)
		}
	}

	// 2) Parse the fields
	proto, _ := ifStatus["proto"].(string)
	device, _ := ifStatus["device"].(string)

	address := ""
	if arr, ok := ifStatus["ipv4-address"].([]any); ok && len(arr) > 0 {
		if m, ok2 := arr[0].(map[string]any); ok2 {
			address, _ = m["address"].(string)
		}
	}
	gateway := ""
	if arr, ok := ifStatus["route"].([]any); ok && len(arr) > 0 {
		if m, ok2 := arr[0].(map[string]any); ok2 {
			gateway, _ = m["nexthop"].(string)
		}
	}
	dns := ""
	if arr, ok := ifStatus["dns-server"].([]any); ok && len(arr) > 0 {
		if s, ok2 := arr[0].(string); ok2 {
			dns = s
		}
	}
	connected := ""
	if u, ok := ifStatus["uptime"].(float64); ok {
		connected = formatDHCPUptime(int(u))
	}

	// 3) Get the MAC from the device layer (fetch the whole table, then index by device)
	devStatus, err := get("network.device", "status", nil)
	if err != nil {
		return NetworkInfo{}, fmt.Errorf("network.device status failed: %v", err)
	}
	mac := ""
	if device != "" {
		if one, ok := devStatus[device].(map[string]any); ok {
			if s, ok2 := one["macaddr"].(string); ok2 {
				mac = strings.ToUpper(s)
			}
		}
	}

	return NetworkInfo{
		Protocol:  nz(proto),
		Address:   nz(address),
		Device:    nz(device),
		Gateway:   nz(gateway),
		DNS:       nz(dns),
		Connected: connected,
		MAC:       nz(mac),
	}, nil
}

func nz(s string) string {
	if strings.TrimSpace(s) == "" {
		return "unknown"
	}
	return s
}

func formatDHCPUptime(sec int) string {
	if sec < 0 {
		sec = 0
	}
	d := sec / 86400
	h := (sec % 86400) / 3600
	m := (sec % 3600) / 60
	if d > 0 {
		return fmt.Sprintf("%d d, %02d h, %02d min", d, h, m)
	}
	return fmt.Sprintf("%02d h, %02d min", h, m)
}