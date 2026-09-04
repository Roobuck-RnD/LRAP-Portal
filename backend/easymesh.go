package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	easyMeshStatePath       = "/etc/roobuck/easymesh.json"
	easyMeshMigrationPath   = "/etc/roobuck/easymesh_migration.json"
	easyMeshMapdUserPath    = "/etc/map/mapd_user.cfg"
	easyMeshMapdDefaultPath = "/etc/map/mapd_default.cfg"
	easyMeshBSSPolicyPath   = "/etc/map/wts_bss_info_config"
	easyMeshMapdRuntimePath = "/etc/map/mapd_cfg"
	easyMesh1905Path        = "/etc/map/1905d.cfg"
	// DBDC_card0.dat is the card-level MediaTek profile that sits above the two
	// per-band profiles. Its list-valued keys carry one entry per band ("0;0").
	easyMeshCardProfilePath  = "/etc/wireless/mediatek/DBDC_card0.dat"
	easyMeshMapdSocket       = "/tmp/mapd_ctrl"
	easyMeshStartReadyPath   = "/tmp/lrap-easymesh.ready"
	easyMeshStartFailedPath  = "/tmp/lrap-easymesh.failed"
	easyMeshStartPIDPath     = "/tmp/lrap-easymesh-start.pid"
	easyMeshTopologyPath     = "/tmp/dump.txt"
	easyMeshBackupPath       = "/etc/roobuck/mesh-backup"
	easyMeshRestoreReady     = "/tmp/lrap-mesh-restore.ready"
	easyMeshRestoreFailed    = "/tmp/lrap-mesh-restore.failed"
	easyMeshRestorePID       = "/tmp/lrap-mesh-restore.pid"
	easyMeshWiFiServicesPath = "/lib/wifi/wifi_services.lua"
	easyMeshClientVLAN       = 100
	easyMeshManagementVLAN   = 200
	easyMeshOnboardingWindow = 5 * time.Minute
	easyMeshPBCRefresh       = 60 * time.Second
	// One short WPS scan is enough to learn the Root's channel.
	easyMeshChannelProbeWait = 25 * time.Second
	// `wifi reload` rebuilds the Radio; wait for it to beacon again.
	easyMeshChannelSettle = 30 * time.Second
	// The first sweep after arming can miss the Root; retry before giving up.
	easyMeshChannelProbeAttempts = 3
	easyMeshBackhaulStable       = 12 * time.Second
	// How long a just-paired Backhaul is allowed to churn before it must show
	// easyMeshBackhaulStable seconds of uninterrupted connectivity.
	easyMeshBackhaulSettle = 120 * time.Second
	easyMeshHandoffTimeout = 75 * time.Second
	easyMeshHandoffReady   = "/tmp/lrap-mesh-agent-handoff.ready"
	easyMeshHandoffFailed  = "/tmp/lrap-mesh-agent-handoff.failed"
	easyMeshHandoffPID     = "/tmp/lrap-mesh-agent-handoff.pid"
	easyMeshRebootPID      = "/tmp/lrap-mesh-reboot.pid"
)

type EasyMeshConfig struct {
	Version         int    `json:"version"`
	Role            string `json:"role"`
	BackhaulBand    string `json:"backhaul_band"`
	NetworkPrepared bool   `json:"network_prepared"`
	Enabled         bool   `json:"enabled"`
	// ActivationPending marks a merged one-click Enable-Mesh flow whose Prepare
	// step scheduled the mandatory Radio reboot. On the next boot, resume finishes
	// the transaction by running activation, so the user reconnects only once.
	ActivationPending bool  `json:"activation_pending,omitempty"`
	AgentHandoff      bool  `json:"agent_handoff"`
	OnboardingAt      int64 `json:"onboarding_at,omitempty"`
	UpdatedAt         int64 `json:"updated_at"`
}

type EasyMeshModule struct {
	ModuleID      string `json:"module_id"`
	Name          string `json:"name"`
	Port          string `json:"port"`
	IP            string `json:"ip"`
	MAC           string `json:"mac"`
	Online        bool   `json:"online"`
	Capable       bool   `json:"capable"`
	CapabilityErr string `json:"capability_error,omitempty"`
}

type EasyMeshRemoteNode struct {
	Name             string   `json:"name"`
	ALID             string   `json:"al_id"`
	MainMAC          string   `json:"main_mac,omitempty"`
	ManagementIP     string   `json:"management_ip,omitempty"`
	BackhaulMedium   string   `json:"backhaul_medium"`
	BackhaulRSSI     string   `json:"backhaul_rssi,omitempty"`
	Distance         string   `json:"distance,omitempty"`
	UpstreamALID     string   `json:"upstream_al_id,omitempty"`
	Connected        bool     `json:"connected"`
	ManagementOnline bool     `json:"management_online"`
	FronthaulReady   bool     `json:"fronthaul_ready"`
	FronthaulSSIDs   []string `json:"fronthaul_ssids"`
}

type EasyMeshJob struct {
	ID        uint64 `json:"id,omitempty"`
	Running   bool   `json:"running"`
	Action    string `json:"action,omitempty"`
	Stage     string `json:"stage,omitempty"`
	Error     string `json:"error,omitempty"`
	StartedAt int64  `json:"started_at,omitempty"`
	EndedAt   int64  `json:"ended_at,omitempty"`
}

type EasyMeshStatus struct {
	Config            EasyMeshConfig       `json:"config"`
	Modules           []EasyMeshModule     `json:"modules"`
	RemoteNodes       []EasyMeshRemoteNode `json:"remote_nodes"`
	NodeID            string               `json:"node_id"`
	EngineAvailable   bool                 `json:"engine_available"`
	EngineRunning     bool                 `json:"engine_running"`
	RuntimeRole       string               `json:"runtime_role"`
	BackhaulStatus    string               `json:"backhaul_status"`
	BackhaulConnected bool                 `json:"backhaul_connected"`
	// The band the Backhaul is really running on. mapd may move it away from the
	// selection, and an operator who picked 5 GHz needs to be told when the Mesh
	// is in fact riding 2.4 GHz rather than left to assume otherwise.
	BackhaulActiveBand   string                `json:"backhaul_active_band,omitempty"`
	ClientHandoffReady   bool                  `json:"client_handoff_ready"`
	ManagementIP         string                `json:"management_ip,omitempty"`
	BackhaulAntennaReady bool                  `json:"backhaul_antenna_ready"`
	RadioPrepared        bool                  `json:"radio_prepared"`
	Topology             string                `json:"topology"`
	TopologyGraph        EasyMeshTopologyGraph `json:"topology_graph"`
	ClientVLAN           int                   `json:"client_vlan"`
	ManagementVLAN       int                   `json:"management_vlan"`
	ManagementSubnet     string                `json:"management_subnet"`
	RequiresSeparation   bool                  `json:"requires_separation"`
	Job                  EasyMeshJob           `json:"job"`
}

type easyMeshRequest struct {
	Action       string `json:"action"`
	Role         string `json:"role"`
	BackhaulBand string `json:"backhaul_band"`
}

type easyMeshFronthaulCredential struct {
	SSID       string
	Passphrase string
	AuthMode   string
	Encryption string
}

type easyMeshFronthaulSet struct {
	TwoG  easyMeshFronthaulCredential
	FiveG easyMeshFronthaulCredential
}

var easyMeshRuntime = struct {
	sync.Mutex
	Job    EasyMeshJob
	NextID uint64
}{}

var easyMeshTopologyMu sync.Mutex

var easyMeshExecLocal = func(command string) (string, error) {
	out, err := exec.Command("sh", "-c", command).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

var easyMeshExecRemote = func(ip string, command string) (string, error) {
	res, err := ubusExecRemoteChecked(ip, command)
	if err != nil {
		return "", err
	}
	return stringFromAny(res["stdout"]), nil
}

var easyMeshWriteRemoteFile = ubusFileWriteRemote

// Optional entries are snapshotted and restored best-effort. They must never be
// required, because chassis that were prepared by an earlier release already
// carry a snapshot directory without them and restore validation would fail.
var easyMeshSnapshotFiles = []struct {
	Source   string
	Name     string
	Optional bool
}{
	{"/etc/config/network", "network", false},
	{"/etc/config/dhcp", "dhcp", false},
	{Mtk24GPath, "wifi-2g.dat", false},
	{Mtk5GPath, "wifi-5g.dat", false},
	{easyMeshCardProfilePath, "wifi-card0.dat", true},
	{easyMeshMapdUserPath, "mapd_user.cfg", false},
	{easyMeshMapdDefaultPath, "mapd_default.cfg", false},
	{"/etc/map/1905d.cfg", "1905d.cfg", false},
	{"/etc/map/wts_bss_info_config", "wts_bss_info_config", false},
}

func defaultEasyMeshConfig() EasyMeshConfig {
	return EasyMeshConfig{
		Version:         1,
		Role:            "standalone",
		BackhaulBand:    "5g",
		NetworkPrepared: antennaManagementIsIsolated(),
	}
}

func easyMeshOwnsWiFi() bool {
	if loadEasyMeshConfig().Enabled {
		return true
	}
	job := currentEasyMeshJob()
	return job.Running && (job.Action == "activate" || job.Action == "onboard")
}

func loadEasyMeshConfig() EasyMeshConfig {
	cfg := defaultEasyMeshConfig()
	raw, err := os.ReadFile(easyMeshStatePath)
	if err != nil {
		return cfg
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return defaultEasyMeshConfig()
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	cfg.NetworkPrepared = antennaManagementIsIsolated()
	return cfg
}

func saveEasyMeshConfig(cfg EasyMeshConfig) error {
	cfg.Version = 1
	cfg.UpdatedAt = time.Now().Unix()
	if err := os.MkdirAll(filepath.Dir(easyMeshStatePath), 0755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	tmp := easyMeshStatePath + ".tmp"
	if err := os.WriteFile(tmp, raw, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, easyMeshStatePath)
}

func easyMeshHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		_ = json.NewEncoder(w).Encode(getEasyMeshStatus())
	case http.MethodPost:
		var req easyMeshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}
		jobID, err := handleEasyMeshAction(req)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		response := map[string]any{"status": "accepted", "action": req.Action}
		if jobID != 0 {
			response["job_id"] = jobID
		}
		_ = json.NewEncoder(w).Encode(response)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleEasyMeshAction(req easyMeshRequest) (uint64, error) {
	req.Action = strings.ToLower(strings.TrimSpace(req.Action))
	switch req.Action {
	case "save":
		cfg := loadEasyMeshConfig()
		if err := normalizeAndValidateEasyMeshConfig(&cfg, req); err != nil {
			return 0, err
		}
		if cfg.Enabled {
			return 0, errors.New("disable EasyMesh before changing the node role or backhaul Antenna")
		}
		return 0, saveEasyMeshConfig(cfg)
	case "enable_mesh", "prepare_network", "prepare_radios", "rollback_network", "activate", "onboard", "sync_fronthaul", "leave", "disable":
		return startEasyMeshJob(req.Action)
	default:
		return 0, fmt.Errorf("unsupported action %q", req.Action)
	}
}

func normalizeAndValidateEasyMeshConfig(cfg *EasyMeshConfig, req easyMeshRequest) error {
	role := strings.ToLower(strings.TrimSpace(req.Role))
	if role != "controller" && role != "agent" && role != "standalone" {
		return errors.New("role must be standalone, controller, or agent")
	}
	band := strings.ToLower(strings.TrimSpace(req.BackhaulBand))
	if band == "" {
		band = "5g"
	}
	if band != "2g" && band != "5g" {
		return errors.New("backhaul band must be 2g or 5g")
	}
	// The Backhaul is carried by this device's own Radio, so there is nothing
	// remote to validate here. Antennas stay plain client APs and are never part
	// of the Mesh.
	cfg.Role = role
	cfg.BackhaulBand = band
	return nil
}

func startEasyMeshJob(action string) (uint64, error) {
	easyMeshRuntime.Lock()
	defer easyMeshRuntime.Unlock()
	if easyMeshRuntime.Job.Running {
		return 0, fmt.Errorf("%s is already running", easyMeshRuntime.Job.Action)
	}
	easyMeshRuntime.NextID++
	jobID := easyMeshRuntime.NextID
	easyMeshRuntime.Job = EasyMeshJob{ID: jobID, Running: true, Action: action, Stage: "Starting", StartedAt: time.Now().Unix()}
	go runEasyMeshJob(action)
	return jobID, nil
}

func runEasyMeshJob(action string) {
	var err error
	switch action {
	case "enable_mesh":
		err = runEnableMesh()
	case "prepare_network":
		err = prepareEasyMeshNetwork()
	case "prepare_radios":
		err = prepareEasyMeshRadios()
	case "rollback_network":
		err = rollbackEasyMeshNetwork()
	case "activate":
		err = activateEasyMesh()
	case "onboard":
		err = triggerEasyMeshOnboarding()
	case "sync_fronthaul":
		err = syncEasyMeshFronthaul()
	case "leave", "disable":
		err = leaveEasyMesh()
	}
	easyMeshRuntime.Lock()
	easyMeshRuntime.Job.Running = false
	easyMeshRuntime.Job.EndedAt = time.Now().Unix()
	if err != nil {
		easyMeshRuntime.Job.Stage = "Failed"
		easyMeshRuntime.Job.Error = err.Error()
	} else {
		easyMeshRuntime.Job.Stage = "Complete"
		easyMeshRuntime.Job.Error = ""
	}
	easyMeshRuntime.Unlock()
}

func setEasyMeshJobStage(stage string) {
	easyMeshRuntime.Lock()
	easyMeshRuntime.Job.Stage = stage
	easyMeshRuntime.Unlock()
}

func currentEasyMeshJob() EasyMeshJob {
	easyMeshRuntime.Lock()
	defer easyMeshRuntime.Unlock()
	return easyMeshRuntime.Job
}

func getEasyMeshStatus() EasyMeshStatus {
	cfg := loadEasyMeshConfig()
	modules := discoverEasyMeshModules()
	items := make([]EasyMeshModule, len(modules))
	var capabilityWG sync.WaitGroup
	for index, module := range modules {
		index, module := index, module
		items[index] = EasyMeshModule{
			ModuleID: module.ModuleID,
			Name:     apDisplayName(module.IP, module.PortIndex),
			Port:     module.Port,
			IP:       module.IP,
			MAC:      module.MAC,
			Online:   true,
		}
		capabilityWG.Add(1)
		go func() {
			defer capabilityWG.Done()
			if err := easyMeshCheckRemoteCapability(module.IP); err != nil {
				items[index].CapabilityErr = err.Error()
			} else {
				items[index].Capable = true
			}
		}()
	}
	capabilityWG.Wait()

	status := EasyMeshStatus{
		Config:             cfg,
		Modules:            items,
		RemoteNodes:        make([]EasyMeshRemoteNode, 0),
		NodeID:             easyMeshNodeID(),
		EngineAvailable:    easyMeshLocalCapability() == nil,
		ClientVLAN:         easyMeshClientVLAN,
		ManagementVLAN:     easyMeshManagementVLAN,
		ManagementSubnet:   "172.31.255.0/29",
		RequiresSeparation: !cfg.NetworkPrepared,
		Job:                currentEasyMeshJob(),
	}
	if cfg.Role == "agent" {
		status.ClientHandoffReady = easyMeshAgentClientHandoffReady()
		if status.ClientHandoffReady {
			status.ManagementIP = easyMeshLANIPv4()
		}
	}
	// A stale control socket can survive a failed/terminated vendor process.
	// Report Running only when both the socket and the mapd process exist.
	if easyMeshLocalEngineRunning() {
		status.EngineRunning = true
		status.RuntimeRole = easyMeshRuntimeCommand("getrole")
		status.BackhaulStatus = easyMeshRuntimeCommand("bh_conn_status")
		if topology, err := easyMeshLocalTopologySnapshot(); err == nil {
			status.Topology = easyMeshRedactTopology(topology)
			// Both roles hold the full device list, so both can draw the Mesh. An
			// Agent's dump simply lists itself first rather than the Controller.
			management := map[string]easyMeshManagementNode{}
			if cfg.Role == "controller" {
				management = easyMeshManagementNodesByALID()
				status.RemoteNodes = easyMeshRemoteNodesFromTopologyWithManagement(
					topology,
					easyMeshDHCPLeasesByMAC(),
					management,
				)
			}
			// Only this chassis can enumerate its own Antennas, so every peer is
			// asked for its own picture and the two are drawn as one fabric.
			localALID := easyMeshLocalALID()
			peerIPs := make(map[string]string, len(management))
			for alID, node := range management {
				if alID != localALID && node.IP != "" {
					peerIPs[alID] = node.IP
				}
			}
			status.TopologyGraph = easyMeshTopologyGraphWithPeerFabric(
				topology, localALID, management,
				easyMeshFabricSnapshot(), easyMeshPeerFabricSnapshot(peerIPs))
		}
	}
	// The wireless Backhaul lives on this device's own Radio, so the local
	// runtime is the authority for both roles. Antennas are plain client APs and
	// contribute nothing to Backhaul state.
	status.RadioPrepared = easyMeshLocalFronthaulInterfacesPresent()
	if raw := easyMeshRuntimeCommand("bh_conn_status"); raw != "" {
		status.BackhaulStatus = raw
		if cfg.Role == "agent" {
			active, associated := easyMeshActiveBackhaulBand(cfg.BackhaulBand)
			status.BackhaulActiveBand = active
			status.BackhaulConnected = easyMeshBackhaulConnected(raw) && associated
		}
	}
	return status
}

func easyMeshNodeID() string {
	for _, path := range []string{"/sys/class/net/br-lan/address", "/sys/class/net/eth0/address"} {
		if raw, err := os.ReadFile(path); err == nil {
			id := strings.ToUpper(strings.TrimSpace(string(raw)))
			if id != "" {
				return id
			}
		}
	}
	return "UNKNOWN"
}

func easyMeshLocalALID() string {
	raw, err := os.ReadFile("/etc/map/1905d.cfg")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || strings.TrimSpace(key) != "map_agent_alid" {
			continue
		}
		alID := strings.ToLower(strings.TrimSpace(value))
		if _, err := net.ParseMAC(alID); err == nil {
			return alID
		}
	}
	return ""
}

func easyMeshLANIPv4() string {
	iface, err := net.InterfaceByName("br-lan")
	if err != nil {
		return ""
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return ""
	}
	for _, addr := range addrs {
		ipText, _, _ := strings.Cut(addr.String(), "/")
		ip := net.ParseIP(ipText)
		if ip != nil && ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
			return ip.String()
		}
	}
	return ""
}

func easyMeshLocalCapability() error {
	for _, path := range []string{"/usr/bin/EasyMesh_openwrt.sh", "/usr/bin/mapd", "/usr/bin/mapd_cli", Mtk24GPath, Mtk5GPath} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("missing %s", path)
		}
	}
	return nil
}

func easyMeshCheckRemoteCapability(ip string) error {
	_, err := easyMeshExecRemote(ip, "test -x /usr/bin/EasyMesh_openwrt.sh && test -x /usr/bin/mapd && test -x /usr/bin/mapd_cli && test -f "+shellSingleQuote(Mtk24GPath)+" && test -f "+shellSingleQuote(Mtk5GPath))
	return err
}

// mapd_cli prefixes every reply with an argv echo and a connection notice, and
// the portal shows the first line of the reply as the runtime state -- so the
// Backhaul card read "count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl" instead of
// the status. The notice carries the vendor's own spelling; both are matched.
func easyMeshStripMapdBanner(raw string) string {
	kept := make([]string, 0, 4)
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		lower := strings.ToLower(trimmed)
		switch {
		case trimmed == "":
		case strings.HasPrefix(lower, "count ="):
		case strings.Contains(lower, "opened connection to mapd"):
		default:
			kept = append(kept, trimmed)
		}
	}
	return strings.Join(kept, "\n")
}

func easyMeshRuntimeCommand(command string) string {
	out, err := easyMeshExecLocal("/usr/bin/mapd_cli " + easyMeshMapdSocket + " " + command + " 2>/dev/null")
	if err != nil {
		return ""
	}
	return easyMeshStripMapdBanner(out)
}

func easyMeshRemoteRuntimeCommand(ip string, command string) (string, error) {
	out, err := easyMeshExecRemote(ip, "/usr/bin/mapd_cli "+easyMeshMapdSocket+" "+command+" 2>/dev/null")
	return easyMeshStripMapdBanner(out), err
}

func easyMeshLocalTopologySnapshot() (string, error) {
	easyMeshTopologyMu.Lock()
	defer easyMeshTopologyMu.Unlock()

	command := "rm -f " + easyMeshTopologyPath + "; /usr/bin/mapd_cli " + easyMeshMapdSocket +
		" dump_topology_v1 >/dev/null 2>&1; test -s " + easyMeshTopologyPath
	if _, err := easyMeshExecLocal(command); err != nil {
		return "", err
	}
	raw, err := os.ReadFile(easyMeshTopologyPath)
	return string(raw), err
}

func easyMeshRemoteALID(ip string) (string, error) {
	out, err := easyMeshExecRemote(ip, "sed -n 's/^map_agent_alid=//p' /etc/map/1905d.cfg | head -n 1")
	if err != nil {
		return "", err
	}
	alID := strings.ToLower(strings.TrimSpace(out))
	if alID == "" {
		return "", errors.New("selected Antenna did not report an EasyMesh AL-MAC")
	}
	return alID, nil
}

func easyMeshTopologyContainsDevice(raw string, alID string) bool {
	topology, err := parseEasyMeshTopology(raw)
	if err != nil {
		return false
	}
	for _, device := range topology.Devices {
		if strings.EqualFold(strings.TrimSpace(device.ALMAC), strings.TrimSpace(alID)) && strings.TrimSpace(device.Distance) != "0" {
			return true
		}
	}
	return false
}

func easyMeshTopologyDeviceIDs(raw string) map[string]bool {
	devices := make(map[string]bool)
	topology, err := parseEasyMeshTopology(raw)
	if err != nil {
		return devices
	}
	for _, device := range topology.Devices {
		alID := strings.ToLower(strings.TrimSpace(device.ALMAC))
		if alID != "" {
			devices[alID] = true
		}
	}
	return devices
}

type easyMeshTopology struct {
	Devices []easyMeshTopologyDevice `json:"topology information"`
}

type easyMeshTopologyDevice struct {
	ALMAC        string `json:"AL MAC"`
	Role         string `json:"Device role"`
	MapVersion   string `json:"MAP Version"`
	Distance     string `json:"Distance from controller"`
	UpstreamALID string `json:"Upstream 1905 device"`
	BackhaulInfo []struct {
		NeighborAL string `json:"neighbor almac addr"`
		Medium     string `json:"Backhaul Medium Type"`
		RSSI       string `json:"RSSI"`
	} `json:"BH Info"`
	// The station this device uses to reach its upstream. It associates to the
	// upstream's Fronthaul BSS exactly like a laptop would, so every
	// client-facing view has to know to leave it out.
	BackhaulSTAInterfaces []struct {
		MAC string `json:"MAC address"`
	} `json:"BH Interface(APCLI)"`
	// Per-hop counters the driver keeps for the Backhaul. These are what tell an
	// operator whether a link that reports "up" is actually carrying traffic.
	BackhaulMetrics []struct {
		NeighborAL string `json:"neighbor_al"`
		Metrics    []struct {
			LocalIfMAC       string `json:"local_if_mac"`
			NeighborIfMAC    string `json:"neighbor_if_mac"`
			TxErrors         string `json:"tx packet Errors"`
			TxPackets        string `json:"transmittedPackets"`
			ThroughputCap    string `json:"macThroughputCap"`
			LinkAvailability string `json:"linkAvailability"`
			PHYRate          string `json:"phyRate"`
			RxErrors         string `json:"rx packet Errors"`
			RxPackets        string `json:"Packets Received"`
			RSSI             string `json:"RSSI"`
		} `json:"metrics"`
	} `json:"backhaul link metrics"`
	OtherClients []struct {
		ClientMAC string `json:"Client Address"`
		Medium    string `json:"Medium"`
	} `json:"Other Clients Info"`
	RadioInfo []struct {
		Band      string `json:"band"`
		Channel   string `json:"channel"`
		Bandwidth string `json:"BW"`
		TxStreams string `json:"Tx Spatial streams"`
		RxStreams string `json:"Rx Spatial streams"`
		BSSInfo   []struct {
			BSSID     string `json:"BSSID"`
			SSID      string `json:"SSID"`
			Backhaul  string `json:"BACKHAUL"`
			Fronthaul string `json:"FRONTHAUL"`
			// Every station the BSS carries, the Mesh Backhaul station included --
			// that one is flagged "BH STA":"Yes" and is a Mesh link, not a client.
			Stations []struct {
				MAC         string `json:"STA MAC address"`
				BackhaulSTA string `json:"BH STA"`
				Medium      string `json:"Medium"`
				UplinkRSSI  string `json:"uplink rssi"`
			} `json:"connected sta info"`
		} `json:"BSSINFO"`
	} `json:"Radio Info"`
}

func parseEasyMeshTopology(raw string) (easyMeshTopology, error) {
	var topology easyMeshTopology
	if err := json.Unmarshal([]byte(raw), &topology); err != nil {
		return topology, err
	}
	return topology, nil
}

// easyMeshBackhaulPeerLink is one Backhaul station as the device it associated
// to reports it.
type easyMeshBackhaulPeerLink struct {
	Medium string
	RSSI   string
}

// easyMeshBackhaulPeerLinks indexes every station the topology flags as a
// Backhaul peer by its address, so a device whose own "BH Info" is empty can
// still be described from the other end of the same link.
func easyMeshBackhaulPeerLinks(topology easyMeshTopology) map[string]easyMeshBackhaulPeerLink {
	links := make(map[string]easyMeshBackhaulPeerLink)
	for _, device := range topology.Devices {
		for _, radio := range device.RadioInfo {
			for _, bss := range radio.BSSInfo {
				for _, station := range bss.Stations {
					if !strings.EqualFold(strings.TrimSpace(station.BackhaulSTA), "Yes") {
						continue
					}
					mac := strings.ToLower(strings.TrimSpace(station.MAC))
					medium := strings.TrimSpace(station.Medium)
					if mac == "" || medium == "" {
						continue
					}
					links[mac] = easyMeshBackhaulPeerLink{
						Medium: medium,
						RSSI:   strings.TrimSpace(station.UplinkRSSI),
					}
				}
			}
		}
	}
	return links
}

type easyMeshManagementNode struct {
	IP     string
	NodeID string
}

func easyMeshRemoteNodesFromTopology(raw string, leaseByMAC map[string]string) []EasyMeshRemoteNode {
	return easyMeshRemoteNodesFromTopologyWithManagement(raw, leaseByMAC, nil)
}

func easyMeshRemoteNodesFromTopologyWithManagement(raw string, leaseByMAC map[string]string, managementByALID map[string]easyMeshManagementNode) []EasyMeshRemoteNode {
	topology, err := parseEasyMeshTopology(raw)
	if err != nil {
		return []EasyMeshRemoteNode{}
	}

	// Where the Backhaul is seen from the other end. "BH Info" is the natural
	// source but it is not always populated -- after a network reload the Agent
	// stayed absent from this list for minutes while the Mesh was demonstrably up:
	// two devices in the topology, the station associated, link metrics flowing.
	// The Root's own BSS reports the same station with its band and signal, so
	// that is used when the Agent's own record has not caught up.
	backhaulPeers := easyMeshBackhaulPeerLinks(topology)

	nodes := make([]EasyMeshRemoteNode, 0)
	for _, device := range topology.Devices {
		medium, rssi, chassisALID := "", "", ""
		for _, backhaul := range device.BackhaulInfo {
			candidate := strings.TrimSpace(backhaul.Medium)
			if strings.EqualFold(candidate, "Ethernet") {
				neighbor := strings.ToLower(strings.TrimSpace(backhaul.NeighborAL))
				if neighbor != "" {
					chassisALID = neighbor
				}
				continue
			}
			if candidate == "" {
				continue
			}
			medium = candidate
			rssi = strings.TrimSpace(backhaul.RSSI)
			break
		}
		if medium == "" {
			// Fall back to how the upstream sees this device's Backhaul station.
			for _, sta := range device.BackhaulSTAInterfaces {
				peer, ok := backhaulPeers[strings.ToLower(strings.TrimSpace(sta.MAC))]
				if !ok {
					continue
				}
				medium, rssi = peer.Medium, peer.RSSI
				break
			}
		}
		if medium == "" {
			continue
		}

		wirelessALID := strings.ToLower(strings.TrimSpace(device.ALMAC))

		// MediaTek reports the complete Agent's main module as a separate
		// downstream 1905 device. Prefer that Ethernet-adjacent AL-MAC over the
		// legacy "Other Clients Info" heuristic, which is absent on this SDK.
		if chassisALID == "" {
			for _, candidate := range topology.Devices {
				if strings.EqualFold(strings.TrimSpace(candidate.UpstreamALID), wirelessALID) {
					chassisALID = strings.ToLower(strings.TrimSpace(candidate.ALMAC))
					break
				}
			}
		}

		mainMAC := chassisALID
		for _, client := range device.OtherClients {
			if strings.EqualFold(strings.TrimSpace(client.Medium), "Ethernet") {
				legacyMAC := strings.ToLower(strings.TrimSpace(client.ClientMAC))
				if mainMAC == "" && legacyMAC != "" {
					mainMAC = legacyMAC
				}
				if legacyMAC != "" {
					break
				}
			}
		}

		// In the AC-to-AC layout there is no separate downstream "main module"
		// device: the Agent chassis IS the wireless device, so both its lease and
		// its management identity are held under its own AL-MAC. Keying the
		// management lookup on chassisALID alone left it permanently empty here, so
		// a reachable Agent reported "Waiting for an address" forever.
		managementALID := chassisALID
		if managementALID == "" {
			managementALID = wirelessALID
		}
		if mainMAC == "" {
			mainMAC = wirelessALID
		}

		managementIP := ""
		managementOnline := false
		if managed, ok := managementByALID[managementALID]; ok {
			managementIP = managed.IP
			managementOnline = managed.IP != ""
			if managed.NodeID != "" {
				mainMAC = managed.NodeID
			}
		} else if leased := leaseByMAC[mainMAC]; leased != "" && easyMeshLeaseBelongsToChassis(leased, managementALID) {
			// Only a lease whose holder identifies itself as THIS chassis may stand
			// in for the verified lookup. mainMAC can come from the legacy
			// "Other Clients Info" heuristic, which in the AC-to-AC layout picks up
			// the Agent's own Antenna -- measured on the live pair, the Root
			// reported the Antenna's client address as the Agent's management
			// address, which is worse than reporting none.
			managementIP = leased
			managementOnline = easyMeshManagementReachable(leased)
		}

		fronthaulSSIDs := make([]string, 0)
		seenSSID := make(map[string]bool)
		for _, radio := range device.RadioInfo {
			// Hide a BSS from client coverage only when it is Backhaul-ONLY, which
			// is to say the Radio publishes a separate Fronthaul BSS beside it.
			//
			// Testing FRONTHAUL=1 instead does not survive contact with the device:
			// this firmware reports every BSS as BACKHAUL=0 FRONTHAUL=0, the
			// Controller's own included, and at BssidNum=1 the single BSS both
			// serves clients and carries the Backhaul anyway. A healthy Agent
			// broadcasting the Mesh SSID therefore read as "Backhaul only" — the
			// opposite of what an operator sees on a phone standing next to it.
			dedicatedFronthaul := false
			for _, bss := range radio.BSSInfo {
				if strings.TrimSpace(bss.Fronthaul) == "1" && strings.TrimSpace(bss.SSID) != "" {
					dedicatedFronthaul = true
					break
				}
			}
			for _, bss := range radio.BSSInfo {
				ssid := strings.TrimSpace(bss.SSID)
				backhaulOnly := dedicatedFronthaul &&
					strings.TrimSpace(bss.Backhaul) == "1" &&
					strings.TrimSpace(bss.Fronthaul) != "1"
				if backhaulOnly || ssid == "" || seenSSID[ssid] {
					continue
				}
				seenSSID[ssid] = true
				fronthaulSSIDs = append(fronthaulSSIDs, ssid)
			}
		}
		sort.Strings(fronthaulSSIDs)
		nodes = append(nodes, EasyMeshRemoteNode{
			ALID:             strings.ToLower(strings.TrimSpace(device.ALMAC)),
			MainMAC:          mainMAC,
			ManagementIP:     managementIP,
			BackhaulMedium:   medium,
			BackhaulRSSI:     rssi,
			Distance:         strings.TrimSpace(device.Distance),
			UpstreamALID:     strings.ToLower(strings.TrimSpace(device.UpstreamALID)),
			Connected:        true,
			ManagementOnline: managementOnline,
			FronthaulReady:   len(fronthaulSSIDs) > 0,
			FronthaulSSIDs:   fronthaulSSIDs,
		})
	}

	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Distance != nodes[j].Distance {
			return nodes[i].Distance < nodes[j].Distance
		}
		return nodes[i].ALID < nodes[j].ALID
	})
	for index := range nodes {
		nodes[index].Name = fmt.Sprintf("Mesh Agent %d", index+1)
	}
	return nodes
}

var easyMeshManagementReachable = func(ip string) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(ip, "9080"), 750*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func easyMeshDHCPLeasesByMAC() map[string]string {
	leases := make(map[string]string)
	raw, err := os.ReadFile("/tmp/dhcp.leases")
	if err != nil {
		return leases
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		mac := strings.ToLower(strings.TrimSpace(fields[1]))
		if net.ParseIP(fields[2]) != nil {
			leases[mac] = fields[2]
		}
	}
	return leases
}

var easyMeshManagementIdentityAt = func(ip string) (string, string, error) {
	client := &http.Client{Timeout: 750 * time.Millisecond}
	response, err := client.Get("http://" + net.JoinHostPort(ip, "9080") + "/healthz")
	if err != nil {
		return "", "", err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("health returned HTTP %d", response.StatusCode)
	}
	var health struct {
		OK       bool   `json:"ok"`
		NodeID   string `json:"node_id"`
		MeshALID string `json:"mesh_al_id"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health); err != nil {
		return "", "", err
	}
	meshALID := strings.ToLower(strings.TrimSpace(health.MeshALID))
	if !health.OK || net.ParseIP(ip) == nil {
		return "", "", errors.New("invalid management health response")
	}
	if _, err := net.ParseMAC(meshALID); err != nil {
		return "", "", errors.New("management health response has no valid EasyMesh AL-MAC")
	}
	nodeID := strings.ToLower(strings.TrimSpace(health.NodeID))
	if nodeID != "" {
		if _, err := net.ParseMAC(nodeID); err != nil {
			nodeID = ""
		}
	}
	return meshALID, nodeID, nil
}

// easyMeshLeaseBelongsToChassis asks whoever holds a lease to identify itself,
// so an address is only ever attributed to the chassis that actually answers for
// it.
var easyMeshLeaseBelongsToChassis = func(ip string, alID string) bool {
	reported, _, err := easyMeshManagementIdentityAt(ip)
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(reported), strings.TrimSpace(alID))
}

func easyMeshManagementNodesByALID() map[string]easyMeshManagementNode {
	nodes := make(map[string]easyMeshManagementNode)
	raw, err := os.ReadFile("/tmp/dhcp.leases")
	if err != nil {
		return nodes
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || !strings.EqualFold(fields[3], "RoobuckAC") || net.ParseIP(fields[2]) == nil {
			continue
		}
		ip := fields[2]
		meshALID, nodeID, err := easyMeshManagementIdentityAt(ip)
		if err != nil {
			continue
		}
		nodes[meshALID] = easyMeshManagementNode{IP: ip, NodeID: nodeID}
	}
	return nodes
}

var easyMeshControllerAntennaAttached = func(ip string) (bool, error) {
	alID, err := easyMeshRemoteALID(ip)
	if err != nil {
		return false, err
	}
	topology, err := easyMeshLocalTopologySnapshot()
	if err != nil {
		return false, err
	}
	return easyMeshTopologyContainsDevice(topology, alID), nil
}

// The wireless Backhaul is carried by the main module's own Radio: the Root
// runs the WPS registrar on its Fronthaul BSS (ra0/rax0) and the joining
// chassis pairs with its own Backhaul STA (apcli0/apclix0). Antennas are plain
// client-serving APs and take no part in the Mesh.
func easyMeshLocalWirelessBackhaulAssociated(band string) (bool, error) {
	out, err := easyMeshExecLocal("iwconfig " + easyMeshBackhaulSTAInterface(band) + " 2>/dev/null")
	if err != nil {
		return false, err
	}
	return easyMeshWirelessBackhaulAssociated(out), nil
}

// easyMeshActiveBackhaulBand reports the band the Backhaul is actually running
// on, which is not always the one that was selected.
//
// mapd is configured with AutoBHSwitching and moves the Backhaul to whichever
// band it prefers -- measured on the live pair, it moved a 5 GHz selection to
// 2.4 GHz because the signal there was 13 dB better. The selection is the
// preference used to bring the Mesh up; once the vendor has moved it, the
// product has to follow. Checking only the selected band made the startup
// recovery conclude the Backhaul was gone and tear down a working Mesh.
//
// The selected band is tried first so a Backhaul that is on both reports the
// one the operator asked for.
func easyMeshActiveBackhaulBand(preferred string) (string, bool) {
	bands := []string{"5g", "2g"}
	if preferred == "2g" {
		bands = []string{"2g", "5g"}
	}
	for _, band := range bands {
		if associated, err := easyMeshLocalWirelessBackhaulAssociated(band); err == nil && associated {
			return band, true
		}
	}
	return "", false
}

func easyMeshWirelessBackhaulAssociated(raw string) bool {
	lower := strings.ToLower(raw)
	if !strings.Contains(lower, "access point:") || strings.Contains(lower, "not-associated") {
		return false
	}
	return !strings.Contains(lower, "access point: 00:00:00:00:00:00")
}

func easyMeshBackhaulConnected(raw string) bool {
	lower := strings.ToLower(strings.TrimSpace(raw))
	if lower == "" || strings.Contains(lower, "not connected") || strings.Contains(lower, "disconnected") {
		return false
	}
	if strings.Contains(lower, "connected") {
		return true
	}
	for _, line := range strings.Split(lower, "\n") {
		if !strings.Contains(line, "backhaul connection status") {
			continue
		}
		fields := strings.FieldsFunc(line, func(r rune) bool {
			return r < '0' || r > '9'
		})
		for index := len(fields) - 1; index >= 0; index-- {
			value, err := strconv.Atoi(fields[index])
			if err == nil {
				// The deployed MediaTek CLI reports 0/1. Fail closed for any
				// undocumented value so DHCP ownership is never transferred on
				// an ambiguous state.
				return value == 1
			}
		}
	}
	return false
}

func discoverEasyMeshModules() []ManagedModule {
	discovered := discoverManagedAPs(true)
	if antennaManagementIsIsolated() {
		return discovered
	}

	// A failed or interrupted migration may leave some Antennas reachable only
	// through VLAN 200 before the final marker is written. Keep their verified
	// physical identities so retrying the transaction remains possible.
	remembered := loadEasyMeshMigrationModules()
	return mergeEasyMeshModules(discovered, remembered)
}

func mergeEasyMeshModules(discovered []ManagedModule, remembered []ManagedModule) []ManagedModule {
	byID := make(map[string]ManagedModule, len(discovered)+len(remembered))
	for _, module := range discovered {
		byID[module.ModuleID] = module
	}
	// A remembered module has already completed management-VLAN DHCP
	// verification. Prefer that address over a stale legacy lease/FDB entry
	// until the migration marker is committed.
	for _, module := range remembered {
		if strings.HasPrefix(module.IP, "172.31.255.") {
			byID[module.ModuleID] = module
		}
	}
	modules := make([]ManagedModule, 0, len(byID))
	for _, module := range byID {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].PortIndex < modules[j].PortIndex })
	return modules
}

func loadEasyMeshMigrationModules() []ManagedModule {
	raw, err := os.ReadFile(easyMeshMigrationPath)
	if err != nil {
		return nil
	}
	var modules []ManagedModule
	if json.Unmarshal(raw, &modules) != nil {
		return nil
	}
	return modules
}

func saveEasyMeshMigrationModules(modules []ManagedModule) error {
	raw, err := json.MarshalIndent(modules, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(easyMeshMigrationPath, raw, 0600)
}

func easyMeshSnapshotCommand() string {
	var b strings.Builder
	b.WriteString("set -e; base=" + shellSingleQuote(easyMeshBackupPath) + "; next=\"${base}.new\"; old=\"${base}.old\"; rm -rf \"$next\"; mkdir -p \"$next\"; ")
	for _, file := range easyMeshSnapshotFiles {
		if file.Optional {
			b.WriteString("if [ -f " + shellSingleQuote(file.Source) + " ]; then cp " + shellSingleQuote(file.Source) + " \"$next/" + file.Name + "\"; fi; ")
			continue
		}
		b.WriteString("test -f " + shellSingleQuote(file.Source) + "; cp " + shellSingleQuote(file.Source) + " \"$next/" + file.Name + "\"; ")
	}
	b.WriteString("printf 'complete\\n' >\"$next/complete\"; sync; rm -rf \"$old\"; if [ -d \"$base\" ]; then mv \"$base\" \"$old\"; fi; mv \"$next\" \"$base\"; rm -rf \"$old\"")
	return b.String()
}

func createEasyMeshSnapshots(modules []ManagedModule) error {
	setEasyMeshJobStage("Creating a fresh recoverable configuration snapshot")
	if _, err := easyMeshExecLocal(easyMeshSnapshotCommand()); err != nil {
		return fmt.Errorf("snapshot Controller configuration: %v", err)
	}
	for _, module := range modules {
		setEasyMeshJobStage("Snapshotting " + module.ModuleID)
		if _, err := easyMeshExecRemote(module.IP, easyMeshSnapshotCommand()); err != nil {
			return fmt.Errorf("snapshot %s configuration: %v", module.ModuleID, err)
		}
	}
	// Save the physical identities before any address migration. If preparation
	// is interrupted, recovery can still address the same installed modules.
	if err := saveEasyMeshMigrationModules(modules); err != nil {
		return fmt.Errorf("save recoverable module identities: %v", err)
	}
	return nil
}

func prepareEasyMeshNetwork() (retErr error) {
	if antennaManagementIsIsolated() {
		// The marker is only a durable hint. A previously interrupted rollback
		// can remove the VLAN devices before it removes the marker. Never treat
		// that stale file as proof that the isolated management plane exists.
		if easyMeshManagementRuntimeReady() {
			return ensureEasyMeshManagementBridge()
		}
		_ = os.Remove(AntennaManagementMarker)
	}
	// A chassis with no Antennas attached is a perfectly valid Mesh node: the
	// Backhaul rides this device's own Radio and Antennas are plain client APs.
	// Every Antenna step below is a loop over this list, so an empty one simply
	// migrates the AC itself and leaves the management VLAN ready for Antennas
	// that are installed later.
	modules := discoverEasyMeshModules()

	// An Antenna that lost the management VLAN is invisible here, and every
	// Antenna step below is a loop over this list -- so preparation used to
	// "succeed" while leaving such a module stranded off the Mesh entirely. Try
	// to bring it back first, then refuse if a port still holds an Antenna that
	// never answered.
	if adopted := adoptUntaggedAntennas(); adopted > 0 {
		setEasyMeshJobStage("Recovered " + strconv.Itoa(adopted) + " Antenna(s) onto the management VLAN")
		modules = discoverEasyMeshModules()
	}
	if err := easyMeshUndiscoveredAntennaError(managedAntennaLinkedPortIndexes(), modules); err != nil {
		return err
	}

	if err := createEasyMeshSnapshots(modules); err != nil {
		return err
	}
	rollbackRequired := true
	defer func() {
		if retErr == nil || !rollbackRequired {
			return
		}
		original := retErr
		setEasyMeshJobStage("Preparation failed; restoring the fresh snapshot")
		if rollbackErr := restorePreEasyMeshNetwork(loadEasyMeshConfig(), false); rollbackErr != nil {
			retErr = fmt.Errorf("%v; automatic rollback also failed: %v", original, rollbackErr)
			return
		}
		retErr = fmt.Errorf("%v; all pre-Mesh configuration was restored automatically", original)
	}()

	setEasyMeshJobStage("Preparing isolated Antenna management VLAN")
	if _, err := easyMeshExecLocal(easyMeshPrepareACManagementCommand()); err != nil {
		return fmt.Errorf("prepare AC management VLAN: %v", err)
	}
	for _, module := range modules {
		if strings.HasPrefix(module.IP, "172.31.255.") {
			continue
		}
		setEasyMeshJobStage("Preparing " + module.ModuleID + " management VLAN")
		if _, err := easyMeshExecRemote(module.IP, easyMeshPrepareAntennaManagementCommand()); err != nil {
			return fmt.Errorf("prepare %s management VLAN: %v", module.ModuleID, err)
		}
	}

	setEasyMeshJobStage("Verifying isolated Antenna management paths")
	managementByMAC, err := waitForEasyMeshManagementLeases(modules, 45*time.Second)
	if err != nil {
		return err
	}
	remembered := make([]ManagedModule, 0, len(modules))
	for _, module := range modules {
		module.IP = managementByMAC[strings.ToUpper(module.MAC)]
		remembered = append(remembered, module)
	}
	if err := saveEasyMeshMigrationModules(remembered); err != nil {
		return fmt.Errorf("save recoverable migration state: %v", err)
	}

	for _, module := range modules {
		managementIP := managementByMAC[strings.ToUpper(module.MAC)]
		setEasyMeshJobStage("Moving " + module.ModuleID + " client traffic to VLAN 100")
		if _, err := easyMeshExecRemote(managementIP, easyMeshActivateAntennaTrunkCommand()); err != nil {
			return fmt.Errorf("activate %s VLAN trunk: %v", module.ModuleID, err)
		}
	}
	for _, ip := range managementByMAC {
		if err := waitForEasyMeshRemoteReachable(ip, 45*time.Second); err != nil {
			return fmt.Errorf("Antenna management path %s did not recover after client VLAN activation: %v", ip, err)
		}
	}

	setEasyMeshJobStage("Moving AC client traffic to VLAN 100")
	if _, err := easyMeshExecLocal(easyMeshActivateACClientVLANCommand()); err != nil {
		return fmt.Errorf("activate AC client VLAN: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(AntennaManagementMarker), 0755); err != nil {
		return err
	}
	if err := os.WriteFile(AntennaManagementMarker, []byte("client_vlan=100\nmanagement_vlan=200\n"), 0644); err != nil {
		return err
	}
	_ = os.Remove(easyMeshMigrationPath)
	// Re-read rather than reuse a stale copy: the marker above is what makes
	// NetworkPrepared true, and loadEasyMeshConfig derives it from the marker.
	cfg := loadEasyMeshConfig()
	cfg.NetworkPrepared = true
	if err := saveEasyMeshConfig(cfg); err != nil {
		return err
	}
	rollbackRequired = false
	return nil
}

func waitForEasyMeshRemoteReachable(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if _, err := easyMeshExecRemote(ip, "true"); err == nil {
			return nil
		} else {
			lastErr = err
		}
		time.Sleep(time.Second)
	}
	if lastErr == nil {
		lastErr = errors.New("no response")
	}
	return lastErr
}

// easyMeshLANPortsShellVar emits the shell that captures br-lan's CURRENT
// physical port list into $LANP. The port count is hardware-specific (this
// chassis carries lan1..lan5, not the four an earlier revision assumed), and a
// hardcoded range silently drops every port beyond it from the rebuilt bridge:
// whatever is plugged into the extra port goes dark until Leave restores the
// snapshot. Deriving the list keeps the migration faithful to the real device.
// Already-tagged entries (lanN.100 from a re-run) collapse back to their base
// port so the migration stays idempotent instead of yielding an empty list.
const easyMeshLANPortsShellVar = `LANP=""; for p in $(uci -q get network.@device[0].ports); do case "$p" in lan*.*) LANP="$LANP ${p%%.*}";; lan*) LANP="$LANP $p";; esac; done; LANP=$(echo $LANP | tr ' ' '\n' | sort -u | tr '\n' ' '); test -n "$LANP"; `

func easyMeshPrepareACManagementCommand() string {
	var b strings.Builder
	b.WriteString("set -e; test \"$(uci -q get network.@device[0].name)\" = br-lan; mkdir -p /etc/roobuck/mesh-backup; ")
	b.WriteString(easyMeshLANPortsShellVar)
	b.WriteString("for p in $LANP; do uci -q delete network.mesh_mgmt_$p 2>/dev/null || true; uci set network.mesh_mgmt_$p=device; uci set network.mesh_mgmt_$p.type=8021q; uci set network.mesh_mgmt_$p.ifname=$p; uci set network.mesh_mgmt_$p.vid=200; uci set network.mesh_mgmt_$p.name=$p.200; done; ")
	b.WriteString("uci -q delete network.mesh_mgmt_bridge 2>/dev/null || true; uci set network.mesh_mgmt_bridge=device; uci set network.mesh_mgmt_bridge.name=" + AntennaManagementBridge + "; uci set network.mesh_mgmt_bridge.type=bridge; ")
	b.WriteString("for p in $LANP; do uci add_list network.mesh_mgmt_bridge.ports=$p.200; done; ")
	b.WriteString("uci -q delete network.antenna_mgmt 2>/dev/null || true; uci set network.antenna_mgmt=interface; uci set network.antenna_mgmt.device=" + AntennaManagementBridge + "; uci set network.antenna_mgmt.proto=static; uci set network.antenna_mgmt.ipaddr=172.31.255.1; uci set network.antenna_mgmt.netmask=255.255.255.248; uci set dhcp.rbap_pool.interface=antenna_mgmt; uci commit network; uci commit dhcp; ubus call network reload; /etc/init.d/dnsmasq restart")
	return b.String()
}

func easyMeshManagementBridgeMigrationCommand() string {
	return "set -e; uci set network.mesh_mgmt_bridge.name=" + AntennaManagementBridge +
		"; uci set network.antenna_mgmt.device=" + AntennaManagementBridge +
		"; uci commit network; ubus call network reload"
}

func easyMeshManagementRuntimeReady() bool {
	out, err := easyMeshExecLocal("ip -4 addr show dev " + AntennaManagementBridge)
	return err == nil && strings.Contains(out, AntennaManagementACIP+"/29")
}

func ensureEasyMeshManagementBridge() error {
	bridgeName, bridgeErr := easyMeshExecLocal("uci -q get network.mesh_mgmt_bridge.name")
	interfaceDevice, interfaceErr := easyMeshExecLocal("uci -q get network.antenna_mgmt.device")
	configured := bridgeErr == nil && interfaceErr == nil &&
		strings.TrimSpace(bridgeName) == AntennaManagementBridge &&
		strings.TrimSpace(interfaceDevice) == AntennaManagementBridge
	if configured && easyMeshManagementRuntimeReady() {
		return nil
	}

	if _, err := easyMeshExecLocal(easyMeshManagementBridgeMigrationCommand()); err != nil {
		return fmt.Errorf("restore compatible Antenna management bridge: %v", err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if easyMeshManagementRuntimeReady() {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("Antenna management bridge did not recover after network reload")
}

func easyMeshPrepareAntennaManagementCommand() string {
	return "set -e; test \"$(uci -q get network.@device[0].name)\" = br-lan; test -d /sys/class/net/eth1; " +
		"uci -q delete network.mesh_mgmt_eth1 2>/dev/null || true; uci set network.mesh_mgmt_eth1=device; uci set network.mesh_mgmt_eth1.type=8021q; uci set network.mesh_mgmt_eth1.ifname=eth1; uci set network.mesh_mgmt_eth1.vid=200; uci set network.mesh_mgmt_eth1.name=eth1.200; " +
		"uci -q delete network.antenna_mgmt 2>/dev/null || true; uci set network.antenna_mgmt=interface; uci set network.antenna_mgmt.device=eth1.200; uci set network.antenna_mgmt.proto=dhcp; uci set network.antenna_mgmt.vendorid=RoobuckAP; uci commit network; ubus call network reload"
}

func easyMeshActivateAntennaTrunkCommand() string {
	return "set -e; test \"$(uci -q get network.@device[0].name)\" = br-lan; uci -q delete network.mesh_client_eth1 2>/dev/null || true; uci set network.mesh_client_eth1=device; uci set network.mesh_client_eth1.type=8021q; uci set network.mesh_client_eth1.ifname=eth1; uci set network.mesh_client_eth1.vid=100; uci set network.mesh_client_eth1.name=eth1.100; " +
		"uci -q delete network.@device[0].ports; uci add_list network.@device[0].ports=eth1.100; uci add_list network.@device[0].ports=ra0; uci add_list network.@device[0].ports=rax0; " +
		// NOTE: no `wifi reload` here. This command runs on the Antenna over the
		// ubus command channel; a wifi reload holds the call open long enough for
		// the concurrent netifd reload to drop the Antenna's eth1.200 management
		// address mid-response ("ubus bad result body="), failing Prepare. The
		// Antenna's radios are re-attached by the mandatory reboot in Prepare
		// Radio, so its client SSID does not need reload recovery here. The local
		// AC path (easyMeshActivateACClientVLANCommand) keeps its wifi reload
		// because it runs locally and owns the SSID the user is connected to.
		"uci set network.lan.proto=dhcp; uci -q delete network.lan.ipaddr 2>/dev/null || true; uci -q delete network.lan.netmask 2>/dev/null || true; uci -q delete network.lan.vendorid 2>/dev/null || true; uci commit network; ubus call network reload"
}

func easyMeshActivateACClientVLANCommand() string {
	var b strings.Builder
	b.WriteString("set -e; test \"$(uci -q get network.@device[0].name)\" = br-lan; ")
	b.WriteString(easyMeshLANPortsShellVar)
	b.WriteString("for p in $LANP; do uci -q delete network.mesh_client_$p 2>/dev/null || true; uci set network.mesh_client_$p=device; uci set network.mesh_client_$p.type=8021q; uci set network.mesh_client_$p.ifname=$p; uci set network.mesh_client_$p.vid=100; uci set network.mesh_client_$p.name=$p.100; done; ")
	// Keep the main module's own radios in the client bridge. Removing them
	// disconnects a user managing the chassis through its local WiFi before the
	// asynchronous Prepare request can even return.
	b.WriteString("uci -q delete network.@device[0].ports; uci add_list network.@device[0].ports=ra0; uci add_list network.@device[0].ports=rax0; ")
	b.WriteString("for p in $LANP; do uci add_list network.@device[0].ports=$p.100; done; ")
	// Rebuilding br-lan's port list and reloading netifd detaches the MediaTek
	// radios (ra0/rax0 are driven by the private .dat stack, not netifd) and they
	// stop beaconing until something re-attaches them. Without this wifi reload the
	// client SSID stays down after Prepare until a full reboot. reload (never
	// restart) re-attaches the vifs to the freshly reconfigured bridge.
	b.WriteString("uci commit network; ubus call network reload; sleep 3; /sbin/wifi reload || true")
	return b.String()
}

func waitForEasyMeshManagementLeases(modules []ManagedModule, timeout time.Duration) (map[string]string, error) {
	wanted := make(map[string]bool, len(modules))
	for _, module := range modules {
		wanted[strings.ToUpper(module.MAC)] = true
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		found := make(map[string]string, len(wanted))
		leases, _ := readManagedAntennaDHCPLeases()
		for _, lease := range leases {
			mac := strings.ToUpper(lease.MAC)
			if wanted[mac] && strings.HasPrefix(lease.IP, "172.31.255.") {
				found[mac] = lease.IP
			}
		}
		if len(found) == len(wanted) {
			return found, nil
		}
		time.Sleep(time.Second)
	}
	return nil, errors.New("timed out waiting for every Antenna on the isolated 172.31.255.0/29 management network")
}

func rollbackEasyMeshNetwork() error {
	cfg := loadEasyMeshConfig()
	if cfg.Enabled {
		return errors.New("leave EasyMesh before restoring the pre-Mesh network")
	}
	return restorePreEasyMeshNetwork(cfg, false)
}

func restorePreEasyMeshNetwork(cfg EasyMeshConfig, resetTurnkey bool) error {
	modules, err := validatePreEasyMeshBackups()
	if err != nil {
		return err
	}
	if err := ensureLocalMediaTekWiFiServicesCompatibility(); err != nil {
		return fmt.Errorf("prepare Router wifi reload compatibility: %v", err)
	}
	setEasyMeshJobStage("Restoring Antenna network configurations")
	// Restore every Antenna best-effort. The Antenna restore worker is detached
	// (start-stop-daemon), so an ambiguous transport error ("ubus bad result
	// body=", timeout, connection reset) means it was almost certainly scheduled —
	// treat it as success. Critically, NO Antenna outcome (ambiguous, hard error,
	// or fully unreachable) may abort here: the AC's own restore below is the
	// invariant that keeps management reachable, so it must always run. A stranded
	// Antenna is recovered separately with a power-cycle and DHCPs back onto the
	// standalone AC. Previously a single flaky Antenna aborted the Leave before the
	// AC restore, stranding the AC in a half-migrated mesh state that a reboot then
	// brought up with a broken client bridge — locking the operator out entirely.
	var antennaRestoreErrs []string
	for _, module := range modules {
		if err := ensureRemoteMediaTekWiFiServicesCompatibility(module.IP); err != nil {
			if !easyMeshRemoteDispatchResultAmbiguous(err) {
				antennaRestoreErrs = append(antennaRestoreErrs, fmt.Sprintf("%s wifi reload compatibility: %v", module.ModuleID, err))
			}
			continue
		}
		// Antennas are plain client APs and never run the Turnkey stack, so their
		// restore is a pure config rollback with nothing to reset.
		remoteRestore := easyMeshRemoteRestoreCommand(false)
		if _, err := easyMeshExecRemote(module.IP, remoteRestore); err != nil && !easyMeshRemoteDispatchResultAmbiguous(err) {
			antennaRestoreErrs = append(antennaRestoreErrs, fmt.Sprintf("restore %s: %v", module.ModuleID, err))
		}
	}

	// ALWAYS restore the AC to a reachable standalone network, and always drop the
	// isolation marker, even if an Antenna could not be reached. leaveEasyMesh
	// relies on the marker being gone to persist the standalone role, so this is
	// what guarantees a Leave can never lock the operator out of the AC.
	setEasyMeshJobStage("Restoring AC network configuration")
	localRestore := easyMeshLocalRestoreCommand(resetTurnkey)
	if _, err := easyMeshExecLocal(localRestore); err != nil {
		return err
	}
	_ = os.Remove(AntennaManagementMarker)
	_ = os.Remove(easyMeshMigrationPath)
	// The restored network snapshot predates the client bridge MAC pin, so the
	// restore takes it away: br-lan drops back to inheriting the lowest member
	// address and this chassis changes identity. Measured after a Leave -- the
	// bridge came back as a random locally-administered address instead of the
	// Radio's, which is exactly the condition that used to break an Agent's DHCP
	// handoff. Startup would put it back, but nothing here reboots, so the unit
	// would stay unpinned through the next onboarding.
	pinClientBridgeMACAtStartup()
	cfg.NetworkPrepared = false
	if err := saveEasyMeshConfig(cfg); err != nil {
		return err
	}
	setEasyMeshJobStage("Verifying restored standalone configuration")
	// MediaTek DBDC can need more than a minute to expose the 5 GHz interface
	// after leaving Turnkey mode. Poll both the Router and every Antenna instead
	// of treating the first transiently missing ESSID as a failed rollback.
	verifyErr := verifyEasyMeshRestore(modules, 150*time.Second)
	if verifyErr != nil || len(antennaRestoreErrs) > 0 {
		// The AC is already standalone and reachable (config saved above); surface
		// any Antenna problems so the operator power-cycles them, without pretending
		// the Leave was fully clean.
		parts := make([]string, 0, 2)
		if len(antennaRestoreErrs) > 0 {
			parts = append(parts, "Antenna(s) need a power-cycle to recover: "+strings.Join(antennaRestoreErrs, "; "))
		}
		if verifyErr != nil {
			parts = append(parts, verifyErr.Error())
		}
		return fmt.Errorf("AC restored to standalone, but %s", strings.Join(parts, "; "))
	}
	return nil
}

const easyMeshWiFiServicesNeedle = `        local ssid_index = devs[devname]["vifs"][vif].vifidx`
const easyMeshWiFiServicesGuard = `        local lrap_dev = devs[devname]
        if lrap_dev == nil or lrap_dev["vifs"] == nil or lrap_dev["vifs"][vif] == nil then
            return
        end
        local ssid_index = lrap_dev["vifs"][vif].vifidx`

func patchMediaTekWiFiServicesContent(content string) (string, error) {
	if strings.Contains(content, `local lrap_dev = devs[devname]`) {
		return content, nil
	}
	if !strings.Contains(content, easyMeshWiFiServicesNeedle) {
		return "", errors.New("unsupported wifi_services.lua layout")
	}
	return strings.Replace(content, easyMeshWiFiServicesNeedle, easyMeshWiFiServicesGuard, 1), nil
}

func ensureLocalMediaTekWiFiServicesCompatibility() error {
	raw, err := os.ReadFile(easyMeshWiFiServicesPath)
	if err != nil {
		return err
	}
	patched, err := patchMediaTekWiFiServicesContent(string(raw))
	if err != nil || patched == string(raw) {
		return err
	}
	info, err := os.Stat(easyMeshWiFiServicesPath)
	if err != nil {
		return err
	}
	tmp := easyMeshWiFiServicesPath + ".lrap.tmp"
	if err := os.WriteFile(tmp, []byte(patched), info.Mode().Perm()); err != nil {
		return err
	}
	if err := os.Rename(tmp, easyMeshWiFiServicesPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func ensureRemoteMediaTekWiFiServicesCompatibility(ip string) error {
	content, err := easyMeshExecRemote(ip, "cat "+shellSingleQuote(easyMeshWiFiServicesPath))
	if err != nil {
		return err
	}
	patched, err := patchMediaTekWiFiServicesContent(content)
	if err != nil || patched == content {
		return err
	}
	if err := easyMeshWriteRemoteFile(ip, easyMeshWiFiServicesPath, patched); err != nil {
		return err
	}
	_, err = easyMeshExecRemote(ip, "chmod 0644 "+shellSingleQuote(easyMeshWiFiServicesPath)+"; grep -F 'local lrap_dev = devs[devname]' "+shellSingleQuote(easyMeshWiFiServicesPath)+" >/dev/null")
	return err
}

func validatePreEasyMeshBackups() ([]ManagedModule, error) {
	if _, err := easyMeshExecLocal(easyMeshValidateSnapshotCommand()); err != nil {
		return nil, fmt.Errorf("Controller pre-Mesh snapshot is incomplete: %v", err)
	}
	// Antennas must never be able to block a Leave.
	//
	// They are plain client APs and carry nothing the Mesh depends on, but a
	// chassis with none attached — or with one that is merely unreachable at this
	// moment — used to be refused here. That left the AC stranded in a half-torn
	// Mesh with no way out through the product: exactly the state a Leave exists
	// to escape. The AC's own snapshot, validated above, is the invariant that
	// makes recovery safe; an Antenna without a usable snapshot is simply skipped
	// and recovers on its own by DHCPing back onto the restored standalone AC.
	restorable := make([]ManagedModule, 0)
	for _, module := range discoverEasyMeshModules() {
		if _, err := easyMeshExecRemote(module.IP, easyMeshValidateSnapshotCommand()); err != nil {
			fmt.Printf("EasyMesh restore: skipping %s, no complete pre-Mesh snapshot: %v\n", module.ModuleID, err)
			continue
		}
		restorable = append(restorable, module)
	}
	return restorable, nil
}

func easyMeshValidateSnapshotCommand() string {
	var b strings.Builder
	b.WriteString("set -e; test -f " + shellSingleQuote(easyMeshBackupPath+"/complete") + "; ")
	for _, file := range easyMeshSnapshotFiles {
		if file.Optional {
			continue
		}
		b.WriteString("test -f " + shellSingleQuote(easyMeshBackupPath+"/"+file.Name) + "; ")
	}
	b.WriteString("echo complete")
	return b.String()
}

func easyMeshRestoreFilesCommand() string {
	var b strings.Builder
	b.WriteString(easyMeshValidateSnapshotCommand() + "; ")
	for _, file := range easyMeshSnapshotFiles {
		b.WriteString(easyMeshRestoreOneFile(file.Name, file.Source, file.Optional))
	}
	b.WriteString("sync; uci commit network; uci commit dhcp; ubus call network reload; /etc/init.d/dnsmasq restart")
	return b.String()
}

func easyMeshRestoreOneFile(name string, source string, optional bool) string {
	backup := shellSingleQuote(easyMeshBackupPath + "/" + name)
	if optional {
		return "if [ -f " + backup + " ]; then cp " + backup + " " + shellSingleQuote(source) + "; fi; "
	}
	return "cp " + backup + " " + shellSingleQuote(source) + "; "
}

func easyMeshRestoreRadioAndMapFilesCommand() string {
	var b strings.Builder
	for _, file := range easyMeshSnapshotFiles {
		if file.Name == "network" || file.Name == "dhcp" {
			continue
		}
		b.WriteString(easyMeshRestoreOneFile(file.Name, file.Source, file.Optional))
	}
	b.WriteString("sync")
	return b.String()
}

func easyMeshRuntimeWiFiSyncCommand() string {
	return `
lrap_sync_radio() {
	profile="$1"
	ifname="$2"
	ssid="$(sed -n 's/^SSID1=//p' "$profile" | head -n 1)"
	pass="$(sed -n 's/^WPAPSK1=//p' "$profile" | head -n 1)"
	auth="$(sed -n 's/^AuthMode=//p' "$profile" | head -n 1)"
	encryption="$(sed -n 's/^EncrypType=//p' "$profile" | head -n 1)"
	[ -n "$auth" ] && iwpriv "$ifname" set "AuthMode=$auth"
	[ -n "$encryption" ] && iwpriv "$ifname" set "EncrypType=$encryption"
	[ -z "$pass" ] || iwpriv "$ifname" set "WPAPSK=$pass"
	iwpriv "$ifname" set "SSID=$ssid"
	iwpriv "$ifname" set mapEnable=0
}
lrap_sync_radio ` + shellSingleQuote(Mtk24GPath) + ` ra0 || true
lrap_sync_radio ` + shellSingleQuote(Mtk5GPath) + ` rax0 || true
`
}

// easyMeshReattachRadiosCommand puts the MediaTek radios back into the restored
// client bridge.
//
// `wifi reload` on its own is not enough during a rollback. The Mesh bridges are
// still being torn down while it runs, the radios are therefore still enslaved
// elsewhere, and every `brctl addif br-lan <radio>` fails with "bridge br-lan:
// Resource busy" — measured in /tmp/lrap-wifi-restore.log. The radios end up in
// no bridge at all, so clients associate to a WiFi that carries no traffic and
// the portal itself becomes unreachable. That turns a failed onboarding, which
// is supposed to be harmless, into a bricked-looking device.
//
// So: detach the radios from any foreign bridge first, then reload, then verify
// and retry. A second reload succeeds once the Mesh bridges are really gone.
func easyMeshReattachRadiosCommand() string {
	return `
lrap_detach_foreign() {
	for ifc in ra0 rax0 apcli0 apclix0; do
		for brif in /sys/class/net/*/brif/$ifc; do
			[ -e "$brif" ] || continue
			br="$(echo "$brif" | cut -d/ -f5)"
			[ "$br" = "br-lan" ] && continue
			brctl delif "$br" "$ifc" >/dev/null 2>&1 || true
		done
	done
}
lrap_radios_attached() {
	[ -e /sys/class/net/br-lan/brif/ra0 ] && [ -e /sys/class/net/br-lan/brif/rax0 ]
}
lrap_reattach_radios() {
	i=0
	while [ $i -lt 4 ]; do
		lrap_radios_attached && return 0
		lrap_detach_foreign
		/sbin/wifi reload >>/tmp/lrap-wifi-restore.log 2>&1 || true
		sleep 8
		i=$((i + 1))
	done
	lrap_radios_attached
}
lrap_reattach_radios || true
`
}

func easyMeshRestoreWorker(resetTurnkey bool, antenna bool) string {
	actions := easyMeshRestoreFilesCommand()
	if resetTurnkey {
		mode := "0"
		if antenna {
			mode = "1"
		}
		// MediaTek's leave helper and wifi reload can return 1 while their
		// asynchronous driver teardown/rebuild is still succeeding. The snapshot
		// copy is the authoritative standalone configuration, so always restore it
		// again after the helper and let the explicit persistent/runtime verifier
		// decide whether recovery actually completed.
		actions += "; /usr/bin/EasyMesh_openwrt.sh lrap " + mode + " >/tmp/lrap-easymesh-leave.log 2>&1 || true; " + easyMeshRestoreRadioAndMapFilesCommand() + "; /sbin/wifi reload >/tmp/lrap-wifi-restore.log 2>&1 || true; " + easyMeshReattachRadiosCommand() + easyMeshRuntimeWiFiSyncCommand()
	}
	// Run the actions in their own shell. Keeping set -e inside that process
	// avoids the POSIX exception where errexit is suppressed for a compound
	// command used directly on the left side of || or as an if condition.
	strict := "/bin/sh -c " + shellSingleQuote("set -e; "+actions)
	return "rm -f " + easyMeshRestoreReady + " " + easyMeshRestoreFailed + "; code=0; " + strict + " || code=$?; if [ $code -eq 0 ]; then touch " + easyMeshRestoreReady + "; rm -f " + easyMeshRestoreFailed + "; else echo $code >" + easyMeshRestoreFailed + "; exit $code; fi"
}

func easyMeshRemoteRestoreCommand(resetTurnkey bool) string {
	// Schedule the Antenna first, but keep its current tagged path alive long
	// enough for the main module to restore its bridge immediately below. If the
	// Antenna restores first, the backend loses the only management path and can
	// never restore the main module.
	worker := "sleep 8; " + easyMeshRestoreWorker(resetTurnkey, true)
	return "set -e; " + easyMeshValidateSnapshotCommand() + "; rm -f " + easyMeshRestorePID + "; /sbin/start-stop-daemon -S -b -m -p " + easyMeshRestorePID + " -x /bin/sh -- -c " + shellSingleQuote(worker+" >/tmp/lrap-mesh-rollback.log 2>&1") + "; echo scheduled"
}

func easyMeshLocalRestoreCommand(resetTurnkey bool) string {
	return "set -e; " + easyMeshRestoreWorker(resetTurnkey, false)
}

func easyMeshVerifyRestoreCommand() string {
	var b strings.Builder
	b.WriteString("set -e; test -f " + easyMeshRestoreReady + "; ")
	for _, file := range easyMeshSnapshotFiles {
		// 1905d.cfg holds the EasyMesh 1905 identity. EasyMesh_openwrt.sh
		// regenerates several of its fields (not only the two AL-MAC lines) from
		// the current hardware MAC on every Turnkey reset, so even a filtered
		// byte comparison makes a fully successful standalone restore look failed.
		// The authoritative mesh-config restore is already covered by the
		// mapd_user.cfg / mapd_default.cfg / wts_bss_info_config compares below.
		// Optional files may legitimately be missing from an older snapshot, so
		// comparing them would fail a restore that in fact succeeded.
		if file.Name == "1905d.cfg" || file.Optional {
			continue
		}
		b.WriteString("cmp -s " + shellSingleQuote(easyMeshBackupPath+"/"+file.Name) + " " + shellSingleQuote(file.Source) + "; ")
	}
	b.WriteString("! pidof mapd >/dev/null; ")
	// The 2.4GHz client radio reloads quickly and is a reliable "left Turnkey"
	// signal, so require its restored ESSID exactly. MediaTek DBDC can take well
	// over two minutes to re-expose the 5GHz client BSS on rax0; racing its
	// runtime ESSID previously made a fully successful standalone restore report
	// as failed, so for 5GHz only require the reloaded interface to exist.
	b.WriteString("ssid=\"$(sed -n 's/^SSID1=//p' " + shellSingleQuote(Mtk24GPath) + " | head -n 1)\"; ")
	b.WriteString("iwconfig ra0 2>/dev/null | grep -F \"ESSID:\\\"$ssid\\\"\" >/dev/null; ")
	b.WriteString("test -d /sys/class/net/rax0; echo restored")
	return b.String()
}

func verifyEasyMeshRestore(expected []ManagedModule, timeout time.Duration) error {
	wanted := make(map[string]bool, len(expected))
	for _, module := range expected {
		wanted[module.ModuleID] = true
	}
	deadline := time.Now().Add(timeout)
	lastErrors := make(map[string]string)
	for time.Now().Before(deadline) {
		localVerified := false
		if _, err := easyMeshExecLocal(easyMeshVerifyRestoreCommand()); err != nil {
			lastErrors["router"] = err.Error()
		} else {
			localVerified = true
			delete(lastErrors, "router")
		}
		verified := make(map[string]bool, len(wanted))
		for _, module := range discoverManagedAPs(true) {
			if !wanted[module.ModuleID] {
				continue
			}
			if _, err := easyMeshExecRemote(module.IP, easyMeshVerifyRestoreCommand()); err != nil {
				lastErrors[module.ModuleID] = err.Error()
				continue
			}
			verified[module.ModuleID] = true
			delete(lastErrors, module.ModuleID)
		}
		if localVerified && len(verified) == len(wanted) {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("not every Antenna returned with its verified standalone configuration: %v", lastErrors)
}

func activateEasyMesh() (retErr error) {
	cfg := loadEasyMeshConfig()
	wasEnabled := cfg.Enabled
	if cfg.Role != "controller" && cfg.Role != "agent" {
		return errors.New("save a Controller or Agent role first")
	}
	if !cfg.NetworkPrepared {
		return errors.New("prepare VLAN separation before enabling EasyMesh")
	}
	if err := ensureEasyMeshManagementBridge(); err != nil {
		return err
	}
	if err := easyMeshLocalCapability(); err != nil {
		return err
	}
	credentials, err := easyMeshLocalFronthaulCredentials()
	if err != nil {
		return err
	}
	if err := easyMeshCheckBackhaulChannelNonDFS(cfg.BackhaulBand); err != nil {
		return err
	}
	if !easyMeshLocalFronthaulInterfacesPresent() {
		return errors.New("prepare the Mesh Radio and let the required reboot finish before enabling EasyMesh")
	}
	if cfg.Role == "controller" {
		if out, err := easyMeshExecLocal("test \"$(uci -q get dhcp.lan.ignore)\" != 1"); err != nil {
			return fmt.Errorf("Controller client DHCP must be enabled: %v %s", err, out)
		}
	}
	defer func() {
		if retErr == nil || wasEnabled {
			return
		}
		original := retErr
		setEasyMeshJobStage("Activation failed; restoring standalone operation")
		if rollbackErr := rollbackFailedEasyMeshOperation(cfg); rollbackErr != nil {
			retErr = fmt.Errorf("%v; automatic rollback also failed: %v", original, rollbackErr)
			return
		}
		retErr = fmt.Errorf("%v; all pre-Mesh configuration was restored automatically", original)
	}()

	setEasyMeshJobStage("Taking Antenna submodules out of Mesh mode")
	ensureAntennasOutOfMeshMode()

	setEasyMeshJobStage("Configuring local EasyMesh engine")
	if err := configureLocalEasyMeshTurnkey(cfg.Role, cfg.BackhaulBand, credentials); err != nil {
		return err
	}

	setEasyMeshJobStage("Starting MediaTek Turnkey")
	if _, err := easyMeshExecLocal(easyMeshStartCommandWithReload(cfg.Role, cfg.Role == "agent")); err != nil {
		return fmt.Errorf("schedule local EasyMesh start: %v", err)
	}

	setEasyMeshJobStage("Verifying MediaTek Turnkey")
	if err := waitForEasyMeshLocalStart(60 * time.Second); err != nil {
		return fmt.Errorf("local EasyMesh start: %v", err)
	}
	if !easyMeshLocalFronthaulInterfacesPresent() {
		return errors.New("the Mesh Radio did not expose its Fronthaul BSS after wifi reload")
	}
	// Keep an Agent's current management address and client DHCP alive until the
	// wireless Backhaul has actually joined the Root. Cutting either one here
	// would make the onboarding button unreachable before pairing happens.
	cfg.AgentHandoff = false
	cfg.OnboardingAt = 0
	cfg.Enabled = true
	return saveEasyMeshConfig(cfg)
}

func waitForEasyMeshBackhaulModule(moduleID string, timeout time.Duration) (ManagedModule, error) {
	return waitForEasyMeshBackhaulModuleWithDiscovery(moduleID, timeout, 2*time.Second, discoverEasyMeshModules)
}

func waitForEasyMeshBackhaulModuleWithDiscovery(
	moduleID string,
	timeout time.Duration,
	retryInterval time.Duration,
	discover func() []ManagedModule,
) (ManagedModule, error) {
	deadline := time.Now().Add(timeout)
	for {
		if backhaul, ok := managedAPByModuleID(discover(), moduleID); ok && strings.TrimSpace(backhaul.IP) != "" {
			return backhaul, nil
		}

		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		delay := retryInterval
		if delay <= 0 || delay > remaining {
			delay = remaining
		}
		time.Sleep(delay)
	}

	return ManagedModule{}, fmt.Errorf("the selected backhaul Antenna did not become reachable within %s", timeout.Round(time.Second))
}

func rollbackFailedEasyMeshOperation(cfg EasyMeshConfig) error {
	restoreErr := restorePreEasyMeshNetwork(cfg, true)
	// Do not claim Standalone when recovery failed before the isolated
	// management network was removed. Once the ordinary LAN is back, always
	// persist Standalone even if a Radio runtime check still fails. Otherwise a
	// reboot would resume the stale Agent role and undo the safety rollback.
	if restoreErr != nil && antennaManagementIsIsolated() {
		return restoreErr
	}
	cfg.Enabled = false
	cfg.Role = "standalone"
	cfg.AgentHandoff = false
	cfg.OnboardingAt = 0
	cfg.NetworkPrepared = false
	if saveErr := saveEasyMeshConfig(cfg); saveErr != nil {
		if restoreErr != nil {
			return fmt.Errorf("%v; persist standalone state: %v", restoreErr, saveErr)
		}
		return saveErr
	}
	return restoreErr
}

func easyMeshDatValues(content string) map[string]string {
	values := make(map[string]string)
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	return values
}

func easyMeshFirstListValue(value string) string {
	if first, _, ok := strings.Cut(value, ";"); ok {
		return strings.TrimSpace(first)
	}
	return strings.TrimSpace(value)
}

func easyMeshCredentialFromContent(content string) (easyMeshFronthaulCredential, error) {
	values := easyMeshDatValues(content)
	credential := easyMeshFronthaulCredential{
		SSID:       strings.TrimSpace(values["SSID1"]),
		Passphrase: strings.TrimSpace(values["WPAPSK1"]),
		AuthMode:   easyMeshFirstListValue(values["AuthMode"]),
		Encryption: easyMeshFirstListValue(values["EncrypType"]),
	}
	if credential.SSID == "" || credential.Passphrase == "" || credential.AuthMode == "" || credential.Encryption == "" {
		return credential, errors.New("the saved client WiFi profile is incomplete")
	}
	return credential, nil
}

// easyMeshUnifiedFronthaulSet collapses the two per-band client profiles into a
// single credential used on BOTH Radios.
//
// A Mesh needs ONE SSID. wapp runs "concurrent WPS": during onboarding the
// 2.4GHz and 5GHz Backhaul stations of the same card BOTH hunt for a registrar
// at the same time. With a different SSID per band they find two different
// registrars and the state machine restarts forever — the driver logs
// "will connect <ssid> on <ch>" and is then torn down with WscStop/ApCliEnable=0
// without ever entering JOIN, and the Root never sees a single WSC frame.
// Giving both Radios the same SSID is what makes the association happen at all.
//
// The 2.4GHz name is the base one (the 5GHz profile normally carries a "_5G"
// suffix), so it becomes the Mesh-wide SSID; clients are steered between bands
// by the Mesh itself. The 5GHz profile is used only if 2.4GHz has no SSID.
func easyMeshUnifiedFronthaulSet(twoG easyMeshFronthaulCredential, fiveG easyMeshFronthaulCredential) easyMeshFronthaulSet {
	unified := twoG
	if strings.TrimSpace(unified.SSID) == "" {
		unified = fiveG
	}
	// Keep each Radio's own security settings; only the identity is shared.
	twoGOut, fiveGOut := twoG, fiveG
	twoGOut.SSID, twoGOut.Passphrase = unified.SSID, unified.Passphrase
	fiveGOut.SSID, fiveGOut.Passphrase = unified.SSID, unified.Passphrase
	return easyMeshFronthaulSet{TwoG: twoGOut, FiveG: fiveGOut}
}

func easyMeshLocalFronthaulCredentials() (easyMeshFronthaulSet, error) {
	readCredential := func(activePath string, backupName string) (easyMeshFronthaulCredential, error) {
		raw, err := os.ReadFile(activePath)
		if err != nil {
			return easyMeshFronthaulCredential{}, err
		}
		credential, credentialErr := easyMeshCredentialFromContent(string(raw))
		if credentialErr == nil && !strings.HasPrefix(strings.ToLower(credential.SSID), "multi-ap-") {
			return credential, nil
		}
		backup, backupErr := os.ReadFile(filepath.Join(easyMeshBackupPath, backupName))
		if backupErr != nil {
			return easyMeshFronthaulCredential{}, credentialErr
		}
		return easyMeshCredentialFromContent(string(backup))
	}

	twoG, err := readCredential(Mtk24GPath, "wifi-2g.dat")
	if err != nil {
		return easyMeshFronthaulSet{}, fmt.Errorf("read 2.4GHz client profile: %v", err)
	}
	fiveG, err := readCredential(Mtk5GPath, "wifi-5g.dat")
	if err != nil {
		return easyMeshFronthaulSet{}, fmt.Errorf("read 5GHz client profile: %v", err)
	}
	return easyMeshUnifiedFronthaulSet(twoG, fiveG), nil
}

// easyMeshFronthaulProfileUpdates builds the driver-profile keys for one Radio.
//
// writeCredential must be false on an Agent. The known-good vendor recipe writes
// SSID/WPAPSK only on the Controller and leaves the Agent's profile alone —
// "SSID is pushed down by the Controller". Writing a local credential on the
// Agent gives its Backhaul station a profile the Controller never issued, which
// is a deviation from the sequence that is known to pair on this firmware.
func easyMeshFronthaulProfileUpdates(content string, credential easyMeshFronthaulCredential, writeCredential bool) map[string]string {
	values := easyMeshDatValues(content)
	firstAuth := easyMeshFirstListValue(values["AuthMode"])
	if firstAuth == "" {
		firstAuth = credential.AuthMode
	}
	firstEncryption := easyMeshFirstListValue(values["EncrypType"])
	if firstEncryption == "" {
		firstEncryption = credential.Encryption
	}
	// Every Radio — Controller, Controller Antenna and Agent alike — keeps exactly
	// ONE BSS. This mirrors the MediaTek factory turnkey layout (BssidNum=1: only
	// ra0/rax0 plus the apcli0/apclix0 Backhaul STA) and is what makes wireless
	// onboarding work at all.
	//
	// The previous design reserved a second BSS (BssidNum=2 -> ra1/rax1) as a
	// dedicated Multi-AP Backhaul AP. That put three VIFs on the shared 5GHz
	// Radio, and the two beaconing APs starved the Backhaul STA's WPS scan
	// ("ApSiteSurvey_by_wdev(): TakeChannelOpCharge fail for SCAN" ->
	// "[apclix0][IDLE][SCAN_TIMEOUT]"), so a joining chassis could never see the
	// registrar. With BssidNum=1 the single fronthaul BSS doubles as the Backhaul
	// AP, exactly as the vendor firmware does — see easyMeshBackhaulBSSInterface.
	//
	// SREnable/SRMode must be 0 whenever MapMode=1. 802.11ax Spatial Reuse inserts
	// an HE SR information element that corrupts the IE offsets in the WPS M8
	// credential, and the enrollee then fails every single pairing attempt with
	// "WscProcessCredential() 445: unexpected WSC IE Length(<random>)" while the
	// M1-M8 exchange otherwise completes. The vendor LuCI applies the same forced
	// override in save_easymesh_driver_profile().
	// SSID1/WPAPSK1 are pinned to the same client credential that is written into
	// the Controller's BSS policy (wts_bss_info_config). Once mapd applies that
	// policy it overwrites the driver profile with the policy's SSID/passphrase,
	// so keeping both sources identical is what stops a foreign name (the factory
	// "Multi-AP-1") from ever appearing on air.
	// WscConfMode/WscConfStatus must be written HERE (during Prepare) and not only
	// at Turnkey time, because they only take effect when the proprietary driver
	// initialises at boot — and Prepare owns the mandatory reboot.
	//
	// WscConfMode=0 is the factory default and means "WPS disabled in the driver
	// profile". With it, the Backhaul STA scans, finds the registrar's PBC beacon
	// and even logs "will connect <SSID> on <ch>", but the association request is
	// never transmitted: the registrar sees nothing at all. Setting 7
	// (enrollee|proxy|registrar) plus WscConfStatus=2 (configured) is what makes
	// the full M1..M8 EAP exchange run. The vendor LuCI writes exactly these two
	// values from __set_wifi_wpsconf when WPS is enabled.
	updates := map[string]string{
		"BssidNum":      "1",
		"SSID2":         "",
		"WPAPSK2":       "",
		"SREnable":      "0",
		"SRMode":        "0",
		"WscConfMode":   "7",
		"WscConfStatus": "2",
		"AuthMode":      firstAuth,
		"EncrypType":    firstEncryption,
		"HideSSID":      "0",
		"NoForwarding":  "0",
		"IEEE8021X":     "0",
		"PMFMFPC":       "1",
		"PMFMFPR":       "0",
		"PMFSHA256":     "0",
		"RekeyMethod":   "TIME",
		"RekeyInterval": "3600",
		"WmmCapable":    "1",
	}
	if writeCredential {
		updates["SSID1"] = credential.SSID
		updates["WPAPSK1"] = credential.Passphrase
	}
	return updates
}

// easyMeshTurnkeyRadioUpdates returns the driver-profile keys that every Radio
// must carry while MediaTek turnkey (MapMode=1) is active. The card-level
// profile (DBDC_card0.dat) uses one list entry per band, so its values are
// doubled.
func easyMeshTurnkeyRadioUpdates(cardLevel bool, role string) map[string]string {
	if cardLevel {
		updates := map[string]string{
			"MapMode": "1", "SREnable": "0;0", "SRMode": "0;0",
		}
		// The vendor recipe writes the card-level WPS keys only on the Controller;
		// an Agent gets them on the per-band profiles alone.
		if role == "controller" {
			updates["WscConfMode"] = "7;7"
			updates["WscConfStatus"] = "2;2"
		}
		return updates
	}
	return map[string]string{
		"MapMode": "1", "SREnable": "0", "SRMode": "0",
		"WscConfMode": "7", "WscConfStatus": "2",
	}
}

func updateLocalEasyMeshFronthaulProfile(path string, credential easyMeshFronthaulCredential, writeCredential bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return updateLocalMtkDatFile(path, easyMeshFronthaulProfileUpdates(string(raw), credential, writeCredential))
}

func updateRemoteEasyMeshFronthaulProfile(ip string, path string, credential easyMeshFronthaulCredential) error {
	content, err := easyMeshExecRemote(ip, "cat "+shellSingleQuote(path))
	if err != nil {
		return err
	}
	return updateRemoteEasyMeshConfigFile(ip, path, easyMeshFronthaulProfileUpdates(content, credential, true))
}

// wts_bss_info_config groups its rows by band: rows 1-4 are 5GHz-low ("11x"),
// 5-8 are 2.4GHz ("8x"), 9-12 are 5GHz-high ("12x") and 13-16 are 6GHz ("13x").
// The FIRST row of each band is the one MediaTek instantiates on a BssidNum=1
// radio; the rest exist for multi-BSS models this product does not ship.
var easyMeshBSSPolicyPrimarySlots = []int{1, 5, 9}

// easyMeshBSSPolicySlot maps a BSS policy row to the client credential it must
// carry, and reports whether the row is its band's primary (single) BSS.
func easyMeshBSSPolicySlot(id int, credentials easyMeshFronthaulSet) (easyMeshFronthaulCredential, bool, bool) {
	switch id {
	case 1, 9:
		return credentials.FiveG, true, true
	case 5:
		return credentials.TwoG, true, true
	case 2, 10:
		return credentials.FiveG, false, true
	case 6:
		return credentials.TwoG, false, true
	}
	return easyMeshFronthaulCredential{}, false, false
}

func updateEasyMeshBSSPolicyContent(content string, credentials easyMeshFronthaulSet) (string, error) {
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	updated := make(map[int]bool)
	for index, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 8 || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		idText, _, _ := strings.Cut(fields[0], ",")
		id, err := strconv.Atoi(idText)
		if err != nil {
			continue
		}
		credential, primary, wanted := easyMeshBSSPolicySlot(id, credentials)
		if !wanted {
			continue
		}
		if strings.ContainsAny(credential.SSID, " \t\r\n") || strings.ContainsAny(credential.Passphrase, " \t\r\n") {
			return "", errors.New("client SSID and passphrase cannot contain whitespace in the MediaTek BSS policy")
		}
		fields[2] = credential.SSID
		fields[5] = credential.Passphrase
		// The two trailing flags are "bh_bss fh_bss" — backhaul first, fronthaul
		// second (proved by wapp's own debug format string: "... key=%s, bh_bss=%s,
		// fh_bss=%s hidden_ssid=%d"). The factory primary row per band ships as
		// "1 0" = backhaul-only, which mapd applies as DevOwnRole=0x40 and which
		// rejects every ordinary client with DEAUTH ReasonCode 37.
		//
		// With BssidNum=1 there is only one BSS per band, so that single BSS must
		// serve both duties: "1 1" -> DevOwnRole=0x60 (0x20 fronthaul | 0x40
		// backhaul). Secondary rows stay pure fronthaul; they are never
		// instantiated at BssidNum=1 but are kept in sync so they carry the right
		// credential if a future radio ever exposes them.
		if primary {
			fields[6] = "1"
			fields[7] = "1"
		} else {
			fields[6] = "0"
			fields[7] = "1"
		}
		// Strip the MAP Traffic Separation VLAN. The row tail is
		// "hidden-N <vid> <pvid…>" and the factory primary row ships as
		// "hidden-N 4095 pvid 5", which asks the driver to tag this BSS's client
		// traffic with VLAN 5. Traffic Separation exists to carry several logical
		// networks over one Mesh, but this product already separates planes itself
		// with VLAN 100/200 on a bridge that has no VLAN filtering, so a driver-side
		// tag has nothing to match. Clearing it to "N/A N/A" — the same tail the
		// factory uses on every non-primary row — keeps MAP out of the VLAN
		// business entirely.
		if len(fields) >= 12 {
			fields[10] = "N/A"
			fields[11] = "N/A"
		}
		lines[index] = strings.Join(fields, " ")
		updated[id] = true
	}
	for _, id := range easyMeshBSSPolicyPrimarySlots {
		if !updated[id] {
			return "", fmt.Errorf("MediaTek BSS policy is missing primary slot %d", id)
		}
	}
	return strings.TrimRight(strings.Join(lines, "\n"), "\n") + "\n", nil
}

func configureLocalEasyMeshBSSPolicy(credentials easyMeshFronthaulSet) error {
	raw, err := os.ReadFile(easyMeshBSSPolicyPath)
	if err != nil {
		return err
	}
	updated, err := updateEasyMeshBSSPolicyContent(string(raw), credentials)
	if err != nil {
		return err
	}
	return os.WriteFile(easyMeshBSSPolicyPath, []byte(updated), 0600)
}

func configureLocalEasyMeshTurnkey(role string, band string, credentials easyMeshFronthaulSet) error {
	for _, path := range []string{Mtk24GPath, Mtk5GPath} {
		if err := updateLocalMtkDatFile(path, easyMeshTurnkeyRadioUpdates(false, role)); err != nil {
			return err
		}
	}
	// The card-level profile is absent on some images; keep it best-effort.
	if _, err := os.Stat(easyMeshCardProfilePath); err == nil {
		if err := updateLocalMtkDatFile(easyMeshCardProfilePath, easyMeshTurnkeyRadioUpdates(true, role)); err != nil {
			return fmt.Errorf("configure card profile: %v", err)
		}
	}
	if role == "controller" {
		if err := configureLocalEasyMeshBSSPolicy(credentials); err != nil {
			return fmt.Errorf("configure Controller Fronthaul policy: %v", err)
		}
	}
	return updateLocalEasyMeshMapdRole(role, band)
}

// ensureAntennasOutOfMeshMode takes every Antenna submodule back out of
// MediaTek turnkey mode.
//
// The Backhaul is carried by the main modules' own Radios, so Antennas must be
// plain client-serving APs. Leaving them at MapMode=1 makes each one an extra
// 1905 Agent in the topology, which is what produced the four-device
// "agent-to-agent" layout whose WPS onboarding could never complete. Failures
// are reported but never abort the caller: an unreachable Antenna must not stop
// the main modules from forming the Mesh.
func ensureAntennasOutOfMeshMode() {
	for _, module := range discoverEasyMeshModules() {
		if strings.TrimSpace(module.IP) == "" {
			continue
		}
		for _, path := range []string{Mtk24GPath, Mtk5GPath, easyMeshCardProfilePath} {
			if _, err := easyMeshExecRemote(module.IP, "test -f "+shellSingleQuote(path)); err != nil {
				continue
			}
			if err := updateRemoteEasyMeshConfigFile(module.IP, path, map[string]string{"MapMode": "0"}); err != nil {
				fmt.Printf("EasyMesh: leaving %s out of Mesh mode failed for %s: %v\n", module.ModuleID, path, err)
			}
		}
	}
}

// MediaTek's boot helper (EasyMesh_openwrt.sh) copies mapd_default.cfg over
// mapd_cfg on every start, then re-applies mapd_user.cfg on top. Writing only
// mapd_user.cfg leaves a stale DeviceRole sitting in mapd_default.cfg, so a
// Controller whose user overlay fails to apply comes back up as an Agent. Write
// all three: the persistent default, the user overlay, and the live runtime copy.
func easyMeshMapdRolePaths() []string {
	return []string{easyMeshMapdUserPath, easyMeshMapdDefaultPath, easyMeshMapdRuntimePath}
}

func updateLocalEasyMeshMapdRole(role string, band string) error {
	updates := easyMeshExpectedMapdUpdates(role, band)
	for _, path := range easyMeshMapdRolePaths() {
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if err := updateLocalEasyMeshConfigFile(path, updates); err != nil {
			return fmt.Errorf("configure %s: %v", filepath.Base(path), err)
		}
	}
	return updateLocalEasyMesh1905Version()
}

// easyMesh1905Updates pins the Multi-AP protocol version used on the 1905
// control plane.
//
// R3 must NOT be used. Measured on the device: with map_ver=R3 the Root builds a
// 271-byte WPS M8 credential payload that this firmware's enrollee cannot parse
// — the full M1..M8 exchange completes and then dies in WscProcessCredential
// with "unexpected WSC IE Length(<garbage>)" -> "0 profile retrieved from
// credential", and the Root immediately deauthenticates the joining chassis.
// With R2 the payload is a single 75-byte credential and the same onboarding
// sequence pairs on the first attempt.
func easyMesh1905Updates() map[string]string {
	return map[string]string{"map_ver": "R2"}
}

func updateLocalEasyMesh1905Version() error {
	if _, err := os.Stat(easyMesh1905Path); err != nil {
		return nil
	}
	if err := updateLocalEasyMeshConfigFile(easyMesh1905Path, easyMesh1905Updates()); err != nil {
		return fmt.Errorf("configure %s: %v", filepath.Base(easyMesh1905Path), err)
	}
	return nil
}

func updateRemoteEasyMeshMapdRole(ip string, role string, band string) error {
	updates := easyMeshExpectedMapdUpdates(role, band)
	for _, path := range easyMeshMapdRolePaths() {
		if _, err := easyMeshExecRemote(ip, "test -f "+shellSingleQuote(path)); err != nil {
			continue
		}
		if err := updateRemoteEasyMeshConfigFile(ip, path, updates); err != nil {
			return fmt.Errorf("configure %s: %v", filepath.Base(path), err)
		}
	}
	return nil
}

// EasyMesh manages Antennas through the restricted anonymous ubus command
// channel. Some firmware images allow file.exec but intentionally deny
// anonymous file.read, so read and verify through the same command channel used
// by the rest of the EasyMesh remote workflow.
func updateRemoteEasyMeshConfigFile(ip string, path string, updates map[string]string) error {
	readCommand := "cat " + shellSingleQuote(path)
	content, err := easyMeshExecRemote(ip, readCommand)
	if err != nil {
		return fmt.Errorf("read %s: %v", path, err)
	}

	updated := updateEasyMeshConfigContent(path, content, updates)
	if err := easyMeshWriteRemoteFile(ip, path, updated); err != nil {
		return fmt.Errorf("write %s: %v", path, err)
	}

	verified, err := easyMeshExecRemote(ip, readCommand)
	if err != nil {
		return fmt.Errorf("verify %s: %v", path, err)
	}
	if err := verifyEasyMeshConfigUpdates(verified, updates); err != nil {
		return fmt.Errorf("verify %s: %v", path, err)
	}
	return nil
}

func updateLocalEasyMeshConfigFile(path string, updates map[string]string) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updated := updateEasyMeshConfigContent(path, string(content), updates)
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		return err
	}
	verified, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return verifyEasyMeshConfigUpdates(string(verified), updates)
}

func updateEasyMeshConfigContent(path string, content string, updates map[string]string) string {
	if path == easyMeshMapdUserPath {
		return updateEasyMeshMapdUserContent(content, updates)
	}
	return updateMtkDatContent(content, updates)
}

func updateEasyMeshMapdUserContent(content string, updates map[string]string) string {
	values := make(map[string]string)
	order := make([]string, 0)
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if !seen[key] {
			order = append(order, key)
			seen[key] = true
		}
		values[key] = strings.TrimSpace(value)
	}
	missing := make([]string, 0)
	for key, value := range updates {
		values[key] = value
		if !seen[key] {
			missing = append(missing, key)
			seen[key] = true
		}
	}
	sort.Strings(missing)
	order = append(order, missing...)

	lines := []string{"###!UserConfigs!!!"}
	for _, key := range order {
		lines = append(lines, key+"="+values[key])
	}
	return strings.Join(lines, "\n") + "\n"
}

func verifyEasyMeshConfigUpdates(content string, updates map[string]string) error {
	values := make(map[string]string)
	for _, line := range strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		values[strings.TrimSpace(key)] = strings.TrimSpace(value)
	}
	for key, expected := range updates {
		if actual, ok := values[key]; !ok || actual != expected {
			return fmt.Errorf("%s=%q, want %q", key, actual, expected)
		}
	}
	return nil
}

func easyMeshExpectedMapdUpdates(role string, band string) map[string]string {
	roleValue := "2"
	modeValue := "1"
	if role == "controller" {
		roleValue = "1"
		modeValue = "0"
	}
	return easyMeshMapdUpdates(modeValue, roleValue, band)
}

func easyMeshRuntimeConfigMatches(content string, role string, band string) bool {
	return verifyEasyMeshConfigUpdates(content, easyMeshExpectedMapdUpdates(role, band)) == nil
}

func easyMeshLocalRuntimeMatches(role string, band string) bool {
	content, err := os.ReadFile(easyMeshMapdRuntimePath)
	if err != nil || !easyMeshRuntimeConfigMatches(string(content), role, band) {
		return false
	}
	// A unit upgraded from a build that never pinned the Multi-AP version can be
	// running an otherwise correct engine on R3, where onboarding always fails in
	// WscProcessCredential. Treat that as out of sync so reconciliation fixes it.
	return easyMeshLocal1905VersionMatches()
}

func easyMeshLocal1905VersionMatches() bool {
	content, err := os.ReadFile(easyMesh1905Path)
	if err != nil {
		// The file is absent on images without the 1905 stack; nothing to reconcile.
		return true
	}
	return verifyEasyMeshConfigUpdates(string(content), easyMesh1905Updates()) == nil
}

func easyMeshRemoteRuntimeMatches(ip string, role string, band string) bool {
	content, err := easyMeshExecRemote(ip, "cat "+shellSingleQuote(easyMeshMapdRuntimePath))
	return err == nil && easyMeshRuntimeConfigMatches(content, role, band)
}

func easyMeshMapdUpdates(mode string, role string, band string) map[string]string {
	updates := map[string]string{
		// MapMode is the mapd CONTROL-PLANE switch and is separate from the
		// MapMode in the driver .dat files. mapd/LuCI decide "is mesh enabled"
		// from this key; writing only the .dat leaves the driver looking meshed
		// while the control plane still believes MapMode=0 — a split brain with no
		// backhaul. The factory images ship without the key at all (only
		// LastMapMode), so it has to be added, not just rewritten.
		"MapMode": "1",
		"mode":    mode, "DeviceRole": role,
		// DhcpCtl and BhPriority* are deliberately NOT written.
		//
		// The factory images ship BhPriority2G=3 (Low) with 5GL/5GH=1 (High), so
		// 5GHz is already preferred, and the known-good vendor recipe never touches
		// these keys. This code used to force BhPriority2G=0 — a value outside the
		// 1..3 range the firmware itself uses — on the assumption that 0 means
		// "disable"; that assumption is unverified and mapd was observed bringing
		// the 2.4GHz station up instead of the selected 5GHz one. The Backhaul band
		// is enforced where it actually takes effect: the WPS registrar and
		// enrollee are armed on the selected band's interfaces only.
		//
		// DhcpCtl is what lets mapd hand the Agent's client network over to the
		// Root by itself. The factory value differs per file (0 in mapd_cfg, 1 in
		// mapd_default.cfg); forcing 0 everywhere suppressed firmware behaviour the
		// vendor flow relies on.
	}
	_ = band
	return updates
}

func easyMeshStartCommand(role string) string {
	return easyMeshStartCommandWithReload(role, false)
}

func easyMeshStartCommandWithReload(role string, reloadWiFi bool) string {
	mode := "1"
	if role == "controller" {
		mode = "0"
	}
	// rpcd file.exec waits for ordinary shell background jobs, so "... &" can
	// still time out and lose its HTTP response. BusyBox start-stop-daemon -b
	// performs a real detach: rpcd returns before the MediaTek script rebuilds
	// wireless bridges. Result markers keep the detached work verifiable.
	prefix := "sleep 5; "
	if reloadWiFi {
		// Never use wifi restart. reload applies the saved SSID/policy while allowing
		// the driver and Turnkey engine to recover the saved backhaul profile.
		prefix += "wifi reload; sleep 8; "
	}
	// MAP Traffic Separation must be off on both Radios. It is a Multi-AP R2
	// feature for carrying several logical networks over one Mesh by VLAN-tagging
	// each Fronthaul BSS, driven by the `pvid` field in wts_bss_info_config. This
	// product already separates planes itself with VLAN 100/200 on a bridge that
	// has no VLAN filtering, so the driver's tagging has nothing to match: on-device
	// clients associated fine and ARP resolved, but every unicast IP packet was
	// dropped. It must be re-applied after each start because `wifi reload`
	// restores it from the profile.
	tsOff := "iwpriv " + Mtk24GIfName + " set mapTSEnable=0; iwpriv " + Mtk5GIfName + " set mapTSEnable=0; "
	worker := prefix + "if /usr/bin/EasyMesh_openwrt.sh lrap " + mode +
		" >/tmp/lrap-easymesh.log 2>&1; then sleep 5; " + tsOff + "touch " + easyMeshStartReadyPath +
		"; else code=$?; echo $code >" + easyMeshStartFailedPath + "; fi"
	return "rm -f " + easyMeshStartReadyPath + " " + easyMeshStartFailedPath + " " + easyMeshStartPIDPath +
		"; /sbin/start-stop-daemon -S -b -m -p " + easyMeshStartPIDPath +
		" -x /bin/sh -- -c " + shellSingleQuote(worker) + "; echo scheduled"
}

func dispatchRemoteEasyMeshStart(ip string, role string) error {
	_, err := easyMeshExecRemote(ip, easyMeshStartCommandWithReload(role, true))
	if err == nil {
		return nil
	}
	if easyMeshRemoteDispatchResultAmbiguous(err) {
		// Some rpcd/uhttpd builds close the response with an empty body (or
		// while it is being read) after file.exec has already accepted the
		// detached worker. Do not roll back a potentially healthy start based
		// on the transport response alone. The caller always follows this with
		// waitForEasyMeshRemoteStart, which verifies the ready marker, control
		// socket and mapd process and still fails closed if nothing was started.
		return nil
	}
	return err
}

func easyMeshRemoteDispatchResultAmbiguous(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.HasSuffix(message, "ubus bad result body=") ||
		strings.Contains(message, "unexpected eof") ||
		strings.Contains(message, "context deadline exceeded") ||
		strings.Contains(message, "client.timeout exceeded") ||
		strings.Contains(message, "connection reset by peer")
}

func waitForEasyMeshLocalStart(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(easyMeshStartFailedPath); err == nil {
			return fmt.Errorf("MediaTek script exited with status %s", strings.TrimSpace(string(raw)))
		}
		_, readyErr := os.Stat(easyMeshStartReadyPath)
		_, socketErr := os.Stat(easyMeshMapdSocket)
		processErr := exec.Command("pidof", "mapd").Run()
		if readyErr == nil && socketErr == nil && processErr == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("MediaTek EasyMesh engine did not become ready")
}

func waitForEasyMeshRemoteStart(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	command := "if [ -f " + easyMeshStartFailedPath + " ]; then echo script-exit-$(cat " + easyMeshStartFailedPath + "); exit 2; fi; " +
		"test -f " + easyMeshStartReadyPath + " && test -S " + easyMeshMapdSocket + " && pidof mapd >/dev/null"
	for time.Now().Before(deadline) {
		if _, err := easyMeshExecRemote(ip, command); err == nil {
			return nil
		} else {
			lastErr = err
			if strings.Contains(err.Error(), "script-exit-") {
				return err
			}
		}
		time.Sleep(time.Second)
	}
	if lastErr == nil {
		lastErr = errors.New("no response")
	}
	return fmt.Errorf("MediaTek EasyMesh engine did not become ready: %v", lastErr)
}

func resumeEasyMeshAtStartup() {
	cfg := loadEasyMeshConfig()
	// A merged Enable-Mesh flow scheduled activation across the Prepare reboot;
	// finish it now so the user reconnects only once.
	if cfg.ActivationPending && !cfg.Enabled && cfg.NetworkPrepared {
		go finishPendingActivation()
		return
	}
	if !cfg.Enabled || !cfg.NetworkPrepared {
		return
	}
	go func() {
		// Let netifd, the isolated management DHCP pool and Antennas settle first.
		time.Sleep(15 * time.Second)
		if err := ensureEasyMeshManagementBridge(); err != nil {
			fmt.Printf("EasyMesh startup: management bridge recovery failed: %v\n", err)
			return
		}
		credentials, err := easyMeshLocalFronthaulCredentials()
		if err != nil {
			fmt.Printf("EasyMesh startup: Fronthaul profile recovery failed: %v\n", err)
			return
		}

		// An Antenna orphaned by an earlier rollback cannot recover on its own,
		// so give it a way back on every start rather than only at Enable Mesh.
		adoptUntaggedAntennas()

		// The Backhaul is carried by this device's own Radio, so recovery is
		// entirely local. Antennas are plain client APs and are never reconciled
		// into Mesh state here.
		if !easyMeshLocalFronthaulInterfacesPresent() {
			fmt.Printf("EasyMesh startup: the Mesh Radio is not prepared; run Enable Mesh before activation\n")
			return
		}
		if !easyMeshLocalEngineRunning() || !easyMeshLocalRuntimeMatches(cfg.Role, cfg.BackhaulBand) {
			if err := configureLocalEasyMeshTurnkey(cfg.Role, cfg.BackhaulBand, credentials); err != nil {
				fmt.Printf("EasyMesh startup: engine reconciliation failed: %v\n", err)
				return
			}
			if _, err := easyMeshExecLocal(easyMeshStartCommandWithReload(cfg.Role, cfg.Role == "agent")); err != nil {
				fmt.Printf("EasyMesh startup: engine start failed: %v\n", err)
				return
			}
			if err := waitForEasyMeshLocalStart(60 * time.Second); err != nil {
				fmt.Printf("EasyMesh startup: engine readiness failed: %v\n", err)
				return
			}
		}

		if cfg.Role == "agent" && cfg.AgentHandoff && easyMeshLocalEngineRunning() {
			startupErr := func() error {
				// Follow the band the Backhaul actually came back on. Insisting on
				// the selected one tore down working Meshes: mapd's AutoBHSwitching
				// had moved the Backhaul to the other band, the selected station
				// never associated, and this recovery rolled the Agent all the way
				// back to standalone.
				activeBand, _, err := waitForEasyMeshBackhaulOnAnyBand(cfg.BackhaulBand, 90*time.Second)
				if err != nil {
					return fmt.Errorf("Backhaul did not return on either band: %v", err)
				}
				if err := waitForEasyMeshBackhaulStable(activeBand, easyMeshBackhaulStable, 30*time.Second); err != nil {
					return err
				}
				if !easyMeshAgentClientHandoffReady() {
					if _, err := easyMeshExecLocal(easyMeshScheduleAgentHandoffCommand(3)); err != nil {
						return fmt.Errorf("resume Agent client handoff: %v", err)
					}
				}
				if _, err := waitForEasyMeshAgentClientHandoff(easyMeshHandoffTimeout); err != nil {
					return err
				}
				return waitForEasyMeshBackhaulStable(activeBand, easyMeshBackhaulStable, 30*time.Second)
			}()
			if startupErr != nil {
				fmt.Printf("EasyMesh startup: Agent recovery failed: %v\n", startupErr)
				if rollbackErr := rollbackFailedEasyMeshOperation(cfg); rollbackErr != nil {
					fmt.Printf("EasyMesh startup: standalone rollback also failed: %v\n", rollbackErr)
				}
			}
		}
	}()
}

func easyMeshLocalEngineRunning() bool {
	if _, err := os.Stat(easyMeshMapdSocket); err != nil {
		return false
	}
	return exec.Command("pidof", "mapd").Run() == nil
}

func easyMeshRemoteEngineRunning(ip string) bool {
	_, err := easyMeshExecRemote(ip, "test -S "+easyMeshMapdSocket+" && pidof mapd >/dev/null")
	return err == nil
}

func ensureControllerBackhaulAntennaOnboarded(ip string, timeout time.Duration) error {
	if attached, err := easyMeshControllerAntennaAttached(ip); err == nil && attached {
		return nil
	}
	// Both ends must use Ethernet onboarding for the chassis-internal link.
	// Opening Wi-Fi onboarding on the Root before this finishes leaves its
	// directional Antenna outside the Root topology, so it cannot relay the
	// later wireless onboarding window to the joining chassis.
	if _, err := easyMeshExecLocal("/usr/bin/mapd_cli " + easyMeshMapdSocket + " onboarding 0"); err != nil {
		return fmt.Errorf("start local Root Ethernet onboarding: %v", err)
	}
	if _, err := easyMeshExecRemote(ip, "/usr/bin/mapd_cli "+easyMeshMapdSocket+" onboarding 0"); err != nil {
		return fmt.Errorf("start selected Antenna Ethernet onboarding: %v", err)
	}
	if err := waitForControllerAntenna(ip, timeout); err != nil {
		return fmt.Errorf("selected Antenna did not join the local Root over Ethernet: %v", err)
	}
	return nil
}

func waitForControllerAntenna(ip string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if attached, err := easyMeshControllerAntennaAttached(ip); err == nil && attached {
			return nil
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("Antenna was not registered in the Root topology within %s", timeout.Round(time.Second))
}

func triggerEasyMeshOnboarding() (retErr error) {
	cfg := loadEasyMeshConfig()
	handoffVerified := false
	rejoiningHandedOffAgent := cfg.Role == "agent" && cfg.AgentHandoff
	if !cfg.Enabled {
		return errors.New("EasyMesh is not enabled")
	}
	defer func() {
		if retErr == nil || !easyMeshOnboardingFailureNeedsRollback(cfg, handoffVerified) {
			return
		}
		original := retErr
		setEasyMeshJobStage("Pairing failed; restoring standalone operation")
		if rollbackErr := rollbackFailedEasyMeshOperation(cfg); rollbackErr != nil {
			retErr = fmt.Errorf("%v; automatic rollback also failed: %v", original, rollbackErr)
			return
		}
		retErr = fmt.Errorf("%v; all pre-Mesh configuration was restored automatically", original)
	}()
	setEasyMeshJobStage("Waiting for EasyMesh engine")
	if err := waitForEasyMeshSocket(35 * time.Second); err != nil {
		return err
	}
	if cfg.Role == "controller" {
		setEasyMeshJobStage("Opening wireless onboarding on the Root Radio")
		baseline := make(map[string]bool)
		if topology, err := easyMeshLocalTopologySnapshot(); err == nil {
			baseline = easyMeshTopologyDeviceIDs(topology)
		}
		if err := openControllerOnboardingWindow(cfg.BackhaulBand); err != nil {
			return err
		}
		cfg.OnboardingAt = time.Now().Unix()
		if err := saveEasyMeshConfig(cfg); err != nil {
			return err
		}
		return maintainControllerOnboardingWindow(cfg.BackhaulBand, baseline, easyMeshOnboardingWindow)
	}
	if cfg.Role != "agent" {
		return errors.New("save a Controller or Agent role before onboarding")
	}
	if rejoiningHandedOffAgent {
		setEasyMeshJobStage("Rejoining the Root")
	} else {
		setEasyMeshJobStage("Starting Wi-Fi onboarding on the Backhaul Radio")
	}
	// Silence the unselected band's station so only the selected band hunts for
	// the Root's registrar.
	if _, err := easyMeshExecLocal(easyMeshBackhaulSTASilenceCommand(easyMeshOtherBand(cfg.BackhaulBand))); err != nil {
		fmt.Printf("EasyMesh onboarding: silencing the unselected-band station failed (continuing): %v\n", err)
	}
	// The Backhaul station and this chassis' own AP share one DBDC Radio, so they
	// cannot sit on different channels. If the Root is on another channel the scan
	// finds it, logs "will connect <ssid> on <ch>", and then the Radio snaps back
	// to the local channel and nothing else ever happens — no association, no WPS
	// frame at the Root. Move this Radio onto the Root's channel first.
	setEasyMeshJobStage("Matching the Root's Backhaul channel")
	if err := alignLocalBackhaulChannelWithRoot(cfg.BackhaulBand); err != nil {
		// A channel that already matches, or a Root that has not been seen yet, must
		// not abort onboarding: the enrollee below still gets its full window.
		fmt.Printf("EasyMesh onboarding: Backhaul channel alignment skipped: %v\n", err)
	}
	setEasyMeshJobStage("Starting Wi-Fi onboarding on the Backhaul Radio")

	// Drive the WPS enrollee directly and let it own the Backhaul station.
	//
	// `mapd_cli onboarding 1` is deliberately NOT used. Measured on the device, it
	// makes mapd bring the station up in a tight ApCliIfUp/join loop with an EMPTY
	// SSID; the probe times out, mapd retries, and the connection state machine is
	// never idle long enough for WscScanExec to take the channel
	// ("TakeChannelOpCharge fail for SCAN"). It also resets both stations of the
	// card every few seconds, which tears down a PBC session that has already
	// picked its Root ("will connect <ssid> on <ch>" followed immediately by
	// SetApCliEnableByWdev(...)=0 on both). The enrollee sequence alone brings the
	// station up and pairs — that is the sequence that is known to work here.
	if _, err := easyMeshExecLocal(easyMeshBackhaulWPSEnrolleeCommand(cfg.BackhaulBand)); err != nil {
		return fmt.Errorf("start Backhaul WPS enrollee: %v", err)
	}
	cfg.OnboardingAt = time.Now().Unix()
	if err := saveEasyMeshConfig(cfg); err != nil {
		return err
	}

	setEasyMeshJobStage("Waiting for the wireless backhaul")
	if _, err := waitForEasyMeshBackhaulDuringOnboarding(cfg.BackhaulBand, easyMeshOnboardingWindow); err != nil {
		return err
	}
	// A freshly paired Backhaul legitimately bounces: the enrollee writes the
	// credential it just received, the station reconnects with it, and mapd
	// re-runs its own AP selection. Measured on the device, a 30s budget is not
	// enough to see 12 uninterrupted seconds through that churn and the whole
	// onboarding was rolled back moments after it had actually succeeded. Give the
	// link time to settle before requiring stability.
	setEasyMeshJobStage("Verifying the selected Backhaul band is stable")
	if err := waitForEasyMeshBackhaulStable(cfg.BackhaulBand, easyMeshBackhaulStable, easyMeshBackhaulSettle); err != nil {
		return err
	}

	// Persist the handoff intent before changing the management address so a
	// reboot can resume the same transaction. It is not considered successful
	// until the DHCP client has a real Root-issued address and the selected-band
	// Backhaul remains stable after the network transition.
	if !cfg.AgentHandoff {
		cfg.AgentHandoff = true
		if err := saveEasyMeshConfig(cfg); err != nil {
			return err
		}
	}
	if !easyMeshAgentClientHandoffReady() {
		setEasyMeshJobStage("Backhaul connected; transferring client network to the Root")
		if _, err := easyMeshExecLocal(easyMeshScheduleAgentHandoffCommand(8)); err != nil {
			if !rejoiningHandedOffAgent {
				cfg.AgentHandoff = false
				_ = saveEasyMeshConfig(cfg)
			}
			return fmt.Errorf("schedule Agent network handoff: %v", err)
		}
	}
	setEasyMeshJobStage("Waiting for a Root-issued management address")
	if _, err := waitForEasyMeshAgentClientHandoff(easyMeshHandoffTimeout); err != nil {
		return err
	}
	setEasyMeshJobStage("Verifying Backhaul after the network handoff")
	if err := waitForEasyMeshBackhaulStable(cfg.BackhaulBand, easyMeshBackhaulStable, easyMeshBackhaulSettle); err != nil {
		return err
	}
	handoffVerified = true
	return nil
}

func waitForEasyMeshBackhaulDuringOnboarding(band string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	nextRefresh := time.Now().Add(easyMeshPBCRefresh)
	lastStatus := ""
	for time.Now().Before(deadline) {
		raw := easyMeshRuntimeCommand("bh_conn_status")
		if raw != "" {
			lastStatus = raw
			associated, assocErr := easyMeshLocalWirelessBackhaulAssociated(band)
			if easyMeshBackhaulConnected(raw) && assocErr == nil && associated {
				return raw, nil
			}
		}
		if !time.Now().Before(nextRefresh) {
			setEasyMeshJobStage("Keeping Agent wireless onboarding active")
			// No mapd onboarding call here either — see triggerEasyMeshOnboarding.
			// The WPS PBC window is shorter than the product onboarding window, so
			// re-arm the enrollee each refresh to keep the STA pairing-eligible.
			if _, refreshErr := easyMeshExecLocal(easyMeshBackhaulWPSEnrolleeCommand(band)); refreshErr != nil {
				fmt.Printf("EasyMesh Agent WPS enrollee refresh failed: %v\n", refreshErr)
			}
			nextRefresh = nextRefresh.Add(easyMeshPBCRefresh)
			setEasyMeshJobStage("Waiting for the wireless backhaul")
		}
		time.Sleep(time.Second)
	}
	if lastStatus == "" {
		lastStatus = "no status returned"
	}
	return lastStatus, fmt.Errorf("wireless backhaul did not connect within %s; the Agent kept its original management address and DHCP server", timeout.Round(time.Second))
}

func easyMeshAgentClientHandoffApplied() bool {
	_, err := easyMeshExecLocal("test \"$(uci -q get dhcp.lan.ignore)\" = 1 && test \"$(uci -q get network.lan.proto)\" = dhcp")
	return err == nil
}

func easyMeshAgentClientHandoffReady() bool {
	ip := easyMeshLANIPv4()
	return easyMeshAgentClientHandoffApplied() && ip != "" && ip != ACManagementIP
}

func easyMeshOnboardingFailureNeedsRollback(cfg EasyMeshConfig, handoffVerified bool) bool {
	return cfg.Role == "agent" && !handoffVerified
}

func easyMeshMediumMatchesBand(medium string, band string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(medium), " ", ""))
	if band == "2g" {
		return strings.Contains(normalized, "2.4") || normalized == "2g" || normalized == "24g"
	}
	return strings.Contains(normalized, "5g")
}

func maintainControllerOnboardingWindow(band string, baseline map[string]bool, window time.Duration) error {
	deadline := time.Now().Add(window)
	nextRefresh := time.Now().Add(easyMeshPBCRefresh)
	candidateALID := ""
	var candidateSince time.Time
	policyApplied := false
	sawWrongBand := false
	sawManagementMissing := false
	setEasyMeshJobStage("Wireless onboarding open for up to 5 minutes")

	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			if sawManagementMissing {
				return errors.New("the selected-band Backhaul connected, but the Agent did not obtain a reachable Root management address")
			}
			if sawWrongBand {
				return fmt.Errorf("a Mesh Agent appeared on a different Backhaul band; no Agent joined the selected %s band", strings.ToUpper(band))
			}
			return fmt.Errorf("no Mesh Agent joined within the %s onboarding window", window.Round(time.Second))
		}

		delay := 5 * time.Second
		untilRefresh := time.Until(nextRefresh)
		if untilRefresh > 0 && untilRefresh < delay {
			delay = untilRefresh
		}
		if remaining < delay {
			delay = remaining
		}
		time.Sleep(delay)

		if topology, err := easyMeshLocalTopologySnapshot(); err == nil {
			management := easyMeshManagementNodesByALID()
			nodes := easyMeshRemoteNodesFromTopologyWithManagement(topology, easyMeshDHCPLeasesByMAC(), management)
			matched := false
			for _, node := range nodes {
				if !easyMeshMediumMatchesBand(node.BackhaulMedium, band) {
					sawWrongBand = true
					continue
				}
				matched = true
				if node.ALID != candidateALID {
					candidateALID = node.ALID
					candidateSince = time.Now()
					policyApplied = false
				}
				if !policyApplied {
					if baseline[node.ALID] {
						setEasyMeshJobStage("Existing selected-band Mesh Agent detected; refreshing client WiFi policy")
					} else {
						setEasyMeshJobStage("New selected-band Mesh Agent detected; applying client WiFi policy")
					}
					if _, renewErr := easyMeshExecLocal("/usr/bin/mapd_cli " + easyMeshMapdSocket + " config_renew"); renewErr != nil {
						return fmt.Errorf("Mesh Agent joined but client WiFi policy refresh failed: %v", renewErr)
					}
					policyApplied = true
				}
				if time.Since(candidateSince) < easyMeshBackhaulStable {
					setEasyMeshJobStage("Selected-band Backhaul detected; verifying stability")
					break
				}
				if !node.ManagementOnline || node.ManagementIP == "" {
					sawManagementMissing = true
					setEasyMeshJobStage("Backhaul stable; waiting for the Agent management address")
					break
				}
				setEasyMeshJobStage("Agent Backhaul and management path verified")
				return nil
			}
			if !matched {
				candidateALID = ""
				candidateSince = time.Time{}
				policyApplied = false
			}
		}

		if !time.Now().Before(nextRefresh) {
			// MediaTek's PBC window is shorter than the product-level five-minute
			// pairing window. Refresh it while enough time remains for the final
			// native window; failures here are transient and must not roll back an
			// otherwise healthy Root Controller.
			// Re-arming the registrar tears down whatever is already associated to
			// the Backhaul BSS. The 1905 topology only learns the Agent some seconds
			// AFTER it associates, so gating on candidateALID alone leaves a window in
			// which the refresh kills the pairing it is meant to protect. Check the
			// Radio itself as well: if a station is already on the Backhaul BSS, the
			// window has done its job and must be left alone.
			if candidateALID == "" && !easyMeshBackhaulBSSHasStation(band) && time.Until(deadline) >= 2*time.Minute {
				setEasyMeshJobStage("Keeping the wireless onboarding window open")
				if err := openControllerOnboardingWindow(band); err != nil {
					fmt.Printf("EasyMesh onboarding refresh failed: %v\n", err)
				}
			}
			nextRefresh = nextRefresh.Add(easyMeshPBCRefresh)
			setEasyMeshJobStage("Wireless onboarding open for up to 5 minutes")
		}
	}
}

// openControllerOnboardingWindow arms the WPS PBC registrar on the Root's own
// Fronthaul BSS, which doubles as the Backhaul AP at BssidNum=1.
//
// mapd's `trigger_map_wps <AL-MAC>` is deliberately NOT used. On this firmware
// it opens PBC on BOTH bands of the target device, and WPS PBC tolerates exactly
// one PBC-active AP in range: an enrollee that sees two PBC beacons with
// different UUID-E either declares a session overlap or latches onto the wrong
// band. On-device this made the 5GHz Backhaul STA repeatedly select the 2.4GHz
// BSS ("SSID: <name>, CH: 5") and never associate. Arming the selected band
// directly — the same iwpriv sequence wapp runs internally — is both sufficient
// and deterministic.
func openControllerOnboardingWindow(band string) error {
	// Close the unselected band first so exactly one PBC window is open.
	// Best-effort: WscStop on a band with no WPS session returns non-zero and
	// must not abort an otherwise healthy onboarding attempt.
	if _, err := easyMeshExecLocal(easyMeshBackhaulWPSStopCommand(easyMeshOtherBand(band))); err != nil {
		fmt.Printf("EasyMesh onboarding: closing the unselected-band WPS window failed (continuing): %v\n", err)
	}
	if _, err := easyMeshExecLocal(easyMeshBackhaulWPSRegistrarCommand(band)); err != nil {
		return fmt.Errorf("open Backhaul WPS registrar on the Root Radio: %v", err)
	}
	return nil
}

// easyMeshBackhaulBSSHasStation reports whether anything is associated to this
// Radio's Backhaul BSS. The MediaTek driver answers `show stainfo` through the
// kernel log, so the count is read back from there.
func easyMeshBackhaulBSSHasStation(band string) bool {
	bss := easyMeshBackhaulBSSInterface(band)
	out, err := easyMeshExecLocal("iwpriv " + bss + " show stainfo >/dev/null 2>&1; sleep 1; dmesg | grep -o 'sta_cnt=[0-9]*' | tail -n 1")
	if err != nil {
		return false
	}
	_, value, ok := strings.Cut(strings.TrimSpace(out), "=")
	if !ok {
		return false
	}
	count, convErr := strconv.Atoi(strings.TrimSpace(value))
	return convErr == nil && count > 0
}

// easyMeshOtherBand returns the band that is NOT the selected Backhaul band.
func easyMeshOtherBand(band string) string {
	if band == "2g" {
		return "5g"
	}
	return "2g"
}

// easyMeshBackhaulSTASilenceCommand closes the WPS session on one band's
// Backhaul station so only the selected band hunts for the Root's registrar.
//
// It deliberately does NOT touch ApCliEnable. Measured on the device: an
// ApCliEnable write on either station is applied to BOTH stations of the card
// (the driver logs SetApCliEnableByWdev for apcli0 and apcli1 together), so
// disabling the unselected band also took down the selected one — and with the
// station down the enrollee never scans at all. That is strictly worse than the
// channel contention this was meant to avoid.
func easyMeshBackhaulSTASilenceCommand(band string) string {
	sta := easyMeshBackhaulSTAInterface(band)
	return fmt.Sprintf("iwpriv %s set WscStop=1; true", sta)
}

// easyMeshBackhaulWPSStopCommand closes an open WPS window on one band's BSS so
// it stops advertising PBC in its beacon.
func easyMeshBackhaulWPSStopCommand(band string) string {
	bss := easyMeshBackhaulBSSInterface(band)
	return fmt.Sprintf("iwpriv %s set WscStop=1", bss)
}

func easyMeshBackhaulBSSInterface(band string) string {
	// At BssidNum=1 each Radio exposes exactly one BSS, and that BSS serves both
	// duties: it is the client-facing Fronthaul AND the Multi-AP Backhaul AP a
	// joining chassis associates to (the BSS policy marks it "1 1", which mapd
	// applies as DevOwnRole=0x60). This matches the MediaTek factory layout —
	// there is no ra1/rax1 to open a registrar on.
	if band == "2g" {
		return "ra0"
	}
	return "rax0"
}

func easyMeshBackhaulSTAInterface(band string) string {
	// The directional Antenna's Backhaul station interface that associates to the
	// Root Antenna's Backhaul BSS.
	if band == "2g" {
		return "apcli0"
	}
	return "apclix0"
}

// easyMeshBackhaulWPSRegistrarCommand opens a WPS PBC registrar window on the
// directional Antenna's dedicated Multi-AP Backhaul BSS. This is the iwpriv
// sequence wapp runs internally for `map trigger_wps` (WscConfMode=4 registrar,
// WscMode=2 PBC); driving it directly is required because the mapd path does not
// raise the WPS IE on this firmware.
func easyMeshBackhaulWPSRegistrarCommand(band string) string {
	bss := easyMeshBackhaulBSSInterface(band)
	return fmt.Sprintf("iwpriv %s set WscConfMode=4; iwpriv %s set WscMode=2; iwpriv %s set WscConfStatus=2; iwpriv %s set WscGetConf=1", bss, bss, bss, bss)
}

// easyMeshBackhaulWPSEnrolleeCommand puts the directional Antenna's Backhaul STA
// into WPS PBC enrollee mode (WscConfMode=1 enrollee, WscMode=2 PBC) so it pairs
// with the Root Antenna's registrar opened by easyMeshBackhaulWPSRegistrarCommand.
// ApCliEnable=1 is deliberately omitted: the vendor's own enrollee helper
// (mtkwifi.lua apcli_do_enr_pbc_wps) has it commented out, and the sequence below
// is the one verified to complete onboarding end to end on this firmware.
func easyMeshBackhaulWPSEnrolleeCommand(band string) string {
	sta := easyMeshBackhaulSTAInterface(band)
	return fmt.Sprintf("iwpriv %s set WscConfMode=1; iwpriv %s set WscMode=2; iwpriv %s set WscGetConf=1", sta, sta, sta)
}

// waitForEasyMeshBackhaul waits for the Backhaul to come up on ANY band, not
// only the selected one, and reports which band carried it. See
// easyMeshActiveBackhaulBand: insisting on the selection tore down Meshes that
// the vendor had legitimately moved to the other band.
func waitForEasyMeshBackhaul(band string, timeout time.Duration) (string, error) {
	_, status, err := waitForEasyMeshBackhaulOnAnyBand(band, timeout)
	return status, err
}

func waitForEasyMeshBackhaulOnAnyBand(preferred string, timeout time.Duration) (string, string, error) {
	deadline := time.Now().Add(timeout)
	lastStatus := ""
	for time.Now().Before(deadline) {
		raw := easyMeshRuntimeCommand("bh_conn_status")
		if raw != "" {
			lastStatus = raw
			if active, ok := easyMeshActiveBackhaulBand(preferred); ok && easyMeshBackhaulConnected(raw) {
				return active, raw, nil
			}
		}
		time.Sleep(time.Second)
	}
	if lastStatus == "" {
		lastStatus = "no status returned"
	}
	return "", lastStatus, fmt.Errorf("wireless backhaul did not connect within %s; the Agent kept its original management address and DHCP server", timeout.Round(time.Second))
}

func waitForEasyMeshBackhaulStable(band string, stableFor time.Duration, timeout time.Duration) error {
	if stableFor <= 0 {
		stableFor = time.Second
	}
	deadline := time.Now().Add(timeout)
	var stableSince time.Time
	for time.Now().Before(deadline) {
		raw := easyMeshRuntimeCommand("bh_conn_status")
		associated, assocErr := easyMeshLocalWirelessBackhaulAssociated(band)
		if assocErr == nil && easyMeshBackhaulConnected(raw) && associated {
			if stableSince.IsZero() {
				stableSince = time.Now()
			}
			if time.Since(stableSince) >= stableFor {
				return nil
			}
		} else {
			stableSince = time.Time{}
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("the selected %s Backhaul did not remain stable for %s", strings.ToUpper(band), stableFor.Round(time.Second))
}

// easyMeshRadioReadyInterfaces returns the interfaces whose presence confirms a
// Radio finished coming up after the Prepare reboot. Every role now runs the
// single-BSS (BssidNum=1) layout, so the base Fronthaul BSS is the one to check.
func easyMeshRadioReadyInterfaces() []string {
	return []string{"ra0", "rax0"}
}

func easyMeshLocalFronthaulInterfacesPresent() bool {
	for _, ifname := range easyMeshRadioReadyInterfaces() {
		if _, err := os.Stat("/sys/class/net/" + ifname); err != nil {
			return false
		}
	}
	return true
}

func easyMeshRemoteFronthaulInterfacesPresent(ip string) (bool, error) {
	ifaces := easyMeshRadioReadyInterfaces()
	_, err := easyMeshExecRemote(ip, "test -d /sys/class/net/"+ifaces[0]+" && test -d /sys/class/net/"+ifaces[1])
	return err == nil, err
}

func easyMeshDetachedRebootCommand(delaySeconds int) string {
	if delaySeconds < 2 {
		delaySeconds = 2
	}
	// Changing BssidNum requires the proprietary driver to be initialized at
	// boot. Never substitute `wifi restart`: every later SSID/policy update uses
	// `wifi reload`, which cannot change the BSS count.
	//
	// -m -p <pidfile> is REQUIRED, not cosmetic. BusyBox start-stop-daemon
	// selects candidate processes by matching the -x executable path, unless a
	// pidfile is given — in which case it checks only that pidfile. Antennas are
	// driven through the anonymous ubus file.exec channel, which runs every
	// command as literal `/bin/sh -c ...`, so a bare `-x /bin/sh` matches the very
	// shell issuing the command: start-stop-daemon then refuses with "/bin/sh is
	// already running" and exits 1, failing the Prepare step before either unit
	// could reboot. (Over SSH the login shell is ash, so the bug is invisible
	// there and only ever appears on the remote path.) The same pattern is used by
	// easyMeshScheduleAgentHandoffCommand.
	worker := fmt.Sprintf("sleep %d; /sbin/reboot", delaySeconds)
	return "rm -f " + easyMeshRebootPID + "; /sbin/start-stop-daemon -S -b -m -p " + easyMeshRebootPID +
		" -x /bin/sh -- -c " + shellSingleQuote(worker)
}

// easyMesh5GDFSChannels are the 5GHz channels that require DFS radar detection
// (CAC + passive scanning). MediaTek's wireless Backhaul is onboarded with WPS
// PBC, whose active probe/scan is unreliable on DFS channels: the enrollee STA
// frequently never associates, producing a silent five-minute pairing timeout.
// The directional Backhaul must therefore use a non-DFS channel (36-48 UNII-1 or
// 149-165 UNII-3), and both chassis must share it.
var easyMesh5GDFSChannels = map[string]bool{
	"52": true, "56": true, "60": true, "64": true,
	"100": true, "104": true, "108": true, "112": true, "116": true, "120": true,
	"124": true, "128": true, "132": true, "136": true, "140": true, "144": true,
}

// easyMeshRadioProfilePath returns the driver profile that owns one band's Radio.
func easyMeshRadioProfilePath(band string) string {
	if band == "2g" {
		return Mtk24GPath
	}
	return Mtk5GPath
}

// easyMeshBackhaulChannelFromScan reads the channel the driver's own WPS scan
// selected out of its "will connect <ssid> (<bssid>) on <channel>" line.
//
// This reuses the driver's selection instead of re-parsing the site-survey
// table: the scan already applies the Multi-AP/PBC rules that identify the Root,
// and its log line is a stable, single-purpose format.
func easyMeshBackhaulChannelFromScan(log string) (int, bool) {
	best := 0
	found := false
	for _, line := range strings.Split(strings.ReplaceAll(log, "\r\n", "\n"), "\n") {
		marker := strings.Index(line, "will connect ")
		if marker < 0 {
			continue
		}
		tail := line[marker:]
		onIndex := strings.LastIndex(tail, " on ")
		if onIndex < 0 {
			continue
		}
		channel, err := strconv.Atoi(strings.TrimSpace(tail[onIndex+len(" on "):]))
		if err != nil || channel <= 0 {
			continue
		}
		// Later lines win: the most recent scan reflects the Root's current channel.
		best, found = channel, true
	}
	return best, found
}

// easyMeshProbeRootChannelCommand arms one short WPS scan and returns the
// driver's own selection line. Nothing is paired here — the scan is only used to
// learn which channel the Root's Backhaul BSS is on.
func easyMeshProbeRootChannelCommand(band string) string {
	sta := easyMeshBackhaulSTAInterface(band)
	return "dmesg -c >/dev/null 2>&1; iwpriv " + sta + " set WscConfMode=1; iwpriv " + sta +
		" set WscMode=2; iwpriv " + sta + " set WscGetConf=1; sleep " +
		strconv.Itoa(int(easyMeshChannelProbeWait/time.Second)) +
		"; iwpriv " + sta + " set WscStop=1; dmesg | grep 'will connect' | tail -n 5; true"
}

// easyMeshSetRadioChannelCommand pins one band's Radio to a channel.
//
// The channel is written to the driver profile and applied with `wifi reload`:
// `iwpriv <ap> set Channel=N` alone is not honoured on this firmware (measured —
// the Radio snaps straight back to the profile's channel).
func easyMeshSetRadioChannelCommand(band string, channel int) string {
	path := easyMeshRadioProfilePath(band)
	quoted := shellSingleQuote(path)
	return "sed -i 's/^Channel=.*/Channel=" + strconv.Itoa(channel) + "/' " + quoted +
		"; sed -i 's/^AutoChannelSelect=.*/AutoChannelSelect=0/' " + quoted +
		"; /sbin/wifi reload; true"
}

// alignLocalBackhaulChannelWithRoot moves this chassis' Backhaul Radio onto the
// channel the Root is using.
//
// The Backhaul station and the local AP share one DBDC Radio and therefore one
// channel. When the two chassis are configured on different channels the WPS
// scan still finds the Root and logs "will connect <ssid> on <ch>", but the
// Radio immediately restores its own channel and no association is ever
// attempted — the Root sees no WSC frame at all and the window times out with a
// completely silent failure.
func alignLocalBackhaulChannelWithRoot(band string) error {
	local, err := easyMeshLocalBackhaulChannel(band)
	if err != nil {
		return fmt.Errorf("read local Backhaul channel: %v", err)
	}
	// The first sweep after arming can miss the Root, so probe a few times before
	// giving up rather than silently leaving the Radio on the wrong channel.
	rootChannel, ok := 0, false
	for attempt := 0; attempt < easyMeshChannelProbeAttempts && !ok; attempt++ {
		probe, probeErr := easyMeshExecLocal(easyMeshProbeRootChannelCommand(band))
		if probeErr != nil {
			return fmt.Errorf("probe the Root's channel: %v", probeErr)
		}
		rootChannel, ok = easyMeshBackhaulChannelFromScan(probe)
	}
	if !ok {
		return errors.New("the Root's Backhaul BSS was not seen in a scan")
	}
	if strings.TrimSpace(local) == strconv.Itoa(rootChannel) {
		return nil
	}
	setEasyMeshJobStage(fmt.Sprintf("Moving the Backhaul Radio to the Root's channel %d", rootChannel))
	if _, err := easyMeshExecLocal(easyMeshSetRadioChannelCommand(band, rootChannel)); err != nil {
		return fmt.Errorf("set Backhaul channel %d: %v", rootChannel, err)
	}
	// `wifi reload` rebuilds the Radio; give it time to beacon again before the
	// enrollee starts scanning.
	time.Sleep(easyMeshChannelSettle)
	return nil
}

func easyMeshLocalBackhaulChannel(band string) (string, error) {
	path := easyMeshRadioProfilePath(band)
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return easyMeshFirstListValue(easyMeshDatValues(string(raw))["Channel"]), nil
}

// easyMeshCheckBackhaulChannelNonDFS fails fast when the selected 5GHz Backhaul
// Antenna is on a DFS (or Auto) channel, turning the otherwise silent five-minute
// WPS pairing timeout into an actionable error. 2.4GHz has no DFS and is skipped.
func easyMeshCheckBackhaulChannelNonDFS(band string) error {
	if band != "5g" {
		return nil
	}
	channel, err := easyMeshLocalBackhaulChannel(band)
	if err != nil {
		return fmt.Errorf("read 5GHz Backhaul channel: %v", err)
	}
	if channel == "" || channel == "0" {
		return errors.New("the 5GHz Backhaul channel is set to Auto; wireless Backhaul WPS pairing is unreliable unless an explicit non-DFS channel (36-48 or 149-165) is set on both units")
	}
	if easyMesh5GDFSChannels[channel] {
		return fmt.Errorf("the 5GHz Backhaul channel %s is a DFS channel; MediaTek wireless Backhaul WPS pairing is unreliable on DFS. Set the same non-DFS channel (36-48 or 149-165) on BOTH units in WiFi configuration, then retry", channel)
	}
	return nil
}

// runEnableMesh is the merged one-click activation. It performs the VLAN
// separation (if not done) and the shared Radio preparation in a single job.
// The mandatory Radio reboot at the end of preparation does double duty: it
// recovers the client SSID that the VLAN migration drops (the netifd bridge
// rebuild detaches the MediaTek radios) AND carries the transaction across to
// activation via resumeEasyMeshAtStartup. The user reconnects only once.
func runEnableMesh() error {
	cfg := loadEasyMeshConfig()
	if cfg.Role != "controller" && cfg.Role != "agent" {
		return errors.New("save a Controller or Agent role first")
	}
	if cfg.Enabled {
		return errors.New("EasyMesh is already enabled")
	}
	if !cfg.NetworkPrepared {
		if err := prepareEasyMeshNetwork(); err != nil {
			return err
		}
	}
	// Persist the intent before the reboot so a boot that races job completion
	// still finishes activation.
	cfg = loadEasyMeshConfig()
	cfg.ActivationPending = true
	if err := saveEasyMeshConfig(cfg); err != nil {
		return err
	}
	if err := prepareEasyMeshRadios(); err != nil {
		// Preparation failed before the reboot; drop the intent so the device does
		// not try to auto-activate an unprepared Radio on the next boot.
		cfg = loadEasyMeshConfig()
		cfg.ActivationPending = false
		_ = saveEasyMeshConfig(cfg)
		return err
	}
	setEasyMeshJobStage("Rebooting to apply Radio and recover client WiFi; activation resumes automatically after reboot")
	return nil
}

// finishPendingActivation runs on the first boot after a merged Enable-Mesh
// Prepare rebooted the hardware. It waits for the shared Radio to come back,
// clears the intent, then activates through the normal job machinery so the UI
// shows progress. A failed activation rolls itself back to standalone.
func finishPendingActivation() {
	time.Sleep(20 * time.Second) // let netifd, the isolated mgmt DHCP pool and Antennas settle
	cfg := loadEasyMeshConfig()
	// Clear the intent up front so an unexpected second reboot cannot loop.
	cfg.ActivationPending = false
	if err := saveEasyMeshConfig(cfg); err != nil {
		fmt.Printf("EasyMesh startup: clear pending activation flag failed: %v\n", err)
	}
	// MediaTek DBDC can need well over a minute to re-expose the 5GHz BSS after
	// the Prepare reboot; activation would fail if it ran before that.
	deadline := time.Now().Add(150 * time.Second)
	for time.Now().Before(deadline) {
		if easyMeshLocalFronthaulInterfacesPresent() {
			break
		}
		time.Sleep(3 * time.Second)
	}
	if _, err := startEasyMeshJob("activate"); err != nil {
		fmt.Printf("EasyMesh startup: automatic activation after reboot failed to start: %v\n", err)
	}
}

func prepareEasyMeshRadios() error {
	cfg := loadEasyMeshConfig()
	if cfg.Role != "controller" && cfg.Role != "agent" {
		return errors.New("save a Controller or Agent role first")
	}
	if !cfg.NetworkPrepared {
		return errors.New("prepare VLAN separation before preparing the shared Radio")
	}
	credentials, err := easyMeshLocalFronthaulCredentials()
	if err != nil {
		return err
	}
	if err := easyMeshCheckBackhaulChannelNonDFS(cfg.BackhaulBand); err != nil {
		return err
	}
	if err := ensureLocalMediaTekWiFiServicesCompatibility(); err != nil {
		return fmt.Errorf("prepare Router wifi reload compatibility: %v", err)
	}

	// The Backhaul runs on this main module's own Radio, so only the local
	// profile is prepared. Antenna submodules stay plain client APs: they are
	// never put into MediaTek turnkey mode and are never rebooted here.
	// Only the Controller writes the client credential into its driver profiles.
	// The Agent's SSID/passphrase are pushed down by the Controller once the
	// Backhaul is up, exactly as the known-good vendor recipe does; writing them
	// locally on the Agent gives its Backhaul station a profile the Controller
	// never issued.
	setEasyMeshJobStage("Applying the single-BSS Mesh Radio layout")
	isController := cfg.Role == "controller"
	if err := updateLocalEasyMeshFronthaulProfile(Mtk24GPath, credentials.TwoG, isController); err != nil {
		return fmt.Errorf("prepare 2.4GHz Radio: %v", err)
	}
	if err := updateLocalEasyMeshFronthaulProfile(Mtk5GPath, credentials.FiveG, isController); err != nil {
		return fmt.Errorf("prepare 5GHz Radio: %v", err)
	}
	if isController {
		if err := updateLocalEasyMeshCardProfile(credentials); err != nil {
			return fmt.Errorf("prepare card-level Radio profile: %v", err)
		}
	}

	setEasyMeshJobStage("Rebooting prepared Radio hardware")
	// BssidNum, WscConfMode and WscConfStatus only take effect when the
	// proprietary driver initialises at boot, so this reboot is mandatory — a
	// `wifi reload` silently leaves the old values live. It also recovers the
	// client SSID that the VLAN migration drops and lets resumeEasyMeshAtStartup
	// finish a merged Enable-Mesh flow. The delay lets the asynchronous Prepare
	// HTTP response return first.
	if _, err := easyMeshExecLocal(easyMeshDetachedRebootCommand(8)); err != nil {
		return fmt.Errorf("schedule main-module reboot: %v", err)
	}
	return nil
}

// updateLocalEasyMeshCardProfile keeps DBDC_card0.dat's credentials in step with
// the per-band profiles.
//
// This is NOT redundant: when the card-level profile and the per-band profiles
// disagree, the driver broadcasts the card-level SSID. On-device, setting
// SSID1 in mt7981.dbdc.b0/b1.dat while DBDC_card0.dat still held the previous
// name left the radios advertising the stale card-level SSID, and the resulting
// three-way mismatch between card0, the per-band profiles and
// wts_bss_info_config is what corrupted the WPS M8 credential during onboarding.
func updateLocalEasyMeshCardProfile(credentials easyMeshFronthaulSet) error {
	if _, err := os.Stat(easyMeshCardProfilePath); err != nil {
		return nil
	}
	return updateLocalMtkDatFile(easyMeshCardProfilePath, map[string]string{
		"SSID1":   credentials.TwoG.SSID,
		"WPAPSK1": credentials.TwoG.Passphrase,
		"SSID2":   credentials.FiveG.SSID,
		"WPAPSK2": credentials.FiveG.Passphrase,
	})
}

func syncEasyMeshFronthaul() error {
	cfg := loadEasyMeshConfig()
	if !cfg.Enabled {
		return errors.New("enable EasyMesh before synchronizing client WiFi")
	}
	credentials, err := easyMeshLocalFronthaulCredentials()
	if err != nil {
		return err
	}

	setEasyMeshJobStage("Configuring shared Backhaul and client WiFi")
	if cfg.Role == "controller" {
		if err := configureLocalEasyMeshBSSPolicy(credentials); err != nil {
			return err
		}
	} else {
		if err := configureLocalEasyMeshTurnkey("agent", cfg.BackhaulBand, credentials); err != nil {
			return err
		}
	}

	setEasyMeshJobStage("Applying client WiFi without restarting the Radio")
	if _, err := easyMeshExecLocal(easyMeshStartCommandWithReload(cfg.Role, cfg.Role == "agent")); err != nil {
		return err
	}
	if err := waitForEasyMeshLocalStart(90 * time.Second); err != nil {
		return err
	}
	if !easyMeshLocalFronthaulInterfacesPresent() {
		return errors.New("the profile was saved, but the Fronthaul BSS is absent; run Enable Mesh first")
	}
	if cfg.Role == "controller" {
		setEasyMeshJobStage("Refreshing client WiFi policy on connected Mesh devices")
		if _, err := easyMeshExecLocal("/usr/bin/mapd_cli " + easyMeshMapdSocket + " config_renew"); err != nil {
			return fmt.Errorf("refresh Controller BSS policy: %v", err)
		}
	} else {
		setEasyMeshJobStage("Waiting for the wireless backhaul to return")
		if _, err := waitForEasyMeshBackhaul(cfg.BackhaulBand, 90*time.Second); err != nil {
			return err
		}
	}
	// The Antenna submodules keep serving clients on the same SSID, but they are
	// configured through the ordinary WiFi path — never as Mesh members.
	return nil
}

func easyMeshScheduleAgentHandoffCommand(delaySeconds int) string {
	if delaySeconds < 1 {
		delaySeconds = 1
	}
	// Never use wifi restart here. The wireless backhaul must stay up while the
	// client bridge and DHCP ownership move to the Root. start-stop-daemon is
	// required here: a plain shell background job can keep the API response pipe
	// open and make the browser report a false fetch failure.
	// The client bridge must survive this. `ubus call network reload` rebuilds
	// br-lan, which detaches the radios and the Backhaul station — the very path
	// the DHCP exchange has to travel — so the Root's OFFER never came back and
	// the handoff timed out with "no-root-dhcp-lease" even though the Mesh was
	// healthy. Measured on the device: with the bridge left alone, the same
	// exchange completes on the first try and the Agent gets a Root-issued
	// address and default route.
	//
	// So the uci change is committed for future boots, but the address is
	// acquired here with udhcpc against the live bridge instead of being applied
	// by a netifd reload.
	worker := fmt.Sprintf(`set -e
sleep %d
rm -f %s %s
uci set dhcp.lan.ignore=1
uci set dhcp.lan.lrap_client_enabled=0
uci commit dhcp
/etc/init.d/dnsmasq restart
uci set network.lan.proto=dhcp
uci set network.lan.hostname=RoobuckAC
uci set network.lan.vendorid=RoobuckAC
uci -q delete network.lan.ipaddr
uci -q delete network.lan.netmask
uci commit network
i=0
while [ "$i" -lt 12 ]; do
	if ip -4 addr show dev br-lan scope global 2>/dev/null | grep -q "inet " && ! ip -4 addr show dev br-lan scope global 2>/dev/null | grep -q "inet %s/"; then
		touch %s
		exit 0
	fi
	udhcpc -i br-lan -q -t 4 -T 3 -H RoobuckAC -V RoobuckAC >/dev/null 2>&1 || true
	i=$((i + 1))
	sleep 2
done
echo no-root-dhcp-lease >%s
exit 1`, delaySeconds, easyMeshHandoffReady, easyMeshHandoffFailed, ACManagementIP, easyMeshHandoffReady, easyMeshHandoffFailed)
	return "rm -f " + easyMeshHandoffReady + " " + easyMeshHandoffFailed + " " + easyMeshHandoffPID +
		"; /sbin/start-stop-daemon -S -b -m -p " + easyMeshHandoffPID + " -x /bin/sh -- -c " +
		shellSingleQuote(worker+" >/tmp/lrap-mesh-agent-handoff.log 2>&1") + "; echo scheduled"
}

func waitForEasyMeshAgentClientHandoff(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(easyMeshHandoffFailed); err == nil {
			message := strings.TrimSpace(string(raw))
			if message == "" {
				message = "unknown handoff error"
			}
			return "", fmt.Errorf("Agent network handoff failed: %s", message)
		}
		if easyMeshAgentClientHandoffReady() {
			return easyMeshLANIPv4(), nil
		}
		time.Sleep(time.Second)
	}
	return "", fmt.Errorf("Agent did not obtain a Root-issued management address within %s", timeout.Round(time.Second))
}

func waitForEasyMeshSocket(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(easyMeshMapdSocket); err == nil {
			return nil
		}
		time.Sleep(time.Second)
	}
	return errors.New("MediaTek EasyMesh engine did not become ready")
}

func leaveEasyMesh() error {
	cfg := loadEasyMeshConfig()
	if !cfg.Enabled {
		if cfg.NetworkPrepared {
			return restorePreEasyMeshNetwork(cfg, false)
		}
		return errors.New("EasyMesh is not enabled")
	}
	if _, err := validatePreEasyMeshBackups(); err != nil {
		return err
	}
	// Tell the Agents first, while the Backhaul that carries the message is still
	// up. Our own teardown takes that path away within seconds.
	if cfg.Role == "controller" {
		setEasyMeshJobStage("Asking Mesh Agents to return to standalone")
		notified, unreachable := notifyPeersToLeave()
		if unreachable > 0 {
			fmt.Printf("EasyMesh: %d Agent(s) could not be told to leave and must be "+
				"returned to standalone from their own portal\n", unreachable)
		}
		if notified > 0 {
			// Give the reply a moment to be acted on before the Radio is reset.
			time.Sleep(2 * time.Second)
		}
	}
	// Antennas are never part of the Mesh, so a Leave has nothing to tear down on
	// them. Clear MapMode anyway as a best effort: a unit upgraded from the old
	// Antenna-backhaul build can still be carrying it, and an unreachable Antenna
	// must never block the AC's own restore below.
	ensureAntennasOutOfMeshMode()
	for _, path := range []string{Mtk24GPath, Mtk5GPath} {
		if err := updateLocalMtkDatFile(path, map[string]string{"MapMode": "0"}); err != nil {
			return err
		}
	}
	setEasyMeshJobStage("Leaving EasyMesh and restoring standalone networking")
	restoreErr := restorePreEasyMeshNetwork(cfg, true)
	if restoreErr != nil && antennaManagementIsIsolated() {
		return restoreErr
	}
	cfg.Enabled = false
	cfg.Role = "standalone"
	cfg.AgentHandoff = false
	cfg.OnboardingAt = 0
	cfg.NetworkPrepared = false
	if saveErr := saveEasyMeshConfig(cfg); saveErr != nil {
		if restoreErr != nil {
			return fmt.Errorf("%v; persist standalone state: %v", restoreErr, saveErr)
		}
		return saveErr
	}
	return restoreErr
}

func easyMeshSortedModuleIDs(modules []ManagedModule) []string {
	ids := make([]string, 0, len(modules))
	for _, module := range modules {
		ids = append(ids, module.ModuleID)
	}
	sort.Strings(ids)
	return ids
}
