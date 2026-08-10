package identity

import (
	"crypto/tls"
	"path/filepath"
	"testing"
)

// A regenerated identity means a new SKI, which silently invalidates every existing pairing.
// LoadOrCreate's "created" flag is the only signal a caller has, so both halves of it are
// pinned here: true exactly once, false on every later load, and the same key material back.
func TestLoadOrCreateReportsGenerationOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	first, created, err := LoadOrCreate(certPath, keyPath, "LAB", "model", "DE", "serial-01")
	if err != nil {
		t.Fatalf("first LoadOrCreate: %v", err)
	}
	if !created {
		t.Error("first call generated the identity but reported created=false")
	}

	second, created, err := LoadOrCreate(certPath, keyPath, "LAB", "model", "DE", "serial-01")
	if err != nil {
		t.Fatalf("second LoadOrCreate: %v", err)
	}
	if created {
		t.Error("second call reported created=true, so an existing identity was overwritten")
	}

	firstSKI, err := SKI(first)
	if err != nil {
		t.Fatalf("ski of generated identity: %v", err)
	}
	secondSKI, err := SKI(second)
	if err != nil {
		t.Fatalf("ski of reloaded identity: %v", err)
	}
	if firstSKI != secondSKI {
		t.Errorf("reload produced a different ski: %s then %s", firstSKI, secondSKI)
	}
	if len(firstSKI) != 40 {
		t.Errorf("ski is %d chars, want 40: %q", len(firstSKI), firstSKI)
	}
}

func TestSKIRejectsEmptyCertificate(t *testing.T) {
	if _, err := SKI(tls.Certificate{}); err == nil {
		t.Error("expected an error for a certificate with no leaf")
	}
}

func TestAnnouncedSerial(t *testing.T) {
	// Synthetic SKI: 40 hex chars, not a real device.
	const ski = "0123456789abcdef0123456789abcdef01234567"

	cases := []struct {
		name   string
		serial string
		ski    string
		want   string
	}{
		{"appends a fragment", "testbench-01", ski, "testbench-01-01234567"},
		{"lowercases the fragment", "testbench-01", "ABCDEF89" + ski[8:], "testbench-01-abcdef89"},
		{"missing ski leaves the serial alone", "testbench-01", "", "testbench-01"},
		{"short ski leaves the serial alone", "testbench-01", "abc", "testbench-01"},
		{"empty serial still gets a fragment", "", ski, "-01234567"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := AnnouncedSerial(tc.serial, tc.ski); got != tc.want {
				t.Errorf("AnnouncedSerial(%q, %q) = %q, want %q", tc.serial, tc.ski, got, tc.want)
			}
		})
	}
}

// Two identities differing only in their SKI must not produce the same announced serial --
// that collision is exactly what made the wrong one selectable in a device's pairing list.
func TestAnnouncedSerialDistinguishesTwoIdentities(t *testing.T) {
	const serial = "testbench-01"
	a := AnnouncedSerial(serial, "aaaaaaaa0000000000000000000000000000cafe")
	b := AnnouncedSerial(serial, "bbbbbbbb0000000000000000000000000000cafe")
	if a == b {
		t.Errorf("two different identities announce the same serial %q", a)
	}
}
