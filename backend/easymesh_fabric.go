package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// EasyMeshTopologyAntenna is one directional Antenna module hanging off a
// chassis. Antennas are plain client-serving APs: they take no part in the Mesh
// and never appear in the vendor topology dump, so they are collected from this
// device's own module registry instead.
type EasyMeshTopologyAntenna struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Port        string `json:"port,omitempty"`
	IP          string `json:"ip,omitempty"`
	MAC         string `json:"mac,omitempty"`
	SSID24      string `json:"ssid_24,omitempty"`
	SSID5       string `json:"ssid_5,omitempty"`
	Online      bool   `json:"online"`
	ChassisALID string `json:"chassis_al_id"`
}

// EasyMeshTopologyClient is an end-user device. ParentID names what it is
// actually associated to -- an Antenna's module ID, or a chassis AL-MAC when it
// sits on the chassis radio itself.
type EasyMeshTopologyClient struct {
	MAC           string `json:"mac"`
	IP            string `json:"ip,omitempty"`
	Hostname      string `json:"hostname,omitempty"`
	SSID          string `json:"ssid,omitempty"`
	Band          string `json:"band,omitempty"`
	Interface     string `json:"interface,omitempty"`
	RSSI          int    `json:"rssi,omitempty"`
	Signal        string `json:"signal,omitempty"`
	ConnectedTime string `json:"connected_time,omitempty"`
	ParentID      string `json:"parent_id"`
}

// Clients on the chassis's own radios are collected before the chassis AL-MAC is
// known, so they are parked under this sentinel and re-parented when the graph
// is assembled.
const easyMeshFabricChassisParent = ""

type easyMeshFabric struct {
	// The chassis's own hostname, which is what an operator named the unit and a
	// more useful label than a generic role.
	Hostname string
	Antennas []EasyMeshTopologyAntenna
	Clients  []EasyMeshTopologyClient
}

// Collecting the fabric queries every Antenna over the network. The Mesh page
// polls status every 3 seconds, so the result is cached and refreshed in the
// background: a poll never waits on an Antenna round trip, and a stale-but-real
// picture is preferred over a blank one.
const easyMeshFabricTTL = 20 * time.Second

var (
	easyMeshFabricMu       sync.Mutex
	easyMeshFabricCache    easyMeshFabric
	easyMeshFabricStamp    time.Time
	easyMeshFabricInFlight bool
)

// easyMeshCollectFabric reuses the Connected Clients collector rather than
// re-deriving attribution. That collector already decides which module a client
// belongs to from each module's own bridge FDB and station table, and already
// resolves the same MAC appearing on two modules while roaming.
var easyMeshCollectFabric = func() easyMeshFabric {
	leases := ccParseLocalDHCPLeases()
	arpByMAC := ccParseLocalARPByMAC()

	modules := make([]ConnectedClientModule, 0, 4)
	modules = append(modules, ccBuildMainModule(leases, arpByMAC))
	modules = append(modules, ccBuildAPModules(leases, arpByMAC)...)
	ccDedupeClientsAcrossModules(modules)

	fabric := easyMeshFabric{
		Antennas: make([]EasyMeshTopologyAntenna, 0, len(modules)),
		Clients:  make([]EasyMeshTopologyClient, 0),
	}
	for _, module := range modules {
		parent := easyMeshFabricChassisParent
		if module.Type == "main" {
			fabric.Hostname = module.Name
		} else {
			parent = module.ModuleID
			fabric.Antennas = append(fabric.Antennas, EasyMeshTopologyAntenna{
				ID:     module.ModuleID,
				Name:   module.Name,
				Port:   module.Port,
				IP:     module.IP,
				MAC:    strings.ToLower(module.BrLanMAC),
				SSID24: module.SSID24,
				SSID5:  module.SSID5,
				Online: module.Online,
			})
		}
		for _, client := range module.Clients {
			fabric.Clients = append(fabric.Clients, EasyMeshTopologyClient{
				MAC:           strings.ToLower(strings.TrimSpace(client.MAC)),
				IP:            client.IP,
				Hostname:      client.Hostname,
				SSID:          client.SSID,
				Band:          easyMeshTopologyBandLabel(client.Band),
				Interface:     client.Interface,
				RSSI:          client.RSSI,
				Signal:        client.Signal,
				ConnectedTime: client.ConnectedTime,
				ParentID:      parent,
			})
		}
	}
	return fabric
}

// easyMeshFabricSnapshot returns the most recent fabric, refreshing it in the
// background when it has aged out. The first call after boot returns nothing and
// the next poll a few seconds later has the real picture.
func easyMeshFabricSnapshot() easyMeshFabric {
	easyMeshFabricMu.Lock()
	cached := easyMeshFabricCache
	stale := time.Since(easyMeshFabricStamp) > easyMeshFabricTTL
	if stale && !easyMeshFabricInFlight {
		easyMeshFabricInFlight = true
		go refreshEasyMeshFabric()
	}
	easyMeshFabricMu.Unlock()
	return cached
}

func refreshEasyMeshFabric() {
	fabric := easyMeshCollectFabric()
	easyMeshFabricMu.Lock()
	easyMeshFabricCache = fabric
	easyMeshFabricStamp = time.Now()
	easyMeshFabricInFlight = false
	easyMeshFabricMu.Unlock()
}

// ---------- Peer fabric exchange ----------

// A chassis can only enumerate its own Antennas: Antenna management lives on a
// chassis-local VLAN and every chassis uses the same 172.31.255.0/29, so a Root
// cannot reach an Agent's Antennas directly. It can reach the Agent itself
// though, and the Agent has already done the work -- so the Root asks for the
// result instead of trying to repeat the measurement.
type easyMeshFabricResponse struct {
	Hostname string                    `json:"hostname,omitempty"`
	Antennas []EasyMeshTopologyAntenna `json:"antennas"`
	Clients  []EasyMeshTopologyClient  `json:"clients"`
}

// requestSourceIP returns the peer address of the connection itself. Forwarding
// headers are deliberately ignored: they are set by whoever sends the request,
// so trusting them would hand the guard below to any caller.
func requestSourceIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(strings.TrimSpace(host)); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4.String()
		}
		return ip.String()
	}
	return ""
}

// defaultGatewayIPv4 reads the router this device sends its traffic to. On an
// Agent that is its Mesh upstream -- the Controller -- which is the only peer
// allowed to read the fabric.
var defaultGatewayIPv4 = func() string {
	raw, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(raw), "\n")[1:] {
		fields := strings.Fields(line)
		if len(fields) < 4 || fields[1] != "00000000" {
			continue
		}
		// The gateway is little-endian hex in /proc.
		value, err := strconv.ParseUint(fields[2], 16, 32)
		if err != nil {
			continue
		}
		gateway := net.IPv4(byte(value), byte(value>>8), byte(value>>16), byte(value>>24))
		if !gateway.IsUnspecified() {
			return gateway.String()
		}
	}
	return ""
}

// easyMeshUpstreamRequestAllowed decides whether a caller may act as this
// device's Mesh upstream. It is deliberately narrow: only an onboarded Agent
// answers, and only to the router it is already trusting with all of its
// traffic. Both the fabric read and the leave instruction are gated on it.
func easyMeshUpstreamRequestAllowed(cfg EasyMeshConfig, sourceIP string) bool {
	if !cfg.Enabled || cfg.Role != "agent" || sourceIP == "" {
		return false
	}
	gateway := defaultGatewayIPv4()
	return gateway != "" && gateway == sourceIP
}

// easyMeshFabricHandler serves this device's own Antennas and clients to its
// Mesh upstream. It carries no session because the Controller has no account on
// the Agent, so the guard is the caller's address rather than a token; a request
// from anywhere else is refused without disclosing whether a fabric exists.
func easyMeshFabricHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !easyMeshUpstreamRequestAllowed(loadEasyMeshConfig(), requestSourceIP(r)) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}
	fabric := easyMeshFabricSnapshot()
	// The cache is empty until the first collection completes. Declared arrays
	// are always sent as arrays so a caller never has to tell null from empty.
	response := easyMeshFabricResponse{
		Hostname: fabric.Hostname,
		Antennas: fabric.Antennas,
		Clients:  fabric.Clients,
	}
	if response.Antennas == nil {
		response.Antennas = []EasyMeshTopologyAntenna{}
	}
	if response.Clients == nil {
		response.Clients = []EasyMeshTopologyClient{}
	}
	_ = json.NewEncoder(w).Encode(response)
}

// ---------- Fetching a peer's fabric ----------

var (
	easyMeshPeerFabricMu       sync.Mutex
	easyMeshPeerFabricCache    map[string]easyMeshFabric
	easyMeshPeerFabricStamp    time.Time
	easyMeshPeerFabricInFlight bool
)

var easyMeshFetchPeerFabric = func(ip string) (easyMeshFabric, error) {
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Get("http://" + net.JoinHostPort(ip, "9080") + "/api/mtk/easymesh/fabric")
	if err != nil {
		return easyMeshFabric{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return easyMeshFabric{}, fmt.Errorf("peer fabric returned HTTP %d", response.StatusCode)
	}
	var payload easyMeshFabricResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return easyMeshFabric{}, err
	}
	return easyMeshFabric{
		Hostname: payload.Hostname,
		Antennas: payload.Antennas,
		Clients:  payload.Clients,
	}, nil
}

// easyMeshPeerFabricSnapshot returns each peer's fabric by AL-MAC, refreshing in
// the background on the same terms as the local one: the Mesh page polls every
// three seconds and must never wait on a peer that has gone quiet.
func easyMeshPeerFabricSnapshot(peerIPByALID map[string]string) map[string]easyMeshFabric {
	easyMeshPeerFabricMu.Lock()
	cached := easyMeshPeerFabricCache
	stale := time.Since(easyMeshPeerFabricStamp) > easyMeshFabricTTL
	if stale && !easyMeshPeerFabricInFlight && len(peerIPByALID) > 0 {
		easyMeshPeerFabricInFlight = true
		peers := make(map[string]string, len(peerIPByALID))
		for alID, ip := range peerIPByALID {
			peers[alID] = ip
		}
		go refreshEasyMeshPeerFabric(peers)
	}
	easyMeshPeerFabricMu.Unlock()
	return cached
}

func refreshEasyMeshPeerFabric(peers map[string]string) {
	collected := make(map[string]easyMeshFabric, len(peers))
	for alID, ip := range peers {
		fabric, err := easyMeshFetchPeerFabric(ip)
		if err != nil {
			log.Printf("EasyMesh: could not read fabric from peer %s (%s): %v", alID, ip, err)
			continue
		}
		collected[alID] = fabric
	}
	easyMeshPeerFabricMu.Lock()
	easyMeshPeerFabricCache = collected
	easyMeshPeerFabricStamp = time.Now()
	easyMeshPeerFabricInFlight = false
	easyMeshPeerFabricMu.Unlock()
}

// ---------- Leaving on the upstream's instruction ----------

// easyMeshPeerLeaveHandler returns this Agent to standalone because its
// Controller is tearing the Mesh down.
//
// It answers before doing the work, and does the work in the background, for a
// blunt reason: leaving restores this device's pre-Mesh network, which drops the
// very Backhaul the reply would have travelled over. Holding the connection open
// would guarantee the Controller sees a failure on a leave that in fact
// succeeded.
func easyMeshPeerLeaveHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	if !easyMeshUpstreamRequestAllowed(loadEasyMeshConfig(), requestSourceIP(r)) {
		http.Error(w, `{"error":"forbidden"}`, http.StatusForbidden)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"accepted":true}`))
	// Push the reply out before the restore can take the network away.
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}

	go func() {
		log.Printf("EasyMesh: leaving at the Controller's request")
		if err := leaveEasyMesh(); err != nil {
			// Nothing upstream is listening any more, so the log is the only
			// record. The unit stays an Agent and has to be recovered from its
			// own portal on the Antenna management VLAN.
			log.Printf("EasyMesh: leaving at the Controller's request failed: %v", err)
			return
		}
		log.Printf("EasyMesh: returned to standalone at the Controller's request")
	}()
}

// ---------- Telling Agents to leave ----------

var easyMeshRequestPeerLeave = func(ip string) error {
	// Long enough to hand the request over, short enough that an Agent that has
	// already dropped off cannot hold up the Controller's own teardown. The Agent
	// answers immediately and restores in its own time, so this never waits for
	// the restore itself.
	client := &http.Client{Timeout: 4 * time.Second}
	response, err := client.Post(
		"http://"+net.JoinHostPort(ip, "9080")+"/api/mtk/easymesh/peer-leave", "application/json", nil)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4096))
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		return fmt.Errorf("peer leave returned HTTP %d", response.StatusCode)
	}
	return nil
}

// notifyPeersToLeave asks every Agent to return to standalone before this
// Controller tears its own Mesh down.
//
// Leaving is otherwise purely local, which used to strand every Agent: it kept
// role=agent with proto=dhcp on a Backhaul that no longer existed, so it lost
// its address and its portal along with it.
//
// This does not wait for the Agents to finish, by design -- each one restores
// itself locally and the Backhaul dies the moment either side does. The cost is
// that an Agent already out of contact still cannot be told, so the count of
// failures is returned for the caller to report rather than swallowed.
func notifyPeersToLeave() (notified int, unreachable int) {
	return notifyPeersToLeaveVia(easyMeshManagementNodesByALID(), easyMeshLocalALID())
}

func notifyPeersToLeaveVia(nodes map[string]easyMeshManagementNode, localALID string) (notified int, unreachable int) {
	for alID, node := range nodes {
		if node.IP == "" || alID == localALID {
			continue
		}
		if err := easyMeshRequestPeerLeave(node.IP); err != nil {
			log.Printf("EasyMesh: could not tell Agent %s (%s) to leave: %v", alID, node.IP, err)
			unreachable++
			continue
		}
		log.Printf("EasyMesh: told Agent %s (%s) to leave", alID, node.IP)
		notified++
	}
	return notified, unreachable
}
