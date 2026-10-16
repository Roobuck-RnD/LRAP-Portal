package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Factory WiFi: a chassis out of the box, or out of a factory reset, offers
// one network on the main module and every antenna module alike --
// "Roobuck-" and the last six hex digits of the main module's ra0 MAC (the
// same digits as its serial number), "_5G" added on 5 GHz, password 12345678.
//
// It is applied once, by the portal, when the WiFi is still the firmware's own
// and nothing says it was ever set. A marker in /etc/wireless records that it
// was: /etc/wireless is in the firmware's sysupgrade keep list, so the marker
// and the WiFi profiles it guards survive an upgrade that keeps settings
// together, and a factory reset (or an upgrade that does not keep settings)
// clears them together. So a customer's own names and password survive
// reboots and upgrades, and a reset brings the factory WiFi back.
//
// A unit already configured before this existed -- its WiFi no longer the
// firmware's -- is left as it is and only marked.

const (
	factoryWiFiPassword   = "12345678"
	factoryWiFiPrefix     = "Roobuck-"
	factoryWiFiAlreadySet = "WiFi already set on this unit"
)

var (
	factoryWiFiMarker   = "/etc/wireless/lrap-wifi-defaults.json"
	factoryWiFiROM      = "/rom"
	factoryWiFiProfiles = []struct {
		Path string
		Band mtkBand
	}{{Mtk24GPath, mtkBand24G}, {Mtk5GPath, mtkBand5G}}
	factoryWiFiMAC = func() (string, error) {
		raw, err := os.ReadFile("/sys/class/net/ra0/address")
		return strings.TrimSpace(string(raw)), err
	}
	// How long to let the antenna modules come up and settle onto the
	// management network before writing to them: at least factoryWiFiMinWait
	// after start, then until the set of reachable modules has not changed for
	// factoryWiFiSteady, but never longer than factoryWiFiMaxWait. An antenna
	// still untagged is moved across by the adoption scan, which runs once a
	// minute, so two can arrive more than a scan apart; the steady window
	// spans two and a half scans.
	factoryWiFiMinWait = 3 * time.Minute
	factoryWiFiSteady  = 150 * time.Second
	factoryWiFiMaxWait = 10 * time.Minute
	factoryWiFiPoll    = 15 * time.Second
	factoryWiFiSleep   = time.Sleep
	factoryWiFiNow     = time.Now
	factoryWiFiRead    = getMtkWifiLogic
	factoryWiFiSave    = func(req MtkWifiInfo) error {
		_, plan, err := setMtkWifiLogic(req)
		if err != nil {
			return err
		}
		scheduleMtkWifiApply(plan)
		return nil
	}
)

type factoryWiFiRecord struct {
	Result     string   `json:"result"` // "applied" or "kept"
	SSID24     string   `json:"ssid_2g,omitempty"`
	SSID5      string   `json:"ssid_5g,omitempty"`
	Modules    []string `json:"modules,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	RecordedAt string   `json:"recorded_at"`
}

// factoryWiFiNames is the factory SSID pair for a ra0 MAC.
func factoryWiFiNames(mac string) (string, string, error) {
	hex := strings.ToUpper(strings.NewReplacer(":", "", "-", "").Replace(strings.TrimSpace(mac)))
	if len(hex) != 12 || strings.Trim(hex, "0123456789ABCDEF") != "" {
		return "", "", fmt.Errorf("not a MAC address: %q", mac)
	}
	base := factoryWiFiPrefix + hex[6:]
	return base, base + "_5G", nil
}

// factoryWiFiPending says whether the factory WiFi is still to be applied: no
// marker, and both main-module profiles name the same network as the
// firmware's own copies in /rom. Anything it cannot read counts as "not
// pending", so a doubt never overwrites someone's WiFi.
func factoryWiFiPending() (bool, string) {
	if _, err := os.Stat(factoryWiFiMarker); err == nil {
		return false, "already recorded"
	}
	for _, p := range factoryWiFiProfiles {
		live, err := os.ReadFile(p.Path)
		if err != nil {
			return false, "cannot read " + p.Path
		}
		rom, err := os.ReadFile(filepath.Join(factoryWiFiROM, p.Path))
		if err != nil {
			return false, "no firmware copy of " + p.Path
		}
		if parseMtkDatContent(string(live), p.Band).SSID != parseMtkDatContent(string(rom), p.Band).SSID {
			return false, factoryWiFiAlreadySet
		}
	}
	return true, ""
}

func recordFactoryWiFi(r factoryWiFiRecord) {
	r.RecordedAt = factoryWiFiNow().UTC().Format(time.RFC3339)
	raw, _ := json.MarshalIndent(r, "", "  ")
	tmp := factoryWiFiMarker + ".tmp"
	if err := os.WriteFile(tmp, raw, 0644); err != nil {
		log.Printf("[WiFi defaults] cannot record: %v", err)
		return
	}
	if err := os.Rename(tmp, factoryWiFiMarker); err != nil {
		log.Printf("[WiFi defaults] cannot record: %v", err)
	}
}

// reachableModules is what a save would write to now: the main module and
// every antenna that answered with its identity known.
func reachableModules(info MtkWifiInfo) []MtkWifiModuleRadio {
	var out []MtkWifiModuleRadio
	for _, m := range info.Modules {
		if m.Type == "main" || (m.Online && m.IdentityReady && m.ModuleID != "") {
			out = append(out, m)
		}
	}
	return out
}

func moduleKey(mods []MtkWifiModuleRadio) string {
	ids := make([]string, 0, len(mods))
	for _, m := range mods {
		ids = append(ids, m.ModuleID)
	}
	return strings.Join(ids, ",")
}

// applyFactoryWiFiDefaults runs once at startup, in the background.
func applyFactoryWiFiDefaults() {
	pending, why := factoryWiFiPending()
	if !pending {
		if why == factoryWiFiAlreadySet {
			recordFactoryWiFi(factoryWiFiRecord{Result: "kept", Reason: why})
		}
		return
	}
	mac, err := factoryWiFiMAC()
	if err != nil {
		log.Printf("[WiFi defaults] no ra0 address, not applied: %v", err)
		return
	}
	ssid24, ssid5, err := factoryWiFiNames(mac)
	if err != nil {
		log.Printf("[WiFi defaults] not applied: %v", err)
		return
	}

	start := factoryWiFiNow()
	var info MtkWifiInfo
	lastKey, lastChange := "", start
	for {
		if easyMeshOwnsWiFi() {
			log.Printf("[WiFi defaults] a Mesh owns the WiFi; not applied")
			return
		}
		current, err := factoryWiFiRead()
		if err == nil {
			info = current
			if key := moduleKey(reachableModules(info)); key != lastKey {
				lastKey, lastChange = key, factoryWiFiNow()
			}
		}
		now := factoryWiFiNow()
		if now.Sub(start) >= factoryWiFiMaxWait ||
			(now.Sub(start) >= factoryWiFiMinWait && now.Sub(lastChange) >= factoryWiFiSteady && lastKey != "") {
			break
		}
		factoryWiFiSleep(factoryWiFiPoll)
	}
	mods := reachableModules(info)
	if len(mods) == 0 {
		log.Printf("[WiFi defaults] the WiFi configuration could not be read; not applied")
		return
	}
	// Someone may have saved their own WiFi while this waited.
	if pending, _ := factoryWiFiPending(); !pending {
		recordFactoryWiFi(factoryWiFiRecord{Result: "kept", Reason: "WiFi set while waiting"})
		return
	}

	req := info
	req.SSID24, req.Pass24 = ssid24, factoryWiFiPassword
	req.SSID5, req.Pass5 = ssid5, factoryWiFiPassword
	req.Modules = mods
	if err := factoryWiFiSave(req); err != nil {
		// No marker: the next start tries again.
		log.Printf("[WiFi defaults] not applied: %v", err)
		return
	}
	names := make([]string, 0, len(mods))
	for _, m := range mods {
		names = append(names, nonEmpty(m.Name, m.ModuleID))
	}
	recordFactoryWiFi(factoryWiFiRecord{Result: "applied", SSID24: ssid24, SSID5: ssid5, Modules: names})
	log.Printf("[WiFi defaults] %s / %s on %d module(s)", ssid24, ssid5, len(mods))
}
