package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// Antenna management is decided by the network configuration, not by a marker
// recording that a Mesh migration once happened. A chassis that has the VLAN
// manages its Antennas over it whether or not it has ever been in a Mesh.
func TestIsolationFollowsTheConfiguredNetwork(t *testing.T) {
	originalConfigured := easyMeshManagementNetworkConfigured
	originalMarker := AntennaManagementMarker
	t.Cleanup(func() {
		easyMeshManagementNetworkConfigured = originalConfigured
		AntennaManagementMarker = originalMarker
	})
	// No marker anywhere, so only the configuration can decide.
	AntennaManagementMarker = t.TempDir() + "/absent"

	easyMeshManagementNetworkConfigured = func() bool { return true }
	if !antennaManagementIsIsolated() {
		t.Fatal("a chassis with the management VLAN configured was not treated as isolated")
	}
	if got := APManagementIPs(); len(got) == 0 || !strings.HasPrefix(got[0], "172.31.255.") {
		t.Fatalf("Antenna pool = %v, want the management VLAN addresses", got)
	}

	easyMeshManagementNetworkConfigured = func() bool { return false }
	if antennaManagementIsIsolated() {
		t.Fatal("a chassis with no management VLAN was treated as isolated")
	}
}

// A unit whose configuration read fails must not silently change planes, so the
// marker still counts.
func TestTheMarkerStillCountsWhenTheConfigurationCannotBeRead(t *testing.T) {
	originalConfigured := easyMeshManagementNetworkConfigured
	t.Cleanup(func() { easyMeshManagementNetworkConfigured = originalConfigured })
	easyMeshManagementNetworkConfigured = func() bool { return false }

	// The real marker path exists on a migrated unit; simulate that.
	originalMarker := AntennaManagementMarker
	t.Cleanup(func() { AntennaManagementMarker = originalMarker })
	dir := t.TempDir()
	AntennaManagementMarker = dir + "/easymesh_network_v1"
	if err := writeMarkerForTest(AntennaManagementMarker); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	if !antennaManagementIsIsolated() {
		t.Fatal("a migrated unit lost its isolation when the configuration read failed")
	}
}

// Creating the VLAN is skipped when it is already there, so an ordinary boot
// does not reload the network for nothing.
// The VLAN is built when it is missing and left alone when it is already there
// AND up. "Configured" on its own is not enough: this test used to assert that a
// configured plane was never touched, which is exactly the assumption that let a
// chassis whose br-mgmt had gone stay broken across every restart.
func TestManagementVLANIsOnlyCreatedWhenMissing(t *testing.T) {
	originalConfigured := easyMeshManagementNetworkConfigured
	originalExec := easyMeshExecLocal
	t.Cleanup(func() {
		easyMeshManagementNetworkConfigured = originalConfigured
		easyMeshExecLocal = originalExec
	})

	// Count only commands that change something; reading the plane's state is
	// expected on every call.
	changed := 0
	planeUp := true
	easyMeshExecLocal = func(command string) (string, error) {
		if strings.HasPrefix(command, "ip -4 addr show dev "+AntennaManagementBridge) {
			if planeUp {
				return "inet " + AntennaManagementACIP + "/29 scope global " + AntennaManagementBridge, nil
			}
			return "", errors.New("Device does not exist")
		}
		changed++
		return "", nil
	}

	easyMeshManagementNetworkConfigured = func() bool { return true }
	ensureAntennaManagementVLAN()
	if changed != 0 {
		t.Fatalf("a configured and running VLAN was reconfigured (%d commands)", changed)
	}

	planeUp = false
	easyMeshManagementNetworkConfigured = func() bool { return false }
	ensureAntennaManagementVLAN()
	if changed != 1 {
		t.Fatalf("the VLAN was not created when missing (%d commands)", changed)
	}
}

func writeMarkerForTest(path string) error {
	return os.WriteFile(path, []byte("1"), 0o644)
}

// Readiness to run a Mesh is now simply whether the permanent management plane
// is actually up. There is no client VLAN step left to switch on.
func TestMeshReadinessIsTheManagementPlaneBeingUp(t *testing.T) {
	originalIsolated := antennaManagementIsIsolated
	originalExec := easyMeshExecLocal
	t.Cleanup(func() {
		antennaManagementIsIsolated = originalIsolated
		easyMeshExecLocal = originalExec
	})

	antennaManagementIsIsolated = func() bool { return true }
	easyMeshExecLocal = func(string) (string, error) {
		return "inet 172.31.255.1/29 brd 172.31.255.7 scope global br-mgmt", nil
	}
	if !easyMeshNetworkReadyForMesh() {
		t.Fatal("a chassis with the management plane up was not ready for a Mesh")
	}

	// The address is missing, so the plane is configured but not actually up.
	easyMeshExecLocal = func(string) (string, error) { return "", nil }
	if easyMeshNetworkReadyForMesh() {
		t.Fatal("a chassis whose management bridge has no address was reported ready")
	}
}

// Client traffic is never tagged, in a Mesh or out of one. Tagging it was a
// second arrangement that had to be switched on and off, and while the two ends
// disagreed every device behind the Antenna lost the network.
func TestAntennaClientTrafficIsNeverTagged(t *testing.T) {
	originalRemote := easyMeshExecRemote
	t.Cleanup(func() { easyMeshExecRemote = originalRemote })

	sent := []string{}
	easyMeshExecRemote = func(_ string, command string) (string, error) {
		sent = append(sent, command)
		return "", nil
	}

	if !applyAntennaManagementVLAN("172.31.255.5") {
		t.Fatal("applying the management VLAN failed")
	}
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "vid=200") {
		t.Fatalf("the management VLAN was not applied: %v", sent)
	}
	if strings.Contains(joined, "vid=100") {
		t.Fatalf("client traffic was tagged: %v", sent)
	}
	if !strings.Contains(joined, "ports=eth1;") {
		t.Fatalf("client traffic was not set untagged: %v", sent)
	}
}

// An Antenna whose client side disagrees with the chassis is still perfectly
// reachable for management, so adoption -- which only looks for Antennas it
// cannot reach at all -- never sees it. Every client behind it is meanwhile cut
// off, which is the state that had to be repaired by hand.
func TestAMismatchedClientPlaneIsRealignedEvenWhenTheAntennaIsReachable(t *testing.T) {
	originalLocal := easyMeshExecLocal
	originalRemote := easyMeshExecRemote
	originalIsolated := antennaManagementIsIsolated
	originalDiscover := discoverManagedAPsForAlignment
	t.Cleanup(func() {
		easyMeshExecLocal = originalLocal
		easyMeshExecRemote = originalRemote
		antennaManagementIsIsolated = originalIsolated
		discoverManagedAPsForAlignment = originalDiscover
	})

	antennaManagementIsIsolated = func() bool { return true }
	discoverManagedAPsForAlignment = func() []ManagedModule {
		return []ManagedModule{{ModuleID: "antenna:3", IP: "172.31.255.5", PortIndex: 3}}
	}

	sent := []string{}
	easyMeshExecRemote = func(_ string, command string) (string, error) {
		if command == antennaClientPlaneStateCommand {
			return "none|||", nil
		}
		if strings.Contains(command, "uci -q get network.@device[0].ports") {
			// The Antenna is tagged while the chassis is not.
			return "eth1.100 ra0 rax0", nil
		}
		sent = append(sent, command)
		return "", nil
	}

	alignAntennaClientPlanes()
	if len(sent) != 1 {
		t.Fatalf("the mismatch was not corrected: %v", sent)
	}
	if strings.Contains(sent[0], "vid=100") {
		t.Fatalf("the Antenna was left tagged against an untagged chassis: %v", sent)
	}
	if !strings.Contains(sent[0], "ports=eth1;") {
		t.Fatalf("client traffic was not returned to untagged: %v", sent)
	}
}

// An Antenna that already agrees with the chassis must be left alone -- a
// needless network reload on every boot would drop its clients for no reason.
func TestAMatchingClientPlaneIsLeftAlone(t *testing.T) {
	originalLocal := easyMeshExecLocal
	originalRemote := easyMeshExecRemote
	originalIsolated := antennaManagementIsIsolated
	originalDiscover := discoverManagedAPsForAlignment
	t.Cleanup(func() {
		easyMeshExecLocal = originalLocal
		easyMeshExecRemote = originalRemote
		antennaManagementIsIsolated = originalIsolated
		discoverManagedAPsForAlignment = originalDiscover
	})

	antennaManagementIsIsolated = func() bool { return true }
	discoverManagedAPsForAlignment = func() []ManagedModule {
		return []ManagedModule{{ModuleID: "antenna:3", IP: "172.31.255.5", PortIndex: 3}}
	}

	sent := []string{}
	easyMeshExecRemote = func(_ string, command string) (string, error) {
		if command == antennaClientPlaneStateCommand {
			return "none|||", nil
		}
		if strings.Contains(command, "uci -q get network.@device[0].ports") {
			return "eth1 ra0 rax0", nil
		}
		sent = append(sent, command)
		return "", nil
	}

	alignAntennaClientPlanes()
	if len(sent) != 0 {
		t.Fatalf("an Antenna that already agreed was reconfigured: %v", sent)
	}
}

// Configured is not the same as up. A chassis whose br-mgmt has gone must
// rebuild it, not return satisfied because the configuration is still on disk.
//
// Measured on a live chassis: br-mgmt absent from the kernel, the configuration
// complete, and netifd still reporting the interface "up" on a device that did
// not exist. This returned immediately, and the only other repair sits behind
// resumeEasyMeshAtStartup's !NetworkPrepared guard — which is computed from
// whether this very plane is up. The repair was gated on the fault it repairs,
// so the chassis stayed broken across restarts.
func TestAConfiguredButAbsentManagementPlaneIsRebuilt(t *testing.T) {
	originalConfigured := easyMeshManagementNetworkConfigured
	originalExec := easyMeshExecLocal
	t.Cleanup(func() {
		easyMeshManagementNetworkConfigured = originalConfigured
		easyMeshExecLocal = originalExec
	})

	easyMeshManagementNetworkConfigured = func() bool { return true }

	// The plane is down on the first look and up once the interface is bounced.
	up := false
	var ran []string
	easyMeshExecLocal = func(command string) (string, error) {
		ran = append(ran, command)
		if strings.Contains(command, "ifup") {
			up = true
		}
		if strings.HasPrefix(command, "ip -4 addr show dev "+AntennaManagementBridge) {
			if up {
				return "inet " + AntennaManagementACIP + "/29 scope global " + AntennaManagementBridge, nil
			}
			return "", errors.New("Device does not exist")
		}
		return "", nil
	}

	ensureAntennaManagementVLAN()

	joined := strings.Join(ran, " ;; ")
	if !strings.Contains(joined, "ifdown "+AntennaManagementInterface) ||
		!strings.Contains(joined, "ifup "+AntennaManagementInterface) {
		t.Fatalf("the plane was not rebuilt: %v", ran)
	}
	// Rebuilding must not reload the whole network: that rebuilds br-lan too and
	// drops this chassis's radios, and an Agent's Backhaul with them.
	if strings.Contains(joined, "ubus call network reload") {
		t.Fatalf("the rebuild reloaded the whole network: %v", ran)
	}
	// It also must not re-run the first-time setup, which rewrites the config.
	if strings.Contains(joined, "uci set network.mesh_mgmt_bridge") {
		t.Fatalf("an existing configuration was rewritten instead of rebuilt: %v", ran)
	}
}

// A plane that is both configured and up is left alone — this runs on every
// start and must cost nothing when there is nothing wrong.
func TestAHealthyManagementPlaneIsNotTouched(t *testing.T) {
	originalConfigured := easyMeshManagementNetworkConfigured
	originalExec := easyMeshExecLocal
	t.Cleanup(func() {
		easyMeshManagementNetworkConfigured = originalConfigured
		easyMeshExecLocal = originalExec
	})

	easyMeshManagementNetworkConfigured = func() bool { return true }
	var ran []string
	easyMeshExecLocal = func(command string) (string, error) {
		ran = append(ran, command)
		if strings.HasPrefix(command, "ip -4 addr show dev "+AntennaManagementBridge) {
			return "inet " + AntennaManagementACIP + "/29 scope global " + AntennaManagementBridge, nil
		}
		return "", nil
	}

	ensureAntennaManagementVLAN()

	for _, command := range ran {
		if strings.Contains(command, "ifdown") || strings.Contains(command, "uci set") {
			t.Fatalf("a healthy plane was disturbed by %q", command)
		}
	}
}

// ---------- the adoption scan only acts on a port that just came up ----------

// The cold-boot case. At process start the switch PHYs are still coming up, so
// there is no usable baseline yet and nothing may be reported as new -- a
// chassis that had two Antennas connected all along announced both as freshly
// plugged in on every restart.
func TestNothingIsJustPluggedInWithoutAPreviousLook(t *testing.T) {
	current := map[int]bool{1: false, 2: true, 3: true, 4: false}
	if got := antennaPortsJustPluggedIn(nil, current); len(got) != 0 {
		t.Fatalf("the first look reported %v as newly connected", got)
	}
}

// A port that has been up all along must not be touched: adoption decides what
// to lend out from carrier alone, so acting on it would take the network away
// from whatever is plugged in there, once every interval.
func TestOnlyPortsThatChangedCountAsPluggedIn(t *testing.T) {
	previous := map[int]bool{1: false, 2: true, 3: true, 4: true}
	current := map[int]bool{1: true, 2: true, 3: false, 4: true}
	got := antennaPortsJustPluggedIn(previous, current)
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("expected only lan1, got %v", got)
	}
}

// Reported in port order, so the log reads the same way twice.
func TestPluggedInPortsAreReportedInOrder(t *testing.T) {
	previous := map[int]bool{1: false, 2: false, 3: false, 4: false}
	current := map[int]bool{1: true, 2: false, 3: true, 4: true}
	got := antennaPortsJustPluggedIn(previous, current)
	want := []int{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for index, port := range want {
		if got[index] != port {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

// ---------- a booting module gets a few passes, a bare socket does not ----------

// An Antenna that was just plugged in is booting on power the cable carries, and
// one adoption attempt does not span that. Measured on the Root: adoption gave
// up after 45 seconds and the module took its address 33 seconds later.
func TestAPortIsRetriedWhileItStillHasBudget(t *testing.T) {
	budget := map[int]int{2: antennaAdoptionRetries}
	carrier := map[int]bool{2: true}
	orphans := []int{2}

	for pass := 1; pass <= antennaAdoptionRetries; pass++ {
		got := antennaAdoptionPortsToTry(budget, carrier, orphans)
		if len(got) != 1 || got[0] != 2 {
			t.Fatalf("pass %d: expected lan2 to be retried, got %v", pass, got)
		}
	}
	if got := antennaAdoptionPortsToTry(budget, carrier, orphans); len(got) != 0 {
		t.Fatalf("the budget did not run out: %v", got)
	}
}

// The bound is the whole point. Adoption works out what to lend out from carrier
// alone, so a port that keeps failing must stop being retried or a socket with a
// non-Antenna device on it loses its network once a minute, for ever.
func TestAPortThatKeepsFailingIsEventuallyLeftAlone(t *testing.T) {
	budget := map[int]int{3: 1}
	carrier := map[int]bool{3: true}
	orphans := []int{3}

	antennaAdoptionPortsToTry(budget, carrier, orphans)
	if budget[3] != 0 {
		t.Fatalf("expected the last attempt to be spent, got %d left", budget[3])
	}
	if got := antennaAdoptionPortsToTry(budget, carrier, orphans); len(got) != 0 {
		t.Fatalf("a port with no budget was tried again: %v", got)
	}
}

// Once the module answers, the port stops being an orphan and the rest of the
// budget is dropped -- whether adoption is what got it there or it simply
// finished booting and took an address on its own.
func TestAPortIsForgottenOnceItStopsBeingAnOrphan(t *testing.T) {
	budget := map[int]int{2: antennaAdoptionRetries}
	carrier := map[int]bool{2: true}

	if got := antennaAdoptionPortsToTry(budget, carrier, nil); len(got) != 0 {
		t.Fatalf("a port that is no longer an orphan was tried: %v", got)
	}
	if _, held := budget[2]; held {
		t.Fatal("the budget for a port that answered was kept")
	}
}

// Unplugging drops the port entirely, so a later replug starts from a full
// budget rather than the remains of the last attempt.
func TestAPortIsForgottenWhenItLosesCarrier(t *testing.T) {
	budget := map[int]int{4: antennaAdoptionRetries}
	carrier := map[int]bool{4: false}

	if got := antennaAdoptionPortsToTry(budget, carrier, []int{4}); len(got) != 0 {
		t.Fatalf("a port with no carrier was tried: %v", got)
	}
	if _, held := budget[4]; held {
		t.Fatal("the budget for an unplugged port was kept")
	}
}

// A port nothing armed is never touched, however orphaned it looks.
func TestAnUnarmedPortIsNeverTried(t *testing.T) {
	budget := map[int]int{}
	carrier := map[int]bool{1: true, 2: true}
	if got := antennaAdoptionPortsToTry(budget, carrier, []int{1, 2}); len(got) != 0 {
		t.Fatalf("expected nothing to be tried, got %v", got)
	}
}

// Reported in port order, so the log and the work read the same way twice.
func TestPortsToTryAreReportedInOrder(t *testing.T) {
	budget := map[int]int{1: 2, 3: 2, 4: 2}
	carrier := map[int]bool{1: true, 3: true, 4: true}
	got := antennaAdoptionPortsToTry(budget, carrier, []int{4, 1, 3})
	want := []int{1, 3, 4}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for index, port := range want {
		if got[index] != port {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}

// ---------- the client plane takes no address; management is keyed by radio ----------

const testRadioMAC = "f0:a8:82:fb:00:ec"

func healthyClientPlane() string { return "none||01:" + testRadioMAC + "|" + testRadioMAC }

// fakeAntennaClientPlane stands in for Antennas whose client-plane state is
// looked up by address, and records every repair sent. When takes is false the
// repair never lands, which is how the lost second adoption command looked.
func fakeAntennaClientPlane(t *testing.T, states map[string]string, takes bool, repairErr error) *int {
	t.Helper()
	repairs := 0
	originalRemote := easyMeshExecRemote
	originalDelay := antennaClientPlaneRetryDelay
	originalAddress := antennaAddressOnPort
	t.Cleanup(func() {
		easyMeshExecRemote = originalRemote
		antennaClientPlaneRetryDelay = originalDelay
		antennaAddressOnPort = originalAddress
	})
	antennaClientPlaneRetryDelay = 0
	antennaAddressOnPort = func(_ int, fallback string) string { return fallback }
	easyMeshExecRemote = func(ip string, command string) (string, error) {
		switch {
		case command == antennaClientPlaneStateCommand:
			return states[ip], nil
		case command == easyMeshAntennaClientPlaneRepairCommand():
			repairs++
			if takes {
				states[ip] = healthyClientPlane()
			}
			return "", repairErr
		}
		return "", nil
	}
	return &repairs
}

// The state measured on the Root after TestVersion1.7: management working, and
// the client plane still a DHCP client on the same MAC -- which is what let
// dnsmasq forget one Antenna's management lease and give it to another.
func TestAnAntennaThatTakesAClientAddressIsCorrected(t *testing.T) {
	states := map[string]string{"172.31.255.3": "dhcp||01:" + testRadioMAC + "|" + testRadioMAC}
	repairs := fakeAntennaClientPlane(t, states, true, nil)
	if !alignAntennaClientPlane(ManagedModule{IP: "172.31.255.3", PortIndex: 3}) {
		t.Fatal("an Antenna taking a client address was left alone")
	}
	if *repairs != 1 {
		t.Fatalf("expected one repair, sent %d", *repairs)
	}
}

// A management lease keyed on eth1's regenerated MAC is abandoned at every
// restart and holds a pool address for five minutes; it has to be keyed by
// radio instead.
func TestAnAntennaWithoutARadioClientIDIsCorrected(t *testing.T) {
	states := map[string]string{"172.31.255.4": "none|||" + testRadioMAC}
	repairs := fakeAntennaClientPlane(t, states, true, nil)
	if !alignAntennaClientPlane(ManagedModule{IP: "172.31.255.4", PortIndex: 2}) {
		t.Fatal("an Antenna without a management client-id was left alone")
	}
	if *repairs != 1 {
		t.Fatalf("expected one repair, sent %d", *repairs)
	}
}

// And an identity still announced on the client plane is corrected with it.
func TestAnAntennaAnnouncingItsIdentityOnTheClientPlaneIsCorrected(t *testing.T) {
	states := map[string]string{"172.31.255.5": "none|RoobuckAP|01:" + testRadioMAC + "|" + testRadioMAC}
	repairs := fakeAntennaClientPlane(t, states, true, nil)
	if !alignAntennaClientPlane(ManagedModule{IP: "172.31.255.5", PortIndex: 1}) || *repairs != 1 {
		t.Fatalf("the identity was left on the client plane (%d repairs)", *repairs)
	}
}

// An Antenna that is already right is not touched: the repair ends in a network
// reload, and doing that on every pass would disturb it for nothing.
func TestAnAntennaAlreadyRightIsNotTouched(t *testing.T) {
	states := map[string]string{"172.31.255.2": healthyClientPlane()}
	repairs := fakeAntennaClientPlane(t, states, true, nil)
	if alignAntennaClientPlane(ManagedModule{IP: "172.31.255.2", PortIndex: 4}) {
		t.Fatal("an Antenna with nothing to correct was reported as corrected")
	}
	if *repairs != 0 {
		t.Fatalf("an Antenna that was already right was reconfigured %d time(s)", *repairs)
	}
}

// The client-id comes from the radio, in the form dnsmasq keys on; with no radio
// to read there is nothing to derive it from, so none is demanded.
func TestTheManagementClientIDComesFromTheRadio(t *testing.T) {
	state := parseAntennaClientPlaneState("none||x|F0:A8:82:FB:00:EC")
	if got := state.wantManagementClientID(); got != "01:f0:a8:82:fb:00:ec" {
		t.Fatalf("expected the client-id to be 01:<radio MAC>, got %q", got)
	}
	if drift := parseAntennaClientPlaneState("none|||").drift(); len(drift) != 0 {
		t.Fatalf("an Antenna whose radio could not be read was held to a client-id: %v", drift)
	}
}

// The reply to the repair is routinely lost to its own reload. That is not
// failure -- the read-back decides.
func TestALostRepairReplyIsCheckedRatherThanTrusted(t *testing.T) {
	states := map[string]string{"172.31.255.3": "dhcp|||" + testRadioMAC}
	fakeAntennaClientPlane(t, states, true, errors.New("unexpected EOF"))
	if !alignAntennaClientPlane(ManagedModule{IP: "172.31.255.3", PortIndex: 3}) {
		t.Fatal("a repair that took was reported as failed because its reply was lost")
	}
}

// And the other way round: a reply is no proof. A repair that never lands is
// retried a bounded number of times and then left to the next pass.
func TestARepairThatNeverTakesIsRetriedThenLeftForTheNextPass(t *testing.T) {
	states := map[string]string{"172.31.255.4": "dhcp|||" + testRadioMAC}
	repairs := fakeAntennaClientPlane(t, states, false, nil)
	if alignAntennaClientPlane(ManagedModule{IP: "172.31.255.4", PortIndex: 2}) {
		t.Fatal("a repair that never took was reported as done")
	}
	if *repairs != antennaClientPlaneAttempts {
		t.Fatalf("expected %d attempts, made %d", antennaClientPlaneAttempts, *repairs)
	}
}

// A new client-id is a new DHCP client to dnsmasq, so the repair can move the
// Antenna to another management address. The read-back follows the port.
func TestTheRepairIsReadBackWhereTheAntennaIsNow(t *testing.T) {
	states := map[string]string{"172.31.255.3": "dhcp|||" + testRadioMAC, "172.31.255.5": healthyClientPlane()}
	fakeAntennaClientPlane(t, states, false, nil)
	antennaAddressOnPort = func(port int, fallback string) string {
		if port == 3 {
			return "172.31.255.5"
		}
		return fallback
	}
	if !alignAntennaClientPlane(ManagedModule{IP: "172.31.255.3", PortIndex: 3}) {
		t.Fatal("the read-back looked at the old address instead of following the port")
	}
}

// The repair changes the client plane and the management lease identity, and
// nothing else: not the ports, and not how the management interface is built.
func TestTheClientPlaneRepairTouchesOnlyWhatItMust(t *testing.T) {
	command := easyMeshAntennaClientPlaneRepairCommand()
	for _, required := range []string{
		"uci set network.lan.proto=none",
		"vendorid",
		"/sys/class/net/ra0/address",
		"network.antenna_mgmt.clientid",
		"uci commit network",
		"ubus call network reload",
	} {
		if !strings.Contains(command, required) {
			t.Fatalf("the repair is missing %q: %s", required, command)
		}
	}
	for _, forbidden := range []string{"ports", "mesh_mgmt_eth1", "antenna_mgmt.device", "antenna_mgmt.proto", "antenna_mgmt.vendorid"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("the repair reaches beyond its scope (%s): %s", forbidden, command)
		}
	}
}

// Adoption itself leaves an Antenna in the same state, so there is nothing for
// the next pass to correct.
func TestAdoptionLeavesNoClientAddressAndARadioClientID(t *testing.T) {
	client := easyMeshAntennaUntaggedClientCommand()
	if !strings.Contains(client, "network.lan.proto=none") || strings.Contains(client, "proto=dhcp") {
		t.Fatalf("adoption still gives the client plane an address: %s", client)
	}
	management := easyMeshPrepareAntennaManagementCommand()
	if !strings.Contains(management, "network.antenna_mgmt.clientid") || !strings.Contains(management, "/sys/class/net/ra0/address") {
		t.Fatalf("adoption does not key the management lease by radio: %s", management)
	}
}

// Alignment is what catches it on a running chassis: the Antenna is reachable,
// so adoption never looks at it.
func TestAlignmentCorrectsAnAntennaThatTakesAClientAddress(t *testing.T) {
	originalIsolated := antennaManagementIsIsolated
	originalDiscover := discoverManagedAPsForAlignment
	t.Cleanup(func() {
		antennaManagementIsIsolated = originalIsolated
		discoverManagedAPsForAlignment = originalDiscover
	})
	antennaManagementIsIsolated = func() bool { return true }
	discoverManagedAPsForAlignment = func() []ManagedModule {
		return []ManagedModule{{ModuleID: "antenna:3", IP: "172.31.255.3", PortIndex: 3}}
	}
	states := map[string]string{"172.31.255.3": "dhcp|||" + testRadioMAC}
	repairs := fakeAntennaClientPlane(t, states, true, nil)

	alignAntennaClientPlanes()
	if *repairs != 1 {
		t.Fatalf("alignment left the client plane taking an address (%d repairs)", *repairs)
	}
}

// The pass must run on every tick, not only when something was plugged in: an
// Antenna that is reachable never looks like an orphan.
func TestTheScanAlignsOnEveryPass(t *testing.T) {
	source, err := os.ReadFile("antenna_management_vlan.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	start := strings.Index(body, "func startAntennaAdoptionScan() {")
	if start < 0 {
		t.Fatal("the scan is missing")
	}
	scan := body[start:]
	align := strings.Index(scan, "alignAntennaClientPlanes()")
	if align < 0 {
		t.Fatal("the scan never aligns the Antennas")
	}
	// Nothing between the top of the loop and the alignment may skip the rest
	// of the pass.
	if strings.Contains(scan[:align], "continue") {
		t.Fatal("a pass can skip alignment")
	}
}
