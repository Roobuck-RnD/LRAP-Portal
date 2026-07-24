package main

import (
	"net"
	"reflect"
	"strings"
	"testing"
)

func TestSRValidateRoute(t *testing.T) {
	tests := []struct {
		name    string
		route   StaticRouteConfig
		wantErr string
	}{
		{
			name: "valid network route",
			route: StaticRouteConfig{
				Interface: "lan",
				Target:    "192.168.50.0",
				Netmask:   "255.255.255.0",
				Gateway:   "10.10.18.254",
				Metric:    "10",
			},
		},
		{
			name: "valid default route",
			route: StaticRouteConfig{
				Interface: "wan",
				Target:    "0.0.0.0",
				Netmask:   "0.0.0.0",
				Gateway:   "192.0.2.1",
				Metric:    "0",
			},
		},
		{
			name: "non-contiguous netmask",
			route: StaticRouteConfig{
				Interface: "lan",
				Target:    "192.168.50.0",
				Netmask:   "255.0.255.0",
			},
			wantErr: "contiguous",
		},
		{
			name: "target is not network address",
			route: StaticRouteConfig{
				Interface: "lan",
				Target:    "192.168.50.12",
				Netmask:   "255.255.255.0",
			},
			wantErr: "network address",
		},
		{
			name: "invalid interface",
			route: StaticRouteConfig{
				Interface: "lan;reboot",
				Target:    "192.168.50.0",
				Netmask:   "255.255.255.0",
			},
			wantErr: "invalid interface",
		},
		{
			name: "invalid metric",
			route: StaticRouteConfig{
				Interface: "lan",
				Target:    "192.168.50.0",
				Netmask:   "255.255.255.0",
				Metric:    "-1",
			},
			wantErr: "metric",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := srValidateRoute(tt.route)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("srValidateRoute() unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("srValidateRoute() error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestSRLiveRouteArgs(t *testing.T) {
	route := StaticRouteConfig{
		Interface: "lan",
		Target:    "192.168.50.0",
		Netmask:   "255.255.255.0",
		Gateway:   "10.10.18.254",
		Metric:    "20",
	}

	got, err := srLiveRouteArgs("replace", route, "br-lan")
	if err != nil {
		t.Fatalf("srLiveRouteArgs() error: %v", err)
	}

	want := []string{
		"-4", "route", "replace", "192.168.50.0/24",
		"via", "10.10.18.254", "dev", "br-lan",
		"metric", "20", "table", "main",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("srLiveRouteArgs() = %#v, want %#v", got, want)
	}
}

func TestSRRouteConflictsWithLocalNetwork(t *testing.T) {
	tests := []struct {
		target  string
		mask    string
		want    bool
		message string
	}{
		{"10.10.18.0", "255.255.255.0", true, "more-specific local route"},
		{"10.10.18.1", "255.255.255.255", true, "local host route"},
		{"10.10.16.0", "255.255.252.0", false, "less-specific local route"},
		{"0.0.0.0", "0.0.0.0", false, "default route"},
		{"192.168.50.0", "255.255.255.0", false, "unrelated route"},
	}

	for _, tt := range tests {
		_, routeNetwork, err := srRoutePrefix(tt.target, tt.mask)
		if err != nil {
			t.Fatalf("%s: srRoutePrefix() error: %v", tt.message, err)
		}
		if got := srRouteConflictsWithLocalNetwork(routeNetwork, 23); got != tt.want {
			t.Errorf("%s: conflict = %v, want %v", tt.message, got, tt.want)
		}
	}
}

func TestSRRoutePrefixReturnsCanonicalIPv4Network(t *testing.T) {
	prefix, network, err := srRoutePrefix("203.0.113.0", "255.255.255.128")
	if err != nil {
		t.Fatalf("srRoutePrefix() error: %v", err)
	}
	if prefix != "203.0.113.0/25" {
		t.Fatalf("prefix = %q, want %q", prefix, "203.0.113.0/25")
	}
	if !network.Contains(net.ParseIP("203.0.113.127")) {
		t.Fatal("network does not contain its last address")
	}
}
