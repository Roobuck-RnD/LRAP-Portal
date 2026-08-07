package main

import (
	"errors"
	"testing"
)

func TestIfaceNetmaskSupportsRequiredAddresses(t *testing.T) {
	tests := []struct {
		name string
		mask string
		want bool
	}{
		{name: "current slash 23", mask: "255.255.254.0", want: true},
		{name: "slash 24", mask: "255.255.255.0", want: true},
		{name: "smallest compatible slash 29", mask: "255.255.255.248", want: true},
		{name: "slash 30 excludes required addresses", mask: "255.255.255.252", want: false},
		{name: "slash 31 is not a usable LAN", mask: "255.255.255.254", want: false},
		{name: "non contiguous mask", mask: "255.0.255.0", want: false},
		{name: "invalid IPv4 mask", mask: "255.255.255.999", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ifaceNetmaskSupportsRequiredAddresses(tt.mask); got != tt.want {
				t.Fatalf("ifaceNetmaskSupportsRequiredAddresses(%q) = %v, want %v", tt.mask, got, tt.want)
			}
		})
	}
}

func TestIfaceEditRejectsUnsafeLANIdentityBeforeWritingConfig(t *testing.T) {
	t.Run("changed address", func(t *testing.T) {
		_, err := ifaceEdit("", InterfaceActionReq{
			Interface: "lan",
			IPAddr:    "10.10.18.99",
			Netmask:   "255.255.254.0",
		})
		if !errors.Is(err, errIfaceLANIPFixed) {
			t.Fatalf("ifaceEdit() error = %v, want %v", err, errIfaceLANIPFixed)
		}
	})

	t.Run("incompatible netmask", func(t *testing.T) {
		_, err := ifaceEdit("", InterfaceActionReq{
			Interface: "lan",
			IPAddr:    ACManagementIP,
			Netmask:   "255.255.255.252",
		})
		if !errors.Is(err, errIfaceLANNetmaskIncompatible) {
			t.Fatalf("ifaceEdit() error = %v, want %v", err, errIfaceLANNetmaskIncompatible)
		}
	})
}

func TestIfaceValidateLanDHCPPool(t *testing.T) {
	request := func(start, limit string) *InterfaceDHCPReq {
		return &InterfaceDHCPReq{
			Enabled:   true,
			Start:     start,
			Limit:     limit,
			LeaseTime: "12h",
		}
	}

	tests := []struct {
		name    string
		netmask string
		req     *InterfaceDHCPReq
		wantErr error
	}{
		{
			name:    "current client pool fits slash 23",
			netmask: "255.255.254.0",
			req:     request("100", "150"),
		},
		{
			name:    "start below protected boundary",
			netmask: "255.255.254.0",
			req:     request("99", "150"),
			wantErr: errIfaceDHCPStartTooLow,
		},
		{
			name:    "largest valid slash 24 pool",
			netmask: "255.255.255.0",
			req:     request("100", "155"),
		},
		{
			name:    "pool reaches slash 24 broadcast",
			netmask: "255.255.255.0",
			req:     request("100", "156"),
			wantErr: errIfaceDHCPPoolIncompatible,
		},
		{
			name:    "pool cannot fit small subnet",
			netmask: "255.255.255.248",
			req:     request("100", "1"),
			wantErr: errIfaceDHCPPoolIncompatible,
		},
		{
			name:    "broader subnet pool overlaps reserved addresses",
			netmask: "255.255.0.0",
			req:     request("4600", "20"),
			wantErr: errIfaceDHCPPoolIncompatible,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ifaceValidateLanDHCPPool(tt.netmask, tt.req)
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("ifaceValidateLanDHCPPool() error = %v, want nil", err)
				}
				return
			}
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("ifaceValidateLanDHCPPool() error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestIfaceSortAPManagementUsesAntennaIdentity(t *testing.T) {
	items := []APManagementInfo{
		{Name: "Antenna3", AntennaIndex: 3, IP: "10.10.18.2"},
		{Name: "Antenna1", AntennaIndex: 1, IP: "10.10.18.5"},
		{Name: "Antenna4", AntennaIndex: 4, IP: "10.10.18.3"},
		{Name: "Antenna2", AntennaIndex: 2, IP: "10.10.18.4"},
	}

	ifaceSortAPManagement(items)

	for index, item := range items {
		want := index + 1
		if item.AntennaIndex != want {
			t.Fatalf("items[%d].AntennaIndex = %d, want %d", index, item.AntennaIndex, want)
		}
	}
}

func TestIfaceDHCPSettingsFromValuesSeparatesClientSwitchFromProtectedPool(t *testing.T) {
	t.Run("legacy enabled configuration remains enabled", func(t *testing.T) {
		got := ifaceDHCPSettingsFromValues(map[string]any{
			"ignore":      "0",
			"dynamicdhcp": "1",
		})
		if !got.Enabled || !got.DynamicDHCP {
			t.Fatalf("legacy settings = enabled %v dynamic %v, want true true", got.Enabled, got.DynamicDHCP)
		}
	})

	t.Run("disabled client pool preserves requested dynamic preference", func(t *testing.T) {
		got := ifaceDHCPSettingsFromValues(map[string]any{
			"ignore":                  "0",
			"dynamicdhcp":             "0",
			"lrap_client_enabled":     "0",
			"lrap_client_dynamicdhcp": "1",
		})
		if got.Enabled || !got.DynamicDHCP {
			t.Fatalf("client settings = enabled %v dynamic %v, want false true", got.Enabled, got.DynamicDHCP)
		}
	})
}
