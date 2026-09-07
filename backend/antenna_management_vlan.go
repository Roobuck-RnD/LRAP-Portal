package main

import (
	"log"
	"strconv"
	"strings"
)

// ensureAntennaManagementVLAN puts Antenna management on its own VLAN and keeps
// it there, whether or not this chassis is in a Mesh.
//
// Antennas used to be managed on the client LAN at fixed addresses 10.10.18.2-5,
// and the isolated VLAN was something Enable Mesh switched on. Having two
// arrangements was the root of a whole family of faults, every one of them
// caused by the transition rather than by either arrangement:
//
//   - the Antenna pool was rebound between the client LAN and the management
//     bridge, and a boot that ran the repair before netifd was up rebound it the
//     wrong way, after which no Antenna could get a management address;
//   - a rollback restored an Antenna to untagged operation while the chassis had
//     already moved to tagged ports, orphaning it where nothing could reach it;
//   - discovery probed whichever pool the current mode named, so an Antenna on
//     the other side of the switch was simply invisible.
//
// With one permanent arrangement none of those transitions exist.
//
// This only creates what is missing. Migrating an Antenna that is still talking
// untagged is a separate step -- see adoptUntaggedAntennas -- because it needs
// the Antenna pool to be serving the management bridge first.
func ensureAntennaManagementVLAN() {
	if easyMeshManagementNetworkConfigured() {
		return
	}
	log.Printf("Antenna management: creating the management VLAN")
	if out, err := easyMeshExecLocal(easyMeshPrepareACManagementCommand()); err != nil {
		// Not fatal: the chassis keeps serving clients and the next start tries
		// again. Antennas stay unreachable until it succeeds, which the Antenna
		// pages already report as an offline module.
		log.Printf("Antenna management: could not create the management VLAN: %v output=%s", err, out)
		return
	}
	log.Printf("Antenna management: management VLAN ready on %s", AntennaManagementBridge)
}

// migrateUntaggedAntennasAtStartup brings any Antenna still talking untagged --
// a factory-fresh replacement, or one left behind by an older build -- onto the
// management VLAN. It must run after the Antenna DHCP pool has been bound to the
// management bridge, because that pool is what answers the Antenna once its port
// is briefly bridged across.
func migrateUntaggedAntennasAtStartup() {
	if adopted := adoptUntaggedAntennas(); adopted > 0 {
		log.Printf("Antenna management: moved %d Antenna(s) onto the management VLAN", adopted)
	}
}

// alignAntennaClientPlanes makes sure every reachable Antenna carries client
// traffic untagged.
//
// Only the management plane is tagged, so an Antenna left on the old client VLAN
// -- by an earlier build, or by a preparation that stopped half way -- is still
// perfectly reachable for management while every device behind it is cut off:
// the AC's IP stack never sees frames tagged for a VLAN it does not carry.
// Measured: associated, holding a stale lease, no route to anything.
//
// That state is invisible to adoption, which only looks for Antennas it cannot
// reach at all, so it is corrected here on its own terms.
func alignAntennaClientPlanes() {
	if !antennaManagementIsIsolated() {
		return
	}
	for _, module := range discoverManagedAPsForAlignment() {
		tagged, err := antennaClientPlaneIsTagged(module.IP)
		if err != nil || !tagged {
			continue
		}
		log.Printf("Antenna management: %s still tags client traffic; returning it to untagged", module.ModuleID)
		if _, err := easyMeshExecRemote(module.IP, easyMeshAntennaUntaggedClientCommand()); err != nil &&
			!easyMeshRemoteDispatchResultAmbiguous(err) {
			log.Printf("Antenna management: could not return client traffic to untagged for the Antenna at %s: %v",
				module.IP, err)
			continue
		}
		log.Printf("Antenna management: the Antenna at %s now carries client traffic untagged", module.IP)
	}
}

// antennaClientPlaneIsTagged reports whether the Antenna's client bridge is
// still on the retired client VLAN rather than untagged.
func antennaClientPlaneIsTagged(ip string) (bool, error) {
	out, err := easyMeshExecRemote(ip, "uci -q get network.@device[0].ports")
	if err != nil {
		return false, err
	}
	for _, port := range strings.Fields(out) {
		if strings.HasSuffix(port, "."+strconv.Itoa(retiredClientVLAN)) {
			return true, nil
		}
	}
	return false, nil
}

// A seam so the alignment can be exercised without a network.
var discoverManagedAPsForAlignment = func() []ManagedModule {
	return discoverManagedAPs(false)
}
