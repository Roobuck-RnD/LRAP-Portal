package main

import (
	"bufio"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	managedAntennaDHCPTag         = "rbap"
	managedAntennaVendorClass     = "RoobuckAP"
	managedAntennaDHCPSection     = "rbap_pool"
	managedAntennaLeaseTime       = "5m"
	managedAntennaVendorSection   = "rbap_vc"
	ordinaryClientDHCPSection     = "lan"
	managedAntennaDHCPLeasePath   = "/tmp/dhcp.leases"
	protectedDHCPReconcileTimeout = 60 * time.Second
)

type protectedDHCPSection struct {
	Type    string
	Options map[string]string
	Lists   map[string][]string
}

type managedAntennaDHCPLease struct {
	MAC      string
	IP       string
	Hostname string
}

var protectedDHCPMu sync.Mutex

// parseProtectedDHCPExport keeps option and list values separate. This matters
// because OpenWrt's dnsmasq init script only consumes DHCP range tags declared
// with `list tag`; `option tag` looks similar in `uci show` but is ignored.
func parseProtectedDHCPExport(raw string) map[string]protectedDHCPSection {
	sections := make(map[string]protectedDHCPSection)
	currentName := ""

	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}

		switch fields[0] {
		case "config":
			currentName = strings.Trim(fields[2], `"'`)
			if currentName == "" {
				continue
			}
			sections[currentName] = protectedDHCPSection{
				Type:    strings.Trim(fields[1], `"'`),
				Options: map[string]string{},
				Lists:   map[string][]string{},
			}
		case "option", "list":
			if currentName == "" {
				continue
			}
			section := sections[currentName]
			key := strings.Trim(fields[1], `"'`)
			value := strings.Trim(strings.Join(fields[2:], " "), `"'`)
			if fields[0] == "list" {
				section.Lists[key] = append(section.Lists[key], value)
			} else {
				section.Options[key] = value
			}
			sections[currentName] = section
		}
	}

	return sections
}

func protectedDHCPConfigHealthy(raw string) bool {
	sections := parseProtectedDHCPExport(raw)

	ordinary, ok := sections[ordinaryClientDHCPSection]
	if !ok || !stringSliceExactly(ordinary.Lists["tag"], "!"+managedAntennaDHCPTag) {
		return false
	}

	vendor, ok := sections[managedAntennaVendorSection]
	if !ok ||
		vendor.Type != "vendorclass" ||
		vendor.Options["networkid"] != managedAntennaDHCPTag ||
		vendor.Options["vendorclass"] != managedAntennaVendorClass {
		return false
	}

	pool, ok := sections[managedAntennaDHCPSection]
	if !ok ||
		pool.Type != "dhcp" ||
		pool.Options["interface"] != managedAntennaDHCPInterface() ||
		pool.Options["start"] != "2" ||
		pool.Options["limit"] != "4" ||
		// Included so units still carrying the original 12h lease are repaired on
		// upgrade rather than staying vulnerable to pool exhaustion.
		pool.Options["leasetime"] != managedAntennaLeaseTime ||
		!stringSliceExactly(pool.Lists["tag"], managedAntennaDHCPTag) {
		return false
	}

	return true
}

// managedAntennaDHCPInterface picks the network the managed-Antenna pool serves.
//
// The isolation marker alone is not enough. A Mesh rollback that fails partway
// can leave the marker behind after the VLAN devices are gone, and this pool
// then points at an "antenna_mgmt" interface that does not exist. dnsmasq
// answers every client on the box with "no address available" — WiFi associates
// and nothing gets an address, on the Antenna pool AND the ordinary LAN pool.
// What decides it is therefore whether the management network is CONFIGURED,
// not whether netifd has finished bringing it up. Reading the live address here
// looked equivalent and is not: this repair runs at startup, before the boot
// network has settled, so every reboot in isolated mode found no address yet,
// concluded the device was not isolated, and rebound the Antenna pool onto the
// client network. Measured on the Controller after the reboot that Enable Mesh
// performs: the Antennas then never got a management address, fell back to a
// client-network lease, and disappeared from discovery, which only ever probes
// the fixed management pools.
func managedAntennaDHCPInterface() string {
	if antennaManagementIsIsolated() && easyMeshManagementNetworkConfigured() {
		return "antenna_mgmt"
	}
	return "lan"
}

// easyMeshManagementNetworkConfigured reports whether the isolated Antenna
// management network still exists in configuration: both the interface and the
// bridge it rides on, so a rollback that removed either one is not mistaken for
// a working management plane.
var easyMeshManagementNetworkConfigured = func() bool {
	device, deviceErr := easyMeshExecLocal("uci -q get network.antenna_mgmt.device")
	bridge, bridgeErr := easyMeshExecLocal("uci -q get network.mesh_mgmt_bridge.name")
	return deviceErr == nil && bridgeErr == nil &&
		strings.TrimSpace(device) == AntennaManagementBridge &&
		strings.TrimSpace(bridge) == AntennaManagementBridge
}

func stringSliceExactly(values []string, expected string) bool {
	return len(values) == 1 && strings.TrimSpace(values[0]) == expected
}

func readProtectedDHCPExport() (string, error) {
	out, err := exec.Command("uci", "-q", "export", "dhcp").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("uci export dhcp failed: %w output=%s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// ensureProtectedDHCPConfig creates the internal Antenna pool and, critically,
// stores both range selectors as UCI lists. Do not replace either add_list call
// with `uci set ...tag=...`: a scalar tag is silently omitted by OpenWrt when it
// generates dnsmasq's dhcp-range directives.
func ensureProtectedDHCPConfig(commit bool, restartDNSMasq bool) (bool, error) {
	protectedDHCPMu.Lock()
	defer protectedDHCPMu.Unlock()

	raw, err := readProtectedDHCPExport()
	if err == nil && protectedDHCPConfigHealthy(raw) {
		return false, nil
	}

	commands := [][]string{
		{"set", "dhcp." + managedAntennaVendorSection + "=vendorclass"},
		{"set", "dhcp." + managedAntennaVendorSection + ".networkid=" + managedAntennaDHCPTag},
		{"set", "dhcp." + managedAntennaVendorSection + ".vendorclass=" + managedAntennaVendorClass},
		{"set", "dhcp." + managedAntennaDHCPSection + "=dhcp"},
		{"set", "dhcp." + managedAntennaDHCPSection + ".interface=" + managedAntennaDHCPInterface()},
		{"set", "dhcp." + managedAntennaDHCPSection + ".start=2"},
		{"set", "dhcp." + managedAntennaDHCPSection + ".limit=4"},
		// Short lease on purpose. The pool holds only four addresses and an
		// Antenna's DHCP client MAC is regenerated on every boot, so each reboot
		// burns one lease. With a 12h lease four reboots exhausted the pool: the
		// Antenna then got no management address at all — its 1905 daemon kept
		// transmitting (visible in the AC's bridge FDB) while ARP, ping, ubus and
		// SSH were all dead, which looks exactly like a bricked unit. A short
		// lease lets the stale entries age out on their own.
		{"set", "dhcp." + managedAntennaDHCPSection + ".leasetime=" + managedAntennaLeaseTime},
		{"set", "dhcp." + managedAntennaDHCPSection + ".dynamicdhcp=1"},
		{"set", "dhcp." + managedAntennaDHCPSection + ".ignore=0"},
	}

	for _, args := range commands {
		if out, runErr := exec.Command("uci", args...).CombinedOutput(); runErr != nil {
			return false, fmt.Errorf("uci %s failed: %w output=%s", strings.Join(args, " "), runErr, strings.TrimSpace(string(out)))
		}
	}

	for _, section := range []struct {
		Name string
		Tag  string
	}{
		{Name: ordinaryClientDHCPSection, Tag: "!" + managedAntennaDHCPTag},
		{Name: managedAntennaDHCPSection, Tag: managedAntennaDHCPTag},
	} {
		// Missing options are expected on first install, so ignore delete errors.
		_ = exec.Command("uci", "-q", "delete", "dhcp."+section.Name+".tag").Run()
		if out, addErr := exec.Command("uci", "add_list", "dhcp."+section.Name+".tag="+section.Tag).CombinedOutput(); addErr != nil {
			return false, fmt.Errorf("uci add_list dhcp.%s.tag failed: %w output=%s", section.Name, addErr, strings.TrimSpace(string(out)))
		}
	}

	if commit {
		if out, commitErr := exec.Command("uci", "commit", "dhcp").CombinedOutput(); commitErr != nil {
			return false, fmt.Errorf("uci commit dhcp failed: %w output=%s", commitErr, strings.TrimSpace(string(out)))
		}
	}

	if restartDNSMasq {
		if out, restartErr := exec.Command("/etc/init.d/dnsmasq", "restart").CombinedOutput(); restartErr != nil {
			return false, fmt.Errorf("dnsmasq restart after DHCP protection repair failed: %w output=%s", restartErr, strings.TrimSpace(string(out)))
		}
	}

	return true, nil
}

func parseManagedAntennaDHCPLeases(raw string) []managedAntennaDHCPLease {
	leases := make([]managedAntennaDHCPLease, 0)
	scanner := bufio.NewScanner(strings.NewReader(raw))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 || !strings.EqualFold(fields[3], managedAntennaVendorClass) {
			continue
		}
		mac := firmwareNormalizeMAC(fields[1])
		ip := strings.TrimSpace(fields[2])
		if mac == "" || ip == "" {
			continue
		}
		leases = append(leases, managedAntennaDHCPLease{
			MAC:      mac,
			IP:       ip,
			Hostname: fields[3],
		})
	}
	return leases
}

func readManagedAntennaDHCPLeases() ([]managedAntennaDHCPLease, error) {
	raw, err := os.ReadFile(managedAntennaDHCPLeasePath)
	if err != nil {
		return nil, fmt.Errorf("read %s failed: %w", managedAntennaDHCPLeasePath, err)
	}
	return parseManagedAntennaDHCPLeases(string(raw)), nil
}

func isAntennaManagementIP(ip string) bool {
	for _, pool := range [][]string{LegacyAPManagementIPs(), isolatedAPManagementIPs} {
		for _, managedIP := range pool {
			if strings.TrimSpace(ip) == managedIP {
				return true
			}
		}
	}
	return false
}

func findManagedAntennaDHCPDrift() []managedAntennaDHCPLease {
	leases, err := readManagedAntennaDHCPLeases()
	if err != nil {
		return nil
	}

	portByMAC := listLanEdgePortsByMAC()
	drift := make([]managedAntennaDHCPLease, 0)
	for _, lease := range leases {
		if isAntennaManagementIP(lease.IP) {
			continue
		}

		port := strings.TrimSpace(portByMAC[strings.ToUpper(lease.MAC)])
		if lanPortNumber(port) == 0 {
			continue
		}

		// Hostname matching alone is not trusted. A real managed Antenna must also
		// answer the same anonymous ubus read used by firmware precheck.
		if _, readErr := firmwareReadRemoteText(lease.IP, firmwareLRAPVersionPath); readErr != nil {
			continue
		}
		drift = append(drift, lease)
	}

	sort.Slice(drift, func(i, j int) bool {
		return drift[i].IP < drift[j].IP
	})
	return drift
}

func scheduleManagedAntennaDHCPRenew(ip string) error {
	command := `( sleep 2; ubus call network.interface.lan renew >/tmp/lrap-dhcp-renew.log 2>&1 || { ifdown lan; sleep 1; ifup lan; } ) </dev/null >/dev/null 2>&1 & echo LRAP_DHCP_RENEW_OK`
	res, err := firmwareUbusCallAtWithTimeout(ip, AnonSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", command},
	}, 12*time.Second)
	if err != nil {
		return fmt.Errorf("schedule DHCP renew on %s failed: %w", ip, err)
	}

	if stdout, ok := res["stdout"].(string); ok &&
		strings.TrimSpace(stdout) != "" &&
		!strings.Contains(stdout, "LRAP_DHCP_RENEW_OK") {
		return fmt.Errorf("unexpected DHCP renew response from %s: %s", ip, strings.TrimSpace(stdout))
	}
	return nil
}

func waitForManagedAntennaMACsInPool(macs []string, timeout time.Duration) error {
	wanted := make(map[string]bool, len(macs))
	for _, mac := range macs {
		if normalized := firmwareNormalizeMAC(mac); normalized != "" {
			wanted[normalized] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		leases, err := readManagedAntennaDHCPLeases()
		if err == nil {
			found := make(map[string]bool, len(wanted))
			for _, lease := range leases {
				if wanted[lease.MAC] && isAntennaManagementIP(lease.IP) {
					found[lease.MAC] = true
				}
			}
			if len(found) == len(wanted) {
				return nil
			}
		}
		time.Sleep(2 * time.Second)
	}
	return fmt.Errorf("timed out waiting for %d Antenna DHCP lease(s) to return to 10.10.18.2-5", len(wanted))
}

// reconcileManagedAntennaDHCPDrift moves a verified Antenna that somehow holds
// an ordinary client lease back into the protected pool. With the range tags
// repaired, its renewed request can only receive 10.10.18.2-5.
func reconcileManagedAntennaDHCPDrift(timeout time.Duration) (int, error) {
	protectedDHCPMu.Lock()
	defer protectedDHCPMu.Unlock()

	drift := findManagedAntennaDHCPDrift()
	if len(drift) == 0 {
		return 0, nil
	}

	ips := make([]string, 0, len(drift))
	macs := make([]string, 0, len(drift))
	for _, lease := range drift {
		ips = append(ips, lease.IP)
		macs = append(macs, lease.MAC)
	}

	if _, err := firmwareClearLocalDHCPLeasesForIPs(ips); err != nil {
		return 0, err
	}
	for _, lease := range drift {
		if err := scheduleManagedAntennaDHCPRenew(lease.IP); err != nil {
			return 0, err
		}
	}
	if timeout > 0 {
		if err := waitForManagedAntennaMACsInPool(macs, timeout); err != nil {
			return len(drift), err
		}
	}
	return len(drift), nil
}

func repairProtectedDHCPAtStartup() {
	changed, err := ensureProtectedDHCPConfig(true, true)
	if err != nil {
		log.Printf("protected DHCP startup repair failed: %v", err)
		return
	}
	if changed {
		log.Printf("protected DHCP tags repaired; dnsmasq restarted")
	}

	go func() {
		time.Sleep(2 * time.Second)
		count, reconcileErr := reconcileManagedAntennaDHCPDrift(protectedDHCPReconcileTimeout)
		if reconcileErr != nil {
			log.Printf("protected DHCP Antenna reconciliation failed: %v", reconcileErr)
			return
		}
		if count > 0 {
			log.Printf("protected DHCP moved %d Antenna lease(s) back to 10.10.18.2-5", count)
		}
	}()
}
