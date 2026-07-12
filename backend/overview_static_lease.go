package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// 这两个端点(static-map / static-lease)服务于 Overview 页面(以 MAC 为中心)。
// 真正的 UCI 增删改查 + dnsmasq reload 逻辑集中在 static_leases_config.go 的共享
// service(staticLeaseUpsert / staticLeaseDeleteByMAC / getUciStaticLeasesLocal),
// 这里只做请求解析与 Overview 期望的响应格式化。静态租约是 AC-only(AP 关了
// DHCP),故不再支持 ?ip= 远程。鉴权由 main.go 的 withAuth 统一保证。

// ---------- 静态租约 列表 ----------

// StaticEntry 是 GET /api/lan/static-map 的响应结构(字段名 ip/name,供 Overview 页解析)。
type StaticEntry struct {
	Section string `json:"section"`
	MAC     string `json:"mac"`
	IP      string `json:"ip"`
	Name    string `json:"name,omitempty"`
}

func staticMapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	leases, err := getUciStaticLeasesLocal(bearerSID(r))
	if err != nil {
		http.Error(w, `{"error":"uci get dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}

	out := make([]StaticEntry, 0, len(leases))
	for _, l := range leases {
		out = append(out, StaticEntry{
			Section: l.Section,
			MAC:     strings.ToUpper(l.MAC),
			IP:      l.IPAddr,
			Name:    l.Hostname,
		})
	}

	_ = json.NewEncoder(w).Encode(out)
}

// ---------- 设置/取消 静态租约 ----------

type staticLeaseIn struct {
	Hostname string `json:"hostname"`
	MAC      string `json:"mac"`
	IPAddr   string `json:"ipaddr"`
}

type staticLeaseOut struct {
	Updated bool   `json:"updated"` // true=更新，false=新增
	Section string `json:"section"`
}

func setStaticLeaseHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handleSetStatic(w, r)
	case http.MethodDelete:
		handleUnsetStatic(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleSetStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var in staticLeaseIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	in.MAC = strings.ToUpper(strings.TrimSpace(in.MAC))
	in.IPAddr = strings.TrimSpace(in.IPAddr)
	in.Hostname = strings.TrimSpace(in.Hostname)
	if in.MAC == "" || in.IPAddr == "" {
		http.Error(w, `{"error":"mac and ipaddr are required"}`, http.StatusBadRequest)
		return
	}

	section, updated, err := staticLeaseUpsert(bearerSID(r), in.MAC, in.IPAddr, in.Hostname)
	if err != nil {
		http.Error(w, `{"error":"save failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(staticLeaseOut{Updated: updated, Section: section})
}

func handleUnsetStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var in struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	in.MAC = strings.ToUpper(strings.TrimSpace(in.MAC))
	if in.MAC == "" {
		http.Error(w, `{"error":"mac is required"}`, http.StatusBadRequest)
		return
	}

	section, deleted, err := staticLeaseDeleteByMAC(bearerSID(r), in.MAC)
	if err != nil {
		http.Error(w, `{"error":"delete failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}
	if !deleted {
		// 本就不存在，当作成功
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": false})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true, "section": section})
}
