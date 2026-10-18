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

// Reuses (already in this project):
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

// GET /api/lan/leases
// In the AC+AP architecture DHCP runs only on the AC main module.
// So this always reads the main module's own /tmp/dhcp.leases; reading a sub-module with ?ip= is no longer supported.
// 10.10.18.2 - 10.10.18.5 are the fixed sub-module management IPs; they are not returned in the lease list nor counted by the frontend.
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
// Empties the AC's own DHCP lease file and restarts dnsmasq.
// Note: clients then renew their leases, so the page may briefly show no leases.
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

	return "", fmt.Errorf("unauthorized: missing session token")
}

// ---------- Reset DHCP ----------

func resetLocalDhcpLeases() error {
	// Empty the lease file. WriteFile rather than Remove, since dnsmasq is sensitive to the file path and permissions.
	if err := os.WriteFile("/tmp/dhcp.leases", []byte(""), 0644); err != nil {
		return fmt.Errorf("clear /tmp/dhcp.leases failed: %w", err)
	}

	// Restart dnsmasq so DHCP starts over.
	// OpenWrt normally uses /etc/init.d/dnsmasq restart.
	out, err := exec.Command("/etc/init.d/dnsmasq", "restart").CombinedOutput()
	if err != nil {
		return fmt.Errorf("dnsmasq restart failed: %w output=%s", err, string(out))
	}

	return nil
}

// ---------- shared reads + fallback ----------

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

// Parses the usual shapes of a ubus error and extracts the numeric error code.
// Accepts: result":[5] / result:[5] / "code":5 / code=5
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

// ---------- text parsing and small helpers ----------

func parseLeasesText(s string) []LeaseInfo {
	lines := strings.Split(s, "\n")
	now := time.Now().Unix()
	out := make([]LeaseInfo, 0, len(lines))

	for _, ln := range lines {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}

		// Shape: <expiry> <mac> <ip> <hostname> <clientid|duid>
		// e.g. 1762164925 f4:3b:d8:7f:cc:40 10.10.18.248 DESKTOP-KC17O22 01:f4:3b:d8:7f:cc:40
		parts := strings.Fields(ln)
		if len(parts) < 4 {
			continue
		}

		expStr, mac, ip, host := parts[0], parts[1], parts[2], parts[3]
		ip = strings.TrimSpace(ip)

		// 10.10.18.2 - 10.10.18.5 are the AP sub-module management IPs: hidden and not counted.
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
	return IsManagedAPIP(strings.TrimSpace(ip))
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
