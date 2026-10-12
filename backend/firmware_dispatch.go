package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

// Antenna firmware is dispatched in two steps so that a failure part-way through
// never leaves some Antennas flashing and others not.
//
//  1. Arm: every Antenna gets a worker that downloads and flashes, but the
//     worker only waits. It proceeds when the Root writes this job's token into
//     the go file, gives up when the armed file stops holding the token
//     (cancelled, or a newer job re-armed it), and gives up on its own after
//     firmwareAPArmWindowSeconds so a Root that dies mid-job strands nothing.
//  2. Go: only once every Antenna is armed does the Root tell them to go.
//
// If any Antenna cannot be armed, the ones already armed are cancelled and none
// of them is told to go. Before this, each Antenna got a worker that slept 45
// seconds and then flashed unconditionally. On TestVersion1.7 one reply timed out
// although its command had run, the job failed, the sub image was deleted, and
// the four sleeping workers only stopped because their downloads came back 404.
//
// A lost or garbled reply is not taken as failure. The Root reads back the armed
// file and the worker log on the Antenna and decides from what is actually there.
const (
	firmwareAPFirmwarePath = "/tmp/lrap-ap-firmware.bin"
	firmwareAPLogPath      = "/tmp/lrap-firmware-upgrade.log"
	firmwareAPWorkerPath   = "/tmp/lrap-firmware-worker.sh"
	firmwareAPArmedPath    = "/tmp/lrap-firmware-armed"
	firmwareAPGoPath       = "/tmp/lrap-firmware-go"

	firmwareAPArmedMarker     = "LRAP_ARMED_OK"
	firmwareAPGoMarker        = "LRAP_GO_OK"
	firmwareAPNotArmedMarker  = "LRAP_NOT_ARMED"
	firmwareAPCancelledMarker = "LRAP_CANCELLED"
	firmwareAPTooLateMarker   = "LRAP_ALREADY_GO"
)

var (
	// firmwareRemoteExec runs a shell command on an Antenna and returns stdout.
	firmwareRemoteExec = func(ip string, cmd string, timeout time.Duration) (string, error) {
		res, err := firmwareUbusCallAtWithTimeout(ip, AnonSID, "file", "exec", map[string]any{
			"command": "sh",
			"params":  []string{"-c", cmd},
		}, timeout)
		if err != nil {
			return "", err
		}
		stdout, _ := res["stdout"].(string)
		return stdout, nil
	}
	firmwareRemoteWriteWorker = func(ip string, script string) error {
		return firmwareWriteRemoteText(ip, AnonSID, firmwareAPWorkerPath, script, 0700)
	}

	firmwareAPArmWindowSeconds = 300
	firmwareAPGoGraceSeconds   = 5

	firmwareDispatchExecTimeout     = 12 * time.Second
	firmwareDispatchConfirmAttempts = 3
	firmwareDispatchConfirmDelay    = 2 * time.Second
	firmwareDispatchSpacing         = 800 * time.Millisecond
)

func firmwareNewDispatchToken() string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%016x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b)
}

func firmwareBuildAPUpgradeWorkerScript(downloadURL string, keepSettings bool, token string) string {
	keepValue := "1"
	if !keepSettings {
		keepValue = "0"
	}
	return fmt.Sprintf(`#!/bin/sh
AP_FIRMWARE=%s
AP_LOG=%s
DOWNLOAD_URL=%s
KEEP_SETTINGS=%s
TOKEN=%s
ARMED=%s
GO=%s
{
  echo "LRAP worker armed $(date)"
  n=0
  until [ "$(cat "$GO" 2>/dev/null)" = "$TOKEN" ]; do
    if [ "$(cat "$ARMED" 2>/dev/null)" != "$TOKEN" ]; then
      echo "LRAP worker cancelled $(date)"
      exit 0
    fi
    n=$((n+1))
    if [ "$n" -gt %d ]; then
      echo "LRAP worker expired without go $(date)"
      rm -f "$ARMED"
      exit 0
    fi
    sleep 1
  done
  echo "LRAP worker go $(date)"
  sleep %d
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
		shellQuote(firmwareAPFirmwarePath),
		shellQuote(firmwareAPLogPath),
		shellQuote(downloadURL),
		shellQuote(keepValue),
		shellQuote(token),
		shellQuote(firmwareAPArmedPath),
		shellQuote(firmwareAPGoPath),
		firmwareAPArmWindowSeconds,
		firmwareAPGoGraceSeconds,
	)
}

// The worker is backgrounded with its output redirected, so this returns at
// once; the worker itself only waits until it is told to go.
func firmwareAPArmCommand(token string) string {
	return fmt.Sprintf(`rm -f %[1]s %[2]s; echo %[3]s > %[4]s; ( sh %[5]s ) </dev/null >/dev/null 2>&1 & echo %[6]s`,
		shellQuote(firmwareAPLogPath),
		shellQuote(firmwareAPGoPath),
		shellQuote(token),
		shellQuote(firmwareAPArmedPath),
		shellQuote(firmwareAPWorkerPath),
		firmwareAPArmedMarker,
	)
}

// Armed means the armed file holds this token and the worker has started and
// not given up.
func firmwareAPArmedCheckCommand(token string) string {
	return fmt.Sprintf(`[ "$(cat %[1]s 2>/dev/null)" = %[2]s ] && grep -q "LRAP worker armed" %[3]s 2>/dev/null && ! grep -qE "LRAP worker (cancelled|expired)" %[3]s && echo %[4]s`,
		shellQuote(firmwareAPArmedPath),
		shellQuote(token),
		shellQuote(firmwareAPLogPath),
		firmwareAPArmedMarker,
	)
}

func firmwareAPGoCommand(token string) string {
	return fmt.Sprintf(`if [ "$(cat %[1]s 2>/dev/null)" = %[2]s ]; then echo %[2]s > %[3]s; echo %[4]s; else echo %[5]s; fi`,
		shellQuote(firmwareAPArmedPath),
		shellQuote(token),
		shellQuote(firmwareAPGoPath),
		firmwareAPGoMarker,
		firmwareAPNotArmedMarker,
	)
}

func firmwareAPGoCheckCommand(token string) string {
	return fmt.Sprintf(`{ [ "$(cat %[1]s 2>/dev/null)" = %[2]s ] || grep -q "LRAP worker go" %[3]s 2>/dev/null; } && echo %[4]s`,
		shellQuote(firmwareAPGoPath),
		shellQuote(token),
		shellQuote(firmwareAPLogPath),
		firmwareAPGoMarker,
	)
}

func firmwareAPCancelCommand(token string) string {
	return fmt.Sprintf(`if [ "$(cat %[1]s 2>/dev/null)" = %[2]s ]; then rm -f %[1]s; fi; if grep -q "LRAP worker go" %[3]s 2>/dev/null; then echo %[4]s; else echo %[5]s; fi`,
		shellQuote(firmwareAPArmedPath),
		shellQuote(token),
		shellQuote(firmwareAPLogPath),
		firmwareAPTooLateMarker,
		firmwareAPCancelledMarker,
	)
}

// firmwareConfirmOnAP re-reads the Antenna's own state after a reply that could
// not be trusted.
func firmwareConfirmOnAP(ip string, checkCmd string, marker string) bool {
	for attempt := 0; attempt < firmwareDispatchConfirmAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(firmwareDispatchConfirmDelay)
		}
		out, err := firmwareRemoteExec(ip, checkCmd, firmwareDispatchExecTimeout)
		if err == nil && strings.Contains(out, marker) {
			return true
		}
	}
	return false
}

// firmwareArmAPUpgrade writes the worker and starts it waiting. detail is set
// when the reply was lost but the Antenna itself shows the worker armed.
func firmwareArmAPUpgrade(ip string, downloadURL string, keepSettings bool, token string) (detail string, err error) {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return "", fmt.Errorf("empty antenna IP")
	}
	if downloadURL == "" {
		return "", fmt.Errorf("empty download URL")
	}

	if err := firmwareRemoteWriteWorker(ip, firmwareBuildAPUpgradeWorkerScript(downloadURL, keepSettings, token)); err != nil {
		return "", fmt.Errorf("remote antenna worker script write failed: %v", err)
	}

	out, execErr := firmwareRemoteExec(ip, firmwareAPArmCommand(token), firmwareDispatchExecTimeout)
	if execErr == nil && strings.Contains(out, firmwareAPArmedMarker) {
		return "", nil
	}
	if firmwareConfirmOnAP(ip, firmwareAPArmedCheckCommand(token), firmwareAPArmedMarker) {
		return fmt.Sprintf("The arm reply was lost (%s), but the Antenna shows its upgrade worker armed.", firmwareDispatchReplyProblem(out, execErr)), nil
	}
	return "", fmt.Errorf("upgrade worker could not be armed: %s, and the Antenna does not show it armed", firmwareDispatchReplyProblem(out, execErr))
}

// firmwareGoAPUpgrade tells an armed worker to download and flash.
func firmwareGoAPUpgrade(ip string, token string) (detail string, err error) {
	out, execErr := firmwareRemoteExec(ip, firmwareAPGoCommand(token), firmwareDispatchExecTimeout)
	if execErr == nil && strings.Contains(out, firmwareAPGoMarker) {
		return "", nil
	}
	if execErr == nil && strings.Contains(out, firmwareAPNotArmedMarker) {
		return "", fmt.Errorf("upgrade worker was no longer armed when told to go")
	}
	if firmwareConfirmOnAP(ip, firmwareAPGoCheckCommand(token), firmwareAPGoMarker) {
		return fmt.Sprintf("The go reply was lost (%s), but the Antenna shows its upgrade worker going.", firmwareDispatchReplyProblem(out, execErr)), nil
	}
	return "", fmt.Errorf("upgrade worker could not be told to go: %s", firmwareDispatchReplyProblem(out, execErr))
}

// firmwareCancelAPUpgrade disarms a worker that has not been told to go. An
// unreachable Antenna is left alone: its worker never hears go and expires.
func firmwareCancelAPUpgrade(ip string, token string) FirmwareFlashResult {
	result := FirmwareFlashResult{IP: ip, Role: "sub", Stage: "cancel", Status: "ok", OK: true}
	out, err := firmwareRemoteExec(ip, firmwareAPCancelCommand(token), firmwareDispatchExecTimeout)
	switch {
	case err == nil && strings.Contains(out, firmwareAPTooLateMarker):
		result.Status, result.OK = "failed", false
		result.Error = "upgrade worker had already been told to go and could not be cancelled"
	case err == nil && strings.Contains(out, firmwareAPCancelledMarker):
		result.Detail = "Upgrade worker cancelled; this Antenna will not be flashed."
	default:
		result.Detail = fmt.Sprintf("Could not reach the Antenna to cancel (%s). Its worker was never told to go and gives up on its own within %d seconds.", firmwareDispatchReplyProblem(out, err), firmwareAPArmWindowSeconds)
	}
	return result
}

func firmwareStatesForIPs(states []firmwareAPUpgradeState, ips []string) []firmwareAPUpgradeState {
	keep := make(map[string]bool, len(ips))
	for _, ip := range ips {
		keep[ip] = true
	}
	out := make([]firmwareAPUpgradeState, 0, len(ips))
	for _, st := range states {
		if keep[st.IP] {
			out = append(out, st)
		}
	}
	return out
}

func firmwareDispatchReplyProblem(out string, err error) string {
	if err != nil {
		return err.Error()
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "empty reply"
	}
	return "unexpected reply: " + out
}

// firmwareDispatchAPUpgrades arms every target and only then tells them to go.
// launched lists the Antennas that were told to go; ok is true only when all of
// them were. When arming fails nothing is launched and every armed worker is
// cancelled.
func firmwareDispatchAPUpgrades(ips []string, downloadURL string, keepSettings bool, role string, report func(FirmwareFlashResult)) (launched []string, ok bool) {
	token := firmwareNewDispatchToken()

	armed := make([]string, 0, len(ips))
	armOK := true
	for i, ip := range ips {
		if i > 0 {
			time.Sleep(firmwareDispatchSpacing)
		}
		result := FirmwareFlashResult{IP: ip, Role: role, Stage: "arm", Status: "ok", OK: true}
		detail, err := firmwareArmAPUpgrade(ip, downloadURL, keepSettings, token)
		if err != nil {
			result.Status, result.OK, result.Error = "failed", false, err.Error()
			armOK = false
		} else {
			result.Detail = detail
			armed = append(armed, ip)
		}
		report(result)
		if !armOK {
			// The remaining Antennas would only be armed to be cancelled.
			break
		}
	}

	if !armOK {
		for _, ip := range armed {
			cancelled := firmwareCancelAPUpgrade(ip, token)
			cancelled.Role = role
			report(cancelled)
		}
		return nil, false
	}

	launched = make([]string, 0, len(armed))
	for _, ip := range armed {
		result := FirmwareFlashResult{IP: ip, Role: role, Stage: "dispatch", Status: "ok", OK: true}
		detail, err := firmwareGoAPUpgrade(ip, token)
		if err != nil {
			result.Status, result.OK, result.Error = "failed", false, err.Error()
		} else {
			result.Detail = detail
			launched = append(launched, ip)
		}
		report(result)
	}
	return launched, len(launched) == len(ips)
}
