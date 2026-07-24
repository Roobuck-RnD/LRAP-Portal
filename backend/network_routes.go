package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
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

var srInterfaceNamePattern = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

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
	if !srInterfaceNamePattern.MatchString(req.Interface) {
		return fmt.Errorf("invalid interface")
	}

	if req.Target == "" {
		return fmt.Errorf("target is required")
	}

	if req.Netmask == "" {
		return fmt.Errorf("netmask is required")
	}

	if _, _, err := srRoutePrefix(req.Target, req.Netmask); err != nil {
		return err
	}

	if req.Gateway != "" && net.ParseIP(req.Gateway).To4() == nil {
		return fmt.Errorf("gateway must be a valid IPv4 address")
	}

	if req.Metric != "" {
		if _, err := strconv.ParseUint(req.Metric, 10, 32); err != nil {
			return fmt.Errorf("metric must be a non-negative integer")
		}
	}

	return nil
}

func srRoutePrefix(target string, netmask string) (string, *net.IPNet, error) {
	targetIP := net.ParseIP(strings.TrimSpace(target)).To4()
	if targetIP == nil {
		return "", nil, fmt.Errorf("target must be a valid IPv4 address")
	}

	maskIP := net.ParseIP(strings.TrimSpace(netmask)).To4()
	if maskIP == nil {
		return "", nil, fmt.Errorf("netmask must be a valid IPv4 netmask")
	}

	mask := net.IPMask(maskIP)
	ones, bits := mask.Size()
	if bits != 32 {
		return "", nil, fmt.Errorf("netmask must be contiguous")
	}

	networkIP := targetIP.Mask(mask)
	if !targetIP.Equal(networkIP) {
		return "", nil, fmt.Errorf("target must be the network address for the selected netmask")
	}

	network := &net.IPNet{IP: networkIP, Mask: mask}
	return fmt.Sprintf("%s/%d", networkIP.String(), ones), network, nil
}

func srRouteConflictsWithLocalNetwork(routeNetwork *net.IPNet, localPrefixBits int) bool {
	if routeNetwork == nil {
		return false
	}

	routePrefixBits, _ := routeNetwork.Mask.Size()
	if routePrefixBits < localPrefixBits {
		// Less-specific routes, including the default route, cannot override the
		// directly connected local network.
		return false
	}

	protectedAddresses := append([]string{ACManagementIP}, APManagementIPs()...)
	for _, address := range protectedAddresses {
		if ip := net.ParseIP(address).To4(); ip != nil && routeNetwork.Contains(ip) {
			return true
		}
	}

	return false
}

func srLocalPrefixBits(sid string) int {
	status, err := ubusCallJSONLocal(sid, "network.interface.lan", "status", nil)
	if err == nil {
		if addresses, ok := status["ipv4-address"].([]any); ok {
			for _, raw := range addresses {
				address, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				if mask, ok := address["mask"].(float64); ok && mask >= 0 && mask <= 32 {
					return int(mask)
				}
			}
		}
	}

	// The current product default is /23. Falling back here keeps protected
	// local addresses safe if interface status is temporarily unavailable.
	return 23
}

func srResolveInterfaceDevice(sid string, interfaceName string) (string, error) {
	if !srInterfaceNamePattern.MatchString(interfaceName) {
		return "", fmt.Errorf("invalid interface")
	}

	status, err := ubusCallJSONLocal(sid, "network.interface."+interfaceName, "status", nil)
	if err != nil {
		return "", fmt.Errorf("interface is unavailable")
	}

	for _, key := range []string{"l3_device", "device"} {
		if device := strings.TrimSpace(srGetString(status, key)); device != "" {
			return device, nil
		}
	}

	return "", fmt.Errorf("interface has no active network device")
}

func srLiveRouteArgs(action string, route StaticRouteConfig, device string) ([]string, error) {
	if action != "replace" && action != "del" {
		return nil, fmt.Errorf("unsupported route action")
	}

	prefix, _, err := srRoutePrefix(route.Target, route.Netmask)
	if err != nil {
		return nil, err
	}

	args := []string{"-4", "route", action, prefix}
	if gateway := strings.TrimSpace(route.Gateway); gateway != "" {
		args = append(args, "via", gateway)
	}
	if device = strings.TrimSpace(device); device != "" {
		args = append(args, "dev", device)
	}
	if metric := strings.TrimSpace(route.Metric); metric != "" {
		args = append(args, "metric", metric)
	}
	args = append(args, "table", "main")

	return args, nil
}

func srApplyLiveRoute(action string, route StaticRouteConfig, device string) error {
	args, err := srLiveRouteArgs(action, route, device)
	if err != nil {
		return err
	}

	output, err := exec.Command("/sbin/ip", args...).CombinedOutput()
	if err == nil {
		return nil
	}

	message := strings.TrimSpace(string(output))
	if action == "del" &&
		(strings.Contains(strings.ToLower(message), "no such process") ||
			strings.Contains(strings.ToLower(message), "not found")) {
		// The persistent route has still been removed and there is no matching
		// live route left to delete.
		return nil
	}
	if message == "" {
		message = err.Error()
	}

	return fmt.Errorf("failed to update active route: %s", message)
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

func getLocalUciRoute(headerSid string, section string) (StaticRouteConfig, error) {
	sid, err := srResolveLocalSID(headerSid)
	if err != nil {
		return StaticRouteConfig{}, err
	}

	res, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config":  "network",
		"section": section,
	})
	if err != nil {
		return StaticRouteConfig{}, fmt.Errorf("route not found")
	}

	values, ok := res["values"].(map[string]any)
	if !ok || srGetString(values, ".type") != "route" {
		return StaticRouteConfig{}, fmt.Errorf("route not found")
	}

	return StaticRouteConfig{
		Section:   section,
		Interface: srGetString(values, "interface"),
		Target:    srGetString(values, "target"),
		Netmask:   srGetString(values, "netmask"),
		Gateway:   srGetString(values, "gateway"),
		Metric:    srGetString(values, "metric"),
	}, nil
}

func srCommitNetwork(sid string) error {
	if _, err := ubusCallJSONLocal(sid, "uci", "commit", map[string]any{
		"config": "network",
	}); err != nil {
		return fmt.Errorf("commit failed: %v", err)
	}

	return nil
}

func srAddUciRoute(sid string, section string, route StaticRouteConfig) error {
	values := map[string]string{
		"interface": route.Interface,
		"target":    route.Target,
		"netmask":   route.Netmask,
	}
	if route.Gateway != "" {
		values["gateway"] = route.Gateway
	}
	if route.Metric != "" {
		values["metric"] = route.Metric
	}

	_, err := ubusCallJSONLocal(sid, "uci", "add", map[string]any{
		"config": "network",
		"type":   "route",
		"name":   section,
		"values": values,
	})
	return err
}

func srDeleteUciRoute(sid string, section string) error {
	_, err := ubusCallJSONLocal(sid, "uci", "delete", map[string]any{
		"config":  "network",
		"section": section,
	})
	return err
}

func srRestoreRoute(sid string, route StaticRouteConfig) {
	if err := srAddUciRoute(sid, route.Section, route); err != nil {
		fmt.Printf("[StaticRoutes] rollback add failed: %v\n", err)
		return
	}
	if err := srCommitNetwork(sid); err != nil {
		fmt.Printf("[StaticRoutes] rollback commit failed: %v\n", err)
	}
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

		currSid, err := srResolveLocalSID(sid)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		device, err := srResolveInterfaceDevice(currSid, req.Interface)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusBadRequest)
			return
		}

		_, routeNetwork, _ := srRoutePrefix(req.Target, req.Netmask)
		if srRouteConflictsWithLocalNetwork(routeNetwork, srLocalPrefixBits(currSid)) {
			http.Error(w, `{"error":"route conflicts with the current local network"}`, http.StatusBadRequest)
			return
		}

		section := "route_" + strconv.FormatInt(time.Now().UnixNano(), 36)
		if err := srAddUciRoute(currSid, section, req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"add failed: %v"}`, err), http.StatusInternalServerError)
			return
		}
		if err := srCommitNetwork(currSid); err != nil {
			_ = srDeleteUciRoute(currSid, section)
			http.Error(w, fmt.Sprintf(`{"error":"add failed: %v"}`, err), http.StatusInternalServerError)
			return
		}
		if err := srApplyLiveRoute("replace", req, device); err != nil {
			_ = srDeleteUciRoute(currSid, section)
			_ = srCommitNetwork(currSid)
			http.Error(w, fmt.Sprintf(`{"error":"add failed: %v"}`, err), http.StatusBadRequest)
			return
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":  "ok",
			"section": section,
		})

	case http.MethodDelete:
		section := strings.TrimSpace(r.URL.Query().Get("section"))
		if section == "" {
			http.Error(w, `{"error":"missing section param"}`, http.StatusBadRequest)
			return
		}

		currSid, err := srResolveLocalSID(sid)
		if err != nil {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}

		route, err := getLocalUciRoute(currSid, section)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusNotFound)
			return
		}
		if err := srValidateRoute(route); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: invalid route configuration"}`), http.StatusBadRequest)
			return
		}

		device, _ := srResolveInterfaceDevice(currSid, route.Interface)
		if err := srDeleteUciRoute(currSid, section); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusInternalServerError)
			return
		}
		if err := srCommitNetwork(currSid); err != nil {
			srRestoreRoute(currSid, route)
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusInternalServerError)
			return
		}
		if err := srApplyLiveRoute("del", route, device); err != nil {
			srRestoreRoute(currSid, route)
			http.Error(w, fmt.Sprintf(`{"error":"delete failed: %v"}`, err), http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(`{"status":"ok"}`))

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}
