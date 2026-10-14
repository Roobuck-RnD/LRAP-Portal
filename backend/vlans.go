package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Customer VLANs: tagged networks on the customer port, each with its own
// subnet, optional DHCP pool and firewall zone.
//
// Only lan5 carries them. lan1-lan4 belong to the Antennas, whose own ports the
// portal rewrites on every adoption pass, so a tag sent there goes nowhere; and
// the adoption pass borrows those ports out of br-lan, which a customer bridge
// would break. Wi-Fi clients stay on the main LAN: the Antennas bridge every
// SSID untagged.
//
// Everything this file writes is named cvlan<vid> or cvlan<vid>_*, in network,
// dhcp and firewall, and it reads, changes and deletes nothing else. No bridge
// is made and no name contains "br": the vendor EasyMesh script finds its
// bridge by name.
//
// A change is applied with a way back. The three files are copied aside, the
// change is committed and loaded, and the main LAN, the Antenna management
// network and the WAN are checked to be as they were; otherwise the copies go
// back. The browser then has to confirm within a minute, which it does by
// itself if it can still reach the portal; if it cannot, the copies go back.

const (
	customerVLANPort      = "lan5"
	customerVLANMax       = 8
	customerVLANBackupDir = "/tmp/lrap-vlan-backup"
)

// How long the browser has to confirm a change. A var for the tests.
var customerVLANConfirm = 60 * time.Second

var customerVLANConfigs = []string{"network", "dhcp", "firewall"}

// VLAN IDs a customer VLAN may not use, and why.
var customerVLANReserved = map[int]string{
	1:                      "VLAN 1 is the untagged default on most switches",
	retiredClientVLAN:      "VLAN 100 is reserved for the Antennas",
	easyMeshManagementVLAN: "VLAN 200 is the Antenna management network",
}

// Address ranges a customer VLAN may never overlap, whatever the current
// configuration says: the main LAN at its widest and the Antenna management
// network.
var customerVLANProtectedNets = []string{"10.10.18.0/23", "172.31.255.0/29"}

var (
	customerVLANSection = regexp.MustCompile(`^cvlan([0-9]{1,4})(_[a-z]+)?$`)
	customerVLANLabel   = regexp.MustCompile(`^[A-Za-z0-9 ._-]{0,32}$`)
)

type CustomerVLAN struct {
	VID       int    `json:"vid"`
	Label     string `json:"label"`
	IPAddr    string `json:"ipaddr"`
	Netmask   string `json:"netmask"`
	DHCP      bool   `json:"dhcp"`
	DHCPStart int    `json:"dhcp_start"`
	DHCPLimit int    `json:"dhcp_limit"`
	// "internet": out to the WAN only; the router answers DHCP and DNS and
	// nothing else. "lan": the same standing as the main LAN.
	Access string `json:"access"`
	Up     bool   `json:"up"`
}

type CustomerVLANList struct {
	Port        string         `json:"port"`
	PortPresent bool           `json:"port_present"`
	Max         int            `json:"max"`
	Reserved    []int          `json:"reserved"`
	Locked      string         `json:"locked,omitempty"`
	VLANs       []CustomerVLAN `json:"vlans"`
}

// ---------- reading the configuration ----------

type uciSection struct {
	Type    string
	Options map[string]string
}

type uciConfig map[string]uciSection

// parseUCIShow reads `uci show <config>`. A list option keeps its values
// space-separated, still quoted, which is all this file needs of lists.
func parseUCIShow(config, text string) uciConfig {
	out := uciConfig{}
	prefix := config + "."
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		key, value, ok := strings.Cut(line[len(prefix):], "=")
		if !ok {
			continue
		}
		section, option, isOption := strings.Cut(key, ".")
		s, exists := out[section]
		if !exists {
			s = uciSection{Options: map[string]string{}}
		}
		if isOption {
			s.Options[option] = uciUnquote(value)
		} else {
			s.Type = value
		}
		out[section] = s
	}
	return out
}

func uciUnquote(value string) string {
	if len(value) >= 2 && strings.HasPrefix(value, "'") && strings.HasSuffix(value, "'") && !strings.Contains(value[1:len(value)-1], "' '") {
		return value[1 : len(value)-1]
	}
	return value
}

type liveInterface struct {
	Name  string
	Up    bool
	Nets  []*net.IPNet
	Addrs []string
}

type customerVLANState struct {
	Network  uciConfig
	DHCP     uciConfig
	Firewall uciConfig
	Live     map[string]liveInterface
}

// The device, behind seams so the logic can be tested without one.
var (
	customerVLANShell = func(script string) (string, error) {
		out, err := exec.Command("/bin/sh", "-c", script).CombinedOutput()
		return string(out), err
	}
	customerVLANPortPresent = func() bool {
		_, err := os.Stat(filepath.Join(netSysfsRoot, customerVLANPort))
		return err == nil
	}
	customerVLANMeshState = loadEasyMeshConfig
	customerVLANMeshBusy  = func() bool {
		easyMeshRuntime.Lock()
		defer easyMeshRuntime.Unlock()
		return easyMeshRuntime.Job.Running
	}
	customerVLANSettle = settleClientBridgeAfterReload
	customerVLANSleep  = time.Sleep
)

func readCustomerVLANState() (customerVLANState, error) {
	st := customerVLANState{}
	for _, config := range customerVLANConfigs {
		out, err := customerVLANShell("uci -q show " + config)
		if err != nil {
			return st, fmt.Errorf("read %s: %v", config, err)
		}
		parsed := parseUCIShow(config, out)
		switch config {
		case "network":
			st.Network = parsed
		case "dhcp":
			st.DHCP = parsed
		case "firewall":
			st.Firewall = parsed
		}
	}
	live, err := readLiveInterfaces()
	if err != nil {
		return st, err
	}
	st.Live = live
	return st, nil
}

func readLiveInterfaces() (map[string]liveInterface, error) {
	out, err := customerVLANShell("ubus call network.interface dump")
	if err != nil {
		return nil, fmt.Errorf("read interface status: %v", err)
	}
	return parseInterfaceDump(out)
}

func parseInterfaceDump(text string) (map[string]liveInterface, error) {
	var dump struct {
		Interface []struct {
			Interface string `json:"interface"`
			Up        bool   `json:"up"`
			IPv4      []struct {
				Address string `json:"address"`
				Mask    int    `json:"mask"`
			} `json:"ipv4-address"`
		} `json:"interface"`
	}
	if err := json.Unmarshal([]byte(text), &dump); err != nil {
		return nil, fmt.Errorf("read interface status: %v", err)
	}
	out := map[string]liveInterface{}
	for _, i := range dump.Interface {
		li := liveInterface{Name: i.Interface, Up: i.Up}
		for _, a := range i.IPv4 {
			if _, n, err := net.ParseCIDR(fmt.Sprintf("%s/%d", a.Address, a.Mask)); err == nil {
				li.Nets = append(li.Nets, n)
				li.Addrs = append(li.Addrs, fmt.Sprintf("%s/%d", a.Address, a.Mask))
			}
		}
		sort.Strings(li.Addrs)
		out[i.Interface] = li
	}
	return out, nil
}

// customerVLANsFrom lists the VLANs this file made, from their interface
// sections and the sections that go with them.
func customerVLANsFrom(st customerVLANState) []CustomerVLAN {
	var out []CustomerVLAN
	for name, s := range st.Network {
		m := customerVLANSection.FindStringSubmatch(name)
		if m == nil || m[2] != "" || s.Type != "interface" {
			continue
		}
		vid, _ := strconv.Atoi(m[1])
		v := CustomerVLAN{
			VID:     vid,
			Label:   s.Options["lrap_label"],
			IPAddr:  s.Options["ipaddr"],
			Netmask: s.Options["netmask"],
			Access:  "internet",
			Up:      st.Live[name].Up,
		}
		if d, ok := st.DHCP[name]; ok && d.Type == "dhcp" && d.Options["ignore"] != "1" {
			v.DHCP = true
			v.DHCPStart, _ = strconv.Atoi(d.Options["start"])
			v.DHCPLimit, _ = strconv.Atoi(d.Options["limit"])
		}
		if f, ok := st.Firewall[name+"_lan"]; ok && f.Type == "forwarding" {
			v.Access = "lan"
		}
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].VID < out[j].VID })
	return out
}

// customerVLANLock says why VLANs cannot be changed now, or "".
//
// Not while a Mesh is on, or on its way on or off: leaving it copies back the
// network and DHCP files from before it was enabled, and a Mesh job rewrites
// both as it goes. (NetworkPrepared is no reason: it only says the Antenna
// management network is up, which on a current chassis it always is.)
// Outside a Mesh, every confirmed change is also written into that pre-Mesh
// copy, so a later restore keeps it (customerVLANSyncSnapshot).
func customerVLANLock(mesh EasyMeshConfig, jobRunning bool) string {
	if mesh.Enabled || mesh.ActivationPending || mesh.AgentHandoff || jobRunning {
		return "mesh"
	}
	return ""
}

// ---------- validation ----------

var errVLANNotFound = errors.New("vlan not found")

func normaliseCustomerVLAN(v *CustomerVLAN) {
	v.Label = strings.TrimSpace(v.Label)
	v.IPAddr = strings.TrimSpace(v.IPAddr)
	v.Netmask = strings.TrimSpace(v.Netmask)
	v.Access = strings.TrimSpace(v.Access)
	if v.Access == "" {
		v.Access = "internet"
	}
	if !v.DHCP {
		v.DHCPStart, v.DHCPLimit = 0, 0
	}
}

// validateCustomerVLAN checks a VLAN against itself and against everything
// else on the router. editing is the VID being changed, or 0 for a new one.
func validateCustomerVLAN(v CustomerVLAN, st customerVLANState, editing int) error {
	if v.VID < 1 || v.VID > 4094 {
		return fmt.Errorf("VLAN ID must be between 2 and 4094")
	}
	if why, reserved := customerVLANReserved[v.VID]; reserved {
		return errors.New(why)
	}
	existing := customerVLANsFrom(st)
	if editing == 0 {
		for _, e := range existing {
			if e.VID == v.VID {
				return fmt.Errorf("VLAN %d already exists", v.VID)
			}
		}
		if len(existing) >= customerVLANMax {
			return fmt.Errorf("at most %d VLANs", customerVLANMax)
		}
	} else if editing != v.VID {
		return fmt.Errorf("the VLAN ID cannot be changed; delete the VLAN and add it again")
	}
	if !customerVLANLabel.MatchString(v.Label) {
		return fmt.Errorf("name may be up to 32 letters, digits, spaces, dots, dashes or underscores")
	}
	if v.Access != "internet" && v.Access != "lan" {
		return fmt.Errorf("access must be internet or lan")
	}

	subnet, err := customerVLANSubnet(v.IPAddr, v.Netmask)
	if err != nil {
		return err
	}
	if v.DHCP {
		ones, _ := subnet.Mask.Size()
		hosts := (1 << (32 - ones)) - 2
		if v.DHCPStart < 1 || v.DHCPLimit < 1 || v.DHCPStart+v.DHCPLimit-1 > hosts {
			return fmt.Errorf("the DHCP range must lie within the subnet (start 1 or more, start + count - 1 at most %d)", hosts)
		}
	}
	if what := customerVLANOverlap(subnet, st, "cvlan"+strconv.Itoa(v.VID)); what != "" {
		return fmt.Errorf("%s overlaps %s", subnet, what)
	}
	return nil
}

func customerVLANSubnet(ipaddr, netmask string) (*net.IPNet, error) {
	ip := net.ParseIP(ipaddr).To4()
	if ip == nil {
		return nil, fmt.Errorf("router address must be an IPv4 address")
	}
	maskIP := net.ParseIP(netmask).To4()
	if maskIP == nil {
		return nil, fmt.Errorf("netmask must be an IPv4 netmask")
	}
	mask := net.IPMask(maskIP)
	ones, bits := mask.Size()
	if bits != 32 {
		return nil, fmt.Errorf("netmask must be contiguous")
	}
	if ones < 16 || ones > 30 {
		return nil, fmt.Errorf("netmask must be between /16 and /30")
	}
	if !ip.IsGlobalUnicast() || ip.IsLinkLocalUnicast() {
		return nil, fmt.Errorf("router address must be an ordinary unicast address")
	}
	subnet := &net.IPNet{IP: ip.Mask(mask), Mask: mask}
	broadcast := make(net.IP, 4)
	for i := range broadcast {
		broadcast[i] = subnet.IP[i] | ^mask[i]
	}
	if ip.Equal(subnet.IP) || ip.Equal(broadcast) {
		return nil, fmt.Errorf("router address cannot be the network or broadcast address")
	}
	return subnet, nil
}

func netsOverlap(a, b *net.IPNet) bool {
	return a.Contains(b.IP) || b.Contains(a.IP)
}

// customerVLANOverlap names what subnet would overlap, or returns "". self is
// the VLAN's own interface, left out when it is being edited.
func customerVLANOverlap(subnet *net.IPNet, st customerVLANState, self string) string {
	for _, cidr := range customerVLANProtectedNets {
		_, n, _ := net.ParseCIDR(cidr)
		if netsOverlap(subnet, n) {
			return "a network the router reserves (" + cidr + ")"
		}
	}
	names := make([]string, 0, len(st.Network))
	for name := range st.Network {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		s := st.Network[name]
		if name == self {
			continue
		}
		switch s.Type {
		case "interface":
			if n := uciInterfaceNet(s); n != nil && netsOverlap(subnet, n) {
				return "interface " + name + " (" + n.String() + ")"
			}
		case "route":
			if _, n, err := srRoutePrefix(s.Options["target"], s.Options["netmask"]); err == nil && netsOverlap(subnet, n) {
				return "a static route (" + n.String() + ")"
			}
		}
	}
	live := make([]string, 0, len(st.Live))
	for name := range st.Live {
		live = append(live, name)
	}
	sort.Strings(live)
	for _, name := range live {
		if name == self {
			continue
		}
		for _, n := range st.Live[name].Nets {
			if netsOverlap(subnet, n) {
				return "interface " + name + " (" + n.String() + ")"
			}
		}
	}
	return ""
}

func uciInterfaceNet(s uciSection) *net.IPNet {
	addr := s.Options["ipaddr"]
	if addr == "" {
		return nil
	}
	if strings.Contains(addr, "/") {
		_, n, err := net.ParseCIDR(strings.Fields(addr)[0])
		if err != nil {
			return nil
		}
		return n
	}
	ip := net.ParseIP(addr).To4()
	mask := net.ParseIP(s.Options["netmask"]).To4()
	if ip == nil || mask == nil {
		return nil
	}
	return &net.IPNet{IP: ip.Mask(net.IPMask(mask)), Mask: net.IPMask(mask)}
}

// customerVLANConflict is for the interfaces page: whether an address it is
// about to give one of its own interfaces would overlap a customer VLAN.
func customerVLANConflict(ipaddr, netmask string) error {
	ip := net.ParseIP(ipaddr).To4()
	mask := net.ParseIP(netmask).To4()
	if ip == nil || mask == nil {
		return nil
	}
	st, err := readCustomerVLANState()
	if err != nil {
		return nil
	}
	n := &net.IPNet{IP: ip.Mask(net.IPMask(mask)), Mask: net.IPMask(mask)}
	for _, v := range customerVLANsFrom(st) {
		if vn, err := customerVLANSubnet(v.IPAddr, v.Netmask); err == nil && netsOverlap(n, vn) {
			return fmt.Errorf("%s overlaps VLAN %d (%s)", n, v.VID, vn)
		}
	}
	return nil
}

// ---------- the commands ----------

// customerVLANOwned lists every section this file made for vid, in each config.
func customerVLANOwned(st customerVLANState, vid int) []string {
	var out []string
	for _, c := range []struct {
		name string
		cfg  uciConfig
	}{{"network", st.Network}, {"dhcp", st.DHCP}, {"firewall", st.Firewall}} {
		for name := range c.cfg {
			if m := customerVLANSection.FindStringSubmatch(name); m != nil && m[1] == strconv.Itoa(vid) {
				out = append(out, c.name+"."+name)
			}
		}
	}
	sort.Strings(out)
	return out
}

func customerVLANDeleteLines(st customerVLANState, vid int) []string {
	var out []string
	for _, s := range customerVLANOwned(st, vid) {
		out = append(out, "delete "+s)
	}
	return out
}

// customerVLANSetLines writes one VLAN from scratch. Every value has been
// validated, and none can hold a quote.
func customerVLANSetLines(v CustomerVLAN) []string {
	id := "cvlan" + strconv.Itoa(v.VID)
	device := fmt.Sprintf("%s.%d", customerVLANPort, v.VID)
	lines := []string{
		"set network." + id + "_dev=device",
		"set network." + id + "_dev.type='8021q'",
		"set network." + id + "_dev.ifname='" + customerVLANPort + "'",
		"set network." + id + "_dev.vid='" + strconv.Itoa(v.VID) + "'",
		"set network." + id + "_dev.name='" + device + "'",
		"set network." + id + "=interface",
		"set network." + id + ".proto='static'",
		"set network." + id + ".device='" + device + "'",
		"set network." + id + ".ipaddr='" + v.IPAddr + "'",
		"set network." + id + ".netmask='" + v.Netmask + "'",
	}
	if v.Label != "" {
		lines = append(lines, "set network."+id+".lrap_label='"+v.Label+"'")
	}
	lines = append(lines,
		"set dhcp."+id+"=dhcp",
		"set dhcp."+id+".interface='"+id+"'",
	)
	if v.DHCP {
		lines = append(lines,
			"set dhcp."+id+".start='"+strconv.Itoa(v.DHCPStart)+"'",
			"set dhcp."+id+".limit='"+strconv.Itoa(v.DHCPLimit)+"'",
			"set dhcp."+id+".leasetime='12h'",
			// Never an Antenna's pool, as on the main LAN (dhcp_protection.go).
			"add_list dhcp."+id+".tag='!rbap'",
		)
	} else {
		lines = append(lines, "set dhcp."+id+".ignore='1'")
	}
	input := "REJECT"
	if v.Access == "lan" {
		input = "ACCEPT"
	}
	lines = append(lines,
		"set firewall."+id+"=zone",
		"set firewall."+id+".name='"+id+"'",
		"add_list firewall."+id+".network='"+id+"'",
		"set firewall."+id+".input='"+input+"'",
		"set firewall."+id+".output='ACCEPT'",
		"set firewall."+id+".forward='REJECT'",
		"set firewall."+id+"_wan=forwarding",
		"set firewall."+id+"_wan.src='"+id+"'",
		"set firewall."+id+"_wan.dest='wan'",
	)
	if v.Access == "lan" {
		lines = append(lines,
			"set firewall."+id+"_lan=forwarding",
			"set firewall."+id+"_lan.src='"+id+"'",
			"set firewall."+id+"_lan.dest='lan'",
			"set firewall."+id+"_fromlan=forwarding",
			"set firewall."+id+"_fromlan.src='lan'",
			"set firewall."+id+"_fromlan.dest='"+id+"'",
		)
	} else {
		lines = append(lines,
			"set firewall."+id+"_dhcp=rule",
			"set firewall."+id+"_dhcp.name='"+id+" DHCP'",
			"set firewall."+id+"_dhcp.src='"+id+"'",
			"set firewall."+id+"_dhcp.proto='udp'",
			"set firewall."+id+"_dhcp.dest_port='67-68'",
			"set firewall."+id+"_dhcp.target='ACCEPT'",
			"set firewall."+id+"_dns=rule",
			"set firewall."+id+"_dns.name='"+id+" DNS'",
			"set firewall."+id+"_dns.src='"+id+"'",
			"set firewall."+id+"_dns.proto='tcp udp'",
			"set firewall."+id+"_dns.dest_port='53'",
			"set firewall."+id+"_dns.target='ACCEPT'",
		)
	}
	return lines
}

// ---------- applying with a way back ----------

type customerVLANChange struct {
	ID    string
	VID   int
	Lines []string
	timer *time.Timer
}

var customerVLANMu sync.Mutex
var customerVLANPending *customerVLANChange

var errVLANChangePending = errors.New("change_pending")

func customerVLANBackupScript() string {
	var b strings.Builder
	b.WriteString("set -e; rm -rf " + customerVLANBackupDir + "; mkdir -p " + customerVLANBackupDir + "; ")
	for _, c := range customerVLANConfigs {
		b.WriteString("cp /etc/config/" + c + " " + customerVLANBackupDir + "/" + c + "; ")
	}
	b.WriteString("sync")
	return b.String()
}

func customerVLANRestoreScript() string {
	var b strings.Builder
	b.WriteString("set -e; ")
	for _, c := range customerVLANConfigs {
		b.WriteString("test -f " + customerVLANBackupDir + "/" + c + "; ")
	}
	for _, c := range customerVLANConfigs {
		b.WriteString("cp " + customerVLANBackupDir + "/" + c + " /etc/config/" + c + "; ")
	}
	b.WriteString("rm -rf " + customerVLANStage + "; sync")
	return b.String()
}

// customerVLANStage is uci's change directory for these edits alone, so a
// commit here never carries anyone else's staged changes, nor theirs ours.
const customerVLANStage = "/tmp/.uci-lrap-vlan-apply"

func customerVLANCommitScript(lines []string) string {
	u := "uci -t " + customerVLANStage
	return "set -e; rm -rf " + customerVLANStage + "; mkdir -p " + customerVLANStage + "; " +
		u + " batch <<'LRAP_VLAN_EOF'\n" + strings.Join(lines, "\n") + "\nLRAP_VLAN_EOF\n" +
		u + " commit network; " + u + " commit dhcp; " + u + " commit firewall; rm -rf " + customerVLANStage
}

// customerVLANLoad makes the committed files the running configuration.
func customerVLANLoad() {
	if out, err := customerVLANShell("ubus call network reload"); err != nil {
		log.Printf("[VLAN] network reload: %v %s", err, strings.TrimSpace(out))
	}
	// A reload drops this chassis's radios out of the client bridge.
	customerVLANSettle()
	if out, err := customerVLANShell("/etc/init.d/dnsmasq restart; /etc/init.d/firewall reload"); err != nil {
		log.Printf("[VLAN] dnsmasq/firewall reload: %v %s", err, strings.TrimSpace(out))
	}
}

// customerVLANVitals is what a VLAN change must leave exactly as it found it.
func customerVLANVitals(live map[string]liveInterface) string {
	var parts []string
	for _, name := range []string{"lan", "antenna_mgmt", "wan"} {
		i, ok := live[name]
		if !ok {
			parts = append(parts, name+"=absent")
			continue
		}
		parts = append(parts, fmt.Sprintf("%s=%v:%s", name, i.Up, strings.Join(i.Addrs, ",")))
	}
	return strings.Join(parts, " ")
}

// customerVLANApply commits lines, loads them, checks the rest of the router,
// and leaves a change waiting for confirmation. It returns the change's ID.
func customerVLANApply(vid int, lines []string, before map[string]liveInterface) (string, error) {
	if customerVLANPending != nil {
		return "", errVLANChangePending
	}
	want := customerVLANVitals(before)
	if out, err := customerVLANShell(customerVLANBackupScript()); err != nil {
		return "", fmt.Errorf("could not set the current configuration aside, nothing was changed: %v %s", err, strings.TrimSpace(out))
	}
	if out, err := customerVLANShell(customerVLANCommitScript(lines)); err != nil {
		customerVLANRevert("commit failed")
		return "", fmt.Errorf("the change could not be saved and was undone: %v %s", err, strings.TrimSpace(out))
	}
	customerVLANLoad()

	// netifd brings interfaces back asynchronously; give it a while.
	got := ""
	for i := 0; i < 20; i++ {
		live, err := readLiveInterfaces()
		if err == nil {
			got = customerVLANVitals(live)
			if got == want {
				break
			}
		}
		customerVLANSleep(time.Second)
	}
	if got != want {
		log.Printf("[VLAN] after the change: %s; before: %s", got, want)
		customerVLANRevert("the router's other networks changed")
		return "", fmt.Errorf("the change disturbed the router's other networks, so it was undone")
	}

	id := newCustomerVLANChangeID()
	change := &customerVLANChange{ID: id, VID: vid, Lines: lines}
	change.timer = time.AfterFunc(customerVLANConfirm, func() {
		customerVLANMu.Lock()
		defer customerVLANMu.Unlock()
		if customerVLANPending == change {
			customerVLANRevert("not confirmed in time")
		}
	})
	customerVLANPending = change
	return id, nil
}

// customerVLANRevert puts the copies back and loads them. The caller holds
// customerVLANMu.
func customerVLANRevert(why string) {
	log.Printf("[VLAN] undoing the last change: %s", why)
	if customerVLANPending != nil {
		customerVLANPending.timer.Stop()
		customerVLANPending = nil
	}
	if out, err := customerVLANShell(customerVLANRestoreScript()); err != nil {
		log.Printf("[VLAN] restore failed: %v %s", err, strings.TrimSpace(out))
		return
	}
	customerVLANLoad()
	_, _ = customerVLANShell("rm -rf " + customerVLANBackupDir)
}

func customerVLANConfirmChange(id string) bool {
	customerVLANMu.Lock()
	defer customerVLANMu.Unlock()
	if customerVLANPending == nil || customerVLANPending.ID != id {
		return false
	}
	change := customerVLANPending
	change.timer.Stop()
	customerVLANPending = nil
	_, _ = customerVLANShell("rm -rf " + customerVLANBackupDir)
	if out, err := customerVLANShell(customerVLANSnapshotScript(change.VID, change.Lines)); err != nil {
		log.Printf("[VLAN] could not write VLAN %d into the pre-Mesh copy: %v %s", change.VID, err, strings.TrimSpace(out))
	}
	return true
}

// customerVLANSnapshotScript writes a confirmed change into the pre-Mesh copy
// of network and dhcp (easyMeshBackupPath), so restoring that copy keeps the
// customer's VLANs. The firewall is not in the copy and keeps its own.
//
// The copy may or may not hold this VLAN, whatever the router holds, so its
// sections are deleted one by one, tolerating absence, before the change's
// own set lines run. uci gets the copy's directory (-c) and a change directory
// of its own (-t): with the default one, these staged edits would be
// committed into /etc/config by the next plain `uci commit`.
func customerVLANSnapshotScript(vid int, lines []string) string {
	id := "cvlan" + strconv.Itoa(vid)
	var b strings.Builder
	b.WriteString("d=" + shellSingleQuote(easyMeshBackupPath) + "; t=/tmp/.uci-lrap-vlan; ")
	b.WriteString("[ -f \"$d/complete\" ] && [ -f \"$d/network\" ] && [ -f \"$d/dhcp\" ] || exit 0; ")
	b.WriteString("set -e; rm -rf \"$t\"; mkdir -p \"$t\"; ")
	b.WriteString("uci -q -c \"$d\" -t \"$t\" delete network." + id + "_dev || true; ")
	b.WriteString("uci -q -c \"$d\" -t \"$t\" delete network." + id + " || true; ")
	b.WriteString("uci -q -c \"$d\" -t \"$t\" delete dhcp." + id + " || true; ")
	var keep []string
	for _, line := range lines {
		if (strings.HasPrefix(line, "set ") || strings.HasPrefix(line, "add_list ")) &&
			(strings.Contains(line, " network.") || strings.Contains(line, " dhcp.")) {
			keep = append(keep, line)
		}
	}
	if len(keep) > 0 {
		b.WriteString("uci -c \"$d\" -t \"$t\" batch <<'LRAP_VLAN_EOF'\n" + strings.Join(keep, "\n") + "\nLRAP_VLAN_EOF\n")
	}
	b.WriteString("uci -c \"$d\" -t \"$t\" commit network; uci -c \"$d\" -t \"$t\" commit dhcp; rm -rf \"$t\"; sync")
	return b.String()
}

// recoverUnconfirmedVLANChange runs at startup. Copies still set aside mean
// the process stopped with a change unconfirmed, so it is undone.
func recoverUnconfirmedVLANChange() {
	if _, err := os.Stat(filepath.Join(customerVLANBackupDir, "network")); err != nil {
		return
	}
	customerVLANMu.Lock()
	defer customerVLANMu.Unlock()
	customerVLANRevert("found unconfirmed at startup")
}

func newCustomerVLANChangeID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ---------- HTTP ----------

func writeVLANJSON(w http.ResponseWriter, status int, v any) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeVLANError(w http.ResponseWriter, status int, code, message string) {
	writeVLANJSON(w, status, map[string]string{"error": message, "code": code})
}

// customerVLANsHandler: GET lists; POST adds; PUT ?vid= changes; DELETE ?vid=
// removes. A change answers with change_id, to be confirmed at
// /api/net/vlans/confirm.
func customerVLANsHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == http.MethodGet {
		st, err := readCustomerVLANState()
		if err != nil {
			writeVLANError(w, http.StatusInternalServerError, "read_failed", err.Error())
			return
		}
		list := CustomerVLANList{
			Port:        customerVLANPort,
			PortPresent: customerVLANPortPresent(),
			Max:         customerVLANMax,
			Locked:      customerVLANLock(customerVLANMeshState(), customerVLANMeshBusy()),
			VLANs:       customerVLANsFrom(st),
		}
		for vid := range customerVLANReserved {
			list.Reserved = append(list.Reserved, vid)
		}
		sort.Ints(list.Reserved)
		if list.VLANs == nil {
			list.VLANs = []CustomerVLAN{}
		}
		writeVLANJSON(w, http.StatusOK, list)
		return
	}

	// Only someone signed in to the portal changes anything. The agent's own
	// rpcd session reads the router; the shell this runs is root regardless.
	if sessionUser(r) == "" {
		writeVLANError(w, http.StatusForbidden, "forbidden", "sign in to the portal to change VLANs")
		return
	}

	var req CustomerVLAN
	vid, _ := strconv.Atoi(r.URL.Query().Get("vid"))
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
			writeVLANError(w, http.StatusBadRequest, "bad_json", "bad json")
			return
		}
		normaliseCustomerVLAN(&req)
		if r.Method == http.MethodPut && req.VID == 0 {
			req.VID = vid
		}
	case http.MethodDelete:
	default:
		writeVLANError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}

	customerVLANMu.Lock()
	defer customerVLANMu.Unlock()

	if lock := customerVLANLock(customerVLANMeshState(), customerVLANMeshBusy()); lock != "" {
		writeVLANError(w, http.StatusConflict, "mesh_active", "VLANs cannot be changed while a Mesh is on or being set up or taken down")
		return
	}
	if customerVLANPending != nil {
		writeVLANError(w, http.StatusConflict, "change_pending", "another VLAN change is waiting to be confirmed")
		return
	}
	st, err := readCustomerVLANState()
	if err != nil {
		writeVLANError(w, http.StatusInternalServerError, "read_failed", err.Error())
		return
	}

	var lines []string
	switch r.Method {
	case http.MethodPost:
		if !customerVLANPortPresent() {
			writeVLANError(w, http.StatusConflict, "port_missing", "this router has no "+customerVLANPort)
			return
		}
		if err := validateCustomerVLAN(req, st, 0); err != nil {
			writeVLANError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		// Names cvlan<vid>* belong to this page (the interfaces page refuses
		// them), so anything left under them -- a firewall zone whose network a
		// pre-Mesh restore took away, say -- is cleared first.
		lines = append(customerVLANDeleteLines(st, req.VID), customerVLANSetLines(req)...)
	case http.MethodPut:
		if !customerVLANExists(st, vid) {
			writeVLANError(w, http.StatusNotFound, "not_found", errVLANNotFound.Error())
			return
		}
		if err := validateCustomerVLAN(req, st, vid); err != nil {
			writeVLANError(w, http.StatusBadRequest, "invalid", err.Error())
			return
		}
		lines = append(customerVLANDeleteLines(st, vid), customerVLANSetLines(req)...)
	case http.MethodDelete:
		if !customerVLANExists(st, vid) {
			writeVLANError(w, http.StatusNotFound, "not_found", errVLANNotFound.Error())
			return
		}
		lines = customerVLANDeleteLines(st, vid)
	}

	target := vid
	if r.Method == http.MethodPost {
		target = req.VID
	}
	id, err := customerVLANApply(target, lines, st.Live)
	if err != nil {
		writeVLANError(w, http.StatusInternalServerError, "apply_failed", err.Error())
		return
	}
	writeVLANJSON(w, http.StatusOK, map[string]any{
		"status":          "applied",
		"change_id":       id,
		"confirm_seconds": int(customerVLANConfirm / time.Second),
	})
}

func customerVLANExists(st customerVLANState, vid int) bool {
	for _, v := range customerVLANsFrom(st) {
		if v.VID == vid {
			return true
		}
	}
	return false
}

func customerVLANConfirmHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		writeVLANError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	if sessionUser(r) == "" {
		writeVLANError(w, http.StatusForbidden, "forbidden", "sign in to the portal to change VLANs")
		return
	}
	var req struct {
		ChangeID string `json:"change_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeVLANError(w, http.StatusBadRequest, "bad_json", "bad json")
		return
	}
	if !customerVLANConfirmChange(req.ChangeID) {
		writeVLANError(w, http.StatusGone, "not_pending", "this change is no longer waiting; it may have been undone")
		return
	}
	writeVLANJSON(w, http.StatusOK, map[string]string{"status": "confirmed"})
}
