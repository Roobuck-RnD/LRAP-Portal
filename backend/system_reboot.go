package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

const (
	rebootDispatchGrace      = 3 * time.Second
	rebootRemoteDelaySeconds = 5
	rebootLocalDelay         = 4 * time.Second
	rebootEstimatedSeconds   = 90
	rebootLockTimeout        = 3 * time.Minute
)

var rebootState = struct {
	sync.Mutex
	active bool
}{}

// ---------- Types ----------

type RebootAllResponse struct {
	Status   string   `json:"status"`
	Message  string   `json:"message,omitempty"`
	Targets  []string `json:"targets,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

type RebootAcceptedResponse struct {
	Status           string `json:"status"`
	Message          string `json:"message"`
	EstimatedSeconds int    `json:"estimated_seconds"`
}

// ---------- Reboot Logic ----------

func rebootLocalDelayed(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		fmt.Println("[System] Rebooting local AC system...")
		if err := exec.Command("reboot").Run(); err != nil {
			log.Printf("local AC reboot command failed: %v", err)
		}
	}()
}

func rebootRemoteAP(ip string) error {
	if strings.TrimSpace(ip) == "" {
		return fmt.Errorf("empty target ip")
	}

	// Keep the AP alive long enough for the accepted response and reboot screen
	// to reach a browser whose current network path crosses this AP.
	cmdStr := fmt.Sprintf("(sleep %d; reboot) >/dev/null 2>&1 &", rebootRemoteDelaySeconds)

	_, err := ubusCallJSONAt(ip, AnonSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", cmdStr},
	})

	if err != nil {
		return fmt.Errorf("remote reboot failed: %v", err)
	}

	return nil
}

func rebootAllModules() RebootAllResponse {
	apIPs := APManagementIPs()

	var wg sync.WaitGroup
	var mu sync.Mutex

	targets := make([]string, 0, len(apIPs)+1)
	warnings := make([]string, 0)

	for _, ip := range apIPs {
		ip := ip

		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := rebootRemoteAP(ip); err != nil {
				mu.Lock()
				warnings = append(warnings, fmt.Sprintf("%s: %v", ip, err))
				mu.Unlock()
				return
			}

			mu.Lock()
			targets = append(targets, ip)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Reboot AC after HTTP response has time to reach frontend.
	targets = append(targets, "AC")

	rebootLocalDelayed(rebootLocalDelay)

	return RebootAllResponse{
		Status:   "ok",
		Message:  "Reboot sequence initiated for AC and reachable AP modules",
		Targets:  targets,
		Warnings: warnings,
	}
}

func beginRebootAllModules() bool {
	rebootState.Lock()
	if rebootState.active {
		rebootState.Unlock()
		return false
	}
	rebootState.active = true
	rebootState.Unlock()

	// The process normally exits during this sequence. If the reboot command is
	// rejected by the OS, release the guard later so an administrator can retry.
	time.AfterFunc(rebootLockTimeout, func() {
		rebootState.Lock()
		rebootState.active = false
		rebootState.Unlock()
	})

	go func() {
		// systemRebootHandler returns 202 before this grace period expires.
		time.Sleep(rebootDispatchGrace)

		result := rebootAllModules()
		if len(result.Warnings) > 0 {
			log.Printf("reboot sequence warnings: %s", strings.Join(result.Warnings, "; "))
		}
		log.Printf("reboot sequence dispatched to %d target(s)", len(result.Targets))
	}()

	return true
}

// ---------- Handler ----------

func systemRebootHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	if len(auth) < 7 || !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		http.Error(w, `{"error":"Unauthorized"}`, http.StatusUnauthorized)
		return
	}

	if !beginRebootAllModules() {
		http.Error(w, `{"error":"reboot already in progress"}`, http.StatusConflict)
		return
	}

	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(RebootAcceptedResponse{
		Status:           "accepted",
		Message:          "Reboot accepted. The router will restart shortly.",
		EstimatedSeconds: rebootEstimatedSeconds,
	})
}
