package main

import (
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// How often the client bridge is re-checked.
const clientBridgeCheckInterval = 15 * time.Second

// Where bridge membership is read from. It is a variable so the watchdog can be
// exercised against a fabricated layout instead of the running kernel's.
var netSysfsRoot = "/sys/class/net"

// backhaulSTAInterfaces returns both stations of the card. The Mesh only uses
// the selected band's, but the ordinary bridge carries both, so both are
// restored to keep the composition identical to a healthy boot.
func backhaulSTAInterfaces() []string {
	return []string{"apcli0", "apclix0"}
}

// clientBridgeMemberInterfaces returns every wireless interface that belongs in
// the client bridge on a healthy boot: the two APs this chassis serves clients
// with, and the two Backhaul stations.
//
// The AP radios are here because they have exactly the same exposure as the
// stations and nothing else covers them. Measured on a factory-fresh chassis the
// first time it ran a build that creates the Antenna management VLAN: the
// `ubus call network reload` that ends that setup rebuilt br-lan without ra0 and
// rax0, and they never came back. The radios stayed up and the SSID stayed on
// the air, so clients still associated -- and nothing was bridged to anything,
// so the chassis could only be repaired over the serial console.
//
// LAN ports are deliberately absent. Antenna adoption takes one out of the
// client bridge on purpose and puts it back itself, and a watchdog that also
// reattached ports would fight it mid-adoption.
func clientBridgeMemberInterfaces() []string {
	return append([]string{Mtk24GIfName, Mtk5GIfName}, backhaulSTAInterfaces()...)
}

func interfaceIsBridged(bridge string, iface string) bool {
	_, err := os.Stat(filepath.Join(netSysfsRoot, bridge, "brif", iface))
	return err == nil
}

func interfaceExists(iface string) bool {
	_, err := os.Stat(filepath.Join(netSysfsRoot, iface))
	return err == nil
}

// attachToClientBridge performs the one repair this watchdog makes.
//
// brctl is used rather than a netifd operation on purpose -- re-running netifd
// is what caused the damage in the first place.
var attachToClientBridge = func(iface string) (string, error) {
	out, err := exec.Command("brctl", "addif", clientBridgeName, iface).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// reattachClientBridgeMembers puts every wireless interface that belongs in the
// client bridge back into it, and reports how many it had to repair.
//
// Any `ubus call network reload` rebuilds br-lan and drops these out of it.
// Measured on the device: after a reload the Mesh data path is dead -- mapd
// immediately loses the Controller -- while `iwconfig` still shows the station
// associated and `mapd_cli bh_conn_status` still returns 1. Both of the signals
// the product uses to decide "the Backhaul is up" keep saying yes, so nothing
// notices. A network reload is not exotic: editing any network setting in the
// portal triggers one, and so does the first start of a build that has to create
// the Antenna management VLAN on a chassis that does not carry it yet.
func reattachClientBridgeMembers() int {
	// Before the bridge itself exists there is nothing to repair, and asking
	// brctl anyway would log a failure every interval for the whole of boot.
	if !interfaceExists(clientBridgeName) {
		return 0
	}
	repaired := 0
	for _, iface := range clientBridgeMemberInterfaces() {
		if !interfaceExists(iface) || interfaceIsBridged(clientBridgeName, iface) {
			continue
		}
		out, err := attachToClientBridge(iface)
		if err != nil {
			log.Printf("Client bridge watchdog: could not reattach %s to %s: %v output=%s",
				iface, clientBridgeName, err, out)
			continue
		}
		log.Printf("Client bridge watchdog: reattached %s to %s", iface, clientBridgeName)
		repaired++
	}
	return repaired
}

// How long a network reload is given to finish rebuilding the client bridge,
// and how many consecutive complete checks end the wait early.
var (
	clientBridgeSettleWindow = 20 * time.Second
	clientBridgeSettleSteady = 3
)

// settleClientBridgeAfterReload returns this chassis's wireless interfaces to
// the client bridge after a network reload has rebuilt it, and waits to see the
// bridge stay complete.
//
// netifd rebuilds br-lan whenever it reloads, and the MediaTek radios are put
// into that bridge by the wifi driver rather than by netifd, so a reload leaves
// them out and nothing else puts them back. The watchdog does, on its own
// fifteen-second schedule; calling the same repair from the code that caused the
// reload closes the window in between -- during which the chassis is on the air,
// accepting associations, with nothing bridged behind them. Measured on a
// factory unit the first time it built the Antenna management VLAN: SSID up,
// clients associating, no path to anything, recoverable only over serial.
//
// It keeps watching rather than repairing once, because the reload is
// asynchronous: br-lan can be rebuilt again a second or two after ubus has
// returned, which would quietly undo a single repair.
func settleClientBridgeAfterReload() {
	deadline := time.Now().Add(clientBridgeSettleWindow)
	steady := 0
	repaired := 0
	for time.Now().Before(deadline) {
		if fixed := reattachClientBridgeMembers(); fixed > 0 {
			repaired += fixed
			steady = 0
		} else {
			steady++
		}
		if steady >= clientBridgeSettleSteady {
			break
		}
		time.Sleep(time.Second)
	}
	if repaired > 0 {
		log.Printf("Client bridge watchdog: returned %d interface(s) after a network reload", repaired)
	}
}

// startClientBridgeWatchdog keeps this chassis's own wireless interfaces in the
// client bridge for the whole life of the process.
//
// It is deliberately not conditional on the Mesh being enabled or on this device
// being an Agent. The reload that breaks the bridge can come from anywhere --
// our own code, the portal's network pages, or an operator on the shell -- and
// the worst case measured so far happened on a standalone chassis, where an
// earlier Agent-only guard meant this never ran at all.
func startClientBridgeWatchdog() {
	go func() {
		for {
			time.Sleep(clientBridgeCheckInterval)
			reattachClientBridgeMembers()
			reconcileClientBridgeAddress()
		}
	}()
}

// uciClientLANAddress reads what the client LAN is configured to hold.
var uciClientLANAddress = func() (proto string, ipaddr string, netmask string) {
	get := func(key string) string {
		out, err := exec.Command("uci", "-q", "get", key).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("network.lan.proto"), get("network.lan.ipaddr"), get("network.lan.netmask")
}

// clientBridgeIPv4 lists the IPv4 addresses the kernel actually holds on the
// client bridge, in CIDR form.
var clientBridgeIPv4 = func() ([]string, error) {
	out, err := exec.Command("ip", "-4", "-o", "addr", "show", "dev", clientBridgeName).Output()
	if err != nil {
		return nil, err
	}
	return parseIPv4CIDRs(string(out)), nil
}

// parseIPv4CIDRs pulls every "inet <cidr>" pair out of an ip(8) listing.
func parseIPv4CIDRs(raw string) []string {
	var found []string
	fields := strings.Fields(raw)
	for index, field := range fields {
		if field == "inet" && index+1 < len(fields) {
			found = append(found, fields[index+1])
		}
	}
	return found
}

var changeClientBridgeAddress = func(op string, cidr string) (string, error) {
	out, err := exec.Command("ip", "-4", "addr", op, cidr, "dev", clientBridgeName).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// netmaskPrefix turns a dotted netmask into its prefix length, and reports
// whether the input was a netmask at all: the ones have to be contiguous and
// leading, so 255.255.254.0 is /23 and 255.0.255.0 is nothing.
func netmaskPrefix(mask string) (int, bool) {
	parts := strings.Split(strings.TrimSpace(mask), ".")
	if len(parts) != 4 {
		return 0, false
	}
	var value uint32
	for _, part := range parts {
		octet, err := strconv.Atoi(part)
		if err != nil || octet < 0 || octet > 255 {
			return 0, false
		}
		value = value<<8 | uint32(octet)
	}
	prefix := 0
	for prefix < 32 && value&(1<<uint(31-prefix)) != 0 {
		prefix++
	}
	if prefix == 0 || (prefix < 32 && value<<uint(prefix) != 0) {
		return 0, false
	}
	return prefix, true
}

// reconcileClientBridgeAddress puts the client bridge back on the prefix its own
// configuration asks for, and reports whether it had to.
//
// The MediaTek EasyMesh script this product starts the engine with sets the
// bridge address itself, with a netmask it never reads:
//
//	br_ip=`uci -q get network.lan.ipaddr`
//	ifconfig ${bridge_name} $br_ip
//
// ifconfig assigns through the old ioctl, which fills in a classful prefix when
// none is given -- so a 10.x address lands as a /8. Measured on a chassis a few
// minutes after Enable Mesh: uci held netmask 255.255.255.0, netifd reported the
// interface up on "mask": 24, and the kernel had 10.10.18.1/8 with the classful
// broadcast 10.255.255.255 to match. The same shape was reproduced on that
// device from `ifconfig <dev> 10.10.77.1`, and could not be produced from netifd
// (which gives /32 without a netmask) or from `ip addr add` (also /32).
//
// It normally hides. The script's Agent branch runs udhcpc straight afterwards,
// so a chassis that finishes joining has the classful address replaced by the
// Root's lease within seconds. Only a chassis whose Backhaul never associates
// keeps it -- which is also the chassis nobody is watching.
//
// A netifd reload cannot fix this, and that is the whole reason this exists:
// netifd believes it already applied /24, so a reload finds nothing to do. This
// is the same trap as the management bridge, and needs the same medicine -- act
// on the kernel, not on netifd's opinion of it.
//
// The rule is deliberately narrow: correct an address that is present with the
// wrong prefix, and never invent one that is absent. An Agent handing over to
// the Root spends a moment with br-lan flushed between udhcpc's deconfig and its
// bound; a watchdog that added the static address back during that gap would
// leave 10.10.18.1 sitting on the bridge, and the handoff's own completion test
// is "an address that is not 10.10.18.1" -- so it would wait out its timeout and
// report no-root-dhcp-lease on a handoff that had actually worked.
func reconcileClientBridgeAddress() bool {
	proto, ipaddr, netmask := uciClientLANAddress()
	// Only a static LAN makes a claim about its own prefix. Under DHCP the
	// address belongs to whoever issued it.
	if proto != "static" || ipaddr == "" || netmask == "" {
		return false
	}
	prefix, ok := netmaskPrefix(netmask)
	if !ok {
		return false
	}
	want := ipaddr + "/" + strconv.Itoa(prefix)
	addresses, err := clientBridgeIPv4()
	if err != nil {
		return false
	}
	held := false
	var wrong []string
	for _, address := range addresses {
		if address == want {
			held = true
			continue
		}
		// Other addresses on this bridge are somebody else's business.
		if strings.HasPrefix(address, ipaddr+"/") {
			wrong = append(wrong, address)
		}
	}
	if len(wrong) == 0 {
		return false
	}
	// Add before removing, so the bridge is never left without an address.
	// The kernel holds the same address under two prefixes quite happily --
	// verified on the device, where adding the /24 alongside the stray /8
	// succeeded rather than colliding.
	if !held {
		if out, err := changeClientBridgeAddress("add", want); err != nil {
			log.Printf("Client bridge watchdog: could not put %s back on %s: %v output=%s",
				want, clientBridgeName, err, out)
			return false
		}
	}
	repaired := false
	for _, address := range wrong {
		if out, err := changeClientBridgeAddress("del", address); err != nil {
			log.Printf("Client bridge watchdog: could not remove %s from %s: %v output=%s",
				address, clientBridgeName, err, out)
			continue
		}
		log.Printf("Client bridge watchdog: %s carried %s where the configuration asks for %s; corrected",
			clientBridgeName, address, want)
		repaired = true
	}
	return repaired
}
