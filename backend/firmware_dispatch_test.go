package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeDispatchAntenna answers the dispatch commands without a shell. reply
// decides, per step, what the Root gets back; state is what is really on the
// Antenna, which the read-back commands report.
type fakeDispatchAntenna struct {
	armed, going bool
	armReply     func() (string, error)
	goReply      func() (string, error)
	armReadback  bool // whether the Antenna really armed despite armReply
	unreachable  bool
	calls        []string
}

type fakeDispatchNetwork struct {
	mu       sync.Mutex
	antennas map[string]*fakeDispatchAntenna
}

func (n *fakeDispatchNetwork) exec(ip string, cmd string, _ time.Duration) (string, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	a := n.antennas[ip]
	if a == nil || a.unreachable {
		return "", errors.New("timeout after 12s calling file.exec on " + ip)
	}
	switch {
	case strings.Contains(cmd, "( sh "):
		a.calls = append(a.calls, "arm")
		a.armed = true
		if a.armReply != nil {
			a.armed = a.armReadback
			return a.armReply()
		}
		return firmwareAPArmedMarker + "\n", nil
	case strings.Contains(cmd, "LRAP worker armed"):
		a.calls = append(a.calls, "arm-check")
		if a.armed {
			return firmwareAPArmedMarker + "\n", nil
		}
		return "", nil
	case strings.Contains(cmd, "rm -f "+shellQuote(firmwareAPArmedPath)+"; fi"):
		a.calls = append(a.calls, "cancel")
		if a.going {
			return firmwareAPTooLateMarker + "\n", nil
		}
		a.armed = false
		return firmwareAPCancelledMarker + "\n", nil
	case strings.Contains(cmd, "then echo"):
		a.calls = append(a.calls, "go")
		if !a.armed {
			return firmwareAPNotArmedMarker + "\n", nil
		}
		a.going = true
		if a.goReply != nil {
			return a.goReply()
		}
		return firmwareAPGoMarker + "\n", nil
	case strings.Contains(cmd, "LRAP worker go"):
		a.calls = append(a.calls, "go-check")
		if a.going {
			return firmwareAPGoMarker + "\n", nil
		}
		return "", nil
	}
	return "", errors.New("unexpected command: " + cmd)
}

func (n *fakeDispatchNetwork) write(ip string, _ string) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	a := n.antennas[ip]
	if a == nil || a.unreachable {
		return errors.New("write timed out on " + ip)
	}
	a.calls = append(a.calls, "write")
	return nil
}

func useFakeDispatchNetwork(t *testing.T, antennas map[string]*fakeDispatchAntenna) *fakeDispatchNetwork {
	t.Helper()
	n := &fakeDispatchNetwork{antennas: antennas}
	oldExec, oldWrite := firmwareRemoteExec, firmwareRemoteWriteWorker
	oldDelay, oldSpacing := firmwareDispatchConfirmDelay, firmwareDispatchSpacing
	firmwareRemoteExec, firmwareRemoteWriteWorker = n.exec, n.write
	firmwareDispatchConfirmDelay, firmwareDispatchSpacing = 0, 0
	t.Cleanup(func() {
		firmwareRemoteExec, firmwareRemoteWriteWorker = oldExec, oldWrite
		firmwareDispatchConfirmDelay, firmwareDispatchSpacing = oldDelay, oldSpacing
	})
	return n
}

func collectResults(out *[]FirmwareFlashResult) func(FirmwareFlashResult) {
	return func(r FirmwareFlashResult) { *out = append(*out, r) }
}

func resultsAt(results []FirmwareFlashResult, stage string) []FirmwareFlashResult {
	var out []FirmwareFlashResult
	for _, r := range results {
		if r.Stage == stage {
			out = append(out, r)
		}
	}
	return out
}

var dispatchTestIPs = []string{"172.31.255.5", "172.31.255.4", "172.31.255.3", "172.31.255.2"}

func TestDispatchArmsEveryAntennaBeforeTellingAnyToGo(t *testing.T) {
	antennas := map[string]*fakeDispatchAntenna{}
	for _, ip := range dispatchTestIPs {
		antennas[ip] = &fakeDispatchAntenna{}
	}
	useFakeDispatchNetwork(t, antennas)

	var order []string
	var results []FirmwareFlashResult
	launched, ok := firmwareDispatchAPUpgrades(dispatchTestIPs, "http://ac/fw", true, "sub", func(r FirmwareFlashResult) {
		order = append(order, r.Stage)
		results = append(results, r)
	})

	if !ok || len(launched) != len(dispatchTestIPs) {
		t.Fatalf("launched=%v ok=%v, want all four", launched, ok)
	}
	want := "arm arm arm arm dispatch dispatch dispatch dispatch"
	if got := strings.Join(order, " "); got != want {
		t.Fatalf("stages %q, want %q", got, want)
	}
	for _, r := range results {
		if !r.OK || r.Role != "sub" {
			t.Fatalf("result %+v, want ok sub", r)
		}
	}
}

// The TestVersion1.7 failure: the command ran but its reply timed out.
func TestDispatchTrustsTheAntennaOverALostArmReply(t *testing.T) {
	antennas := map[string]*fakeDispatchAntenna{}
	for _, ip := range dispatchTestIPs {
		antennas[ip] = &fakeDispatchAntenna{}
	}
	antennas["172.31.255.3"].armReply = func() (string, error) {
		return "", errors.New("timeout after 12s calling file.exec on 172.31.255.3")
	}
	antennas["172.31.255.3"].armReadback = true
	useFakeDispatchNetwork(t, antennas)

	var results []FirmwareFlashResult
	launched, ok := firmwareDispatchAPUpgrades(dispatchTestIPs, "http://ac/fw", true, "sub", collectResults(&results))

	if !ok || len(launched) != 4 {
		t.Fatalf("launched=%v ok=%v, want all four", launched, ok)
	}
	arm := resultsAt(results, "arm")
	if !arm[2].OK || !strings.Contains(arm[2].Detail, "reply was lost") {
		t.Fatalf("lost-reply arm result %+v, want ok with a note", arm[2])
	}
}

func TestDispatchFailureCancelsArmedAntennasAndTellsNoneToGo(t *testing.T) {
	antennas := map[string]*fakeDispatchAntenna{}
	for _, ip := range dispatchTestIPs {
		antennas[ip] = &fakeDispatchAntenna{}
	}
	antennas["172.31.255.3"].armReply = func() (string, error) {
		return "", errors.New("ubus bad result body=")
	}
	antennas["172.31.255.3"].armReadback = false
	useFakeDispatchNetwork(t, antennas)

	var results []FirmwareFlashResult
	launched, ok := firmwareDispatchAPUpgrades(dispatchTestIPs, "http://ac/fw", true, "sub", collectResults(&results))

	if ok || len(launched) != 0 {
		t.Fatalf("launched=%v ok=%v, want nothing launched", launched, ok)
	}
	for ip, a := range antennas {
		for _, c := range a.calls {
			if c == "go" {
				t.Fatalf("%s was told to go: %v", ip, a.calls)
			}
		}
	}
	if a := antennas["172.31.255.2"]; len(a.calls) != 0 {
		t.Fatalf("the Antenna after the failure was touched: %v", a.calls)
	}
	cancelled := resultsAt(results, "cancel")
	if len(cancelled) != 2 || cancelled[0].IP != "172.31.255.5" || cancelled[1].IP != "172.31.255.4" {
		t.Fatalf("cancel results %+v, want the two armed Antennas", cancelled)
	}
	for _, ip := range []string{"172.31.255.5", "172.31.255.4"} {
		if antennas[ip].armed {
			t.Fatalf("%s is still armed after cancel", ip)
		}
	}
	if arm := resultsAt(results, "arm"); len(arm) != 3 || arm[2].OK {
		t.Fatalf("arm results %+v, want two ok then the failure", arm)
	}
}

func TestDispatchCancelOfAnUnreachableAntennaReliesOnExpiry(t *testing.T) {
	antennas := map[string]*fakeDispatchAntenna{
		"172.31.255.5": {},
		"172.31.255.4": {armReply: func() (string, error) { return "", errors.New("connection reset by peer") }},
	}
	useFakeDispatchNetwork(t, antennas)
	// .5 arms, then drops off before the Root can cancel it.
	origWrite := firmwareRemoteWriteWorker
	firmwareRemoteWriteWorker = func(ip, script string) error {
		if ip == "172.31.255.4" {
			antennas["172.31.255.5"].unreachable = true
		}
		return origWrite(ip, script)
	}

	var results []FirmwareFlashResult
	_, ok := firmwareDispatchAPUpgrades([]string{"172.31.255.5", "172.31.255.4"}, "http://ac/fw", true, "sub", collectResults(&results))

	if ok {
		t.Fatal("dispatch reported ok")
	}
	cancelled := resultsAt(results, "cancel")
	if len(cancelled) != 1 || !cancelled[0].OK || !strings.Contains(cancelled[0].Detail, "gives up on its own") {
		t.Fatalf("cancel results %+v, want a note that the worker expires", cancelled)
	}
}

func TestDispatchGoRefusedLeavesThatAntennaOutOfTheLaunch(t *testing.T) {
	antennas := map[string]*fakeDispatchAntenna{}
	for _, ip := range dispatchTestIPs {
		antennas[ip] = &fakeDispatchAntenna{}
	}
	useFakeDispatchNetwork(t, antennas)
	// .4's worker expires between arm and go.
	origExec := firmwareRemoteExec
	firmwareRemoteExec = func(ip, cmd string, timeout time.Duration) (string, error) {
		if ip == "172.31.255.4" && strings.Contains(cmd, "then echo") {
			antennas[ip].armed = false
		}
		return origExec(ip, cmd, timeout)
	}

	var results []FirmwareFlashResult
	launched, ok := firmwareDispatchAPUpgrades(dispatchTestIPs, "http://ac/fw", true, "sub", collectResults(&results))

	if ok {
		t.Fatal("dispatch reported ok")
	}
	if strings.Join(launched, ",") != "172.31.255.5,172.31.255.3,172.31.255.2" {
		t.Fatalf("launched %v, want every Antenna but .4", launched)
	}
	for _, r := range resultsAt(results, "dispatch") {
		if r.IP == "172.31.255.4" && (r.OK || !strings.Contains(r.Error, "no longer armed")) {
			t.Fatalf(".4 dispatch result %+v, want refused", r)
		}
	}
}

func TestDispatchTrustsTheAntennaOverALostGoReply(t *testing.T) {
	antennas := map[string]*fakeDispatchAntenna{
		"172.31.255.5": {goReply: func() (string, error) { return "", errors.New("unexpected EOF") }},
	}
	useFakeDispatchNetwork(t, antennas)

	var results []FirmwareFlashResult
	launched, ok := firmwareDispatchAPUpgrades([]string{"172.31.255.5"}, "http://ac/fw", true, "sub", collectResults(&results))

	if !ok || len(launched) != 1 {
		t.Fatalf("launched=%v ok=%v, want the Antenna launched", launched, ok)
	}
	if d := resultsAt(results, "dispatch"); !strings.Contains(d[0].Detail, "reply was lost") {
		t.Fatalf("dispatch result %+v, want a note about the lost reply", d[0])
	}
}

func TestDispatchStatesForIPsKeepsOnlyLaunched(t *testing.T) {
	states := []firmwareAPUpgradeState{{IP: "a", MAC: "1"}, {IP: "b", MAC: "2"}, {IP: "c", MAC: "3"}}
	got := firmwareStatesForIPs(states, []string{"c", "a"})
	if len(got) != 2 || got[0].IP != "a" || got[1].IP != "c" {
		t.Fatalf("got %+v", got)
	}
}

func TestWorkerScriptNeverFactoryResetsWhenKeepingSettings(t *testing.T) {
	script := firmwareBuildAPUpgradeWorkerScript("http://ac/fw", true, "tok")
	if !strings.Contains(script, `KEEP_SETTINGS='1'`) {
		t.Fatalf("keep-settings worker does not carry KEEP_SETTINGS=1:\n%s", script)
	}
	if !strings.Contains(script, "TOKEN='tok'") {
		t.Fatalf("worker does not carry its token:\n%s", script)
	}
}

// ---- The real scripts, run by a local shell standing in for the Antenna ----

type shellDispatchAntenna struct {
	dir string
}

func (a shellDispatchAntenna) rewrite(s string) string {
	s = strings.ReplaceAll(s, "/tmp/lrap-", a.dir+"/lrap-")
	s = strings.ReplaceAll(s, `wget -O "$AP_FIRMWARE" "$DOWNLOAD_URL"`, `echo image > "$AP_FIRMWARE"`)
	s = strings.ReplaceAll(s, "/sbin/sysupgrade", "echo FAKE-SYSUPGRADE")
	return s
}

func (a shellDispatchAntenna) path(name string) string { return a.dir + "/" + name }

func (a shellDispatchAntenna) log() string {
	b, _ := os.ReadFile(a.path("lrap-firmware-upgrade.log"))
	return string(b)
}

func (a shellDispatchAntenna) waitForLog(t *testing.T, want string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if strings.Contains(a.log(), want) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("worker log never showed %q; log:\n%s", want, a.log())
}

func useShellDispatchAntenna(t *testing.T, window int) shellDispatchAntenna {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh to stand in for the Antenna")
	}
	a := shellDispatchAntenna{dir: filepath.ToSlash(t.TempDir())}
	oldExec, oldWrite := firmwareRemoteExec, firmwareRemoteWriteWorker
	oldWindow, oldGrace, oldDelay := firmwareAPArmWindowSeconds, firmwareAPGoGraceSeconds, firmwareDispatchConfirmDelay
	firmwareRemoteExec = func(_ string, cmd string, _ time.Duration) (string, error) {
		out, err := exec.Command("sh", "-c", a.rewrite(cmd)).Output()
		return string(out), err
	}
	firmwareRemoteWriteWorker = func(_ string, script string) error {
		return os.WriteFile(a.path("lrap-firmware-worker.sh"), []byte(a.rewrite(script)), 0700)
	}
	firmwareAPArmWindowSeconds, firmwareAPGoGraceSeconds, firmwareDispatchConfirmDelay = window, 0, 200*time.Millisecond
	t.Cleanup(func() {
		// Stop a worker still waiting so the temp dir can go.
		_ = os.Remove(a.path("lrap-firmware-armed"))
		time.Sleep(1500 * time.Millisecond)
		firmwareRemoteExec, firmwareRemoteWriteWorker = oldExec, oldWrite
		firmwareAPArmWindowSeconds, firmwareAPGoGraceSeconds, firmwareDispatchConfirmDelay = oldWindow, oldGrace, oldDelay
	})
	return a
}

func TestShellWorkerFlashesOnlyAfterGo(t *testing.T) {
	a := useShellDispatchAntenna(t, 30)
	token := "t-go"

	if _, err := firmwareArmAPUpgrade("ip", "http://ac/fw", true, token); err != nil {
		t.Fatalf("arm: %v", err)
	}
	a.waitForLog(t, "LRAP worker armed", 5*time.Second)
	if !firmwareConfirmOnAP("ip", firmwareAPArmedCheckCommand(token), firmwareAPArmedMarker) {
		t.Fatalf("armed worker not confirmed; log:\n%s", a.log())
	}
	time.Sleep(1500 * time.Millisecond)
	if strings.Contains(a.log(), "FAKE-SYSUPGRADE") {
		t.Fatalf("worker flashed before go; log:\n%s", a.log())
	}

	if _, err := firmwareGoAPUpgrade("ip", token); err != nil {
		t.Fatalf("go: %v", err)
	}
	a.waitForLog(t, "FAKE-SYSUPGRADE", 5*time.Second)
	if !firmwareConfirmOnAP("ip", firmwareAPGoCheckCommand(token), firmwareAPGoMarker) {
		t.Fatal("go not confirmed after the worker went")
	}
}

func TestShellCancelledWorkerNeverFlashes(t *testing.T) {
	a := useShellDispatchAntenna(t, 30)
	token := "t-cancel"

	if _, err := firmwareArmAPUpgrade("ip", "http://ac/fw", true, token); err != nil {
		t.Fatalf("arm: %v", err)
	}
	a.waitForLog(t, "LRAP worker armed", 5*time.Second)

	if r := firmwareCancelAPUpgrade("ip", token); !r.OK || !strings.Contains(r.Detail, "cancelled") {
		t.Fatalf("cancel result %+v", r)
	}
	a.waitForLog(t, "LRAP worker cancelled", 5*time.Second)
	if _, err := firmwareGoAPUpgrade("ip", token); err == nil || !strings.Contains(err.Error(), "no longer armed") {
		t.Fatalf("go after cancel: %v, want refused", err)
	}
	time.Sleep(1500 * time.Millisecond)
	if strings.Contains(a.log(), "FAKE-SYSUPGRADE") {
		t.Fatalf("cancelled worker flashed; log:\n%s", a.log())
	}
}

func TestShellWorkerExpiresWithoutGo(t *testing.T) {
	a := useShellDispatchAntenna(t, 1)
	token := "t-expire"

	if _, err := firmwareArmAPUpgrade("ip", "http://ac/fw", true, token); err != nil {
		t.Fatalf("arm: %v", err)
	}
	a.waitForLog(t, "LRAP worker expired without go", 8*time.Second)
	if _, err := os.Stat(a.path("lrap-firmware-armed")); !os.IsNotExist(err) {
		t.Fatalf("armed file left behind after expiry: %v", err)
	}
	if firmwareConfirmOnAP("ip", firmwareAPArmedCheckCommand(token), firmwareAPArmedMarker) {
		t.Fatal("an expired worker was confirmed as armed")
	}
	if strings.Contains(a.log(), "FAKE-SYSUPGRADE") {
		t.Fatalf("expired worker flashed; log:\n%s", a.log())
	}
}
