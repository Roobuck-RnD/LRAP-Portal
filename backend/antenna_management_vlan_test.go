package main

import (
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
func TestManagementVLANIsOnlyCreatedWhenMissing(t *testing.T) {
	originalConfigured := easyMeshManagementNetworkConfigured
	originalExec := easyMeshExecLocal
	t.Cleanup(func() {
		easyMeshManagementNetworkConfigured = originalConfigured
		easyMeshExecLocal = originalExec
	})

	ran := 0
	easyMeshExecLocal = func(string) (string, error) {
		ran++
		return "", nil
	}

	easyMeshManagementNetworkConfigured = func() bool { return true }
	ensureAntennaManagementVLAN()
	if ran != 0 {
		t.Fatalf("the network was reconfigured although the VLAN already existed (%d commands)", ran)
	}

	easyMeshManagementNetworkConfigured = func() bool { return false }
	ensureAntennaManagementVLAN()
	if ran != 1 {
		t.Fatalf("the VLAN was not created when missing (%d commands)", ran)
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
