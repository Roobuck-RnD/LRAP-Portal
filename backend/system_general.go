package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------- 数据结构 ----------

type SystemConfigResp struct {
	Hostname  string `json:"hostname"`
	Timezone  string `json:"timezone"`
	LocalTime string `json:"localtime"`
}

type SystemConfigReq struct {
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
}

type SystemConfigSaveResp struct {
	Status        string   `json:"status"`
	Message       string   `json:"message,omitempty"`
	SyncedModules int      `json:"synced_modules"`
	Warnings      []string `json:"warnings,omitempty"`
}

type TimezoneEntry struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ---------- 全局时区缓存 ----------

var (
	tzCache     []TimezoneEntry
	tzCacheOnce sync.Once
	tzMap       map[string]string
)

// ---------- Timezone ----------

func loadTimezones() {
	tzMap = make(map[string]string)
	tzCache = []TimezoneEntry{}

	path := "/usr/lib/lua/luci/sys/zoneinfo/tzdata.lua"
	content, err := osReadFileCompat(path)
	if err != nil {
		fmt.Printf("[WARN] Cannot read tzdata.lua: %v. Using fallback.\n", err)

		fallback := []TimezoneEntry{
			{"UTC", "UTC"},
			{"Asia/Shanghai", "CST-8"},
			{"Australia/Sydney", "EST-10EST,M10.1.0,M4.1.0/3"},
		}

		for _, t := range fallback {
			tzMap[t.Label] = t.Value
			tzCache = append(tzCache, t)
		}

		return
	}

	re := regexp.MustCompile(`\{\s*'([^']+)'\s*,\s*'([^']+)'\s*\}`)
	matches := re.FindAllStringSubmatch(string(content), -1)

	for _, m := range matches {
		if len(m) == 3 {
			label := m[1]
			value := m[2]

			tzCache = append(tzCache, TimezoneEntry{
				Label: label,
				Value: value,
			})
			tzMap[label] = value
		}
	}

	sort.Slice(tzCache, func(i, j int) bool {
		return tzCache[i].Label < tzCache[j].Label
	})
}

// osReadFileCompat avoids depending on ioutil in newer code.
// If your Go version is old and os.ReadFile is unavailable, replace this with ioutil.ReadFile.
func osReadFileCompat(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func resolveTimezoneValues(zonename string) (string, string) {
	tzCacheOnce.Do(loadTimezones)

	zonename = strings.TrimSpace(zonename)
	if zonename == "" {
		zonename = "UTC"
	}

	timezonePosix := "UTC"

	if val, ok := tzMap[zonename]; ok {
		timezonePosix = val
	} else {
		timezonePosix = zonename
	}

	return zonename, timezonePosix
}

// ---------- SID / Caller ----------

func systemLocalSIDFromRequest(r *http.Request) (string, error) {
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

// ---------- GET AC only ----------

func getSystemGeneral(r *http.Request) (SystemConfigResp, error) {
	sid, err := systemLocalSIDFromRequest(r)
	if err != nil {
		return SystemConfigResp{}, err
	}

	infoRes, err := ubusCallJSONLocal(sid, "system", "info", nil)

	localtime := "Unknown"
	if err == nil {
		if lt, ok := infoRes["localtime"].(float64); ok {
			t := time.Unix(int64(lt), 0)
			localtime = t.Format("2006-01-02 15:04:05")
		}
	}

	uciRes, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config":  "system",
		"section": "@system[0]",
	})

	hostname := ""
	timezoneName := "UTC"

	if err == nil {
		if values, ok := uciRes["values"].(map[string]any); ok {
			if h, ok := values["hostname"].(string); ok {
				hostname = h
			}

			if z, ok := values["zonename"].(string); ok && z != "" {
				timezoneName = z
			} else if z, ok := values["timezone"].(string); ok && z != "" {
				timezoneName = z
			}
		}
	} else {
		fmt.Printf("[Error] Get system config failed for AC: %v\n", err)
	}

	return SystemConfigResp{
		Hostname:  hostname,
		Timezone:  timezoneName,
		LocalTime: localtime,
	}, nil
}

// ---------- SET AC + sync AP timezone ----------

func setSystemGeneral(r *http.Request, req SystemConfigReq) (SystemConfigSaveResp, error) {
	sid, err := systemLocalSIDFromRequest(r)
	if err != nil {
		return SystemConfigSaveResp{}, err
	}

	req.Hostname = strings.TrimSpace(req.Hostname)
	req.Timezone = strings.TrimSpace(req.Timezone)

	zonename, timezonePosix := resolveTimezoneValues(req.Timezone)

	// 1. AC: set hostname + timezone
	acValues := map[string]string{
		"hostname": req.Hostname,
		"zonename": zonename,
		"timezone": timezonePosix,
	}

	if _, err := ubusCallJSONLocal(sid, "uci", "set", map[string]any{
		"config":  "system",
		"section": "@system[0]",
		"values":  acValues,
	}); err != nil {
		return SystemConfigSaveResp{}, fmt.Errorf("AC uci set failed: %v", err)
	}

	if _, err := ubusCallJSONLocal(sid, "uci", "commit", map[string]any{
		"config": "system",
	}); err != nil {
		return SystemConfigSaveResp{}, fmt.Errorf("AC uci commit failed: %v", err)
	}

	notifySystemConfigChangeLocal(sid)

	// uci commit 不会把 hostname 应用到运行系统,必须让 system init 重载才会写入
	// 内核运行时 hostname(否则新名字要等重启才生效,UI 读的正是运行时 hostname)。
	applyLocalHostname()

	// 2. APs: sync timezone only, keep AP hostnames unchanged
	apIPs := APManagementIPs()
	warnings := make([]string, 0)
	synced := 0

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range apIPs {
		targetIP := ip

		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := syncTimezoneToAP(targetIP, zonename, timezonePosix); err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s: %v", targetIP, err))
				mu.Unlock()
				return
			}

			mu.Lock()
			synced++
			mu.Unlock()
		}()
	}

	wg.Wait()

	return SystemConfigSaveResp{
		Status:        "ok",
		Message:       "AC saved and timezone sync attempted for AP modules",
		SyncedModules: synced,
		Warnings:      warnings,
	}, nil
}

func syncTimezoneToAP(ip string, zonename string, timezonePosix string) error {

	values := map[string]string{
		"zonename": zonename,
		"timezone": timezonePosix,
	}

	if _, err := ubusCallJSONAt(ip, AnonSID, "uci", "set", map[string]any{
		"config":  "system",
		"section": "@system[0]",
		"values":  values,
	}); err != nil {
		return fmt.Errorf("uci set failed: %v", err)
	}

	if _, err := ubusCallJSONAt(ip, AnonSID, "uci", "commit", map[string]any{
		"config": "system",
	}); err != nil {
		return fmt.Errorf("uci commit failed: %v", err)
	}

	notifySystemConfigChangeRemote(ip, AnonSID)

	return nil
}

func notifySystemConfigChangeLocal(sid string) {
	_, _ = ubusCallJSONLocal(sid, "service", "event", map[string]any{
		"type": "config.change",
		"data": map[string]any{
			"package": "system",
		},
	})
}

func notifySystemConfigChangeRemote(ip string, sid string) {
	_, _ = ubusCallJSONAt(ip, sid, "service", "event", map[string]any{
		"type": "config.change",
		"data": map[string]any{
			"package": "system",
		},
	})
}

// applyLocalHostname 把已提交的 hostname 应用到运行中的系统。`/etc/init.d/system
// reload` 会从 UCI 重新应用 hostname(及相关 /proc/sys 设置);仅 uci commit 不会,
// 那样新 hostname 要等重启才生效。fire-and-forget:失败只记日志,不影响保存结果。
func applyLocalHostname() {
	if out, err := exec.Command("/etc/init.d/system", "reload").CombinedOutput(); err != nil {
		log.Printf("applyLocalHostname: /etc/init.d/system reload failed: %v, output: %s", err, string(out))
	}
}

// ---------- Timezones Handler ----------

func timezonesHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	tzCacheOnce.Do(loadTimezones)

	_ = json.NewEncoder(w).Encode(tzCache)
}

// ---------- Handler ----------

func systemGeneralHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	switch r.Method {
	case http.MethodGet:
		data, err := getSystemGeneral(r)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(data)

	case http.MethodPost:
		var req SystemConfigReq

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		result, err := setSystemGeneral(r, req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(result)

	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}