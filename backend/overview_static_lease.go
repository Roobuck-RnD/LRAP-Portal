package main

import (
	"encoding/json"
	"net/http"
	"strings"
)

// These two endpoints (static-map / static-lease) serve the Overview page (MAC-centred).
// The real UCI create/read/update/delete + dnsmasq reload live in the shared
// service(staticLeaseUpsert / staticLeaseDeleteByMAC / getUciStaticLeasesLocal),
// Only request parsing and the response shape Overview expects are done here. Static leases are AC-only (APs have
// DHCP off), so remote ?ip= is no longer supported. main.go's withAuth guarantees authentication.

// ---------- static leases: list ----------

// StaticEntry is the response of GET /api/lan/static-map (field names ip/name, as the Overview page parses them).
type StaticEntry struct {
	Section string `json:"section"`
	MAC     string `json:"mac"`
	IP      string `json:"ip"`
	Name    string `json:"name,omitempty"`
}

func staticMapHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")

	leases, err := getUciStaticLeasesLocal(bearerSID(r))
	if err != nil {
		http.Error(w, `{"error":"uci get dhcp failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}

	out := make([]StaticEntry, 0, len(leases))
	for _, l := range leases {
		out = append(out, StaticEntry{
			Section: l.Section,
			MAC:     strings.ToUpper(l.MAC),
			IP:      l.IPAddr,
			Name:    l.Hostname,
		})
	}

	_ = json.NewEncoder(w).Encode(out)
}

// ---------- static leases: set / unset ----------

type staticLeaseIn struct {
	Hostname string `json:"hostname"`
	MAC      string `json:"mac"`
	IPAddr   string `json:"ipaddr"`
}

type staticLeaseOut struct {
	Updated bool   `json:"updated"` // true = updated, false = added
	Section string `json:"section"`
}

func setStaticLeaseHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		handleSetStatic(w, r)
	case http.MethodDelete:
		handleUnsetStatic(w, r)
	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

func handleSetStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var in staticLeaseIn
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	in.MAC = staticLeaseNormalizeMAC(in.MAC)
	in.IPAddr = strings.TrimSpace(in.IPAddr)
	in.Hostname = strings.TrimSpace(in.Hostname)
	if in.MAC == "" || in.IPAddr == "" {
		http.Error(w, `{"error":"mac and ipaddr are required"}`, http.StatusBadRequest)
		return
	}

	section, updated, err := staticLeaseUpsert(bearerSID(r), in.MAC, in.IPAddr, in.Hostname)
	if err != nil {
		http.Error(w, `{"error":"save failed: `+escapeErr(err)+`"}`, staticLeaseErrorStatus(err))
		return
	}

	_ = json.NewEncoder(w).Encode(staticLeaseOut{Updated: updated, Section: section})
}

func handleUnsetStatic(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	var in struct {
		MAC string `json:"mac"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
		return
	}
	in.MAC = staticLeaseNormalizeMAC(in.MAC)
	if in.MAC == "" {
		http.Error(w, `{"error":"mac is required"}`, http.StatusBadRequest)
		return
	}

	section, deleted, err := staticLeaseDeleteByMAC(bearerSID(r), in.MAC)
	if err != nil {
		http.Error(w, `{"error":"delete failed: `+escapeErr(err)+`"}`, http.StatusInternalServerError)
		return
	}
	if !deleted {
		// It did not exist anyway; treat as success
		_ = json.NewEncoder(w).Encode(map[string]any{"deleted": false})
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]any{"deleted": true, "section": section})
}
