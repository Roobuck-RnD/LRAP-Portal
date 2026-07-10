// overview_dhcplist.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// 复用（项目内已有）：
// - envOr
// - ubusLoginLocal
// - ubusCallJSONLocal

type LeaseInfo struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	MAC      string `json:"mac"`
	Expires  string `json:"expires"`
}

type ResetDhcpResponse struct {
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

var reservedAPDHCPIPs = map[string]bool{
	"10.10.18.2": true,
	"10.10.18.3": true,
	"10.10.18.4": true,
	"10.10.18.5": true,
}

// GET /api/lan/leases
// AC+AP 架构下，DHCP 只在 AC 主模块上运行。
// 因此这里永远只读取主模块本机 /tmp/dhcp.leases，不再支持 ?ip= 子模块读取。
// 10.10.18.2 - 10.10.18.5 是固定子模块管理 IP，不返回到 DHCP lease 列表，也不参与前端计数。
func lanLeasesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	leases, err := getLocalDhcpLeasesViaUbus(r)
	if err != nil {
		http.Error(w, `{"error":"`+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(leases)
}

// POST /api/lan/leases/reset
// 清空 AC 本机 DHCP lease 文件并重启 dnsmasq。
// 注意：这会让客户端重新续租 DHCP，短时间内页面 lease 可能为空。
func resetDhcpLeasesHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	w.Header().Set("Content-Type", "application/json")

	_, err := getLocalSIDFromRequest(r)
	if err != nil {
		http.Error(w, `{"error":"unauthorized: `+escapeErr(err)+`"}`, http.StatusUnauthorized)
		return
	}

	if err := resetLocalDhcpLeases(); err != nil {
		http.Error(w, `{"error":"`+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(ResetDhcpResponse{
		Status:  "ok",
		Message: "DHCP leases reset and dnsmasq restarted",
	})
}

func getLocalDhcpLeasesViaUbus(r *http.Request) ([]LeaseInfo, error) {
	sid, err := getLocalSIDFromRequest(r)
	if err != nil {
		return nil, err
	}

	return readLeasesWith(func(object, method string, params map[string]any) (map[string]any, error) {
		return ubusCallJSONLocal(sid, object, method, params)
	})
}

func getLocalSIDFromRequest(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	sid := ""

	if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
		sid = strings.TrimSpace(auth[7:])
	}

	if sid != "" {
		return sid, nil
	}

	user := envOr("RPC_USER", "root")
	pass := envOr("RPC_PASS", "")

	var extra map[string]any
	var err error

	sid, extra, err = ubusLoginLocal(user, pass)
	_ = extra

	if err != nil || sid == "" {
		return "", fmt.Errorf("ubus login failed: %v", err)
	}

	return sid, nil
}

// ---------- Reset DHCP ----------

func resetLocalDhcpLeases() error {
	// 清空 lease 文件。使用 WriteFile 而不是 Remove，避免 dnsmasq 对文件路径/权限敏感。
	if err := os.WriteFile("/tmp/dhcp.leases", []byte(""), 0644); err != nil {
		return fmt.Errorf("clear /tmp/dhcp.leases failed: %w", err)
	}

	// 重启 dnsmasq，让 DHCP 状态重新开始。
	// OpenWrt 通常使用 /etc/init.d/dnsmasq restart。
	out, err := exec.Command("/etc/init.d/dnsmasq", "restart").CombinedOutput()
	if err != nil {
		return fmt.Errorf("dnsmasq restart failed: %w output=%s", err, string(out))
	}

	return nil
}

// ---------- 公共读取 + 兜底逻辑 ----------

func readLeasesWith(call func(object, method string, params map[string]any) (map[string]any, error)) ([]LeaseInfo, error) {
	out, err := call("file", "read", map[string]any{"path": "/tmp/dhcp.leases"})
	if err != nil {
		if code, ok := ubusErrCode(err); ok {
			switch code {
			case 4, 5:
				return []LeaseInfo{}, nil
			case 6:
				return nil, fmt.Errorf("permission denied for file.read on /tmp/dhcp.leases (ubus code=%d): check ACL or token privileges", code)
			}

			return nil, err
		}

		es := strings.ToLower(err.Error())
		if strings.Contains(es, "not found") {
			return []LeaseInfo{}, nil
		}

		return nil, err
	}

	text, _ := out["data"].(string)
	if strings.TrimSpace(text) == "" {
		return []LeaseInfo{}, nil
	}

	return parseLeasesText(text), nil
}

// 解析 ubus 错误里常见的几种形态，提取出数字错误码。
// 兼容：result":[5] / result:[5] / "code":5 / code=5
var reUbusCode = regexp.MustCompile(`(?i)(?:result["']?\s*:\s*\[\s*(\d+)\s*\]|code["']?\s*[:=]\s*(\d+))`)

func ubusErrCode(err error) (int, bool) {
	if err == nil {
		return 0, false
	}

	s := err.Error()
	m := reUbusCode.FindStringSubmatch(s)

	if len(m) >= 3 {
		for _, g := range m[1:3] {
			if g != "" {
				n, convErr := strconv.Atoi(g)
				if convErr == nil {
					return n, true
				}
			}
		}
	}

	return 0, false
}

// ---------- 文本解析与小工具 ----------

func parseLeasesText(s string) []LeaseInfo {
	lines := strings.Split(s, "\n")
	now := time.Now().Unix()
	out := make([]LeaseInfo, 0, len(lines))

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}

		// 形如：<expiry> <mac> <ip> <hostname> <clientid|duid>
		// 例：1762164925 f4:3b:d8:7f:cc:40 10.10.18.248 DESKTOP-KC17O22 01:f4:3b:d8:7f:cc:40
		parts := strings.Fields(ln)
		if len(parts) < 4 {
			continue
		}

		expStr, mac, ip, host := parts[0], parts[1], parts[2], parts[3]
		ip = strings.TrimSpace(ip)

		// 10.10.18.2 - 10.10.18.5 是 AP 子模块管理 IP，隐藏并且不计数。
		if isReservedAPDHCPIP(ip) {
			continue
		}

		if host == "*" || host == "* *" || host == "assets" {
			host = "(unknown)"
		}

		mac = strings.ToUpper(mac)

		var expUnix int64

		onlyDigits := true
		for i := 0; i < len(expStr); i++ {
			if expStr[i] < '0' || expStr[i] > '9' {
				onlyDigits = false
				break
			}
		}

		if onlyDigits {
			if v, err := parseInt64(expStr); err == nil {
				expUnix = v
			}
		}

		remain := expUnix - now
		if remain < 0 {
			remain = 0
		}

		expires := humanRemain(remain)

		out = append(out, LeaseInfo{
			Hostname: host,
			IP:       ip,
			MAC:      mac,
			Expires:  expires,
		})
	}

	return out
}

func isReservedAPDHCPIP(ip string) bool {
	return reservedAPDHCPIPs[strings.TrimSpace(ip)]
}

func parseInt64(s string) (int64, error) {
	var n int64

	for i := 0; i < len(s); i++ {
		n = n*10 + int64(s[i]-'0')
	}

	return n, nil
}

func humanRemain(sec int64) string {
	h := sec / 3600
	m := (sec % 3600) / 60
	s := sec % 60

	return fmt.Sprintf("%dh %dm %ds", h, m, s)
}

func escapeErr(err error) string {
	return strings.ReplaceAll(err.Error(), `"`, `'`)
}
