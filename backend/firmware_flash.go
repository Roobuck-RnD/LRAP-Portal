package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const firmwareMaxUploadBytes int64 = 256 << 20 // 256 MB
const firmwareDownloadTTL = 30 * time.Minute

const firmwareBundleMagic = "LRAPFW01"
const firmwareMaxManifestBytes = 1 << 20 // 1 MB

const firmwareLRAPVersionPath = "/etc/lrap.version"
const firmwareRemoteTimeout = 8 * time.Minute
const firmwareRemoteCallTimeout = 6 * time.Second
const firmwareUpgradeJobTTL = 2 * time.Hour

type FirmwareFlashResult struct {
	IP      string `json:"ip"`
	Name    string `json:"name,omitempty"`
	Role    string `json:"role,omitempty"`   // bundle / sub / main / ap / ac
	Stage   string `json:"stage,omitempty"`  // dispatch / wait / verify / flash
	Status  string `json:"status,omitempty"` // running / ok / failed
	OK      bool   `json:"ok"`
	Detail  string `json:"detail,omitempty"`
	Error   string `json:"error,omitempty"`
	Version string `json:"version,omitempty"`
}

type FirmwareFlashResponse struct {
	OK         bool                  `json:"ok"`
	Message    string                `json:"message,omitempty"`
	TargetType string                `json:"target_type,omitempty"`
	Version    string                `json:"version,omitempty"`
	JobID      string                `json:"job_id,omitempty"`
	Status     string                `json:"status,omitempty"`
	Done       bool                  `json:"done,omitempty"`
	Results    []FirmwareFlashResult `json:"results,omitempty"`
}

type firmwareDownloadItem struct {
	Path      string
	FileName  string
	ExpiresAt time.Time
}

type firmwareBundlePart struct {
	Role   string `json:"role"`
	Model  string `json:"model"`
	Offset int64  `json:"offset"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

type firmwareBundleManifest struct {
	Format  string               `json:"format"`
	Version string               `json:"version"`
	Parts   []firmwareBundlePart `json:"parts"`
}

type firmwareUnpackedBundle struct {
	Version  string
	Manifest firmwareBundleManifest
	MainPath string
	SubPath  string
}

type firmwareAPUpgradeState struct {
	IP           string
	MAC          string
	BeforeBootID string
}

type firmwareUpgradeJob struct {
	ID         string
	TargetType string
	Version    string
	Status     string
	Message    string
	OK         bool
	Done       bool
	Results    []FirmwareFlashResult
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

var firmwareJobStore = struct {
	sync.Mutex
	Items map[string]*firmwareUpgradeJob
}{
	Items: map[string]*firmwareUpgradeJob{},
}

var firmwareDownloadStore = struct {
	sync.Mutex
	Items map[string]firmwareDownloadItem
}{
	Items: map[string]firmwareDownloadItem{},
}

// ---------- Routes ----------
//
// Register in main.go:
//
// mux.HandleFunc("/api/system/firmware/flash", firmwareFlashHandler)
// mux.HandleFunc("/api/system/firmware/download", firmwareDownloadHandler)

// ---------- Auth ----------

func firmwareAuthorized(r *http.Request) bool {
	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	return strings.HasPrefix(strings.ToLower(auth), "bearer ") && len(auth) > len("bearer ")
}

// ---------- Browser upload endpoint ----------
//
// New bundle mode:
//   target_type=bundle or target_type omitted
//   Browser uploads one LRAPFW01 .bin package.
//   AC unpacks and validates main/sub parts, flashes antennas first, waits for them,
//   then starts AC sysupgrade last.
//
// Compatibility mode still exists:
//   target_type=ac -> raw AC image, local sysupgrade
//   target_type=ap -> raw AP image, dispatch to selected antennas only

func firmwareFlashHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		firmwareJobStatusHandler(w, r)
		return
	}

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	if !firmwareAuthorized(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	// Important on MT7981/OpenWrt:
	// Do not use ParseMultipartForm(64 << 20) here. A 33 MB LRAP bundle can be
	// kept in Go heap before being copied again, and the kernel OOM killer can
	// kill lrapServer. Stream the multipart file directly to one temporary file.
	r.Body = http.MaxBytesReader(w, r.Body, firmwareMaxUploadBytes)

	upload, err := firmwareReadUploadRequest(r)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":"parse upload failed: %v"}`, err), http.StatusBadRequest)
		return
	}

	tmpPath := upload.FirmwarePath
	removeUpload := true
	defer func() {
		if removeUpload && tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()

	targetType := strings.ToLower(strings.TrimSpace(upload.TargetType))
	if targetType == "" {
		targetType = "bundle"
	}

	keepSettings := upload.KeepSettings

	if targetType != "bundle" && targetType != "ac" && targetType != "ap" {
		http.Error(w, `{"error":"target_type must be bundle, ac, or ap"}`, http.StatusBadRequest)
		return
	}

	switch targetType {
	case "bundle":
		resp, err := firmwareStartBundleUpgradeJob(r, tmpPath, keepSettings, upload.TargetIPs)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, firmwareJSONSafeError(err)), http.StatusInternalServerError)
			return
		}

		// The bundle has been split into main/sub images. Remove the uploaded
		// container immediately so /tmp tmpfs does not keep duplicate firmware bytes.
		_ = os.Remove(tmpPath)
		removeUpload = false
		_ = json.NewEncoder(w).Encode(resp)

	case "ac":
		firmwareStartLocalSysupgrade(tmpPath, keepSettings)
		// Keep the file; sysupgrade needs to read it after this HTTP handler returns.
		removeUpload = false

		_ = json.NewEncoder(w).Encode(FirmwareFlashResponse{
			OK:         true,
			TargetType: "ac",
			Message:    "AC firmware upload complete. sysupgrade has been started. The AC will reboot shortly.",
			Results: []FirmwareFlashResult{{
				Role:  "ac",
				Stage: "flash",
				OK:    true,
			}},
		})

	case "ap":
		resp, err := firmwareHandleLegacyAPUpgrade(r, tmpPath, upload.FirmwareName, keepSettings, upload.TargetIPs)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, firmwareJSONSafeError(err)), http.StatusInternalServerError)
			return
		}
		// Keep the file while antennas download it through /api/system/firmware/download.
		removeUpload = false
		_ = json.NewEncoder(w).Encode(resp)
	}
}

// firmwareReadUploadRequest streams the multipart upload instead of buffering it
// in memory. This is critical on OpenWrt devices where /tmp is tmpfs and RAM is
// limited. Only small text fields are read into memory; the firmware image is
// copied straight to a temp file with a fixed-size buffer.
type firmwareUploadRequest struct {
	TargetType   string
	KeepSettings bool
	TargetIPs    []string
	FirmwarePath string
	FirmwareName string
}

func firmwareReadUploadRequest(r *http.Request) (firmwareUploadRequest, error) {
	reader, err := r.MultipartReader()
	if err != nil {
		return firmwareUploadRequest{}, err
	}

	upload := firmwareUploadRequest{
		TargetType:   "bundle",
		KeepSettings: true,
	}

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			if upload.FirmwarePath != "" {
				_ = os.Remove(upload.FirmwarePath)
			}
			return firmwareUploadRequest{}, err
		}

		name := part.FormName()
		switch name {
		case "firmware":
			if upload.FirmwarePath != "" {
				_ = os.Remove(upload.FirmwarePath)
				return firmwareUploadRequest{}, fmt.Errorf("multiple firmware files supplied")
			}

			path, filename, err := firmwareSaveUploadPart(part)
			if err != nil {
				return firmwareUploadRequest{}, err
			}
			upload.FirmwarePath = path
			upload.FirmwareName = filename

		case "target_type":
			value, err := firmwareReadSmallMultipartField(part)
			if err != nil {
				if upload.FirmwarePath != "" {
					_ = os.Remove(upload.FirmwarePath)
				}
				return firmwareUploadRequest{}, err
			}
			upload.TargetType = strings.ToLower(strings.TrimSpace(value))

		case "keep_settings":
			value, err := firmwareReadSmallMultipartField(part)
			if err != nil {
				if upload.FirmwarePath != "" {
					_ = os.Remove(upload.FirmwarePath)
				}
				return firmwareUploadRequest{}, err
			}
			upload.KeepSettings = firmwareParseBool(value, true)

		case "target_ips":
			value, err := firmwareReadSmallMultipartField(part)
			if err != nil {
				if upload.FirmwarePath != "" {
					_ = os.Remove(upload.FirmwarePath)
				}
				return firmwareUploadRequest{}, err
			}
			upload.TargetIPs = firmwareParseTargetIPs(value)

		default:
			// Drain and ignore unknown fields so multipart parsing can continue.
			_, _ = io.Copy(io.Discard, part)
		}
	}

	if upload.FirmwarePath == "" {
		return firmwareUploadRequest{}, fmt.Errorf("missing firmware file")
	}
	if upload.FirmwareName == "" {
		upload.FirmwareName = "firmware.bin"
	}

	return upload, nil
}

func firmwareReadSmallMultipartField(part *multipart.Part) (string, error) {
	const maxFieldBytes int64 = 64 << 10

	data, err := io.ReadAll(io.LimitReader(part, maxFieldBytes+1))
	if err != nil {
		return "", err
	}
	if int64(len(data)) > maxFieldBytes {
		return "", fmt.Errorf("multipart field %q is too large", part.FormName())
	}
	return string(data), nil
}

func firmwareSaveUploadPart(part *multipart.Part) (string, string, error) {
	filename := filepath.Base(part.FileName())
	if filename == "." || filename == "/" || filename == "" {
		filename = "firmware.bin"
	}

	tmp, err := os.CreateTemp("/tmp", "lrap-firmware-upload-*.bin")
	if err != nil {
		return "", "", err
	}

	path := tmp.Name()
	buf := make([]byte, 128<<10)
	written, copyErr := io.CopyBuffer(tmp, part, buf)
	closeErr := tmp.Close()

	if copyErr != nil {
		_ = os.Remove(path)
		return "", "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", "", closeErr
	}
	if written <= 0 {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("empty firmware file")
	}

	return path, filename, nil
}

// ---------- LRAP bundle upgrade ----------

func firmwareStartBundleUpgradeJob(r *http.Request, uploadedPath string, keepSettings bool, requestedTargetIPs []string) (FirmwareFlashResponse, error) {
	bundle, err := firmwareUnpackLRAPBundle(uploadedPath)
	if err != nil {
		return FirmwareFlashResponse{}, err
	}

	results := []FirmwareFlashResult{
		{
			Role:    "bundle",
			Stage:   "validate",
			Status:  "ok",
			OK:      true,
			Version: bundle.Version,
		},
	}

	// The protected .2-.5 range is discovery capacity, not the installed module
	// count. Snapshot carrier-up/proven physical ports so a 1-4 Antenna product
	// upgrades every installed module without waiting for empty LAN ports.
	registry := discoverManagedAPs(true)
	expectedPorts := currentManagedAntennaPortIndexes(registry)
	if len(expectedPorts) == 0 {
		_ = os.Remove(bundle.MainPath)
		_ = os.Remove(bundle.SubPath)
		return FirmwareFlashResponse{}, fmt.Errorf("no installed antenna targets found")
	}

	// Probe the complete protected range because an installed Antenna may be
	// between DHCP addresses while the job is starting.
	targetIPs := firmwareMergeIPs(APManagementIPs(), requestedTargetIPs)

	if len(targetIPs) == 0 {
		_ = os.Remove(bundle.MainPath)
		_ = os.Remove(bundle.SubPath)
		return FirmwareFlashResponse{}, fmt.Errorf("no antenna targets found")
	}

	downloadID, err := firmwareRegisterDownload(bundle.SubPath, "lrap-sub-"+bundle.Version+".bin")
	if err != nil {
		_ = os.Remove(bundle.MainPath)
		_ = os.Remove(bundle.SubPath)
		return FirmwareFlashResponse{}, fmt.Errorf("create antenna firmware download token failed: %w", err)
	}

	downloadURL := firmwareBuildDownloadURL(r, downloadID)

	jobID, resp := firmwareCreateUpgradeJob("bundle", bundle.Version, "Package accepted. Antenna upgrade is running in the background.", results)

	go firmwareRunBundleUpgradeJob(jobID, bundle, downloadURL, keepSettings, targetIPs, expectedPorts)

	return resp, nil
}

func firmwareRunBundleUpgradeJob(jobID string, bundle firmwareUnpackedBundle, downloadURL string, keepSettings bool, targetIPs []string, expectedPorts []int) {
	acStarted := false

	defer func() {
		// Sub image can be removed once AP stage is finished. The download store may also
		// remove it later; double remove is safe.
		_ = os.Remove(bundle.SubPath)

		// Keep main image only when local sysupgrade was started, because sysupgrade needs
		// to read it from /tmp until reboot. If AC was not flashed, clean it up.
		if !acStarted {
			_ = os.Remove(bundle.MainPath)
		}
	}()

	firmwareUpdateJob(jobID, "running", "Checking protected Antenna DHCP addressing before firmware precheck.", true, false)
	configChanged, configErr := ensureProtectedDHCPConfig(true, true)
	if configErr != nil {
		firmwareAppendJobResult(jobID, FirmwareFlashResult{
			Role: "dhcp", Stage: "protect", Status: "failed", OK: false, Error: configErr.Error(),
		})
		firmwareUpdateJob(jobID, "failed", "Protected Antenna DHCP configuration could not be verified. Firmware was not flashed.", false, true)
		return
	}
	if configChanged {
		firmwareAppendJobResult(jobID, FirmwareFlashResult{
			Role: "dhcp", Stage: "protect", Status: "ok", OK: true,
			Detail: "Repaired the internal Antenna/client DHCP range tags and restarted dnsmasq.",
		})
	}

	reconciled, reconcileErr := reconcileManagedAntennaDHCPDrift(protectedDHCPReconcileTimeout)
	if reconcileErr != nil {
		firmwareAppendJobResult(jobID, FirmwareFlashResult{
			Role: "dhcp", Stage: "reconcile", Status: "failed", OK: false, Error: reconcileErr.Error(),
		})
		firmwareUpdateJob(jobID, "failed", "An Antenna address could not be returned to the protected management pool. Firmware was not flashed.", false, true)
		return
	}
	if reconciled > 0 {
		firmwareAppendJobResult(jobID, FirmwareFlashResult{
			Role: "dhcp", Stage: "reconcile", Status: "ok", OK: true,
			Detail: fmt.Sprintf("Returned %d Antenna lease(s) to the protected 10.10.18.2-5 pool.", reconciled),
		})
	}

	resolvedTargets, resolveErr := firmwareWaitForManagedTargetsByPhysicalPort(targetIPs, expectedPorts, protectedDHCPReconcileTimeout)
	if resolveErr != nil {
		firmwareAppendJobResult(jobID, FirmwareFlashResult{
			Role: "sub", Stage: "precheck", Status: "failed", OK: false, Error: resolveErr.Error(),
		})
		firmwareUpdateJob(jobID, "failed", "Not all physical Antenna ports could be resolved to protected management addresses. Firmware was not flashed.", false, true)
		return
	}
	targetIPs = resolvedTargets

	firmwareUpdateJob(jobID, "running", "Prechecking antennas with ping and ubus before firmware dispatch.", true, false)

	states, precheckOK := firmwarePrecheckAPsAndUpdateJob(jobID, targetIPs)
	if len(states) == 0 {
		firmwareUpdateJob(jobID, "failed", "No antenna targets passed precheck. antenna firmware was not flashed, and AC firmware was not flashed.", false, true)
		return
	}

	if !precheckOK {
		firmwareUpdateJob(jobID, "failed", "At least one antenna failed ping/ubus precheck. Antenna firmware was not flashed, and AC firmware was not flashed.", false, true)
		return
	}

	firmwareUpdateJob(jobID, "running", "All antennas passed precheck. Dispatching antenna firmware upgrade commands.", true, false)

	dispatchOK := true
	for _, st := range states {
		result := FirmwareFlashResult{
			IP:     st.IP,
			Role:   "sub",
			Stage:  "dispatch",
			Status: "failed",
			OK:     false,
		}

		if err := firmwareCommandAPWgetAndFlash(st.IP, downloadURL, keepSettings); err != nil {
			result.Error = err.Error()
			dispatchOK = false
		} else {
			result.OK = true
			result.Status = "ok"
		}

		firmwareAppendJobResult(jobID, result)
		time.Sleep(800 * time.Millisecond)
	}

	if !dispatchOK {
		firmwareUpdateJob(jobID, "failed", "Antenna firmware dispatch failed for at least one antenna. AC firmware was not flashed.", false, true)
		return
	}

	// Important timing fix:
	// Do not clear the AP DHCP leases immediately after dispatch. The AP worker sleeps
	// before wget/sysupgrade so /ubus can return cleanly. If dnsmasq is restarted while
	// the old AP OS is still alive, the old MAC can immediately reclaim .2-.5 before
	// the real sysupgrade reboot. Wait until the antennas have entered the reboot/upgrade
	// window, then clear the AP IP/MAC leases.
	fwCleanupWaitResult := firmwareWaitBeforeDHCPLeaseCleanup(jobID, states, 75*time.Second, 180*time.Second)
	firmwareCleanupWaitDetail := fwCleanupWaitResult.Detail
	firmwareAppendJobResult(jobID, fwCleanupWaitResult)

	firmwareUpdateJob(jobID, "running", "Clearing old DHCP leases after antennas entered the upgrade/reboot window.", true, false)
	removedLeases, cleanupErr := firmwareClearCapturedAntennaLeases(states)
	cleanupResult := FirmwareFlashResult{
		Role:   "dhcp",
		Stage:  "cleanup",
		Status: "ok",
		OK:     true,
		Detail: fmt.Sprintf("Removed %d stale pre-upgrade Antenna lease record(s). New reachable leases were preserved. %s Restarted dnsmasq.", removedLeases, firmwareCleanupWaitDetail),
	}
	if cleanupErr != nil {
		cleanupResult.Status = "failed"
		cleanupResult.OK = false
		cleanupResult.Error = cleanupErr.Error()
		firmwareAppendJobResult(jobID, cleanupResult)
		firmwareUpdateJob(jobID, "failed", "DHCP lease cleanup failed. antenna firmware may still be running, but AC firmware was not flashed.", false, true)
		return
	}
	firmwareAppendJobResult(jobID, cleanupResult)

	if len(states) > 0 {
		firmwareUpdateJob(
			jobID,
			"running",
			fmt.Sprintf("Waiting for antennas to reboot and verify version. 0/%d completed.", len(states)),
			true,
			false,
		)

		waitResults := firmwareWaitForAPsAndUpdateJob(jobID, states, bundle.Version, firmwareRemoteTimeout)

		allAPsOK := true
		for _, r := range waitResults {
			if !r.OK {
				allAPsOK = false
				break
			}
		}

		if !allAPsOK {
			firmwareUpdateJob(jobID, "failed", "At least one antenna did not come back with the expected LRAP version. AC firmware was not flashed.", false, true)
			return
		}
	}

	firmwareUpdateJob(jobID, "running", "All antennas are verified. Starting AC firmware upgrade now.", true, false)

	firmwareStartLocalSysupgrade(bundle.MainPath, keepSettings)
	acStarted = true

	firmwareAppendJobResult(jobID, FirmwareFlashResult{
		IP:      firmwareDetectACLANIP(),
		Role:    "main",
		Stage:   "flash",
		Status:  "ok",
		OK:      true,
		Version: bundle.Version,
	})

	firmwareUpdateJob(jobID, "ac_rebooting", "All antennas are verified. AC sysupgrade has been started; the portal will reboot shortly.", true, true)
}

func firmwareJobStatusHandler(w http.ResponseWriter, r *http.Request) {
	if !firmwareAuthorized(r) {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	jobID := strings.TrimSpace(r.URL.Query().Get("job_id"))
	if jobID == "" {
		http.Error(w, `{"error":"missing job_id"}`, http.StatusBadRequest)
		return
	}

	firmwareCleanupOldJobs()

	resp, ok := firmwareGetUpgradeJob(jobID)
	if !ok {
		http.Error(w, `{"error":"job not found or expired"}`, http.StatusNotFound)
		return
	}

	_ = json.NewEncoder(w).Encode(resp)
}

func firmwareCreateUpgradeJob(targetType string, version string, message string, results []FirmwareFlashResult) (string, FirmwareFlashResponse) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		// Extremely unlikely. Keep the job usable even if crypto/rand fails.
		idBytes = []byte(fmt.Sprintf("%016x", time.Now().UnixNano()))[:16]
	}

	id := hex.EncodeToString(idBytes)
	now := time.Now()

	job := &firmwareUpgradeJob{
		ID:         id,
		TargetType: targetType,
		Version:    version,
		Status:     "running",
		Message:    message,
		OK:         true,
		Done:       false,
		Results:    append([]FirmwareFlashResult(nil), results...),
		CreatedAt:  now,
		UpdatedAt:  now,
	}

	firmwareJobStore.Lock()
	firmwareJobStore.Items[id] = job
	firmwareJobStore.Unlock()

	return id, firmwareJobSnapshot(job)
}

func firmwareGetUpgradeJob(jobID string) (FirmwareFlashResponse, bool) {
	firmwareJobStore.Lock()
	defer firmwareJobStore.Unlock()

	job, ok := firmwareJobStore.Items[jobID]
	if !ok {
		return FirmwareFlashResponse{}, false
	}

	return firmwareJobSnapshot(job), true
}

func firmwareAppendJobResult(jobID string, result FirmwareFlashResult) {
	firmwareJobStore.Lock()
	defer firmwareJobStore.Unlock()

	job, ok := firmwareJobStore.Items[jobID]
	if !ok {
		return
	}

	job.Results = append(job.Results, result)
	job.UpdatedAt = time.Now()
}

func firmwareUpdateJob(jobID string, status string, message string, ok bool, done bool) {
	firmwareJobStore.Lock()
	defer firmwareJobStore.Unlock()

	job, exists := firmwareJobStore.Items[jobID]
	if !exists {
		return
	}

	job.Status = status
	job.Message = message
	job.OK = ok
	job.Done = done
	job.UpdatedAt = time.Now()
}

func firmwareJobSnapshot(job *firmwareUpgradeJob) FirmwareFlashResponse {
	if job == nil {
		return FirmwareFlashResponse{}
	}

	results := append([]FirmwareFlashResult(nil), job.Results...)

	return FirmwareFlashResponse{
		OK:         job.OK,
		Message:    job.Message,
		TargetType: job.TargetType,
		Version:    job.Version,
		JobID:      job.ID,
		Status:     job.Status,
		Done:       job.Done,
		Results:    results,
	}
}

func firmwareCleanupOldJobs() {
	now := time.Now()

	firmwareJobStore.Lock()
	defer firmwareJobStore.Unlock()

	for id, job := range firmwareJobStore.Items {
		if now.Sub(job.UpdatedAt) > firmwareUpgradeJobTTL {
			delete(firmwareJobStore.Items, id)
		}
	}
}

func firmwareUnpackLRAPBundle(path string) (firmwareUnpackedBundle, error) {
	f, err := os.Open(path)
	if err != nil {
		return firmwareUnpackedBundle{}, err
	}
	defer f.Close()

	stat, err := f.Stat()
	if err != nil {
		return firmwareUnpackedBundle{}, err
	}
	bundleSize := stat.Size()
	if bundleSize < 12 {
		return firmwareUnpackedBundle{}, fmt.Errorf("bad magic: not a LRantenna firmware package")
	}

	header := make([]byte, 12)
	if _, err := io.ReadFull(f, header); err != nil {
		return firmwareUnpackedBundle{}, fmt.Errorf("read bundle header failed: %w", err)
	}

	if string(header[:8]) != firmwareBundleMagic {
		return firmwareUnpackedBundle{}, fmt.Errorf("bad magic: not a LRantenna firmware package")
	}

	manifestLen := binary.BigEndian.Uint32(header[8:12])
	if manifestLen == 0 || manifestLen > firmwareMaxManifestBytes {
		return firmwareUnpackedBundle{}, fmt.Errorf("invalid manifest length: %d", manifestLen)
	}

	manifestStart := int64(12)
	manifestEnd := manifestStart + int64(manifestLen)
	if manifestEnd > bundleSize {
		return firmwareUnpackedBundle{}, fmt.Errorf("truncated manifest")
	}

	manifestBytes := make([]byte, manifestLen)
	if _, err := io.ReadFull(f, manifestBytes); err != nil {
		return firmwareUnpackedBundle{}, fmt.Errorf("read manifest failed: %w", err)
	}

	var manifest firmwareBundleManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		return firmwareUnpackedBundle{}, fmt.Errorf("manifest json parse failed: %w", err)
	}

	if manifest.Format != firmwareBundleMagic {
		return firmwareUnpackedBundle{}, fmt.Errorf("manifest format mismatch: %s", manifest.Format)
	}

	payloadStart := manifestEnd
	payloadSize := bundleSize - payloadStart
	parts := map[string]string{}
	createdPaths := make([]string, 0, len(manifest.Parts))

	cleanupCreated := func() {
		for _, p := range createdPaths {
			_ = os.Remove(p)
		}
	}

	for _, part := range manifest.Parts {
		role := strings.ToLower(strings.TrimSpace(part.Role))
		if role != "main" && role != "sub" {
			cleanupCreated()
			return firmwareUnpackedBundle{}, fmt.Errorf("unsupported firmware role: %s", part.Role)
		}
		if _, exists := parts[role]; exists {
			cleanupCreated()
			return firmwareUnpackedBundle{}, fmt.Errorf("duplicate firmware part role: %s", role)
		}
		if part.Offset < 0 || part.Size <= 0 || part.Offset > payloadSize || part.Size > payloadSize-part.Offset {
			cleanupCreated()
			return firmwareUnpackedBundle{}, fmt.Errorf("part %s out of range", role)
		}

		outPath, got, err := firmwareCopyBundlePartToTemp(f, payloadStart+part.Offset, part.Size, role)
		if err != nil {
			cleanupCreated()
			return firmwareUnpackedBundle{}, fmt.Errorf("write %s image failed: %w", role, err)
		}
		createdPaths = append(createdPaths, outPath)

		want := strings.ToLower(strings.TrimSpace(part.SHA256))
		if got != want {
			cleanupCreated()
			return firmwareUnpackedBundle{}, fmt.Errorf("sha256 mismatch for %s", role)
		}

		parts[role] = outPath
	}

	mainPath := parts["main"]
	subPath := parts["sub"]
	if mainPath == "" || subPath == "" {
		cleanupCreated()
		return firmwareUnpackedBundle{}, fmt.Errorf("bundle must contain both main and sub firmware parts")
	}

	version := strings.TrimSpace(manifest.Version)
	if version == "" {
		cleanupCreated()
		return firmwareUnpackedBundle{}, fmt.Errorf("manifest version is empty")
	}

	return firmwareUnpackedBundle{
		Version:  version,
		Manifest: manifest,
		MainPath: mainPath,
		SubPath:  subPath,
	}, nil
}

func firmwareCopyBundlePartToTemp(f *os.File, start int64, size int64, role string) (string, string, error) {
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		return "", "", err
	}

	tmp, err := os.CreateTemp("/tmp", "lrap-"+role+"-*.bin")
	if err != nil {
		return "", "", err
	}

	path := tmp.Name()
	h := sha256.New()
	buf := make([]byte, 128<<10)
	written, copyErr := io.CopyBuffer(io.MultiWriter(tmp, h), io.LimitReader(f, size), buf)
	closeErr := tmp.Close()

	if copyErr != nil {
		_ = os.Remove(path)
		return "", "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(path)
		return "", "", closeErr
	}
	if written != size {
		_ = os.Remove(path)
		return "", "", fmt.Errorf("short read: copied %d of %d bytes", written, size)
	}

	return path, hex.EncodeToString(h.Sum(nil)), nil
}

func firmwarePrecheckAPsAndUpdateJob(jobID string, targetIPs []string) ([]firmwareAPUpgradeState, bool) {
	cleanIPs := firmwareMergeIPs(targetIPs, nil)
	states := make([]firmwareAPUpgradeState, 0, len(cleanIPs))
	allOK := true

	// First ping every AP IP once. This warms the ARP table before ubus checks.
	// DHCP leases are intentionally not used here: an AP can be reachable even when
	// its old DHCP lease record was already cleared.
	pingOKByIP := make(map[string]bool, len(cleanIPs))
	for _, ip := range cleanIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}
		pingOKByIP[ip] = firmwarePingOnce(ip)
		time.Sleep(150 * time.Millisecond)
	}

	// Then verify ubus/file.read is available and capture boot_id before dispatch.
	// boot_id is later used to prove that this upgrade attempt caused an AP reboot.
	for _, ip := range cleanIPs {
		ip = strings.TrimSpace(ip)
		if ip == "" {
			continue
		}

		pingText := "ping failed"
		if pingOKByIP[ip] {
			pingText = "ping OK"
		}

		result := FirmwareFlashResult{
			IP:     ip,
			Role:   "sub",
			Stage:  "precheck",
			Status: "failed",
			OK:     false,
			Detail: pingText + "; checking ubus...",
		}

		bootID, err := firmwareReadRemoteText(ip, "/proc/sys/kernel/random/boot_id")
		if err != nil || strings.TrimSpace(bootID) == "" {
			allOK = false
			if err != nil {
				result.Error = fmt.Sprintf("%s; ubus/file.read failed: %v", pingText, err)
			} else {
				result.Error = pingText + "; ubus/file.read returned empty boot_id"
			}
			firmwareAppendJobResult(jobID, result)
			continue
		}

		mac := ""
		if capturedMAC, macErr := firmwareReadRemoteInterfaceMAC(ip); macErr == nil {
			mac = capturedMAC
		}

		result.OK = true
		result.Status = "ok"
		macDetail := ""
		if mac != "" {
			macDetail = "; management MAC captured: " + mac
		}
		if pingOKByIP[ip] {
			result.Detail = "ping OK; ubus OK; boot_id captured" + macDetail + "."
		} else {
			// Keep this OK because ubus is the real requirement for firmware dispatch.
			// Some devices can reject ICMP while HTTP ubus is still healthy.
			result.Detail = "ping failed, but ubus OK; boot_id captured" + macDetail + "."
		}
		firmwareAppendJobResult(jobID, result)

		states = append(states, firmwareAPUpgradeState{
			IP:           ip,
			MAC:          mac,
			BeforeBootID: strings.TrimSpace(bootID),
		})
	}

	return states, allOK
}

// firmwareWaitForAPsAndUpdateJob 验证 AP 是否都刷成功。**不再盯每台烧录前的固定 IP**
// (那样在"4 格池 + AP MAC 随机化"下会因鬼影租约占格而假失败),改为**池级按数量**验证:
//   - 成功 = rbap 池(.2-.5)里有 expected 台"可达 + 版本匹配 + 确实重启过(boot_id 不在
//     烧录前那组里)"的 AP。
//   - 若池已满且有"持续不可达、且未验证"的 leased IP → 判为疑似鬼影,限频清掉其租约,
//     腾出格子让被锁住的 AP 重新拿 IP。(复用已验证的 firmwareClearLocalDHCPLeasesForIPs。)
//   - 到 timeout 仍不足 expected → 失败(真变砖也走这条,不会误判成功)。
//
// 返回值语义与调用方一致:全 OK 表示成功;含任一 !OK 表示失败(调用方据此中止刷 AC)。
func firmwareWaitForAPsAndUpdateJob(jobID string, states []firmwareAPUpgradeState, expectedVersion string, timeout time.Duration) []FirmwareFlashResult {
	expected := len(states)
	poolIPs := APManagementIPs()

	preBootIDs := make(map[string]bool)
	for _, st := range states {
		if b := strings.TrimSpace(st.BeforeBootID); b != "" {
			preBootIDs[b] = true
		}
	}

	const (
		ghostUnreachableFor = 30 * time.Second // 持续不可达多久才判疑似鬼影
		ghostClearCooldown  = 60 * time.Second // 同一 IP 两次清理的最小间隔
		pollInterval        = 5 * time.Second
	)

	unreachableSince := make(map[string]time.Time) // ip -> 首次持续不可达时间
	lastCleared := make(map[string]time.Time)      // ip -> 上次清鬼影时间
	verified := make(map[string]string)            // ip -> 已确认的版本

	firmwareUpdateJob(jobID, "running",
		fmt.Sprintf("Waiting for antennas to reboot and report the new version. 0/%d verified.", expected),
		true, false)

	deadline := time.Now().Add(timeout)
	// 给 AP 一点启动时间再开始判定(与旧逻辑一致)。
	time.Sleep(10 * time.Second)

	for time.Now().Before(deadline) {
		leased := firmwarePoolLeasedIPs(poolIPs)
		now := time.Now()

		for _, ip := range poolIPs {
			if _, ok := verified[ip]; ok {
				continue
			}

			if !firmwarePingOnce(ip) {
				if unreachableSince[ip].IsZero() {
					unreachableSince[ip] = now
				}
				continue
			}
			delete(unreachableSince, ip) // 可达了,重置持续不可达计时

			version, verr := firmwareReadRemoteLRAPVersion(ip)
			if verr != nil || !firmwareVersionMatches(version, expectedVersion) {
				continue
			}
			// 必须"这次真重启过":boot_id 不在烧录前那组里,防止重刷同名版本时
			// 一台没真重刷的 AP 因版本号相同被误算成功。
			bootID := strings.TrimSpace(firmwareReadRemoteTextIgnoreErr(ip, "/proc/sys/kernel/random/boot_id"))
			if bootID == "" || preBootIDs[bootID] {
				continue
			}

			verified[ip] = version
			firmwareAppendJobResult(jobID, FirmwareFlashResult{
				IP: ip, Role: "sub", Stage: "verify", Status: "ok", OK: true, Version: version,
			})
		}

		firmwareUpdateJob(jobID, "running",
			fmt.Sprintf("Verifying antennas: %d/%d confirmed on the new version.", len(verified), expected),
			true, false)

		if len(verified) >= expected {
			break
		}

		// 池满 + 有"持续不可达、且未验证"的 leased IP → 疑似鬼影,限频清掉腾格子。
		if len(leased) >= len(poolIPs) {
			for _, ip := range poolIPs {
				if _, ok := verified[ip]; ok || !leased[ip] {
					continue
				}
				since, seen := unreachableSince[ip]
				if !seen || now.Sub(since) < ghostUnreachableFor {
					continue
				}
				if last, ok := lastCleared[ip]; ok && now.Sub(last) < ghostClearCooldown {
					continue
				}
				lastCleared[ip] = now
				if _, err := firmwareClearLocalDHCPLeasesForIPs([]string{ip}); err == nil {
					delete(unreachableSince, ip) // 给它重新拿 IP 的机会
					firmwareAppendJobResult(jobID, FirmwareFlashResult{
						IP: ip, Role: "sub", Stage: "verify", Status: "running", OK: true,
						Detail: fmt.Sprintf("%s was leased but unreachable; cleared a stale (ghost) lease so the antenna can re-acquire an address.", ip),
					})
				}
			}
		}

		time.Sleep(pollInterval)
	}

	results := make([]FirmwareFlashResult, 0, expected+1)
	for ip, ver := range verified {
		results = append(results, FirmwareFlashResult{
			IP: ip, Role: "sub", Stage: "verify", Status: "ok", OK: true, Version: ver,
		})
	}

	if len(verified) >= expected {
		firmwareUpdateJob(jobID, "running",
			fmt.Sprintf("All %d antennas verified on the new version.", expected), true, false)
		return results
	}

	firmwareUpdateJob(jobID, "running",
		fmt.Sprintf("Antenna verification failed: only %d/%d confirmed on the new version before timeout.", len(verified), expected),
		true, false)
	results = append(results, FirmwareFlashResult{
		Role: "sub", Stage: "verify", Status: "failed", OK: false,
		Error: fmt.Sprintf("only %d/%d antennas verified on the new version before timeout", len(verified), expected),
	})
	return results
}

// firmwarePoolLeasedIPs 返回 poolIPs 中当前在 /tmp/dhcp.leases 里有租约的那些 IP。
func firmwarePoolLeasedIPs(poolIPs []string) map[string]bool {
	inPool := make(map[string]bool, len(poolIPs))
	for _, ip := range poolIPs {
		inPool[ip] = true
	}

	leased := make(map[string]bool)
	raw, err := os.ReadFile("/tmp/dhcp.leases")
	if err != nil {
		return leased
	}
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 3 && inPool[fields[2]] {
			leased[fields[2]] = true
		}
	}
	return leased
}

// firmwareReadRemoteTextIgnoreErr 是 firmwareReadRemoteText 的便捷封装(出错返回空串)。
func firmwareReadRemoteTextIgnoreErr(ip, path string) string {
	text, err := firmwareReadRemoteText(ip, path)
	if err != nil {
		return ""
	}
	return text
}

func firmwareWaitBeforeDHCPLeaseCleanup(jobID string, states []firmwareAPUpgradeState, minWait time.Duration, maxWait time.Duration) FirmwareFlashResult {
	start := time.Now()
	offline := make(map[string]bool, len(states))
	total := len(states)

	if minWait < 0 {
		minWait = 0
	}
	if maxWait < minWait {
		maxWait = minWait
	}

	for {
		elapsed := time.Since(start)

		for _, st := range states {
			if st.IP == "" || offline[st.IP] {
				continue
			}
			if !firmwarePingOnce(st.IP) {
				offline[st.IP] = true
			}
		}

		if elapsed >= minWait && len(offline) == total {
			break
		}
		if elapsed >= maxWait {
			break
		}

		firmwareUpdateJob(
			jobID,
			"running",
			fmt.Sprintf("Waiting before DHCP cleanup so old antenna leases cannot be reclaimed: %d/%d antennas have gone offline, elapsed %s.", len(offline), total, elapsed.Truncate(time.Second)),
			true,
			false,
		)

		time.Sleep(5 * time.Second)
	}

	offlineIPs := make([]string, 0, len(offline))
	for _, st := range states {
		if offline[st.IP] {
			offlineIPs = append(offlineIPs, st.IP)
		}
	}

	detail := fmt.Sprintf("Waited %s after antenna dispatch before DHCP cleanup; %d/%d antennas were offline/rebooting.", time.Since(start).Truncate(time.Second), len(offline), total)
	if len(offlineIPs) > 0 {
		detail += " Offline antennas: " + strings.Join(offlineIPs, ", ") + "."
	}
	if len(offline) < total {
		detail += " Cleanup continued after max wait to avoid blocking forever."
	}

	return FirmwareFlashResult{
		Role:   "dhcp",
		Stage:  "cleanup-wait",
		Status: "ok",
		OK:     true,
		Detail: detail,
	}
}

func firmwareVersionMatches(actual string, expected string) bool {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return actual != ""
	}
	return actual == expected || strings.Contains(actual, expected)
}

func firmwareReadRemoteLRAPVersion(ip string) (string, error) {
	return firmwareReadRemoteText(ip, firmwareLRAPVersionPath)
}

func firmwareReadRemoteInterfaceMAC(ip string) (string, error) {
	// DHCP requests normally come from the LAN management bridge. On some images
	// br-lan may not exist during early boot, so fall back to eth0 and common LAN ports.
	candidates := []string{
		"/sys/class/net/br-lan/address",
		"/sys/class/net/eth0/address",
		"/sys/class/net/lan1/address",
		"/sys/class/net/wan/address",
	}

	var lastErr error
	for _, path := range candidates {
		text, err := firmwareReadRemoteText(ip, path)
		if err != nil {
			lastErr = err
			continue
		}
		mac := firmwareNormalizeMAC(text)
		if mac != "" {
			return mac, nil
		}
	}

	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("no remote interface MAC found")
}

func firmwareReadRemoteText(ip string, path string) (string, error) {

	ip = strings.TrimSpace(ip)
	path = strings.TrimSpace(path)
	if ip == "" || path == "" {
		return "", fmt.Errorf("empty ip or path")
	}

	// Prefer file.exec because AP ACLs in this project already allow it for firmware tasks.
	// Use a timeout wrapper so one unresponsive AP cannot block the whole background job forever.
	res, err := firmwareUbusCallAtWithTimeout(ip, AnonSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", "cat " + shellQuote(path) + " 2>/dev/null"},
	}, firmwareRemoteCallTimeout)
	if err == nil {
		if stdout, ok := res["stdout"].(string); ok && strings.TrimSpace(stdout) != "" {
			return strings.TrimSpace(stdout), nil
		}
		if data, ok := res["data"].(string); ok && strings.TrimSpace(data) != "" {
			return strings.TrimSpace(data), nil
		}
	}

	// Fallback to file.read when ACL allows a specific path.
	res, readErr := firmwareUbusCallAtWithTimeout(ip, AnonSID, "file", "read", map[string]any{
		"path": path,
	}, firmwareRemoteCallTimeout)
	if readErr != nil {
		if err != nil {
			return "", fmt.Errorf("remote read failed: exec=%v read=%v", err, readErr)
		}
		return "", readErr
	}

	if data, ok := res["data"].(string); ok {
		return strings.TrimSpace(data), nil
	}

	return "", fmt.Errorf("remote file read returned no data")
}

type firmwareRemoteCallResult struct {
	res map[string]any
	err error
}

func firmwareUbusCallAtWithTimeout(ip string, sid string, object string, method string, params map[string]any, timeout time.Duration) (map[string]any, error) {
	ch := make(chan firmwareRemoteCallResult, 1)

	go func() {
		res, err := ubusCallJSONAt(ip, sid, object, method, params)
		ch <- firmwareRemoteCallResult{res: res, err: err}
	}()

	select {
	case r := <-ch:
		return r.res, r.err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout after %s calling %s.%s on %s", timeout, object, method, ip)
	}
}

func firmwareAPIPsFromStates(states []firmwareAPUpgradeState) []string {
	out := make([]string, 0, len(states))
	for _, st := range states {
		ip := strings.TrimSpace(st.IP)
		if ip != "" {
			out = append(out, ip)
		}
	}
	return firmwareMergeIPs(out)
}

func firmwareAPMACsFromStates(states []firmwareAPUpgradeState) []string {
	out := make([]string, 0, len(states))
	seen := make(map[string]bool)
	for _, st := range states {
		mac := firmwareNormalizeMAC(st.MAC)
		if mac == "" || seen[mac] {
			continue
		}
		seen[mac] = true
		out = append(out, mac)
	}
	return out
}

func firmwareTargetsForPorts(registry []ManagedModule, expectedPorts []int) ([]string, []int, error) {
	expectedPorts = managedAntennaPortIndexes(expectedPorts, nil)
	expected := make(map[int]bool, len(expectedPorts))
	for _, portIndex := range expectedPorts {
		expected[portIndex] = true
	}

	byPort := make(map[int]string, len(expectedPorts))
	for _, module := range registry {
		if !expected[module.PortIndex] || !isAntennaManagementIP(module.IP) {
			continue
		}
		if existing := byPort[module.PortIndex]; existing != "" && existing != module.IP {
			return nil, nil, fmt.Errorf("multiple Antenna addresses resolved on %s", module.Port)
		}
		byPort[module.PortIndex] = module.IP
	}

	targets := make([]string, 0, len(expectedPorts))
	missing := make([]int, 0)
	for _, portIndex := range expectedPorts {
		if ip := strings.TrimSpace(byPort[portIndex]); ip != "" {
			targets = append(targets, ip)
		} else {
			missing = append(missing, portIndex)
		}
	}
	return targets, missing, nil
}

// firmwareWaitForManagedTargetsByPhysicalPort resolves only the physical
// ports installed when the job began. Fixed IP order is not device identity:
// addresses can legitimately swap between Antennas after a simultaneous reboot.
func firmwareWaitForManagedTargetsByPhysicalPort(hints []string, expectedPorts []int, timeout time.Duration) ([]string, error) {
	deadline := time.Now().Add(timeout)
	if timeout <= 0 {
		deadline = time.Now()
	}

	for {
		probeStaticIPs(firmwareMergeIPs(APManagementIPs(), hints))
		registry := discoverManagedAPs(false)
		targets, missingPorts, err := firmwareTargetsForPorts(registry, expectedPorts)
		if err != nil {
			return nil, err
		}
		if len(missingPorts) == 0 && len(targets) > 0 {
			return targets, nil
		}

		if !time.Now().Before(deadline) {
			missing := make([]string, 0, len(missingPorts))
			for _, portIndex := range missingPorts {
				missing = append(missing, fmt.Sprintf("Antenna%d/lan%d", portIndex, portIndex))
			}
			return nil, fmt.Errorf("physical Antenna discovery incomplete; missing %s", strings.Join(missing, ", "))
		}
		time.Sleep(2 * time.Second)
	}
}

// firmwareClearCapturedAntennaLeases removes the pre-upgrade MAC leases and
// only clears an old IP lease when that IP is currently unreachable. A blanket
// IP deletion can erase a valid lease already acquired by another Antenna
// after addresses swap during a simultaneous reboot.
func firmwareClearCapturedAntennaLeases(states []firmwareAPUpgradeState) (int, error) {
	macs := firmwareAPMACsFromStates(states)
	offlineIPs := make([]string, 0, len(states))
	for _, state := range states {
		ip := strings.TrimSpace(state.IP)
		if ip != "" && !firmwarePingOnce(ip) {
			offlineIPs = append(offlineIPs, ip)
		}
	}
	if len(offlineIPs) == 0 && len(macs) == 0 {
		return 0, nil
	}
	return firmwareClearLocalDHCPLeasesForIPsAndMACs(offlineIPs, macs)
}

func firmwareMergeIPs(groups ...[]string) []string {
	out := make([]string, 0)
	seen := make(map[string]bool)

	for _, group := range groups {
		for _, raw := range group {
			ip := strings.TrimSpace(raw)
			if ip == "" || seen[ip] {
				continue
			}

			// Keep only valid IPv4/IPv6 text. Product targets are IPv4, but this
			// prevents accidentally matching a malformed token in /tmp/dhcp.leases.
			if parsed := net.ParseIP(ip); parsed == nil {
				continue
			}

			seen[ip] = true
			out = append(out, ip)
		}
	}

	return out
}

func firmwareClearLocalDHCPLeasesForIPs(ips []string) (int, error) {
	return firmwareClearLocalDHCPLeasesForIPsAndMACs(ips, nil)
}

func firmwareClearLocalDHCPLeasesForIPsAndMACs(ips []string, macs []string) (int, error) {
	ips = firmwareMergeIPs(ips)
	if len(ips) == 0 && len(macs) == 0 {
		return 0, fmt.Errorf("no antenna DHCP lease IPs or MACs supplied")
	}

	removeIP := make(map[string]bool, len(ips))
	for _, ip := range ips {
		removeIP[ip] = true
	}

	removeMAC := make(map[string]bool, len(macs))
	for _, mac := range macs {
		mac = firmwareNormalizeMAC(mac)
		if mac != "" {
			removeMAC[mac] = true
		}
	}

	leasePath := "/tmp/dhcp.leases"
	raw, err := os.ReadFile(leasePath)
	if err != nil {
		if os.IsNotExist(err) {
			raw = []byte{}
		} else {
			return 0, fmt.Errorf("read %s failed: %w", leasePath, err)
		}
	}

	backupPath := "/tmp/dhcp.leases.lrap-firmware.bak"
	_ = os.WriteFile(backupPath, raw, 0644)

	removed := 0
	kept := make([]string, 0)
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		fields := strings.Fields(trimmed)
		lineMAC := ""
		lineIP := ""
		if len(fields) >= 2 {
			lineMAC = firmwareNormalizeMAC(fields[1])
		}
		if len(fields) >= 3 {
			lineIP = fields[2]
		}

		if (lineIP != "" && removeIP[lineIP]) || (lineMAC != "" && removeMAC[lineMAC]) {
			removed++
			continue
		}

		kept = append(kept, trimmed)
	}

	newContent := ""
	if len(kept) > 0 {
		newContent = strings.Join(kept, "\n") + "\n"
	}

	// Stop dnsmasq before rewriting /tmp/dhcp.leases. If we edit the file while
	// dnsmasq is running, it can immediately rewrite/reclaim leases during the
	// AP worker delay window. Stop -> write clean file -> start is more deterministic
	// for the AP .2-.5 upgrade flow.
	if err := firmwareStopDnsmasq(); err != nil {
		return removed, err
	}

	if err := os.WriteFile(leasePath, []byte(newContent), 0644); err != nil {
		_ = firmwareStartDnsmasqService()
		return removed, fmt.Errorf("write %s failed: %w", leasePath, err)
	}

	if err := firmwareStartDnsmasqService(); err != nil {
		return removed, err
	}

	return removed, nil
}

func firmwareStopDnsmasq() error {
	out, err := exec.Command("/etc/init.d/dnsmasq", "stop").CombinedOutput()
	if err != nil {
		return fmt.Errorf("dnsmasq stop failed: %w output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func firmwareStartDnsmasqService() error {
	out, err := exec.Command("/etc/init.d/dnsmasq", "start").CombinedOutput()
	if err != nil {
		return fmt.Errorf("dnsmasq start failed: %w output=%s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// ---------- Compatibility: old AP-only mode ----------

func firmwareHandleLegacyAPUpgrade(r *http.Request, uploadedPath string, filename string, keepSettings bool, targetIPs []string) (FirmwareFlashResponse, error) {
	if len(targetIPs) == 0 {
		return FirmwareFlashResponse{}, fmt.Errorf("no antenna targets selected")
	}

	downloadID, err := firmwareRegisterDownload(uploadedPath, filename)
	if err != nil {
		return FirmwareFlashResponse{}, fmt.Errorf("create download token failed: %w", err)
	}

	downloadURL := firmwareBuildDownloadURL(r, downloadID)
	results := make([]FirmwareFlashResult, 0, len(targetIPs))

	allDispatchOK := true
	for _, ip := range targetIPs {
		result := FirmwareFlashResult{
			IP:     ip,
			Role:   "ap",
			Stage:  "dispatch",
			Status: "failed",
			OK:     false,
		}

		if err := firmwareCommandAPWgetAndFlash(ip, downloadURL, keepSettings); err != nil {
			result.Error = err.Error()
			allDispatchOK = false
		} else {
			result.OK = true
			result.Status = "ok"
		}

		results = append(results, result)
		time.Sleep(800 * time.Millisecond)
	}

	if allDispatchOK {
		cleanupIPs := firmwareMergeIPs(APManagementIPs(), targetIPs)
		removedLeases, cleanupErr := firmwareClearLocalDHCPLeasesForIPs(cleanupIPs)
		cleanupResult := FirmwareFlashResult{
			Role:   "dhcp",
			Stage:  "cleanup",
			Status: "ok",
			OK:     true,
			Detail: fmt.Sprintf("Removed %d lease record(s) for %s and restarted dnsmasq.", removedLeases, strings.Join(cleanupIPs, ", ")),
		}
		if cleanupErr != nil {
			cleanupResult.Status = "failed"
			cleanupResult.OK = false
			cleanupResult.Error = cleanupErr.Error()
		}
		results = append(results, cleanupResult)
	}

	return FirmwareFlashResponse{
		OK:         true,
		TargetType: "ap",
		Message:    "antenna firmware upgrade commands have been sent. Selected antennas will download firmware from AC and reboot shortly.",
		Results:    results,
	}, nil
}

// ---------- Temporary firmware download endpoint ----------
//
// antennas download firmware from AC using wget.
// No Authorization header is required, because AP wget does not know the browser token.
// The random download id acts as a temporary capability token.

func firmwareDownloadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `method not allowed`, http.StatusMethodNotAllowed)
		return
	}

	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		http.Error(w, `missing id`, http.StatusBadRequest)
		return
	}

	firmwareCleanupExpiredDownloads()

	item, ok := firmwareGetDownload(id)
	if !ok {
		http.Error(w, `firmware not found or expired`, http.StatusNotFound)
		return
	}

	f, err := os.Open(item.Path)
	if err != nil {
		http.Error(w, `firmware file missing`, http.StatusNotFound)
		return
	}
	defer f.Close()

	filename := filepath.Base(item.FileName)
	if filename == "." || filename == "/" || filename == "" {
		filename = "firmware.bin"
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))

	http.ServeContent(w, r, filename, time.Now(), f)
}

// ---------- Local AC sysupgrade ----------

func firmwareStartLocalSysupgrade(path string, keepSettings bool) {
	go func() {
		// 调用方已把 job 状态置为 ac_rebooting。这里延迟 ~12s 再真正触发重启,
		// 给前端(每 3s 轮询一次)足够机会读到 ac_rebooting 那一帧并进入倒计时,
		// 否则 AC 立刻重启会让前端漏帧、进度页卡在某个 AP 的 waiting。
		time.Sleep(12 * time.Second)

		args := []string{}
		if !keepSettings {
			args = append(args, "-n")
		}
		args = append(args, path)

		cmd := exec.Command("/sbin/sysupgrade", args...)
		_ = cmd.Start()
	}()
}

// ---------- AP wget + sysupgrade through ubus ----------

func firmwareCommandAPWgetAndFlash(ip string, downloadURL string, keepSettings bool) error {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return fmt.Errorf("empty antenna IP")
	}

	if downloadURL == "" {
		return fmt.Errorf("empty download URL")
	}

	apFirmwarePath := "/tmp/lrap-ap-firmware.bin"
	apLogPath := "/tmp/lrap-firmware-upgrade.log"
	apWorkerPath := "/tmp/lrap-firmware-worker.sh"

	keepValue := "1"
	if !keepSettings {
		keepValue = "0"
	}

	// Root fix for intermittent empty ubus response bodies:
	//
	// The previous implementation sent one long "sh -c" command containing the
	// delayed wget + sysupgrade worker. Some AP/rpcd/uhttpd builds intermittently
	// returned an empty HTTP body even though the command was actually scheduled.
	// The symptom was:
	//   remote antenna exec failed before upgrade worker was scheduled: ubus bad result body=
	//
	// This version separates the complex upgrade logic from the dispatch command:
	//   1) Write the real worker script onto the AP first. This does not reboot or
	//      kill services, so ubus has time to return normal JSON.
	//   2) Dispatch only a tiny command shaped like the manual test that was proven
	//      stable on the device:
	//        ( sleep 45; sh /tmp/lrap-firmware-worker.sh ) </dev/null >/dev/null 2>&1 & echo LRAP_DISPATCH_OK
	//
	// Bad/empty ubus body is still treated as a real dispatch failure. The fix is
	// that the command which must return through ubus is now minimal.
	workerScript := firmwareBuildAPUpgradeWorkerScript(apFirmwarePath, apLogPath, downloadURL, keepValue)
	if err := firmwareWriteRemoteText(ip, AnonSID, apWorkerPath, workerScript, 0700); err != nil {
		return fmt.Errorf("remote antenna worker script write failed: %v", err)
	}

	cmd := fmt.Sprintf(`rm -f %[1]s; ( sleep 45; sh %[2]s ) </dev/null >/dev/null 2>&1 & echo LRAP_DISPATCH_OK`,
		shellQuote(apLogPath),
		shellQuote(apWorkerPath),
	)

	res, err := firmwareUbusCallAtWithTimeout(ip, AnonSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", cmd},
	}, 12*time.Second)
	if err != nil {
		return fmt.Errorf("remote antenna exec failed before upgrade worker was scheduled: %v", err)
	}

	// Require the same positive marker used in the manual wget --post-file test.
	// If a device returns a JSON result but omits stdout, we allow it because some
	// rpcd builds do not include stdout for file.exec. An actual empty/bad HTTP body
	// is still an error above and is not hidden.
	if stdout, ok := res["stdout"].(string); ok && strings.TrimSpace(stdout) != "" && !strings.Contains(stdout, "LRAP_DISPATCH_OK") {
		return fmt.Errorf("remote antenna exec returned unexpected stdout: %s", strings.TrimSpace(stdout))
	}

	return nil
}

func firmwareBuildAPUpgradeWorkerScript(apFirmwarePath string, apLogPath string, downloadURL string, keepValue string) string {
	return fmt.Sprintf(`#!/bin/sh
AP_FIRMWARE=%s
AP_LOG=%s
DOWNLOAD_URL=%s
KEEP_SETTINGS=%s
{
  echo "LRAP worker start $(date)"
  rm -f "$AP_FIRMWARE"
  wget -O "$AP_FIRMWARE" "$DOWNLOAD_URL"
  rc=$?
  echo "wget rc=$rc"
  [ "$rc" -eq 0 ] || exit "$rc"
  [ -s "$AP_FIRMWARE" ] || exit 1
  sync
  sleep 2
  echo "sysupgrade start $(date)"
  if [ "$KEEP_SETTINGS" = "0" ]; then
    /sbin/sysupgrade -n "$AP_FIRMWARE"
  else
    /sbin/sysupgrade "$AP_FIRMWARE"
  fi
} >> "$AP_LOG" 2>&1
`,
		shellQuote(apFirmwarePath),
		shellQuote(apLogPath),
		shellQuote(downloadURL),
		shellQuote(keepValue),
	)
}

func firmwareWriteRemoteText(ip string, sid string, path string, data string, perm os.FileMode) error {
	ip = strings.TrimSpace(ip)
	path = strings.TrimSpace(path)
	if ip == "" || path == "" {
		return fmt.Errorf("empty ip or path")
	}

	// Prefer ubus file.write when the AP ACL allows it.
	_, err := firmwareUbusCallAtWithTimeout(ip, sid, "file", "write", map[string]any{
		"path": path,
		"data": data,
	}, 12*time.Second)
	if err == nil {
		if perm != 0 {
			_, chmodErr := firmwareUbusCallAtWithTimeout(ip, sid, "file", "exec", map[string]any{
				"command": "chmod",
				"params":  []string{fmt.Sprintf("%o", perm), path},
			}, 12*time.Second)
			if chmodErr != nil {
				return fmt.Errorf("remote chmod failed after file.write: %w", chmodErr)
			}
		}
		return nil
	}

	// Fallback for AP ACLs that allow file.exec but not file.write. This command only
	// writes a script; it does not start wget/sysupgrade, so it should not interrupt
	// the ubus HTTP response.
	writeCmd := fmt.Sprintf("umask 077; cat > %s <<'LRAP_FW_SCRIPT_EOF'\n%s\nLRAP_FW_SCRIPT_EOF\nchmod %o %s",
		shellQuote(path),
		data,
		perm,
		shellQuote(path),
	)

	_, execErr := firmwareUbusCallAtWithTimeout(ip, sid, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", writeCmd},
	}, 20*time.Second)
	if execErr != nil {
		return fmt.Errorf("file.write failed: %v; exec fallback failed: %w", err, execErr)
	}

	return nil
}

// ---------- Temporary Download Store ----------

func firmwareRegisterDownload(path string, filename string) (string, error) {
	idBytes := make([]byte, 16)
	if _, err := rand.Read(idBytes); err != nil {
		return "", err
	}

	id := hex.EncodeToString(idBytes)

	firmwareDownloadStore.Lock()
	firmwareDownloadStore.Items[id] = firmwareDownloadItem{
		Path:      path,
		FileName:  filename,
		ExpiresAt: time.Now().Add(firmwareDownloadTTL),
	}
	firmwareDownloadStore.Unlock()

	return id, nil
}

func firmwareGetDownload(id string) (firmwareDownloadItem, bool) {
	firmwareDownloadStore.Lock()
	defer firmwareDownloadStore.Unlock()

	item, ok := firmwareDownloadStore.Items[id]
	if !ok {
		return firmwareDownloadItem{}, false
	}

	if time.Now().After(item.ExpiresAt) {
		delete(firmwareDownloadStore.Items, id)
		_ = os.Remove(item.Path)
		return firmwareDownloadItem{}, false
	}

	return item, true
}

func firmwareCleanupExpiredDownloads() {
	now := time.Now()

	firmwareDownloadStore.Lock()
	defer firmwareDownloadStore.Unlock()

	for id, item := range firmwareDownloadStore.Items {
		if now.After(item.ExpiresAt) {
			delete(firmwareDownloadStore.Items, id)
			_ = os.Remove(item.Path)
		}
	}
}

// ---------- Download URL ----------

func firmwareBuildDownloadURL(r *http.Request, id string) string {
	acIP := firmwareDetectACLANIP()
	// Isolated Antennas can reach the backend only through the per-chassis
	// management VLAN. The client LAN is deliberately not routed into VLAN 200.
	if antennaManagementIsIsolated() {
		acIP = AntennaManagementACIP
	}
	if acIP == "" {
		acIP = firmwareHostWithoutPort(r.Host)
	}

	if acIP == "" {
		acIP = ACManagementIP
	}

	// IMPORTANT:
	// The backend portal normally listens on 9080, not port 80.
	// AP wget must include the same port as the browser used for the upload,
	// otherwise the AP will try http://10.10.18.1/... on port 80 and get 404.
	portSuffix := firmwarePortSuffixFromRequest(r)

	return fmt.Sprintf("http://%s%s/api/system/firmware/download?id=%s", acIP, portSuffix, id)
}

func firmwareHostWithoutPort(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}

	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}

	// Simple IPv4/hostname fallback. Keep IPv6 edge cases out of this product LAN path.
	if strings.Count(host, ":") == 1 {
		parts := strings.SplitN(host, ":", 2)
		return parts[0]
	}

	return host
}

func firmwarePortSuffixFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}

	host := strings.TrimSpace(r.Host)
	if host == "" {
		return ""
	}

	_, port, err := net.SplitHostPort(host)
	if err == nil && port != "" {
		return ":" + port
	}

	// Fallback for simple host:port forms when SplitHostPort cannot parse it.
	if strings.Count(host, ":") == 1 {
		parts := strings.SplitN(host, ":", 2)
		if len(parts) == 2 && parts[1] != "" {
			return ":" + parts[1]
		}
	}

	return ""
}

func firmwareDetectACLANIP() string {
	// Prefer runtime LAN IP via ubus. The AC's rpcd unauthenticated ACL permits
	// network.interface.lan status, so the anonymous SID is enough here.
	st, err := ubusCallJSONLocal(AnonSID, "network.interface.lan", "status", nil)
	if err == nil {
		if arr, ok := st["ipv4-address"].([]any); ok && len(arr) > 0 {
			if m, ok := arr[0].(map[string]any); ok {
				if addr, ok := m["address"].(string); ok && addr != "" {
					return addr
				}
			}
		}
	}

	// Fallback: fixed AC LAN IP for this project.
	return ACManagementIP
}

// ---------- Helpers ----------

func firmwareParseTargetIPs(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0)
	seen := make(map[string]bool)

	for _, p := range parts {
		ip := strings.TrimSpace(p)
		if ip == "" || seen[ip] {
			continue
		}
		seen[ip] = true
		out = append(out, ip)
	}

	return out
}

func firmwareParseBool(raw string, fallback bool) bool {
	v := strings.ToLower(strings.TrimSpace(raw))

	switch v {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func firmwarePingOnce(ip string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return false
	}

	return exec.Command("ping", "-c", "1", "-W", "1", ip).Run() == nil
}

func firmwareNormalizeMAC(raw string) string {
	mac := strings.ToLower(strings.TrimSpace(raw))
	if mac == "" {
		return ""
	}
	mac = strings.ReplaceAll(mac, "-", ":")
	parts := strings.Split(mac, ":")
	if len(parts) != 6 {
		return ""
	}
	for i, p := range parts {
		if len(p) == 1 {
			p = "0" + p
		}
		if len(p) != 2 {
			return ""
		}
		for _, ch := range p {
			if !((ch >= '0' && ch <= '9') || (ch >= 'a' && ch <= 'f')) {
				return ""
			}
		}
		parts[i] = p
	}
	return strings.Join(parts, ":")
}

func firmwareJoinOrNone(items []string) string {
	clean := make([]string, 0, len(items))
	seen := make(map[string]bool)
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		clean = append(clean, item)
	}
	if len(clean) == 0 {
		return "none captured"
	}
	return strings.Join(clean, ", ")
}

func firmwareJSONSafeError(err error) string {
	if err == nil {
		return ""
	}
	return strings.ReplaceAll(err.Error(), `"`, `'`)
}

func firmwareShortText(s string, max int) string {
	s = strings.TrimSpace(s)
	if max <= 0 || len(s) <= max {
		return s
	}
	return "..." + s[len(s)-max:]
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
