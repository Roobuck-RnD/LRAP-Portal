package main

import (
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
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
// Only one adoption pass may run at a time. A pass lends a raw LAN port to the
// management bridge and gives it back afterwards; two passes doing that at once
// would race over which bridge the port ends up in, and now that a timer can
// start one there is more than the startup path to collide with.
var antennaAdoptionMu sync.Mutex

func adoptUntaggedAntennas() int {
	antennaAdoptionMu.Lock()
	defer antennaAdoptionMu.Unlock()
	if !antennaManagementIsIsolated() || !easyMeshManagementNetworkConfigured() {
		return 0
	}
	if len(easyMeshOrphanedAntennaPorts(managedAntennaLinkedPortIndexes(), discoverManagedAPs(true))) == 0 {
		return 0
	}

	// A unit coming from the old arrangement still has its Antennas on the client
	// LAN at 10.10.18.2-5, where they are reachable right now. Reconfiguring them
	// in place is the whole migration -- no bridging trick, no window where the
	// Antenna has no address.
	adopted := migrateLegacyAddressedAntennas()

	// Whatever is still missing is talking untagged with no address at all: a
	// factory-fresh module, or one an older build restored. That one does need
	// the port bridged across so the Antenna pool can answer it.
	for _, port := range easyMeshOrphanedAntennaPorts(managedAntennaLinkedPortIndexes(), discoverManagedAPs(true)) {
		if adoptUntaggedAntennaOnPort(port) {
			adopted++
			settleAdoptedAntenna(port)
		}
	}
	return adopted
}

// migrateLegacyAddressedAntennas moves Antennas that are still managed on the
// client LAN onto the management VLAN.
//
// Every Antenna gets the same configuration, so which port an address belongs to
// does not matter here; what matters is that each one that answers is given the
// VLAN it should have been on.
func migrateLegacyAddressedAntennas() int {
	legacy := LegacyAPManagementIPs()
	probeStaticIPs(legacy)

	migrated := 0
	for _, ip := range legacy {
		if _, err := easyMeshExecLocal("ping -c 1 -W 2 " + ip + " >/dev/null 2>&1"); err != nil {
			continue
		}
		log.Printf("Antenna management: %s is still on the client LAN; moving it to the management VLAN", ip)
		if !applyAntennaManagementVLAN(ip) {
			continue
		}
		migrated++
	}
	return migrated
}

// applyAntennaManagementVLAN puts one Antenna's MANAGEMENT traffic on VLAN 200
// and makes sure its client traffic is untagged.
//
// Only management is tagged. Tagging clients too was a second arrangement that
// had to be switched on and off with the Mesh, and while the two ends disagreed
// every device behind the Antenna lost the network: associated, holding a stale
// lease, no route to anything.
//
// Both commands end by reloading the Antenna's network, which drops the address
// the reply travels over, so an ambiguous dispatch is not treated as failure --
// see adoptUntaggedAntennaOnPort.
func applyAntennaManagementVLAN(ip string) bool {
	if _, err := easyMeshExecRemote(ip, easyMeshPrepareAntennaManagementCommand()); err != nil &&
		!easyMeshRemoteDispatchResultAmbiguous(err) {
		log.Printf("Antenna management: could not set the management VLAN on the Antenna at %s: %v", ip, err)
		return false
	}

	// Client traffic is never tagged, in a Mesh or out of one, so this is a
	// plain assertion rather than a choice that has to follow the chassis.
	if _, err := easyMeshExecRemote(ip, easyMeshAntennaUntaggedClientCommand()); err != nil &&
		!easyMeshRemoteDispatchResultAmbiguous(err) {
		log.Printf("Antenna management: could not return client traffic to untagged for the Antenna at %s: %v", ip, err)
		return false
	}
	return true
}

// easyMeshAntennaUntaggedClientCommand asserts the Antenna's client bridge:
// eth1 plus both radios, untagged, with the management VLAN left alone -- and
// no address of its own on it.
//
// The Antenna only bridges on the client plane; it needs no address there to
// forward a client's traffic, and its own traffic already leaves through the
// management interface, which carries its default route.
//
// It used to be a DHCP client there too, and that is what broke the management
// plane. Both of its DHCP clients use the same MAC, and dnsmasq keys a lease on
// the MAC when no client-id is sent, keeping one lease per client: the second
// lease an Antenna took replaced its first. Measured on the Root just after an
// upgrade -- an Antenna took 172.31.255.2 on the management VLAN and 10.10.18.180
// on the client LAN a moment later, the .2 record vanished, dnsmasq handed .2 to
// the next Antenna, and one of the two dropped out of management for three
// minutes. It only showed up once the identity came off the client plane: while
// that plane was refused an address, there was only ever one lease to keep.
func easyMeshAntennaUntaggedClientCommand() string {
	return "set -e; test \"$(uci -q get network.@device[0].name)\" = br-lan; " +
		"uci -q delete network.mesh_client_eth1 2>/dev/null || true; " +
		"uci -q delete network.@device[0].ports; " +
		"uci add_list network.@device[0].ports=eth1; " +
		"uci add_list network.@device[0].ports=ra0; " +
		"uci add_list network.@device[0].ports=rax0; " +
		// No address on the client plane, and nothing left over from the retired
		// arrangements that gave it one: the static 10.10.18.2-5 of an old
		// snapshot, or the identity the factory script used to put here.
		"uci set network.lan.proto=none; " +
		"for o in ipaddr netmask gateway dns vendorid clientid; do uci -q delete network.lan.$o 2>/dev/null || true; done; " +
		"uci commit network; ubus call network reload"
}

// adoptUntaggedAntennasOnPorts adopts only the ports it is given, and reports
// how many it managed.
//
// The scan needs this rather than adoptUntaggedAntennas, which works out the
// whole orphan set for itself: a retry has to be able to leave alone a port it
// has already given up on, which is the only thing keeping a socket that holds
// no Antenna from being lent out on every pass for ever.
func adoptUntaggedAntennasOnPorts(ports []int) int {
	antennaAdoptionMu.Lock()
	defer antennaAdoptionMu.Unlock()
	if !antennaManagementIsIsolated() || !easyMeshManagementNetworkConfigured() {
		return 0
	}
	adopted := 0
	for _, port := range ports {
		if adoptUntaggedAntennaOnPort(port) {
			adopted++
			settleAdoptedAntenna(port)
		}
	}
	return adopted
}

// settleAdoptedAntenna finishes an adoption once the raw port has been handed
// back: it finds the module on its tagged management address and makes sure
// the client-plane half of adoption actually took.
//
// Adoption verifies only that the module reappeared on the tagged VLAN, which
// the first command alone achieves. The second -- the one that takes the
// identity off the client plane -- was lost on three of four Antennas on the
// Root without anything noticing. Reading it back here, rather than waiting for
// the next scan pass, fixes it before the refusals start.
func settleAdoptedAntenna(portIndex int) {
	for _, module := range discoverManagedAPs(true) {
		if module.PortIndex == portIndex {
			alignAntennaClientPlane(module)
			return
		}
	}
}

func adoptUntaggedAntennaOnPort(portIndex int) bool {
	port := "lan" + strconv.Itoa(portIndex)
	if _, err := os.Stat("/sys/class/net/" + port); err != nil {
		return false
	}
	log.Printf("EasyMesh: %s has a linked Antenna the Mesh cannot see; adopting it onto the management VLAN", port)

	// Which addresses are already taken has to be known before this port is
	// bridged across, or there is no way to tell the module arriving now from
	// one that was adopted long ago.
	baseline := liveAntennaManagementAddresses()

	// A port can only belong to one bridge. On a chassis whose LAN ports are
	// still untagged the raw port sits in the client bridge, and brctl refuses
	// with "Resource busy" -- so it is lent out and given back.
	borrowedFromClientBridge := interfaceIsBridged(clientBridgeName, port)
	if borrowedFromClientBridge {
		if _, err := easyMeshExecLocal("brctl delif " + clientBridgeName + " " + port); err != nil {
			log.Printf("EasyMesh: could not free %s from %s for adoption: %v", port, clientBridgeName, err)
			return false
		}
	}
	if _, err := easyMeshExecLocal("brctl addif " + AntennaManagementBridge + " " + port); err != nil {
		log.Printf("EasyMesh: could not bridge %s for adoption: %v", port, err)
		if borrowedFromClientBridge {
			_, _ = easyMeshExecLocal("brctl addif " + clientBridgeName + " " + port)
		}
		return false
	}
	// The raw port must not be left in the management bridge: it carries the
	// Antenna's untagged client traffic too, which does not belong there.
	defer func() {
		if _, err := easyMeshExecLocal("brctl delif " + AntennaManagementBridge + " " + port); err != nil {
			log.Printf("EasyMesh: could not remove %s from %s after adoption: %v",
				port, AntennaManagementBridge, err)
		}
		if borrowedFromClientBridge {
			if _, err := easyMeshExecLocal("brctl addif " + clientBridgeName + " " + port); err != nil {
				log.Printf("EasyMesh: could not return %s to %s after adoption: %v",
					port, clientBridgeName, err)
			}
		}
	}()

	ip, err := waitForAdoptedAntennaAddress(baseline, easyMeshAntennaAdoptionTimeout)
	if err != nil {
		log.Printf("EasyMesh: no Antenna answered on %s within %s: %v", port, easyMeshAntennaAdoptionTimeout, err)
		return false
	}

	if !applyAntennaManagementVLAN(ip) {
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

// liveAntennaManagementAddresses reports which management addresses already
// answer. Adoption takes this before it touches anything, so it can wait for an
// address that was not there rather than for any address at all.
var liveAntennaManagementAddresses = func() map[string]bool {
	live := make(map[string]bool, len(APManagementIPs()))
	for _, ip := range APManagementIPs() {
		if _, err := easyMeshExecLocal("ping -c 1 -W 2 " + ip + " >/dev/null 2>&1"); err == nil {
			live[ip] = true
		}
	}
	return live
}

// waitForAdoptedAntennaAddress returns the management address taken by the
// Antenna that has just been let onto the management network.
//
// Only an address absent from the baseline counts. This used to return the
// first address in the pool that answered, on the reasoning that the module
// being adopted was the only thing that could be answered on it -- true of a
// chassis with one Antenna, false of every chassis with two. The pool is walked
// in order from 172.31.255.2, and an Antenna adopted earlier holds its address
// and answers every ping, so the walk returned that one instead: the already
// configured module was configured again, the new one never was, and the
// port-specific check that follows then failed for its full eighteen seconds.
// A second Antenna could not be adopted at all.
var waitForAdoptedAntennaAddress = func(baseline map[string]bool, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	for {
		for _, ip := range APManagementIPs() {
			if baseline[ip] {
				continue
			}
			if _, err := easyMeshExecLocal("ping -c 1 -W 2 " + ip + " >/dev/null 2>&1"); err == nil {
				return ip, nil
			}
		}
		if !time.Now().Before(deadline) {
			return "", fmt.Errorf("no new Antenna took a management address within %s", timeout.Round(time.Second))
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
