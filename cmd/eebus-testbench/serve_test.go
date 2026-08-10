package main

import (
	"os"
	"path/filepath"
	"testing"
)

// The release archive ships eebus.yaml beside the executable while the historical default was
// config/eebus.yaml. Resolving only one of them meant a bare `serve` ignored a real config file,
// dropping its configured peers without a word, so both layouts are pinned here.
func TestDefaultConfigPath(t *testing.T) {
	cases := []struct {
		name    string
		create  []string
		want    string
		wantAbs bool
	}{
		{name: "prefers config/ when both exist", create: []string{"config/eebus.yaml", "eebus.yaml"}, want: "config/eebus.yaml"},
		{name: "finds config/ alone", create: []string{"config/eebus.yaml"}, want: "config/eebus.yaml"},
		{name: "finds the archive layout alone", create: []string{"eebus.yaml"}, want: "eebus.yaml"},
		// With nothing present the caller reports "no config file at X"; naming the archive
		// layout is the more useful hint, since that is where a user would put one.
		{name: "names the last candidate when none exist", create: nil, want: "eebus.yaml"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			previous, err := os.Getwd()
			if err != nil {
				t.Fatalf("getwd: %v", err)
			}
			if err := os.Chdir(dir); err != nil {
				t.Fatalf("chdir: %v", err)
			}
			t.Cleanup(func() { _ = os.Chdir(previous) })

			for _, rel := range tc.create {
				if err := os.MkdirAll(filepath.Dir(rel), 0o755); err != nil {
					t.Fatalf("mkdir for %s: %v", rel, err)
				}
				if err := os.WriteFile(rel, []byte("network:\n  mode: mdns\n"), 0o644); err != nil {
					t.Fatalf("write %s: %v", rel, err)
				}
			}

			if got := defaultConfigPath(); got != tc.want {
				t.Errorf("defaultConfigPath() = %q, want %q", got, tc.want)
			}
		})
	}
}
