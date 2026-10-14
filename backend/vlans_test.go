package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeRouter stands in for uci and ubus. It keeps the three configs as uci
// would and applies `uci batch` scripts to them, so a change can be read back
// the way the handler reads it.
type fakeRouter struct {
	mu      sync.Mutex
	configs map[string]uciConfig
	saved   map[string]uciConfig
	dumps   []string // successive answers to the interface dump; the last repeats
	scripts []string
}

func newFakeRouter() *fakeRouter {
	f := &fakeRouter{configs: map[string]uciConfig{
		"network": parseUCIShow("network", `network.loopback=interface
network.loopback.device='lo'
network.loopback.proto='static'
network.loopback.ipaddr='127.0.0.1'
network.loopback.netmask='255.0.0.0'
network.@device[0]=device
network.@device[0].name='br-lan'
network.@device[0].type='bridge'
network.@device[0].ports='lan1' 'lan2' 'lan3' 'lan4' 'lan5'
network.lan=interface
network.lan.device='br-lan'
network.lan.proto='static'
network.lan.ipaddr='10.10.18.1'
network.lan.netmask='255.255.255.0'
network.wan=interface
network.wan.device='eth1'
network.wan.proto='dhcp'
network.antenna_mgmt=interface
network.antenna_mgmt.device='br-mgmt'
network.antenna_mgmt.proto='static'
network.antenna_mgmt.ipaddr='172.31.255.1'
network.antenna_mgmt.netmask='255.255.255.248'
network.route_x=route
network.route_x.interface='lan'
network.route_x.target='192.168.50.0'
network.route_x.netmask='255.255.255.0'
network.route_x.gateway='10.10.18.20'`),
		"dhcp": parseUCIShow("dhcp", `dhcp.lan=dhcp
dhcp.lan.interface='lan'
dhcp.lan.start='100'
dhcp.lan.limit='150'
dhcp.lan.tag='!rbap'
dhcp.rbap_pool=dhcp
dhcp.rbap_pool.interface='antenna_mgmt'`),
		"firewall": parseUCIShow("firewall", `firewall.@zone[0]=zone
firewall.@zone[0].name='lan'
firewall.@zone[0].network='lan'
firewall.@zone[1]=zone
firewall.@zone[1].name='wan'
firewall.@zone[1].network='wan' 'wan6'`),
	}}
	f.dumps = []string{healthyDump}
	return f
}

const healthyDump = `{"interface":[
{"interface":"lan","up":true,"ipv4-address":[{"address":"10.10.18.1","mask":24}]},
{"interface":"antenna_mgmt","up":true,"ipv4-address":[{"address":"172.31.255.1","mask":29}]},
{"interface":"wan","up":true,"ipv4-address":[{"address":"192.168.1.191","mask":24}]},
{"interface":"loopback","up":true,"ipv4-address":[{"address":"127.0.0.1","mask":8}]}]}`

func copyConfig(c uciConfig) uciConfig {
	out := uciConfig{}
	for name, s := range c {
		opts := map[string]string{}
		for k, v := range s.Options {
			opts[k] = v
		}
		out[name] = uciSection{Type: s.Type, Options: opts}
	}
	return out
}

func (f *fakeRouter) show(config string) string {
	var lines []string
	for name, s := range f.configs[config] {
		lines = append(lines, config+"."+name+"="+s.Type)
		for k, v := range s.Options {
			if !strings.HasPrefix(v, "'") {
				v = "'" + v + "'"
			}
			lines = append(lines, config+"."+name+"."+k+"="+v)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func (f *fakeRouter) batch(lines []string) {
	for _, line := range lines {
		verb, rest, _ := strings.Cut(line, " ")
		key, value, _ := strings.Cut(rest, "=")
		value = strings.Trim(value, "'")
		parts := strings.SplitN(key, ".", 3)
		cfg := f.configs[parts[0]]
		switch {
		case verb == "delete":
			delete(cfg, parts[1])
		case len(parts) == 2:
			cfg[parts[1]] = uciSection{Type: value, Options: map[string]string{}}
		default:
			s := cfg[parts[1]]
			if verb == "add_list" && s.Options[parts[2]] != "" {
				value = s.Options[parts[2]] + " " + value
			}
			s.Options[parts[2]] = value
		}
	}
}

func (f *fakeRouter) shell(script string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scripts = append(f.scripts, script)
	switch {
	case strings.HasPrefix(script, "uci -q show "):
		return f.show(strings.TrimPrefix(script, "uci -q show ")), nil
	case script == "ubus call network.interface dump":
		d := f.dumps[0]
		if len(f.dumps) > 1 {
			f.dumps = f.dumps[1:]
		}
		return d, nil
	case strings.Contains(script, "uci -t "+customerVLANStage+" batch"):
		body := script[strings.Index(script, "\n")+1 : strings.Index(script, "\nLRAP_VLAN_EOF")]
		f.batch(strings.Split(body, "\n"))
	case strings.Contains(script, "mkdir -p "+customerVLANBackupDir):
		f.saved = map[string]uciConfig{}
		for k, v := range f.configs {
			f.saved[k] = copyConfig(v)
		}
	case strings.Contains(script, restoreMarker):
		for k, v := range f.saved {
			f.configs[k] = copyConfig(v)
		}
	}
	return "", nil
}

// What only putting the copies back runs. ("cp " + the directory alone also
// matches the end of "/etc/config/dhcp " when setting them aside.)
var restoreMarker = "cp " + customerVLANBackupDir + "/network /etc/config/network"

func (f *fakeRouter) ran(fragment string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.scripts {
		if strings.Contains(s, fragment) {
			return true
		}
	}
	return false
}

func useFakeRouter(t *testing.T) *fakeRouter {
	t.Helper()
	f := newFakeRouter()
	oldShell, oldPort, oldMesh, oldBusy, oldSettle, oldSleep, oldConfirm := customerVLANShell, customerVLANPortPresent, customerVLANMeshState, customerVLANMeshBusy, customerVLANSettle, customerVLANSleep, customerVLANConfirm
	customerVLANShell = f.shell
	customerVLANPortPresent = func() bool { return true }
	customerVLANMeshState = func() EasyMeshConfig { return EasyMeshConfig{} }
	customerVLANMeshBusy = func() bool { return false }
	customerVLANSettle = func() {}
	customerVLANSleep = func(time.Duration) {}
	t.Cleanup(func() {
		customerVLANShell, customerVLANPortPresent, customerVLANMeshState, customerVLANMeshBusy, customerVLANSettle, customerVLANSleep, customerVLANConfirm = oldShell, oldPort, oldMesh, oldBusy, oldSettle, oldSleep, oldConfirm
		customerVLANMu.Lock()
		if customerVLANPending != nil {
			customerVLANPending.timer.Stop()
			customerVLANPending = nil
		}
		customerVLANMu.Unlock()
	})
	return f
}

func cameras() CustomerVLAN {
	return CustomerVLAN{VID: 10, Label: "Cameras", IPAddr: "192.168.20.1", Netmask: "255.255.255.0", DHCP: true, DHCPStart: 100, DHCPLimit: 100, Access: "internet"}
}

func stateOf(t *testing.T) customerVLANState {
	t.Helper()
	st, err := readCustomerVLANState()
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func TestCustomerVLANValidation(t *testing.T) {
	useFakeRouter(t)
	st := stateOf(t)
	cases := []struct {
		name string
		edit func(*CustomerVLAN)
		want string
	}{
		{"ok", func(*CustomerVLAN) {}, ""},
		{"vlan 1", func(v *CustomerVLAN) { v.VID = 1 }, "VLAN 1"},
		{"vlan 100", func(v *CustomerVLAN) { v.VID = 100 }, "reserved for the Antennas"},
		{"vlan 200", func(v *CustomerVLAN) { v.VID = 200 }, "management"},
		{"vlan 4095", func(v *CustomerVLAN) { v.VID = 4095 }, "between 2 and 4094"},
		{"quote in name", func(v *CustomerVLAN) { v.Label = "it's" }, "name may be"},
		{"access", func(v *CustomerVLAN) { v.Access = "all" }, "access must be"},
		{"main lan", func(v *CustomerVLAN) { v.IPAddr = "10.10.19.1" }, "10.10.18.0/23"},
		{"management", func(v *CustomerVLAN) { v.IPAddr, v.Netmask = "172.31.0.1", "255.255.0.0" }, "172.31.255.0/29"},
		{"wan", func(v *CustomerVLAN) { v.IPAddr = "192.168.1.5" }, "interface wan"},
		{"route", func(v *CustomerVLAN) { v.IPAddr = "192.168.50.1" }, "static route"},
		{"too wide", func(v *CustomerVLAN) { v.Netmask = "255.254.0.0" }, "/16 and /30"},
		{"too narrow", func(v *CustomerVLAN) { v.Netmask = "255.255.255.255" }, "/16 and /30"},
		{"not contiguous", func(v *CustomerVLAN) { v.Netmask = "255.0.255.0" }, "contiguous"},
		{"network address", func(v *CustomerVLAN) { v.IPAddr = "192.168.20.0" }, "network or broadcast"},
		{"broadcast", func(v *CustomerVLAN) { v.IPAddr = "192.168.20.255" }, "network or broadcast"},
		{"loopback", func(v *CustomerVLAN) { v.IPAddr = "127.1.0.1" }, "ordinary unicast"},
		{"dhcp past the end", func(v *CustomerVLAN) { v.DHCPStart, v.DHCPLimit = 200, 60 }, "DHCP range"},
		{"dhcp start 0", func(v *CustomerVLAN) { v.DHCPStart = 0 }, "DHCP range"},
	}
	for _, c := range cases {
		v := cameras()
		c.edit(&v)
		err := validateCustomerVLAN(v, st, 0)
		if c.want == "" && err != nil || c.want != "" && (err == nil || !strings.Contains(err.Error(), c.want)) {
			t.Errorf("%s: got %v, want %q", c.name, err, c.want)
		}
	}
}

func TestCustomerVLANValidationAgainstOtherVLANs(t *testing.T) {
	f := useFakeRouter(t)
	f.batch(customerVLANSetLines(cameras()))
	st := stateOf(t)

	again := cameras()
	if err := validateCustomerVLAN(again, st, 0); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("a second VLAN 10: %v", err)
	}
	other := cameras()
	other.VID = 20
	if err := validateCustomerVLAN(other, st, 0); err == nil || !strings.Contains(err.Error(), "cvlan10") {
		t.Errorf("VLAN 20 on VLAN 10's subnet: %v", err)
	}
	// Editing keeps its own subnet, but not by moving to another ID.
	if err := validateCustomerVLAN(cameras(), st, 10); err != nil {
		t.Errorf("editing VLAN 10 in place: %v", err)
	}
	if err := validateCustomerVLAN(other, st, 10); err == nil || !strings.Contains(err.Error(), "cannot be changed") {
		t.Errorf("moving VLAN 10 to 20: %v", err)
	}
}

// A firewall zone left behind under one of this page's names -- by a pre-Mesh
// restore, which brings back network and dhcp but not the firewall -- does not
// stop the VLAN being added again, and goes before it is.
func TestCustomerVLANClearsLeftoversOnAdd(t *testing.T) {
	f := useFakeRouter(t)
	f.batch(customerVLANSetLines(cameras()))
	delete(f.configs["network"], "cvlan10")
	delete(f.configs["network"], "cvlan10_dev")
	delete(f.configs["dhcp"], "cvlan10")
	body, _ := json.Marshal(cameras())
	code, out := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans", string(body))
	if code != http.StatusOK {
		t.Fatalf("add over leftovers: %d %v", code, out)
	}
	if got := customerVLANsFrom(stateOf(t)); len(got) != 1 || got[0].Access != "internet" {
		t.Errorf("after: %+v", got)
	}
}

func TestCustomerVLANWrittenIntoThePreMeshCopy(t *testing.T) {
	lines := append([]string{"delete firewall.cvlan10_lan", "delete network.cvlan10"}, customerVLANSetLines(cameras())...)
	script := customerVLANSnapshotScript(10, lines)
	dirs := `-c "$d" -t "$t"`
	for _, want := range []string{dirs, "delete network.cvlan10_dev || true", "delete dhcp.cvlan10 || true", "set network.cvlan10.ipaddr='192.168.20.1'", "add_list dhcp.cvlan10.tag='!rbap'", "commit network", "commit dhcp", "|| exit 0"} {
		if !strings.Contains(script, want) {
			t.Errorf("snapshot script lacks %s", want)
		}
	}
	// The copy has no firewall, and the script never runs uci on /etc/config.
	if strings.Contains(script, "firewall") {
		t.Error("snapshot script touches the firewall")
	}
	for _, part := range strings.Split(script, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "uci ") && !strings.Contains(part, dirs) {
			t.Errorf("uci without the copy's directories: %s", part)
		}
	}
}

func TestCustomerVLANLimit(t *testing.T) {
	f := useFakeRouter(t)
	for i := 0; i < customerVLANMax; i++ {
		v := cameras()
		v.VID = 10 + i
		v.IPAddr = "192.168." + itoa(20+i) + ".1"
		f.batch(customerVLANSetLines(v))
	}
	v := cameras()
	v.VID, v.IPAddr = 99, "192.168.99.1"
	if err := validateCustomerVLAN(v, stateOf(t), 0); err == nil || !strings.Contains(err.Error(), "at most") {
		t.Errorf("a ninth VLAN: %v", err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func TestCustomerVLANReadsBackAsWritten(t *testing.T) {
	f := useFakeRouter(t)
	internet := cameras()
	lan := CustomerVLAN{VID: 20, Label: "Office", IPAddr: "192.168.30.1", Netmask: "255.255.255.0", Access: "lan"}
	f.batch(customerVLANSetLines(internet))
	f.batch(customerVLANSetLines(lan))
	got := customerVLANsFrom(stateOf(t))
	want := []CustomerVLAN{internet, lan}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read back %+v, want %+v", got, want)
	}
}

func TestCustomerVLANFirewallFollowsAccess(t *testing.T) {
	join := func(v CustomerVLAN) string { return strings.Join(customerVLANSetLines(v), "\n") }
	internet := join(cameras())
	for _, want := range []string{"firewall.cvlan10.input='REJECT'", "cvlan10_wan.dest='wan'", "cvlan10_dhcp.dest_port='67-68'", "cvlan10_dns.dest_port='53'", "add_list dhcp.cvlan10.tag='!rbap'"} {
		if !strings.Contains(internet, want) {
			t.Errorf("internet-only VLAN lacks %s", want)
		}
	}
	if strings.Contains(internet, "dest='lan'") {
		t.Error("internet-only VLAN forwards to the main LAN")
	}
	v := cameras()
	v.Access, v.DHCP = "lan", false
	lan := join(v)
	for _, want := range []string{"firewall.cvlan10.input='ACCEPT'", "cvlan10_lan.dest='lan'", "cvlan10_fromlan.src='lan'", "dhcp.cvlan10.ignore='1'"} {
		if !strings.Contains(lan, want) {
			t.Errorf("LAN VLAN lacks %s", want)
		}
	}
	// The vendor EasyMesh script finds its bridge by name.
	for _, line := range customerVLANSetLines(v) {
		key, value, _ := strings.Cut(line, "=")
		if strings.Contains(key, "br") || strings.Contains(value, "br") {
			t.Errorf("a name containing br: %s", line)
		}
	}
}

func TestCustomerVLANDeleteTouchesOnlyItsOwn(t *testing.T) {
	f := useFakeRouter(t)
	one := cameras()
	one.VID = 1000
	ten := cameras()
	ten.VID, ten.IPAddr = 10, "192.168.40.1"
	f.batch(customerVLANSetLines(one))
	f.batch(customerVLANSetLines(ten))
	for _, line := range customerVLANDeleteLines(stateOf(t), 10) {
		_, section, _ := strings.Cut(strings.TrimPrefix(line, "delete "), ".")
		if !strings.HasPrefix(line, "delete ") || section != "cvlan10" && !strings.HasPrefix(section, "cvlan10_") {
			t.Errorf("deleting VLAN 10 runs %q", line)
		}
	}
	f.batch(customerVLANDeleteLines(stateOf(t), 10))
	if got := customerVLANsFrom(stateOf(t)); len(got) != 1 || got[0].VID != 1000 {
		t.Errorf("left %+v", got)
	}
	for _, name := range []string{"lan", "wan", "antenna_mgmt", "route_x", "@device[0]"} {
		if _, ok := f.configs["network"][name]; !ok {
			t.Errorf("network.%s went too", name)
		}
	}
}

func TestCustomerVLANLockedByMesh(t *testing.T) {
	for _, m := range []EasyMeshConfig{{Enabled: true}, {ActivationPending: true}, {AgentHandoff: true}} {
		if customerVLANLock(m, false) != "mesh" {
			t.Errorf("%+v does not lock", m)
		}
	}
	if customerVLANLock(EasyMeshConfig{}, true) != "mesh" {
		t.Error("a running Mesh job does not lock")
	}
	// Every current chassis reports NetworkPrepared: it is no reason to lock.
	if customerVLANLock(EasyMeshConfig{NetworkPrepared: true}, false) != "" {
		t.Error("a standalone chassis is locked")
	}
}

func asUser(user string, r *http.Request) *http.Request {
	if user == "" {
		return r
	}
	return r.WithContext(context.WithValue(r.Context(), userKey{}, user))
}

func vlanCall(t *testing.T, user, method, path, body string) (int, map[string]any) {
	t.Helper()
	r := asUser(user, httptest.NewRequest(method, path, strings.NewReader(body)))
	w := httptest.NewRecorder()
	if strings.HasSuffix(path, "/confirm") {
		customerVLANConfirmHandler(w, r)
	} else {
		customerVLANsHandler(w, r)
	}
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func TestCustomerVLANAddConfirm(t *testing.T) {
	f := useFakeRouter(t)
	body, _ := json.Marshal(cameras())

	if code, _ := vlanCall(t, "", http.MethodPost, "/api/net/vlans", string(body)); code != http.StatusForbidden {
		t.Errorf("the agent's session added a VLAN: %d", code)
	}
	code, out := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans", string(body))
	if code != http.StatusOK {
		t.Fatalf("add: %d %v", code, out)
	}
	if !f.ran("mkdir -p "+customerVLANBackupDir) || !f.ran("ubus call network reload") || !f.ran("/etc/init.d/firewall reload") {
		t.Errorf("did not set aside, reload network and firewall: %q", f.scripts)
	}
	// A second change waits for the first.
	if code, out := vlanCall(t, "admin", http.MethodDelete, "/api/net/vlans?vid=10", ""); code != http.StatusConflict || out["code"] != "change_pending" {
		t.Errorf("change while one is pending: %d %v", code, out)
	}
	if code, _ := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans/confirm", `{"change_id":"nope"}`); code != http.StatusGone {
		t.Errorf("confirming the wrong change: %d", code)
	}
	if code, _ := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans/confirm", `{"change_id":"`+out["change_id"].(string)+`"}`); code != http.StatusOK {
		t.Errorf("confirm: %d", code)
	}
	code, out = vlanCall(t, "admin", http.MethodGet, "/api/net/vlans", "")
	if vlans, _ := out["vlans"].([]any); code != http.StatusOK || len(vlans) != 1 {
		t.Errorf("list after add: %d %v", code, out)
	}
	if f.ran(restoreMarker) {
		t.Error("a confirmed change was undone")
	}
	if !f.ran(easyMeshBackupPath) {
		t.Error("the confirmed change was not written into the pre-Mesh copy")
	}
}

func TestCustomerVLANUndoneWithoutConfirmation(t *testing.T) {
	f := useFakeRouter(t)
	customerVLANConfirm = 20 * time.Millisecond
	body, _ := json.Marshal(cameras())
	if code, out := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans", string(body)); code != http.StatusOK {
		t.Fatalf("add: %d %v", code, out)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !f.ran(restoreMarker) {
		time.Sleep(5 * time.Millisecond)
	}
	if !f.ran(restoreMarker) {
		t.Fatal("an unconfirmed change was kept")
	}
	if got := customerVLANsFrom(stateOf(t)); len(got) != 0 {
		t.Errorf("after the undo: %+v", got)
	}
}

func TestCustomerVLANUndoneWhenOtherNetworksChange(t *testing.T) {
	f := useFakeRouter(t)
	// Before the change the LAN is healthy; after it, the management network
	// never comes back.
	f.dumps = []string{healthyDump, strings.Replace(healthyDump, `"interface":"antenna_mgmt","up":true`, `"interface":"antenna_mgmt","up":false`, 1)}
	body, _ := json.Marshal(cameras())
	code, out := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans", string(body))
	if code != http.StatusInternalServerError || !strings.Contains(out["error"].(string), "undone") {
		t.Fatalf("got %d %v", code, out)
	}
	if got := customerVLANsFrom(stateOf(t)); len(got) != 0 {
		t.Errorf("after the undo: %+v", got)
	}
	if customerVLANPending != nil {
		t.Error("an undone change is still pending")
	}
}

func TestCustomerVLANRefusedDuringMesh(t *testing.T) {
	useFakeRouter(t)
	customerVLANMeshState = func() EasyMeshConfig { return EasyMeshConfig{Enabled: true} }
	body, _ := json.Marshal(cameras())
	if code, out := vlanCall(t, "admin", http.MethodPost, "/api/net/vlans", string(body)); code != http.StatusConflict || out["code"] != "mesh_active" {
		t.Errorf("add during a Mesh: %d %v", code, out)
	}
}

func TestParseUCIShowLists(t *testing.T) {
	c := parseUCIShow("firewall", "firewall.@zone[1]=zone\nfirewall.@zone[1].network='wan' 'wan6'\nfirewall.@zone[1].name='wan'")
	if c["@zone[1]"].Type != "zone" || c["@zone[1]"].Options["name"] != "wan" || c["@zone[1]"].Options["network"] != "'wan' 'wan6'" {
		t.Errorf("parsed %+v", c)
	}
}
