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
