package main

import (
	"strconv"
	"strings"
)

// ap_port.go gives each AP a stable identity/number from the AC's physical LAN port it is plugged into. The port is fixed by the cabling,
// so it does not move with the DHCP address or a reboot, which makes it steadier than an IP. The mapping:
//   IP --(/proc/net/arp)--> MAC --(bridge fdb)--> lanN
// Reuses parseARPByIP() and listLanEdgePortsByMAC() from lan_clients.go.

// apPortIndexByIP returns "AP IP -> physical port number (1-4)". An IP that cannot be resolved is left out of the map.
func apPortIndexByIP() map[string]int {
	return managedAPPortIndexByIP(discoverManagedAPs(false))
}

// lanPortNumber parses N out of "lanN" (e.g. "lan3" -> 3); anything that is not a lan port returns 0.
func lanPortNumber(port string) int {
	p := strings.ToLower(strings.TrimSpace(port))
	if !strings.HasPrefix(p, "lan") {
		return 0
	}
	n, err := strconv.Atoi(strings.TrimPrefix(p, "lan"))
	if err != nil {
		return 0
	}
	return n
}

// apDisplayName gives an AP a display name numbered by its physical port: with portIndex>0 it is "Antenna" + the port number
// (e.g. lan1 -> "Antenna1"); when the port cannot be resolved (portIndex<=0) it falls back to the real hostname.
// It only changes what the frontend shows, never the device's real hostname.
func apDisplayName(hostname string, portIndex int) string {
	if portIndex <= 0 {
		return hostname
	}
	return "Antenna" + strconv.Itoa(portIndex)
}
