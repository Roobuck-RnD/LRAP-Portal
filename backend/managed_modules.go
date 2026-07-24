package main

import (
	"sort"
	"strconv"
	"strings"
)

const (
	managedMainModuleID  = "main"
	managedAntennaPrefix = "antenna:"
)

// ManagedModule separates stable product identity from the address currently
// used to contact a device. For APs, ModuleID follows the physical AC LAN port
// (antenna:1..antenna:4); IP and MAC are transient discovery data.
type ManagedModule struct {
	ModuleID  string
	Type      string
	Port      string
	PortIndex int
	IP        string
	MAC       string
}

func managedAntennaModuleID(portIndex int) string {
	if portIndex < 1 || portIndex > len(APManagementIPs()) {
		return ""
	}
	return managedAntennaPrefix + strconv.Itoa(portIndex)
}

func managedAntennaPortIndex(moduleID string) (int, bool) {
	moduleID = strings.ToLower(strings.TrimSpace(moduleID))
	if !strings.HasPrefix(moduleID, managedAntennaPrefix) {
		return 0, false
	}

	portIndex, err := strconv.Atoi(strings.TrimPrefix(moduleID, managedAntennaPrefix))
	if err != nil || portIndex < 1 || portIndex > len(APManagementIPs()) {
		return 0, false
	}
	return portIndex, true
}

// discoverManagedAPs builds a point-in-time AP registry. When refreshARP is
// true, the fixed management pool is probed first so ARP/FDB mappings are fresh.
// APs whose physical port cannot be proven are deliberately omitted: applying
// another antenna's persisted state would be worse than retrying next cycle.
func discoverManagedAPs(refreshARP bool) []ManagedModule {
	ips := APManagementIPs()
	if refreshARP {
		probeStaticIPs(ips)
	}

	arpByIP := parseARPByIP()
	portByMAC := listLanEdgePortsByMAC()
	modules := make([]ManagedModule, 0, len(ips))

	for _, ip := range ips {
		mac := strings.ToUpper(strings.TrimSpace(arpByIP[ip]))
		if mac == "" {
			continue
		}

		port := strings.TrimSpace(portByMAC[mac])
		portIndex := lanPortNumber(port)
		moduleID := managedAntennaModuleID(portIndex)
		if moduleID == "" {
			continue
		}

		modules = append(modules, ManagedModule{
			ModuleID:  moduleID,
			Type:      "ap",
			Port:      port,
			PortIndex: portIndex,
			IP:        ip,
			MAC:       mac,
		})
	}

	sort.Slice(modules, func(i, j int) bool {
		if modules[i].PortIndex != modules[j].PortIndex {
			return modules[i].PortIndex < modules[j].PortIndex
		}
		return modules[i].IP < modules[j].IP
	})

	return modules
}

func managedAPByModuleID(modules []ManagedModule, moduleID string) (ManagedModule, bool) {
	moduleID = strings.ToLower(strings.TrimSpace(moduleID))
	for _, module := range modules {
		if module.ModuleID == moduleID {
			return module, true
		}
	}
	return ManagedModule{}, false
}

func managedAPByIP(modules []ManagedModule, ip string) (ManagedModule, bool) {
	ip = strings.TrimSpace(ip)
	for _, module := range modules {
		if module.IP == ip {
			return module, true
		}
	}
	return ManagedModule{}, false
}

func managedAPPortIndexByIP(modules []ManagedModule) map[string]int {
	out := make(map[string]int, len(modules))
	for _, module := range modules {
		out[module.IP] = module.PortIndex
	}
	return out
}

// managedModuleDisplaySortKey is the single display-order rule used by status
// APIs. Stable identity wins, physical port is the legacy fallback, and IP is
// deliberately last because it can move between antennas after reboot.
func managedModuleDisplaySortKey(moduleID, moduleType, port, ip string) int {
	moduleID = strings.ToLower(strings.TrimSpace(moduleID))
	moduleType = strings.ToLower(strings.TrimSpace(moduleType))
	port = strings.ToLower(strings.TrimSpace(port))

	if moduleID == managedMainModuleID || moduleType == "main" || port == "br-lan" {
		return 0
	}
	if portIndex, ok := managedAntennaPortIndex(moduleID); ok {
		return portIndex
	}
	if portIndex := lanPortNumber(port); portIndex > 0 {
		return portIndex
	}

	parts := strings.Split(strings.TrimSpace(ip), ".")
	if len(parts) == 4 {
		if last, err := strconv.Atoi(parts[3]); err == nil {
			return 1000 + last
		}
	}
	return 2000
}
