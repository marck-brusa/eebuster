package eebusgo

import (
	"testing"

	"github.com/marck-brusa/eebuster/internal/staticmdns"
)

// Addresses below are RFC 5737 documentation range, and the SKIs are synthetic.
func TestAddressPins(t *testing.T) {
	peers := []staticmdns.Peer{
		{SKI: "1111111111111111111111111111111111111111", Host: "192.0.2.13", Port: 4712},
		// Uppercase in config must still match the lowercase SKI the stack reports.
		{SKI: "2222222222222222222222222222222222222222", Host: "198.51.100.7", Port: 12345},
		// A hostname cannot be pinned: the field upstream exposes holds one IPv4 literal, and
		// resolving the name is the step that fails on the networks this exists for.
		{SKI: "3333333333333333333333333333333333333333", Host: "device.local", Port: 4712},
		// IPv6 has no equivalent field upstream, so it is skipped rather than mangled.
		{SKI: "4444444444444444444444444444444444444444", Host: "2001:db8::1", Port: 4712},
		{SKI: "5555555555555555555555555555555555555555", Host: "", Port: 4712},
	}

	pins := addressPins(peers)

	want := map[string]string{
		"1111111111111111111111111111111111111111": "192.0.2.13",
		"2222222222222222222222222222222222222222": "198.51.100.7",
	}
	if len(pins) != len(want) {
		t.Fatalf("got %d pins %v, want %d", len(pins), pins, len(want))
	}
	for ski, addr := range want {
		if pins[ski] != addr {
			t.Errorf("pin for %s = %q, want %q", ski, pins[ski], addr)
		}
	}
}

func TestAddressPinsLowercasesTheSKI(t *testing.T) {
	pins := addressPins([]staticmdns.Peer{
		{SKI: "AABBCCDD11111111111111111111111111111111", Host: "192.0.2.13", Port: 4712},
	})
	if got := pins["aabbccdd11111111111111111111111111111111"]; got != "192.0.2.13" {
		t.Errorf("lowercase lookup returned %q, want 192.0.2.13 (pins: %v)", got, pins)
	}
}

func TestAddressPinsOfNoPeers(t *testing.T) {
	// Must be an empty map rather than nil: applyAddressPin indexes it without a nil check,
	// and a reload to an empty peer list has to clear the pins, not leave the old ones.
	if pins := addressPins(nil); pins == nil || len(pins) != 0 {
		t.Errorf("addressPins(nil) = %v, want an empty non-nil map", pins)
	}
}
