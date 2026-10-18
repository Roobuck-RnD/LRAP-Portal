// overview_system.go
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)



const lrapVersionPath = "/etc/lrap.version"

type SysInfo struct {
	Hostname        string `json:"hostname"`
	Model           string `json:"model"`
	Architecture    string `json:"architecture"`
	Target          string `json:"target"`
	FirmwareVersion string `json:"firmware_version"`
	KernelVersion   string `json:"kernel_version"`
	LocalTime       string `json:"local_time"`
	Uptime          string `json:"uptime"`
	LoadAverage     string `json:"load_average"`
}

func getArchitecture() string {
	out, err := exec.Command("uname", "-m").Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(out))
}

// One way to read system information
func getSysInfoViaUbus(r *http.Request) (SysInfo, error) {
	q := r.URL.Query()
	ip := strings.TrimSpace(q.Get("ip"))

	if ip == "" {
		// This device: use the frontend's Bearer if given, otherwise sign in locally from the backend
		auth := r.Header.Get("Authorization")
		sid := ""
		if len(auth) > 7 && (strings.HasPrefix(auth, "Bearer ") || strings.HasPrefix(auth, "bearer ")) {
			sid = strings.TrimSpace(auth[7:])
		}
		if sid == "" {
			return SysInfo{}, fmt.Errorf("unauthorized: missing session token")
		}

		board, err := ubusCallJSONLocal(sid, "system", "board", nil)
		if err != nil {
			return SysInfo{}, err
		}
		info, err := ubusCallJSONLocal(sid, "system", "info", nil)
		if err != nil {
			return SysInfo{}, err
		}

		arch := architectureFromBoard(board)
		if arch == "" {
			arch = getArchitecture()
		}

		customVersion := readLocalLRAPVersion()
		return mapToSysInfo(board, info, arch, customVersion), nil
	}

	// Remote sub-module (unauthenticated zero SID; the sub-module's rpcd unauth ACL must allow system.board/info/file.read)

	board, err := ubusCallJSONAt(ip, AnonSID, "system", "board", nil)
	if err != nil {
		return SysInfo{}, fmt.Errorf("remote board failed (%s): %v", ip, err)
	}
	info, err := ubusCallJSONAt(ip, AnonSID, "system", "info", nil)
	if err != nil {
		return SysInfo{}, fmt.Errorf("remote info failed (%s): %v", ip, err)
	}

	arch := architectureFromBoard(board)
	if arch == "" {
		// The main module and the APs are all MT7981 today, so this fallback works;
		// if the hardware ever differs, prefer the board field above.
		arch = getArchitecture()
	}

	customVersion := readRemoteLRAPVersion(ip)
	sys := mapToSysInfo(board, info, arch, customVersion)
	// An AP's System Info card is also named by physical port (Antenna1-4), consistent with Connected Clients / WiFi;
	// the AC (ip=="") takes the local branch above and keeps its real hostname. If the port cannot be resolved, the real hostname is used.
	sys.Hostname = apDisplayName(sys.Hostname, apPortIndexByIP()[ip])
	return sys, nil
}

func mapToSysInfo(board, info map[string]any, arch string, customFirmwareVersion string) SysInfo {
	hostname, _ := board["hostname"].(string)
	model, _ := board["model"].(string)
	kernel, _ := board["kernel"].(string)

	target := ""
	if rel, ok := board["release"].(map[string]any); ok {
		if t, ok2 := rel["target"].(string); ok2 {
			target = t
		}
	}

	var localTimeStr, uptimeStr, loadStr string
	if u, ok := info["uptime"].(float64); ok {
		sec := int(u)
		d := sec / 86400
		h := (sec % 86400) / 3600
		m := (sec % 3600) / 60
		uptimeStr = fmt.Sprintf("%d d, %02d h, %02d min", d, h, m)
	}
	if lt, ok := info["localtime"].(float64); ok {
		t := time.Unix(int64(lt), 0)
		localTimeStr = t.Format("2006-01-02 15:04:05")
	}
	if arr, ok := info["load"].([]any); ok && len(arr) >= 3 {
		toF := func(x any) float64 { f, _ := x.(float64); return f / 1000.0 }
		loadStr = fmt.Sprintf("%.2f %.2f %.2f", toF(arr[0]), toF(arr[1]), toF(arr[2]))
	}

	return SysInfo{
		Hostname:        hostname,
		Model:           model,
		Architecture:    arch,
		Target:          target,
		FirmwareVersion: strings.TrimSpace(customFirmwareVersion),
		KernelVersion:   kernel,
		LocalTime:       localTimeStr,
		Uptime:          uptimeStr,
		LoadAverage:     loadStr,
	}
}

func architectureFromBoard(board map[string]any) string {
	for _, key := range []string{"architecture", "system"} {
		if v, ok := board[key].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}

	return ""
}

func readLocalLRAPVersion() string {
	b, err := os.ReadFile(lrapVersionPath)
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(b))
}

func readRemoteLRAPVersion(ip string) string {

	res, err := ubusCallJSONAt(ip, AnonSID, "file", "read", map[string]any{
		"path": lrapVersionPath,
	})
	if err != nil {
		return ""
	}

	if data, ok := res["data"].(string); ok {
		return strings.TrimSpace(data)
	}

	return ""
}

// GET /api/status/overview/system[?ip=10.10.18.X]
func systemOverviewHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	si, err := getSysInfoViaUbus(r)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(si)
}
