// overview_memory.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Reuses helpers already in overview_system.go / main.go:
// - envOr
// - ubusLoginLocal
// - ubusCallJSONLocal
// - ubusCallJSONAt
// - parseUbusResp
// - newHTTPClient

type MemoryInfo struct {
	// ubus system.info reports these in BYTES (passed through unconverted below).
	Total     int64 `json:"total"`      // bytes
	Available int64 `json:"available"`  // bytes
	Used      int64 `json:"used"`       // bytes (Total - Available)
	Buffered  int64 `json:"buffered"`   // bytes
	Cached    int64 `json:"cached"`     // bytes
}

// GET /api/status/overview/memory[?ip=10.10.18.X]
func memoryOverviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	mi, err := getMemoryViaUbus(r)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	_ = json.NewEncoder(w).Encode(mi)
}

func getMemoryViaUbus(r *http.Request) (MemoryInfo, error) {
	q := r.URL.Query()
	ip := strings.TrimSpace(q.Get("ip"))

	// -------- this device ----------
	if ip == "" {
		// 1) Get a sid: the frontend's Bearer first; otherwise sign in to the local ubus with the backend's account
		auth := r.Header.Get("Authorization")
		sid := ""
		if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
			sid = auth[7:]
		}
		if sid == "" {
			return MemoryInfo{}, fmt.Errorf("unauthorized: missing session token")
		}

		// 2) Call the local ubus: system.info
		info, err := ubusCallJSONLocal(sid, "system", "info", nil)
		if err != nil {
			return MemoryInfo{}, err
		}
		return mapToMemoryInfo(info)
	}

	// -------- remote sub-module ----------
	// Anonymous zero SID (needs the sub-board's rpcd to allow system.info unauthenticated)

	info, err := ubusCallJSONAt(ip, AnonSID, "system", "info", nil)
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("remote info failed (%s): %v", ip, err)
	}
	return mapToMemoryInfo(info)
}

// Parses ubus system.info into MemoryInfo
func mapToMemoryInfo(info map[string]any) (MemoryInfo, error) {
	mem, ok := info["memory"].(map[string]any)
	if !ok {
		return MemoryInfo{}, fmt.Errorf("no memory section in ubus system.info")
	}

	toI64 := func(v any) int64 {
		switch x := v.(type) {
		case float64:
			return int64(x)
		case int:
			return int64(x)
		case int64:
			return x
		default:
			return 0
		}
	}

	total := toI64(mem["total"])
	available := toI64(mem["available"])
	buffered := toI64(mem["buffered"])
	cached := toI64(mem["cached"])
	used := total - available
	if used < 0 {
		used = 0
	}

	return MemoryInfo{
		Total:     total,
		Available: available,
		Used:      used,
		Buffered:  buffered,
		Cached:    cached,
	}, nil
}
