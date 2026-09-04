package main

import (
	"os"
	"strings"
	"testing"
)

// The watchdog exists because a network reload drops the Backhaul station out of
// br-lan while iwconfig and mapd_cli both keep reporting the Backhaul as up — so
// it must repair the bridge directly and must never re-run netifd, which is what
// caused the damage.
func TestBackhaulBridgeWatchdogRepairsWithoutNetifd(t *testing.T) {
	source, err := os.ReadFile("mesh_bridge_watchdog.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	if !strings.Contains(body, `"brctl", "addif", clientBridgeName`) {
		t.Fatal("the watchdog does not reattach the station with brctl")
	}
	// Match invocations, not the comment that explains why they are absent.
	for _, forbidden := range []string{`"ubus"`, `"ifup"`, `"ifdown"`, `"/sbin/wifi"`, `exec.Command("uci"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the watchdog invokes the disruptive %s", forbidden)
		}
	}
}

// Both stations of the card belong in the bridge on a healthy boot.
func TestBackhaulSTAInterfacesCoverBothBands(t *testing.T) {
	stas := backhaulSTAInterfaces()
	if len(stas) != 2 {
		t.Fatalf("expected both stations, got %v", stas)
	}
	for _, band := range []string{"2g", "5g"} {
		selected := easyMeshBackhaulSTAInterface(band)
		found := false
		for _, sta := range stas {
			if sta == selected {
				found = true
			}
		}
		if !found {
			t.Fatalf("the %s Backhaul station %q is not watched: %v", band, selected, stas)
		}
	}
}

// A missing interface must never be "repaired" into the bridge.
func TestWatchdogSkipsAbsentInterfaces(t *testing.T) {
	if interfaceExists("definitely-not-an-interface") {
		t.Fatal("a missing interface was reported as present")
	}
	if interfaceIsBridged(clientBridgeName, "definitely-not-an-interface") {
		t.Fatal("a missing interface was reported as bridged")
	}
}
