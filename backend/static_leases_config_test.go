package main

import (
	"strings"
	"testing"
)

func TestStaticLeaseValidateAssignment(t *testing.T) {
	existing := []StaticLeaseConfig{
		{MAC: "AA:BB:CC:DD:EE:01", IPAddr: "10.10.18.20"},
	}

	tests := []struct {
		name    string
		mac     string
		ip      string
		wantErr string
	}{
		{
			name: "valid static address",
			mac:  "AA:BB:CC:DD:EE:02",
			ip:   "10.10.18.6",
		},
		{
			name:    "system reserved address",
			mac:     "AA:BB:CC:DD:EE:02",
			ip:      "10.10.18.2",
			wantErr: "must be between",
		},
		{
			name:    "dynamic pool address",
			mac:     "AA:BB:CC:DD:EE:02",
			ip:      "10.10.18.100",
			wantErr: "must be between",
		},
		{
			name:    "outside fixed static prefix",
			mac:     "AA:BB:CC:DD:EE:02",
			ip:      "10.10.19.20",
			wantErr: "must be between",
		},
		{
			name:    "duplicate address",
			mac:     "AA:BB:CC:DD:EE:02",
			ip:      "10.10.18.20",
			wantErr: "already assigned",
		},
		{
			name: "same device may update existing assignment",
			mac:  "aa-bb-cc-dd-ee-01",
			ip:   "10.10.18.20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := staticLeaseValidateAssignment(
				existing,
				tt.mac,
				tt.ip,
				"10.10.18.1",
				"255.255.254.0",
			)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("staticLeaseValidateAssignment() error = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("staticLeaseValidateAssignment() error = %v, want containing %q", err, tt.wantErr)
			}
			if got := staticLeaseErrorStatus(err); got != 400 {
				t.Fatalf("staticLeaseErrorStatus() = %d, want 400", got)
			}
		})
	}
}

func TestStaticLeaseManagedAntennaMACs(t *testing.T) {
	modules := []ManagedModule{
		{
			ModuleID:  "antenna:1",
			Type:      "ap",
			Port:      "lan1",
			PortIndex: 1,
			IP:        "10.10.18.2",
			MAC:       "AA:BB:CC:DD:EE:01",
		},
	}
	leasesRaw := strings.Join([]string{
		"0 aa:bb:cc:dd:ee:02 10.10.18.3 * *",
		"0 aa:bb:cc:dd:ee:03 10.10.18.100 client *",
		"0 aa:bb:cc:dd:ee:04 10.10.18.4 * *",
	}, "\n")
	portByMAC := map[string]string{
		"AA:BB:CC:DD:EE:02": "lan2",
		"AA:BB:CC:DD:EE:03": "lan3",
	}

	got := staticLeaseManagedAntennaMACs(modules, leasesRaw, portByMAC)
	for _, mac := range []string{"AA:BB:CC:DD:EE:01", "AA:BB:CC:DD:EE:02"} {
		if _, ok := got[mac]; !ok {
			t.Fatalf("managed MAC %s was not protected", mac)
		}
	}
	for _, mac := range []string{"AA:BB:CC:DD:EE:03", "AA:BB:CC:DD:EE:04"} {
		if _, ok := got[mac]; ok {
			t.Fatalf("ordinary or unproven MAC %s was unexpectedly protected", mac)
		}
	}
}
