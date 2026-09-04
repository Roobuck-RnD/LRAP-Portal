package main

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// EasyMeshTopologyGraph is the drawable form of the vendor topology dump: who is
// in the Mesh, what carries each hop, and which clients hang off each node.
type EasyMeshTopologyGraph struct {
	Nodes    []EasyMeshTopologyNode    `json:"nodes"`
	Links    []EasyMeshTopologyLink    `json:"links"`
	Antennas []EasyMeshTopologyAntenna `json:"antennas"`
	Clients  []EasyMeshTopologyClient  `json:"clients"`
}

type EasyMeshTopologyNode struct {
	ALID         string `json:"al_id"`
	Label        string `json:"label"`
	Role         string `json:"role"`
	Distance     int    `json:"distance"`
	MapVersion   string `json:"map_version,omitempty"`
	IsLocal      bool   `json:"is_local"`
	Hostname     string `json:"hostname,omitempty"`
	ManagementIP string `json:"management_ip,omitempty"`
	// Whether this chassis's Antennas and clients have been accounted for. Only
	// the chassis that owns them can enumerate them -- Antenna management lives on
	// a chassis-local VLAN and every chassis uses the same 172.31.255.0/29 -- so a
	// peer's fabric is known only once that peer has reported it.
	FabricKnown bool                    `json:"fabric_known"`
	Radios      []EasyMeshTopologyRadio `json:"radios"`
	Clients     []EasyMeshTopologyPeer  `json:"clients"`
}

type EasyMeshTopologyRadio struct {
	Band      string   `json:"band"`
	Channel   string   `json:"channel,omitempty"`
	Bandwidth string   `json:"bandwidth,omitempty"`
	Streams   string   `json:"streams,omitempty"`
	BSSID     string   `json:"bssid,omitempty"`
	SSIDs     []string `json:"ssids"`
}

type EasyMeshTopologyPeer struct {
	MAC  string `json:"mac"`
	Band string `json:"band,omitempty"`
	RSSI string `json:"rssi,omitempty"`
}

type EasyMeshTopologyLink struct {
	FromALID string `json:"from_al_id"`
	ToALID   string `json:"to_al_id"`
	Medium   string `json:"medium,omitempty"`
	RSSI     string `json:"rssi,omitempty"`
	Wireless bool   `json:"wireless"`
	// Driver counters for the hop. A Backhaul can report "associated" while
	// carrying nothing, so the packet totals are the honest health signal.
	ThroughputCap    string `json:"throughput_cap,omitempty"`
	LinkAvailability string `json:"link_availability,omitempty"`
	TxPackets        string `json:"tx_packets,omitempty"`
	RxPackets        string `json:"rx_packets,omitempty"`
	TxErrors         string `json:"tx_errors,omitempty"`
	RxErrors         string `json:"rx_errors,omitempty"`
}

// The dump repeats each BSS's WPA passphrase in clear text. It is shown verbatim
// in the portal's diagnostic block and travels over a plain-HTTP API, so it is
// removed before the dump leaves the device. Nothing in the portal reads it --
// the configured credentials already come from the Mesh settings themselves.
var easyMeshTopologyPassphrase = regexp.MustCompile(`("Pass-phrase"\s*:\s*")[^"]*(")`)

func easyMeshRedactTopology(raw string) string {
	return easyMeshTopologyPassphrase.ReplaceAllString(raw, "${1}"+"••••••••"+"${2}")
}

// easyMeshTopologyRoleOf prefers the hop count over the vendor's role code: the
// dump is not ordered by role, and an Agent's own dump lists itself first.
func easyMeshTopologyRoleOf(device easyMeshTopologyDevice) (string, int) {
	distance, err := strconv.Atoi(strings.TrimSpace(device.Distance))
	if err != nil || distance < 0 {
		distance = -1
	}
	if distance == 0 || strings.TrimSpace(device.Role) == "01" {
		return "controller", 0
	}
	if distance < 0 {
		distance = 1
	}
	return "agent", distance
}

func easyMeshTopologyGraphFrom(raw string, localALID string, managementByALID map[string]easyMeshManagementNode) EasyMeshTopologyGraph {
	return easyMeshTopologyGraphWithFabric(raw, localALID, managementByALID, easyMeshFabric{})
}

func easyMeshTopologyGraphWithFabric(raw string, localALID string, managementByALID map[string]easyMeshManagementNode, fabric easyMeshFabric) EasyMeshTopologyGraph {
	return easyMeshTopologyGraphWithPeerFabric(raw, localALID, managementByALID, fabric, nil)
}

func easyMeshTopologyGraphWithPeerFabric(raw string, localALID string, managementByALID map[string]easyMeshManagementNode, fabric easyMeshFabric, peerFabric map[string]easyMeshFabric) EasyMeshTopologyGraph {
	graph := EasyMeshTopologyGraph{
		Nodes:    []EasyMeshTopologyNode{},
		Links:    []EasyMeshTopologyLink{},
		Antennas: []EasyMeshTopologyAntenna{},
		Clients:  []EasyMeshTopologyClient{},
	}
	topology, err := parseEasyMeshTopology(raw)
	if err != nil {
		return graph
	}
	localALID = strings.ToLower(strings.TrimSpace(localALID))
	// A device's own "BH Info" can lag behind reality; the other end of the same
	// link reports the band and signal in the meantime. See easyMeshBackhaulPeerLinks.
	peerLinks := easyMeshBackhaulPeerLinks(topology)

	for _, device := range topology.Devices {
		alID := strings.ToLower(strings.TrimSpace(device.ALMAC))
		if alID == "" {
			continue
		}
		role, distance := easyMeshTopologyRoleOf(device)

		node := EasyMeshTopologyNode{
			ALID:       alID,
			Role:       role,
			Distance:   distance,
			MapVersion: strings.TrimSpace(device.MapVersion),
			IsLocal:    alID == localALID,
			Radios:     []EasyMeshTopologyRadio{},
			Clients:    []EasyMeshTopologyPeer{},
		}
		if managed, ok := managementByALID[alID]; ok {
			node.ManagementIP = managed.IP
		}

		seenClient := make(map[string]bool)
		for _, radio := range device.RadioInfo {
			band := easyMeshTopologyBandLabel(radio.Band)
			seenSSID := make(map[string]bool)
			ssids := []string{}
			for _, bss := range radio.BSSInfo {
				if ssid := strings.TrimSpace(bss.SSID); ssid != "" && !seenSSID[ssid] {
					seenSSID[ssid] = true
					ssids = append(ssids, ssid)
				}
				for _, station := range bss.Stations {
					mac := strings.ToLower(strings.TrimSpace(station.MAC))
					// A Backhaul station is the other end of a Mesh hop; it is drawn
					// as a link, and counting it as a client would double it.
					if mac == "" || seenClient[mac] || strings.EqualFold(strings.TrimSpace(station.BackhaulSTA), "Yes") {
						continue
					}
					seenClient[mac] = true
					clientBand := easyMeshTopologyBandLabel(station.Medium)
					if clientBand == "" {
						clientBand = band
					}
					node.Clients = append(node.Clients, EasyMeshTopologyPeer{
						MAC:  mac,
						Band: clientBand,
						RSSI: strings.TrimSpace(station.UplinkRSSI),
					})
				}
			}
			sort.Strings(ssids)
			if band == "" && len(ssids) == 0 {
				continue
			}
			bssid := ""
			if len(radio.BSSInfo) > 0 {
				bssid = strings.ToLower(strings.TrimSpace(radio.BSSInfo[0].BSSID))
			}
			node.Radios = append(node.Radios, EasyMeshTopologyRadio{
				Band:      band,
				Channel:   strings.TrimSpace(radio.Channel),
				Bandwidth: easyMeshTopologyOptional(radio.Bandwidth),
				Streams:   easyMeshTopologyStreams(radio.TxStreams, radio.RxStreams),
				BSSID:     bssid,
				SSIDs:     ssids,
			})
		}
		sort.Slice(node.Clients, func(i, j int) bool { return node.Clients[i].MAC < node.Clients[j].MAC })
		graph.Nodes = append(graph.Nodes, node)

		if link, ok := easyMeshTopologyLinkFor(device, alID, peerLinks); ok {
			graph.Links = append(graph.Links, link)
		}
	}

	sort.SliceStable(graph.Nodes, func(i, j int) bool {
		if graph.Nodes[i].Distance != graph.Nodes[j].Distance {
			return graph.Nodes[i].Distance < graph.Nodes[j].Distance
		}
		return graph.Nodes[i].ALID < graph.Nodes[j].ALID
	})
	agent := 0
	for i := range graph.Nodes {
		if graph.Nodes[i].Role == "controller" {
			graph.Nodes[i].Label = "Root Controller"
			continue
		}
		agent++
		graph.Nodes[i].Label = fmt.Sprintf("Mesh Agent %d", agent)
	}
	sort.SliceStable(graph.Links, func(i, j int) bool { return graph.Links[i].ToALID < graph.Links[j].ToALID })

	stations := easyMeshBackhaulStationMACs(topology)
	graph.attachFabric(fabric, localALID, stations)
	for peerALID, peer := range peerFabric {
		graph.attachFabric(peer, peerALID, stations)
	}
	graph.nameClientsFromLeases()
	sort.SliceStable(graph.Antennas, func(i, j int) bool {
		if graph.Antennas[i].ChassisALID != graph.Antennas[j].ChassisALID {
			return graph.Antennas[i].ChassisALID < graph.Antennas[j].ChassisALID
		}
		return graph.Antennas[i].Name < graph.Antennas[j].Name
	})
	sort.SliceStable(graph.Clients, func(i, j int) bool { return graph.Clients[i].MAC < graph.Clients[j].MAC })
	return graph
}

// attachFabric hangs one chassis's Antennas, and every client, off the node that
// actually serves them. It runs once for this device and once per peer that has
// reported its own fabric.
func (graph *EasyMeshTopologyGraph) attachFabric(fabric easyMeshFabric, chassisALID string, backhaulStations map[string]bool) {
	if chassisALID == "" {
		return
	}
	owner := -1
	for i, node := range graph.Nodes {
		if node.ALID == chassisALID {
			owner = i
			break
		}
	}
	if owner < 0 {
		return
	}
	graph.Nodes[owner].FabricKnown = true
	if fabric.Hostname != "" {
		graph.Nodes[owner].Hostname = fabric.Hostname
	}

	for _, antenna := range fabric.Antennas {
		// Module IDs are only unique within a chassis -- two chassis can both hold
		// an "antenna:3" -- so they are qualified before they share one drawing.
		antenna.ID = easyMeshFabricNodeID(chassisALID, antenna.ID)
		antenna.ChassisALID = chassisALID
		graph.Antennas = append(graph.Antennas, antenna)
	}
	for _, client := range fabric.Clients {
		// An Agent's Backhaul station associates to the Root's Fronthaul BSS just
		// like a laptop, so the Connected Clients collector lists it as a client of
		// the Root. Drawing it here would show a phantom device that is really the
		// far end of a Mesh hop already drawn as a link.
		if backhaulStations[client.MAC] {
			continue
		}
		if client.ParentID == easyMeshFabricChassisParent {
			client.ParentID = chassisALID
		} else {
			client.ParentID = easyMeshFabricNodeID(chassisALID, client.ParentID)
		}
		graph.Clients = append(graph.Clients, client)
	}
}

func easyMeshFabricNodeID(chassisALID string, moduleID string) string {
	return chassisALID + "/" + moduleID
}

// easyMeshBackhaulStationMACs collects every address that is one end of a Mesh
// hop rather than a client: the station each Agent uses upstream, and any
// station the driver has already flagged as a Backhaul peer.
func easyMeshBackhaulStationMACs(topology easyMeshTopology) map[string]bool {
	stations := make(map[string]bool)
	for _, device := range topology.Devices {
		for _, sta := range device.BackhaulSTAInterfaces {
			if mac := strings.ToLower(strings.TrimSpace(sta.MAC)); mac != "" {
				stations[mac] = true
			}
		}
		for _, radio := range device.RadioInfo {
			for _, bss := range radio.BSSInfo {
				for _, station := range bss.Stations {
					if !strings.EqualFold(strings.TrimSpace(station.BackhaulSTA), "Yes") {
						continue
					}
					if mac := strings.ToLower(strings.TrimSpace(station.MAC)); mac != "" {
						stations[mac] = true
					}
				}
			}
		}
	}
	return stations
}

// easyMeshTopologyLinkFor returns the hop that carries this device towards the
// Controller. The upstream AL-MAC names the peer; "BH Info" says what the hop
// runs over. Either can be missing on its own, so both are consulted.
func easyMeshTopologyLinkFor(
	device easyMeshTopologyDevice,
	alID string,
	peerLinks map[string]easyMeshBackhaulPeerLink,
) (EasyMeshTopologyLink, bool) {
	upstream := strings.ToLower(strings.TrimSpace(device.UpstreamALID))
	medium, rssi := "", ""
	for _, backhaul := range device.BackhaulInfo {
		if strings.TrimSpace(backhaul.Medium) == "" {
			continue
		}
		medium = easyMeshTopologyBandLabel(backhaul.Medium)
		rssi = strings.TrimSpace(backhaul.RSSI)
		if upstream == "" {
			upstream = strings.ToLower(strings.TrimSpace(backhaul.NeighborAL))
		}
		break
	}
	if medium == "" {
		// Describe the hop from the upstream's side while this device's own record
		// catches up, so the drawing does not show a blank link across a Backhaul
		// that is plainly carrying traffic.
		for _, sta := range device.BackhaulSTAInterfaces {
			peer, ok := peerLinks[strings.ToLower(strings.TrimSpace(sta.MAC))]
			if !ok {
				continue
			}
			medium = easyMeshTopologyBandLabel(peer.Medium)
			rssi = peer.RSSI
			break
		}
	}
	if upstream == "" || upstream == alID {
		return EasyMeshTopologyLink{}, false
	}
	link := EasyMeshTopologyLink{
		FromALID: upstream,
		ToALID:   alID,
		Medium:   medium,
		RSSI:     rssi,
		Wireless: medium != "" && !strings.EqualFold(medium, "Ethernet"),
	}
	for _, entry := range device.BackhaulMetrics {
		if !strings.EqualFold(strings.TrimSpace(entry.NeighborAL), upstream) {
			continue
		}
		for _, metric := range entry.Metrics {
			link.ThroughputCap = easyMeshTopologyOptional(metric.ThroughputCap)
			link.LinkAvailability = easyMeshTopologyOptional(metric.LinkAvailability)
			link.TxPackets = easyMeshTopologyOptional(metric.TxPackets)
			link.RxPackets = easyMeshTopologyOptional(metric.RxPackets)
			link.TxErrors = easyMeshTopologyOptional(metric.TxErrors)
			link.RxErrors = easyMeshTopologyOptional(metric.RxErrors)
			if link.RSSI == "" {
				link.RSSI = strings.TrimSpace(metric.RSSI)
			}
			break
		}
		break
	}
	return link, true
}

// The dump writes "NA" where the driver has nothing, which must not reach the
// drawing as if it were a reading.
func easyMeshTopologyOptional(value string) string {
	trimmed := strings.TrimSpace(value)
	if strings.EqualFold(trimmed, "NA") || strings.EqualFold(trimmed, "N/A") {
		return ""
	}
	return trimmed
}

func easyMeshTopologyStreams(tx string, rx string) string {
	transmit, receive := easyMeshTopologyOptional(tx), easyMeshTopologyOptional(rx)
	if transmit == "" || receive == "" {
		return ""
	}
	return transmit + "x" + receive
}

// The dump writes "2G" for the 2.4 GHz band. Operators read channel numbers off
// a phone, which calls it 2.4 GHz.
func easyMeshTopologyBandLabel(band string) string {
	switch strings.ToUpper(strings.TrimSpace(band)) {
	case "2G", "2.4G", "2.4GHZ":
		return "2.4 GHz"
	case "5G", "5GHZ":
		return "5 GHz"
	case "6G", "6GHZ":
		return "6 GHz"
	case "ETHERNET":
		return "Ethernet"
	}
	return strings.TrimSpace(band)
}

// easyMeshLocalBackhaulStationMACs reports the addresses that are one end of a
// Mesh hop as this device currently sees them. It is empty unless the Mesh is
// actually running, so a standalone unit pays nothing for the check.
func easyMeshLocalBackhaulStationMACs() map[string]bool {
	cfg := loadEasyMeshConfig()
	if !cfg.Enabled || !easyMeshLocalEngineRunning() {
		return nil
	}
	raw, err := easyMeshLocalTopologySnapshot()
	if err != nil {
		return nil
	}
	topology, err := parseEasyMeshTopology(raw)
	if err != nil {
		return nil
	}
	return easyMeshBackhaulStationMACs(topology)
}

// nameClientsFromLeases fills in the address and name of clients whose own
// chassis cannot resolve them.
//
// An Agent has no DHCP server: its clients are leased by the Root, so the
// Agent's own collector sees a MAC and nothing else. Measured on the pair -- a
// phone on the Agent's Antenna drew as a bare MAC while the Root's lease table
// held "10.10.18.244 iPhone" for exactly that address. Whichever device is
// assembling this drawing has the lease file, so it answers for every client
// rather than only its own.
func (graph *EasyMeshTopologyGraph) nameClientsFromLeases() {
	leases := easyMeshClientLeases()
	if len(leases) == 0 {
		return
	}
	for i := range graph.Clients {
		lease, ok := leases[strings.ToLower(strings.TrimSpace(graph.Clients[i].MAC))]
		if !ok {
			continue
		}
		if strings.TrimSpace(graph.Clients[i].IP) == "" {
			graph.Clients[i].IP = lease.IP
		}
		if !easyMeshClientIsNamed(graph.Clients[i].Hostname) && easyMeshClientIsNamed(lease.Hostname) {
			graph.Clients[i].Hostname = lease.Hostname
		}
	}
}

// The collector writes "(unknown)" when it has no name, so an absent name is
// not simply an empty string.
func easyMeshClientIsNamed(hostname string) bool {
	trimmed := strings.TrimSpace(hostname)
	return trimmed != "" && !strings.EqualFold(trimmed, "(unknown)")
}

var easyMeshClientLeases = func() map[string]ccDhcpLease {
	byMAC := make(map[string]ccDhcpLease)
	for mac, lease := range ccParseLocalDHCPLeases() {
		byMAC[strings.ToLower(strings.TrimSpace(mac))] = lease
	}
	return byMAC
}
