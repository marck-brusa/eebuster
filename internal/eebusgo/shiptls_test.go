package eebusgo

import (
	"crypto/tls"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/enbility/ship-go/cert"
)

// SHIP 9 and 9.1 allow TLS 1.2 on secp256r1 (brainpool aside) and nothing else. The vendored
// ship-go only gets there through patches/ship-go-tls12-only.patch, which a plain
// `go mod vendor` drops; without it crypto/tls negotiates TLS 1.3, and X25519, with any peer
// that offers them.
func TestVendoredShipTLSIsTLS12OnP256(t *testing.T) {
	if cert.TLSVersion != tls.VersionTLS12 {
		t.Errorf("cert.TLSVersion = %#04x, want TLS 1.2", cert.TLSVersion)
	}
	if !slices.Equal(cert.CurvePreferences, []tls.CurveID{tls.CurveP256}) {
		t.Errorf("cert.CurvePreferences = %v, want only P-256", cert.CurvePreferences)
	}

	shipGo := "../../vendor/github.com/enbility/ship-go"
	settings := []*regexp.Regexp{
		regexp.MustCompile(`MinVersion:\s+cert\.TLSVersion,`),
		regexp.MustCompile(`MaxVersion:\s+cert\.TLSVersion,`),
		regexp.MustCompile(`CurvePreferences:\s+cert\.CurvePreferences,`),
	}
	for _, file := range []string{"hub/hub_connections_client.go", "hub/hub_connections_server.go"} {
		source, err := os.ReadFile(filepath.Join(shipGo, file))
		if err != nil {
			t.Fatalf("vendored hub not readable: %v", err)
		}
		for _, setting := range settings {
			if !setting.Match(source) {
				t.Errorf("%s: its tls.Config lacks %s", file, setting)
			}
		}
	}

	// The two configs above must be the only ones: a new one in an upgraded ship-go would
	// fall back to the crypto/tls defaults.
	configs := 0
	err := filepath.WalkDir(shipGo, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && !entry.IsDir() && strings.HasSuffix(path, ".go") {
			source, readErr := os.ReadFile(path)
			configs += strings.Count(string(source), "tls.Config{")
			walkErr = readErr
		}
		return walkErr
	})
	if err != nil {
		t.Fatalf("vendored ship-go not readable: %v", err)
	}
	if configs != 2 {
		t.Errorf("vendored ship-go builds %d tls.Config literals, want the 2 checked above", configs)
	}
}
