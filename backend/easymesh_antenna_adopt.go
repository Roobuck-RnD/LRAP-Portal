package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"
)

// How long an Antenna is given to take a management lease once its port has been
// bridged onto the management network. It retries DHCP every few seconds, so this
// only has to cover a couple of attempts.
const easyMeshAntennaAdoptionTimeout = 45 * time.Second

// easyMeshOrphanedAntennaPorts returns the LAN ports that have an Antenna
// plugged in but no Antenna the product can talk to.
//
// A port with carrier and no module behind it is the signature of an Antenna
// that has fallen off the management VLAN.
func easyMeshOrphanedAntennaPorts(linked []int, discovered []ManagedModule) []int {
	seen := make(map[int]bool, len(discovered))
	for _, module := range discovered {
		seen[module.PortIndex] = true
	}
	orphans := make([]int, 0, len(linked))
	for _, port := range linked {
		if !seen[port] {
			orphans = append(orphans, port)
		}
	}
	return orphans
}

// adoptUntaggedAntennas puts Antennas that lost the management VLAN back on it.
//
// An Antenna that is restored to its pre-Mesh configuration talks untagged, but
// once this chassis is in Mesh mode its LAN ports only carry lanN.100 and
// lanN.200 -- the untagged frames land in no bridge at all. The Antenna cannot
// recover on its own either, and not because it is broken: it asks for DHCP
// every three seconds with vendor class RoobuckAP, the ordinary client pool is
// tagged '!rbap' and refuses it by design, and its own pool only exists on the
// management bridge it can no longer reach. Measured on the live Agent -- the
// Antenna had been asking, unanswered, for as long as it had been orphaned.
//
// So the raw port is bridged onto the management network for as long as it takes
// the existing Antenna pool to hand out an address, the Antenna is given back its
// VLAN configuration, and the raw port is removed again. Nothing here is a new
// mechanism: it is the same preparation the Mesh runs, applied to a module that
// missed it.
func adoptUntaggedAntennas() int {
	if !antennaManagementIsIsolated() || !easyMeshManagementNetworkConfigured() {
		return 0
	}
	orphans := easyMeshOrphanedAntennaPorts(managedAntennaLinkedPortIndexes(), discoverManagedAPs(true))
	adopted := 0
	for _, port := range orphans {
		if adoptUntaggedAntennaOnPort(port) {
			adopted++
		}
	}
	return adopted
}

func adoptUntaggedAntennaOnPort(portIndex int) bool {
	port := "lan" + strconv.Itoa(portIndex)
	if _, err := os.Stat("/sys/class/net/" + port); err != nil {
		return false
	}
	log.Printf("EasyMesh: %s has a linked Antenna the Mesh cannot see; adopting it onto the management VLAN", port)

	if _, err := easyMeshExecLocal("brctl addif " + AntennaManagementBridge + " " + port); err != nil {
		log.Printf("EasyMesh: could not bridge %s for adoption: %v", port, err)
		return false
	}
	// The raw port must not be left in the management bridge: it carries the
	// Antenna's untagged client traffic too, which does not belong there.
	defer func() {
		if _, err := easyMeshExecLocal("brctl delif " + AntennaManagementBridge + " " + port); err != nil {
			log.Printf("EasyMesh: could not remove %s from %s after adoption: %v",
				port, AntennaManagementBridge, err)
		}
	}()

	ip, err := waitForAdoptedAntennaAddress(easyMeshAntennaAdoptionTimeout)
	if err != nil {
		log.Printf("EasyMesh: no Antenna answered on %s within %s: %v", port, easyMeshAntennaAdoptionTimeout, err)
		return false
	}

	if _, err := easyMeshExecRemote(ip, easyMeshPrepareAntennaManagementCommand()); err != nil {
		log.Printf("EasyMesh: could not restore the management VLAN on the Antenna at %s: %v", ip, err)
		return false
	}
	// The trunk command ends by reloading the Antenna's network, which drops the
	// very address the reply has to travel over -- the call reports "ubus bad
	// result body=" on a change that in fact applied. Measured here: the Antenna
	// came back correctly trunked on lanN.200 after exactly that error. So an
	// ambiguous dispatch is not treated as failure; what settles it is whether
	// the Antenna reappears on the tagged management VLAN.
	if _, err := easyMeshExecRemote(ip, easyMeshActivateAntennaTrunkCommand()); err != nil &&
		!easyMeshRemoteDispatchResultAmbiguous(err) {
		log.Printf("EasyMesh: could not restore the client VLAN trunk on the Antenna at %s: %v", ip, err)
		return false
	}
	// Confirm it rather than assume it: the reply to the trunk command is
	// routinely lost, so the Antenna reappearing on the tagged VLAN is the only
	// honest evidence the change took.
	for attempt := 0; attempt < 6; attempt++ {
		if easyMeshAntennaTaggedOnManagementVLAN(portIndex) {
			log.Printf("EasyMesh: adopted the Antenna on %s back onto the management VLAN (was %s)", port, ip)
			return true
		}
		time.Sleep(3 * time.Second)
	}
	log.Printf("EasyMesh: the Antenna on %s did not come back on the tagged management VLAN after adoption", port)
	return false
}

// easyMeshAntennaTaggedOnManagementVLAN reports whether the Antenna is talking
// on the port's management VLAN rather than untagged, which is what proves the
// adoption held once the raw port is taken away again.
func easyMeshAntennaTaggedOnManagementVLAN(portIndex int) bool {
	tagged := "lan" + strconv.Itoa(portIndex) + "." + strconv.Itoa(easyMeshManagementVLAN)
	out, err := easyMeshExecLocal("bridge fdb show br " + AntennaManagementBridge + " 2>/dev/null")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " "+tagged+" ") && !strings.Contains(line, "permanent") {
			return true
		}
	}
	return false
}

// waitForAdoptedAntennaAddress returns the management address of an Antenna that
// has just been let onto the management network. The pool is small and the
// Antenna is the only thing that can be answered on it, so any management lease
// that appears belongs to the module being adopted.
var waitForAdoptedAntennaAddress = func(timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		for _, ip := range APManagementIPs() {
			if _, err := easyMeshExecLocal("ping -c 1 -W 2 " + ip + " >/dev/null 2>&1"); err == nil {
				return ip, nil
			}
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("no Antenna took a management address within %s", timeout.Round(time.Second))
		}
		time.Sleep(3 * time.Second)
	}
}

// easyMeshUndiscoveredAntennaError describes ports that hold an Antenna the Mesh
// could not reach, so preparation refuses rather than quietly building a Mesh
// that leaves them stranded.
func easyMeshUndiscoveredAntennaError(linked []int, discovered []ManagedModule) error {
	orphans := easyMeshOrphanedAntennaPorts(linked, discovered)
	if len(orphans) == 0 {
		return nil
	}
	names := make([]string, 0, len(orphans))
	for _, port := range orphans {
		names = append(names, "lan"+strconv.Itoa(port))
	}
	return fmt.Errorf(
		"an Antenna is connected to %s but did not answer on the management network; "+
			"power-cycle it and run Enable Mesh again, otherwise it would be left off the Mesh without being reported",
		strings.Join(names, ", "))
}
