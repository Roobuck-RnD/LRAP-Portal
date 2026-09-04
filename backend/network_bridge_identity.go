package main

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
)

// The bridge whose hardware address must never move, and the radio that supplies
// it.
const (
	clientBridgeName        = "br-lan"
	clientBridgeMACSourceIf = "ra0"
	clientBridgeMACOption   = "network.@device[0].macaddr"
)

// readInterfaceMAC returns one interface's hardware address, lowercased.
func readInterfaceMAC(name string) (string, error) {
	raw, err := os.ReadFile("/sys/class/net/" + name + "/address")
	if err != nil {
		return "", err
	}
	mac := strings.ToLower(strings.TrimSpace(string(raw)))
	if len(mac) != len("00:00:00:00:00:00") {
		return "", fmt.Errorf("unexpected MAC %q on %s", mac, name)
	}
	return mac, nil
}

// pinClientBridgeMACAtStartup gives br-lan a fixed hardware address.
//
// A Linux bridge with no address of its own inherits the LOWEST one among its
// member ports, so its identity moves whenever the membership does. On this
// chassis the LAN ports carry locally-administered (randomised) addresses while
// the radios carry factory EEPROM ones, and Mesh onboarding repeatedly changes
// the membership — lanN is swapped for lanN.100, and radios and the Backhaul
// station attach and detach. br-lan was observed walking through four different
// addresses during a single onboarding.
//
// That breaks DHCP, which identifies a client by its MAC: the Agent sends its
// DISCOVER from one address, the membership shifts, and the Root's OFFER is
// addressed to a MAC that no longer exists — no REQUEST, no lease. It also makes
// the unit look like a brand new device to its upstream after every reboot, so
// no static reservation can ever hold.
//
// ra0's address is the right anchor: it comes from the WiFi EEPROM, so it is
// unique per unit and stable across reboots, and the firmware already treats it
// as this device's identity — mapd uses exactly this value as the 1905 AL MAC.
// Two units can only collide if they shipped with the same radio EEPROM, which
// would already break the Mesh through duplicate AL MACs.
func pinClientBridgeMACAtStartup() {
	mac, err := readInterfaceMAC(clientBridgeMACSourceIf)
	if err != nil {
		// No radio yet (very early boot) or a board without it: nothing to anchor to.
		log.Printf("client bridge MAC: no %s address to anchor to: %v", clientBridgeMACSourceIf, err)
		return
	}

	// Persist it so netifd applies it on every future bring-up.
	current, _ := exec.Command("uci", "-q", "get", clientBridgeMACOption).Output()
	if !strings.EqualFold(strings.TrimSpace(string(current)), mac) {
		if out, setErr := exec.Command("uci", "set", clientBridgeMACOption+"="+mac).CombinedOutput(); setErr != nil {
			log.Printf("client bridge MAC: uci set failed: %v output=%s", setErr, strings.TrimSpace(string(out)))
			return
		}
		if out, commitErr := exec.Command("uci", "commit", "network").CombinedOutput(); commitErr != nil {
			log.Printf("client bridge MAC: uci commit failed: %v output=%s", commitErr, strings.TrimSpace(string(out)))
			return
		}
		log.Printf("client bridge MAC pinned to %s (from %s)", mac, clientBridgeMACSourceIf)
	}

	// Apply it live, but only when it is actually wrong. `ip link set address` is a
	// single atomic change; it deliberately does NOT rebuild the bridge, because
	// tearing br-lan down is what detaches the radios and the Backhaul station and
	// breaks the very path a Mesh Agent's DHCP exchange has to travel.
	live, err := readInterfaceMAC(clientBridgeName)
	if err != nil || strings.EqualFold(live, mac) {
		return
	}
	if out, applyErr := exec.Command("ip", "link", "set", "dev", clientBridgeName, "address", mac).CombinedOutput(); applyErr != nil {
		log.Printf("client bridge MAC: could not apply %s live (was %s): %v output=%s", mac, live, applyErr, strings.TrimSpace(string(out)))
		return
	}
	log.Printf("client bridge MAC corrected live: %s -> %s", live, mac)
}
