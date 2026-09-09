package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// How often the client bridge is re-checked.
const clientBridgeCheckInterval = 15 * time.Second

// Where bridge membership is read from. It is a variable so the watchdog can be
// exercised against a fabricated layout instead of the running kernel's.
var netSysfsRoot = "/sys/class/net"

// backhaulSTAInterfaces returns both stations of the card. The Mesh only uses
// the selected band's, but the ordinary bridge carries both, so both are
// restored to keep the composition identical to a healthy boot.
func backhaulSTAInterfaces() []string {
	return []string{"apcli0", "apclix0"}
}

// clientBridgeMemberInterfaces returns every wireless interface that belongs in
// the client bridge on a healthy boot: the two APs this chassis serves clients
// with, and the two Backhaul stations.
//
// The AP radios are here because they have exactly the same exposure as the
// stations and nothing else covers them. Measured on a factory-fresh chassis the
// first time it ran a build that creates the Antenna management VLAN: the
// `ubus call network reload` that ends that setup rebuilt br-lan without ra0 and
// rax0, and they never came back. The radios stayed up and the SSID stayed on
// the air, so clients still associated -- and nothing was bridged to anything,
// so the chassis could only be repaired over the serial console.
//
// LAN ports are deliberately absent. Antenna adoption takes one out of the
// client bridge on purpose and puts it back itself, and a watchdog that also
// reattached ports would fight it mid-adoption.
func clientBridgeMemberInterfaces() []string {
	return append([]string{Mtk24GIfName, Mtk5GIfName}, backhaulSTAInterfaces()...)
}

func interfaceIsBridged(bridge string, iface string) bool {
	_, err := os.Stat(filepath.Join(netSysfsRoot, bridge, "brif", iface))
	return err == nil
}

func interfaceExists(iface string) bool {
	_, err := os.Stat(filepath.Join(netSysfsRoot, iface))
	return err == nil
}

// attachToClientBridge performs the one repair this watchdog makes.
//
// brctl is used rather than a netifd operation on purpose -- re-running netifd
// is what caused the damage in the first place.
var attachToClientBridge = func(iface string) (string, error) {
	out, err := exec.Command("brctl", "addif", clientBridgeName, iface).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// reattachClientBridgeMembers puts every wireless interface that belongs in the
// client bridge back into it, and reports how many it had to repair.
//
// Any `ubus call network reload` rebuilds br-lan and drops these out of it.
// Measured on the device: after a reload the Mesh data path is dead -- mapd
// immediately loses the Controller -- while `iwconfig` still shows the station
// associated and `mapd_cli bh_conn_status` still returns 1. Both of the signals
// the product uses to decide "the Backhaul is up" keep saying yes, so nothing
// notices. A network reload is not exotic: editing any network setting in the
// portal triggers one, and so does the first start of a build that has to create
// the Antenna management VLAN on a chassis that does not carry it yet.
func reattachClientBridgeMembers() int {
	// Before the bridge itself exists there is nothing to repair, and asking
	// brctl anyway would log a failure every interval for the whole of boot.
	if !interfaceExists(clientBridgeName) {
		return 0
	}
	repaired := 0
	for _, iface := range clientBridgeMemberInterfaces() {
		if !interfaceExists(iface) || interfaceIsBridged(clientBridgeName, iface) {
			continue
		}
		out, err := attachToClientBridge(iface)
		if err != nil {
			log.Printf("Client bridge watchdog: could not reattach %s to %s: %v output=%s",
				iface, clientBridgeName, err, out)
			continue
		}
		log.Printf("Client bridge watchdog: reattached %s to %s", iface, clientBridgeName)
		repaired++
	}
	return repaired
}

// startClientBridgeWatchdog keeps this chassis's own wireless interfaces in the
// client bridge for the whole life of the process.
//
// It is deliberately not conditional on the Mesh being enabled or on this device
// being an Agent. The reload that breaks the bridge can come from anywhere --
// our own code, the portal's network pages, or an operator on the shell -- and
// the worst case measured so far happened on a standalone chassis, where an
// earlier Agent-only guard meant this never ran at all.
func startClientBridgeWatchdog() {
	go func() {
		for {
			time.Sleep(clientBridgeCheckInterval)
			reattachClientBridgeMembers()
		}
	}()
}
