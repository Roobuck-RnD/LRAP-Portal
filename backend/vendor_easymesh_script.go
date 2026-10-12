package main

import (
	"log"
	"strings"
)

// The MediaTek EasyMesh script this product starts the engine with, and which
// easyMeshStartCommandWithReload runs on every Enable Mesh and every startup
// reconciliation.
var vendorEasyMeshScriptPath = "/usr/bin/EasyMesh_openwrt.sh"

// How that script looks for the bridge to work on, and what it has to look for
// instead.
//
// It scans the whole of ifconfig for anything matching "br" and keeps every
// match:
//
//	ifconfig | grep "br" > br_name
//	bridge_name=`cat br_name | awk '{ print $1}'`
//
// There is no head, and the awk prints a field per line, so on a chassis that
// carries the Antenna management bridge as well, bridge_name comes out as two
// words -- "br-lan" and "br-mgmt" -- and every use of it is unquoted. Measured
// on the device, that turns the script's own calls into:
//
//	ifconfig br-lan br-mgmt 10.10.18.1   -> bad address, exit 1, nothing set
//	brctl addif br-lan br-mgmt ra0       -> the kernel refuses a bridge inside
//	                                        a bridge, and ra0 is dropped
//
// busybox brctl takes the first interface argument and silently ignores the
// rest, so the radio never reaches the bridge and nothing reports a problem.
// This is why the client bridge watchdog has work to do at all: the damage was
// read as "a network reload drops the radios", but a reload is only half of it.
// We introduced the second bridge, so we broke the scan.
//
// Narrowing the scan to the client bridge is the whole fix. It is idempotent by
// construction -- once applied, the unpatched text is no longer in the file --
// and a firmware upgrade that restores the vendor copy simply gets patched
// again on the next start.
const (
	vendorBridgeScan      = `grep "br" > br_name`
	vendorBridgeScanFixed = `grep "` + clientBridgeName + `" > br_name`
)

func vendorEasyMeshScriptBackupPath() string {
	return vendorEasyMeshScriptPath + ".lrap-orig"
}

// vendorBridgeSelectionPatchCommand narrows that scan, keeping the vendor copy
// alongside it. It does nothing at all when the script is absent or already
// correct, so it is safe to run on every start.
func vendorBridgeSelectionPatchCommand() string {
	script := shellSingleQuote(vendorEasyMeshScriptPath)
	return "test -f " + script + " || exit 0; " +
		"grep -qF " + shellSingleQuote(vendorBridgeScan) + " " + script + " || exit 0; " +
		"cp " + script + " " + shellSingleQuote(vendorEasyMeshScriptBackupPath()) + "; " +
		"sed -i " + shellSingleQuote("s@"+vendorBridgeScan+"@"+vendorBridgeScanFixed+"@g") + " " + script + "; " +
		"echo patched"
}

var runVendorBridgeSelectionPatch = func() (string, error) {
	return easyMeshExecLocal(vendorBridgeSelectionPatchCommand())
}

// repairVendorBridgeSelection makes the vendor script able to find the client
// bridge again. It is quiet when there was nothing to do.
func repairVendorBridgeSelection() {
	out, err := runVendorBridgeSelectionPatch()
	if err != nil {
		log.Printf("Vendor EasyMesh script: could not narrow its bridge scan: %v output=%s", err, out)
		return
	}
	if !strings.Contains(out, "patched") {
		return
	}
	log.Printf("Vendor EasyMesh script: its bridge scan now matches only %s, so it can put the radios back in the bridge itself; the vendor copy is kept at %s",
		clientBridgeName, vendorEasyMeshScriptBackupPath())
}
