package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestManagedAntennaPortIndexesSupportsDynamicCounts(t *testing.T) {
	tests := []struct {
		name    string
		linked  []int
		modules []ManagedModule
		want    []int
	}{
		{
			name:   "two non-contiguous linked antennas",
			linked: []int{1, 3},
			want:   []int{1, 3},
		},
		{
			name:   "proven module survives transient carrier read",
			linked: []int{2},
			modules: []ManagedModule{
				{ModuleID: "antenna:4", PortIndex: 4, Port: "lan4", IP: "10.10.18.2"},
			},
			want: []int{2, 4},
		},
		{
			name:   "duplicates and invalid ports are ignored",
			linked: []int{3, 1, 3, 0, 5},
			modules: []ManagedModule{
				{PortIndex: 1},
				{PortIndex: 7},
			},
			want: []int{1, 3},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := managedAntennaPortIndexes(tt.linked, tt.modules)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("managedAntennaPortIndexes() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestManagedAntennaCarrierStateOverridesStaleDiscovery(t *testing.T) {
	carrierByPort := map[int]bool{1: true, 2: false, 3: true, 4: false}
	modules := []ManagedModule{
		{PortIndex: 1},
		{PortIndex: 2}, // stale ARP/FDB entry on a carrier-down port
	}

	got := managedAntennaPortIndexesFromCarrier(carrierByPort, modules)
	if !reflect.DeepEqual(got, []int{1, 3}) {
		t.Fatalf("carrier-authoritative ports = %v, want [1 3]", got)
	}
}

func TestFirmwareTargetsForDynamicPorts(t *testing.T) {
	registry := []ManagedModule{
		{ModuleID: "antenna:1", PortIndex: 1, Port: "lan1", IP: "10.10.18.5"},
		{ModuleID: "antenna:2", PortIndex: 2, Port: "lan2", IP: "10.10.18.4"},
		{ModuleID: "antenna:3", PortIndex: 3, Port: "lan3", IP: "10.10.18.2"},
	}

	targets, missing, err := firmwareTargetsForPorts(registry, []int{1, 3})
	if err != nil {
		t.Fatalf("firmwareTargetsForPorts() error = %v", err)
	}
	if !reflect.DeepEqual(targets, []string{"10.10.18.5", "10.10.18.2"}) {
		t.Fatalf("targets = %v, want physical-port order", targets)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}

	targets, missing, err = firmwareTargetsForPorts(registry[:1], []int{1, 3})
	if err != nil {
		t.Fatalf("firmwareTargetsForPorts() error = %v", err)
	}
	if !reflect.DeepEqual(targets, []string{"10.10.18.5"}) || !reflect.DeepEqual(missing, []int{3}) {
		t.Fatalf("partial resolution targets=%v missing=%v", targets, missing)
	}
}

func TestFirmwareTargetsRejectsAmbiguousPhysicalIdentity(t *testing.T) {
	registry := []ManagedModule{
		{ModuleID: "antenna:2", PortIndex: 2, Port: "lan2", IP: "10.10.18.2"},
		{ModuleID: "antenna:2", PortIndex: 2, Port: "lan2", IP: "10.10.18.3"},
	}

	_, _, err := firmwareTargetsForPorts(registry, []int{2})
	if err == nil || !strings.Contains(err.Error(), "multiple Antenna addresses") {
		t.Fatalf("firmwareTargetsForPorts() error = %v, want ambiguous identity error", err)
	}
}
