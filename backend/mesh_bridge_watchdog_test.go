package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The watchdog exists because a network reload drops interfaces out of br-lan
// while iwconfig and mapd_cli both keep reporting the Backhaul as up — so it
// must repair the bridge directly and must never re-run netifd, which is what
// caused the damage.
func TestClientBridgeWatchdogRepairsWithoutNetifd(t *testing.T) {
	source, err := os.ReadFile("mesh_bridge_watchdog.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	if !strings.Contains(body, `"brctl", "addif", clientBridgeName`) {
		t.Fatal("the watchdog does not reattach the interface with brctl")
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

// The AP radios have the same exposure as the stations: a reload drops them out
// of br-lan too, and when it happened on a factory-fresh chassis the SSID stayed
// on the air with nothing bridged behind it, which took a serial console to fix.
//
// LAN ports must stay out of the watched set — Antenna adoption borrows one from
// the client bridge on purpose and returns it itself.
func TestTheWatchdogAlsoWatchesTheAPRadios(t *testing.T) {
	watched := make(map[string]bool)
	for _, iface := range clientBridgeMemberInterfaces() {
		watched[iface] = true
	}
	for _, radio := range []string{Mtk24GIfName, Mtk5GIfName} {
		if !watched[radio] {
			t.Fatalf("the AP radio %q is not watched: %v", radio, clientBridgeMemberInterfaces())
		}
	}
	for _, sta := range backhaulSTAInterfaces() {
		if !watched[sta] {
			t.Fatalf("the Backhaul station %q is no longer watched: %v", sta, clientBridgeMemberInterfaces())
		}
	}
	for _, port := range []string{"lan1", "lan2", "lan3", "lan4", "eth1"} {
		if watched[port] {
			t.Fatalf("%q is watched; reattaching a LAN port would fight Antenna adoption", port)
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

// fakeNetSysfs builds a /sys/class/net layout: every name in present exists, and
// every name in bridged is also a member of the client bridge.
func fakeNetSysfs(t *testing.T, bridgeExists bool, present []string, bridged []string) *[]string {
	t.Helper()
	root := t.TempDir()
	originalRoot := netSysfsRoot
	originalAttach := attachToClientBridge
	t.Cleanup(func() {
		netSysfsRoot = originalRoot
		attachToClientBridge = originalAttach
	})
	netSysfsRoot = root

	if bridgeExists {
		if err := os.MkdirAll(filepath.Join(root, clientBridgeName, "brif"), 0o755); err != nil {
			t.Fatalf("create bridge: %v", err)
		}
	}
	for _, iface := range present {
		if err := os.MkdirAll(filepath.Join(root, iface), 0o755); err != nil {
			t.Fatalf("create %s: %v", iface, err)
		}
	}
	for _, iface := range bridged {
		if err := os.MkdirAll(filepath.Join(root, clientBridgeName, "brif", iface), 0o755); err != nil {
			t.Fatalf("bridge %s: %v", iface, err)
		}
	}

	attached := make([]string, 0, 4)
	attachToClientBridge = func(iface string) (string, error) {
		attached = append(attached, iface)
		return "", nil
	}
	return &attached
}

// The repair the watchdog makes: exactly the interfaces that exist and are not
// already members, and nothing else.
func TestTheWatchdogReattachesOnlyWhatIsMissingFromTheBridge(t *testing.T) {
	// rax0 is already a member; ra0 and apcli0 are up but unbridged, which is
	// what a network reload leaves behind; apclix0 does not exist at all.
	attached := fakeNetSysfs(t, true,
		[]string{Mtk24GIfName, Mtk5GIfName, "apcli0"},
		[]string{Mtk5GIfName})

	if repaired := reattachClientBridgeMembers(); repaired != 2 {
		t.Fatalf("repaired %d interfaces, want 2 (%v)", repaired, *attached)
	}
	got := strings.Join(*attached, ",")
	if got != Mtk24GIfName+",apcli0" {
		t.Fatalf("reattached %q, want the unbridged interfaces only", got)
	}
}

// Early in boot the bridge does not exist yet. Asking brctl anyway would log a
// failure every interval until it did.
func TestTheWatchdogWaitsForTheBridgeToExist(t *testing.T) {
	attached := fakeNetSysfs(t, false, []string{Mtk24GIfName, Mtk5GIfName}, nil)

	if repaired := reattachClientBridgeMembers(); repaired != 0 {
		t.Fatalf("repaired %d interfaces before the bridge existed", repaired)
	}
	if len(*attached) != 0 {
		t.Fatalf("attached %v before the bridge existed", *attached)
	}
}

// The watchdog used to run only for an enabled Agent, so the one chassis that
// needed it most — a standalone unit creating the management VLAN for the first
// time — was never watched at all. It must not consult the Mesh config again.
func TestTheWatchdogRunsWhateverTheMeshIsDoing(t *testing.T) {
	source, err := os.ReadFile("mesh_bridge_watchdog.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	for _, gate := range []string{"loadEasyMeshConfig", "cfg.Enabled", `Role != "agent"`, `Role == "agent"`} {
		if strings.Contains(string(source), gate) {
			t.Fatalf("the watchdog is gated on %s again; a standalone chassis needs it too", gate)
		}
	}
}
