package config

import (
	"os"
	"path/filepath"
	"testing"
)

// The default is deliberately "advertise as available for pairing": with it off, a device lists
// the built-in simulated device and hides the energy manager, which reads as a broken tool.
// Advertising and accepting-without-asking are separate settings.
func TestPairingDefaults(t *testing.T) {
	cases := []struct {
		name           string
		yaml           string
		wantAdvertised bool
		wantAutomatic  bool
	}{
		{
			name:           "absent means advertised and automatic",
			yaml:           "network:\n  mode: mdns\n  interface: \"*\"\n",
			wantAdvertised: true,
			wantAutomatic:  true,
		},
		{
			name:           "require_approval keeps the advertisement but stops for a decision",
			yaml:           "network:\n  mode: mdns\n  interface: \"*\"\nrequire_approval: true\n",
			wantAdvertised: true,
			wantAutomatic:  false,
		},
		{
			name:           "explicit false is honoured, not overwritten by the default",
			yaml:           "network:\n  mode: mdns\n  interface: \"*\"\nauto_accept: false\n",
			wantAdvertised: false,
			wantAutomatic:  false,
		},
		{
			name:           "explicit true is still automatic",
			yaml:           "network:\n  mode: mdns\n  interface: \"*\"\nauto_accept: true\n",
			wantAdvertised: true,
			wantAutomatic:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "eebus.yaml")
			if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
				t.Fatalf("write config: %v", err)
			}
			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if got := cfg.Advertised(); got != tc.wantAdvertised {
				t.Errorf("Advertised() = %v, want %v", got, tc.wantAdvertised)
			}
			if got := cfg.AcceptsPairingAutomatically(); got != tc.wantAutomatic {
				t.Errorf("AcceptsPairingAutomatically() = %v, want %v", got, tc.wantAutomatic)
			}
		})
	}
}
