package main

import (
	"errors"
	"testing"
)

func TestMeshNamesAreRecognised(t *testing.T) {
	for _, s := range []string{"Roobuck-BH-FB00A4", "roobuck-bh-000000", "Multi-AP-1", " Multi-AP-5G "} {
		if !mtkMeshName(s) {
			t.Errorf("%q not taken for a Mesh name", s)
		}
	}
	for _, s := range []string{"Chase_Test_2.4G", "Roobuck", "", "BH-Roobuck"} {
		if mtkMeshName(s) {
			t.Errorf("%q taken for a Mesh name", s)
		}
	}
}

// During a Mesh the page shows what the Antennas broadcast -- the one on the
// lowest port, so two reads agree -- and never the Backhaul identity.
func TestClientWiFiDuringMeshComesFromTheAntennas(t *testing.T) {
	old := mtkReadPreMeshProfile
	t.Cleanup(func() { mtkReadPreMeshProfile = old })
	mtkReadPreMeshProfile = func(string) (string, error) {
		t.Fatal("read the pre-Mesh copy although an Antenna answered")
		return "", nil
	}

	antennas := map[string]mtkClientCredential{
		"172.31.255.3": {"Office", "office-pass", "Office_5G", "office-pass"},
		"172.31.255.2": {"Office", "office-pass", "Office_5G", "office-pass-2"},
		"172.31.255.9": {"", "", "", ""},
	}
	got := mtkClientWiFiDuringMesh(antennas, map[string]int{"172.31.255.3": 1, "172.31.255.2": 2})
	if got.Pass5 != "office-pass" {
		t.Errorf("took %+v, want the Antenna on port 1", got)
	}
}

func TestClientWiFiDuringMeshFallsBackToThePreMeshCopy(t *testing.T) {
	old := mtkReadPreMeshProfile
	t.Cleanup(func() { mtkReadPreMeshProfile = old })
	profiles := map[string]string{
		"wifi-2g.dat": "SSID1=Office\nWPAPSK1=office-pass\n",
		"wifi-5g.dat": "SSID1=Office_5G\nWPAPSK1=office-pass\n",
	}
	mtkReadPreMeshProfile = func(name string) (string, error) { return profiles[name], nil }
	if got := mtkClientWiFiDuringMesh(nil, nil); got.SSID24 != "Office" || got.Pass24 != "office-pass" || got.SSID5 != "Office_5G" {
		t.Errorf("from the pre-Mesh copy: %+v", got)
	}

	// A copy that itself holds a Mesh name says nothing about the client WiFi.
	profiles["wifi-2g.dat"] = "SSID1=Roobuck-BH-FB00A4\nWPAPSK1=generated\n"
	if got := mtkClientWiFiDuringMesh(nil, nil); got != (mtkClientCredential{}) {
		t.Errorf("a Mesh name came back as the client WiFi: %+v", got)
	}

	mtkReadPreMeshProfile = func(string) (string, error) { return "", errors.New("no copy") }
	if got := mtkClientWiFiDuringMesh(nil, nil); got != (mtkClientCredential{}) {
		t.Errorf("without a copy: %+v", got)
	}
}
