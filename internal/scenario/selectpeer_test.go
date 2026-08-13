package scenario

import (
	"strings"
	"testing"
)

const (
	// Synthetic SKIs: 40 hex characters, deliberately not any real device's. "real" here means
	// "the configured device under test" as opposed to the simulator, not a real SKI.
	realSKI = "cccccccc3333333333333333333333333333feed"
	simSKI  = "dddddddd4444444444444444444444444444face"
)

// peers mimics GET /api/v1/peers. Note "name" is the mDNS instance name, which is what the
// endpoint actually returns -- never the configured peer name.
func peers() []any {
	return []any{
		map[string]any{"ski": simSKI, "name": "sim-load-1._ship._tcp.local."},
		map[string]any{"ski": realSKI, "name": "device-under-test._ship._tcp.local."},
	}
}

var configPeers = map[string]string{"device-under-test": realSKI}

// The regression this whole function exists for: the configured name never equals the mDNS
// name, so the old lookup missed and silently fell back to peers[0]. With a simulator enabled
// that is a different device, and every scenario reported results for the wrong one.
func TestSelectPeerResolvesConfiguredNameNotFirstPeer(t *testing.T) {
	got, err := selectPeer(peers(), configPeers, "device-under-test")
	if err != nil {
		t.Fatalf("selectPeer: %v", err)
	}
	if ski := got["ski"]; ski != realSKI {
		t.Errorf("resolved to ski %v; want the configured device %s", ski, realSKI)
	}
}

// Ordering must not matter. If it does, the fallback bug is back.
func TestSelectPeerIgnoresPeerOrder(t *testing.T) {
	reversed := []any{peers()[1], peers()[0]}
	for name, list := range map[string][]any{"sim first": peers(), "real first": reversed} {
		got, err := selectPeer(list, configPeers, "device-under-test")
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got["ski"] != realSKI {
			t.Errorf("%s: resolved to %v; want %s", name, got["ski"], realSKI)
		}
	}
}

func TestSelectPeerAcceptsLiteralSKI(t *testing.T) {
	got, err := selectPeer(peers(), nil, realSKI)
	if err != nil {
		t.Fatalf("selectPeer with literal ski: %v", err)
	}
	if got["ski"] != realSKI {
		t.Errorf("resolved to %v; want %s", got["ski"], realSKI)
	}
}

func TestSelectPeerFailsRatherThanGuess(t *testing.T) {
	cases := map[string]struct {
		peers    []any
		cfg      map[string]string
		peerName string
	}{
		"unknown name":            {peers(), configPeers, "not-in-config"},
		"configured but offline":  {[]any{peers()[0]}, configPeers, "device-under-test"},
		"literal ski not present": {[]any{peers()[0]}, nil, realSKI},
		"no peers at all":         {nil, configPeers, "device-under-test"},
	}
	for name, tc := range cases {
		got, err := selectPeer(tc.peers, tc.cfg, tc.peerName)
		if err == nil {
			t.Errorf("%s: expected an error, got peer %v", name, got)
		}
		if got != nil {
			t.Errorf("%s: expected a nil peer alongside the error, got %v", name, got)
		}
	}
}

// Every bundled scenario targets "device-under-test", so a peers: entry named anything else
// fails all 18 of them. The message has to name the configured peers, otherwise the mismatch is
// invisible and looks like a broken test runner rather than a one-word config fix.
func TestSelectPeerUnknownNameListsConfiguredNames(t *testing.T) {
	cfg := map[string]string{"station-under-test": realSKI, "bench-sim": simSKI}

	_, err := selectPeer(peers(), cfg, "device-under-test")
	if err == nil {
		t.Fatal("expected an error for a name that is not configured")
	}
	for _, want := range []string{"station-under-test", "bench-sim", "device-under-test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

// With no peers: entries at all, listing names is useless -- say what to add instead.
func TestSelectPeerNoConfiguredPeersExplainsHowToFix(t *testing.T) {
	_, err := selectPeer(peers(), nil, "device-under-test")
	if err == nil {
		t.Fatal("expected an error when the config has no peers")
	}
	for _, want := range []string{"no peers: entries", "device-under-test"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should mention %q, got: %v", want, err)
		}
	}
}

func TestIsSKI(t *testing.T) {
	valid := []string{realSKI, "ABCDEF0123456789abcdef0123456789ABCDEF01"}
	invalid := []string{"", "device-under-test", realSKI[:39], realSKI + "0", "zz" + realSKI[2:]}
	for _, s := range valid {
		if !isSKI(s) {
			t.Errorf("isSKI(%q) = false; want true", s)
		}
	}
	for _, s := range invalid {
		if isSKI(s) {
			t.Errorf("isSKI(%q) = true; want false", s)
		}
	}
}

// The truststore workflow has no peers: entries at all; a single connected peer is then
// unambiguous and used automatically, while two or more still fail loudly -- targeting the
// wrong device silently is the failure mode strict resolution was built to prevent.
func TestSelectPeerWithoutConfigEntries(t *testing.T) {
	one := []any{
		map[string]any{"ski": "aaaa000000000000000000000000000000000000", "connected": true},
		map[string]any{"ski": "bbbb000000000000000000000000000000000000", "connected": false},
	}
	peer, err := selectPeer(one, map[string]string{}, "device-under-test")
	if err != nil {
		t.Fatalf("single connected peer must resolve automatically: %v", err)
	}
	if peer["ski"] != "aaaa000000000000000000000000000000000000" {
		t.Fatalf("resolved the wrong peer: %v", peer)
	}

	two := append(one, map[string]any{"ski": "cccc000000000000000000000000000000000000", "connected": true})
	if _, err := selectPeer(two, map[string]string{}, "device-under-test"); err == nil {
		t.Fatal("two connected peers without config entries must fail loudly")
	}

	if _, err := selectPeer(nil, map[string]string{}, "device-under-test"); err == nil {
		t.Fatal("no connected peers must fail")
	}
}
