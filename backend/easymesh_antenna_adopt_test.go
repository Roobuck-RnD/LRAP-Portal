package main

import (
	"strings"
	"testing"
)

// A port with carrier and no module behind it is an Antenna the Mesh cannot see.
func TestOrphanedAntennaPortsAreTheLinkedPortsNothingAnsweredOn(t *testing.T) {
	linked := []int{1, 2, 3}
	discovered := []ManagedModule{
		{ModuleID: "antenna:1", PortIndex: 1},
		{ModuleID: "antenna:3", PortIndex: 3},
	}
	orphans := easyMeshOrphanedAntennaPorts(linked, discovered)
	if len(orphans) != 1 || orphans[0] != 2 {
		t.Fatalf("orphans = %v, want only the port whose Antenna never answered", orphans)
	}
}

func TestNoOrphansWhenEveryLinkedPortAnswered(t *testing.T) {
	linked := []int{2, 4}
	discovered := []ManagedModule{{PortIndex: 2}, {PortIndex: 4}}
	if orphans := easyMeshOrphanedAntennaPorts(linked, discovered); len(orphans) != 0 {
		t.Fatalf("orphans = %v on a healthy chassis", orphans)
	}
}

// Preparation used to loop over an empty Antenna list and report success, which
// is how a module ended up stranded off the Mesh with nothing said about it.
func TestPreparationRefusesWhenALinkedAntennaNeverAnswered(t *testing.T) {
	err := easyMeshUndiscoveredAntennaError([]int{2}, nil)
	if err == nil {
		t.Fatal("preparation accepted a linked Antenna that never answered")
	}
	if !strings.Contains(err.Error(), "lan2") {
		t.Fatalf("the error does not say which port to look at: %v", err)
	}
}

// A chassis with no Antennas at all is a valid Mesh node and must still prepare.
func TestPreparationAcceptsAChassisWithNoAntennas(t *testing.T) {
	if err := easyMeshUndiscoveredAntennaError(nil, nil); err != nil {
		t.Fatalf("a chassis with no Antennas was refused: %v", err)
	}
}

// Adoption only makes sense once the management network exists; on a standalone
// unit the raw ports carry ordinary client traffic and must not be touched.
func TestAdoptionDoesNothingWithoutTheManagementNetwork(t *testing.T) {
	originalIsolated := antennaManagementIsIsolated
	originalConfigured := easyMeshManagementNetworkConfigured
	originalExec := easyMeshExecLocal
	t.Cleanup(func() {
		antennaManagementIsIsolated = originalIsolated
		easyMeshManagementNetworkConfigured = originalConfigured
		easyMeshExecLocal = originalExec
	})

	touched := 0
	easyMeshExecLocal = func(string) (string, error) {
		touched++
		return "", nil
	}

	antennaManagementIsIsolated = func() bool { return false }
	easyMeshManagementNetworkConfigured = func() bool { return true }
	if adopted := adoptUntaggedAntennas(); adopted != 0 || touched != 0 {
		t.Fatalf("adoption ran on a standalone unit (adopted=%d touched=%d)", adopted, touched)
	}

	antennaManagementIsIsolated = func() bool { return true }
	easyMeshManagementNetworkConfigured = func() bool { return false }
	if adopted := adoptUntaggedAntennas(); adopted != 0 || touched != 0 {
		t.Fatalf("adoption ran with no management network (adopted=%d touched=%d)", adopted, touched)
	}
}
