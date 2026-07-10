package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ---------- Struct Definitions ----------

type StaticLeaseConfig struct {
	Section  string `json:"section,omitempty"`
	Hostname string `json:"hostname"`
	MAC      string `json:"mac"`
	IPAddr   string `json:"ipaddr"`
}

// ---------- SID Helper ----------

func staticLeaseSIDFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")
	sid := strings.TrimPrefix(authHeader, "Bearer ")

	if sid == authHeader {
		return ""
	}

	return strings.TrimSpace(sid)
}

// ---------- UCI Helpers: AC-only ----------

func getUciStaticLeasesLocal(sid string) ([]StaticLeaseConfig, error) {
	useSid := resolveSid("", sid)

	res, err := ubusCallJSONLocal(useSid, "uci", "get", map[string]any{
		"config": "dhcp",
	})
	if err != nil {
		return nil, err
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return []StaticLeaseConfig{}, nil
	}

	list := make([]StaticLeaseConfig, 0)

	for sectionName, v := range values {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}

		if item[".type"] != "host" {
			continue
		}

		entry := StaticLeaseConfig{
			Section:  sectionName,
			Hostname: getString(item, "name"),
			MAC:      strings.ToUpper(getString(item, "mac")),
			IPAddr:   getString(item, "ip"),
		}

		if entry.MAC == "" || entry.IPAddr == "" {
			continue
		}

		list = append(list, entry)
	}

	return list, nil
}

func applyLocalUciLeaseChange(sid string, action func(currentSid string) error) error {
	useSid := resolveSid("", sid)

	if err := action(useSid); err != nil {
		return err
	}

	if _, err := ubusCallJSONLocal(useSid, "uci", "commit", map[string]any{
		"config": "dhcp",
	}); err != nil {
		return fmt.Errorf("commit failed: %v", err)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)

		_, _ = ubusCallJSONLocal(useSid, "service", "event", map[string]any{
			"type": "config.change",
			"data": map[string]any{
				"package": "dhcp",
			},
		})
	}()

	return nil
}

// ---------- HTTP Handler: AC-only ----------

func staticLeaseConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sid := staticLeaseSIDFromRequest(r)

	switch r.Method {
	case http.MethodGet:
		list, err := getUciStaticLeasesLocal(sid)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(list)

	case http.MethodPost:
		var req StaticLeaseConfig

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		req.Hostname = strings.TrimSpace(req.Hostname)
		req.MAC = strings.ToUpper(strings.TrimSpace(req.MAC))
		req.IPAddr = strings.TrimSpace(req.IPAddr)

		if req.MAC == "" || req.IPAddr == "" {
			http.Error(w, `{"error":"mac and ipaddr are required"}`, http.StatusBadRequest)
			return
		}

		existingLeases, _ := getUciStaticLeasesLocal(sid)

		targetSection := ""
		for _, lease := range existingLeases {
			if strings.EqualFold(lease.MAC, req.MAC) {
				targetSection = lease.Section
				break
			}
		}

		err := applyLocalUciLeaseChange(sid, func(currSid string) error {
			values := map[string]string{
				"mac": req.MAC,
				"ip":  req.IPAddr,
			}

			if req.Hostname != "" {
				values["name"] = req.Hostname
			}

			if targetSection != "" {
				_, callErr := ubusCallJSONLocal(currSid, "uci", "set", map[string]any{
					"config":  "dhcp",
					"section": targetSection,
					"values":  values,
				})
				return callErr
			}

			_, callErr := ubusCallJSONLocal(currSid, "uci", "add", map[string]any{
				"config": "dhcp",
				"type":   "host",
				"values": values,
			})
			return callErr
		})

		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"save failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(`{"status":"ok"}`))

	case http.MethodDelete:
		section := strings.TrimSpace(r.URL.Query().Get("section"))
		if section == "" {
			http.Error(w, `{"error":"missing section param"}`, http.StatusBadRequest)
			return
		}

		err := applyLocalUciLeaseChange(sid, func(currSid string) error {
			_, callErr := ubusCallJSONLocal(currSid, "uci", "delete", map[string]any{
				"config":  "dhcp",
				"section": section,
			})
			return callErr
		})

		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(`{"status":"ok"}`))

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}