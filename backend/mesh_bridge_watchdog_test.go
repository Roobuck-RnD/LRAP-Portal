package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	//
	// Reading uci is allowed and this used to forbid it outright, which is a
	// different thing from what the rule is for. The watchdog has to know what
	// the LAN is configured to hold before it can tell that the kernel is
	// holding something else, and `uci -q get` changes nothing. What must stay
	// out is anything that writes configuration or hands the interface back to
	// netifd -- that is the damage this watchdog is repairing.
	for _, forbidden := range []string{`"ubus"`, `"ifup"`, `"ifdown"`, `"/sbin/wifi"`,
		`"uci", "set"`, `"uci", "commit"`, `"uci", "delete"`, `"uci", "-q", "set"`} {
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
		// A real brctl addif takes effect, so the fake has to as well: a repair
		// that never shows up in the bridge would make the caller repair forever.
		if err := os.MkdirAll(filepath.Join(root, clientBridgeName, "brif", iface), 0o755); err != nil {
			return "", err
		}
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

// The code that causes a reload has to put the radios back itself. Leaving it to
// the watchdog's own schedule leaves the chassis on the air with nothing bridged
// behind it for up to a full interval -- which on a factory unit building the
// management VLAN for the first time meant it could only be repaired over serial.
func TestAReloadPutsTheRadiosBackBeforeItReturns(t *testing.T) {
	attached := fakeNetSysfs(t, true, []string{Mtk24GIfName, Mtk5GIfName}, nil)
	shortSettle(t, 5*time.Second, 2)

	settleClientBridgeAfterReload()

	if len(*attached) != 2 {
		t.Fatalf("reattached %v, want both radios returned to the bridge", *attached)
	}
}

// netifd's reload is asynchronous: br-lan can be rebuilt again a second or two
// after ubus has returned. A repair that ran once would be quietly undone.
func TestTheSettleSurvivesASecondRebuild(t *testing.T) {
	root := t.TempDir()
	originalRoot := netSysfsRoot
	originalAttach := attachToClientBridge
	t.Cleanup(func() {
		netSysfsRoot = originalRoot
		attachToClientBridge = originalAttach
	})
	netSysfsRoot = root
	shortSettle(t, 10*time.Second, 2)

	brif := filepath.Join(root, clientBridgeName, "brif")
	if err := os.MkdirAll(brif, 0o755); err != nil {
		t.Fatalf("create bridge: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, Mtk24GIfName), 0o755); err != nil {
		t.Fatalf("create radio: %v", err)
	}

	rebuilt := false
	attaches := 0
	attachToClientBridge = func(iface string) (string, error) {
		attaches++
		if err := os.MkdirAll(filepath.Join(brif, iface), 0o755); err != nil {
			return "", err
		}
		// netifd rebuilds the bridge once more just after the first repair.
		if !rebuilt {
			rebuilt = true
			if err := os.RemoveAll(filepath.Join(brif, iface)); err != nil {
				return "", err
			}
		}
		return "", nil
	}

	settleClientBridgeAfterReload()

	if attaches < 2 {
		t.Fatalf("repaired %d time(s); a single pass would have been undone by the rebuild", attaches)
	}
	if !interfaceIsBridged(clientBridgeName, Mtk24GIfName) {
		t.Fatal("the radio was left out of the bridge after the second rebuild")
	}
}

// A bridge that was never broken must not hold startup for the whole window.
func TestTheSettleReturnsQuicklyWhenNothingIsBroken(t *testing.T) {
	attached := fakeNetSysfs(t, true,
		[]string{Mtk24GIfName, Mtk5GIfName},
		[]string{Mtk24GIfName, Mtk5GIfName})
	shortSettle(t, 30*time.Second, 2)

	start := time.Now()
	settleClientBridgeAfterReload()
	elapsed := time.Since(start)

	if len(*attached) != 0 {
		t.Fatalf("a healthy bridge was 'repaired': %v", *attached)
	}
	// Two steady checks, one sleep between them -- nowhere near the window.
	if elapsed > 5*time.Second {
		t.Fatalf("a healthy bridge held startup for %s", elapsed.Round(time.Millisecond))
	}
}

// shortSettle keeps the tests quick without hiding the real defaults.
func shortSettle(t *testing.T, window time.Duration, steady int) {
	t.Helper()
	originalWindow, originalSteady := clientBridgeSettleWindow, clientBridgeSettleSteady
	t.Cleanup(func() {
		clientBridgeSettleWindow = originalWindow
		clientBridgeSettleSteady = originalSteady
	})
	clientBridgeSettleWindow = window
	clientBridgeSettleSteady = steady
}

// ---------- the client bridge keeps the prefix its configuration asks for ----------

// fakeClientLAN replaces the three seams the address repair reads and writes
// through, and records every ip(8) call it makes.
func fakeClientLAN(t *testing.T, proto, ipaddr, netmask string, held []string) *[]string {
	t.Helper()
	calls := []string{}
	realUCI, realShow, realChange := uciClientLANAddress, clientBridgeIPv4, changeClientBridgeAddress
	t.Cleanup(func() {
		uciClientLANAddress, clientBridgeIPv4, changeClientBridgeAddress = realUCI, realShow, realChange
	})
	uciClientLANAddress = func() (string, string, string) { return proto, ipaddr, netmask }
	clientBridgeIPv4 = func() ([]string, error) { return held, nil }
	changeClientBridgeAddress = func(op string, cidr string) (string, error) {
		calls = append(calls, op+" "+cidr)
		return "", nil
	}
	return &calls
}

// The defect this exists for, as measured on the device: uci asks for a /24,
// the vendor EasyMesh script had put the address on with a classful /8, and
// netifd reported the interface up on mask 24 the whole time.
func TestClientBridgeClassfulPrefixIsCorrected(t *testing.T) {
	calls := fakeClientLAN(t, "static", "10.10.18.1", "255.255.255.0", []string{"10.10.18.1/8"})
	if !reconcileClientBridgeAddress() {
		t.Fatal("a bridge holding the wrong prefix was reported as healthy")
	}
	want := []string{"add 10.10.18.1/24", "del 10.10.18.1/8"}
	if len(*calls) != len(want) {
		t.Fatalf("expected %v, got %v", want, *calls)
	}
	for index, call := range want {
		if (*calls)[index] != call {
			t.Fatalf("expected %v, got %v", want, *calls)
		}
	}
}

// Adding before removing is the point of the order: the bridge must never be
// left without an address, and the kernel holds both prefixes happily.
func TestClientBridgeGainsTheRightPrefixBeforeLosingTheWrongOne(t *testing.T) {
	calls := fakeClientLAN(t, "static", "10.10.18.1", "255.255.255.0", []string{"10.10.18.1/8"})
	reconcileClientBridgeAddress()
	if len(*calls) < 2 || !strings.HasPrefix((*calls)[0], "add ") || !strings.HasPrefix((*calls)[1], "del ") {
		t.Fatalf("the repair must add before it removes, got %v", *calls)
	}
}

// An Agent that has handed over to the Root runs on a DHCP lease. Its address
// is not this watchdog's to assert, whatever the stale static block says.
func TestClientBridgeAddressIsLeftAloneUnderDHCP(t *testing.T) {
	calls := fakeClientLAN(t, "dhcp", "10.10.18.1", "255.255.255.0", []string{"10.10.18.128/23"})
	if reconcileClientBridgeAddress() || len(*calls) != 0 {
		t.Fatalf("a DHCP LAN was interfered with: %v", *calls)
	}
}

// The handoff safety property, and the reason the rule is "fix what is there"
// rather than "make sure it is there".
//
// udhcpc flushes br-lan between deconfig and bound. Putting the static address
// back in that gap would leave 10.10.18.1 on the bridge, and the handoff decides
// it is done by seeing an address that is NOT 10.10.18.1 -- so a helpful
// watchdog would make a working handoff time out as no-root-dhcp-lease.
func TestClientBridgeAddressIsNeverInventedWhenAbsent(t *testing.T) {
	calls := fakeClientLAN(t, "static", "10.10.18.1", "255.255.255.0", nil)
	if reconcileClientBridgeAddress() || len(*calls) != 0 {
		t.Fatalf("the watchdog created an address that was deliberately absent: %v", *calls)
	}
}

// A healthy bridge is not touched, and neither is anything else living on it --
// the Root carries a second, unrelated address on br-lan.
func TestClientBridgeLeavesAHealthyBridgeAndItsOtherAddressesAlone(t *testing.T) {
	calls := fakeClientLAN(t, "static", "10.10.18.1", "255.255.254.0",
		[]string{"10.10.18.1/23", "10.95.64.6/24"})
	if reconcileClientBridgeAddress() || len(*calls) != 0 {
		t.Fatalf("a healthy bridge was changed: %v", *calls)
	}
}

func TestNetmaskPrefix(t *testing.T) {
	for mask, want := range map[string]int{
		"255.255.255.0":   24,
		"255.255.254.0":   23,
		"255.0.0.0":       8,
		"255.255.255.255": 32,
	} {
		got, ok := netmaskPrefix(mask)
		if !ok || got != want {
			t.Fatalf("%s: expected /%d, got /%d ok=%v", mask, want, got, ok)
		}
	}
	// Holes, short forms and junk are not netmasks, and a mask that cannot be
	// read must leave the bridge alone rather than guess at a prefix.
	for _, bad := range []string{"255.0.255.0", "255.255.255", "0.0.0.0", "", "abc", "255.255.255.256"} {
		if _, ok := netmaskPrefix(bad); ok {
			t.Fatalf("%q was accepted as a netmask", bad)
		}
	}
}

func TestParseIPv4CIDRs(t *testing.T) {
	// Exactly the shape the device produced: the stray classful address first,
	// and the Root's unrelated second address behind it.
	raw := `22: br-lan    inet 10.10.18.1/8 brd 10.255.255.255 scope global br-lan
22: br-lan    inet 10.95.64.6/24 brd 10.95.64.255 scope global br-lan`
	got := parseIPv4CIDRs(raw)
	want := []string{"10.10.18.1/8", "10.95.64.6/24"}
	if len(got) != len(want) {
		t.Fatalf("expected %v, got %v", want, got)
	}
	for index, address := range want {
		if got[index] != address {
			t.Fatalf("expected %v, got %v", want, got)
		}
	}
}
