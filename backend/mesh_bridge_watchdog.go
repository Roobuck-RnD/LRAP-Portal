package main

import (
	"log"
	"os"
	"os/exec"
	"strings"
	"time"
)

// How often an Agent re-checks that its Backhaul station is still bridged.
const backhaulBridgeCheckInterval = 15 * time.Second

// backhaulSTAInterfaces returns both stations of the card. The Mesh only uses
// the selected band's, but the ordinary bridge carries both, so both are
// restored to keep the composition identical to a healthy boot.
func backhaulSTAInterfaces() []string {
	return []string{"apcli0", "apclix0"}
}

func interfaceIsBridged(bridge string, iface string) bool {
	_, err := os.Stat("/sys/class/net/" + bridge + "/brif/" + iface)
	return err == nil
}

func interfaceExists(iface string) bool {
	_, err := os.Stat("/sys/class/net/" + iface)
	return err == nil
}

// reattachBackhaulSTAs puts the Backhaul stations back into the client bridge and
// reports how many it had to repair.
//
// Any `ubus call network reload` rebuilds br-lan and drops apcli0/apclix0 out of
// it. Measured on the device: after a reload the Mesh data path is dead — mapd
// immediately loses the Controller — while `iwconfig` still shows the station
// associated and `mapd_cli bh_conn_status` still returns 1. Both of the signals
// the product uses to decide "the Backhaul is up" keep saying yes, so nothing
// notices. A network reload is not exotic: editing any network setting in the
// portal triggers one.
//
// brctl is used rather than a netifd operation on purpose — re-running netifd is
// what caused the damage in the first place.
func reattachBackhaulSTAs() int {
	repaired := 0
	for _, sta := range backhaulSTAInterfaces() {
		if !interfaceExists(sta) || interfaceIsBridged(clientBridgeName, sta) {
			continue
		}
		out, err := exec.Command("brctl", "addif", clientBridgeName, sta).CombinedOutput()
		if err != nil {
			log.Printf("Mesh bridge watchdog: could not reattach %s to %s: %v output=%s",
				sta, clientBridgeName, err, strings.TrimSpace(string(out)))
			continue
		}
		log.Printf("Mesh bridge watchdog: reattached %s to %s", sta, clientBridgeName)
		repaired++
	}
	return repaired
}

// startBackhaulBridgeWatchdog keeps an Agent's Backhaul station in the client
// bridge for as long as the Mesh is enabled.
//
// This runs for the whole process lifetime rather than only around onboarding,
// because the reload that breaks the bridge can come from anywhere — our own
// code, the portal's network pages, or an operator on the shell.
func startBackhaulBridgeWatchdog() {
	go func() {
		for {
			time.Sleep(backhaulBridgeCheckInterval)
			cfg := loadEasyMeshConfig()
			if !cfg.Enabled || cfg.Role != "agent" {
				continue
			}
			reattachBackhaulSTAs()
		}
	}()
}
