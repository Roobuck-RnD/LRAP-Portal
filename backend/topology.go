package main

import "os"

// topology.go is the single source of truth for the device topology and its constants. Every AP management IP, the AC IP and the anonymous SID
// come from here; do not write them as literals in other files, or a topology or subnet change will miss them.

// AnonSID is the zero SID the AC uses for anonymous (unauthenticated) ubus calls to an AP. The AP's rpcd
// unauthenticated ACL grants this SID. Defined here and nowhere else.
const AnonSID = "00000000000000000000000000000000"

// ACManagementIP is the fixed management IP of the main module (AC).
const ACManagementIP = "10.10.18.1"

// Mesh isolates the per-chassis Antenna management plane from the client LAN.
// Every chassis may reuse this /29 because VLAN 200 never crosses a wireless
// backhaul.  The marker is written only after every locally attached Antenna
// has been migrated and verified through the isolated management bridge.
const (
	AntennaManagementACIP   = "172.31.255.1"
	AntennaManagementBridge = "br-mgmt"
	// The netifd interface that owns that bridge.
	AntennaManagementInterface = "antenna_mgmt"
)

// Legacy marker recording that a Mesh migration completed. Isolation is decided
// from the network configuration now; this remains only as a fallback for a unit
// whose configuration cannot be read. A var so tests can point it elsewhere.
var AntennaManagementMarker = "/etc/roobuck/easymesh_network_v1"

// apManagementIPs are the fixed management IPs of the sub-modules (APs). The AC's LAN ports connect to the APs' WAN ports,
// and the APs use these static addresses, all on the 10.10.18.0/24 management network.
var legacyAPManagementIPs = []string{
	"10.10.18.2",
	"10.10.18.3",
	"10.10.18.4",
	"10.10.18.5",
}

var isolatedAPManagementIPs = []string{
	"172.31.255.2",
	"172.31.255.3",
	"172.31.255.4",
	"172.31.255.5",
}

// antennaManagementIsIsolated reports whether Antenna management runs on its own
// VLAN rather than on the client LAN.
//
// This asks the network configuration, not a marker file. The marker only ever
// recorded "a Mesh migration completed here", which stops being the right
// question once the management VLAN is the ordinary arrangement rather than
// something Mesh switches on: a chassis that has the VLAN configured manages its
// Antennas over it whether or not it has ever been in a Mesh. The marker is
// still written and still consulted as a fallback so a unit whose
// configuration read fails does not silently change planes.
var antennaManagementIsIsolated = func() bool {
	if easyMeshManagementNetworkConfigured() {
		return true
	}
	_, err := os.Stat(AntennaManagementMarker)
	return err == nil
}

// APManagementIPs returns the managed APs' IPs (a copy, so callers cannot change the internal slice).
func APManagementIPs() []string {
	pool := legacyAPManagementIPs
	if antennaManagementIsIsolated() {
		pool = isolatedAPManagementIPs
	}
	out := make([]string, len(pool))
	copy(out, pool)
	return out
}

func LegacyAPManagementIPs() []string {
	out := make([]string, len(legacyAPManagementIPs))
	copy(out, legacyAPManagementIPs)
	return out
}

// IsManagedAPIP reports whether an IP is the management address of a managed AP.
func IsManagedAPIP(ip string) bool {
	for _, pool := range [][]string{legacyAPManagementIPs, isolatedAPManagementIPs} {
		for _, a := range pool {
			if a == ip {
				return true
			}
		}
	}
	return false
}
