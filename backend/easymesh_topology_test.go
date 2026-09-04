package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"testing"
)

func realTopologyDump(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("testdata/mapd_dump_topology_v1.json")
	if err != nil {
		t.Fatalf("read captured dump: %v", err)
	}
	return string(raw)
}

// The graph is built from a dump captured off the paired chassis, so a firmware
// quirk cannot pass here and fail on the device.
func TestTopologyGraphFromCapturedDump(t *testing.T) {
	graph := easyMeshTopologyGraphFrom(realTopologyDump(t), "f0:a8:82:fb:00:a4",
		map[string]easyMeshManagementNode{
			"de:84:03:11:57:80": {IP: "10.10.18.127", NodeID: "dc:84:03:01:57:80"},
		})

	if len(graph.Nodes) != 2 {
		t.Fatalf("nodes = %#v, want the Controller and one Agent", graph.Nodes)
	}
	root, agent := graph.Nodes[0], graph.Nodes[1]
	if root.Role != "controller" || root.Label != "Root Controller" || root.Distance != 0 {
		t.Fatalf("first node is not the Controller: %#v", root)
	}
	if !root.IsLocal {
		t.Fatalf("the Controller is this device but was not marked local: %#v", root)
	}
	if agent.Role != "agent" || agent.Label != "Mesh Agent 1" || agent.Distance != 1 {
		t.Fatalf("second node is not the Agent: %#v", agent)
	}
	if agent.ManagementIP != "10.10.18.127" {
		t.Fatalf("the Agent's management address was not carried into the graph: %#v", agent)
	}

	if len(graph.Links) != 1 {
		t.Fatalf("links = %#v, want the single Backhaul hop", graph.Links)
	}
	link := graph.Links[0]
	if link.FromALID != "f0:a8:82:fb:00:a4" || link.ToALID != "de:84:03:11:57:80" {
		t.Fatalf("the hop does not run Controller -> Agent: %#v", link)
	}
	// RSSI is a live reading, so the hop is checked for a plausible dBm value
	// rather than the exact one that happened to be captured.
	dBm, err := strconv.Atoi(link.RSSI)
	if link.Medium != "5 GHz" || !link.Wireless || err != nil || dBm >= 0 || dBm < -100 {
		t.Fatalf("the hop lost its medium or signal: %#v", link)
	}
}

// The Backhaul station appears in the Controller's own "connected sta info". It
// is one end of a Mesh hop, so drawing it as a client would show a phantom
// device that is really the Agent counted twice.
func TestBackhaulStationIsNotDrawnAsAClient(t *testing.T) {
	graph := easyMeshTopologyGraphFrom(realTopologyDump(t), "", nil)
	root := graph.Nodes[0]
	if len(root.Clients) != 1 || root.Clients[0].MAC != "02:5e:9a:1f:09:51" {
		t.Fatalf("clients = %#v, want only the real station", root.Clients)
	}
	if root.Clients[0].RSSI != "-79" || root.Clients[0].Band != "5 GHz" {
		t.Fatalf("the client lost its signal or band: %#v", root.Clients[0])
	}
}

func TestTopologyGraphReportsRadioChannels(t *testing.T) {
	graph := easyMeshTopologyGraphFrom(realTopologyDump(t), "", nil)
	byBand := map[string]EasyMeshTopologyRadio{}
	for _, radio := range graph.Nodes[0].Radios {
		byBand[radio.Band] = radio
	}
	five, ok := byBand["5 GHz"]
	if !ok || five.Channel != "48" {
		t.Fatalf("5 GHz radio = %#v, want channel 48", five)
	}
	// The dump writes "2G"; operators read 2.4 GHz off their phone.
	two, ok := byBand["2.4 GHz"]
	if !ok || two.Channel != "5" {
		t.Fatalf("2.4 GHz radio = %#v, want channel 5", two)
	}
	if len(five.SSIDs) != 1 || five.SSIDs[0] != "Chase_Test" {
		t.Fatalf("5 GHz SSIDs = %#v", five.SSIDs)
	}
}

// An Agent's dump lists itself first. Ordering by hop count rather than by
// position keeps the Controller at the root of the drawing on both roles.
func TestAgentDumpStillDrawsTheControllerAsRoot(t *testing.T) {
	reversed := `{"topology information":[
		{"AL MAC":"de:84:03:11:57:80","Device role":"02","Distance from controller":"1","Upstream 1905 device":"f0:a8:82:fb:00:a4","BH Info":[{"neighbor almac addr":"f0:a8:82:fb:00:a4","Backhaul Medium Type":"5G","RSSI":"-72"}]},
		{"AL MAC":"f0:a8:82:fb:00:a4","Device role":"01","Distance from controller":"0","BH Info":[]}
	]}`
	graph := easyMeshTopologyGraphFrom(reversed, "de:84:03:11:57:80", nil)
	if graph.Nodes[0].Role != "controller" || !graph.Nodes[1].IsLocal {
		t.Fatalf("the Agent's own dump did not resolve to a rooted graph: %#v", graph.Nodes)
	}
}

// The dump repeats every BSS passphrase in clear text, and the portal renders
// the dump verbatim in its diagnostics over plain HTTP.
func TestTopologyDumpNeverCarriesThePassphrase(t *testing.T) {
	raw := realTopologyDump(t)
	if !strings.Contains(raw, `"Pass-phrase":"12345678"`) {
		t.Fatalf("the fixture no longer exercises redaction")
	}
	redacted := easyMeshRedactTopology(raw)
	if strings.Contains(redacted, "12345678") {
		t.Fatalf("the passphrase survived redaction")
	}
	// The field itself stays, so the diagnostic dump keeps its shape.
	if !strings.Contains(redacted, `"Pass-phrase":"`) {
		t.Fatalf("redaction removed the field instead of its value")
	}
	if strings.Count(redacted, "Chase_Test") != strings.Count(raw, "Chase_Test") {
		t.Fatalf("redaction damaged the rest of the dump")
	}
}

func fabricFixture() easyMeshFabric {
	return easyMeshFabric{
		Antennas: []EasyMeshTopologyAntenna{
			{ID: "antenna-3", Name: "Antenna3", Port: "lan3", IP: "172.31.255.3", Online: true},
		},
		Clients: []EasyMeshTopologyClient{
			// A laptop on the Antenna.
			{MAC: "f4:3b:d8:7f:cc:40", IP: "10.10.18.249", Hostname: "DESKTOP-KC17O22",
				SSID: "Chase_Test", Band: "2.4 GHz", Interface: "ra0", RSSI: -46, ParentID: "antenna-3"},
			// The Agent's Backhaul station, which associates to the Root exactly
			// like a client and is reported as one.
			{MAC: "d6:84:03:01:57:80", SSID: "Chase_Test", Band: "5 GHz", Interface: "rax0",
				RSSI: -40, ParentID: easyMeshFabricChassisParent},
		},
	}
}

// An end device must hang off the module that actually serves it, not off the
// chassis that happens to own that module.
func TestClientsAreAttachedToTheServingAntenna(t *testing.T) {
	graph := easyMeshTopologyGraphWithFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabricFixture())

	if len(graph.Antennas) != 1 || graph.Antennas[0].ChassisALID != "f0:a8:82:fb:00:a4" {
		t.Fatalf("antennas = %#v, want one owned by the Controller", graph.Antennas)
	}
	if graph.Antennas[0].Port != "lan3" || graph.Antennas[0].IP != "172.31.255.3" {
		t.Fatalf("the Antenna lost its port or address: %#v", graph.Antennas[0])
	}
	if len(graph.Clients) != 1 {
		t.Fatalf("clients = %#v, want only the laptop", graph.Clients)
	}
	client := graph.Clients[0]
	if client.ParentID != graph.Antennas[0].ID {
		t.Fatalf("the laptop was not attached to the Antenna serving it: %#v", client)
	}
	if client.Hostname != "DESKTOP-KC17O22" || client.IP != "10.10.18.249" || client.RSSI != -46 {
		t.Fatalf("the laptop lost its identity: %#v", client)
	}
}

// The Backhaul station is drawn as a Mesh hop. Listing it again as a client
// shows a phantom device that is really the Agent counted twice -- which is
// exactly what the Connected Clients collector reports on the live Controller.
func TestBackhaulStationIsNotDrawnAsAFabricClient(t *testing.T) {
	graph := easyMeshTopologyGraphWithFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabricFixture())
	for _, client := range graph.Clients {
		if client.MAC == "d6:84:03:01:57:80" {
			t.Fatalf("the Agent's Backhaul station was drawn as an end device: %#v", client)
		}
	}
}

// A client on the chassis radio itself is re-parented onto the chassis, so the
// drawing never leaves it dangling.
func TestChassisRadioClientsAreParentedOntoTheChassis(t *testing.T) {
	fabric := easyMeshFabric{
		Clients: []EasyMeshTopologyClient{
			{MAC: "aa:bb:cc:dd:ee:ff", Band: "5 GHz", ParentID: easyMeshFabricChassisParent},
		},
	}
	graph := easyMeshTopologyGraphWithFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabric)
	if len(graph.Clients) != 1 || graph.Clients[0].ParentID != "f0:a8:82:fb:00:a4" {
		t.Fatalf("clients = %#v, want the chassis as parent", graph.Clients)
	}
}

// A chassis that has not reported its fabric must be marked as such, so the
// drawing never presents "no Antennas" and "Antennas not collected" alike.
func TestChassisWithoutAReportedFabricIsMarkedUnknown(t *testing.T) {
	graph := easyMeshTopologyGraphWithFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabricFixture())
	for _, node := range graph.Nodes {
		want := node.ALID == "f0:a8:82:fb:00:a4"
		if node.FabricKnown != want {
			t.Fatalf("node %s FabricKnown = %v, want %v", node.ALID, node.FabricKnown, want)
		}
	}
}

// The Agent reports its own Antennas to the Root, which cannot reach them
// directly. Module IDs are only unique within a chassis, so two chassis holding
// the same module number must not collapse into one node.
func TestPeerFabricIsMergedWithoutCollidingModuleIDs(t *testing.T) {
	peer := map[string]easyMeshFabric{
		"de:84:03:11:57:80": {
			Antennas: []EasyMeshTopologyAntenna{
				// Same module ID as the Controller's own Antenna.
				{ID: "antenna-3", Name: "Antenna2", Port: "lan2", IP: "172.31.255.3", Online: true},
			},
			Clients: []EasyMeshTopologyClient{
				{MAC: "11:22:33:44:55:66", Hostname: "phone", ParentID: "antenna-3"},
			},
		},
	}
	graph := easyMeshTopologyGraphWithPeerFabric(
		realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabricFixture(), peer)

	if len(graph.Antennas) != 2 {
		t.Fatalf("antennas = %#v, want one per chassis", graph.Antennas)
	}
	if graph.Antennas[0].ID == graph.Antennas[1].ID {
		t.Fatalf("the two chassis' Antennas collapsed onto one ID: %#v", graph.Antennas)
	}
	byChassis := map[string]EasyMeshTopologyAntenna{}
	for _, antenna := range graph.Antennas {
		byChassis[antenna.ChassisALID] = antenna
	}
	remote, ok := byChassis["de:84:03:11:57:80"]
	if !ok || remote.Name != "Antenna2" {
		t.Fatalf("the Agent's Antenna is missing: %#v", graph.Antennas)
	}

	var phone EasyMeshTopologyClient
	for _, client := range graph.Clients {
		if client.MAC == "11:22:33:44:55:66" {
			phone = client
		}
	}
	if phone.ParentID != remote.ID {
		t.Fatalf("the Agent's client did not land on the Agent's Antenna: %#v", phone)
	}

	// Both chassis have now accounted for their fabric.
	for _, node := range graph.Nodes {
		if !node.FabricKnown {
			t.Fatalf("node %s still reports an unknown fabric", node.ALID)
		}
	}
}

// These endpoints carry no session, so the guard is the whole of their access
// control: only an onboarded Agent answers, and only to its own upstream. It
// gates the leave instruction as well as the fabric read, so a stranger can
// neither read the client list nor tear the Mesh down.
func TestUpstreamOnlyEndpointsRefuseEveryoneElse(t *testing.T) {
	original := defaultGatewayIPv4
	t.Cleanup(func() { defaultGatewayIPv4 = original })
	defaultGatewayIPv4 = func() string { return "10.10.18.1" }

	agent := EasyMeshConfig{Enabled: true, Role: "agent"}
	if !easyMeshUpstreamRequestAllowed(agent, "10.10.18.1") {
		t.Fatalf("the Agent refused its own Controller")
	}
	for _, caller := range []string{"10.10.18.249", "10.10.18.127", "", "10.10.18.10"} {
		if easyMeshUpstreamRequestAllowed(agent, caller) {
			t.Fatalf("a request from %q was served the fabric", caller)
		}
	}
	// A device that is not an onboarded Agent exposes nothing at all, so a
	// standalone unit on an untrusted network never answers.
	for _, cfg := range []EasyMeshConfig{
		{Enabled: false, Role: "agent"},
		{Enabled: true, Role: "controller"},
		{Enabled: true, Role: "standalone"},
	} {
		if easyMeshUpstreamRequestAllowed(cfg, "10.10.18.1") {
			t.Fatalf("config %#v served the fabric", cfg)
		}
	}
}

// The same phantom device the topology drawing already excludes must not survive
// on the Connected Clients page either.
func TestConnectedClientsDropTheBackhaulStation(t *testing.T) {
	modules := []ConnectedClientModule{
		{
			ModuleID: "main", Type: "main", Name: "Roobuck",
			Clients: []ConnectedClient{
				// Exactly what the live Root reported: no hostname, no address.
				{MAC: "D6:84:03:01:57:80", SSID: "Chase_Test", Band: "5G", Interface: "rax0", RSSI: -29},
				{MAC: "AA:BB:CC:DD:EE:01", Hostname: "phone", IP: "10.10.18.50"},
			},
		},
		{
			ModuleID: "antenna:3", Type: "ap", Name: "Antenna3",
			Clients: []ConnectedClient{
				{MAC: "F4:3B:D8:7F:CC:40", Hostname: "DESKTOP-KC17O22", IP: "10.10.18.249"},
			},
		},
	}
	// The set is lowercase; the collector reports MACs uppercase.
	stations := map[string]bool{"d6:84:03:01:57:80": true}

	if dropped := ccDropBackhaulStations(modules, stations); dropped != 1 {
		t.Fatalf("dropped = %d, want the single Backhaul station", dropped)
	}
	if len(modules[0].Clients) != 1 || modules[0].Clients[0].MAC != "AA:BB:CC:DD:EE:01" {
		t.Fatalf("main module clients = %#v, want only the phone", modules[0].Clients)
	}
	if len(modules[1].Clients) != 1 {
		t.Fatalf("the Antenna's client was disturbed: %#v", modules[1].Clients)
	}
}

// Without a Mesh there are no Backhaul stations, and the client lists must pass
// through untouched.
func TestConnectedClientsAreUntouchedWithoutAMesh(t *testing.T) {
	modules := []ConnectedClientModule{
		{ModuleID: "main", Type: "main", Clients: []ConnectedClient{{MAC: "AA:BB:CC:DD:EE:01"}}},
	}
	if dropped := ccDropBackhaulStations(modules, nil); dropped != 0 {
		t.Fatalf("dropped = %d with no Mesh running", dropped)
	}
	if len(modules[0].Clients) != 1 {
		t.Fatalf("clients = %#v, want the list untouched", modules[0].Clients)
	}
}

// The vendor counters are the honest health signal for a hop: a Backhaul can
// report "associated" while carrying nothing.
func TestBackhaulHopCarriesItsDriverCounters(t *testing.T) {
	graph := easyMeshTopologyGraphFrom(realTopologyDump(t), "", nil)
	if len(graph.Links) != 1 {
		t.Fatalf("links = %#v", graph.Links)
	}
	link := graph.Links[0]
	if link.ThroughputCap == "" || link.LinkAvailability == "" {
		t.Fatalf("the hop lost its capacity readings: %#v", link)
	}
	if link.RxPackets == "" {
		t.Fatalf("the hop lost its packet counters: %#v", link)
	}
}

// "NA" means the driver reported nothing; it must not surface as a reading.
func TestUnavailableDriverReadingsAreNotShown(t *testing.T) {
	graph := easyMeshTopologyGraphFrom(realTopologyDump(t), "", nil)
	for _, node := range graph.Nodes {
		for _, radio := range node.Radios {
			if strings.EqualFold(radio.Bandwidth, "NA") {
				t.Fatalf("radio %#v surfaced an unavailable bandwidth", radio)
			}
			// The captured dump reports 2 transmit and 2 receive chains.
			if radio.Streams != "2x2" {
				t.Fatalf("radio %#v lost its spatial stream count", radio)
			}
			if radio.BSSID == "" {
				t.Fatalf("radio %#v lost its BSSID", radio)
			}
		}
	}
}

// The hostname is what an operator actually named the unit, so it must reach the
// node it belongs to -- including a peer that reported it from across the Mesh.
func TestChassisCarriesItsHostnameIncludingFromAPeer(t *testing.T) {
	local := fabricFixture()
	local.Hostname = "Roobuck"
	peer := map[string]easyMeshFabric{
		"de:84:03:11:57:80": {Hostname: "RoobuckAC"},
	}
	graph := easyMeshTopologyGraphWithPeerFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, local, peer)
	byALID := map[string]EasyMeshTopologyNode{}
	for _, node := range graph.Nodes {
		byALID[node.ALID] = node
	}
	if byALID["f0:a8:82:fb:00:a4"].Hostname != "Roobuck" {
		t.Fatalf("the local hostname is missing: %#v", byALID["f0:a8:82:fb:00:a4"])
	}
	if byALID["de:84:03:11:57:80"].Hostname != "RoobuckAC" {
		t.Fatalf("the peer's hostname did not cross the Mesh: %#v", byALID["de:84:03:11:57:80"])
	}
}

// Uptime and the Antenna's own networks are collected anyway; dropping them on
// the way to the drawing is what left a client showing nothing but a name.
func TestFabricCarriesClientUptimeAndAntennaNetworks(t *testing.T) {
	fabric := easyMeshFabric{
		Antennas: []EasyMeshTopologyAntenna{
			{ID: "antenna-3", Name: "Antenna3", SSID24: "Chase_Test", SSID5: "Chase_Test_5G", Online: true},
		},
		Clients: []EasyMeshTopologyClient{
			{MAC: "f4:3b:d8:7f:cc:40", Hostname: "DESKTOP-KC17O22", ConnectedTime: "2h 15m",
				Signal: "Excellent", RSSI: -46, ParentID: "antenna-3"},
		},
	}
	graph := easyMeshTopologyGraphWithFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabric)
	if graph.Antennas[0].SSID24 != "Chase_Test" || graph.Antennas[0].SSID5 != "Chase_Test_5G" {
		t.Fatalf("the Antenna lost the networks it broadcasts: %#v", graph.Antennas[0])
	}
	if graph.Clients[0].ConnectedTime != "2h 15m" || graph.Clients[0].Signal != "Excellent" {
		t.Fatalf("the client lost its uptime or signal wording: %#v", graph.Clients[0])
	}
}

// A Leave used to be purely local, which stranded every Agent: it stayed
// role=agent with proto=dhcp on a Backhaul that no longer existed, losing its
// address and its portal with it. The Controller must speak first.
func TestControllerTellsItsAgentsToLeave(t *testing.T) {
	originalRequest := easyMeshRequestPeerLeave
	originalNodes := easyMeshManagementIdentityAt
	t.Cleanup(func() {
		easyMeshRequestPeerLeave = originalRequest
		easyMeshManagementIdentityAt = originalNodes
	})

	asked := []string{}
	easyMeshRequestPeerLeave = func(ip string) error {
		asked = append(asked, ip)
		if ip == "10.10.18.200" {
			return errors.New("no route to host")
		}
		return nil
	}

	// Two Agents hold leases; one of them has already dropped off.
	leases := map[string]easyMeshManagementNode{
		"de:84:03:11:57:80": {IP: "10.10.18.127"},
		"de:84:03:11:57:81": {IP: "10.10.18.200"},
	}
	notified, unreachable := notifyPeersToLeaveVia(leases, "f0:a8:82:fb:00:a4")

	if notified != 1 || unreachable != 1 {
		t.Fatalf("notified=%d unreachable=%d, want one of each", notified, unreachable)
	}
	if len(asked) != 2 {
		t.Fatalf("asked = %#v, want both Agents attempted", asked)
	}
	// An Agent that cannot be reached must be reported, not silently accepted --
	// it is still stranded and needs recovering from its own portal.
	if unreachable == 0 {
		t.Fatalf("an unreachable Agent was swallowed")
	}
}

// The Controller must never send itself a leave instruction.
func TestControllerDoesNotAskItselfToLeave(t *testing.T) {
	original := easyMeshRequestPeerLeave
	t.Cleanup(func() { easyMeshRequestPeerLeave = original })
	asked := []string{}
	easyMeshRequestPeerLeave = func(ip string) error {
		asked = append(asked, ip)
		return nil
	}
	nodes := map[string]easyMeshManagementNode{
		"f0:a8:82:fb:00:a4": {IP: "10.10.18.1"},
		"de:84:03:11:57:80": {IP: "10.10.18.127"},
	}
	notified, _ := notifyPeersToLeaveVia(nodes, "f0:a8:82:fb:00:a4")
	if notified != 1 || len(asked) != 1 || asked[0] != "10.10.18.127" {
		t.Fatalf("asked = %#v, want only the Agent", asked)
	}
}

// Captured verbatim from both chassis. The portal shows the first line of the
// reply as the runtime state, so the argv echo read out as the Backhaul status.
func TestMapdBannerIsStrippedFromRuntimeReplies(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "controller backhaul status",
			raw: "count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl\n" +
				"Succesfully opened connection to mapd\n" +
				"backhaul connection status 0\n",
			want: "backhaul connection status 0",
		},
		{
			name: "agent role",
			raw: "count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl\n" +
				"Succesfully opened connection to mapd\n" +
				"dev_role 2\n",
			want: "dev_role 2",
		},
		{
			name: "nothing but banner",
			raw:  "count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl\nSuccesfully opened connection to mapd\n",
			want: "",
		},
		{
			name: "a reply that never carried a banner is untouched",
			raw:  "backhaul connection status 1",
			want: "backhaul connection status 1",
		},
	}
	for _, test := range cases {
		if got := easyMeshStripMapdBanner(test.raw); got != test.want {
			t.Fatalf("%s: got %q, want %q", test.name, got, test.want)
		}
	}
}

// Stripping must not change what the product decides about the Backhaul, which
// is what gates handing DHCP over to the Root.
func TestStrippingDoesNotChangeTheBackhaulVerdict(t *testing.T) {
	up := "count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl\nSuccesfully opened connection to mapd\nbackhaul connection status 1"
	down := "count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl\nSuccesfully opened connection to mapd\nbackhaul connection status 0"

	if !easyMeshBackhaulConnected(easyMeshStripMapdBanner(up)) {
		t.Fatalf("a connected Backhaul read as down after stripping")
	}
	if easyMeshBackhaulConnected(easyMeshStripMapdBanner(down)) {
		t.Fatalf("a down Backhaul read as connected after stripping")
	}
	// The banner alone must never be taken for a verdict.
	if easyMeshBackhaulConnected(easyMeshStripMapdBanner("count =3 1=/usr/bin/mapd_cli 2=/tmp/mapd_ctrl")) {
		t.Fatalf("the banner alone was read as a connected Backhaul")
	}
}

// mapd is configured with AutoBHSwitching and moves the Backhaul to whichever
// band it prefers. Measured on the live pair: a 5 GHz selection ended up on
// 2.4 GHz because the signal was 13 dB better. Checking only the selected band
// made the startup recovery declare the Backhaul gone and roll a working Agent
// all the way back to standalone.
func TestBackhaulIsFoundOnWhicheverBandItMovedTo(t *testing.T) {
	original := easyMeshExecLocal
	t.Cleanup(func() { easyMeshExecLocal = original })

	associated := `apcli0    IEEE 802.11ax  ESSID:"Chase_Test"
          Mode:Managed  Access Point: F0:A8:82:FB:00:A4   Bit Rate:286 Mb/s`
	idle := `apclix0   IEEE 802.11ax  ESSID:""
          Mode:Managed  Access Point: Not-Associated   Bit Rate:2.401 Gb/s`

	// The selection is 5 GHz; only the 2.4 GHz station is up.
	easyMeshExecLocal = func(command string) (string, error) {
		if strings.Contains(command, "apcli0") {
			return associated, nil
		}
		return idle, nil
	}
	band, ok := easyMeshActiveBackhaulBand("5g")
	if !ok || band != "2g" {
		t.Fatalf("active band = %q ok=%v, want the 2.4 GHz station the Backhaul moved to", band, ok)
	}
}

// When the Backhaul is on both, report the band the operator actually selected.
func TestActiveBackhaulBandPrefersTheSelection(t *testing.T) {
	original := easyMeshExecLocal
	t.Cleanup(func() { easyMeshExecLocal = original })
	easyMeshExecLocal = func(string) (string, error) {
		return `x  Mode:Managed  Access Point: F0:A8:82:FB:00:A4`, nil
	}
	if band, ok := easyMeshActiveBackhaulBand("2g"); !ok || band != "2g" {
		t.Fatalf("active band = %q ok=%v, want the selected 2g", band, ok)
	}
	if band, ok := easyMeshActiveBackhaulBand("5g"); !ok || band != "5g" {
		t.Fatalf("active band = %q ok=%v, want the selected 5g", band, ok)
	}
}

// No station associated anywhere means the Backhaul really is down.
func TestNoActiveBackhaulBandWhenNothingIsAssociated(t *testing.T) {
	original := easyMeshExecLocal
	t.Cleanup(func() { easyMeshExecLocal = original })
	easyMeshExecLocal = func(string) (string, error) {
		return `x  Mode:Managed  Access Point: Not-Associated`, nil
	}
	if band, ok := easyMeshActiveBackhaulBand("5g"); ok {
		t.Fatalf("reported band %q with nothing associated", band)
	}
}

// The legacy "Other Clients Info" heuristic picks up whatever Ethernet device
// sits behind a chassis -- in the AC-to-AC layout that is the Agent's own
// Antenna. Measured on the live pair: the Root reported the Antenna's client
// address as the Agent's management address. Reporting none is better than
// reporting somebody else's.
func TestAnotherDevicesLeaseIsNotReportedAsTheAgentsAddress(t *testing.T) {
	originalBelongs := easyMeshLeaseBelongsToChassis
	t.Cleanup(func() { easyMeshLeaseBelongsToChassis = originalBelongs })
	// Whoever holds the lease denies being this chassis.
	easyMeshLeaseBelongsToChassis = func(string, string) bool { return false }

	topology := `{"topology information":[
		{"AL MAC":"f0:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"de:84:03:11:57:80","Distance from controller":"1","BH Info":[{"Backhaul Medium Type":"5G","RSSI":"-72"}],"Other Clients Info":[{"Client Address":"CA:3C:84:CF:D8:24","Medium":"Ethernet"}]}
	]}`
	nodes := easyMeshRemoteNodesFromTopology(topology, map[string]string{
		"ca:3c:84:cf:d8:24": "10.10.18.205",
	})
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v", nodes)
	}
	if nodes[0].ManagementIP != "" {
		t.Fatalf("the Antenna's address was reported as the Agent's: %#v", nodes[0])
	}
	if nodes[0].ManagementOnline {
		t.Fatalf("an unattributed address was reported as reachable: %#v", nodes[0])
	}
}

// After a network reload the Agent's own "BH Info" stayed empty for minutes
// while the Mesh was demonstrably up -- two devices in the topology, the station
// associated, link metrics flowing -- and the Root reported no Mesh devices at
// all. The other end of the same link knows the band and the signal, so that is
// what fills the gap.
func TestAgentIsStillReportedWhenItsOwnBackhaulRecordIsEmpty(t *testing.T) {
	originalBelongs := easyMeshLeaseBelongsToChassis
	t.Cleanup(func() { easyMeshLeaseBelongsToChassis = originalBelongs })
	easyMeshLeaseBelongsToChassis = func(string, string) bool { return false }

	// The Agent reports its Backhaul station but no "BH Info"; the Root's BSS
	// reports that same station as a Backhaul peer on 5 GHz.
	topology := `{"topology information":[
		{"AL MAC":"f2:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[],"Radio Info":[
			{"band":"5G","BSSINFO":[{"SSID":"Chase_Test","connected sta info":[
				{"STA MAC address":"d6:84:03:01:57:80","BH STA":"Yes","Medium":"5G","uplink rssi":"-68"}
			]}]}
		]},
		{"AL MAC":"de:84:03:11:57:80","Distance from controller":"1","BH Info":[],
		 "BH Interface(APCLI)":[{"MAC address":"d6:84:03:01:57:80"}],
		 "Radio Info":[{"band":"5G","BSSINFO":[{"SSID":"Chase_Test"}]}]}
	]}`

	nodes := easyMeshRemoteNodesFromTopology(topology, nil)
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v, want the Agent that is plainly connected", nodes)
	}
	if nodes[0].BackhaulMedium != "5G" || nodes[0].BackhaulRSSI != "-68" {
		t.Fatalf("the link was not described from the Root's side: %#v", nodes[0])
	}
}

// A device with no Backhaul anywhere is still not a Mesh Agent.
func TestDeviceWithNoBackhaulAtAllIsNotReported(t *testing.T) {
	topology := `{"topology information":[
		{"AL MAC":"f2:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"de:84:03:11:57:80","Distance from controller":"1","BH Info":[]}
	]}`
	if nodes := easyMeshRemoteNodesFromTopology(topology, nil); len(nodes) != 0 {
		t.Fatalf("remote nodes = %#v, want none", nodes)
	}
}

// The drawing must describe the same hop the Mesh devices card does: a blank
// link across a Backhaul that is plainly carrying traffic reads as a fault.
func TestTopologyLinkIsDescribedFromTheUpstreamWhenTheAgentRecordIsEmpty(t *testing.T) {
	topology := `{"topology information":[
		{"AL MAC":"f2:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[],"Radio Info":[
			{"band":"5G","BSSINFO":[{"SSID":"Chase_Test","connected sta info":[
				{"STA MAC address":"d6:84:03:01:57:80","BH STA":"Yes","Medium":"5G","uplink rssi":"-50"}
			]}]}
		]},
		{"AL MAC":"de:84:03:11:57:80","Distance from controller":"1","Upstream 1905 device":"f2:a8:82:fb:00:a4",
		 "BH Info":[],"BH Interface(APCLI)":[{"MAC address":"d6:84:03:01:57:80"}]}
	]}`
	graph := easyMeshTopologyGraphFrom(topology, "", nil)
	if len(graph.Links) != 1 {
		t.Fatalf("links = %#v, want the single hop", graph.Links)
	}
	link := graph.Links[0]
	if link.Medium != "5 GHz" || link.RSSI != "-50" || !link.Wireless {
		t.Fatalf("the hop was drawn blank: %#v", link)
	}
}

// An Agent has no DHCP server: its clients are leased by the Root, so the
// Agent's own collector sees a MAC and nothing else. Measured on the pair -- a
// phone on the Agent's Antenna drew as a bare MAC while the Root's lease table
// held "10.10.18.244 iPhone" for that very address.
func TestClientsAreNamedFromTheLeasesOfWhicheverDeviceDrawsTheGraph(t *testing.T) {
	original := easyMeshClientLeases
	t.Cleanup(func() { easyMeshClientLeases = original })
	easyMeshClientLeases = func() map[string]ccDhcpLease {
		return map[string]ccDhcpLease{
			"1a:0f:39:f9:11:d4": {IP: "10.10.18.244", Hostname: "iPhone"},
		}
	}

	peer := map[string]easyMeshFabric{
		"de:84:03:11:57:80": {
			Antennas: []EasyMeshTopologyAntenna{{ID: "antenna-2", Name: "Antenna2", Online: true}},
			// Exactly what an Agent can report on its own.
			Clients: []EasyMeshTopologyClient{
				{MAC: "1a:0f:39:f9:11:d4", Hostname: "(unknown)", SSID: "Chase_Test_2", ParentID: "antenna-2"},
			},
		},
	}
	graph := easyMeshTopologyGraphWithPeerFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, easyMeshFabric{}, peer)

	if len(graph.Clients) != 1 {
		t.Fatalf("clients = %#v", graph.Clients)
	}
	if graph.Clients[0].IP != "10.10.18.244" {
		t.Fatalf("the Root did not supply the address it had leased: %#v", graph.Clients[0])
	}
	if graph.Clients[0].Hostname != "iPhone" {
		t.Fatalf("the Root did not supply the name it had leased: %#v", graph.Clients[0])
	}
}

// A client the collector already identified must not be relabelled by a stale
// lease for the same address.
func TestALocallyKnownClientKeepsItsOwnIdentity(t *testing.T) {
	original := easyMeshClientLeases
	t.Cleanup(func() { easyMeshClientLeases = original })
	easyMeshClientLeases = func() map[string]ccDhcpLease {
		return map[string]ccDhcpLease{
			"f4:3b:d8:7f:cc:40": {IP: "10.10.18.9", Hostname: "stale-name"},
		}
	}
	fabric := easyMeshFabric{
		Clients: []EasyMeshTopologyClient{
			{MAC: "f4:3b:d8:7f:cc:40", IP: "10.10.18.100", Hostname: "DESKTOP-KC17O22",
				ParentID: easyMeshFabricChassisParent},
		},
	}
	graph := easyMeshTopologyGraphWithFabric(realTopologyDump(t), "f0:a8:82:fb:00:a4", nil, fabric)
	if graph.Clients[0].IP != "10.10.18.100" || graph.Clients[0].Hostname != "DESKTOP-KC17O22" {
		t.Fatalf("a known client was overwritten by a lease: %#v", graph.Clients[0])
	}
}
