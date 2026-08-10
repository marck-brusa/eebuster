// Package staticmdns implements ship-go's api.MdnsProviderInterface by synthesising
// SHIP mDNS TXT records from a static peer list instead of performing real mDNS discovery.
//
// This exists because SHIP peer discovery is mDNS multicast on _ship._tcp, which does not
// cross a Docker bridge network and, on WSL2, does not reach the LAN even under
// network_mode: host (WSL2 sits behind Windows' NAT). See docs/01-architecture.md
// "Connectivity" for the full explanation. Rather than fight multicast, this provider feeds
// the real mDNS manager exactly the TXT elements a genuine _ship._tcp announcement would
// contain, so every layer above it (parsing, trust, connection) is untouched.
package staticmdns

import (
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	shipapi "github.com/enbility/ship-go/api"
)

// Peer is one statically configured SHIP peer, mirroring config/eebus.yaml's peers: list.
type Peer struct {
	Name       string // free-form label, used only in the synthesised service name
	SKI        string // 40 hex chars, mandatory
	Host       string // hostname or IP, mandatory
	Port       int    // mandatory
	Path       string // websocket path, defaults to "/ship/" if empty
	AutoAccept bool   // becomes the TXT "register" flag
	Brand      string // optional metadata
	Model      string
	Serial     string
	Categories []shipapi.DeviceCategoryType // optional
}

// txtElements builds the exact map[string]string that ship-go's mdns.processShipMdnsEntry
// expects to parse: mandatory keys txtvers, id, path, ski, register; optional brand, type,
// model, serial, cat. See ship-go/mdns/mdns.go for the parser this mirrors.
func (p Peer) txtElements() map[string]string {
	elements := map[string]string{
		"txtvers":  "1",
		"id":       p.SKI, // SHIP ID; using the SKI itself is fine when no separate ID is configured
		"path":     p.Path,
		"ski":      p.SKI,
		"register": strconv.FormatBool(p.AutoAccept),
	}
	if p.Brand != "" {
		elements["brand"] = p.Brand
	}
	if p.Model != "" {
		elements["model"] = p.Model
	}
	if p.Serial != "" {
		elements["serial"] = p.Serial
	}
	if len(p.Categories) > 0 {
		cats := make([]string, len(p.Categories))
		for i, c := range p.Categories {
			cats[i] = strconv.FormatUint(uint64(c), 10)
		}
		elements["cat"] = strings.Join(cats, ",")
	}
	return elements
}

func (p Peer) serviceName() string {
	name := p.Name
	if name == "" {
		name = p.SKI
	}
	return fmt.Sprintf("%s._ship._tcp.local.", name)
}

func (p Peer) resolveAddresses() ([]net.IP, error) {
	if ip := net.ParseIP(p.Host); ip != nil {
		return []net.IP{ip}, nil
	}
	addrs, err := net.LookupIP(p.Host)
	if err != nil {
		return nil, fmt.Errorf("staticmdns: resolving %q for peer %q: %w", p.Host, p.SKI, err)
	}
	return addrs, nil
}

// reannounceInterval controls how often configured peers are re-delivered to the mDNS
// manager's callback. Real mDNS re-announces periodically and on TTL expiry; this mimics
// that so the Hub's reconnect logic (which reacts to entry updates) keeps retrying a peer
// that was temporarily down, without requiring a container restart.
const reannounceInterval = 30 * time.Second

// Provider is a static-peer implementation of shipapi.MdnsProviderInterface. Construct with
// New, populate via SetPeers (safe to call before or after Start), then hand it to
// ship-go's mdns manager via the exported hook added by patches/eebus-service-mdns-hook.patch
// (Service.SetMdnsProvider), after Configuration.SetMdnsProviderSelection(mdns.MdnsProviderSelectionTestSetup).
type Provider struct {
	mu    sync.Mutex
	peers []Peer

	cb       shipapi.MdnsResolveCB
	stopCh   chan struct{}
	started  bool
	localSKI string // never report a peer whose SKI equals our own, mirroring ship-go's own self-filter

	// real, when set, is a genuine mDNS provider (ship-go's zeroconf) that this provider wraps
	// rather than replaces -- see NewHybrid.
	real shipapi.MdnsProviderInterface
}

// New creates a purely static provider: no real multicast at all, only the configured peer
// list. localSKI, if non-empty, is used to skip a configured peer that happens to share our
// own SKI (a misconfiguration, not a real peer).
func New(localSKI string) *Provider {
	return &Provider{localSKI: localSKI}
}

// NewHybrid wraps a real mDNS provider so this one *adds* static peers rather than replacing
// real discovery. Fixes two real, separate bugs that the pure-static provider caused, both
// reported from the dashboard against real hardware:
//
//  1. Clicking Trust on an mDNS scan result silently did nothing. Trust() ->
//     RegisterRemoteService() registers the SKI and then asks the *Service's own* mDNS manager
//     to re-report what it knows (hub_pairing.go's RequestMdnsEntries). With a pure-static
//     provider that manager has only ever heard of the configured peers, so a scan-discovered
//     device was never in its entry table, no host/port could be resolved, and no dial was
//     ever attempted. (The scan itself comes from an independent browse -- see
//     eebusgo.Discover -- which is why the device appeared in the UI but could not be reached.)
//  2. Our own service was invisible to real devices. AnnounceService was a deliberate no-op
//     here, so nothing was ever published on the LAN and a real device browsing for
//     _ship._tcp could not see us at all.
//
// Delegating announce + browse to a real provider while still injecting static peers makes
// static mode a superset of both instead of a substitute for real mDNS, so the simulator
// (a static peer on localhost) and real hardware work at the same time.
func NewHybrid(localSKI string, real shipapi.MdnsProviderInterface) *Provider {
	return &Provider{localSKI: localSKI, real: real}
}

// SetPeers replaces the configured peer list. Safe to call at any time, including after
// Start; the next reannounce tick (or an immediate synchronous delivery if already started)
// picks up the change. This is what backs POST /api/v1/config/reload for network.mode: static.
func (p *Provider) SetPeers(peers []Peer) {
	p.mu.Lock()
	p.peers = append([]Peer(nil), peers...)
	cb := p.cb
	started := p.started
	p.mu.Unlock()

	if started && cb != nil {
		p.deliverAll(cb)
	}
}

// Start implements shipapi.MdnsProviderInterface. pairingMode and autoReconnect are accepted
// for interface compatibility but unused: a static peer list has no ask/deny pairing gate at
// the discovery layer (trust is handled the normal SHIP way once connected), and reannounce
// here always runs regardless of autoReconnect, since retrying a static peer costs nothing.
func (p *Provider) Start(pairingMode shipapi.PairingMode, autoReconnect bool, cb shipapi.MdnsResolveCB) bool {
	p.mu.Lock()
	if p.started {
		p.mu.Unlock()
		return true
	}
	p.cb = cb
	p.stopCh = make(chan struct{})
	p.started = true
	stopCh := p.stopCh
	real := p.real
	p.mu.Unlock()

	// Real discoveries flow into the same callback the static entries use, so the Service's
	// own mDNS manager ends up with one entry table covering both -- that table is what
	// RegisterRemoteService/Trust later resolves a host:port from.
	if real != nil && !real.Start(pairingMode, autoReconnect, cb) {
		return false
	}

	p.deliverAll(cb)

	go func() {
		ticker := time.NewTicker(reannounceInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				p.mu.Lock()
				currentCb := p.cb
				p.mu.Unlock()
				if currentCb != nil {
					p.deliverAll(currentCb)
				}
			}
		}
	}()

	return true
}

// deliverAll invokes cb once per configured peer with a synthesised, valid _ship._tcp TXT
// record. Resolution failures are skipped rather than fatal: a peer that is briefly
// unresolvable (DHCP hiccup) should not take down the whole provider, and will be retried on
// the next reannounce tick.
func (p *Provider) deliverAll(cb shipapi.MdnsResolveCB) {
	p.mu.Lock()
	peers := append([]Peer(nil), p.peers...)
	p.mu.Unlock()

	for _, peer := range peers {
		if peer.SKI == p.localSKI {
			continue
		}
		path := peer.Path
		if path == "" {
			path = "/ship/"
		}
		peer.Path = path

		addrs, err := peer.resolveAddresses()
		if err != nil {
			continue
		}

		cb(peer.txtElements(), peer.serviceName(), peer.Host, "_ship._tcp", addrs, peer.Port, false)
	}
}

// Shutdown implements shipapi.MdnsProviderInterface.
func (p *Provider) Shutdown() {
	p.mu.Lock()
	if p.started && p.stopCh != nil {
		close(p.stopCh)
	}
	p.started = false
	real := p.real
	p.mu.Unlock()

	if real != nil {
		real.Shutdown()
	}
}

// AnnounceService publishes our own service on the LAN through the wrapped real provider, so
// real devices browsing for _ship._tcp can see us -- without this a device has no way to
// discover this testbench and initiate its own pairing. In pure-static mode (no real provider)
// it stays a no-op: there, we are always the connecting side, dialling out to a known
// host:port, and nothing needs to announce us for that direction to work. Returns a stable
// synthetic instance ID in that case so callers that log/track it have something.
func (p *Provider) AnnounceService(serviceType, serviceName string, port int, txt []string) (string, error) {
	p.mu.Lock()
	real := p.real
	p.mu.Unlock()

	if real != nil {
		return real.AnnounceService(serviceType, serviceName, port, txt)
	}
	return "static:" + serviceType + ":" + serviceName, nil
}

// UnannounceService mirrors AnnounceService: delegate when wrapping a real provider, no-op
// otherwise.
func (p *Provider) UnannounceService(instanceID string) error {
	p.mu.Lock()
	real := p.real
	p.mu.Unlock()

	if real != nil {
		return real.UnannounceService(instanceID)
	}
	return nil
}
