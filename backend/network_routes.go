package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// ---------- Struct Definitions ----------

type StaticRouteConfig struct {
	Section   string `json:"section,omitempty"`
	Interface string `json:"interface"`
	Target    string `json:"target"`
	Netmask   string `json:"netmask"`
	Gateway   string `json:"gateway"`
	Metric    string `json:"metric"`
}

// ---------- Local Helpers ----------

func srGetString(m map[string]any, k string) string {
	if v, ok := m[k].(string); ok {
		return v
	}
	return ""
}

func srSIDFromRequest(r *http.Request) string {
	authHeader := r.Header.Get("Authorization")

	if len(authHeader) > 7 && strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return strings.TrimSpace(authHeader[7:])
	}

	return ""
}

func srResolveLocalSID(headerSid string) (string, error) {
	if headerSid != "" {
		return headerSid, nil
	}

	return "", fmt.Errorf("unauthorized: missing session token")
}

func srValidateRoute(req StaticRouteConfig) error {
	req.Interface = strings.TrimSpace(req.Interface)
	req.Target = strings.TrimSpace(req.Target)
	req.Netmask = strings.TrimSpace(req.Netmask)
	req.Gateway = strings.TrimSpace(req.Gateway)
	req.Metric = strings.TrimSpace(req.Metric)

	if req.Interface == "" {
		return fmt.Errorf("interface is required")
	}

	if req.Target == "" {
		return fmt.Errorf("target is required")
	}

	if req.Netmask == "" {
		return fmt.Errorf("netmask is required")
	}

	return nil
}

// ---------- UCI Helpers: AC-only ----------

func getLocalUciRoutes(headerSid string) ([]StaticRouteConfig, error) {
	sid, err := srResolveLocalSID(headerSid)
	if err != nil {
		return nil, err
	}

	res, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "network",
		"type":   "route",
	})
	if err != nil {
		return nil, err
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return []StaticRouteConfig{}, nil
	}

	list := make([]StaticRouteConfig, 0)

	for sectionName, v := range values {
		item, ok := v.(map[string]any)
		if !ok {
			continue
		}

		entry := StaticRouteConfig{
			Section:   sectionName,
			Interface: srGetString(item, "interface"),
			Target:    srGetString(item, "target"),
			Netmask:   srGetString(item, "netmask"),
			Gateway:   srGetString(item, "gateway"),
			Metric:    srGetString(item, "metric"),
		}

		list = append(list, entry)
	}

	return list, nil
}

func applyLocalUciRouteChange(headerSid string, action func(currentSid string) error) error {
	sid, err := srResolveLocalSID(headerSid)
	if err != nil {
		return err
	}

	if err := action(sid); err != nil {
		return err
	}

	if _, err := ubusCallJSONLocal(sid, "uci", "commit", map[string]any{
		"config": "network",
	}); err != nil {
		return fmt.Errorf("commit failed: %v", err)
	}

	go func() {
		time.Sleep(1200 * time.Millisecond)

		if _, err := ubusCallJSONLocal(sid, "network", "reload", nil); err != nil {
			fmt.Printf("[StaticRoutes] network reload failed: %v\n", err)
		}
	}()

	return nil
}

// ---------- HTTP Handler: AC-only ----------

func staticRoutesConfigHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	sid := srSIDFromRequest(r)

	if sid == "" && r.Header.Get("Authorization") != "" {
		http.Error(w, `{"error":"invalid authorization header"}`, http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		list, err := getLocalUciRoutes(sid)
		if err != nil {
			fmt.Printf("[StaticRoutes] get routes failed: %v\n", err)
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(list)

	case http.MethodPost:
		var req StaticRouteConfig

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		req.Interface = strings.TrimSpace(req.Interface)
		req.Target = strings.TrimSpace(req.Target)
		req.Netmask = strings.TrimSpace(req.Netmask)
		req.Gateway = strings.TrimSpace(req.Gateway)
		req.Metric = strings.TrimSpace(req.Metric)

		if err := srValidateRoute(req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		err := applyLocalUciRouteChange(sid, func(currSid string) error {
			values := map[string]string{
				"interface": req.Interface,
				"target":    req.Target,
				"netmask":   req.Netmask,
			}

			if req.Gateway != "" {
				values["gateway"] = req.Gateway
			}

			if req.Metric != "" {
				values["metric"] = req.Metric
			}

			_, callErr := ubusCallJSONLocal(currSid, "uci", "add", map[string]any{
				"config": "network",
				"type":   "route",
				"values": values,
			})

			return callErr
		})

		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"add failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(`{"status":"ok"}`))

	case http.MethodDelete:
		section := strings.TrimSpace(r.URL.Query().Get("section"))
		if section == "" {
			http.Error(w, `{"error":"missing section param"}`, http.StatusBadRequest)
			return
		}

		err := applyLocalUciRouteChange(sid, func(currSid string) error {
			_, callErr := ubusCallJSONLocal(currSid, "uci", "delete", map[string]any{
				"config":  "network",
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