// overview_memory.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// 复用 overview_system.go / main.go 中已有的工具函数：
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

	// -------- 本机 ----------
	if ip == "" {
		// 1) 获取 sid：优先从前端 Bearer；否则用后端账号登录本机 ubus
		auth := r.Header.Get("Authorization")
		sid := ""
		if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
			sid = auth[7:]
		}
		if sid == "" {
			return MemoryInfo{}, fmt.Errorf("unauthorized: missing session token")
		}

		// 2) 调用本机 ubus: system.info
		info, err := ubusCallJSONLocal(sid, "system", "info", nil)
		if err != nil {
			return MemoryInfo{}, err
		}
		return mapToMemoryInfo(info)
	}

	// -------- 远程子模块 ----------
	// 匿名零 SID（要求子板 rpcd 放开 unauthenticated 的 system.info）

	info, err := ubusCallJSONAt(ip, AnonSID, "system", "info", nil)
	if err != nil {
		return MemoryInfo{}, fmt.Errorf("remote info failed (%s): %v", ip, err)
	}
	return mapToMemoryInfo(info)
}

// 将 ubus 的 system.info 结果解析为 MemoryInfo
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
