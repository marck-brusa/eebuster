package staticmdns

import (
	"net"
	"sync"
	"testing"
	"time"

	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mdns"
)

// fakeReport captures whatever ship-go's real MdnsManager reports after running our
// synthesised TXT elements through its actual, unexported parsing path
// (processMdnsEntry -> processShipMdnsEntry). This is deliberately an end-to-end test against
// the real mdns package, not a test of our own parsing, because the whole point of this
// provider is to feed data that ship-go's real parser accepts.
type fakeReport struct {
	mu      sync.Mutex
	entries map[string]*shipapi.MdnsEntry
}

func (f *fakeReport) ReportMdnsEntries(entries map[string]*shipapi.MdnsEntry, _ bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = entries
}

func (f *fakeReport) snapshot() map[string]*shipapi.MdnsEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]*shipapi.MdnsEntry, len(f.entries))
	for k, v := range f.entries {
		out[k] = v
	}
	return out
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

func TestProvider_DeliversPeerThroughRealMdnsManager(t *testing.T) {
	provider := New("")
	provider.SetPeers([]Peer{
		{
			Name:       "cs-under-test",
			SKI:        "0011223344556677889900112233445566778899",
			Host:       "127.0.0.1",
			Port:       4712,
			Path:       "/ship/",
			AutoAccept: true,
			Brand:      "Testbench",
			Model:      "DUT",
			Serial:     "bench3",
		},
	})

	// Construct the real ship-go MdnsManager exactly as eebus-go's service.Service does
	// internally, but with our provider forced in via MdnsProviderSelectionTestSetup.
	manager := mdns.NewMDNS(
		"selfski0000000000000000000000000000000",
		"LAB", "eebus-testbench", "EnergyManagementSystem", "test-01",
		nil, "test-01", "eebus-testbench", 4711, nil,
		mdns.MdnsProviderSelectionTestSetup,
	)
	manager.SetMdnsProvider(provider)

	report := &fakeReport{}
	if err := manager.Start(shipapi.PairingModeBoth, report); err != nil {
		t.Fatalf("manager.Start: %v", err)
	}
	defer manager.Shutdown()

	waitFor(t, 2*time.Second, func() bool { return len(report.snapshot()) > 0 })

	entries := report.snapshot()
	var found *shipapi.MdnsEntry
	for _, e := range entries {
		if e.Ski == "0011223344556677889900112233445566778899" {
			found = e
			break
		}
	}
	if found == nil {
		t.Fatalf("peer SKI not found in reported entries: %+v", entries)
	}
	if found.Host != "127.0.0.1" {
		t.Errorf("Host = %q, want 127.0.0.1", found.Host)
	}
	if found.Port != 4712 {
		t.Errorf("Port = %d, want 4712", found.Port)
	}
	if found.Path != "/ship/" {
		t.Errorf("Path = %q, want /ship/", found.Path)
	}
	if !found.Register {
		t.Errorf("Register = false, want true (AutoAccept was set)")
	}
	if len(found.Addresses) == 0 {
		t.Errorf("Addresses is empty, want at least 127.0.0.1")
	}
}

func TestProvider_SkipsOwnSKI(t *testing.T) {
	const selfSKI = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	provider := New(selfSKI)
	provider.SetPeers([]Peer{
		{Name: "self-loop", SKI: selfSKI, Host: "127.0.0.1", Port: 9999, AutoAccept: true},
		{Name: "real-peer", SKI: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", Host: "127.0.0.1", Port: 4712, AutoAccept: true},
	})

	var mu sync.Mutex
	delivered := map[string]bool{}
	ok := provider.Start(shipapi.PairingModeBoth, true, func(elements map[string]string, _, _, _ string, _ []net.IP, _ int, _ bool) {
		mu.Lock()
		delivered[elements["ski"]] = true
		mu.Unlock()
	})
	if !ok {
		t.Fatalf("Start returned false")
	}
	defer provider.Shutdown()

	waitFor(t, 2*time.Second, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(delivered) > 0
	})

	mu.Lock()
	defer mu.Unlock()
	if delivered[selfSKI] {
		t.Errorf("self SKI %s was delivered, want it filtered out", selfSKI)
	}
	if !delivered["bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"] {
		t.Errorf("real peer SKI was not delivered: %+v", delivered)
	}
}
