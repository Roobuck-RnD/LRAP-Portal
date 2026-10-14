package main

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ensureAntennaManagementVLAN puts Antenna management on its own VLAN and keeps
// it there, whether or not this chassis is in a Mesh.
//
// Antennas used to be managed on the client LAN at fixed addresses 10.10.18.2-5,
// and the isolated VLAN was something Enable Mesh switched on. Having two
// arrangements was the root of a whole family of faults, every one of them
// caused by the transition rather than by either arrangement:
//
//   - the Antenna pool was rebound between the client LAN and the management
//     bridge, and a boot that ran the repair before netifd was up rebound it the
//     wrong way, after which no Antenna could get a management address;
//   - a rollback restored an Antenna to untagged operation while the chassis had
//     already moved to tagged ports, orphaning it where nothing could reach it;
//   - discovery probed whichever pool the current mode named, so an Antenna on
//     the other side of the switch was simply invisible.
//
// With one permanent arrangement none of those transitions exist.
//
// This only creates what is missing. Migrating an Antenna that is still talking
// untagged is a separate step -- see adoptUntaggedAntennas -- because it needs
// the Antenna pool to be serving the management bridge first.
func ensureAntennaManagementVLAN() {
	// Configured is not the same as up, and this used to treat them as one.
	//
	// Measured on a chassis whose br-mgmt had been removed underneath it: the
	// configuration was still complete, so this returned immediately and never
	// rebuilt anything -- while the only other repair, ensureEasyMeshManagementBridge,
	// sits behind resumeEasyMeshAtStartup's `!cfg.NetworkPrepared` guard, and
	// NetworkPrepared is computed from whether that very plane is up. The repair
	// was gated on the condition it existed to repair, so the chassis stayed
	// broken across restarts: Antennas unreachable, no management addresses, and
	// the portal logging a bridge error against a device that did not exist.
	configured := easyMeshManagementNetworkConfigured()
	if configured && easyMeshManagementRuntimeReady() {
		return
	}
	if configured {
		log.Printf("Antenna management: the management VLAN is configured but not up; rebuilding %s", AntennaManagementBridge)
		if out, err := easyMeshExecLocal(easyMeshRebuildManagementBridgeCommand()); err != nil {
			log.Printf("Antenna management: could not rebuild %s: %v output=%s", AntennaManagementBridge, err, out)
			return
		}
		if !easyMeshManagementRuntimeReady() {
			log.Printf("Antenna management: %s did not come back; the next start tries again", AntennaManagementBridge)
			return
		}
		log.Printf("Antenna management: %s rebuilt from the existing configuration", AntennaManagementBridge)
		return
	}
	log.Printf("Antenna management: creating the management VLAN")
	if out, err := easyMeshExecLocal(easyMeshPrepareACManagementCommand()); err != nil {
		// Not fatal: the chassis keeps serving clients and the next start tries
		// again. Antennas stay unreachable until it succeeds, which the Antenna
		// pages already report as an offline module.
		log.Printf("Antenna management: could not create the management VLAN: %v output=%s", err, out)
		return
	}
	// Creating the plane ends in a network reload, which rebuilds br-lan without
	// this chassis's own radios. Put them back before returning rather than
	// leaving the chassis unreachable over the air until the watchdog next runs.
	settleClientBridgeAfterReload()
	log.Printf("Antenna management: management VLAN ready on %s", AntennaManagementBridge)
}

// migrateUntaggedAntennasAtStartup brings any Antenna still talking untagged --
// a factory-fresh replacement, or one left behind by an older build -- onto the
// management VLAN. It must run after the Antenna DHCP pool has been bound to the
// management bridge, because that pool is what answers the Antenna once its port
// is briefly bridged across.
func migrateUntaggedAntennasAtStartup() {
	if adopted := adoptUntaggedAntennas(); adopted > 0 {
		log.Printf("Antenna management: moved %d Antenna(s) onto the management VLAN", adopted)
	}
}

// alignAntennaClientPlanes makes sure every reachable Antenna carries client
// traffic the way the chassis expects: untagged, with no address of its own
// on the client plane, and with its management lease identified by radio.
//
// Only the management plane is tagged, so an Antenna left on the old client VLAN
// -- by an earlier build, or by a preparation that stopped half way -- is still
// perfectly reachable for management while every device behind it is cut off:
// the AC's IP stack never sees frames tagged for a VLAN it does not carry.
// Measured: associated, holding a stale lease, no route to anything.
//
// That state is invisible to adoption, which only looks for Antennas it cannot
// reach at all, so it is corrected here on its own terms -- and so is the rest
// of the client plane, for the same reason. It runs at startup and on every pass
// of the adoption scan, and holds the adoption lock so it never pushes to an
// Antenna that is mid-adoption.
func alignAntennaClientPlanes() {
	if !antennaManagementIsIsolated() {
		return
	}
	antennaAdoptionMu.Lock()
	defer antennaAdoptionMu.Unlock()
	for _, module := range discoverManagedAPsForAlignment() {
		if tagged, err := antennaClientPlaneIsTagged(module.IP); err == nil && tagged {
			log.Printf("Antenna management: %s still tags client traffic; returning it to untagged", module.ModuleID)
			if _, err := easyMeshExecRemote(module.IP, easyMeshAntennaUntaggedClientCommand()); err != nil &&
				!easyMeshRemoteDispatchResultAmbiguous(err) {
				log.Printf("Antenna management: could not return client traffic to untagged for the Antenna at %s: %v",
					module.IP, err)
				continue
			}
			log.Printf("Antenna management: the Antenna at %s now carries client traffic untagged", module.IP)
		}
		alignAntennaClientPlane(module)
	}
}

// How many times a client-plane repair is attempted before the pass gives up on
// it, and how long each attempt waits before reading the result back. Vars so
// tests need not wait.
var (
	antennaClientPlaneAttempts   = 3
	antennaClientPlaneRetryDelay = 3 * time.Second
)

// antennaClientPlaneStateCommand reads everything the alignment checks in one
// round trip. It always exits 0, so a missing option is an empty field rather
// than an error that would look the same as an unreachable Antenna.
const antennaClientPlaneStateCommand = `printf '%s|%s|%s|%s' "$(uci -q get network.lan.proto)" "$(uci -q get network.lan.vendorid)" "$(uci -q get network.antenna_mgmt.clientid)" "$(cat /sys/class/net/ra0/address 2>/dev/null)"`

// antennaClientPlaneState is what an Antenna reports about its client plane and
// the identity of its management lease.
//
// The client plane must take no address. The Antenna only bridges there, and a
// second DHCP client on the same MAC is what let dnsmasq forget a management
// lease and hand the address to another Antenna -- see
// easyMeshAntennaUntaggedClientCommand. Nor may it announce RoobuckAP: that
// identity belongs to the management interface, and on the client plane it gets
// the request refused once a second for as long as the Antenna runs -- measured
// at 471 of the 517 lines the Root's syslog could hold.
//
// The management lease is identified by client-id 01:<ra0 MAC>. The radio's
// address is the factory one and never changes; eth1's is regenerated on every
// boot, so a lease keyed on it is abandoned at each restart and holds a pool
// address for five minutes after the Antenna has already asked for another.
// Measured during an upgrade of four Antennas: the pool of four ran dry and
// management went quiet for five minutes until the orphaned leases expired.
type antennaClientPlaneState struct {
	LanProto     string
	LanVendorID  string
	MgmtClientID string
	RadioMAC     string
}

func parseAntennaClientPlaneState(raw string) antennaClientPlaneState {
	fields := strings.SplitN(strings.TrimSpace(raw), "|", 4)
	for len(fields) < 4 {
		fields = append(fields, "")
	}
	return antennaClientPlaneState{
		LanProto:     strings.TrimSpace(fields[0]),
		LanVendorID:  strings.TrimSpace(fields[1]),
		MgmtClientID: strings.TrimSpace(fields[2]),
		RadioMAC:     strings.ToLower(strings.TrimSpace(fields[3])),
	}
}

// wantManagementClientID is the client-id the management lease should carry,
// or "" when the radio could not be read and there is nothing to derive it from.
func (s antennaClientPlaneState) wantManagementClientID() string {
	if s.RadioMAC == "" {
		return ""
	}
	return "01:" + s.RadioMAC
}

// drift lists what is wrong with the state, and is empty when nothing is.
func (s antennaClientPlaneState) drift() []string {
	var wrong []string
	if s.LanProto != "none" {
		wrong = append(wrong, "the client plane takes an address (proto="+s.LanProto+")")
	}
	if s.LanVendorID != "" {
		wrong = append(wrong, "the client plane announces "+s.LanVendorID)
	}
	if want := s.wantManagementClientID(); want != "" && !strings.EqualFold(s.MgmtClientID, want) {
		have := s.MgmtClientID
		if have == "" {
			have = "none"
		}
		wrong = append(wrong, "the management lease is identified by "+have+", not "+want)
	}
	return wrong
}

// easyMeshAntennaClientPlaneRepairCommand puts the client plane and the
// management lease identity right, and touches nothing else: not the bridge
// ports, and not the management interface's device, protocol or vendor class.
func easyMeshAntennaClientPlaneRepairCommand() string {
	return "uci set network.lan.proto=none; " +
		"for o in ipaddr netmask gateway dns vendorid clientid; do uci -q delete network.lan.$o 2>/dev/null || true; done; " +
		"m=$(cat /sys/class/net/ra0/address 2>/dev/null); if [ -n \"$m\" ]; then uci set network.antenna_mgmt.clientid=\"01:$m\"; fi; " +
		"uci commit network; ubus call network reload"
}

// antennaAddressOnPort finds the management address the Antenna on a port holds
// now. A repair can move it -- a new client-id is a new DHCP client as far as
// dnsmasq is concerned -- so the read-back follows the port, not the address it
// started from. Falls back to that address when the port cannot be resolved.
var antennaAddressOnPort = func(port int, fallback string) string {
	if port < 1 {
		return fallback
	}
	for _, module := range discoverManagedAPs(true) {
		if module.PortIndex == port {
			return module.IP
		}
	}
	return fallback
}

// alignAntennaClientPlane corrects one Antenna's client plane and management
// lease identity, and reports whether it had to change anything.
//
// Success is decided by reading the state back, never by the reply to the
// repair: the repair ends in a network reload, and a reply lost to that reload
// is exactly how an Antenna was once left with the management half of adoption
// done and the client half not.
func alignAntennaClientPlane(module ManagedModule) bool {
	raw, err := easyMeshExecRemote(module.IP, antennaClientPlaneStateCommand)
	if err != nil {
		return false
	}
	wrong := parseAntennaClientPlaneState(raw).drift()
	if len(wrong) == 0 {
		return false
	}
	log.Printf("Antenna management: correcting the Antenna at %s: %s", module.IP, strings.Join(wrong, "; "))
	ip := module.IP
	for attempt := 1; attempt <= antennaClientPlaneAttempts; attempt++ {
		if _, err := easyMeshExecRemote(ip, easyMeshAntennaClientPlaneRepairCommand()); err != nil &&
			!easyMeshRemoteDispatchResultAmbiguous(err) {
			log.Printf("Antenna management: could not correct the Antenna at %s: %v", ip, err)
		}
		time.Sleep(antennaClientPlaneRetryDelay)
		ip = antennaAddressOnPort(module.PortIndex, ip)
		if raw, err := easyMeshExecRemote(ip, antennaClientPlaneStateCommand); err == nil &&
			len(parseAntennaClientPlaneState(raw).drift()) == 0 {
			log.Printf("Antenna management: the Antenna at %s now bridges clients without an address of its own and identifies its management lease by radio", ip)
			return true
		}
	}
	log.Printf("Antenna management: the Antenna at %s is still not corrected after %d attempts; the next pass tries again",
		ip, antennaClientPlaneAttempts)
	return false
}

// antennaClientPlaneIsTagged reports whether the Antenna's client bridge is
// still on the retired client VLAN rather than untagged.
func antennaClientPlaneIsTagged(ip string) (bool, error) {
	out, err := easyMeshExecRemote(ip, "uci -q get network.@device[0].ports")
	if err != nil {
		return false, err
	}
	for _, port := range strings.Fields(out) {
		if strings.HasSuffix(port, "."+strconv.Itoa(retiredClientVLAN)) {
			return true, nil
		}
	}
	return false, nil
}

// A seam so the alignment can be exercised without a network.
var discoverManagedAPsForAlignment = func() []ManagedModule {
	return discoverManagedAPs(false)
}

// narrowAntennaManagementVLAN removes management legs from ports that carry
// ordinary equipment. It is quiet when there is nothing to remove, so it is safe
// on every start.
func narrowAntennaManagementVLAN() {
	out, err := easyMeshExecLocal(easyMeshNarrowManagementVLANCommand())
	if err != nil {
		log.Printf("Antenna management: could not narrow the management VLAN: %v output=%s", err, out)
		return
	}
	if !strings.Contains(out, "narrowed") {
		return
	}
	log.Printf("Antenna management: the management VLAN now covers only %s; rebuilding %s",
		strings.Join(antennaManagementPortNames(), ", "), AntennaManagementBridge)
	if out, err := easyMeshExecLocal(easyMeshRebuildManagementBridgeCommand()); err != nil {
		log.Printf("Antenna management: could not rebuild %s after narrowing: %v output=%s", AntennaManagementBridge, err, out)
	}
}

// How often a chassis looks for an Antenna plugged in since the last check.
var antennaAdoptionScanInterval = 60 * time.Second

// startAntennaAdoptionScan adopts an Antenna that appears after the chassis has
// already started.
//
// Adoption used to run only at startup and at a few Mesh transitions, so a module
// plugged into a running chassis was never picked up: it asks for DHCP every
// three seconds with vendor class RoobuckAP, the client pool is tagged !rbap and
// refuses it by design, and its own pool lives on a bridge it cannot reach until
// somebody lends it a port. Measured on the Root -- a second Antenna sat on lan2
// for twelve minutes emitting DHCPDISCOVER, answered by nothing, while the log
// held no adoption attempt at all.
//
// It acts only on a port whose carrier has just come up, never on one that has
// been up all along. Adoption decides what to touch from carrier alone, so a
// pass that ran on every tick would lend out the port of any non-Antenna device
// on lan1-4 once a minute and take its network away each time. Bounded to the
// moment something is plugged in, that costs such a device no more than the
// single interruption it already got at startup.
// antennaPortsJustPluggedIn reports the ports whose carrier has come up since
// the previous look, in port order.
//
// A nil previous means there was no previous look, and nothing counts as having
// just arrived. That case is the process start: the switch PHYs are still coming
// up then, so a baseline taken at that moment records every port as down and the
// next pass reads the whole chassis as freshly plugged in. Measured after a cold
// boot -- two Antennas that had not moved were both announced as just arrived.
// Whatever was already connected at start belongs to the startup pass anyway.
func antennaPortsJustPluggedIn(previous map[int]bool, current map[int]bool) []int {
	if previous == nil {
		return nil
	}
	ports := make([]int, 0, len(current))
	for port, up := range current {
		if up && !previous[port] {
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

// How many consecutive passes a port is retried before it is left alone.
//
// A module that has just been plugged in is booting, because the cable carries
// its power and unplugging it is a power cycle. One attempt does not span that:
// measured on the Root, adoption gave up on lan1 after its 45 seconds and the
// module took its management address 33 seconds later. Three passes cover a cold
// boot, and being a fixed budget rather than a standing condition is what keeps
// a socket holding no Antenna from being lent out once a minute for ever.
var antennaAdoptionRetries = 3

// antennaAdoptionPortsToTry decides which ports this pass should attempt and
// spends their budget.
//
// A port is forgotten as soon as it stops being worth trying: it lost carrier,
// or it is no longer an orphan because the module behind it answered, whether
// adoption is what got it there or it simply finished booting on its own. Only
// a port that is still an orphan and still has budget is returned.
func antennaAdoptionPortsToTry(budget map[int]int, carrier map[int]bool, orphans []int) []int {
	isOrphan := make(map[int]bool, len(orphans))
	for _, port := range orphans {
		isOrphan[port] = true
	}
	for port := range budget {
		if !carrier[port] || !isOrphan[port] {
			delete(budget, port)
		}
	}
	ports := make([]int, 0, len(orphans))
	for _, port := range orphans {
		if budget[port] > 0 {
			budget[port]--
			ports = append(ports, port)
		}
	}
	sort.Ints(ports)
	return ports
}

// startAntennaAdoptionScan adopts an Antenna that appears after the chassis has
// already started, and keeps trying for a few passes while it boots.
//
// Adoption used to run only at startup and at a few Mesh transitions, so a
// module plugged into a running chassis was never picked up: it asks for DHCP
// every three seconds with vendor class RoobuckAP, the client pool is tagged
// !rbap and refuses it by design, and its own pool lives on a bridge it cannot
// reach until somebody lends it a port. Measured on the Root, a second Antenna
// sat on lan2 for twelve minutes emitting DHCPDISCOVER, answered by nothing,
// while the log held no adoption attempt at all.
//
// The first pass always looks, because the pass at process start cannot be
// relied on: measured on a cold boot, the switch brought lan2 and lan3 up 27
// seconds in and lan1 at 34, flapping again at 39 and 48, while the startup pass
// ran around 40. It read carriers that were still settling, found no port to
// work on, and returned without a word.
//
// After that a port earns attention by having just come up. Adoption decides
// what to lend out from carrier alone, so a pass that ran on every tick
// regardless would take the network away from any non-Antenna device on the
// Antenna ports, once a minute. A budget spent over a few passes gives a booting
// module the time it needs without turning into that.
func startAntennaAdoptionScan() {
	go func() {
		var previous map[int]bool
		budget := map[int]int{}
		for {
			time.Sleep(antennaAdoptionScanInterval)
			current := managedAntennaCarrierByPort()
			if previous == nil {
				for port, up := range current {
					if up {
						budget[port] = antennaAdoptionRetries
					}
				}
			} else {
				for _, port := range antennaPortsJustPluggedIn(previous, current) {
					log.Printf("Antenna management: something was plugged into lan%d; looking for an Antenna to adopt", port)
					budget[port] = antennaAdoptionRetries
				}
			}
			previous = current

			if len(budget) > 0 {
				orphans := easyMeshOrphanedAntennaPorts(managedAntennaLinkedPortIndexes(), discoverManagedAPs(true))
				if ports := antennaAdoptionPortsToTry(budget, current, orphans); len(ports) > 0 {
					if adopted := adoptUntaggedAntennasOnPorts(ports); adopted > 0 {
						log.Printf("Antenna management: adopted %d Antenna(s) onto the management VLAN", adopted)
					}
					for _, port := range ports {
						if budget[port] == 0 {
							log.Printf("Antenna management: nothing on lan%d answered as an Antenna; leaving it until something is plugged in there again", port)
						}
					}
				}
			}

			// Every pass, not only when something was plugged in. An Antenna that is
			// already reachable never looks like an orphan, so nothing else would
			// notice one that is mis-configured -- and the one that prompted this was
			// reachable, working, and filling the syslog at a line a second.
			alignAntennaClientPlanes()
		}
	}()
}
