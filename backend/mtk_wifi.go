package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------- 配置常量 ----------

const (
	Mtk24GPath = "/etc/wireless/mediatek/mt7981.dbdc.b0.dat"
	Mtk5GPath  = "/etc/wireless/mediatek/mt7981.dbdc.b1.dat"

	// The AC/main module is the single source of truth for desired runtime WiFi state.
	// APs do not keep their own state file. The AC reads this file every 5 seconds
	// and applies the desired ra0/rax0 up/down state to itself and all reachable APs.
	MtkWifiStatePath = "/etc/roobuck/wifi_state.json"

	Mtk24GIfName = "ra0"
	Mtk5GIfName  = "rax0"

	MtkWifiStateEnforceInterval = 5 * time.Second
)

type mtkBand string

const (
	mtkBand24G mtkBand = "2g"
	mtkBand5G  mtkBand = "5g"
)

// ---------- 数据结构 ----------

type MtkWifiInfo struct {
	SSID24  string               `json:"ssid_2g"`
	Pass24  string               `json:"pass_2g"`
	SSID5   string               `json:"ssid_5g"`
	Pass5   string               `json:"pass_5g"`
	Modules []MtkWifiModuleRadio `json:"modules,omitempty"`
}

type MtkWifiModuleRadio struct {
	Name   string `json:"name"`
	Type   string `json:"type"` // main / ap
	IP     string `json:"ip"`
	Online bool   `json:"online"`

	Channel24      string `json:"channel_2g"`
	ChannelWidth24 string `json:"channel_width_2g"`
	TxPower24      int    `json:"tx_power_2g"`

	Channel5      string `json:"channel_5g"`
	ChannelWidth5 string `json:"channel_width_5g"`
	TxPower5      int    `json:"tx_power_5g"`

	// Desired/persistent runtime state saved in /etc/roobuck/wifi_state.json.
	Radio24Enabled bool `json:"radio_enabled_2g"`
	Radio5Enabled  bool `json:"radio_enabled_5g"`

	// Current interface state. This is useful because wifi restart/reboot can
	// temporarily bring a disabled interface back until the enforcer runs.
	Radio24Running bool `json:"radio_running_2g"`
	Radio5Running  bool `json:"radio_running_5g"`
}

type MtkWifiSyncResult struct {
	Target string `json:"target"`
	IP     string `json:"ip"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

type MtkWifiSaveResponse struct {
	Status  string              `json:"status"`
	Results []MtkWifiSyncResult `json:"results"`
}

type MtkWifiRadioToggleRequest struct {
	Name    string `json:"name"`
	Type    string `json:"type"` // main / ap
	IP      string `json:"ip"`
	Band    string `json:"band"` // 2g / 5g
	Enabled bool   `json:"enabled"`
}

type MtkWifiRadioToggleResponse struct {
	Status  string                `json:"status"`
	Result  MtkWifiSyncResult     `json:"result"`
	State   mtkRadioState         `json:"state"`
	Runtime mtkRadioRuntimeStatus `json:"runtime"`
}

type mtkRadioState struct {
	Radio24Enabled bool  `json:"radio_2g_enabled"`
	Radio5Enabled  bool  `json:"radio_5g_enabled"`
	UpdatedAt      int64 `json:"updated_at"`
}

type mtkCentralWifiState struct {
	Version   int                      `json:"version"`
	Modules   map[string]mtkRadioState `json:"modules"`
	UpdatedAt int64                    `json:"updated_at"`
}

type mtkRadioRuntimeStatus struct {
	Radio24Running bool `json:"radio_2g_running"`
	Radio5Running  bool `json:"radio_5g_running"`
}

type mtkDatConfig struct {
	SSID              string
	Pass              string
	Channel           string
	AutoChannelSelect string
	HTBW              string
	HTBSSCoexist      string
	VHTBW             string
	ChannelWidth      string
	TxPower           int
	PercentageEnable  string
}

var mtkMutex sync.Mutex
var mtkStateEnforcerOnce sync.Once

func init() {
	startMtkWifiStateEnforcer()
}

// ---------- HTTP Handler ----------

func mtkWifiHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// CORS 与 OPTIONS 预检由全局 withCORS 中间件统一处理(尊重 CORS_ORIGIN 配置),
	// 这里不再自设,避免覆盖配置。

	if r.Header.Get("Authorization") == "" {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		info, err := getMtkWifiLogic()
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(info)

	case http.MethodPost:
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, `{"error":"bad body"}`, http.StatusBadRequest)
			return
		}

		var probe struct {
			Action string `json:"action"`
		}
		_ = json.Unmarshal(body, &probe)

		// Radio enable/disable now also uses POST to avoid PATCH CORS issues.
		// Frontend sends: {"action":"radio_state", "type":"ap", "ip":"...", "band":"5g", "enabled":false}
		if strings.TrimSpace(probe.Action) == "radio_state" {
			var req MtkWifiRadioToggleRequest
			if err := json.Unmarshal(body, &req); err != nil {
				http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
				return
			}

			resp, err := setMtkWifiRadioStateLogic(req)
			if err != nil {
				http.Error(w, fmt.Sprintf(`{"error":"radio state change failed: %v"}`, err), http.StatusInternalServerError)
				return
			}

			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(resp)
			return
		}

		var req MtkWifiInfo
		if err := json.Unmarshal(body, &req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		resp, err := setMtkWifiLogic(req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)

	case http.MethodPatch:
		// Kept for direct API callers. The React frontend does not use PATCH anymore.
		var req MtkWifiRadioToggleRequest

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		resp, err := setMtkWifiRadioStateLogic(req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"radio state change failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ---------- 读取配置 ----------

func getMtkWifiLogic() (MtkWifiInfo, error) {
	var info MtkWifiInfo

	local24Content, err := os.ReadFile(Mtk24GPath)
	if err != nil {
		return info, fmt.Errorf("local 2.4g read failed: %v", err)
	}

	local5Content, err := os.ReadFile(Mtk5GPath)
	if err != nil {
		return info, fmt.Errorf("local 5g read failed: %v", err)
	}

	local24Dat := parseMtkDatContent(string(local24Content), mtkBand24G)
	local5Dat := parseMtkDatContent(string(local5Content), mtkBand5G)

	info.SSID24 = local24Dat.SSID
	info.Pass24 = local24Dat.Pass
	info.SSID5 = local5Dat.SSID
	info.Pass5 = local5Dat.Pass

	centralState := readLocalMtkCentralWifiStateDefault()
	localRadioState := getMtkRadioStateForModule(centralState, mtkStateMainKey)
	localRuntime := getLocalMtkRadioRuntimeStatus()

	info.Modules = append(info.Modules, MtkWifiModuleRadio{
		Name:   hostnameFromLocalOrFallback(),
		Type:   "main",
		IP:     "",
		Online: true,

		Channel24:      local24Dat.Channel,
		ChannelWidth24: local24Dat.ChannelWidth,
		TxPower24:      local24Dat.TxPower,

		Channel5:      local5Dat.Channel,
		ChannelWidth5: local5Dat.ChannelWidth,
		TxPower5:      local5Dat.TxPower,

		Radio24Enabled: localRadioState.Radio24Enabled,
		Radio5Enabled:  localRadioState.Radio5Enabled,
		Radio24Running: localRuntime.Radio24Running,
		Radio5Running:  localRuntime.Radio5Running,
	})

	apIPs := APManagementIPs()

	var wg sync.WaitGroup
	var mu sync.Mutex

	for _, ip := range apIPs {
		ip := ip

		wg.Add(1)

		go func() {
			defer wg.Done()

			if !mtkPingOnce(ip) {
				return
			}

			name := mtkRemoteHostname(ip)
			if name == "" {
				name = "RoobuckAP"
			}

			dat24 := defaultMtkDatConfig(mtkBand24G)
			dat5 := defaultMtkDatConfig(mtkBand5G)

			content24, err24 := ubusFileReadRemote(ip, Mtk24GPath)
			content5, err5 := ubusFileReadRemote(ip, Mtk5GPath)

			radioState := getMtkRadioStateForModule(centralState, ip)
			runtimeState := getRemoteMtkRadioRuntimeStatus(ip)

			online := true

			if err24 != nil {
				online = false
			} else {
				dat24 = parseMtkDatContent(content24, mtkBand24G)
			}

			if err5 != nil {
				online = false
			} else {
				dat5 = parseMtkDatContent(content5, mtkBand5G)
			}

			mu.Lock()
			info.Modules = append(info.Modules, MtkWifiModuleRadio{
				Name:   name,
				Type:   "ap",
				IP:     ip,
				Online: online,

				Channel24:      dat24.Channel,
				ChannelWidth24: dat24.ChannelWidth,
				TxPower24:      dat24.TxPower,

				Channel5:      dat5.Channel,
				ChannelWidth5: dat5.ChannelWidth,
				TxPower5:      dat5.TxPower,

				Radio24Enabled: radioState.Radio24Enabled,
				Radio5Enabled:  radioState.Radio5Enabled,
				Radio24Running: runtimeState.Radio24Running,
				Radio5Running:  runtimeState.Radio5Running,
			})
			mu.Unlock()
		}()
	}

	wg.Wait()

	sortMtkModules(info.Modules)

	return info, nil
}

// ---------- 保存配置 ----------

func setMtkWifiLogic(req MtkWifiInfo) (MtkWifiSaveResponse, error) {
	mtkMutex.Lock()
	defer mtkMutex.Unlock()

	req.SSID24 = strings.TrimSpace(req.SSID24)
	req.SSID5 = strings.TrimSpace(req.SSID5)

	normalizeMtkWifiRequest(&req)

	if err := validateMtkWifiRequest(req); err != nil {
		return MtkWifiSaveResponse{}, err
	}

	results := make([]MtkWifiSyncResult, 0, len(req.Modules))
	needApplyLocal := false

	for _, mod := range req.Modules {
		target := mod.Name
		if target == "" {
			if mod.Type == "main" {
				target = "Main Module"
			} else {
				target = "AP"
			}
		}

		if mod.Type == "main" || mod.IP == "" {
			err := updateLocalMtkWifi(req, mod)
			if err == nil {
				needApplyLocal = true
			}

			results = append(results, MtkWifiSyncResult{
				Target: target,
				IP:     "",
				OK:     err == nil,
				Error:  errString(err),
			})

			continue
		}

		if !mtkPingOnce(mod.IP) {
			results = append(results, MtkWifiSyncResult{
				Target: target,
				IP:     mod.IP,
				OK:     false,
				Error:  "module unreachable",
			})
			continue
		}

		err := updateRemoteMtkWifi(mod.IP, req, mod)

		results = append(results, MtkWifiSyncResult{
			Target: target,
			IP:     mod.IP,
			OK:     err == nil,
			Error:  errString(err),
		})
	}

	if needApplyLocal {
		applyLocalMtkWifiAsync()
	}

	return MtkWifiSaveResponse{
		Status:  "ok",
		Results: results,
	}, nil
}

func updateLocalMtkWifi(req MtkWifiInfo, mod MtkWifiModuleRadio) error {
	updates24 := buildMtkDatUpdates(req.SSID24, req.Pass24, mod.Channel24, mod.ChannelWidth24, mod.TxPower24, mtkBand24G)
	if err := updateLocalMtkDatFile(Mtk24GPath, updates24); err != nil {
		return fmt.Errorf("update local 2.4g failed: %v", err)
	}

	updates5 := buildMtkDatUpdates(req.SSID5, req.Pass5, mod.Channel5, mod.ChannelWidth5, mod.TxPower5, mtkBand5G)
	if err := updateLocalMtkDatFile(Mtk5GPath, updates5); err != nil {
		return fmt.Errorf("update local 5g failed: %v", err)
	}

	return nil
}

func updateRemoteMtkWifi(ip string, req MtkWifiInfo, mod MtkWifiModuleRadio) error {
	updates24 := buildMtkDatUpdates(req.SSID24, req.Pass24, mod.Channel24, mod.ChannelWidth24, mod.TxPower24, mtkBand24G)
	if err := updateRemoteMtkDatFile(ip, Mtk24GPath, updates24); err != nil {
		return fmt.Errorf("update remote %s 2.4g failed: %v", ip, err)
	}

	updates5 := buildMtkDatUpdates(req.SSID5, req.Pass5, mod.Channel5, mod.ChannelWidth5, mod.TxPower5, mtkBand5G)
	if err := updateRemoteMtkDatFile(ip, Mtk5GPath, updates5); err != nil {
		return fmt.Errorf("update remote %s 5g failed: %v", ip, err)
	}

	if err := verifyRemoteMtkDat(ip, Mtk24GPath, req.SSID24, mod.Channel24, mod.ChannelWidth24, mod.TxPower24, mtkBand24G); err != nil {
		return fmt.Errorf("verify remote %s 2.4g failed: %v", ip, err)
	}

	if err := verifyRemoteMtkDat(ip, Mtk5GPath, req.SSID5, mod.Channel5, mod.ChannelWidth5, mod.TxPower5, mtkBand5G); err != nil {
		return fmt.Errorf("verify remote %s 5g failed: %v", ip, err)
	}

	applyRemoteMtkWifiAsync(ip)

	return nil
}

func buildMtkDatUpdates(ssid string, pass string, channel string, width string, txPower int, band mtkBand) map[string]string {
	var htBW string
	var coexist string
	var vhtBW string

	if band == mtkBand5G {
		htBW, coexist, vhtBW = widthToMtkDat5G(width)
	} else {
		htBW, coexist, vhtBW = widthToMtkDat24G(width)
	}

	channel = strings.TrimSpace(channel)

	autoChannelSelect := "0"
	if channel == "0" {
		autoChannelSelect = "3"
	}

	percentageEnable := "0"
	if txPower < 100 {
		percentageEnable = "1"
	}

	return map[string]string{
		"SSID1":             ssid,
		"WPAPSK1":           pass,
		"Channel":           channel,
		"AutoChannelSelect": autoChannelSelect,
		"HT_BW":             htBW,
		"HT_BSSCoexistence": coexist,
		"VHT_BW":            vhtBW,
		"TxPower":           strconv.Itoa(txPower),
		"PERCENTAGEenable":  percentageEnable,
	}
}

// ---------- MTK WiFi Apply ----------
//
// LuCI 的 controller 里使用：
// mtkwifi.__run_in_child_env(__mtkwifi_reload, devname)
//
// __mtkwifi_reload 内部根据 diff 调用：
// wifi restart <devname>
// 或：
// wifi reload <devname>
//
// 这里仍然不复刻 Lua diff 逻辑，直接使用更强的 wifi restart。
// 因为 Channel / Bandwidth 变化通常需要 restart，而不是简单 reload。

func applyLocalMtkWifiAsync() {
	go func() {
		time.Sleep(1 * time.Second)
		fmt.Println("Applying local MTK WiFi settings: /sbin/wifi restart...")

		cmd := exec.Command("sh", "-c", mtkLocalApplyWifiCommand())
		out, err := cmd.CombinedOutput()
		if err != nil {
			fmt.Printf("local MTK wifi apply failed: %v output=%s\n", err, string(out))
		}

		// wifi restart can bring disabled radios back up. Re-apply the central
		// desired runtime state after restart finishes.
		time.Sleep(3 * time.Second)
		if err := enforceLocalMtkWifiRadioStateFromCentral(); err != nil {
			fmt.Printf("local MTK wifi state re-apply failed: %v\n", err)
		}
	}()
}

func applyRemoteMtkWifiAsync(ip string) {
	go func() {

		state := getMtkRadioStateForModule(readLocalMtkCentralWifiStateDefault(), ip)

		_, _ = ubusCallJSONAt(ip, AnonSID, "file", "exec", map[string]any{
			"command": "sh",
			"params":  []string{"-c", mtkRemoteApplyWifiCommand(state)},
		})
	}()
}

func mtkLocalApplyWifiCommand() string {
	return `
(
	sleep 1

	if [ -x /sbin/wifi ]; then
		/sbin/wifi restart
		exit $?
	fi

	if [ -x /etc/init.d/network ]; then
		/etc/init.d/network restart
		exit $?
	fi

	exit 1
)
`
}

func mtkRemoteApplyWifiCommand(state mtkRadioState) string {
	return `
(
	sleep 1

	if [ -x /sbin/wifi ]; then
		/sbin/wifi restart
	elif [ -x /etc/init.d/network ]; then
		/etc/init.d/network restart
	else
		exit 1
	fi

	# wifi restart can bring disabled radios back up. Re-apply desired state
	# from the AC/main module state file. APs do not store their own state file.
	sleep 3
	` + mtkRemoteApplyRadioStateCommand(state) + `
) >/dev/null 2>&1 &
`
}

// ---------- 持久化运行时 WiFi 状态 ----------
//
// 这个工厂固件中，LuCI 的 enable/disable 行为是运行时 ifconfig ra0/rax0 up/down。
// /sbin/wifi restart 会再次把 ra0/rax0 拉起来，且 RadioOn=0 不会阻止它。
// 所以这里只在 AC/main 模块保存一个总状态文件，并由 AC 后端每 5 秒重新应用：
//   /etc/roobuck/wifi_state.json
//
// AP 不保存状态文件。AP 只接收 AC 下发的 ifconfig ra0/rax0 up/down 命令。

const mtkStateMainKey = "main"

func startMtkWifiStateEnforcer() {
	mtkStateEnforcerOnce.Do(func() {
		go func() {
			// Give network/interfaces time to come up during boot.
			time.Sleep(8 * time.Second)

			for {
				enforceAllMtkWifiRadioStates()
				time.Sleep(MtkWifiStateEnforceInterval)
			}
		}()
	})
}

func enforceAllMtkWifiRadioStates() {
	centralState := readLocalMtkCentralWifiStateDefault()

	if err := applyLocalMtkRadioState(getMtkRadioStateForModule(centralState, mtkStateMainKey)); err != nil {
		fmt.Printf("local MTK radio state enforce failed: %v\n", err)
	}

	var wg sync.WaitGroup
	for _, ip := range APManagementIPs() {
		ip := ip
		state := getMtkRadioStateForModule(centralState, ip)

		wg.Add(1)
		go func() {
			defer wg.Done()

			if !mtkPingOnce(ip) {
				return
			}

			if err := applyRemoteMtkRadioState(ip, state); err != nil {
				fmt.Printf("remote MTK radio state enforce failed on %s: %v\n", ip, err)
			}
		}()
	}

	wg.Wait()
}

func enforceLocalMtkWifiRadioStateFromCentral() error {
	centralState := readLocalMtkCentralWifiStateDefault()
	return applyLocalMtkRadioState(getMtkRadioStateForModule(centralState, mtkStateMainKey))
}

func setMtkWifiRadioStateLogic(req MtkWifiRadioToggleRequest) (MtkWifiRadioToggleResponse, error) {
	mtkMutex.Lock()
	defer mtkMutex.Unlock()

	req.Type = strings.TrimSpace(req.Type)
	req.IP = strings.TrimSpace(req.IP)
	req.Band = strings.ToLower(strings.TrimSpace(req.Band))

	if req.Band != string(mtkBand24G) && req.Band != string(mtkBand5G) {
		return MtkWifiRadioToggleResponse{}, fmt.Errorf("invalid band: %s", req.Band)
	}

	key := mtkModuleStateKey(req.Type, req.IP)
	target := strings.TrimSpace(req.Name)
	if target == "" {
		if key == mtkStateMainKey {
			target = "Main Module"
		} else {
			target = "AP"
		}
	}

	centralState, err := readLocalMtkCentralWifiState()
	if err != nil {
		centralState = defaultMtkCentralWifiState()
	}

	moduleState := getMtkRadioStateForModule(centralState, key)
	moduleState = setBandInMtkRadioState(moduleState, req.Band, req.Enabled)
	setMtkRadioStateForModule(&centralState, key, moduleState)

	if err := writeLocalMtkCentralWifiState(centralState); err != nil {
		return MtkWifiRadioToggleResponse{}, err
	}

	if key == mtkStateMainKey {
		err := applyLocalMtkRadioState(moduleState)
		runtime := getLocalMtkRadioRuntimeStatus()

		return MtkWifiRadioToggleResponse{
			Status: "ok",
			Result: MtkWifiSyncResult{
				Target: target,
				IP:     "",
				OK:     err == nil,
				Error:  errString(err),
			},
			State:   moduleState,
			Runtime: runtime,
		}, nil
	}

	if !mtkPingOnce(req.IP) {
		return MtkWifiRadioToggleResponse{
			Status: "ok",
			Result: MtkWifiSyncResult{
				Target: target,
				IP:     req.IP,
				OK:     false,
				Error:  "state saved on main module, but module unreachable",
			},
			State:   moduleState,
			Runtime: mtkRadioRuntimeStatus{},
		}, nil
	}

	err = applyRemoteMtkRadioState(req.IP, moduleState)
	runtime := getRemoteMtkRadioRuntimeStatus(req.IP)

	return MtkWifiRadioToggleResponse{
		Status: "ok",
		Result: MtkWifiSyncResult{
			Target: target,
			IP:     req.IP,
			OK:     err == nil,
			Error:  errString(err),
		},
		State:   moduleState,
		Runtime: runtime,
	}, nil
}

func defaultMtkRadioState() mtkRadioState {
	return mtkRadioState{
		Radio24Enabled: true,
		Radio5Enabled:  true,
		UpdatedAt:      time.Now().Unix(),
	}
}

func defaultMtkCentralWifiState() mtkCentralWifiState {
	now := time.Now().Unix()
	state := mtkCentralWifiState{
		Version:   1,
		Modules:   map[string]mtkRadioState{},
		UpdatedAt: now,
	}

	state.Modules[mtkStateMainKey] = mtkRadioState{
		Radio24Enabled: true,
		Radio5Enabled:  true,
		UpdatedAt:      now,
	}

	for _, ip := range APManagementIPs() {
		state.Modules[ip] = mtkRadioState{
			Radio24Enabled: true,
			Radio5Enabled:  true,
			UpdatedAt:      now,
		}
	}

	return state
}

func setBandInMtkRadioState(state mtkRadioState, band string, enabled bool) mtkRadioState {
	if band == string(mtkBand24G) {
		state.Radio24Enabled = enabled
	} else {
		state.Radio5Enabled = enabled
	}

	state.UpdatedAt = time.Now().Unix()
	return state
}

func mtkModuleStateKey(moduleType string, ip string) string {
	moduleType = strings.TrimSpace(moduleType)
	ip = strings.TrimSpace(ip)

	if moduleType == "main" || ip == "" {
		return mtkStateMainKey
	}

	return ip
}

func readLocalMtkCentralWifiStateDefault() mtkCentralWifiState {
	state, err := readLocalMtkCentralWifiState()
	if err != nil {
		return defaultMtkCentralWifiState()
	}

	return state
}

func readLocalMtkCentralWifiState() (mtkCentralWifiState, error) {
	state := defaultMtkCentralWifiState()

	b, err := os.ReadFile(MtkWifiStatePath)
	if err != nil {
		return state, err
	}

	if err := json.Unmarshal(b, &state); err != nil {
		return defaultMtkCentralWifiState(), err
	}

	return sanitizeMtkCentralWifiState(state), nil
}

func writeLocalMtkCentralWifiState(state mtkCentralWifiState) error {
	if err := os.MkdirAll("/etc/roobuck", 0755); err != nil {
		return err
	}

	state = sanitizeMtkCentralWifiState(state)
	state.UpdatedAt = time.Now().Unix()

	b, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(MtkWifiStatePath, append(b, '\n'), 0644)
}

func sanitizeMtkCentralWifiState(state mtkCentralWifiState) mtkCentralWifiState {
	if state.Version == 0 {
		state.Version = 1
	}

	if state.Modules == nil {
		state.Modules = map[string]mtkRadioState{}
	}

	// Make sure all managed modules have a state. Missing entries default to enabled.
	if _, ok := state.Modules[mtkStateMainKey]; !ok {
		state.Modules[mtkStateMainKey] = defaultMtkRadioState()
	}

	for _, ip := range APManagementIPs() {
		if _, ok := state.Modules[ip]; !ok {
			state.Modules[ip] = defaultMtkRadioState()
		}
	}

	if state.UpdatedAt < 0 {
		state.UpdatedAt = 0
	}

	return state
}

func getMtkRadioStateForModule(state mtkCentralWifiState, key string) mtkRadioState {
	state = sanitizeMtkCentralWifiState(state)
	key = strings.TrimSpace(key)
	if key == "" {
		key = mtkStateMainKey
	}

	moduleState, ok := state.Modules[key]
	if !ok {
		return defaultMtkRadioState()
	}

	if moduleState.UpdatedAt < 0 {
		moduleState.UpdatedAt = 0
	}

	return moduleState
}

func setMtkRadioStateForModule(state *mtkCentralWifiState, key string, moduleState mtkRadioState) {
	if state.Modules == nil {
		state.Modules = map[string]mtkRadioState{}
	}

	key = strings.TrimSpace(key)
	if key == "" {
		key = mtkStateMainKey
	}

	if moduleState.UpdatedAt == 0 {
		moduleState.UpdatedAt = time.Now().Unix()
	}

	state.Modules[key] = moduleState
	state.UpdatedAt = time.Now().Unix()
}

func applyLocalMtkRadioState(state mtkRadioState) error {
	var errs []string

	if err := setLocalMtkInterfaceState(Mtk24GIfName, state.Radio24Enabled); err != nil {
		errs = append(errs, "2.4g: "+err.Error())
	}

	if err := setLocalMtkInterfaceState(Mtk5GIfName, state.Radio5Enabled); err != nil {
		errs = append(errs, "5g: "+err.Error())
	}

	if len(errs) > 0 {
		return fmt.Errorf(strings.Join(errs, "; "))
	}

	return nil
}

func setLocalMtkInterfaceState(ifname string, enabled bool) error {
	action := "down"
	if enabled {
		action = "up"
	}

	cmd := exec.Command("sh", "-c", fmt.Sprintf("ifconfig %s %s >/dev/null 2>&1 || true", shellArg(ifname), action))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s failed: %v output=%s", ifname, action, err, string(out))
	}

	return nil
}

func getLocalMtkRadioRuntimeStatus() mtkRadioRuntimeStatus {
	return mtkRadioRuntimeStatus{
		Radio24Running: localMtkInterfaceIsRunning(Mtk24GIfName),
		Radio5Running:  localMtkInterfaceIsRunning(Mtk5GIfName),
	}
}

func localMtkInterfaceIsRunning(ifname string) bool {
	out, err := exec.Command("sh", "-c", fmt.Sprintf("ifconfig %s 2>/dev/null", shellArg(ifname))).CombinedOutput()
	if err != nil {
		return false
	}

	return strings.Contains(string(out), "UP")
}

func applyRemoteMtkRadioState(ip string, state mtkRadioState) error {
	_, err := ubusExecRemoteChecked(ip, mtkRemoteApplyRadioStateCommand(state))
	return err
}

func getRemoteMtkRadioRuntimeStatus(ip string) mtkRadioRuntimeStatus {

	cmd := `
r2=0
r5=0
if ifconfig ra0 2>/dev/null | grep -q UP; then r2=1; fi
if ifconfig rax0 2>/dev/null | grep -q UP; then r5=1; fi
echo "$r2 $r5"
`

	res, err := ubusCallJSONAt(ip, AnonSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", cmd},
	})
	if err != nil {
		return mtkRadioRuntimeStatus{}
	}

	stdout := strings.TrimSpace(stringFromAny(res["stdout"]))
	parts := strings.Fields(stdout)

	return mtkRadioRuntimeStatus{
		Radio24Running: len(parts) > 0 && parts[0] == "1",
		Radio5Running:  len(parts) > 1 && parts[1] == "1",
	}
}

func mtkRemoteApplyRadioStateCommand(state mtkRadioState) string {
	radio2g := "false"
	if state.Radio24Enabled {
		radio2g = "true"
	}

	radio5g := "false"
	if state.Radio5Enabled {
		radio5g = "true"
	}

	return `
radio2g='` + radio2g + `'
radio5g='` + radio5g + `'

if [ "$radio2g" = "true" ]; then
	ifconfig ` + Mtk24GIfName + ` up >/dev/null 2>&1 || true
else
	ifconfig ` + Mtk24GIfName + ` down >/dev/null 2>&1 || true
fi

if [ "$radio5g" = "true" ]; then
	ifconfig ` + Mtk5GIfName + ` up >/dev/null 2>&1 || true
else
	ifconfig ` + Mtk5GIfName + ` down >/dev/null 2>&1 || true
fi
`
}

func shellArg(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

// ---------- dat 解析 / 更新 ----------

func defaultMtkDatConfig(band mtkBand) mtkDatConfig {
	cfg := mtkDatConfig{
		SSID:              "",
		Pass:              "",
		Channel:           "0",
		AutoChannelSelect: "3",
		HTBW:              "1",
		HTBSSCoexist:      "0",
		VHTBW:             "0",
		ChannelWidth:      "40",
		TxPower:           100,
		PercentageEnable:  "0",
	}

	if band == mtkBand5G {
		cfg.ChannelWidth = "80"
		cfg.VHTBW = "1"
	}

	return cfg
}

func parseMtkDatContent(content string, band mtkBand) mtkDatConfig {
	cfg := defaultMtkDatConfig(band)

	lines := strings.Split(content, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)

		switch key {
		case "SSID1":
			cfg.SSID = val
		case "WPAPSK1":
			cfg.Pass = val
		case "Channel":
			if band == mtkBand5G {
				cfg.Channel = normalizeMtkChannel5G(val)
			} else {
				cfg.Channel = normalizeMtkChannel24G(val)
			}
		case "AutoChannelSelect":
			cfg.AutoChannelSelect = val
		case "HT_BW":
			cfg.HTBW = val
		case "HT_BSSCoexistence":
			cfg.HTBSSCoexist = val
		case "VHT_BW":
			cfg.VHTBW = val
		case "TxPower":
			tx, err := strconv.Atoi(val)
			if err == nil && tx >= 1 && tx <= 100 {
				cfg.TxPower = tx
			}
		case "PERCENTAGEenable":
			cfg.PercentageEnable = val
		}
	}

	if strings.TrimSpace(cfg.AutoChannelSelect) != "" && strings.TrimSpace(cfg.AutoChannelSelect) != "0" {
		cfg.Channel = "0"
	}

	if band == mtkBand5G {
		cfg.ChannelWidth = mtkWidthFromDat5G(cfg.HTBW, cfg.VHTBW)
	} else {
		cfg.ChannelWidth = mtkWidthFromDat24G(cfg.HTBW, cfg.HTBSSCoexist)
	}

	return cfg
}

func updateLocalMtkDatFile(path string, updates map[string]string) error {
	contentBytes, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	updated := updateMtkDatContent(string(contentBytes), updates)
	return os.WriteFile(path, []byte(updated), 0644)
}

func updateMtkDatContent(content string, updates map[string]string) string {
	lines := strings.Split(content, "\n")
	found := make(map[string]bool)

	for i, line := range lines {
		trim := strings.TrimSpace(line)

		if strings.HasPrefix(trim, "#") || !strings.Contains(trim, "=") {
			continue
		}

		key, _, ok := strings.Cut(trim, "=")
		if !ok {
			continue
		}

		key = strings.TrimSpace(key)

		if val, exists := updates[key]; exists {
			lines[i] = key + "=" + val
			found[key] = true
		}
	}

	for key, val := range updates {
		if !found[key] {
			lines = append(lines, key+"="+val)
		}
	}

	return strings.Join(lines, "\n")
}

func updateRemoteMtkDatFile(ip string, path string, updates map[string]string) error {
	content, err := ubusFileReadRemote(ip, path)
	if err != nil {
		return fmt.Errorf("read before write failed: %v", err)
	}

	updated := updateMtkDatContent(content, updates)
	if err := ubusFileWriteRemote(ip, path, updated); err != nil {
		return err
	}

	return nil
}

// ---------- 校验与映射 ----------

func normalizeMtkWifiRequest(req *MtkWifiInfo) {
	req.SSID24 = strings.TrimSpace(req.SSID24)
	req.SSID5 = strings.TrimSpace(req.SSID5)

	for i := range req.Modules {
		req.Modules[i].Channel24 = strings.TrimSpace(req.Modules[i].Channel24)
		req.Modules[i].ChannelWidth24 = strings.TrimSpace(req.Modules[i].ChannelWidth24)
		req.Modules[i].Channel5 = strings.TrimSpace(req.Modules[i].Channel5)
		req.Modules[i].ChannelWidth5 = strings.TrimSpace(req.Modules[i].ChannelWidth5)
		req.Modules[i].IP = strings.TrimSpace(req.Modules[i].IP)
		req.Modules[i].Type = strings.TrimSpace(req.Modules[i].Type)
	}
}

func validateMtkWifiRequest(req MtkWifiInfo) error {
	if err := validateSSIDAndPass("2.4g", req.SSID24, req.Pass24); err != nil {
		return err
	}

	if err := validateSSIDAndPass("5g", req.SSID5, req.Pass5); err != nil {
		return err
	}

	if len(req.Modules) == 0 {
		return fmt.Errorf("no module radio settings provided")
	}

	mainCount := 0

	for _, mod := range req.Modules {
		if mod.Type == "main" {
			mainCount++
		}

		name := mod.Name
		if strings.TrimSpace(name) == "" {
			name = "module"
		}

		if !validMtkChannel24G(mod.Channel24) {
			return fmt.Errorf("%s invalid 2.4g channel: %s", name, mod.Channel24)
		}

		if !validMtkWidth24G(mod.ChannelWidth24) {
			return fmt.Errorf("%s invalid 2.4g channel width: %s", name, mod.ChannelWidth24)
		}

		if mod.TxPower24 < 1 || mod.TxPower24 > 100 {
			return fmt.Errorf("%s 2.4g tx power must be between 1 and 100", name)
		}

		if !validMtkChannel5G(mod.Channel5) {
			return fmt.Errorf("%s invalid 5g channel: %s", name, mod.Channel5)
		}

		if !validMtkWidth5G(mod.ChannelWidth5) {
			return fmt.Errorf("%s invalid 5g channel width: %s", name, mod.ChannelWidth5)
		}

		if mod.TxPower5 < 1 || mod.TxPower5 > 100 {
			return fmt.Errorf("%s 5g tx power must be between 1 and 100", name)
		}
	}

	if mainCount == 0 {
		return fmt.Errorf("missing main module radio settings")
	}

	return nil
}

func validateSSIDAndPass(label string, ssid string, pass string) error {
	if ssid == "" {
		return fmt.Errorf("%s ssid cannot be empty", label)
	}

	if len(ssid) > 32 {
		return fmt.Errorf("%s ssid should be 32 characters or less", label)
	}

	if strings.ContainsAny(ssid, "\r\n") || strings.ContainsAny(pass, "\r\n") {
		return fmt.Errorf("%s ssid/password cannot contain newline characters", label)
	}

	if pass != "" && len(pass) < 8 {
		return fmt.Errorf("%s password must be at least 8 characters", label)
	}

	if len(pass) > 63 {
		return fmt.Errorf("%s password should be 63 characters or less", label)
	}

	return nil
}

func normalizeMtkChannel24G(ch string) string {
	ch = strings.TrimSpace(ch)

	if !validMtkChannel24G(ch) {
		return "0"
	}

	return ch
}

func normalizeMtkChannel5G(ch string) string {
	ch = strings.TrimSpace(ch)

	if !validMtkChannel5G(ch) {
		return "0"
	}

	return ch
}

func validMtkChannel24G(ch string) bool {
	switch strings.TrimSpace(ch) {
	case "0", "1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "11":
		return true
	default:
		return false
	}
}

func validMtkChannel5G(ch string) bool {
	switch strings.TrimSpace(ch) {
	case "0",
		"36", "40", "44", "48",
		"52", "56", "60", "64",
		"100", "104", "108", "112", "116",
		"132", "136", "140",
		"149", "153", "157", "161", "165":
		return true
	default:
		return false
	}
}

func validMtkWidth24G(width string) bool {
	switch strings.TrimSpace(width) {
	case "20", "40", "20/40":
		return true
	default:
		return false
	}
}

func validMtkWidth5G(width string) bool {
	switch strings.TrimSpace(width) {
	case "20", "40", "80", "160":
		return true
	default:
		return false
	}
}

func widthToMtkDat24G(width string) (htBW string, coexist string, vhtBW string) {
	switch strings.TrimSpace(width) {
	case "20":
		return "0", "0", "0"
	case "20/40":
		return "1", "1", "0"
	case "40":
		return "1", "0", "0"
	default:
		return "1", "0", "0"
	}
}

func widthToMtkDat5G(width string) (htBW string, coexist string, vhtBW string) {
	switch strings.TrimSpace(width) {
	case "20":
		return "0", "0", "0"
	case "40":
		return "1", "0", "0"
	case "80":
		return "1", "0", "1"
	case "160":
		return "1", "0", "2"
	default:
		return "1", "0", "1"
	}
}

func mtkWidthFromDat24G(htBW string, coexist string) string {
	htBW = strings.TrimSpace(htBW)
	coexist = strings.TrimSpace(coexist)

	if htBW == "0" {
		return "20"
	}

	if htBW == "1" && coexist == "1" {
		return "20/40"
	}

	return "40"
}

func mtkWidthFromDat5G(htBW string, vhtBW string) string {
	htBW = strings.TrimSpace(htBW)
	vhtBW = strings.TrimSpace(vhtBW)

	if htBW == "0" {
		return "20"
	}

	switch vhtBW {
	case "2":
		return "160"
	case "1":
		return "80"
	default:
		return "40"
	}
}

// ---------- 远程读取 ----------

func ubusFileWriteRemote(ip, path, content string) error {
	delimiter := "__MTK_WIFI_CONFIG_EOF__"
	for i := 0; strings.Contains(content, "\n"+delimiter+"\n") || strings.HasSuffix(content, "\n"+delimiter) || strings.HasPrefix(content, delimiter+"\n"); i++ {
		delimiter = fmt.Sprintf("__MTK_WIFI_CONFIG_EOF_%d__", i)
	}

	cmd := fmt.Sprintf(`
path=%s
if [ -z "$path" ]; then
	exit 1
fi

if [ ! -f "$path" ]; then
	exit 1
fi

tmp="${path}.tmp.$$"
cat > "$tmp" <<'%s'
%s
%s
mv "$tmp" "$path"
`, shellSingleQuote(path), delimiter, content, delimiter)

	_, err := ubusExecRemoteChecked(ip, cmd)
	return err
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func ubusExecRemoteChecked(ip string, cmd string) (map[string]any, error) {

	res, err := ubusCallJSONAt(ip, AnonSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", cmd},
	})
	if err != nil {
		return nil, err
	}

	if code, ok := numberFromAny(res["code"]); ok && code != 0 {
		return res, fmt.Errorf("remote command exit code %d, stdout=%q stderr=%q", code, stringFromAny(res["stdout"]), stringFromAny(res["stderr"]))
	}

	return res, nil
}

func verifyRemoteMtkDat(ip string, path string, wantSSID string, wantChannel string, wantWidth string, wantTxPower int, band mtkBand) error {
	content, err := ubusFileReadRemote(ip, path)
	if err != nil {
		return err
	}

	got := parseMtkDatContent(content, band)

	if got.SSID != wantSSID {
		return fmt.Errorf("ssid not updated, got %q", got.SSID)
	}

	if got.Channel != strings.TrimSpace(wantChannel) {
		return fmt.Errorf("channel not updated, got %q", got.Channel)
	}

	if got.ChannelWidth != strings.TrimSpace(wantWidth) {
		return fmt.Errorf("channel width not updated, got %q", got.ChannelWidth)
	}

	if got.TxPower != wantTxPower {
		return fmt.Errorf("tx power not updated, got %d", got.TxPower)
	}

	return nil
}

func numberFromAny(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		if err == nil {
			return int(i), true
		}
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err == nil {
			return i, true
		}
	}

	return 0, false
}

func stringFromAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	if v == nil {
		return ""
	}

	return fmt.Sprint(v)
}

func ubusFileReadRemote(ip, path string) (string, error) {

	res, err := ubusCallJSONAt(ip, AnonSID, "file", "read", map[string]any{
		"path": path,
	})
	if err != nil {
		return "", err
	}

	if data, ok := res["data"].(string); ok {
		return data, nil
	}

	return "", fmt.Errorf("no data field")
}

// ---------- 小工具 ----------

func hostnameFromLocalOrFallback() string {
	// system board 在 AC 的匿名 ACL 下可读,足以拿到真实 hostname;uci get 匿名会
	// 失败(跳过)。旧的 root 空密码登录本就登不上。
	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONLocal(AnonSID, "uci", "get", params)
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hostname, ok2 := values["hostname"].(string); ok2 && hostname != "" {
				return hostname
			}
		}
	}

	board, err := ubusCallJSONLocal(AnonSID, "system", "board", nil)
	if err == nil {
		if hostname, ok := board["hostname"].(string); ok && hostname != "" {
			return hostname
		}
	}

	return "Main Module"
}

func mtkRemoteHostname(ip string) string {

	params := map[string]any{
		"config":  "system",
		"section": "@system[0]",
	}

	res, err := ubusCallJSONAt(ip, AnonSID, "uci", "get", params)
	if err == nil {
		if values, ok := res["values"].(map[string]any); ok {
			if hostname, ok2 := values["hostname"].(string); ok2 && hostname != "" {
				return hostname
			}
		}
	}

	board, err := ubusCallJSONAt(ip, AnonSID, "system", "board", nil)
	if err == nil {
		if hostname, ok := board["hostname"].(string); ok && hostname != "" {
			return hostname
		}
	}

	return ""
}

func mtkPingOnce(ip string) bool {
	cmd := exec.Command("ping", "-c", "1", "-W", "1", ip)
	return cmd.Run() == nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func sortMtkModules(modules []MtkWifiModuleRadio) {
	for i := range modules {
		if modules[i].Type == "main" {
			modules[i].Name = nonEmpty(modules[i].Name, "Main Module")
		}
	}

	for i := 0; i < len(modules); i++ {
		for j := i + 1; j < len(modules); j++ {
			if mtkModuleSortKey(modules[j]) < mtkModuleSortKey(modules[i]) {
				modules[i], modules[j] = modules[j], modules[i]
			}
		}
	}
}

func mtkModuleSortKey(m MtkWifiModuleRadio) int {
	if m.Type == "main" {
		return 0
	}

	parts := strings.Split(m.IP, ".")
	if len(parts) == 4 {
		last, err := strconv.Atoi(parts[3])
		if err == nil {
			return last
		}
	}

	return 999
}

func nonEmpty(v string, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}
