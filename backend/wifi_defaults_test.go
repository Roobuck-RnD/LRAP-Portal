package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFactoryWiFiNames(t *testing.T) {
	two, five, err := factoryWiFiNames("f0:a8:82:fb:00:a4")
	if err != nil || two != "Roobuck-FB00A4" || five != "Roobuck-FB00A4_5G" {
		t.Errorf("got %q %q %v", two, five, err)
	}
	for _, bad := range []string{"", "f0:a8:82:fb:00", "zz:a8:82:fb:00:a4"} {
		if _, _, err := factoryWiFiNames(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// A unit whose WiFi is still the firmware's, with nothing recorded, in a
// working directory of its own.
func factoryUnit(t *testing.T, live24, live5 string) {
	t.Helper()
	t.Chdir(t.TempDir())
	for _, dir := range []string{"etc/wireless/mediatek", "rom/etc/wireless/mediatek"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, content string) {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("rom/etc/wireless/mediatek/b0.dat", "SSID1=RoobuckAC2.4_F5EB7C\nWPAPSK1=12345678\n")
	write("rom/etc/wireless/mediatek/b1.dat", "SSID1=MTK_CHEETAH_AP_5G\nWPAPSK1=12345678\n")
	write("etc/wireless/mediatek/b0.dat", "SSID1="+live24+"\nWPAPSK1=12345678\n")
	write("etc/wireless/mediatek/b1.dat", "SSID1="+live5+"\nWPAPSK1=12345678\n")

	oldProfiles, oldROM, oldMarker := factoryWiFiProfiles, factoryWiFiROM, factoryWiFiMarker
	oldMAC, oldRead, oldSave, oldSleep, oldNow := factoryWiFiMAC, factoryWiFiRead, factoryWiFiSave, factoryWiFiSleep, factoryWiFiNow
	t.Cleanup(func() {
		factoryWiFiProfiles, factoryWiFiROM, factoryWiFiMarker = oldProfiles, oldROM, oldMarker
		factoryWiFiMAC, factoryWiFiRead, factoryWiFiSave, factoryWiFiSleep, factoryWiFiNow = oldMAC, oldRead, oldSave, oldSleep, oldNow
	})
	// A copy: the package's own list must come back untouched.
	factoryWiFiProfiles = append(factoryWiFiProfiles[:0:0], factoryWiFiProfiles...)
	factoryWiFiProfiles[0].Path, factoryWiFiProfiles[1].Path = "etc/wireless/mediatek/b0.dat", "etc/wireless/mediatek/b1.dat"
	factoryWiFiROM = "rom"
	factoryWiFiMarker = "etc/wireless/lrap-wifi-defaults.json"
	factoryWiFiMAC = func() (string, error) { return "f0:a8:82:fb:00:a4", nil }

	// A clock that moves only when the code sleeps.
	now := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	factoryWiFiNow = func() time.Time { return now }
	factoryWiFiSleep = func(d time.Duration) { now = now.Add(d) }
}

func recorded(t *testing.T) factoryWiFiRecord {
	t.Helper()
	var r factoryWiFiRecord
	raw, err := os.ReadFile(factoryWiFiMarker)
	if err != nil {
		t.Fatalf("nothing recorded: %v", err)
	}
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

func chassis() MtkWifiInfo {
	return MtkWifiInfo{SSID24: "RoobuckAC2.4_F5EB7C", SSID5: "MTK_CHEETAH_AP_5G", Modules: []MtkWifiModuleRadio{
		{ModuleID: managedMainModuleID, Type: "main", Online: true, IdentityReady: true},
		{ModuleID: "lan1", Type: "ap", Name: "Antenna1", Online: true, IdentityReady: true},
		{ModuleID: "lan2", Type: "ap", Name: "Antenna2", Online: true, IdentityReady: true},
		{ModuleID: "", Type: "ap", Name: "RoobuckAP", Online: true, IdentityReady: false},
	}}
}

func TestFactoryWiFiAppliedOnAFreshUnit(t *testing.T) {
	factoryUnit(t, "RoobuckAC2.4_F5EB7C", "MTK_CHEETAH_AP_5G")
	factoryWiFiRead = func() (MtkWifiInfo, error) { return chassis(), nil }
	var saved *MtkWifiInfo
	factoryWiFiSave = func(req MtkWifiInfo) error { saved = &req; return nil }

	applyFactoryWiFiDefaults()

	if saved == nil {
		t.Fatal("nothing saved")
	}
	if saved.SSID24 != "Roobuck-FB00A4" || saved.SSID5 != "Roobuck-FB00A4_5G" || saved.Pass24 != "12345678" || saved.Pass5 != "12345678" {
		t.Errorf("saved %q/%q", saved.SSID24, saved.SSID5)
	}
	if len(saved.Modules) != 3 {
		t.Errorf("wrote %d modules, want the main module and the two antennas with an identity", len(saved.Modules))
	}
	if r := recorded(t); r.Result != "applied" || r.SSID24 != "Roobuck-FB00A4" {
		t.Errorf("recorded %+v", r)
	}
}

// The antennas come up one after another; the save waits until they have
// stopped changing, and goes ahead at the latest after the maximum wait.
func TestFactoryWiFiWaitsForTheAntennas(t *testing.T) {
	factoryUnit(t, "RoobuckAC2.4_F5EB7C", "MTK_CHEETAH_AP_5G")
	reads := 0
	factoryWiFiRead = func() (MtkWifiInfo, error) {
		reads++
		c := chassis()
		up := reads / 8 // one more antenna every two minutes
		if up < 2 {
			c.Modules = c.Modules[:1+up]
		}
		return c, nil
	}
	var saved *MtkWifiInfo
	factoryWiFiSave = func(req MtkWifiInfo) error { saved = &req; return nil }
	applyFactoryWiFiDefaults()
	if saved == nil || len(saved.Modules) != 3 {
		t.Fatalf("saved %+v, want all three once they had appeared", saved)
	}
}

func TestFactoryWiFiKeepsAUnitAlreadySet(t *testing.T) {
	factoryUnit(t, "Chase_Test_2.4G", "Chase_Test_5G")
	factoryWiFiSave = func(MtkWifiInfo) error { t.Fatal("overwrote the WiFi"); return nil }
	applyFactoryWiFiDefaults()
	if r := recorded(t); r.Result != "kept" {
		t.Errorf("recorded %+v", r)
	}
	// And once recorded it is never looked at again, whatever the WiFi becomes.
	if pending, _ := factoryWiFiPending(); pending {
		t.Error("pending after being recorded")
	}
}

func TestFactoryWiFiRetriedAfterAFailedSave(t *testing.T) {
	factoryUnit(t, "RoobuckAC2.4_F5EB7C", "MTK_CHEETAH_AP_5G")
	factoryWiFiRead = func() (MtkWifiInfo, error) { return chassis(), nil }
	factoryWiFiSave = func(MtkWifiInfo) error { return errors.New("Antenna2 is not reachable") }
	applyFactoryWiFiDefaults()
	if _, err := os.Stat(factoryWiFiMarker); err == nil {
		t.Error("recorded although nothing was applied; the next start would not try again")
	}
}

func TestFactoryWiFiNotPendingWithoutTheFirmwareCopy(t *testing.T) {
	factoryUnit(t, "RoobuckAC2.4_F5EB7C", "MTK_CHEETAH_AP_5G")
	if err := os.RemoveAll(filepath.Join("rom")); err != nil {
		t.Fatal(err)
	}
	if pending, _ := factoryWiFiPending(); pending {
		t.Error("pending with nothing to compare against")
	}
}
