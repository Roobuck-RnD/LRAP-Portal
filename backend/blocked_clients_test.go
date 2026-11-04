package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// useFakeBlockRouter keeps the firewall config as uci would, applies the
// handler's batches to it, and puts the include in a temporary directory.
func useFakeBlockRouter(t *testing.T) *fakeRouter {
	t.Helper()
	f := newFakeRouter()
	oldShell, oldProtected, oldRequester, oldPath := blockedClientsShell, blockedClientsProtected, blockedClientsRequesterMAC, blockedClientsScriptPath
	blockedClientsShell = func(script string) (string, error) {
		if strings.Contains(script, "uci -t "+blockedClientStage+" batch") {
			f.mu.Lock()
			f.scripts = append(f.scripts, script)
			body := script[strings.Index(script, "\n")+1 : strings.Index(script, "\nLRAP_BLOCK_EOF")]
			f.batch(strings.Split(body, "\n"))
			f.mu.Unlock()
			return "", nil
		}
		return f.shell(script)
	}
	blockedClientsProtected = func() map[string]bool { return map[string]bool{"F0:A8:82:00:00:01": true} }
	blockedClientsRequesterMAC = func(*http.Request) string { return "AA:BB:CC:00:00:99" }
	blockedClientsScriptPath = filepath.Join(t.TempDir(), "etc", "lrap-blocked-clients.fw")
	t.Cleanup(func() {
		blockedClientsShell, blockedClientsProtected, blockedClientsRequesterMAC, blockedClientsScriptPath = oldShell, oldProtected, oldRequester, oldPath
	})
	return f
}

func blockCall(t *testing.T, method, path, body string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	blockedClientsHandler(w, httptest.NewRequest(method, path, strings.NewReader(body)))
	out := map[string]any{}
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func readInclude(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(blockedClientsScriptPath)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Blocking writes one rule and the include, both from the same list, and
// reloads the firewall; allowing again takes both away.
func TestBlockedClientBlockAndAllow(t *testing.T) {
	f := useFakeBlockRouter(t)

	code, out := blockCall(t, http.MethodPost, "/api/net/blocked-clients", `{"mac":"aa-bb-cc-00-00-01","label":"Kid's tablet"}`)
	if code != http.StatusOK || out["mac"] != "AA:BB:CC:00:00:01" || out["label"] != "Kids tablet" {
		t.Fatalf("block: %d %v", code, out)
	}
	rule := f.configs["firewall"]["lrap_block_aabbcc000001"]
	if rule.Type != "rule" || rule.Options["src_mac"] != "AA:BB:CC:00:00:01" || rule.Options["src"] != "*" ||
		rule.Options["dest"] != "*" || rule.Options["target"] != "REJECT" || rule.Options["name"] != "Blocked: Kids tablet" {
		t.Fatalf("rule %+v", rule)
	}
	inc := f.configs["firewall"][blockedClientInclude]
	if inc.Type != "include" || inc.Options["path"] != blockedClientsScriptPath || inc.Options["reload"] != "1" {
		t.Fatalf("include %+v", inc)
	}
	script := readInclude(t)
	for _, tool := range []string{"iptables", "ip6tables"} {
		if !strings.Contains(script, tool+" -w -I forwarding_rule -m mac --mac-source AA:BB:CC:00:00:01 ") {
			t.Fatalf("include lacks %s:\n%s", tool, script)
		}
	}
	if !f.ran("/etc/init.d/firewall reload") || !f.ran("sh "+blockedClientsScriptPath) {
		t.Fatal("the firewall was not reloaded, or the include not run")
	}
	if strings.Index(script, "-D ") > strings.Index(script, "-I forwarding_rule") {
		t.Fatalf("the include does not take out its old rules first:\n%s", script)
	}

	code, out = blockCall(t, http.MethodGet, "/api/net/blocked-clients", "")
	clients, _ := out["clients"].([]any)
	if code != http.StatusOK || len(clients) != 1 {
		t.Fatalf("list: %d %v", code, out)
	}

	code, out = blockCall(t, http.MethodDelete, "/api/net/blocked-clients?mac=AA:BB:CC:00:00:01", "")
	if code != http.StatusOK {
		t.Fatalf("allow: %d %v", code, out)
	}
	if _, ok := f.configs["firewall"]["lrap_block_aabbcc000001"]; ok {
		t.Fatal("rule left behind")
	}
	if _, ok := f.configs["firewall"][blockedClientInclude]; ok {
		t.Fatal("include left behind with nothing blocked")
	}
	// The include is gone from the config, so fw3 will not run it again; the
	// handler runs it once more, and all it does now is take the old rules out.
	if strings.Contains(readInclude(t), "--mac-source") || !strings.Contains(readInclude(t), "--comment lrap-blocked") {
		t.Fatalf("include after the last device is allowed:\n%s", readInclude(t))
	}
	if n := strings.Count(strings.Join(f.scripts, "\n"), "sh "+blockedClientsScriptPath); n != 2 {
		t.Fatalf("include run %d times for two changes", n)
	}
}

// The include stays while anything is still blocked, and nothing else in the
// firewall changes.
func TestBlockedClientTouchesOnlyItsOwn(t *testing.T) {
	f := useFakeBlockRouter(t)
	before := copyConfig(f.configs["firewall"])

	blockCall(t, http.MethodPost, "/api/net/blocked-clients", `{"mac":"AA:BB:CC:00:00:01"}`)
	blockCall(t, http.MethodPost, "/api/net/blocked-clients", `{"mac":"AA:BB:CC:00:00:02"}`)
	if f.configs["firewall"]["lrap_block_aabbcc000001"].Options["name"] != "Blocked: AA:BB:CC:00:00:01" {
		t.Fatal("an unlabelled device is not named by its MAC")
	}
	blockCall(t, http.MethodDelete, "/api/net/blocked-clients?mac=AA:BB:CC:00:00:01", "")
	if _, ok := f.configs["firewall"][blockedClientInclude]; !ok {
		t.Fatal("include removed while a device is still blocked")
	}
	if !strings.Contains(readInclude(t), "AA:BB:CC:00:00:02") || strings.Contains(readInclude(t), "AA:BB:CC:00:00:01") {
		t.Fatalf("include:\n%s", readInclude(t))
	}
	blockCall(t, http.MethodDelete, "/api/net/blocked-clients?mac=AA:BB:CC:00:00:02", "")

	after := f.configs["firewall"]
	if len(after) != len(before) {
		t.Fatalf("firewall has %d sections, had %d", len(after), len(before))
	}
	for name, s := range before {
		if got := after[name]; got.Type != s.Type || len(got.Options) != len(s.Options) {
			t.Fatalf("%s changed", name)
		}
	}
}

// Addresses that are not one device, the chassis's own, and the device the
// request comes from are refused, and nothing is written.
func TestBlockedClientRefusals(t *testing.T) {
	f := useFakeBlockRouter(t)
	for body, want := range map[string]string{
		`{"mac":"nonsense"}`:          "invalid_mac",
		`{"mac":"01:00:5E:00:00:01"}`: "invalid_mac",
		`{"mac":"FF:FF:FF:FF:FF:FF"}`: "invalid_mac",
		`{"mac":"f0:a8:82:00:00:01"}`: "chassis_device",
		`{"mac":"AA:BB:CC:00:00:99"}`: "own_device",
	} {
		code, out := blockCall(t, http.MethodPost, "/api/net/blocked-clients", body)
		if code < 400 || out["code"] != want {
			t.Errorf("%s: %d %v, want %s", body, code, out, want)
		}
	}
	if f.ran("LRAP_BLOCK_EOF") || f.ran("firewall reload") {
		t.Fatal("a refused request changed the firewall")
	}
	if code, out := blockCall(t, http.MethodDelete, "/api/net/blocked-clients?mac=AA:BB:CC:00:00:05", ""); code != http.StatusNotFound {
		t.Fatalf("allowing a device that is not blocked: %d %v", code, out)
	}
}

func TestBlockedClientLimit(t *testing.T) {
	f := useFakeBlockRouter(t)
	for i := 0; i < blockedClientMax; i++ {
		c := BlockedClient{MAC: strings.ToUpper("AA:BB:CC:DD:" + hex2(i/256) + ":" + hex2(i%256))}
		f.batch(blockedClientSetLines(c))
	}
	code, out := blockCall(t, http.MethodPost, "/api/net/blocked-clients", `{"mac":"AA:BB:CC:EE:00:01"}`)
	if code != http.StatusConflict || out["code"] != "too_many" {
		t.Fatalf("over the limit: %d %v", code, out)
	}
	// One already on the list may still be renamed.
	code, out = blockCall(t, http.MethodPost, "/api/net/blocked-clients", `{"mac":"AA:BB:CC:DD:00:00","label":"Printer"}`)
	if code != http.StatusOK {
		t.Fatalf("renaming at the limit: %d %v", code, out)
	}
}

func hex2(i int) string {
	const digits = "0123456789ABCDEF"
	return string([]byte{digits[i/16], digits[i%16]})
}

// After a reboot /var is empty: startup writes the include again from the
// rules, without touching the firewall.
func TestBlockedClientIncludeRewrittenAtStartup(t *testing.T) {
	f := useFakeBlockRouter(t)
	f.batch(blockedClientSetLines(BlockedClient{MAC: "AA:BB:CC:00:00:07"}))

	writeBlockedClientsScriptAtStartup()
	if !strings.Contains(readInclude(t), "--mac-source AA:BB:CC:00:00:07") {
		t.Fatalf("include:\n%s", readInclude(t))
	}
	if f.ran("firewall reload") {
		t.Fatal("startup reloaded the firewall")
	}
}

func TestBlockedClientLabel(t *testing.T) {
	for in, want := range map[string]string{
		"Kid's iPad":                "Kids iPad",
		"  spaces  ":                "spaces",
		"a'; rm -rf /":              "a rm -rf",
		strings.Repeat("x", 40):     strings.Repeat("x", 32),
		"Galaxy-S24_Ultra (2).home": "Galaxy-S24_Ultra 2.home",
	} {
		if got := blockedClientLabel(in); got != want {
			t.Errorf("label(%q) = %q, want %q", in, got, want)
		}
	}
}
