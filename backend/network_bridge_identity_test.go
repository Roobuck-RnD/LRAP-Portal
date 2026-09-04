package main

import (
	"os"
	"strings"
	"testing"
)

// A Linux bridge with no address of its own inherits the lowest member's, so it
// moves whenever the membership does — and Mesh onboarding changes membership
// repeatedly. The anchor has to be the radio EEPROM address (what the firmware
// itself uses as the 1905 AL MAC), not a LAN port whose address is randomised
// per boot.
func TestClientBridgeMACIsAnchoredToTheRadio(t *testing.T) {
	if clientBridgeMACSourceIf != "ra0" {
		t.Fatalf("bridge MAC anchored to %q, want the radio", clientBridgeMACSourceIf)
	}
	if clientBridgeName != "br-lan" {
		t.Fatalf("unexpected client bridge %q", clientBridgeName)
	}
	if !strings.HasPrefix(clientBridgeMACOption, "network.") || !strings.HasSuffix(clientBridgeMACOption, ".macaddr") {
		t.Fatalf("unexpected uci option %q", clientBridgeMACOption)
	}
}

// Correcting the address live must never rebuild the bridge: tearing br-lan down
// detaches the radios and the Backhaul station, which is the path a Mesh Agent's
// own DHCP exchange travels.
func TestClientBridgeMACIsAppliedWithoutRebuildingTheBridge(t *testing.T) {
	source, err := os.ReadFile("network_bridge_identity.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	if !strings.Contains(body, `"ip", "link", "set", "dev", clientBridgeName, "address"`) {
		t.Fatal("the live correction does not use an atomic address change")
	}
	for _, forbidden := range []string{"network reload", "ifup", "ifdown", "wifi reload", "network restart"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("bridge MAC pinning uses the disruptive %q", forbidden)
		}
	}
}

func TestReadInterfaceMACRejectsGarbage(t *testing.T) {
	if _, err := readInterfaceMAC("definitely-not-an-interface"); err == nil {
		t.Fatal("a missing interface was accepted")
	}
}
