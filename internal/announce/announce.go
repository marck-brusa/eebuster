// Package announce publishes this testbench's own _ship._tcp mDNS record with an explicitly
// chosen address list, and filters the addresses of discovered peers on the way in.
//
// It exists because ship-go's ZeroconfProvider announces via zeroconf.Register, which offers no
// control over the published A/AAAA records: it takes every address of every interface it is
// given. See internal/netfilter's package comment for the real-hardware failure that causes.
// zeroconf.RegisterProxy accepts an explicit address list instead, so this provider uses that
// and delegates everything else -- browsing, resolving, the reconnect callback -- to a wrapped
// real provider. Nothing about SHIP, SPINE, or discovery semantics changes; only the contents of
// our A/AAAA records and the ordering of a peer's.
package announce

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"

	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/zeroconf/v2"

	"github.com/marck-brusa/eebuster/internal/netfilter"
)

// announceTTL matches ship-go's DefaultZeroconfFactory so record lifetime behaviour is
// unchanged by routing the announcement through this package.
const announceTTL = 120

// Provider implements shipapi.MdnsProviderInterface by announcing through
// zeroconf.RegisterProxy and delegating discovery to browse.
type Provider struct {
	browse   shipapi.MdnsProviderInterface
	ifaces   []net.Interface
	rules    netfilter.Rules
	override []net.IP
	hostname string

	mu       sync.Mutex
	counter  int
	servers  map[string]*zeroconf.Server
	lastScan Diagnostics
	// advertiseRegister, when set, forces the `register` TXT entry to this value regardless of
	// what the stack passed. See SetAdvertiseRegister.
	advertiseRegister *bool
}

var _ shipapi.MdnsProviderInterface = (*Provider)(nil)

// Diagnostics is the last address decision, surfaced by GET /api/v1/diagnostics/network so the
// exact published record set is visible without a packet capture -- the failure this package
// fixes was invisible from our side and only showed up in the peer's log.
type Diagnostics struct {
	Announced  []netfilter.Addr      `json:"announced"`
	Rejected   []netfilter.Rejection `json:"rejected"`
	Hostname   string                `json:"hostname"`
	Overridden bool                  `json:"overridden"`
	FilterOff  bool                  `json:"filter_disabled"`
}

// New wraps browse. ifaces is the interface set to consider (empty means every interface);
// override, when non-empty, is published verbatim and skips detection entirely.
//
// hostLabel is the single DNS label our SRV records point at, "" meaning the machine's own
// hostname. Every announcer in this process must pass a *distinct* label, because the A/AAAA
// records of a shared hostname merge on the wire: two announcers publishing "myhost.local."
// produce one record set containing the union of both address lists, so one unfiltered announcer
// silently reintroduces the addresses another one carefully filtered out. That is not
// hypothetical -- it is exactly what the simulated devices did to the main stack, and it was
// invisible from our own logs because each announcer was individually correct.
func New(browse shipapi.MdnsProviderInterface, ifaces []net.Interface, rules netfilter.Rules, override []net.IP, hostLabel string) *Provider {
	if hostLabel == "" {
		hostLabel = localHostname()
	}
	return &Provider{
		browse:   browse,
		ifaces:   ifaces,
		rules:    rules,
		override: override,
		hostname: sanitizeLabel(hostLabel),
		servers:  map[string]*zeroconf.Server{},
	}
}

// LocalHostname exposes this machine's hostname label so callers can derive distinct labels from
// it (see New's hostLabel).
func LocalHostname() string { return localHostname() }

// HostLabel is the recommended hostLabel for New: the machine name plus a short prefix of the
// announcer's own SKI. Every announcer therefore gets a unique label, which matters in two ways
// that both showed up on a real network:
//
//   - within one process, the main stack and each simulated device announce separately, and a
//     shared label merges their address records (see New);
//   - across machines, a Windows host and the WSL2 instance running on it report the *same*
//     os.Hostname(), so two testbenches on genuinely different hosts and different subnets would
//     publish one merged record set -- each handing peers the other's unreachable addresses.
//     Observed live: one machine name announcing both a 192.168.x and a 10.11.x address.
//
// A SKI is derived from the keypair, so this is stable across restarts and unique per identity.
func HostLabel(ski string) string {
	const skiPrefixLen = 12
	if len(ski) > skiPrefixLen {
		ski = ski[:skiPrefixLen]
	}
	if ski == "" {
		return localHostname()
	}
	return localHostname() + "-" + ski
}

// localHostname reduces os.Hostname() to a single DNS label. zeroconf appends ".local." itself,
// so a fully-qualified corporate hostname would otherwise be published as
// "host.corp.example.com.local." -- valid but nonsense. Falls back to a fixed label rather than
// failing, since a missing hostname must not stop us announcing.
func localHostname() string {
	name, err := os.Hostname()
	if err != nil || strings.TrimSpace(name) == "" {
		return "eebus-testbench"
	}
	name, _, _ = strings.Cut(name, ".")
	return sanitizeLabel(name)
}

// sanitizeLabel reduces s to characters legal in a single DNS label.
func sanitizeLabel(s string) string {
	clean := strings.Trim(strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-':
			return r
		default:
			return '-'
		}
	}, s), "-")
	if clean == "" {
		return "eebus-testbench"
	}
	if len(clean) > 63 { // DNS label limit
		clean = strings.TrimRight(clean[:63], "-")
	}
	return clean
}

// addresses resolves what to publish, and records the decision for Diagnostics.
//
// The fallback matters: if filtering leaves nothing at all (a host whose only address is an
// IPv6 ULA, say), publishing an unfiltered set is strictly better than publishing no A/AAAA
// record, which zeroconf would reject outright and leave us undiscoverable. The filter is here
// to improve a working announcement, never to prevent one.
func (p *Provider) addresses() ([]string, []net.Interface, error) {
	p.mu.Lock()
	ifaces := p.ifaces // may be replaced by UpdateInterfaces while we run
	p.mu.Unlock()

	if len(p.override) > 0 {
		ips := make([]string, 0, len(p.override))
		kept := make([]netfilter.Addr, 0, len(p.override))
		for _, ip := range p.override {
			ips = append(ips, ip.String())
			kept = append(kept, netfilter.Addr{IP: ip, Interface: "(configured)"})
		}
		p.mu.Lock()
		p.lastScan = Diagnostics{Announced: kept, Hostname: p.hostname, Overridden: true}
		p.mu.Unlock()
		return ips, ifaces, nil
	}

	kept, rejected := netfilter.Local(ifaces, p.rules)
	filterOff := p.rules == netfilter.PermissiveRules()
	if len(kept) == 0 {
		kept, _ = netfilter.Local(ifaces, netfilter.PermissiveRules())
		if len(kept) == 0 {
			return nil, nil, fmt.Errorf("no usable local IP address found on any interface")
		}
		log.Printf("mdns: every local address was filtered out, falling back to announcing all %d unfiltered -- see the reasons above", len(kept))
		rejected = nil
	}

	p.mu.Lock()
	p.lastScan = Diagnostics{Announced: kept, Rejected: rejected, Hostname: p.hostname, FilterOff: filterOff}
	p.mu.Unlock()

	return netfilter.Strings(kept), netfilter.Interfaces(kept, ifaces), nil
}

// UpdateInterfaces is ship-go's optional mdnsProviderInterfaceUpdater hook (mdns.go), called
// when its interface refresh notices the set changed -- a laptop joining a different Wi-Fi
// network, a cable going in. Keeping our list current matters because ship-go then withdraws and
// re-announces the service, and our re-announcement re-detects addresses from these interfaces.
// A nil list is ship-go's "unspecified", meaning every interface, which is what we want too.
func (p *Provider) UpdateInterfaces(ifaces []net.Interface, _ []int32) {
	p.mu.Lock()
	p.ifaces = ifaces
	p.mu.Unlock()
}

// Diagnostics returns the most recent address decision.
func (p *Provider) Diagnostics() Diagnostics {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastScan
}

// LogDecision prints the published address set and every rejection with its reason. Called once
// at startup: this is the single most useful thing in the log when a peer cannot reach us, and
// it is what turns "pairing just fails" into a one-line diagnosis.
func (p *Provider) LogDecision(shipPort int) {
	ips, ifaces, err := p.addresses()
	if err != nil {
		log.Printf("mdns: cannot determine any address to announce: %v", err)
		return
	}
	d := p.Diagnostics()

	source := "auto-detected"
	if d.Overridden {
		source = "from network.announce_addresses"
	} else if d.FilterOff {
		source = "unfiltered (network.announce_filter: false)"
	}
	log.Printf("mdns: announcing %s.local. on SHIP port %d with %d address(es), %s:", p.hostname, shipPort, len(ips), source)
	for _, a := range d.Announced {
		log.Printf("mdns:   + %s (%s)", a.IP, a.Interface)
	}
	for _, r := range d.Rejected {
		where := r.Interface
		if where == "" {
			where = "?"
		}
		log.Printf("mdns:   - %s (%s) skipped: %s", r.IP, where, r.Reason)
	}
	if len(ifaces) > 0 {
		names := make([]string, 0, len(ifaces))
		for _, i := range ifaces {
			names = append(names, i.Name)
		}
		log.Printf("mdns: multicast bound to interface(s): %s", strings.Join(names, ", "))
	}
	log.Printf("mdns: a peer dials those addresses in order and waits for a full TCP timeout on each dead one, so fewer is faster")
}

// Start delegates discovery to the wrapped provider, interposing on the resolve callback to
// filter each discovered peer's addresses before ship-go's hub walks them. Same rules, other
// direction: the hub also dials serially, so an unreachable ULA in a peer's announcement costs
// us a connect timeout exactly as ours cost the device one.
func (p *Provider) Start(pairingMode shipapi.PairingMode, autoReconnect bool, cb shipapi.MdnsResolveCB) bool {
	filtered := func(elements map[string]string, name, host, serviceType string, addresses []net.IP, port int, remove bool) {
		if !remove && len(addresses) > 0 {
			kept, rejected := netfilter.Peer(addresses, p.rules)
			for _, r := range rejected {
				log.Printf("mdns: ignoring %s advertised by %s: %s", r.IP, name, r.Reason)
			}
			addresses = kept
		}
		cb(elements, name, host, serviceType, addresses, port, remove)
	}
	return p.browse.Start(pairingMode, autoReconnect, filtered)
}

// AnnounceService publishes one service instance via RegisterProxy with our chosen addresses.
// Mirrors ZeroconfProvider.AnnounceService's contract: a monotonic instance ID string, one
// dedicated server per instance so instances can be withdrawn independently.
// SetAdvertiseRegister forces the `register` entry of every TXT record we publish, overriding
// what the stack supplies.
//
// This exists because upstream derives `register` from the same flag that decides whether an
// incoming pairing request is accepted without a human decision. Those are different questions:
// a testbench that waits for an operator to approve is still available for pairing, and that is
// exactly what `register` means (SHIP 7.3.2). Tying them together forces a choice between
// testing the approval dialogue and appearing in the device's pairing list at all -- devices
// commonly list only peers advertising register=true.
//
// Call with nil to leave the stack's own value alone.
func (p *Provider) SetAdvertiseRegister(value bool) {
	p.mu.Lock()
	p.advertiseRegister = &value
	p.mu.Unlock()
}

// applyRegisterOverride rewrites the register entry in a TXT record, appending it when the stack
// did not supply one. Order is otherwise preserved: the TXT list is parsed by every peer on the
// network and reordering it for no reason invites trouble.
func applyRegisterOverride(txt []string, override *bool) []string {
	if override == nil {
		return txt
	}
	want := "register=false"
	if *override {
		want = "register=true"
	}
	out := make([]string, 0, len(txt)+1)
	replaced := false
	for _, entry := range txt {
		if strings.HasPrefix(entry, "register=") {
			out = append(out, want)
			replaced = true
			continue
		}
		out = append(out, entry)
	}
	if !replaced {
		out = append(out, want)
	}
	return out
}

func (p *Provider) AnnounceService(serviceType, serviceName string, port int, txt []string) (string, error) {
	ips, ifaces, err := p.addresses()
	if err != nil {
		return "", fmt.Errorf("announcing %s: %w", serviceType, err)
	}

	p.mu.Lock()
	override := p.advertiseRegister
	p.mu.Unlock()
	txt = applyRegisterOverride(txt, override)

	server, err := zeroconf.RegisterProxy(
		serviceName, serviceType, "local.", port, p.hostname, ips, txt, ifaces, zeroconf.TTL(announceTTL),
	)
	if err != nil {
		return "", fmt.Errorf("announcing %s as %q: %w", serviceType, serviceName, err)
	}

	p.mu.Lock()
	p.counter++
	instanceID := strconv.Itoa(p.counter)
	p.servers[instanceID] = server
	p.mu.Unlock()

	log.Printf("mdns: announced %s instance %q on port %d at %s", serviceType, serviceName, port, strings.Join(ips, ", "))
	return instanceID, nil
}

func (p *Provider) UnannounceService(instanceID string) error {
	p.mu.Lock()
	server, ok := p.servers[instanceID]
	if ok {
		delete(p.servers, instanceID)
	}
	p.mu.Unlock()

	if !ok {
		return shipapi.ErrPairingNotActive
	}
	server.Shutdown()
	return nil
}

func (p *Provider) Shutdown() {
	p.mu.Lock()
	servers := make([]*zeroconf.Server, 0, len(p.servers))
	for id, s := range p.servers {
		servers = append(servers, s)
		delete(p.servers, id)
	}
	p.mu.Unlock()

	for _, s := range servers {
		s.Shutdown()
	}
	p.browse.Shutdown()
}
