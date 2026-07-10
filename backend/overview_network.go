// // overview_network.go
// package main

// import (
// 	"encoding/json"
// 	"fmt"
// 	"net/http"
// 	"strings"
// )

// type NetworkInfo struct {
// 	Protocol  string `json:"protocol"`
// 	Address   string `json:"address"`
// 	Device    string `json:"device"`
// 	Gateway   string `json:"gateway"`
// 	DNS       string `json:"dns"`
// 	Connected string `json:"connected"` // 形如 "1 d, 02 h, 03 min"
// 	MAC       string `json:"mac"`
// }

// // GET /api/status/overview/network[?ip=10.10.18.X]
// func networkOverviewHandler(w http.ResponseWriter, r *http.Request) {
// 	if r.Method != http.MethodGet {
// 		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
// 		return
// 	}
// 	w.Header().Set("Content-Type", "application/json")

// 	ni, err := getNetworkViaUbus(r)
// 	if err != nil {
// 		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
// 		return
// 	}
// 	_ = json.NewEncoder(w).Encode(ni)
// }

// func getNetworkViaUbus(r *http.Request) (NetworkInfo, error) {
// 	q := r.URL.Query()
// 	ip := strings.TrimSpace(q.Get("ip"))

// 	// ------- 本机 -------
// 	if ip == "" {
// 		auth := r.Header.Get("Authorization")
// 		sid := ""
// 		if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
// 			sid = auth[7:]
// 		}
// 		if sid == "" {
// 			user := envOr("RPC_USER", "root")
// 			pass := envOr("RPC_PASS", "")
// 			var extra map[string]any
// 			var err error
// 			sid, extra, err = ubusLoginLocal(user, pass)
// 			_ = extra
// 			if err != nil || sid == "" {
// 				return NetworkInfo{}, fmt.Errorf("ubus login failed: %v", err)
// 			}
// 		}
// 		return readNetworkVia(sid,
// 			func(object, method string, params map[string]any) (map[string]any, error) {
// 				return ubusCallJSONLocal(sid, object, method, params)
// 			},
// 		)
// 	}

// 	// ------- 远端（子模块） -------
// 	const zeroSID = "00000000000000000000000000000000"
// 	return readNetworkVia(zeroSID,
// 		func(object, method string, params map[string]any) (map[string]any, error) {
// 			return ubusCallJSONAt(ip, zeroSID, object, method, params)
// 		},
// 	)
// }

// type ubusGetter func(object, method string, params map[string]any) (map[string]any, error)

// func readNetworkVia(sid string, get ubusGetter) (NetworkInfo, error) {
// 	// 1) 逻辑接口状态（取 WAN）
// 	ifStatus, err := get("network.interface", "status", map[string]any{"interface": "wan"})
// 	if err != nil {
// 		return NetworkInfo{}, fmt.Errorf("network.interface status failed: %v", err)
// 	}

// 	// 2) 解析字段
// 	proto, _ := ifStatus["proto"].(string)
// 	device, _ := ifStatus["device"].(string)

// 	address := ""
// 	if arr, ok := ifStatus["ipv4-address"].([]any); ok && len(arr) > 0 {
// 		if m, ok2 := arr[0].(map[string]any); ok2 {
// 			address, _ = m["address"].(string)
// 		}
// 	}
// 	gateway := ""
// 	if arr, ok := ifStatus["route"].([]any); ok && len(arr) > 0 {
// 		if m, ok2 := arr[0].(map[string]any); ok2 {
// 			gateway, _ = m["nexthop"].(string)
// 		}
// 	}
// 	dns := ""
// 	if arr, ok := ifStatus["dns-server"].([]any); ok && len(arr) > 0 {
// 		if s, ok2 := arr[0].(string); ok2 {
// 			dns = s
// 		}
// 	}
// 	connected := ""
// 	if u, ok := ifStatus["uptime"].(float64); ok {
// 		connected = formatDHCPUptime(int(u))
// 	}

// 	// 3) 设备层拿 MAC（整表拿回，再按 device 索引）
// 	devStatus, err := get("network.device", "status", nil)
// 	if err != nil {
// 		return NetworkInfo{}, fmt.Errorf("network.device status failed: %v", err)
// 	}
// 	mac := ""
// 	if device != "" {
// 		if one, ok := devStatus[device].(map[string]any); ok {
// 			if s, ok2 := one["macaddr"].(string); ok2 {
// 				mac = s
// 			}
// 		}
// 	}

// 	return NetworkInfo{
// 		Protocol:  nz(proto),
// 		Address:   nz(address),
// 		Device:    nz(device),
// 		Gateway:   nz(gateway),
// 		DNS:       nz(dns),
// 		Connected: connected,
// 		MAC:       nz(mac),
// 	}, nil
// }

// func nz(s string) string {
// 	if strings.TrimSpace(s) == "" {
// 		return "unknown"
// 	}
// 	return s
// }

// func formatDHCPUptime(sec int) string {
// 	if sec < 0 {
// 		sec = 0
// 	}
// 	d := sec / 86400
// 	h := (sec % 86400) / 3600
// 	m := (sec % 3600) / 60
// 	if d > 0 {
// 		return fmt.Sprintf("%d d, %02d h, %02d min", d, h, m)
// 	}
// 	return fmt.Sprintf("%02d h, %02d min", h, m)
// }

// 新架构：ac+ap:

// overview_network.go
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
	Connected string `json:"connected"` // 形如 "1 d, 02 h, 03 min"
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

	// ------- 本机 -------
	if ip == "" {
		auth := r.Header.Get("Authorization")
		sid := ""
		if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
			sid = auth[7:]
		}
		if sid == "" {
			user := envOr("RPC_USER", "root")
			pass := envOr("RPC_PASS", "")
			var extra map[string]any
			var err error
			sid, extra, err = ubusLoginLocal(user, pass)
			_ = extra
			if err != nil || sid == "" {
				return NetworkInfo{}, fmt.Errorf("ubus login failed: %v", err)
			}
		}
		return readNetworkVia(sid,
			func(object, method string, params map[string]any) (map[string]any, error) {
				return ubusCallJSONLocal(sid, object, method, params)
			},
		)
	}

	// ------- 远端（子模块） -------
	const zeroSID = "00000000000000000000000000000000"
	return readNetworkVia(zeroSID,
		func(object, method string, params map[string]any) (map[string]any, error) {
			return ubusCallJSONAt(ip, zeroSID, object, method, params)
		},
	)
}

type ubusGetter func(object, method string, params map[string]any) (map[string]any, error)

func readNetworkVia(sid string, get ubusGetter) (NetworkInfo, error) {
	// 1) 逻辑接口状态：先尝试取 WAN（主模块适用）
	ifStatus, err := get("network.interface", "status", map[string]any{"interface": "wan"})
	
	// 🟢 核心修复：如果取 WAN 报错（比如 result:[4] 找不到接口），降级去取 LAN（纯 AP 子模块适用）
	if err != nil {
		var errFallback error
		ifStatus, errFallback = get("network.interface", "status", map[string]any{"interface": "lan"})
		if errFallback != nil {
			// 如果连 LAN 都报错，说明底层确实出问题了
			return NetworkInfo{}, fmt.Errorf("network query failed (wan and lan): %v", errFallback)
		}
	}

	// 2) 解析字段
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

	// 3) 设备层拿 MAC（整表拿回，再按 device 索引）
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