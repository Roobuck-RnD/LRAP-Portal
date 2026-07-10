package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ---------- Data Structures ----------

type FirewallDefaults struct {
	Section        string `json:"section"`
	SynFlood       bool   `json:"syn_flood"`
	DropInvalid    bool   `json:"drop_invalid"`
	Input          string `json:"input"`
	Output         string `json:"output"`
	Forward        string `json:"forward"`
	FlowOffloading bool   `json:"flow_offloading"`
}

type FirewallZone struct {
	Section     string   `json:"section"`
	Name        string   `json:"name"`
	Networks    []string `json:"networks"`
	Input       string   `json:"input"`
	Output      string   `json:"output"`
	Forward     string   `json:"forward"`
	Masq        bool     `json:"masq"`
	Forwardings []string `json:"forwardings"`
}

type FirewallPortForward struct {
	Section  string `json:"section"`
	Name     string `json:"name"`
	Enabled  bool   `json:"enabled"`
	Proto    string `json:"proto"`
	Src      string `json:"src"`
	SrcDIP   string `json:"src_dip"`
	SrcDPort string `json:"src_dport"`
	Dest     string `json:"dest"`
	DestIP   string `json:"dest_ip"`
	DestPort string `json:"dest_port"`
	Target   string `json:"target"`
}

type FirewallResponse struct {
	Defaults     FirewallDefaults      `json:"defaults"`
	Zones        []FirewallZone         `json:"zones"`
	PortForwards []FirewallPortForward `json:"port_forwards"`
}

type FirewallActionReq struct {
	Action string `json:"action"`

	Section        string `json:"section"`
	SynFlood       *bool  `json:"syn_flood"`
	FlowOffloading *bool  `json:"flow_offloading"`

	Name     string `json:"name"`
	Enabled  *bool  `json:"enabled"`
	Proto    string `json:"proto"`
	Src      string `json:"src"`
	SrcDIP   string `json:"src_dip"`
	SrcDPort string `json:"src_dport"`
	Dest     string `json:"dest"`
	DestIP   string `json:"dest_ip"`
	DestPort string `json:"dest_port"`
}

// ---------- Handler ----------

func firewallHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(auth), "bearer ") {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	sid := strings.TrimSpace(auth[len("Bearer "):])
	if sid == "" {
		http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		resp, err := fwGetOverview(sid)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_ = json.NewEncoder(w).Encode(resp)

	case http.MethodPost:
		var req FirewallActionReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"bad json"}`, http.StatusBadRequest)
			return
		}

		if err := fwHandleAction(sid, req); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%v"}`, err), http.StatusInternalServerError)
			return
		}

		_, _ = w.Write([]byte(`{"ok":true}`))

	default:
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
	}
}

// ---------- GET Overview ----------

func fwGetOverview(sid string) (FirewallResponse, error) {
	defaults, err := fwGetDefaults(sid)
	if err != nil {
		return FirewallResponse{}, err
	}

	zones, err := fwGetZones(sid)
	if err != nil {
		return FirewallResponse{}, err
	}

	portForwards, err := fwGetPortForwards(sid)
	if err != nil {
		return FirewallResponse{}, err
	}

	if zones == nil {
		zones = []FirewallZone{}
	}

	if portForwards == nil {
		portForwards = []FirewallPortForward{}
	}

	return FirewallResponse{
		Defaults:     defaults,
		Zones:        zones,
		PortForwards: portForwards,
	}, nil
}

func fwGetDefaults(sid string) (FirewallDefaults, error) {
	res, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "firewall",
		"type":   "defaults",
	})
	if err != nil {
		return FirewallDefaults{}, fmt.Errorf("get defaults failed: %v", err)
	}

	values, _ := res["values"].(map[string]any)

	out := FirewallDefaults{
		Section:        "@defaults[0]",
		SynFlood:       true,
		DropInvalid:    false,
		Input:          "accept",
		Output:         "accept",
		Forward:        "reject",
		FlowOffloading: false,
	}

	for section, raw := range values {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		out.Section = section
		out.SynFlood = fwBool(m["syn_flood"], true)
		out.DropInvalid = fwBool(m["drop_invalid"], false)
		out.Input = fwStringDefault(m["input"], "accept")
		out.Output = fwStringDefault(m["output"], "accept")
		out.Forward = fwStringDefault(m["forward"], "reject")
		out.FlowOffloading = fwBool(m["flow_offloading"], false)

		break
	}

	return out, nil
}

func fwGetZones(sid string) ([]FirewallZone, error) {
	existingNetworks := fwGetNetworkInterfaceNames(sid)

	zonesRes, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "firewall",
		"type":   "zone",
	})
	if err != nil {
		return nil, fmt.Errorf("get zones failed: %v", err)
	}

	forwardRes, _ := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "firewall",
		"type":   "forwarding",
	})

	forwardsBySrc := map[string][]string{}

	if forwardRes != nil {
		if values, ok := forwardRes["values"].(map[string]any); ok {
			for _, raw := range values {
				m, ok := raw.(map[string]any)
				if !ok {
					continue
				}

				src := fwValueToString(m["src"])
				dest := fwValueToString(m["dest"])

				if src != "" && dest != "" {
					forwardsBySrc[src] = append(forwardsBySrc[src], dest)
				}
			}
		}
	}

	values, _ := zonesRes["values"].(map[string]any)
	out := make([]FirewallZone, 0)

	for section, raw := range values {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		name := fwValueToString(m["name"])

		rawNetworks := fwValueToList(m["network"])
		networks := fwFilterExistingNetworks(rawNetworks, existingNetworks)
		if networks == nil {
			networks = []string{}
		}

		forwardings := forwardsBySrc[name]
		if forwardings == nil {
			forwardings = []string{}
		}

		out = append(out, FirewallZone{
			Section:     section,
			Name:        name,
			Networks:    networks,
			Input:       fwStringDefault(m["input"], "-"),
			Output:      fwStringDefault(m["output"], "-"),
			Forward:     fwStringDefault(m["forward"], "-"),
			Masq:        fwBool(m["masq"], false),
			Forwardings: forwardings,
		})
	}

	fwSortZones(out)

	return out, nil
}

func fwGetPortForwards(sid string) ([]FirewallPortForward, error) {
	res, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "firewall",
		"type":   "redirect",
	})
	if err != nil {
		return nil, fmt.Errorf("get redirects failed: %v", err)
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return []FirewallPortForward{}, nil
	}

	out := make([]FirewallPortForward, 0)

	for section, raw := range values {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}

		target := fwValueToString(m["target"])
		if target != "" && strings.ToUpper(target) != "DNAT" {
			continue
		}

		enabled := true
		if v := fwValueToString(m["enabled"]); v == "0" {
			enabled = false
		}

		out = append(out, FirewallPortForward{
			Section:  section,
			Name:     fwValueToString(m["name"]),
			Enabled:  enabled,
			Proto:    fwStringDefault(m["proto"], "tcp"),
			Src:      fwStringDefault(m["src"], "lan"),
			SrcDIP:   fwValueToString(m["src_dip"]),
			SrcDPort: fwValueToString(m["src_dport"]),
			Dest:     fwStringDefault(m["dest"], "wan"),
			DestIP:   fwValueToString(m["dest_ip"]),
			DestPort: fwValueToString(m["dest_port"]),
			Target:   "DNAT",
		})
	}

	return out, nil
}

// ---------- Existing Network Filter ----------

func fwGetNetworkInterfaceNames(sid string) map[string]bool {
	out := map[string]bool{}

	res, err := ubusCallJSONLocal(sid, "uci", "get", map[string]any{
		"config": "network",
		"type":   "interface",
	})
	if err != nil {
		return out
	}

	values, ok := res["values"].(map[string]any)
	if !ok {
		return out
	}

	for section := range values {
		section = strings.TrimSpace(section)
		if section != "" {
			out[section] = true
		}
	}

	return out
}

func fwFilterExistingNetworks(networks []string, existing map[string]bool) []string {
	out := make([]string, 0)

	for _, n := range networks {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}

		if existing[n] {
			out = append(out, n)
		}
	}

	return out
}

// ---------- POST Actions ----------

func fwHandleAction(sid string, req FirewallActionReq) error {
	req.Action = strings.TrimSpace(req.Action)

	switch req.Action {
	case "update_settings":
		return fwUpdateSettings(sid, req)

	case "create_forward":
		return fwCreateForward(sid, req)

	case "update_forward":
		return fwUpdateForward(sid, req)

	case "toggle_forward":
		return fwToggleForward(sid, req)

	case "delete_forward":
		return fwDeleteForward(sid, req)

	default:
		return fmt.Errorf("unknown firewall action")
	}
}

func fwUpdateSettings(sid string, req FirewallActionReq) error {
	defaults, err := fwGetDefaults(sid)
	if err != nil {
		return err
	}

	values := map[string]string{}

	if req.SynFlood != nil {
		values["syn_flood"] = fwBoolToUCI(*req.SynFlood)
	}

	if req.FlowOffloading != nil {
		values["flow_offloading"] = fwBoolToUCI(*req.FlowOffloading)
	}

	if len(values) == 0 {
		return fmt.Errorf("no settings to update")
	}

	_, err = ubusCallJSONLocal(sid, "uci", "set", map[string]any{
		"config":  "firewall",
		"section": defaults.Section,
		"values":  values,
	})
	if err != nil {
		return fmt.Errorf("uci set defaults failed: %v", err)
	}

	return fwCommitAndReload(sid)
}

func fwCreateForward(sid string, req FirewallActionReq) error {
	values, err := fwBuildForwardValues(req)
	if err != nil {
		return err
	}

	_, err = ubusCallJSONLocal(sid, "uci", "add", map[string]any{
		"config": "firewall",
		"type":   "redirect",
		"values": values,
	})
	if err != nil {
		return fmt.Errorf("uci add redirect failed: %v", err)
	}

	return fwCommitAndReload(sid)
}

func fwUpdateForward(sid string, req FirewallActionReq) error {
	req.Section = strings.TrimSpace(req.Section)
	if req.Section == "" {
		return fmt.Errorf("missing section")
	}

	values, err := fwBuildForwardValues(req)
	if err != nil {
		return err
	}

	_, err = ubusCallJSONLocal(sid, "uci", "set", map[string]any{
		"config":  "firewall",
		"section": req.Section,
		"values":  values,
	})
	if err != nil {
		return fmt.Errorf("uci set redirect failed: %v", err)
	}

	return fwCommitAndReload(sid)
}

func fwToggleForward(sid string, req FirewallActionReq) error {
	req.Section = strings.TrimSpace(req.Section)
	if req.Section == "" {
		return fmt.Errorf("missing section")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	_, err := ubusCallJSONLocal(sid, "uci", "set", map[string]any{
		"config":  "firewall",
		"section": req.Section,
		"values": map[string]string{
			"enabled": fwBoolToUCI(enabled),
		},
	})
	if err != nil {
		return fmt.Errorf("uci toggle redirect failed: %v", err)
	}

	return fwCommitAndReload(sid)
}

func fwDeleteForward(sid string, req FirewallActionReq) error {
	req.Section = strings.TrimSpace(req.Section)
	if req.Section == "" {
		return fmt.Errorf("missing section")
	}

	_, err := ubusCallJSONLocal(sid, "uci", "delete", map[string]any{
		"config":  "firewall",
		"section": req.Section,
	})
	if err != nil {
		return fmt.Errorf("uci delete redirect failed: %v", err)
	}

	return fwCommitAndReload(sid)
}

func fwBuildForwardValues(req FirewallActionReq) (map[string]string, error) {
	req.Name = strings.TrimSpace(req.Name)
	req.Proto = strings.TrimSpace(req.Proto)
	req.Src = strings.TrimSpace(req.Src)
	req.SrcDIP = strings.TrimSpace(req.SrcDIP)
	req.SrcDPort = strings.TrimSpace(req.SrcDPort)
	req.Dest = strings.TrimSpace(req.Dest)
	req.DestIP = strings.TrimSpace(req.DestIP)
	req.DestPort = strings.TrimSpace(req.DestPort)

	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}

	if !fwValidProto(req.Proto) {
		return nil, fmt.Errorf("invalid protocol")
	}

	if !fwValidZone(req.Src) || !fwValidZone(req.Dest) {
		return nil, fmt.Errorf("invalid zone")
	}

	if req.SrcDIP != "" && net.ParseIP(req.SrcDIP) == nil {
		return nil, fmt.Errorf("invalid external IP")
	}

	if !fwValidPort(req.SrcDPort) {
		return nil, fmt.Errorf("invalid external port")
	}

	if net.ParseIP(req.DestIP) == nil {
		return nil, fmt.Errorf("invalid internal IP")
	}

	if !fwValidPort(req.DestPort) {
		return nil, fmt.Errorf("invalid internal port")
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	values := map[string]string{
		"name":      req.Name,
		"target":    "DNAT",
		"proto":     req.Proto,
		"src":       req.Src,
		"src_dport": req.SrcDPort,
		"dest":      req.Dest,
		"dest_ip":   req.DestIP,
		"dest_port": req.DestPort,
		"enabled":   fwBoolToUCI(enabled),
	}

	if req.SrcDIP != "" {
		values["src_dip"] = req.SrcDIP
	}

	return values, nil
}

func fwCommitAndReload(sid string) error {
	if _, err := ubusCallJSONLocal(sid, "uci", "commit", map[string]any{
		"config": "firewall",
	}); err != nil {
		return fmt.Errorf("uci commit firewall failed: %v", err)
	}

	go func() {
		time.Sleep(500 * time.Millisecond)
		_ = exec.Command("/etc/init.d/firewall", "reload").Run()
	}()

	return nil
}

// ---------- Validators ----------

func fwValidProto(v string) bool {
	switch v {
	case "tcp", "udp", "tcp udp":
		return true
	default:
		return false
	}
}

func fwValidZone(v string) bool {
	switch v {
	case "lan", "wan":
		return true
	default:
		return false
	}
}

func fwValidPort(v string) bool {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	return err == nil && n >= 1 && n <= 65535
}

// ---------- Helpers ----------

func fwValueToString(v any) string {
	switch t := v.(type) {
	case string:
		return t

	case []any:
		parts := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, " ")

	case float64:
		return strconv.Itoa(int(t))

	case bool:
		if t {
			return "1"
		}
		return "0"

	default:
		return ""
	}
}

func fwValueToList(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return []string{}
		}
		return []string{t}

	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out

	default:
		return []string{}
	}
}

func fwStringDefault(v any, fallback string) string {
	s := fwValueToString(v)
	if s == "" {
		return fallback
	}
	return s
}

func fwBool(v any, fallback bool) bool {
	switch t := v.(type) {
	case bool:
		return t

	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		if s == "1" || s == "true" || s == "yes" {
			return true
		}
		if s == "0" || s == "false" || s == "no" {
			return false
		}

	case float64:
		return int(t) != 0
	}

	return fallback
}

func fwBoolToUCI(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

func fwSortZones(items []FirewallZone) {
	weight := func(name string) int {
		switch name {
		case "lan":
			return 0
		case "wan":
			return 1
		default:
			return 10
		}
	}

	for i := 0; i < len(items); i++ {
		for j := i + 1; j < len(items); j++ {
			wi := weight(items[i].Name)
			wj := weight(items[j].Name)

			if wj < wi || (wj == wi && items[j].Name < items[i].Name) {
				items[i], items[j] = items[j], items[i]
			}
		}
	}
}