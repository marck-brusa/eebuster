package eebusgo

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"time"

	shipapi "github.com/enbility/ship-go/api"
)

// Trust() only tells the stack to start dialling -- it returns immediately and says nothing
// about whether the connection actually succeeds. That is why POST /peers/{ski}/trust can
// report success and the device still never appears: a dial-level failure (wrong port,
// firewalled, unreachable address) never reaches the SHIP/SPINE layer, so nothing else
// reports it. This mirrors the Python facade's watch_connection_after_trust, which existed
// for exactly this reason and was missing from the Go rewrite.
const trustWatchTimeout = 25 * time.Second

// dialFailureRe matches ship-go's own failure line, e.g.
//
//	connection to <40-hex-ski> at 192.168.9.12 failed:dial tcp 192.168.9.12:12345: i/o timeout
//
// Scraping the logger is admittedly indirect, but it is the only place the concrete dial error
// exists -- ship-go does not surface it through any API. The Python version explicitly gave up
// here ("the real dial error is still only in that stack's own log"); since this rewrite owns
// the logger it can do better and hand the actual reason back to the caller.
var dialFailureRe = regexp.MustCompile(`connection to ([0-9a-fA-F]{40}) at (\S+) failed:(.*)`)

// identifierConflictRe matches ship-go's rejection when a peer presents a certificate that
// disagrees with the one already cached for its SKI, e.g.
//
//	incoming connection rejected: identifier conflict ... ski <40-hex> ...
//	  identifier conflict on fingerprint: "183B..." vs "2BB7..."
//
// This happens for a legitimate reason: re-imaging or re-commissioning a device can re-issue
// its certificate over the same key pair, so the fingerprint changes while the SKI -- which is
// derived from the public key, and is what EEBUS actually anchors identity on -- does not.
// ship-go caches SKI plus fingerprint together and then refuses the new certificate forever,
// with no API to clear it. Observed on real hardware: after the device was re-commissioned,
// every reconnect was rejected until this process was restarted.
var identifierConflictRe = regexp.MustCompile(`identifier conflict.*?([0-9a-fA-F]{40})`)

// conflictRecoveryCooldown bounds how often a single SKI may be re-registered. The rejection
// repeats on every reconnect attempt (seconds apart), so without a cooldown one rotated
// certificate would produce an unregister/register storm.
const conflictRecoveryCooldown = 30 * time.Second

// observeLogLine records the most recent dial failure per SKI, and recovers from a cached
// certificate that no longer matches the peer. Wired in as StdLogger.Observe for the primary
// stack only.
func (s *Stack) observeLogLine(msg string) {
	if m := identifierConflictRe.FindStringSubmatch(msg); m != nil {
		s.recoverFromIdentifierConflict(strings.ToLower(m[1]))
		return
	}
	m := dialFailureRe.FindStringSubmatch(msg)
	if m == nil {
		return
	}
	ski := strings.ToLower(m[1])
	detail := fmt.Sprintf("%s: %s", m[2], strings.TrimSpace(m[3]))

	s.pairing.mu.Lock()
	if s.pairing.dialErrors == nil {
		s.pairing.dialErrors = map[string]string{}
	}
	s.pairing.dialErrors[ski] = detail
	s.pairing.mu.Unlock()
}

// recoverFromIdentifierConflict clears the cached certificate for a SKI and re-registers it,
// which is the only way back: ship-go exposes no "forget the fingerprint" call, but
// UnregisterRemoteService removes the whole cached service entry (hub_pairing.go's
// removeService), so registering again accepts whatever certificate the peer now presents.
//
// Only SKIs we already trust are recovered. An unknown SKI reporting a conflict is not ours to
// re-register, and doing so would amount to trusting a peer nobody asked for.
func (s *Stack) recoverFromIdentifierConflict(ski string) {
	s.pairing.mu.Lock()
	if s.pairing.conflictRecoveries == nil {
		s.pairing.conflictRecoveries = map[string]time.Time{}
	}
	if last, seen := s.pairing.conflictRecoveries[ski]; seen && time.Since(last) < conflictRecoveryCooldown {
		s.pairing.mu.Unlock()
		return
	}
	s.pairing.conflictRecoveries[ski] = time.Now()
	s.pairing.mu.Unlock()

	trusted := false
	for _, p := range s.Peers() {
		if strings.EqualFold(p.SKI, ski) {
			trusted = true
			break
		}
	}
	if !trusted && !s.isRegistered(ski) {
		log.Printf("ship: %s reported an identifier conflict but is not a trusted peer; ignoring", ski)
		return
	}

	log.Printf("ship: %s presented a certificate that does not match the cached one (device most likely re-imaged or re-commissioned); clearing the cached certificate and re-registering", ski)
	s.Untrust(ski)
	// Give the hub a moment to finish tearing the old entry down before re-registering, so the
	// new registration is not merged back into the entry being removed.
	time.Sleep(500 * time.Millisecond)
	s.Trust(ski)
	s.events.Publish("lifecycle", stackID, "certificate_rotated", ski, map[string]any{
		"detail": "cached certificate no longer matched the peer; re-registered so the new one is accepted",
	})
}

// isRegistered reports whether the SKI is one we have called Trust() on, including peers that
// are registered but not currently connected -- which is exactly the case during a conflict,
// since the connection is being refused. The stale cached entry causing the conflict is itself
// what makes this lookup succeed.
func (s *Stack) isRegistered(ski string) bool {
	return s.service.RemoteServiceFor(shipapi.ServiceIdentity{SKI: ski}) != nil
}

// LastDialError returns the most recent dial failure seen for a SKI, if any.
func (s *Stack) LastDialError(ski string) string {
	s.pairing.mu.Lock()
	defer s.pairing.mu.Unlock()
	return s.pairing.dialErrors[strings.ToLower(ski)]
}

// WatchConnectionAfterTrust polls until the SKI shows up as a connected peer, and if it never
// does, publishes a lifecycle event carrying the real dial error so the dashboard's event
// stream (and the log) explain the failure instead of leaving a bare "trusted" success behind.
// Meant to be fired and forgotten in a goroutine right after Trust().
func (s *Stack) WatchConnectionAfterTrust(ski string) {
	deadline := time.Now().Add(trustWatchTimeout)
	for time.Now().Before(deadline) {
		for _, p := range s.Peers() {
			if strings.EqualFold(p.SKI, ski) {
				return // connected, nothing to report
			}
		}
		time.Sleep(2 * time.Second)
	}

	detail := s.LastDialError(ski)
	if detail == "" {
		detail = "no SHIP connection established and no dial error was reported -- the peer may not be reachable at the advertised address"
	}
	log.Printf("ship: trust of %s did not result in a connection within %s: %s", ski, trustWatchTimeout, detail)
	s.events.Publish("lifecycle", stackID, "trust_connection_timeout", ski, map[string]any{
		"detail":    detail,
		"timeout_s": trustWatchTimeout.Seconds(),
	})
}
