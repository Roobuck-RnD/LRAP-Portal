package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ---------- Types ----------

type RebootAllResponse struct {
	Status   string   `json:"status"`
	Message  string   `json:"message,omitempty"`
	Targets  []string `json:"targets,omitempty"`
	Warnings []string `json:"warnings,omitempty"`
}

// ---------- AP list ----------

func rebootManagedAPIPs() []string {
	return []string{
		"10.10.18.2",
		"10.10.18.3",
		"10.10.18.4",
		"10.10.18.5",
	}
}

// ---------- Reboot Logic ----------

func rebootLocalDelayed(delay time.Duration) {
	go func() {
		time.Sleep(delay)
		fmt.Println("[System] Rebooting local AC system...")
		_ = exec.Command("reboot").Run()
	}()
}

func rebootRemoteAP(ip string) error {
	if strings.TrimSpace(ip) == "" {
		return fmt.Errorf("empty target ip")
	}

	const zeroSID = "00000000000000000000000000000000"

	// Use background shell so ubus file.exec can return before reboot interrupts RPC.
	cmdStr := "(sleep 2; reboot) >/dev/null 2>&1 &"

	_, err := ubusCallJSONAt(ip, zeroSID, "file", "exec", map[string]any{
		"command": "sh",
		"params":  []string{"-c", cmdStr},
	})

	if err != nil {
		return fmt.Errorf("remote reboot failed: %v", err)
	}

	return nil
}

func rebootAllModules() RebootAllResponse {
	apIPs := rebootManagedAPIPs()

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

	rebootLocalDelayed(4 * time.Second)

	return RebootAllResponse{
		Status:   "ok",
		Message:  "Reboot sequence initiated for AC and reachable AP modules",
		Targets:  targets,
		Warnings: warnings,
	}
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

	result := rebootAllModules()

	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(result)
}