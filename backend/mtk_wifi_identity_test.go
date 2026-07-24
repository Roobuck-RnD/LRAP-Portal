package main

import "testing"

func TestManagedAntennaModuleID(t *testing.T) {
	for portIndex := 1; portIndex <= 4; portIndex++ {
		id := managedAntennaModuleID(portIndex)
		got, ok := managedAntennaPortIndex(id)
		if !ok || got != portIndex {
			t.Fatalf("round trip failed for port %d: id=%q got=%d ok=%v", portIndex, id, got, ok)
		}
	}

	for _, invalid := range []string{"", "antenna:0", "antenna:5", "10.10.18.2", "lan1"} {
		if _, ok := managedAntennaPortIndex(invalid); ok {
			t.Fatalf("invalid module id %q was accepted", invalid)
		}
	}
}

func TestManagedModuleDisplaySortKeyPrefersStableIdentity(t *testing.T) {
	tests := []struct {
		name       string
		moduleID   string
		moduleType string
		port       string
		ip         string
		want       int
	}{
		{name: "main", moduleID: "main", moduleType: "main", port: "br-lan", ip: "10.10.18.1", want: 0},
		{name: "antenna one ignores high IP", moduleID: "antenna:1", moduleType: "ap", port: "lan1", ip: "10.10.18.5", want: 1},
		{name: "antenna four ignores low IP", moduleID: "antenna:4", moduleType: "ap", port: "lan4", ip: "10.10.18.2", want: 4},
		{name: "legacy port fallback", moduleType: "ap", port: "lan3", ip: "10.10.18.2", want: 3},
		{name: "IP is last resort", moduleType: "ap", port: "unknown", ip: "10.10.18.4", want: 1004},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := managedModuleDisplaySortKey(test.moduleID, test.moduleType, test.port, test.ip)
			if got != test.want {
				t.Fatalf("sort key=%d, want %d", got, test.want)
			}
		})
	}
}

func TestPrepareMtkCentralWifiStateMigratesIPToPhysicalPort(t *testing.T) {
	off := mtkRadioState{Radio24Enabled: true, Radio5Enabled: false, UpdatedAt: 10}
	on := mtkRadioState{Radio24Enabled: true, Radio5Enabled: true, UpdatedAt: 20}

	legacy := mtkCentralWifiState{
		Version: 1,
		Modules: map[string]mtkRadioState{
			managedMainModuleID: on,
			"10.10.18.2":        off,
			"10.10.18.3":        on,
		},
	}

	// IPs are intentionally mapped to the opposite port order. The state must
	// follow the physical port, not the final octet of the address.
	got := prepareMtkCentralWifiState(legacy, map[string]int{
		"10.10.18.2": 2,
		"10.10.18.3": 1,
	})

	if got.Version != mtkStateVersion {
		t.Fatalf("version=%d, want %d", got.Version, mtkStateVersion)
	}
	if got.Modules["antenna:2"].Radio5Enabled {
		t.Fatal("disabled legacy state did not migrate to antenna:2")
	}
	if !got.Modules["antenna:1"].Radio5Enabled {
		t.Fatal("enabled legacy state did not migrate to antenna:1")
	}
	if _, exists := got.Modules["10.10.18.2"]; exists {
		t.Fatal("legacy IP key remained in modules")
	}
}

func TestPrepareMtkCentralWifiStateRetainsThenResolvesOfflineLegacyState(t *testing.T) {
	off := mtkRadioState{Radio24Enabled: true, Radio5Enabled: false, UpdatedAt: 10}
	legacy := mtkCentralWifiState{
		Version: 1,
		Modules: map[string]mtkRadioState{
			"10.10.18.4": off,
		},
	}

	unresolved := prepareMtkCentralWifiState(legacy, nil)
	if _, ok := unresolved.LegacyIPStates["10.10.18.4"]; !ok {
		t.Fatal("offline legacy state was not retained")
	}

	resolved := prepareMtkCentralWifiState(unresolved, map[string]int{"10.10.18.4": 3})
	if resolved.Modules["antenna:3"].Radio5Enabled {
		t.Fatal("retained legacy state did not override the temporary default")
	}
	if _, ok := resolved.LegacyIPStates["10.10.18.4"]; ok {
		t.Fatal("resolved legacy state was not removed")
	}
}

func TestResolveMtkManagedAPUsesStableIdentity(t *testing.T) {
	registry := []ManagedModule{
		{ModuleID: "antenna:1", PortIndex: 1, Port: "lan1", IP: "10.10.18.3"},
		{ModuleID: "antenna:2", PortIndex: 2, Port: "lan2", IP: "10.10.18.2"},
	}

	got, err := resolveMtkManagedAP("antenna:1", "10.10.18.2", registry)
	if err != nil {
		t.Fatalf("resolve failed: %v", err)
	}
	if got.IP != "10.10.18.3" {
		t.Fatalf("resolved IP=%q, want current antenna:1 address", got.IP)
	}
}

func TestVerifyMtkDatContentChecksUnifiedSaveFields(t *testing.T) {
	original := "SSID1=old\nWPAPSK1=oldpassword\nChannel=1\nAutoChannelSelect=0\nHT_BW=0\nHT_BSSCoexistence=0\nVHT_BW=0\nTxPower=50\nPERCENTAGEenable=1\n"
	updates := buildMtkDatUpdates("UnifiedWiFi", "newpassword", "6", "40", 75, mtkBand24G)
	updated := updateMtkDatContent(original, updates)

	if err := verifyMtkDatContent(updated, "UnifiedWiFi", "newpassword", "6", "40", 75, mtkBand24G); err != nil {
		t.Fatalf("unified WiFi fields did not verify: %v", err)
	}
	if err := verifyMtkDatContent(updated, "UnifiedWiFi", "wrongpassword", "6", "40", 75, mtkBand24G); err == nil {
		t.Fatal("password mismatch was not detected")
	}
}
