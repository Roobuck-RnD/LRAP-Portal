package main

import (
	"errors"
	"strings"
	"testing"
	"time"
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

// ---------- a second Antenna on the same chassis ----------

// The bug this exists for: an Antenna adopted earlier holds a pool address and
// answers every ping, so a wait that returns "the first address that answers"
// hands back the module that was already configured and the new one is never
// reached.
func TestAdoptionWaitsForAnAddressThatWasNotAlreadyTaken(t *testing.T) {
	pool := APManagementIPs()
	if len(pool) < 2 {
		t.Skip("the pool is too small to have an old and a new Antenna in it")
	}
	alreadyTaken, arrivingNow := pool[0], pool[1]

	realExec := easyMeshExecLocal
	t.Cleanup(func() { easyMeshExecLocal = realExec })
	// Both answer: the one adopted long ago still holds its address and replies
	// to every ping, which is exactly what used to mislead this.
	easyMeshExecLocal = func(command string) (string, error) {
		if strings.Contains(command, alreadyTaken) || strings.Contains(command, arrivingNow) {
			return "", nil
		}
		return "", errors.New("unreachable")
	}

	got, err := waitForAdoptedAntennaAddress(map[string]bool{alreadyTaken: true}, 5*time.Second)
	if err != nil {
		t.Fatalf("adoption gave up while %s was answering: %v", arrivingNow, err)
	}
	if got != arrivingNow {
		t.Fatalf("adoption picked %s, which was already taken before this port was bridged", got)
	}
}

// With every pool address already accounted for, there is nothing new to adopt
// and the wait has to say so rather than hand back somebody else.
func TestAdoptionGivesUpWhenNothingNewAppears(t *testing.T) {
	realExec := easyMeshExecLocal
	t.Cleanup(func() { easyMeshExecLocal = realExec })
	easyMeshExecLocal = func(command string) (string, error) { return "", nil }

	baseline := map[string]bool{}
	for _, ip := range APManagementIPs() {
		baseline[ip] = true
	}
	if got, err := waitForAdoptedAntennaAddress(baseline, time.Second); err == nil {
		t.Fatalf("expected no new Antenna, got %s", got)
	}
}

// ---------- the management VLAN covers only the Antenna ports ----------

func TestManagementVLANCoversOnlyTheAntennaPorts(t *testing.T) {
	names := antennaManagementPortNames()
	if len(names) != len(APManagementIPs()) {
		t.Fatalf("the port set and the address pool disagree: %v vs %v", names, APManagementIPs())
	}
	for _, unwanted := range []string{"lan5", "lan0"} {
		for _, name := range names {
			if name == unwanted {
				t.Fatalf("%s carries ordinary equipment and must not get a management leg", unwanted)
			}
		}
	}
}

// Both shells must filter to the same set, or a chassis gets a leg built on a
// port the migration then takes straight back off.
func TestManagementVLANShellsAgreeOnThePortSet(t *testing.T) {
	want := strings.Join(antennaManagementPortNames(), "|")
	for name, command := range map[string]string{
		"port scan": easyMeshLANPortsShellVar(),
		"migration": easyMeshNarrowManagementVLANCommand(),
	} {
		if !strings.Contains(command, want) {
			t.Fatalf("%s does not select %q: %s", name, want, command)
		}
	}
}

// The migration must be silent on a chassis that is already correct, because it
// runs on every start and ends by bouncing the management interface.
func TestManagementVLANMigrationIsQuietWhenNothingToDo(t *testing.T) {
	command := easyMeshNarrowManagementVLANCommand()
	if !strings.Contains(command, `[ "$changed" = 1 ] || exit 0`) {
		t.Fatalf("the migration is not guarded by a change check: %s", command)
	}
	if strings.Index(command, "uci commit network") < strings.Index(command, `[ "$changed" = 1 ]`) {
		t.Fatalf("the migration commits before it knows anything changed: %s", command)
	}
}
