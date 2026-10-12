package main

import (
	"os"
	"strings"
	"testing"
)

// The property the whole patch rests on: once applied, the text the guard looks
// for is gone, so a second run is a no-op without needing a marker or a flag.
func TestVendorBridgeScanPatchIsIdempotentByConstruction(t *testing.T) {
	if strings.Contains(vendorBridgeScanFixed, vendorBridgeScan) {
		t.Fatalf("the patched form %q still contains the unpatched form %q, so it would be applied forever",
			vendorBridgeScanFixed, vendorBridgeScan)
	}
	if !strings.Contains(vendorBridgeScanFixed, clientBridgeName) {
		t.Fatalf("the patched scan does not name the client bridge: %q", vendorBridgeScanFixed)
	}
}

// It must be harmless on a chassis whose script is missing or already correct,
// because it runs on every start.
func TestVendorBridgePatchDoesNothingWhenThereIsNothingToDo(t *testing.T) {
	command := vendorBridgeSelectionPatchCommand()
	for _, guard := range []string{
		"test -f " + shellSingleQuote(vendorEasyMeshScriptPath) + " || exit 0",
		"grep -qF " + shellSingleQuote(vendorBridgeScan) + " " + shellSingleQuote(vendorEasyMeshScriptPath) + " || exit 0",
	} {
		if !strings.Contains(command, guard) {
			t.Fatalf("the patch is not guarded by %q: %s", guard, command)
		}
	}
}

// The vendor copy is kept, and kept before anything is edited.
func TestVendorBridgePatchBacksUpBeforeEditing(t *testing.T) {
	command := vendorBridgeSelectionPatchCommand()
	backup := strings.Index(command, "cp ")
	edit := strings.Index(command, "sed -i ")
	if backup < 0 || edit < 0 {
		t.Fatalf("expected a copy and an edit: %s", command)
	}
	if backup > edit {
		t.Fatalf("the script is edited before it is backed up: %s", command)
	}
	if !strings.Contains(command, shellSingleQuote(vendorEasyMeshScriptBackupPath())) {
		t.Fatalf("the backup does not go to %s: %s", vendorEasyMeshScriptBackupPath(), command)
	}
}

// A failure has to be reported, not swallowed, and must not stop the start.
func TestVendorBridgePatchSurvivesAFailure(t *testing.T) {
	real := runVendorBridgeSelectionPatch
	t.Cleanup(func() { runVendorBridgeSelectionPatch = real })
	runVendorBridgeSelectionPatch = func() (string, error) { return "", os.ErrPermission }
	repairVendorBridgeSelection()
}

// The script is run by resumeEasyMeshAtStartup, so the correction has to be in
// place before that, or the first run of every boot still uses the broken scan.
func TestVendorBridgePatchRunsBeforeTheEngineResumes(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read source: %v", err)
	}
	body := string(source)
	patch := strings.Index(body, "repairVendorBridgeSelection()")
	resume := strings.Index(body, "resumeEasyMeshAtStartup()")
	if patch < 0 {
		t.Fatal("startup never corrects the vendor script")
	}
	if resume < 0 || patch > resume {
		t.Fatal("the vendor script is corrected after the engine has already resumed")
	}
}
