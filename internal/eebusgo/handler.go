package eebusgo

import (
	"log"
	"sync"
	"time"

	"github.com/enbility/eebus-go/api"
	shipapi "github.com/enbility/ship-go/api"
)

// PendingPairing is the JSON shape for GET /peers/pending, matching what
// JsonRpcAdapter.pending_pairings() returned: SKIs waiting for a user decision because
// UserIsAbleToApproveOrCancelPairingRequests(true) holds them instead of auto-denying.
type PendingPairing struct {
	SKI   string `json:"ski"`
	State string `json:"state"`
}

// VisiblePeer is the JSON shape for GET /peers/visible: SKIs the mDNS manager currently sees
// but hasn't connected, matching JsonRpcAdapter.visible_peers().
type VisiblePeer struct {
	SKI string `json:"ski"`
	// Label is the best available human name, so a caller never has to render a bare SKI --
	// same field and same precedence as PeerInfo.Label, so the dashboard can treat a visible
	// and a connected peer identically.
	Label  string `json:"label,omitempty"`
	Name   string `json:"name,omitempty"`
	Brand  string `json:"brand,omitempty"`
	Model  string `json:"model,omitempty"`
	Serial string `json:"serial,omitempty"`
	ShipID string `json:"ship_id,omitempty"`
}

// DeviceMeta is the identity metadata a device announces over mDNS. Cached by SKI because a
// *connected* peer has none of it available: ship-go's ServiceDetails (the only thing the hub
// exposes for an established connection) carries SKI/ShipID/fingerprint and nothing else --
// no brand, model or serial. Without this cache the dashboard's "Known peers" list has only a
// 40-hex SKI to show once a device connects, which is exactly the "show the name, not just
// the SKI" gap this closes.
type DeviceMeta struct {
	Name   string
	Brand  string
	Model  string
	Serial string
	ShipID string
}

type pairingState struct {
	mu      sync.Mutex
	pending map[string]PendingPairing
	visible map[string]VisiblePeer
	meta    map[string]DeviceMeta // by SKI; survives a peer moving from visible -> connected
	// dialErrors holds the last concrete dial failure per SKI, scraped from ship-go's own
	// logging -- see trustwatch.go.
	dialErrors map[string]string
	// conflictRecoveries throttles certificate-rotation recovery per SKI -- see trustwatch.go.
	conflictRecoveries map[string]time.Time
	// lastDiscovery is when we last saw *any* peer over mDNS. Zero means never, which is the
	// signal that inbound multicast is not reaching this host at all -- see DiscoveryHealth.
	lastDiscovery time.Time
	// peakVisible is the most peers ever seen at once, so a transient drop to zero stays
	// distinguishable from having never discovered anything.
	peakVisible int
}

func newPairingState() *pairingState {
	return &pairingState{
		pending:    map[string]PendingPairing{},
		visible:    map[string]VisiblePeer{},
		meta:       map[string]DeviceMeta{},
		dialErrors: map[string]string{},
	}
}

// DiscoveryHealth reports whether inbound mDNS is reaching this host at all.
//
// It exists because "nothing shows up" has two completely different causes that look identical
// from the dashboard: the peer is absent, or multicast never arrives. The second is common and
// invisible -- on a multi-AP wireless network, multicast is normally not forwarded between
// access points, so two hosts on the same SSID but different APs can reach each other by
// unicast (SHIP connects fine once trusted) while neither ever sees the other's announcement.
// Wired-to-wireless bridges and virtual adapters produce the same asymmetry.
type DiscoveryHealth struct {
	VisibleNow  int    `json:"visible_now"`
	PeakVisible int    `json:"peak_visible"`
	EverSeen    bool   `json:"ever_seen"`
	LastSeenAgo *int   `json:"last_seen_seconds_ago"`
	Warning     string `json:"warning,omitempty"`
}

// discoveryGrace is how long to wait before treating silence as a fault. Peers announce on
// startup and answer queries, so a healthy link produces a report well inside this.
const discoveryGrace = 30 * time.Second

func (s *Stack) DiscoveryHealth() DiscoveryHealth {
	s.pairing.mu.Lock()
	visible := len(s.pairing.visible)
	last := s.pairing.lastDiscovery
	peak := s.pairing.peakVisible
	s.pairing.mu.Unlock()

	health := DiscoveryHealth{VisibleNow: visible, PeakVisible: peak, EverSeen: !last.IsZero()}
	if !last.IsZero() {
		ago := int(time.Since(last).Seconds())
		health.LastSeenAgo = &ago
		return health
	}
	if time.Since(s.startedAt) < discoveryGrace {
		return health // still inside the grace period; silence is not yet meaningful
	}
	health.Warning = "no peer has ever been seen over mDNS. Inbound multicast is not reaching this host: " +
		"on a multi-AP wireless network multicast is usually not forwarded between access points, so " +
		"check that this host and the device are associated to the same AP (compare BSSIDs), or pin the " +
		"device's address with a peers: entry, which does not need discovery."
	return health
}

var _ api.ServiceReaderInterface = (*Stack)(nil)

// RemoteServiceConnected clears any pending-pairing entry for this SKI -- connecting is a
// terminal outcome for the pairing wait, same as JsonRpcAdapter.trust() popping it.
func (s *Stack) RemoteServiceConnected(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	s.pairing.mu.Lock()
	delete(s.pairing.pending, identity.SKI)
	s.pairing.mu.Unlock()
	log.Printf("eebus: connection received from %s (ski %s)", identity.ShipID, identity.SKI)
	// A connection is a completed, mutual trust decision no matter who initiated it. The
	// callback lets the caller persist peers that paired *from the device's side* (accepted
	// here via auto-accept), which the trust API alone never sees -- without it, a testbench
	// restart left reconnection entirely to the device's own retry policy, and devices that
	// give up (KEO reaches CANNOT_CONNECT) then looked like a lost pairing.
	if s.OnPeerConnected != nil {
		s.OnPeerConnected(identity.SKI)
	}
	s.events.Publish("lifecycle", stackID, "remote_connected", identity.SKI, nil)
}

func (s *Stack) RemoteServiceDisconnected(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	log.Printf("eebus: disconnected from ski %s", identity.SKI)
	s.events.Publish("lifecycle", stackID, "remote_disconnected", identity.SKI, nil)
}

func (s *Stack) VisibleRemoteMdnsServicesUpdated(_ api.ServiceInterface, entries []shipapi.RemoteMdnsService) {
	s.pairing.mu.Lock()
	s.pairing.visible = make(map[string]VisiblePeer, len(entries))
	for _, e := range entries {
		name := unescapeInstanceName(e.Name)
		s.pairing.visible[e.Ski] = VisiblePeer{
			SKI: e.Ski, Name: name, Brand: e.Brand, Model: e.Model, Serial: e.Serial, ShipID: e.ShipID,
			Label: displayName(e.Brand, e.Model, e.Serial, name, e.Ski),
		}
		// Cache identity metadata, never clear it: a device that connects disappears from the
		// visible list (it is no longer "seen but unconnected") but the dashboard still needs
		// its name afterwards.
		s.pairing.meta[e.Ski] = DeviceMeta{
			Name: name, Brand: e.Brand, Model: e.Model, Serial: e.Serial, ShipID: e.ShipID,
		}
	}
	if len(entries) > 0 {
		s.pairing.lastDiscovery = time.Now()
		if len(entries) > s.pairing.peakVisible {
			s.pairing.peakVisible = len(entries)
		}
	}
	s.pairing.mu.Unlock()
	log.Printf("mdns: %d service(s) visible", len(entries))
	s.events.Publish("lifecycle", stackID, "visible_services_updated", "", map[string]int{"count": len(entries)})
}

// RecordDiscovered folds a standalone scan's results (eebusgo.Discover, which uses its own
// browse and so never reaches the callback above) into the same metadata cache, so a device
// found by clicking Scan is named properly in the peer list once it connects too.
func (s *Stack) RecordDiscovered(found []DiscoveredService) {
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	for _, d := range found {
		if d.SKI == "" {
			continue
		}
		s.pairing.meta[d.SKI] = DeviceMeta{
			Name: d.Name, Brand: d.Brand, Model: d.Model, Serial: d.Serial, ShipID: d.ShipID,
		}
	}
}

// MetaFor returns cached announced identity for a SKI, if anything has ever announced it.
func (s *Stack) MetaFor(ski string) DeviceMeta {
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	return s.pairing.meta[ski]
}

func (s *Stack) ServiceUpdated(identity shipapi.ServiceIdentity) {
	log.Printf("eebus: service updated, ski %s, ship id %s", identity.SKI, identity.ShipID)
	s.events.Publish("lifecycle", stackID, "service_updated", identity.SKI, map[string]string{"ship_id": identity.ShipID})
}

// ServicePairingDetailUpdate is where an incoming pairing request actually surfaces: unlike
// the old RPC wire (ConnectionStateDetail.State() has no JSON tag in ship-go, so Python only
// got best-effort raw notification params), this reads the real, typed
// shipapi.ConnectionState enum directly -- see docs/ for why that's strictly better than what
// this replaces.
func (s *Stack) ServicePairingDetailUpdate(identity shipapi.ServiceIdentity, detail *shipapi.ConnectionStateDetail) {
	s.pairing.mu.Lock()
	switch detail.State() {
	case shipapi.ConnectionStateReceivedPairingRequest:
		s.pairing.pending[identity.SKI] = PendingPairing{SKI: identity.SKI, State: "received_pairing_request"}
	case shipapi.ConnectionStateCompleted, shipapi.ConnectionStateTrusted, shipapi.ConnectionStateRemoteDeniedTrust, shipapi.ConnectionStateNone:
		delete(s.pairing.pending, identity.SKI)
	}
	s.pairing.mu.Unlock()
	log.Printf("ship: pairing state for ski %s -> %s", identity.SKI, connectionStateName(detail.State()))
	s.events.Publish("lifecycle", stackID, "pairing_detail_update", identity.SKI, map[string]any{"state": int(detail.State())})
}

func connectionStateName(state shipapi.ConnectionState) string {
	switch state {
	case shipapi.ConnectionStateNone:
		return "none"
	case shipapi.ConnectionStateQueued:
		return "queued"
	case shipapi.ConnectionStateInitiated:
		return "initiated (we are dialing out)"
	case shipapi.ConnectionStateReceivedPairingRequest:
		return "received pairing request (awaiting approve/deny)"
	case shipapi.ConnectionStateInProgress:
		return "handshake in progress"
	case shipapi.ConnectionStateTrusted:
		return "trusted"
	case shipapi.ConnectionStatePin:
		return "pin"
	case shipapi.ConnectionStateCompleted:
		return "completed"
	case shipapi.ConnectionStateRemoteDeniedTrust:
		return "remote denied trust"
	case shipapi.ConnectionStateError:
		return "error"
	default:
		return "unknown"
	}
}

func (s *Stack) ServiceAutoTrusted(_ api.ServiceInterface, identity shipapi.ServiceIdentity) {
	log.Printf("ship: auto-trusted ski %s", identity.SKI)
	s.events.Publish("lifecycle", stackID, "auto_trusted", identity.SKI, nil)
}

func (s *Stack) ServiceAutoTrustFailed(_ api.ServiceInterface, identity shipapi.ServiceIdentity, reason error) {
	log.Printf("ship: auto-trust failed for ski %s: %v", identity.SKI, reason)
	s.events.Publish("lifecycle", stackID, "auto_trust_failed", identity.SKI, map[string]string{"reason": reason.Error()})
}

func (s *Stack) ServiceAutoTrustRemoved(_ api.ServiceInterface, identity shipapi.ServiceIdentity, reason string) {
	log.Printf("ship: auto-trust removed for ski %s: %s", identity.SKI, reason)
	s.events.Publish("lifecycle", stackID, "auto_trust_removed", identity.SKI, map[string]string{"reason": reason})
}

// PendingPairings lists SKIs currently waiting for an approve/deny decision.
func (s *Stack) PendingPairings() []PendingPairing {
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	out := make([]PendingPairing, 0, len(s.pairing.pending))
	for _, p := range s.pairing.pending {
		out = append(out, p)
	}
	return out
}

// VisiblePeers lists SKIs the mDNS manager currently sees but hasn't connected.
func (s *Stack) VisiblePeers() []VisiblePeer {
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	out := make([]VisiblePeer, 0, len(s.pairing.visible))
	for _, p := range s.pairing.visible {
		out = append(out, p)
	}
	return out
}

// DenyPairing rejects an incoming pairing request -- Service.CancelPairing, matching
// JsonRpcAdapter.deny_pairing's service/CancelPairing call.
func (s *Stack) DenyPairing(ski string) {
	s.service.CancelPairing(shipapi.ServiceIdentity{SKI: ski})
	s.pairing.mu.Lock()
	delete(s.pairing.pending, ski)
	s.pairing.mu.Unlock()
}

// Events exposes the event bus for the SSE/recent-events HTTP routes.
func (s *Stack) Events() *EventBus { return s.events }
