package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

// The mapd control plane carries only the three keys the known-good vendor
// recipe writes. BhPriority* and DhcpCtl must be left at their firmware values:
// the factory already prefers 5GHz (2G=3 Low, 5GL/5GH=1 High), and DhcpCtl is
// what lets mapd hand the Agent's client network over to the Root by itself.
// The Backhaul band is enforced by which interfaces the WPS registrar and
// enrollee are armed on, not by rewriting mapd's band priorities.
func TestEasyMeshMapdUpdatesCarryOnlyTheVendorRecipeKeys(t *testing.T) {
	for _, band := range []string{"2g", "5g"} {
		updates := easyMeshMapdUpdates("1", "2", band)
		if updates["DeviceRole"] != "2" || updates["mode"] != "1" || updates["MapMode"] != "1" {
			t.Fatalf("unexpected Agent controls for %s: %#v", band, updates)
		}
		if len(updates) != 3 {
			t.Fatalf("mapd updates for %s carry keys the vendor recipe never writes: %#v", band, updates)
		}
		for _, forbidden := range []string{"DhcpCtl", "BhPriority2G", "BhPriority5GL", "BhPriority5GH", "BhPriority6G"} {
			if _, present := updates[forbidden]; present {
				t.Fatalf("%s must be left at its firmware value: %#v", forbidden, updates)
			}
		}
	}
	if controller := easyMeshMapdUpdates("0", "1", "5g"); controller["DeviceRole"] != "1" || controller["mode"] != "0" {
		t.Fatalf("unexpected Controller controls: %#v", controller)
	}
}

func TestEasyMeshRuntimeConfigMustMatchSavedRole(t *testing.T) {
	controllerRuntime := "MapMode=1\nmode=0\nDeviceRole=1\nDhcpCtl=0\nBhPriority2G=0\nBhPriority5GL=1\nBhPriority5GH=1\nBhPriority6G=0\n"
	if !easyMeshRuntimeConfigMatches(controllerRuntime, "controller", "5g") {
		t.Fatal("valid Controller runtime configuration was rejected")
	}
	if easyMeshRuntimeConfigMatches(controllerRuntime, "agent", "5g") {
		t.Fatal("Controller runtime configuration must not satisfy an Agent role")
	}
	staleRole := strings.Replace(controllerRuntime, "DeviceRole=1", "DeviceRole=2", 1)
	if easyMeshRuntimeConfigMatches(staleRole, "controller", "5g") {
		t.Fatal("a stale device role must trigger runtime reconciliation")
	}
	// A factory image ships mapd_cfg without MapMode at all; that must count as
	// out of sync so reconciliation adds the control-plane switch.
	noMapMode := strings.Replace(controllerRuntime, "MapMode=1\n", "", 1)
	if easyMeshRuntimeConfigMatches(noMapMode, "controller", "5g") {
		t.Fatal("a mapd runtime without MapMode must trigger reconciliation")
	}
}

func TestEasyMeshTopologyDeviceIDsNormalizesALMACs(t *testing.T) {
	topology := `{"topology information":[{"AL MAC":"F0:A8:82:FB:00:A4"},{"AL MAC":" f0:a8:82:fb:00:e4 "}]}`
	devices := easyMeshTopologyDeviceIDs(topology)
	for _, alID := range []string{"f0:a8:82:fb:00:a4", "f0:a8:82:fb:00:e4"} {
		if !devices[alID] {
			t.Fatalf("topology device %s was not normalized: %#v", alID, devices)
		}
	}
	if len(easyMeshTopologyDeviceIDs("not-json")) != 0 {
		t.Fatal("invalid topology must produce an empty device set")
	}
}

func TestEasyMeshRemoteNodesJoinWirelessTopologyToDHCPLease(t *testing.T) {
	originalReachable := easyMeshManagementReachable
	originalBelongs := easyMeshLeaseBelongsToChassis
	t.Cleanup(func() {
		easyMeshManagementReachable = originalReachable
		easyMeshLeaseBelongsToChassis = originalBelongs
	})
	easyMeshManagementReachable = func(ip string) bool { return ip == "10.10.18.236" }
	// The lease is only attributed once its holder identifies itself as this
	// chassis; the downstream main module does.
	easyMeshLeaseBelongsToChassis = func(string, string) bool { return true }
	topology := `{"topology information":[
		{"AL MAC":"f0:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"f0:a8:82:fb:00:f0","Distance from controller":"2","Upstream 1905 device":"f0:a8:82:fb:00:e4","BH Info":[{"Backhaul Medium Type":"5G","RSSI":"-75"}],"Other Clients Info":[{"Client Address":"1A:0B:70:99:DA:A6","Medium":"Ethernet"}],"Radio Info":[{"band":"5G","BSSINFO":[{"SSID":"Multi-AP-1","BACKHAUL":"1","FRONTHAUL":"0"},{"SSID":"Chase_Test_5G","BACKHAUL":"0","FRONTHAUL":"1"}]}]}
	]}`
	nodes := easyMeshRemoteNodesFromTopology(topology, map[string]string{
		"1a:0b:70:99:da:a6": "10.10.18.236",
	})
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v, want one wireless Agent", nodes)
	}
	node := nodes[0]
	if node.Name != "Mesh Agent 1" || node.ManagementIP != "10.10.18.236" || node.BackhaulMedium != "5G" || node.BackhaulRSSI != "-75" {
		t.Fatalf("remote node = %#v", node)
	}
	if !node.ManagementOnline || !node.FronthaulReady || len(node.FronthaulSSIDs) != 1 || node.FronthaulSSIDs[0] != "Chase_Test_5G" {
		t.Fatalf("remote node readiness = %#v", node)
	}
}

func TestEasyMeshRemoteNodesMapSeparateMainModuleHealthIdentity(t *testing.T) {
	topology := `{"topology information":[
		{"AL MAC":"f0:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"f0:a8:82:fb:00:f0","Distance from controller":"2","Upstream 1905 device":"f0:a8:82:fb:00:e4","BH Info":[{"neighbor almac addr":"f0:a8:82:fb:00:e4","Backhaul Medium Type":"5G","RSSI":"-51"},{"neighbor almac addr":"dc:84:03:01:57:80","Backhaul Medium Type":"Ethernet","RSSI":"NA"}]},
		{"AL MAC":"dc:84:03:01:57:80","Distance from controller":"3","Upstream 1905 device":"f0:a8:82:fb:00:f0","BH Info":[{"neighbor almac addr":"f0:a8:82:fb:00:f0","Backhaul Medium Type":"Ethernet","RSSI":"NA"}]}
	]}`
	nodes := easyMeshRemoteNodesFromTopologyWithManagement(topology, nil, map[string]easyMeshManagementNode{
		"dc:84:03:01:57:80": {IP: "10.10.18.236", NodeID: "4a:1e:a2:6c:ad:c2"},
	})
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v, want one complete Agent", nodes)
	}
	node := nodes[0]
	if node.ManagementIP != "10.10.18.236" || node.MainMAC != "4a:1e:a2:6c:ad:c2" || !node.ManagementOnline {
		t.Fatalf("separate main-module identity was not joined to management health: %#v", node)
	}
}

func TestEasyMeshMediumMatchesExactSelectedBand(t *testing.T) {
	if !easyMeshMediumMatchesBand("5G", "5g") || easyMeshMediumMatchesBand("2.4G", "5g") {
		t.Fatal("5GHz selection accepted the wrong Backhaul medium")
	}
	if !easyMeshMediumMatchesBand("2.4G", "2g") || easyMeshMediumMatchesBand("5G", "2g") {
		t.Fatal("2.4GHz selection accepted the wrong Backhaul medium")
	}
}

// The Mesh Radio layout must stay single-BSS (BssidNum=1) on every role. A
// reserved second BSS puts a third VIF on the shared Radio and starves the
// Backhaul STA's WPS scan, and Spatial Reuse corrupts the WPS M8 credential, so
// both must be forced off whenever the profile is prepared for Mesh.
func TestEasyMeshFronthaulProfilePinsSingleBSSAndDisablesSpatialReuse(t *testing.T) {
	content := "BssidNum=2\nSSID1=Multi-AP-1\nSSID2=Multi-AP-1\nWPAPSK1=maprocks1\nWPAPSK2=maprocks1\nSREnable=1\nSRMode=1\nAuthMode=WPA2PSKWPA3PSK\nEncrypType=AES\nHideSSID=0\n"
	updates := easyMeshFronthaulProfileUpdates(content, easyMeshFronthaulCredential{
		SSID:       "Chase_Test",
		Passphrase: "client-secret",
		AuthMode:   "WPA2PSKWPA3PSK",
		Encryption: "AES",
	}, true)
	updated := updateMtkDatContent(content, updates)
	for _, expected := range []string{
		"BssidNum=1",
		"SSID1=Chase_Test",
		"WPAPSK1=client-secret",
		"SSID2=",
		"WPAPSK2=",
		"SREnable=0",
		"SRMode=0",
		"AuthMode=WPA2PSKWPA3PSK",
		"EncrypType=AES",
	} {
		if !strings.Contains(updated, expected+"\n") {
			t.Fatalf("single-BSS profile does not contain %q: %s", expected, updated)
		}
	}
}

func TestEasyMeshTurnkeyRadioUpdatesDisableSpatialReuse(t *testing.T) {
	perBand := easyMeshTurnkeyRadioUpdates(false, "controller")
	if perBand["MapMode"] != "1" || perBand["SREnable"] != "0" || perBand["SRMode"] != "0" {
		t.Fatalf("per-band turnkey updates are wrong: %v", perBand)
	}
	// WPS must be enabled in the driver profile itself. The factory default
	// WscConfMode=0 lets the Backhaul STA find the registrar's PBC beacon but
	// silently prevents it from ever sending the association request.
	if perBand["WscConfMode"] != "7" || perBand["WscConfStatus"] != "2" {
		t.Fatalf("per-band profile does not enable WPS: %v", perBand)
	}
	// The card-level profile carries one list entry per band.
	card := easyMeshTurnkeyRadioUpdates(true, "controller")
	if card["MapMode"] != "1" || card["SREnable"] != "0;0" || card["SRMode"] != "0;0" {
		t.Fatalf("card-level turnkey updates are wrong: %v", card)
	}
	if card["WscConfMode"] != "7;7" || card["WscConfStatus"] != "2;2" {
		t.Fatalf("card-level profile does not enable WPS: %v", card)
	}
}

// The Prepare step owns the mandatory reboot, and WscConfMode only takes effect
// when the driver initialises at boot, so the Fronthaul profile written during
// Prepare must already carry it.
func TestEasyMeshFronthaulProfileEnablesWPSForTheRebootThatFollows(t *testing.T) {
	content := "BssidNum=1\nSSID1=x\nWPAPSK1=y\nWscConfMode=0\nWscConfStatus=1\nAuthMode=WPA2PSKWPA3PSK\nEncrypType=AES\n"
	updates := easyMeshFronthaulProfileUpdates(content, easyMeshFronthaulCredential{
		SSID: "Chase_Test", Passphrase: "client-secret", AuthMode: "WPA2PSKWPA3PSK", Encryption: "AES",
	}, true)
	updated := updateMtkDatContent(content, updates)
	for _, expected := range []string{"WscConfMode=7", "WscConfStatus=2"} {
		if !strings.Contains(updated, expected+"\n") {
			t.Fatalf("prepared profile does not contain %q: %s", expected, updated)
		}
	}
}

// mapd reads its own MapMode from the control-plane config, independently of the
// driver .dat files. Factory images ship without the key entirely.
func TestEasyMeshMapdUpdatesCarryControlPlaneMapMode(t *testing.T) {
	for _, role := range []string{"controller", "agent"} {
		if got := easyMeshExpectedMapdUpdates(role, "5g")["MapMode"]; got != "1" {
			t.Fatalf("%s mapd updates set MapMode=%q, want 1", role, got)
		}
	}
}

// The primary row of each band is the only BSS a BssidNum=1 radio instantiates,
// so it must carry the client credential AND both role flags ("bh_bss fh_bss" =
// "1 1"). The factory "1 0" makes it backhaul-only and every client is rejected
// with DEAUTH ReasonCode 37.
func TestEasyMeshBSSPolicyMakesPrimarySlotsCombinedRole(t *testing.T) {
	content := `#ucc_bss_info
1,ff:ff:ff:ff:ff:ff 11x Multi-AP-1 0x00E0 0x0008 maprocks1 1 0 hidden-N 4095 pvid 5
2,ff:ff:ff:ff:ff:ff 11x Multi-AP-5LG-2 0x0060 0x0008 maprocks2 0 1 hidden-N 4095 N/A N/A
5,ff:ff:ff:ff:ff:ff 8x Multi-AP-1 0x00E0 0x0008 maprocks1 1 0 hidden-N 4095 N/A N/A
6,ff:ff:ff:ff:ff:ff 8x Multi-AP-24G-2 0x0060 0x0008 maprocks2 0 1 hidden-N 4095 N/A N/A
9,ff:ff:ff:ff:ff:ff 12x Multi-AP-1 0x00E0 0x0008 maprocks1 1 0 hidden-N 4095 N/A N/A
10,ff:ff:ff:ff:ff:ff 12x Multi-AP-5HG-2 0x0060 0x0008 maprocks2 0 1 hidden-N 4095 N/A N/A
13,ff:ff:ff:ff:ff:ff 13x Multi-AP-1 0x00C0 0x0008 maprocks1 1 0 hidden-N 4095 N/A N/A
`
	updated, err := updateEasyMeshBSSPolicyContent(content, easyMeshFronthaulSet{
		TwoG:  easyMeshFronthaulCredential{SSID: "Chase_Test", Passphrase: "two-secret"},
		FiveG: easyMeshFronthaulCredential{SSID: "Chase_Test_5G", Passphrase: "five-secret"},
	})
	if err != nil {
		t.Fatalf("BSS policy update failed: %v", err)
	}
	for _, expected := range []string{
		// Primary rows: client credential, combined backhaul+fronthaul role.
		"1,ff:ff:ff:ff:ff:ff 11x Chase_Test_5G 0x00E0 0x0008 five-secret 1 1 hidden-N 4095 N/A N/A",
		"5,ff:ff:ff:ff:ff:ff 8x Chase_Test 0x00E0 0x0008 two-secret 1 1",
		"9,ff:ff:ff:ff:ff:ff 12x Chase_Test_5G 0x00E0 0x0008 five-secret 1 1",
		// Secondary rows stay fronthaul-only.
		"2,ff:ff:ff:ff:ff:ff 11x Chase_Test_5G 0x0060 0x0008 five-secret 0 1",
		"6,ff:ff:ff:ff:ff:ff 8x Chase_Test 0x0060 0x0008 two-secret 0 1",
		"10,ff:ff:ff:ff:ff:ff 12x Chase_Test_5G 0x0060 0x0008 five-secret 0 1",
		// The 6GHz band is untouched: this hardware has no 6GHz radio.
		"13,ff:ff:ff:ff:ff:ff 13x Multi-AP-1 0x00C0 0x0008 maprocks1 1 0",
	} {
		if !strings.Contains(updated, expected) {
			t.Fatalf("BSS policy does not contain %q: %s", expected, updated)
		}
	}
	if strings.Contains(updated, "Multi-AP-1 0x00E0") {
		t.Fatalf("a factory Multi-AP-1 primary row survived: %s", updated)
	}
}

func TestEasyMeshBSSPolicyRequiresPrimarySlots(t *testing.T) {
	// Only the secondary rows are present: the primary BSS would silently keep
	// the factory backhaul-only credential, so this must be rejected.
	content := `#ucc_bss_info
2,ff:ff:ff:ff:ff:ff 11x Multi-AP-5LG-2 0x0060 0x0008 maprocks2 0 1 hidden-N 4095 N/A N/A
6,ff:ff:ff:ff:ff:ff 8x Multi-AP-24G-2 0x0060 0x0008 maprocks2 0 1 hidden-N 4095 N/A N/A
10,ff:ff:ff:ff:ff:ff 12x Multi-AP-5HG-2 0x0060 0x0008 maprocks2 0 1 hidden-N 4095 N/A N/A
`
	if _, err := updateEasyMeshBSSPolicyContent(content, easyMeshFronthaulSet{
		TwoG:  easyMeshFronthaulCredential{SSID: "Chase_Test", Passphrase: "two-secret"},
		FiveG: easyMeshFronthaulCredential{SSID: "Chase_Test_5G", Passphrase: "five-secret"},
	}); err == nil {
		t.Fatal("a BSS policy without primary slots was accepted")
	}
}

func TestEasyMeshNetworkCommandsSeparatePlanesWithoutWiFiRestart(t *testing.T) {
	commands := []string{
		easyMeshPrepareACManagementCommand(),
		easyMeshPrepareAntennaManagementCommand(),
		easyMeshActivateAntennaTrunkCommand(),
		easyMeshActivateACClientVLANCommand(),
	}
	joined := strings.Join(commands, "\n")
	for _, expected := range []string{"172.31.255.1", ".vid=200", ".vid=100", AntennaManagementBridge, "eth1.100", "$p.100", "$p.200", "ports=ra0", "ports=rax0"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("network migration commands do not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"wifi restart", "network restart", "ifdown lan", "ifup lan"} {
		if strings.Contains(joined, forbidden) {
			t.Fatalf("network migration contains disruptive fallback %q", forbidden)
		}
	}
}

func TestEasyMeshManagementBridgeSortsAfterMainLAN(t *testing.T) {
	if AntennaManagementBridge <= "br-lan" {
		t.Fatalf("management bridge %q must sort after br-lan for the MediaTek script", AntennaManagementBridge)
	}
	command := easyMeshManagementBridgeMigrationCommand()
	for _, expected := range []string{
		"network.mesh_mgmt_bridge.name=" + AntennaManagementBridge,
		"network.antenna_mgmt.device=" + AntennaManagementBridge,
		"ubus call network reload",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("management bridge migration does not contain %q: %s", expected, command)
		}
	}
	if strings.Contains(command, "wifi restart") {
		t.Fatal("management bridge migration must never restart Wi-Fi")
	}
}

func TestEasyMeshMapdUserConfigHasNoBlankTerminator(t *testing.T) {
	content := "###!UserConfigs!!!\n\nExisting=keep\nDeviceRole=1\n\n"
	updated := updateEasyMeshMapdUserContent(content, map[string]string{
		"DeviceRole": "2",
		"DhcpCtl":    "0",
	})
	if strings.Contains(updated, "\n\n") {
		t.Fatalf("mapd user config contains an early blank terminator: %q", updated)
	}
	if !strings.HasPrefix(updated, "###!UserConfigs!!!\n") {
		t.Fatalf("mapd user header is missing: %q", updated)
	}
	for _, expected := range []string{"Existing=keep", "DeviceRole=2", "DhcpCtl=0"} {
		if !strings.Contains(updated, expected+"\n") {
			t.Fatalf("mapd user config does not contain %q: %q", expected, updated)
		}
	}
}

func TestEasyMeshPreparationKeepsLegacyClientLinkUntilManagementVerification(t *testing.T) {
	prepare := easyMeshPrepareAntennaManagementCommand()
	if strings.Contains(prepare, "delete network.@device[0].ports") {
		t.Fatal("preparation must not remove the legacy untagged Antenna link")
	}
	activate := easyMeshActivateAntennaTrunkCommand()
	if !strings.Contains(activate, "delete network.@device[0].ports") {
		t.Fatal("client trunk activation must replace the legacy bridge port")
	}
}

func TestEasyMeshOptionalUCIDeletesAreIdempotentUnderSetE(t *testing.T) {
	commands := []string{
		easyMeshPrepareACManagementCommand(),
		easyMeshPrepareAntennaManagementCommand(),
		easyMeshActivateAntennaTrunkCommand(),
		easyMeshActivateACClientVLANCommand(),
	}
	for _, command := range commands {
		if !strings.Contains(command, "set -e") {
			t.Fatal("migration command must still fail closed on required operations")
		}
		for _, segment := range strings.Split(command, ";") {
			segment = strings.TrimSpace(segment)
			if strings.HasPrefix(segment, "uci -q delete network.mesh_") && !strings.Contains(segment, "|| true") {
				t.Fatalf("optional Mesh UCI delete is not idempotent: %q", segment)
			}
			if strings.HasPrefix(segment, "uci -q delete network.antenna_mgmt") && !strings.Contains(segment, "|| true") {
				t.Fatalf("optional management UCI delete is not idempotent: %q", segment)
			}
		}
	}
}

func TestEasyMeshInterruptedMigrationPrefersVerifiedManagementAddress(t *testing.T) {
	discovered := []ManagedModule{{ModuleID: "antenna:3", PortIndex: 3, IP: "10.10.18.4"}}
	remembered := []ManagedModule{{ModuleID: "antenna:3", PortIndex: 3, IP: "172.31.255.4"}}
	merged := mergeEasyMeshModules(discovered, remembered)
	if len(merged) != 1 || merged[0].IP != "172.31.255.4" {
		t.Fatalf("verified management address was not preferred: %#v", merged)
	}
}

func TestEasyMeshRemoteRecoveryRetriesTransientFailure(t *testing.T) {
	original := easyMeshExecRemote
	t.Cleanup(func() { easyMeshExecRemote = original })
	attempts := 0
	easyMeshExecRemote = func(_ string, _ string) (string, error) {
		attempts++
		if attempts == 1 {
			return "", errors.New("temporarily unavailable")
		}
		return "", nil
	}
	if err := waitForEasyMeshRemoteReachable("172.31.255.4", 2*time.Second); err != nil {
		t.Fatalf("transient management outage was not retried: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
}

func TestEasyMeshBackhaulDiscoveryRetriesTransientOfflineState(t *testing.T) {
	attempts := 0
	discover := func() []ManagedModule {
		attempts++
		if attempts < 3 {
			return nil
		}
		return []ManagedModule{{
			ModuleID:  "antenna:2",
			Port:      "lan2",
			PortIndex: 2,
			IP:        "172.31.255.4",
			MAC:       "82:28:19:E3:DD:1F",
		}}
	}

	backhaul, err := waitForEasyMeshBackhaulModuleWithDiscovery(
		"antenna:2",
		100*time.Millisecond,
		time.Millisecond,
		discover,
	)
	if err != nil {
		t.Fatalf("transient Antenna discovery miss was not retried: %v", err)
	}
	if attempts != 3 {
		t.Fatalf("discovery attempts = %d, want 3", attempts)
	}
	if backhaul.ModuleID != "antenna:2" || backhaul.IP != "172.31.255.4" {
		t.Fatalf("resolved backhaul = %#v", backhaul)
	}
}

func TestEasyMeshBackhaulDiscoveryTimesOut(t *testing.T) {
	_, err := waitForEasyMeshBackhaulModuleWithDiscovery(
		"antenna:2",
		3*time.Millisecond,
		time.Millisecond,
		func() []ManagedModule { return nil },
	)
	if err == nil || !strings.Contains(err.Error(), "did not become reachable") {
		t.Fatalf("timeout error = %v", err)
	}
}

func TestEasyMeshRemoteConfigUsesExecReadAndVerifiesWrite(t *testing.T) {
	originalExec := easyMeshExecRemote
	originalWrite := easyMeshWriteRemoteFile
	t.Cleanup(func() {
		easyMeshExecRemote = originalExec
		easyMeshWriteRemoteFile = originalWrite
	})

	remoteContent := "MapMode=0\nSSID=Roobuck\n"
	readCount := 0
	easyMeshExecRemote = func(ip string, command string) (string, error) {
		if ip != "172.31.255.4" || command != "cat '/etc/wireless/mt7615/mt7615.1.dat'" {
			t.Fatalf("unexpected remote read: ip=%q command=%q", ip, command)
		}
		readCount++
		return remoteContent, nil
	}
	easyMeshWriteRemoteFile = func(ip string, path string, content string) error {
		if ip != "172.31.255.4" || path != "/etc/wireless/mt7615/mt7615.1.dat" {
			t.Fatalf("unexpected remote write: ip=%q path=%q", ip, path)
		}
		remoteContent = content
		return nil
	}

	if err := updateRemoteEasyMeshConfigFile(
		"172.31.255.4",
		"/etc/wireless/mt7615/mt7615.1.dat",
		map[string]string{"MapMode": "1"},
	); err != nil {
		t.Fatalf("remote EasyMesh config update failed: %v", err)
	}
	if readCount != 2 {
		t.Fatalf("remote read count = %d, want 2", readCount)
	}
	if err := verifyEasyMeshConfigUpdates(remoteContent, map[string]string{"MapMode": "1"}); err != nil {
		t.Fatalf("written config was not updated: %v", err)
	}
}

func TestEasyMeshRemoteConfigRejectsUnverifiedWrite(t *testing.T) {
	originalExec := easyMeshExecRemote
	originalWrite := easyMeshWriteRemoteFile
	t.Cleanup(func() {
		easyMeshExecRemote = originalExec
		easyMeshWriteRemoteFile = originalWrite
	})

	readCount := 0
	easyMeshExecRemote = func(_ string, _ string) (string, error) {
		readCount++
		return "MapMode=0\n", nil
	}
	easyMeshWriteRemoteFile = func(_ string, _ string, _ string) error { return nil }

	err := updateRemoteEasyMeshConfigFile("172.31.255.4", Mtk5GPath, map[string]string{"MapMode": "1"})
	if err == nil || !strings.Contains(err.Error(), "MapMode") {
		t.Fatalf("unverified write error = %v", err)
	}
	if readCount != 2 {
		t.Fatalf("remote read count = %d, want 2", readCount)
	}
}

func TestEasyMeshStartUsesTurnkeyWithoutWiFiRestart(t *testing.T) {
	controller := easyMeshStartCommand("controller")
	agent := easyMeshStartCommand("agent")
	if !strings.Contains(controller, "EasyMesh_openwrt.sh lrap 0") {
		t.Fatalf("controller command = %q", controller)
	}
	if !strings.Contains(agent, "EasyMesh_openwrt.sh lrap 1") {
		t.Fatalf("agent command = %q", agent)
	}
	if strings.Contains(controller+agent, "wifi restart") {
		t.Fatal("EasyMesh activation must never call wifi restart")
	}
	for _, command := range []string{controller, agent} {
		for _, expected := range []string{
			"sleep 5",
			"echo scheduled",
			"/sbin/start-stop-daemon -S -b",
			easyMeshStartReadyPath,
			easyMeshStartFailedPath,
			easyMeshStartPIDPath,
		} {
			if !strings.Contains(command, expected) {
				t.Fatalf("delayed verified start command does not contain %q: %s", expected, command)
			}
		}
	}
}

func TestEasyMeshRadioPreparationUsesExplicitRebootWithoutWiFiRestart(t *testing.T) {
	command := easyMeshDetachedRebootCommand(8)
	for _, expected := range []string{"sleep 8", "/sbin/reboot", "/sbin/start-stop-daemon -S -b"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("Radio preparation reboot does not contain %q: %s", expected, command)
		}
	}
	if strings.Contains(command, "wifi restart") {
		t.Fatalf("Radio preparation must never use wifi restart: %s", command)
	}
	// Antennas are driven through ubus file.exec, which runs commands as literal
	// `/bin/sh -c ...`. Without a pidfile, start-stop-daemon matches that very
	// shell via -x and refuses with "/bin/sh is already running" (exit 1), which
	// aborts Prepare before either unit reboots.
	if !strings.Contains(command, "-m -p "+easyMeshRebootPID) {
		t.Fatalf("detached reboot must select by pidfile, not by executable match: %s", command)
	}
	if !strings.Contains(command, "rm -f "+easyMeshRebootPID) {
		t.Fatalf("detached reboot must clear a stale pidfile first: %s", command)
	}
}

func TestEasyMeshRemoteStartDispatchUsesRuntimeVerificationForAmbiguousResponse(t *testing.T) {
	original := easyMeshExecRemote
	t.Cleanup(func() { easyMeshExecRemote = original })

	for _, dispatchError := range []error{
		errors.New("ubus bad result body="),
		errors.New("Post http://172.31.255.4/ubus: context deadline exceeded"),
		errors.New("unexpected EOF"),
		errors.New("read tcp: connection reset by peer"),
	} {
		easyMeshExecRemote = func(_ string, command string) (string, error) {
			if !strings.Contains(command, "EasyMesh_openwrt.sh lrap 1") {
				t.Fatalf("unexpected start command: %s", command)
			}
			return "", dispatchError
		}
		if err := dispatchRemoteEasyMeshStart("172.31.255.4", "agent"); err != nil {
			t.Fatalf("ambiguous dispatch response %q was treated as a definite failure: %v", dispatchError, err)
		}
	}
}

func TestEasyMeshRemoteStartDispatchRejectsDefiniteUbusFailure(t *testing.T) {
	original := easyMeshExecRemote
	t.Cleanup(func() { easyMeshExecRemote = original })

	easyMeshExecRemote = func(_ string, _ string) (string, error) {
		return "", errors.New("ubus code=6 body={}")
	}
	if err := dispatchRemoteEasyMeshStart("172.31.255.4", "agent"); err == nil {
		t.Fatal("definite ubus failure was accepted")
	}
}

func TestEasyMeshRemoteEngineRequiresSocketAndProcess(t *testing.T) {
	original := easyMeshExecRemote
	t.Cleanup(func() { easyMeshExecRemote = original })

	var command string
	easyMeshExecRemote = func(_ string, value string) (string, error) {
		command = value
		return "", nil
	}
	if !easyMeshRemoteEngineRunning("172.31.255.4") {
		t.Fatal("healthy remote engine was not accepted")
	}
	for _, expected := range []string{"test -S " + easyMeshMapdSocket, "pidof mapd"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("remote engine check does not contain %q: %s", expected, command)
		}
	}

	easyMeshExecRemote = func(_ string, _ string) (string, error) {
		return "", errors.New("not running")
	}
	if easyMeshRemoteEngineRunning("172.31.255.4") {
		t.Fatal("failed remote engine was accepted")
	}
}

func TestEasyMeshBackhaulConnectionStatusParsing(t *testing.T) {
	connected := []string{
		"backhaul connection status 1",
		"connected",
	}
	for _, raw := range connected {
		if !easyMeshBackhaulConnected(raw) {
			t.Fatalf("expected connected status for %q", raw)
		}
	}
	disconnected := []string{
		"",
		"backhaul connection status 0",
		"backhaul connection status 2",
		"not connected",
		"disconnected",
	}
	for _, raw := range disconnected {
		if easyMeshBackhaulConnected(raw) {
			t.Fatalf("expected disconnected status for %q", raw)
		}
	}
}

func TestEasyMeshWirelessBackhaulAssociationParsing(t *testing.T) {
	connected := "apclix0  IEEE 802.11ax  ESSID:\"mesh\"  Access Point: 12:34:56:78:9A:BC"
	if !easyMeshWirelessBackhaulAssociated(connected) {
		t.Fatal("a non-zero associated BSSID must be accepted")
	}
	for _, raw := range []string{
		"apclix0  Access Point: Not-Associated",
		"apclix0  Access Point: 00:00:00:00:00:00",
		"apclix0  ESSID:off/any",
	} {
		if easyMeshWirelessBackhaulAssociated(raw) {
			t.Fatalf("unassociated wireless status was accepted: %q", raw)
		}
	}
}

// At BssidNum=1 the single Fronthaul BSS is also the Backhaul AP, so the WPS
// registrar must open on ra0/rax0. There is no ra1/rax1 on this layout.
func TestEasyMeshOnboardingTargetsCombinedFronthaulBSS(t *testing.T) {
	if got := easyMeshBackhaulBSSInterface("2g"); got != "ra0" {
		t.Fatalf("2.4GHz onboarding targeted %q instead of ra0", got)
	}
	if got := easyMeshBackhaulBSSInterface("5g"); got != "rax0" {
		t.Fatalf("5GHz onboarding targeted %q instead of rax0", got)
	}
	if got := easyMeshRadioReadyInterfaces(); len(got) != 2 || got[0] != "ra0" || got[1] != "rax0" {
		t.Fatalf("radio readiness checked %v instead of the base Fronthaul BSSes", got)
	}
}

// ApCliEnable=1 must not be part of the enrollee trigger: the vendor helper has
// it commented out and the sequence below is the verified-working one.
func TestEasyMeshBackhaulWPSCommandsMatchVendorSequence(t *testing.T) {
	registrar := easyMeshBackhaulWPSRegistrarCommand("5g")
	for _, expected := range []string{"iwpriv rax0 set WscConfMode=4", "WscMode=2", "WscConfStatus=2", "WscGetConf=1"} {
		if !strings.Contains(registrar, expected) {
			t.Fatalf("registrar command missing %q: %s", expected, registrar)
		}
	}
	// Exactly one PBC window may be open at a time: two PBC beacons with different
	// UUID-E make the enrollee declare a session overlap and refuse to associate.
	if easyMeshOtherBand("5g") != "2g" || easyMeshOtherBand("2g") != "5g" {
		t.Fatal("easyMeshOtherBand must return the unselected band")
	}
	if got := easyMeshBackhaulWPSStopCommand("2g"); got != "iwpriv ra0 set WscStop=1" {
		t.Fatalf("2.4GHz WPS stop command is wrong: %s", got)
	}
	if got := easyMeshBackhaulWPSStopCommand("5g"); got != "iwpriv rax0 set WscStop=1" {
		t.Fatalf("5GHz WPS stop command is wrong: %s", got)
	}
	enrollee := easyMeshBackhaulWPSEnrolleeCommand("5g")
	if strings.Contains(enrollee, "ApCliEnable") {
		t.Fatalf("enrollee command must not set ApCliEnable: %s", enrollee)
	}
	for _, expected := range []string{"iwpriv apclix0 set WscConfMode=1", "WscMode=2", "WscGetConf=1"} {
		if !strings.Contains(enrollee, expected) {
			t.Fatalf("enrollee command missing %q: %s", expected, enrollee)
		}
	}
}

func TestControllerOnboardingWindowTimesOutWhenNoAgentJoins(t *testing.T) {
	err := maintainControllerOnboardingWindow("5g", nil, 0)
	if err == nil {
		t.Fatal("an expired Controller onboarding window without a new Agent must fail")
	}
	if !strings.Contains(err.Error(), "no Mesh Agent joined") {
		t.Fatalf("unexpected Controller onboarding timeout error: %v", err)
	}
}

// The handoff must not touch the client bridge. Rebuilding br-lan detaches the
// radios and the Backhaul station — the very path the DHCP exchange travels — so
// the Root's OFFER never comes back and the handoff fails with
// "no-root-dhcp-lease" on a perfectly healthy Mesh. The address is acquired
// against the live bridge instead.
func TestEasyMeshAgentHandoffDoesNotRebuildTheClientBridge(t *testing.T) {
	command := easyMeshScheduleAgentHandoffCommand(15)
	for _, expected := range []string{"sleep 15", "dhcp.lan.ignore=1", "network.lan.proto=dhcp", "network.lan.hostname=RoobuckAC", "udhcpc -i br-lan", "start-stop-daemon", easyMeshHandoffReady} {
		if !strings.Contains(command, expected) {
			t.Fatalf("Agent handoff command does not contain %q: %s", expected, command)
		}
	}
	for _, forbidden := range []string{"wifi restart", "network restart", "ifdown lan", "ifup lan", "ubus call network reload"} {
		if strings.Contains(command, forbidden) {
			t.Fatalf("Agent handoff contains disruptive fallback %q", forbidden)
		}
	}
}

func TestEasyMeshAgentFailureRestoresStandaloneUntilHandoffIsVerified(t *testing.T) {
	rejoining := EasyMeshConfig{Role: "agent", AgentHandoff: true}
	if !easyMeshOnboardingFailureNeedsRollback(rejoining, false) {
		t.Fatal("an unverified Agent retry must recover to a manageable standalone network")
	}

	firstJoin := EasyMeshConfig{Role: "agent", AgentHandoff: false}
	if !easyMeshOnboardingFailureNeedsRollback(firstJoin, false) {
		t.Fatal("a first-time Agent pairing failure should still restore the standalone snapshot")
	}
	if easyMeshOnboardingFailureNeedsRollback(firstJoin, true) {
		t.Fatal("a fully verified handoff must not be rolled back")
	}

	establishedRoot := EasyMeshConfig{Role: "controller", Enabled: true}
	if easyMeshOnboardingFailureNeedsRollback(establishedRoot, false) {
		t.Fatal("an onboarding-window failure must never restore an established Root")
	}
}

func TestEasyMeshStandaloneRestoreAvoidsNetworkAndWiFiRestart(t *testing.T) {
	commands := easyMeshRemoteRestoreCommand(true) + "\n" + easyMeshLocalRestoreCommand(true)
	for _, expected := range []string{
		"mesh-backup/network",
		"mesh-backup/dhcp",
		"mesh-backup/wifi-2g.dat",
		"mesh-backup/wifi-5g.dat",
		"mesh-backup/mapd_user.cfg",
		"mesh-backup/1905d.cfg",
		"ubus call network reload",
		"/sbin/wifi reload",
		"iwpriv",
		"EasyMesh_openwrt.sh",
		easyMeshRestoreReady,
		"/bin/sh -c",
		"code=0",
		"-m -p " + easyMeshRestorePID,
		"sleep 8",
	} {
		if !strings.Contains(commands, expected) {
			t.Fatalf("standalone restore does not contain %q", expected)
		}
	}
	for _, forbidden := range []string{"wifi restart", "network restart", "ifdown lan", "ifup lan"} {
		if strings.Contains(commands, forbidden) {
			t.Fatalf("standalone restore contains disruptive fallback %q", forbidden)
		}
	}
	for _, tolerated := range []string{
		"EasyMesh_openwrt.sh lrap 0 >/tmp/lrap-easymesh-leave.log 2>&1 || true",
		"/sbin/wifi reload >/tmp/lrap-wifi-restore.log 2>&1 || true",
	} {
		if !strings.Contains(commands, tolerated) {
			t.Fatalf("standalone restore does not tolerate asynchronous MediaTek result %q", tolerated)
		}
	}
}

func TestMediaTekWiFiServicesCompatibilityGuardsRemovedBSS(t *testing.T) {
	original := "function miniupnpd_chk(devname,vif,enable)\n" + easyMeshWiFiServicesNeedle + "\nend\n"
	patched, err := patchMediaTekWiFiServicesContent(original)
	if err != nil {
		t.Fatalf("patch wifi_services.lua: %v", err)
	}
	for _, expected := range []string{
		`local lrap_dev = devs[devname]`,
		`lrap_dev["vifs"][vif] == nil`,
		`local ssid_index = lrap_dev["vifs"][vif].vifidx`,
	} {
		if !strings.Contains(patched, expected) {
			t.Fatalf("compatibility guard does not contain %q: %s", expected, patched)
		}
	}
	again, err := patchMediaTekWiFiServicesContent(patched)
	if err != nil || again != patched {
		t.Fatalf("compatibility patch must be idempotent: err=%v", err)
	}
}

// EasyMesh_openwrt.sh regenerates several 1905d.cfg fields from the current
// hardware MAC on every Turnkey reset, so that file is excluded from the byte
// comparison entirely; the authoritative mesh config is covered by the other
// files. Optional snapshot files are excluded too, because an older snapshot
// directory legitimately does not contain them.
func TestEasyMeshRestoreVerificationSkipsRegeneratedAndOptionalFiles(t *testing.T) {
	command := easyMeshVerifyRestoreCommand()
	for _, skipped := range []string{"1905d.cfg", "wifi-card0.dat"} {
		if strings.Contains(command, "/etc/roobuck/mesh-backup/"+skipped) {
			t.Fatalf("%s must not fail an otherwise successful restore: %s", skipped, command)
		}
	}
	for _, stableFile := range []string{"network", "dhcp", "wifi-2g.dat", "wifi-5g.dat", "mapd_user.cfg", "mapd_default.cfg", "wts_bss_info_config"} {
		if !strings.Contains(command, "cmp -s '/etc/roobuck/mesh-backup/"+stableFile+"'") {
			t.Fatalf("stable snapshot file %s is no longer verified exactly: %s", stableFile, command)
		}
	}
}

func TestEasyMeshSnapshotIsFreshAndComplete(t *testing.T) {
	command := easyMeshSnapshotCommand()
	for _, file := range easyMeshSnapshotFiles {
		if !strings.Contains(command, file.Source) || !strings.Contains(command, file.Name) {
			t.Fatalf("snapshot does not include %s as %s: %s", file.Source, file.Name, command)
		}
	}
	for _, expected := range []string{`${base}.new`, `${base}.old`, "rm -rf", "mv", "complete"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("fresh snapshot transaction does not contain %q: %s", expected, command)
		}
	}
	if strings.Contains(command, "test -f /etc/roobuck/mesh-backup/network ||") {
		t.Fatal("snapshot must replace stale backups instead of reusing them")
	}
}

func TestEasyMeshRestoreVerificationChecksPersistentAndRuntimeState(t *testing.T) {
	command := easyMeshVerifyRestoreCommand()
	for _, expected := range []string{"cmp -s", "iwconfig", "ESSID", easyMeshRestoreReady, "pidof mapd"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("restore verification does not contain %q: %s", expected, command)
		}
	}
}

// The Backhaul now runs on the main module's own Radio. Antennas are plain
// client APs, so nothing in the onboarding path may touch them.
func TestOnboardingDrivesTheLocalRadioOnly(t *testing.T) {
	originalLocal := easyMeshExecLocal
	originalRemote := easyMeshExecRemote
	t.Cleanup(func() {
		easyMeshExecLocal = originalLocal
		easyMeshExecRemote = originalRemote
	})

	easyMeshExecRemote = func(ip string, command string) (string, error) {
		t.Fatalf("onboarding must not reach an Antenna: %s -> %s", ip, command)
		return "", nil
	}
	stopped := false
	armed := false
	easyMeshExecLocal = func(command string) (string, error) {
		switch {
		case strings.Contains(command, "trigger_map_wps"):
			// mapd's trigger opens PBC on BOTH bands, which makes the enrollee
			// latch onto the wrong one. It must never be used.
			t.Fatalf("trigger_map_wps must not be used: %s", command)
		case strings.Contains(command, "WscStop=1"):
			if !strings.Contains(command, "iwpriv ra0 ") {
				t.Fatalf("5GHz onboarding must close the 2.4GHz WPS window: %s", command)
			}
			stopped = true
		case strings.Contains(command, "WscConfMode=4"):
			if !strings.Contains(command, "iwpriv rax0 ") {
				t.Fatalf("5GHz registrar must arm rax0: %s", command)
			}
			if !stopped {
				t.Fatal("the registrar was armed before the other band was closed")
			}
			armed = true
		}
		return "", nil
	}

	if err := openControllerOnboardingWindow("5g"); err != nil {
		t.Fatalf("opening the local onboarding window failed: %v", err)
	}
	if !armed {
		t.Fatal("the WPS registrar was never armed on the local Radio")
	}
}

// DBDC_card0.dat wins over the per-band profiles for the on-air SSID, so it must
// be written with the same credential or the radios keep broadcasting a stale
// name and the WPS M8 credential is built from mismatched sources.
func TestCardProfileCarriesTheSameCredentialAsPerBand(t *testing.T) {
	content := "SSID1=old2g\nWPAPSK1=oldpass\nSSID2=old5g\nWPAPSK2=oldpass\n"
	updated := updateMtkDatContent(content, map[string]string{
		"SSID1": "Chase_Test", "WPAPSK1": "secret",
		"SSID2": "Chase_Test_5G", "WPAPSK2": "secret",
	})
	for _, expected := range []string{"SSID1=Chase_Test", "SSID2=Chase_Test_5G", "WPAPSK1=secret", "WPAPSK2=secret"} {
		if !strings.Contains(updated, expected+"\n") {
			t.Fatalf("card profile does not contain %q: %s", expected, updated)
		}
	}
}

// MAP Traffic Separation VLAN-tags Fronthaul traffic, which this product's
// non-filtering bridge silently drops. Every Turnkey start must switch it off.
func TestEasyMeshStartDisablesMapTrafficSeparation(t *testing.T) {
	command := easyMeshStartCommandWithReload("agent", true)
	for _, expected := range []string{
		"iwpriv " + Mtk24GIfName + " set mapTSEnable=0",
		"iwpriv " + Mtk5GIfName + " set mapTSEnable=0",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("start command does not disable MAP TS (%q): %s", expected, command)
		}
	}
}

// The chassis carries lan1..lan5, but an earlier revision hardcoded lan1..lan4
// in both VLAN builders. That silently dropped lan5 from the rebuilt client
// bridge on every Enable Mesh, so anything plugged into it went dark until a
// Leave restored the snapshot. Both planes must derive the port list from the
// live br-lan configuration instead.
func TestEasyMeshVLANMigrationDerivesLANPortsFromTheBridge(t *testing.T) {
	for name, command := range map[string]string{
		"management": easyMeshPrepareACManagementCommand(),
		"client":     easyMeshActivateACClientVLANCommand(),
	} {
		if !strings.Contains(command, "uci -q get network.@device[0].ports") {
			t.Fatalf("%s migration does not read the live br-lan port list: %s", name, command)
		}
		for i := 1; i <= 5; i++ {
			hardcoded := fmt.Sprintf("ifname=lan%d", i)
			if strings.Contains(command, hardcoded) {
				t.Fatalf("%s migration still hardcodes %q", name, hardcoded)
			}
		}
	}
}

// An AC with no Antennas attached is a valid Mesh node: the Backhaul rides the
// chassis' own Radio. Preparation must not stall waiting for management leases
// that can never arrive, so an empty module list has to satisfy the wait
// immediately rather than burn the whole timeout and fail the migration.
func TestManagementLeaseWaitSucceedsWithNoAntennas(t *testing.T) {
	start := time.Now()
	leases, err := waitForEasyMeshManagementLeases(nil, 30*time.Second)
	if err != nil {
		t.Fatalf("a chassis with no Antennas failed the management lease wait: %v", err)
	}
	if len(leases) != 0 {
		t.Fatalf("unexpected leases for a chassis with no Antennas: %#v", leases)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the empty wait blocked for %v instead of returning immediately", elapsed)
	}
}

// Silencing the unselected band must close only its WPS session. An ApCliEnable
// write lands on BOTH stations of the card, so disabling the unselected band
// also took the selected one down and the enrollee never scanned.
func TestBackhaulSTASilenceNeverDisablesTheStation(t *testing.T) {
	for _, band := range []string{"2g", "5g"} {
		command := easyMeshBackhaulSTASilenceCommand(band)
		if !strings.Contains(command, "WscStop=1") {
			t.Fatalf("%s WPS session is not closed: %s", band, command)
		}
		if strings.Contains(command, "ApCliEnable") {
			t.Fatalf("%s silence must not touch ApCliEnable: %s", band, command)
		}
	}
}

// The Agent's Backhaul pairing must be driven by the WPS enrollee sequence
// alone. `mapd_cli onboarding 1` makes mapd bring the station up in a tight
// join loop with an empty SSID and reset both stations of the card every few
// seconds, which starves the WPS scan and tears down a PBC session that has
// already picked its Root. The enrollee sequence on its own is what pairs.
func TestAgentOnboardingDoesNotUseMapdOnboarding(t *testing.T) {
	source, err := os.ReadFile("easymesh.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	if strings.Contains(string(source), `" onboarding 1"`) {
		t.Fatal("the Agent path still issues `mapd_cli onboarding 1`")
	}
}

// The vendor recipe writes SSID/WPAPSK only on the Controller — an Agent's
// client credential comes down from the Root after the Backhaul is up.
func TestAgentRadioProfileCarriesNoLocalCredential(t *testing.T) {
	content := "BssidNum=1\nSSID1=Factory\nWPAPSK1=factorypsk\nWscConfMode=0\nAuthMode=WPA2PSKWPA3PSK\nEncrypType=AES\n"
	credential := easyMeshFronthaulCredential{SSID: "Chase_Test_2", Passphrase: "client-secret"}

	agent := easyMeshFronthaulProfileUpdates(content, credential, false)
	if _, present := agent["SSID1"]; present {
		t.Fatalf("the Agent profile must not carry a local SSID: %#v", agent)
	}
	if _, present := agent["WPAPSK1"]; present {
		t.Fatalf("the Agent profile must not carry a local passphrase: %#v", agent)
	}
	// The layout and WPS keys still have to be applied on the Agent.
	if agent["BssidNum"] != "1" || agent["WscConfMode"] != "7" || agent["WscConfStatus"] != "2" {
		t.Fatalf("the Agent profile lost its single-BSS/WPS layout: %#v", agent)
	}

	controller := easyMeshFronthaulProfileUpdates(content, credential, true)
	if controller["SSID1"] != "Chase_Test_2" || controller["WPAPSK1"] != "client-secret" {
		t.Fatalf("the Controller profile must carry the credential: %#v", controller)
	}
}

// Card-level WPS keys are Controller-only in the vendor recipe.
func TestCardProfileWPSKeysAreControllerOnly(t *testing.T) {
	agent := easyMeshTurnkeyRadioUpdates(true, "agent")
	if _, present := agent["WscConfMode"]; present {
		t.Fatalf("the Agent card profile must not carry WPS keys: %#v", agent)
	}
	if agent["MapMode"] != "1" || agent["SREnable"] != "0;0" || agent["SRMode"] != "0;0" {
		t.Fatalf("the Agent card profile lost its turnkey keys: %#v", agent)
	}
	if controller := easyMeshTurnkeyRadioUpdates(true, "controller"); controller["WscConfMode"] != "7;7" || controller["WscConfStatus"] != "2;2" {
		t.Fatalf("the Controller card profile lost its WPS keys: %#v", controller)
	}
}

// A Mesh needs ONE SSID. wapp runs concurrent WPS: both Backhaul stations of a
// card hunt for a registrar at the same time, and with a different SSID per band
// they find two different registrars and restart forever without ever entering
// JOIN. Both Radios must carry the same identity.
func TestMeshCredentialIsUnifiedAcrossBands(t *testing.T) {
	set := easyMeshUnifiedFronthaulSet(
		easyMeshFronthaulCredential{SSID: "Chase_Test", Passphrase: "client-secret", AuthMode: "WPA2PSK", Encryption: "AES"},
		easyMeshFronthaulCredential{SSID: "Chase_Test_5G", Passphrase: "five-secret", AuthMode: "WPA2PSKWPA3PSK", Encryption: "AES"},
	)
	if set.TwoG.SSID != "Chase_Test" || set.FiveG.SSID != "Chase_Test" {
		t.Fatalf("the Mesh SSID is not unified: %#v", set)
	}
	if set.TwoG.Passphrase != "client-secret" || set.FiveG.Passphrase != "client-secret" {
		t.Fatalf("the Mesh passphrase is not unified: %#v", set)
	}
	// Per-Radio security settings stay untouched; only the identity is shared.
	if set.FiveG.AuthMode != "WPA2PSKWPA3PSK" {
		t.Fatalf("the 5GHz security settings were overwritten: %#v", set)
	}

	// An empty 2.4GHz profile falls back to the 5GHz one rather than yielding a
	// nameless Mesh.
	fallback := easyMeshUnifiedFronthaulSet(
		easyMeshFronthaulCredential{SSID: "  "},
		easyMeshFronthaulCredential{SSID: "Only_5G", Passphrase: "five-secret"},
	)
	if fallback.TwoG.SSID != "Only_5G" || fallback.FiveG.SSID != "Only_5G" {
		t.Fatalf("empty 2.4GHz profile did not fall back: %#v", fallback)
	}
}

// map_ver=R3 makes the Root emit a 271-byte M8 credential this firmware's
// enrollee cannot parse ("unexpected WSC IE Length"), so onboarding completes
// M1..M8 and is then deauthenticated. R2 is what pairs.
func TestMultiAPVersionIsPinnedToR2(t *testing.T) {
	if version := easyMesh1905Updates()["map_ver"]; version != "R2" {
		t.Fatalf("map_ver = %q, want R2", version)
	}
	if easyMesh1905Path != "/etc/map/1905d.cfg" {
		t.Fatalf("unexpected 1905 config path %q", easyMesh1905Path)
	}
	// A unit still on R3 must be reported as out of sync so it gets reconciled.
	if verifyEasyMeshConfigUpdates("map_ver=R3\n", easyMesh1905Updates()) == nil {
		t.Fatal("an R3 unit was accepted as already configured")
	}
	if verifyEasyMeshConfigUpdates("map_ver=R2\n", easyMesh1905Updates()) != nil {
		t.Fatal("an R2 unit was reported as needing reconciliation")
	}
}

// The Backhaul station and the local AP share one DBDC Radio, so they cannot sit
// on different channels. When the two chassis are configured on different
// channels the scan finds the Root and logs "will connect ... on <ch>", the
// Radio snaps back to its own channel, and the pairing fails silently — the Root
// never sees a single WSC frame. The Agent has to follow the Root's channel.
func TestBackhaulChannelIsReadFromTheDriversOwnSelection(t *testing.T) {
	log := strings.Join([]string{
		"[  252.929479] will connect Chase_Test (f2:a8:82:fb:00:a4) on 36",
		"[  313.400750] SCAN DONE, Reset FSM/CNTL IDLE.",
		"[  373.801477] will connect Chase_Test (f2:a8:82:fb:00:a4) on 48",
	}, "\n")
	channel, ok := easyMeshBackhaulChannelFromScan(log)
	if !ok {
		t.Fatal("the Root's channel was not parsed")
	}
	// The most recent scan wins — the Root may have moved.
	if channel != 48 {
		t.Fatalf("channel = %d, want 48", channel)
	}

	if _, ok := easyMeshBackhaulChannelFromScan("SCAN DONE, Reset FSM/CNTL IDLE.\n"); ok {
		t.Fatal("a log with no selection reported a channel")
	}
	if _, ok := easyMeshBackhaulChannelFromScan("will connect Chase_Test (f2:a8:82:fb:00:a4) on notanumber"); ok {
		t.Fatal("a malformed channel was accepted")
	}
}

// iwpriv alone does not hold the channel on this firmware, so the alignment must
// write the driver profile and reload.
func TestRadioChannelIsPinnedInTheProfileNotJustAtRuntime(t *testing.T) {
	command := easyMeshSetRadioChannelCommand("5g", 48)
	for _, expected := range []string{"Channel=48", Mtk5GPath, "AutoChannelSelect=0", "wifi reload"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("channel command does not contain %q: %s", expected, command)
		}
	}
	if twoG := easyMeshSetRadioChannelCommand("2g", 6); !strings.Contains(twoG, Mtk24GPath) {
		t.Fatalf("2.4GHz alignment targets the wrong profile: %s", twoG)
	}
	// The probe must close its own WPS session so it cannot collide with the real
	// enrollee that follows.
	probe := easyMeshProbeRootChannelCommand("5g")
	for _, expected := range []string{"apclix0 set WscGetConf=1", "apclix0 set WscStop=1", "will connect"} {
		if !strings.Contains(probe, expected) {
			t.Fatalf("channel probe does not contain %q: %s", expected, probe)
		}
	}
}

// Re-arming the registrar tears down whatever is already associated to the
// Backhaul BSS, and the 1905 topology only learns the Agent seconds after it
// associates. The refresh therefore has to consult the Radio, not just the
// topology, or it kills the pairing it exists to protect.
func TestRegistrarRefreshChecksTheRadioForAnAssociatedStation(t *testing.T) {
	source, err := os.ReadFile("easymesh.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	start := strings.Index(body, "func maintainControllerOnboardingWindow")
	if start < 0 {
		t.Fatal("maintainControllerOnboardingWindow not found")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit maintainControllerOnboardingWindow")
	}
	fn := body[start : start+end]
	refresh := strings.Index(fn, "openControllerOnboardingWindow")
	if refresh < 0 {
		t.Fatal("the window is never refreshed")
	}
	guard := fn[:refresh]
	if !strings.Contains(guard, "easyMeshBackhaulBSSHasStation") {
		t.Fatal("the refresh is not gated on an already-associated Backhaul station")
	}
}

// A failed onboarding must leave a usable device. `wifi reload` alone does not:
// during a rollback the Mesh bridges are still being torn down, every
// `brctl addif br-lan <radio>` fails with "Resource busy", and the radios end up
// in no bridge at all — clients associate to a WiFi that carries nothing and the
// portal becomes unreachable.
func TestRollbackReattachesRadiosToTheClientBridge(t *testing.T) {
	command := easyMeshReattachRadiosCommand()
	for _, expected := range []string{
		"brctl delif",                    // detach from any foreign bridge first
		"/sys/class/net/br-lan/brif/ra0", // verify, do not assume
		"/sys/class/net/br-lan/brif/rax0",
		"/sbin/wifi reload",
	} {
		if !strings.Contains(command, expected) {
			t.Fatalf("radio reattach does not contain %q: %s", expected, command)
		}
	}

	// The rollback path (resetTurnkey) must actually run it.
	worker := easyMeshRestoreWorker(true, false)
	if !strings.Contains(worker, "lrap_reattach_radios") {
		t.Fatal("the Turnkey restore does not reattach the radios")
	}
	if strings.Index(worker, "lrap_reattach_radios") < strings.Index(worker, "EasyMesh_openwrt.sh") {
		t.Fatal("the radios are reattached before the Turnkey teardown that detaches them")
	}
}

// The restore worker is assembled from several multi-line shell blocks. Joining
// a newline-terminated block with "; " puts a bare ";" at the start of a line,
// which busybox sh rejects with "syntax error: unexpected ;" — and because the
// worker runs detached, that only shows up as a failed rollback on a live
// device. Guard the shape here instead.
func TestRestoreWorkerIsWellFormedShell(t *testing.T) {
	for _, worker := range []string{
		easyMeshRestoreWorker(true, false),
		easyMeshRestoreWorker(false, false),
		easyMeshRestoreWorker(true, true),
	} {
		for _, line := range strings.Split(worker, "\n") {
			if strings.HasPrefix(strings.TrimLeft(line, " \t"), ";") {
				t.Fatalf("restore worker has a line starting with %q: %s", ";", line)
			}
		}
	}
}

// Antennas must never be able to block a Leave. They are plain client APs and
// carry nothing the Mesh depends on, but a chassis with none attached — or with
// one merely unreachable at that moment — used to be refused, stranding the AC
// in a half-torn Mesh with no way out through the product.
func TestLeaveIsNotBlockedByUnreachableAntennas(t *testing.T) {
	source, err := os.ReadFile("easymesh.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	start := strings.Index(body, "func validatePreEasyMeshBackups")
	if start < 0 {
		t.Fatal("validatePreEasyMeshBackups not found")
	}
	end := strings.Index(body[start:], "\nfunc ")
	if end < 0 {
		t.Fatal("could not delimit validatePreEasyMeshBackups")
	}
	fn := body[start : start+end]
	if strings.Contains(fn, "refusing to restore only the AC") {
		t.Fatal("a chassis with no reachable Antennas is still refused a Leave")
	}
	// The AC's own snapshot is still the invariant that makes recovery safe.
	if !strings.Contains(fn, "Controller pre-Mesh snapshot is incomplete") {
		t.Fatal("the AC's own snapshot is no longer validated")
	}
}

// In the AC-to-AC layout the Agent chassis IS the wireless device — there is no
// separate downstream "main module" 1905 device to key the DHCP lease on — and
// its single BSS carries clients AND the Backhaul, which the driver reports as
// BACKHAUL=1 FRONTHAUL=0. Both facts used to make a perfectly healthy Agent read
// as "Management: waiting for an address / Fronthaul is not active".
func TestSingleBSSAgentReportsItsAddressAndClientCoverage(t *testing.T) {
	originalReachable := easyMeshManagementReachable
	originalBelongs := easyMeshLeaseBelongsToChassis
	t.Cleanup(func() {
		easyMeshManagementReachable = originalReachable
		easyMeshLeaseBelongsToChassis = originalBelongs
	})
	easyMeshManagementReachable = func(string) bool { return true }
	// A lease is only attributed to a chassis that identifies itself as its
	// holder; here it does.
	easyMeshLeaseBelongsToChassis = func(string, string) bool { return true }

	topology := `{"topology information":[
		{"AL MAC":"f0:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"dc:84:03:01:57:80","Distance from controller":"1","BH Info":[{"Backhaul Medium Type":"5G","RSSI":"-71"}],"Radio Info":[{"band":"5G","BSSINFO":[{"SSID":"Chase_Test","BACKHAUL":"1","FRONTHAUL":"0"}]}]}
	]}`
	nodes := easyMeshRemoteNodesFromTopology(topology, map[string]string{
		"dc:84:03:01:57:80": "10.10.18.127",
	})
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v, want one Agent", nodes)
	}
	node := nodes[0]
	if node.ManagementIP != "10.10.18.127" {
		t.Fatalf("the Agent's own AL MAC did not resolve its lease: %#v", node)
	}
	if !node.FronthaulReady || len(node.FronthaulSSIDs) != 1 || node.FronthaulSSIDs[0] != "Chase_Test" {
		t.Fatalf("the combined single BSS was not counted as client coverage: %#v", node)
	}
}

// A Radio that really does publish a separate Backhaul-only BSS must not have it
// counted as client coverage.
func TestDedicatedBackhaulBSSIsNotClientCoverage(t *testing.T) {
	topology := `{"topology information":[
		{"AL MAC":"f0:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"f0:a8:82:fb:00:f0","Distance from controller":"1","BH Info":[{"Backhaul Medium Type":"5G","RSSI":"-60"}],"Radio Info":[{"band":"5G","BSSINFO":[{"SSID":"Multi-AP-1","BACKHAUL":"1","FRONTHAUL":"0"},{"SSID":"Chase_Test_5G","BACKHAUL":"0","FRONTHAUL":"1"}]}]}
	]}`
	nodes := easyMeshRemoteNodesFromTopology(topology, nil)
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v", nodes)
	}
	if got := nodes[0].FronthaulSSIDs; len(got) != 1 || got[0] != "Chase_Test_5G" {
		t.Fatalf("the dedicated Backhaul BSS leaked into client coverage: %#v", got)
	}
}

// The shape actually measured on the pair of chassis: the Agent's 1905 AL-MAC is
// the 5G Radio's address while its DHCP lease is held by the client bridge, and
// the firmware reports BACKHAUL=0 FRONTHAUL=0 on every BSS — the Controller's own
// included. The card must still show the Agent's real address and the SSID it is
// really broadcasting.
func TestAgentCardResolvesManagementWhenALMACDiffersFromLeaseMAC(t *testing.T) {
	topology := `{"topology information":[
		{"AL MAC":"f0:a8:82:fb:00:a4","Distance from controller":"0","BH Info":[]},
		{"AL MAC":"de:84:03:11:57:80","Distance from controller":"1","BH Info":[{"Backhaul Medium Type":"5G","RSSI":"-72"}],"Radio Info":[
			{"band":"5G","BSSINFO":[{"BSSID":"de:84:03:11:57:80","SSID":"Chase_Test","BACKHAUL":"0","FRONTHAUL":"0"}]},
			{"band":"2.4G","BSSINFO":[{"BSSID":"dc:84:03:01:57:80","SSID":"Chase_Test","BACKHAUL":"0","FRONTHAUL":"0"}]}
		]}
	]}`
	// The lease is under the bridge address, which the AL-MAC lookup can never hit.
	nodes := easyMeshRemoteNodesFromTopologyWithManagement(topology, nil,
		map[string]easyMeshManagementNode{
			"de:84:03:11:57:80": {IP: "10.10.18.127", NodeID: "dc:84:03:01:57:80"},
		})
	if len(nodes) != 1 {
		t.Fatalf("remote nodes = %#v, want one Agent", nodes)
	}
	node := nodes[0]
	if node.ManagementIP != "10.10.18.127" || !node.ManagementOnline {
		t.Fatalf("a reachable Agent still reported no address: %#v", node)
	}
	if node.MainMAC != "dc:84:03:01:57:80" {
		t.Fatalf("main MAC = %q, want the bridge address the Agent reported", node.MainMAC)
	}
	if !node.FronthaulReady || len(node.FronthaulSSIDs) != 1 || node.FronthaulSSIDs[0] != "Chase_Test" {
		t.Fatalf("client coverage was lost to the all-zero BSS flags: %#v", node)
	}
}
