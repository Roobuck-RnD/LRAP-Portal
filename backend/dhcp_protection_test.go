package main

import (
	"testing"
)

func TestProtectedDHCPConfigHealthyRequiresListTags(t *testing.T) {
	healthy := `
config dhcp 'lan'
	option interface 'lan'
	option start '100'
	option limit '150'
	list tag '!rbap'

config vendorclass 'rbap_vc'
	option networkid 'rbap'
	option vendorclass 'RoobuckAP'

config dhcp 'rbap_pool'
	option interface 'lan'
	option start '2'
	option limit '4'
	option leasetime '5m'
	list tag 'rbap'
`
	if !protectedDHCPConfigHealthy(healthy) {
		t.Fatal("expected list-based protected DHCP config to be healthy")
	}

	scalarOrdinaryTag := `
config dhcp 'lan'
	option interface 'lan'
	option tag '!rbap'

config vendorclass 'rbap_vc'
	option networkid 'rbap'
	option vendorclass 'RoobuckAP'

config dhcp 'rbap_pool'
	option interface 'lan'
	option start '2'
	option limit '4'
	list tag 'rbap'
`
	if protectedDHCPConfigHealthy(scalarOrdinaryTag) {
		t.Fatal("scalar ordinary-pool tag must be rejected because dnsmasq ignores it")
	}

	scalarAntennaTag := `
config dhcp 'lan'
	list tag '!rbap'

config vendorclass 'rbap_vc'
	option networkid 'rbap'
	option vendorclass 'RoobuckAP'

config dhcp 'rbap_pool'
	option interface 'lan'
	option start '2'
	option limit '4'
	option tag 'rbap'
`
	if protectedDHCPConfigHealthy(scalarAntennaTag) {
		t.Fatal("scalar Antenna-pool tag must be rejected because dnsmasq ignores it")
	}
}

func TestParseManagedAntennaDHCPLeases(t *testing.T) {
	raw := `
1785153206 9e:55:7b:c2:30:16 10.10.18.216 RoobuckAP *
1785153237 f4:3b:d8:7f:cc:40 10.10.18.248 DESKTOP-KC17O22 01:f4:3b:d8:7f:cc:40
1785153184 c2:fe:cf:11:42:78 10.10.18.3 roobuckap *
malformed
`
	leases := parseManagedAntennaDHCPLeases(raw)
	if len(leases) != 2 {
		t.Fatalf("got %d managed Antenna leases, want 2", len(leases))
	}
	if leases[0].MAC != "9e:55:7b:c2:30:16" || leases[0].IP != "10.10.18.216" {
		t.Fatalf("unexpected first lease: %+v", leases[0])
	}
	if leases[1].IP != "10.10.18.3" {
		t.Fatalf("unexpected second lease: %+v", leases[1])
	}
}

func TestIsAntennaManagementIP(t *testing.T) {
	for _, ip := range APManagementIPs() {
		if !isAntennaManagementIP(ip) {
			t.Fatalf("%s should be recognized as an Antenna management IP", ip)
		}
	}
	for _, ip := range []string{"10.10.18.1", "10.10.18.100", "10.10.18.216", ""} {
		if isAntennaManagementIP(ip) {
			t.Fatalf("%s must not be recognized as an Antenna management IP", ip)
		}
	}
}

// The Antenna pool is bound from configuration, not from whether netifd has
// finished bringing the bridge up. Reading the live address made every reboot in
// isolated mode rebind the pool onto the client network, because this repair
// runs before the boot network has settled: measured on the Controller after the
// reboot Enable Mesh performs.
func TestAntennaPoolBindingSurvivesAnUnsettledBootNetwork(t *testing.T) {
	defer restoreAntennaPoolSeams(t)()
	antennaManagementIsIsolated = func() bool { return true }
	// Configured, but with no address yet -- exactly the state at boot.
	easyMeshManagementNetworkConfigured = func() bool { return true }

	if got := managedAntennaDHCPInterface(); got != "antenna_mgmt" {
		t.Fatalf("interface = %q while the boot network was still settling, want antenna_mgmt", got)
	}
}

// A rollback that tore the management network down must send the pool back to
// the client network: binding it to an interface that no longer exists makes
// dnsmasq answer every client on the box with "no address available".
func TestAntennaPoolFallsBackWhenTheManagementNetworkIsGone(t *testing.T) {
	defer restoreAntennaPoolSeams(t)()
	antennaManagementIsIsolated = func() bool { return true }
	easyMeshManagementNetworkConfigured = func() bool { return false }

	if got := managedAntennaDHCPInterface(); got != "lan" {
		t.Fatalf("interface = %q with no management network configured, want lan", got)
	}
}

// Without the isolation marker the device is not in isolated mode at all.
func TestAntennaPoolUsesTheClientNetworkWhenNotIsolated(t *testing.T) {
	defer restoreAntennaPoolSeams(t)()
	antennaManagementIsIsolated = func() bool { return false }
	easyMeshManagementNetworkConfigured = func() bool { return true }

	if got := managedAntennaDHCPInterface(); got != "lan" {
		t.Fatalf("interface = %q on a standalone unit, want lan", got)
	}
}

func restoreAntennaPoolSeams(t *testing.T) func() {
	t.Helper()
	isolated := antennaManagementIsIsolated
	configured := easyMeshManagementNetworkConfigured
	return func() {
		antennaManagementIsIsolated = isolated
		easyMeshManagementNetworkConfigured = configured
	}
}
