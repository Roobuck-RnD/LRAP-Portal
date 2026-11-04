package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// Blocked clients: devices the customer has stopped from reaching the internet.
//
// A blocked device keeps its place on its own network: it still joins the
// Wi-Fi, gets an address and reaches the portal and the devices beside it. What
// it loses is everything the router forwards for it, out to the WAN and across
// to the customer VLANs. Routing happens only on the main module, so one place
// covers a device whichever Antenna or port it is on.
//
// Each device is a firewall rule of its own, lrap_block_<mac>, which is where
// the list lives: fw3 applies it from the moment the firewall starts, it is kept
// through upgrades with the rest of /etc/config, and LuCI and the cloud's
// firewall view see it as an ordinary rule. Those rules only stop new
// connections, though, because fw3 lets established ones through before it
// looks at any rule. So the same list is also written as a firewall include,
// which fw3 runs after every start and reload, putting the devices into
// forwarding_rule, the one chain that comes before that: a device that was
// online loses the connections it already had, not only its next ones.
//
// Everything this file writes is named lrap_block_* or lrap_blocked_clients,
// in the firewall config alone, and it reads, changes and deletes nothing else.

const (
	blockedClientPrefix  = "lrap_block_"
	blockedClientInclude = "lrap_blocked_clients"
	blockedClientStage   = "/tmp/.uci-lrap-block-apply"
	blockedClientMax     = 64
)

// Where the include lives: in /var, so it is never stale after an upgrade or a
// factory reset; startup writes it again (writeBlockedClientsScriptAtStartup).
// A var for the tests.
var blockedClientsScriptPath = "/var/etc/lrap-blocked-clients.fw"

var blockedClientLabelChars = regexp.MustCompile(`[^A-Za-z0-9 ._-]`)

type BlockedClient struct {
	MAC   string `json:"mac"`
	Label string `json:"label"`
}

type BlockedClientList struct {
	Max     int             `json:"max"`
	Clients []BlockedClient `json:"clients"`
}

// The device, behind seams so the logic can be tested without one.
var (
	blockedClientsShell = func(script string) (string, error) {
		return customerVLANShell(script)
	}
	// MACs that are part of the chassis itself, never a client.
	blockedClientsProtected = chassisOwnMACs
	// The MAC of the device a request comes from, when the router knows it.
	blockedClientsRequesterMAC = requesterMAC
)

var blockedClientsMu sync.Mutex

// ---------- reading ----------

func readBlockedClients() (uciConfig, []BlockedClient, error) {
	out, err := blockedClientsShell("uci -q show firewall")
	if err != nil {
		return nil, nil, fmt.Errorf("read firewall: %v", err)
	}
	fw := parseUCIShow("firewall", out)
	return fw, blockedClientsFrom(fw), nil
}

func blockedClientsFrom(fw uciConfig) []BlockedClient {
	clients := []BlockedClient{}
	for name, s := range fw {
		if s.Type != "rule" || !strings.HasPrefix(name, blockedClientPrefix) {
			continue
		}
		mac := normaliseClientMAC(s.Options["src_mac"])
		if mac == "" {
			continue
		}
		clients = append(clients, BlockedClient{
			MAC:   mac,
			Label: strings.TrimPrefix(s.Options["name"], "Blocked: "),
		})
	}
	sort.Slice(clients, func(i, j int) bool { return clients[i].MAC < clients[j].MAC })
	return clients
}

// normaliseClientMAC answers the MAC in upper case with colons, or "" if it is
// not a single device's address.
func normaliseClientMAC(mac string) string {
	mac = strings.ToUpper(strings.TrimSpace(strings.ReplaceAll(mac, "-", ":")))
	if !ccValidMAC(mac) || !ccIsUnicastMAC(mac) {
		return ""
	}
	return mac
}

func blockedClientSection(mac string) string {
	return blockedClientPrefix + strings.ToLower(strings.ReplaceAll(mac, ":", ""))
}

func blockedClientLabel(label string) string {
	label = strings.TrimSpace(blockedClientLabelChars.ReplaceAllString(label, ""))
	if len(label) > 32 {
		label = strings.TrimSpace(label[:32])
	}
	return label
}

// ---------- writing ----------

func blockedClientSetLines(c BlockedClient) []string {
	s := "firewall." + blockedClientSection(c.MAC)
	name := "Blocked: " + c.MAC
	if c.Label != "" {
		name = "Blocked: " + c.Label
	}
	return []string{
		"set " + s + "=rule",
		"set " + s + ".name='" + name + "'",
		"set " + s + ".src='*'",
		"set " + s + ".dest='*'",
		"set " + s + ".src_mac='" + c.MAC + "'",
		"set " + s + ".target='REJECT'",
		"set firewall." + blockedClientInclude + "=include",
		"set firewall." + blockedClientInclude + ".path='" + blockedClientsScriptPath + "'",
		"set firewall." + blockedClientInclude + ".type='script'",
		"set firewall." + blockedClientInclude + ".reload='1'",
	}
}

func blockedClientDeleteLines(mac string, remaining int) []string {
	lines := []string{"delete firewall." + blockedClientSection(mac)}
	if remaining == 0 {
		lines = append(lines, "delete firewall."+blockedClientInclude)
	}
	return lines
}

func blockedClientCommitScript(lines []string) string {
	u := "uci -t " + blockedClientStage
	return "set -e; rm -rf " + blockedClientStage + "; mkdir -p " + blockedClientStage + "; " +
		u + " batch <<'LRAP_BLOCK_EOF'\n" + strings.Join(lines, "\n") + "\nLRAP_BLOCK_EOF\n" +
		u + " commit firewall; rm -rf " + blockedClientStage
}

// blockedClientsScript is the include fw3 runs. It inserts each device at the
// head of forwarding_rule, for IPv4 and IPv6, into fw3's own reject chain
// (a TCP reset, otherwise port unreachable).
//
// It first takes out whatever it put there before: a reload removes only the
// rules fw3 made itself, so without that a reload would leave a device blocked
// twice, and a device allowed again still blocked. The handler runs it too,
// after every change, because once nothing is blocked the include is gone and
// fw3 no longer would.
func blockedClientsScript(clients []BlockedClient) string {
	var b strings.Builder
	b.WriteString("# Written by lrapServer (blocked_clients.go) from the lrap_block_* rules in\n")
	b.WriteString("# /etc/config/firewall. fw3 runs it after every start and reload.\n")
	b.WriteString("for t in iptables ip6tables; do\n")
	b.WriteString("\t$t -w -S forwarding_rule 2>/dev/null | grep -- '--comment lrap-blocked' | sed 's/^-A /-D /' |\n")
	b.WriteString("\t\twhile read -r rule; do $t -w $rule; done\n")
	b.WriteString("done\n")
	for _, c := range clients {
		for _, tool := range []string{"iptables", "ip6tables"} {
			b.WriteString(tool + " -w -I forwarding_rule -m mac --mac-source " + c.MAC +
				" -m comment --comment lrap-blocked -j reject\n")
		}
	}
	return b.String()
}

func writeBlockedClientsScript(clients []BlockedClient) error {
	if err := os.MkdirAll(filepath.Dir(blockedClientsScriptPath), 0o755); err != nil {
		return err
	}
	tmp := blockedClientsScriptPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(blockedClientsScript(clients)), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, blockedClientsScriptPath)
}

// applyBlockedClients commits lines, writes the include from what was
// committed and reloads the firewall. The caller holds blockedClientsMu.
func applyBlockedClients(lines []string) error {
	if out, err := blockedClientsShell(blockedClientCommitScript(lines)); err != nil {
		return fmt.Errorf("the change could not be saved: %v %s", err, strings.TrimSpace(out))
	}
	_, clients, err := readBlockedClients()
	if err != nil {
		return err
	}
	if err := writeBlockedClientsScript(clients); err != nil {
		return fmt.Errorf("saved, but the firewall include could not be written: %v", err)
	}
	if out, err := blockedClientsShell("/etc/init.d/firewall reload"); err != nil {
		return fmt.Errorf("saved, but the firewall did not reload: %v %s", err, strings.TrimSpace(out))
	}
	if out, err := blockedClientsShell("sh " + blockedClientsScriptPath); err != nil {
		return fmt.Errorf("saved, but the firewall include did not run: %v %s", err, strings.TrimSpace(out))
	}
	return nil
}

// writeBlockedClientsScriptAtStartup puts the include back after a reboot,
// where /var starts empty. The rules already stop new connections, and nothing
// was connected before the router started, so the firewall need not reload.
func writeBlockedClientsScriptAtStartup() {
	blockedClientsMu.Lock()
	defer blockedClientsMu.Unlock()
	_, clients, err := readBlockedClients()
	if err != nil {
		log.Printf("[block] %v", err)
		return
	}
	if len(clients) == 0 {
		return
	}
	if err := writeBlockedClientsScript(clients); err != nil {
		log.Printf("[block] write %s: %v", blockedClientsScriptPath, err)
	}
}

// ---------- guards ----------

// chassisOwnMACs is every address the main module and its Antennas use
// themselves. Blocking one would cut the chassis off from its own parts.
func chassisOwnMACs() map[string]bool {
	out := map[string]bool{}
	if entries, err := os.ReadDir(netSysfsRoot); err == nil {
		for _, e := range entries {
			if mac := ccReadSysfsMAC(e.Name()); mac != "" {
				out[strings.ToUpper(mac)] = true
			}
		}
	}
	for _, m := range discoverManagedAPs(false) {
		if m.MAC != "" {
			out[strings.ToUpper(m.MAC)] = true
		}
	}
	for mac := range easyMeshLocalBackhaulStationMACs() {
		out[strings.ToUpper(mac)] = true
	}
	return out
}

// requesterMAC looks the request's address up in the ARP table.
func requesterMAC(r *http.Request) string {
	addr := clientIP(r)
	if first, _, ok := strings.Cut(addr, ","); ok {
		addr = first
	}
	addr = strings.TrimSpace(addr)
	if host, _, err := net.SplitHostPort(addr); err == nil {
		addr = host
	}
	for mac, entry := range ccParseLocalARPByMAC() {
		if entry.IP == addr {
			return mac
		}
	}
	return ""
}

// ---------- HTTP ----------

// blockedClientsHandler: GET lists; POST {mac, label} blocks; DELETE ?mac=
// allows again.
func blockedClientsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	blockedClientsMu.Lock()
	defer blockedClientsMu.Unlock()

	_, clients, err := readBlockedClients()
	if err != nil {
		writeVLANError(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeVLANJSON(w, http.StatusOK, BlockedClientList{Max: blockedClientMax, Clients: clients})

	case http.MethodPost:
		var req BlockedClient
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeVLANError(w, http.StatusBadRequest, "bad_json", "bad json")
			return
		}
		mac := normaliseClientMAC(req.MAC)
		if mac == "" {
			writeVLANError(w, http.StatusBadRequest, "invalid_mac", "Not a device's MAC address.")
			return
		}
		if blockedClientsProtected()[mac] {
			writeVLANError(w, http.StatusConflict, "chassis_device", "This address belongs to the router or one of its antennas.")
			return
		}
		if blockedClientsRequesterMAC(r) == mac {
			writeVLANError(w, http.StatusConflict, "own_device", "This is the device you are using. Block it from another one.")
			return
		}
		already := false
		for _, c := range clients {
			already = already || c.MAC == mac
		}
		if !already && len(clients) >= blockedClientMax {
			writeVLANError(w, http.StatusConflict, "too_many", fmt.Sprintf("At most %d devices can be blocked.", blockedClientMax))
			return
		}
		c := BlockedClient{MAC: mac, Label: blockedClientLabel(req.Label)}
		if err := applyBlockedClients(blockedClientSetLines(c)); err != nil {
			writeVLANError(w, http.StatusInternalServerError, "apply_failed", err.Error())
			return
		}
		log.Printf("[block] %s blocked (%s)", mac, c.Label)
		writeVLANJSON(w, http.StatusOK, c)

	case http.MethodDelete:
		mac := normaliseClientMAC(r.URL.Query().Get("mac"))
		found := false
		for _, c := range clients {
			found = found || c.MAC == mac
		}
		if mac == "" || !found {
			writeVLANError(w, http.StatusNotFound, "not_blocked", "This device is not blocked.")
			return
		}
		if err := applyBlockedClients(blockedClientDeleteLines(mac, len(clients)-1)); err != nil {
			writeVLANError(w, http.StatusInternalServerError, "apply_failed", err.Error())
			return
		}
		log.Printf("[block] %s allowed again", mac)
		writeVLANJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		writeVLANError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}
