// Package netfilter decides which IP addresses are worth putting on the wire: which of our own
// local addresses to publish in an mDNS announcement, and which of a discovered peer's
// addresses are worth dialling.
//
// This exists because of a specific, reproducible failure against real hardware. zeroconf's
// Register() collects *every* IPv4 and every "global unicast" IPv6 address from *every*
// multicast-capable interface and flattens them into one shared A/AAAA record set (see
// zeroconf/server.go: addrsForInterface). On a Windows host running WSL2 and/or Hyper-V that
// announcement contained eight addresses, only one of which any peer on the LAN could ever
// reach:
//
//	fc00:...:2d4c:bbd:6466:3aea   IPv6 ULA          -> 10s connect timeout
//	fc00:...:1e86:806b:c7b6:917c  IPv6 ULA (temp)   -> 10s connect timeout
//	192.168.9.100                 the real address  -> the only useful entry
//	172.20.160.1                  vEthernet (WSL)   -> "Network is unreachable"
//	172.22.112.1                  vEthernet (Hyper-V) -> "Network is unreachable"
//	fe80::dc81:764b:9969:64a0     link-local        -> peer retries under *its* scope, fails
//	fe80::e14f:160b:eca:5009      link-local        -> same
//	fe80::38eb:4ff4:ee28:846b     link-local        -> same
//
// A SHIP peer walks that list serially and waits for a full TCP connect timeout on each dead
// entry, so seven junk addresses cost ~70 seconds per attempt and bury the one real address in
// the middle. Publishing fewer, better addresses is therefore not cosmetic: it is the
// difference between pairing in milliseconds and pairing never completing.
//
// Every rejection carries a human-readable reason, because the whole point is that the next
// person to hit this can see *why* an address was dropped instead of wondering where it went.
package netfilter

import (
	"fmt"
	"net"
	"sort"
	"strings"
)

// Rules is the filtering policy. The zero value is deliberately the most permissive setting
// for IPv6 being off; use DefaultRules for the intended defaults.
type Rules struct {
	// AllowIPv6 publishes IPv6 addresses at all. Default true: a genuine global IPv6 address
	// is perfectly reachable, and the addresses that actually caused trouble (ULA, link-local)
	// are excluded by their own rules below.
	AllowIPv6 bool
	// AllowULA keeps IPv6 unique-local addresses (fc00::/7). Default false.
	AllowULA bool
	// AllowLinkLocal keeps IPv6 fe80::/10 and IPv4 169.254.0.0/16. Default false.
	AllowLinkLocal bool
	// AllowVirtual keeps addresses belonging to virtual/NAT host adapters. Default false.
	AllowVirtual bool
}

// DefaultRules is the policy that fixes the failure documented in the package comment: real
// IPv4 and real global IPv6 only.
func DefaultRules() Rules {
	return Rules{AllowIPv6: true}
}

// PermissiveRules reproduces upstream zeroconf behaviour exactly -- everything published,
// nothing filtered. Selected by network.announce_filter: false, so the filter can be taken out
// of the picture when diagnosing whether it is itself the problem.
func PermissiveRules() Rules {
	return Rules{AllowIPv6: true, AllowULA: true, AllowLinkLocal: true, AllowVirtual: true}
}

// Addr is one address that survived filtering, tagged with the interface it came from.
type Addr struct {
	IP        net.IP `json:"ip"`
	Interface string `json:"interface"`
}

// Rejection is one address that was dropped, with the reason why.
type Rejection struct {
	IP        net.IP `json:"ip"`
	Interface string `json:"interface,omitempty"`
	Reason    string `json:"reason"`
}

// virtualPrefixes are interface-name prefixes (matched case-insensitively) whose addresses a
// peer elsewhere on the LAN can never route to. "veth" covers both Windows' Hyper-V/WSL2/Docker
// Desktop host adapters, which Go reports by their friendly name "vEthernet (WSL)" /
// "vEthernet (Default Switch)", and Linux container veth pairs.
//
// Deliberately *not* listed: "eth" (WSL2's own eth0 is the real interface), "br0" and other
// plain bridge names (frequently a host's actual LAN bridge), and "en"/"wl" of any kind.
// Over-filtering here silently removes the one address that works, which is a worse failure
// than the one this package fixes -- when in doubt, keep the address.
var virtualPrefixes = []string{
	"veth",                   // Windows vEthernet (WSL/Hyper-V/Docker Desktop), Linux veth pairs
	"docker",                 // Linux docker0
	"br-",                    // Linux docker user-defined bridges (br-<id>), not plain br0
	"virbr",                  // libvirt
	"vmware network adapter", // VMware host-only/NAT
	"virtualbox host-only",   // VirtualBox
	"tun", "tap",             // OpenVPN and friends
	"tailscale", "zt", "wg", // Tailscale, ZeroTier, WireGuard
	"utun", "awdl", "llw", "bridge", // macOS tunnels, AirDrop, Thunderbolt bridge
}

// IsVirtualInterface reports whether name looks like a virtual or NAT host adapter.
func IsVirtualInterface(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, prefix := range virtualPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

// classify applies the rules to a single address, returning the rejection reason or "" to keep
// it. Kept separate from the interface walk so both directions (our own addresses, a peer's
// addresses) share exactly one definition of "reachable".
func classify(ip net.IP, r Rules) string {
	switch {
	case ip.IsLoopback():
		return "loopback address, not reachable from another host"
	case ip.IsUnspecified():
		return "unspecified address"
	}

	if ip.To4() != nil {
		if ip.IsLinkLocalUnicast() && !r.AllowLinkLocal {
			return "IPv4 link-local (169.254.0.0/16): means DHCP never handed out a lease, so nothing can route here"
		}
		return ""
	}

	if !r.AllowIPv6 {
		return "IPv6 disabled (network.announce_ipv6: false)"
	}
	switch {
	case ip.IsLinkLocalUnicast() && !r.AllowLinkLocal:
		// The decisive one. A DNS AAAA record carries no scope zone, so a peer that reads
		// fe80::… off the wire has to guess an interface and will append its own -- the real
		// device log showed it retrying our address as fe80::dc81:764b:9969:64a0%uap1, its
		// own AP interface, which can never work.
		return "IPv6 link-local (fe80::/10): a DNS record carries no scope zone, so a peer retries it against its own interface and always fails"
	case ip.IsPrivate() && !r.AllowULA:
		// net.IP.IsPrivate() is fc00::/7 for IPv6. Go's IsGlobalUnicast() returns true for
		// these, which is precisely why upstream zeroconf publishes them.
		return "IPv6 unique-local (fc00::/7): only routable inside one administrative domain, and timed out from real hardware"
	case ip.IsInterfaceLocalMulticast() || ip.IsLinkLocalMulticast() || ip.IsMulticast():
		return "multicast address"
	}
	return ""
}

// Local picks the addresses to publish in our own mDNS announcement. ifaces limits the walk;
// an empty slice means every interface on the host. Returns the kept addresses ordered IPv4
// first (a peer dials them in order, and IPv4 is what reliably works against this hardware),
// alongside every rejection and its reason.
//
// Note there is no "kept is empty" fallback here: an empty result is reported honestly so the
// caller can decide, which it does -- see announce.Provider.addresses, which falls back to
// unfiltered rather than announcing nothing at all.
func Local(ifaces []net.Interface, r Rules) (kept []Addr, rejected []Rejection) {
	if len(ifaces) == 0 {
		all, err := net.Interfaces()
		if err != nil {
			return nil, []Rejection{{Reason: fmt.Sprintf("enumerating interfaces failed: %v", err)}}
		}
		ifaces = all
	}

	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 {
			continue // down: no addresses worth reporting, and not an interesting rejection
		}
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			rejected = append(rejected, Rejection{Interface: iface.Name, Reason: fmt.Sprintf("listing addresses failed: %v", err)})
			continue
		}
		virtual := !r.AllowVirtual && IsVirtualInterface(iface.Name)

		for _, addr := range addrs {
			ipnet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if virtual {
				rejected = append(rejected, Rejection{IP: ipnet.IP, Interface: iface.Name,
					Reason: "virtual or NAT host adapter: a peer on the real network has no route to it"})
				continue
			}
			if reason := classify(ipnet.IP, r); reason != "" {
				rejected = append(rejected, Rejection{IP: ipnet.IP, Interface: iface.Name, Reason: reason})
				continue
			}
			kept = append(kept, Addr{IP: ipnet.IP, Interface: iface.Name})
		}
	}

	sort.SliceStable(kept, func(i, j int) bool {
		return (kept[i].IP.To4() != nil) && (kept[j].IP.To4() == nil)
	})
	return kept, rejected
}

// Peer filters a discovered peer's advertised addresses before ship-go's hub walks them. Same
// reachability logic as Local minus the virtual-adapter rule, which is about our interfaces and
// says nothing about a peer's addresses.
//
// Unlike Local this never returns an empty list: if every address is rejected the originals are
// returned unchanged. A peer we cannot rank is still better than a peer we have silently
// deleted, and getting this wrong would make a reachable device undiscoverable.
func Peer(addrs []net.IP, r Rules) (kept []net.IP, rejected []Rejection) {
	for _, ip := range addrs {
		if reason := classify(ip, r); reason != "" {
			rejected = append(rejected, Rejection{IP: ip, Reason: reason})
			continue
		}
		kept = append(kept, ip)
	}
	if len(kept) == 0 {
		return addrs, nil
	}
	sort.SliceStable(kept, func(i, j int) bool {
		return (kept[i].To4() != nil) && (kept[j].To4() == nil)
	})
	return kept, rejected
}

// Strings renders kept addresses for zeroconf.RegisterProxy, which takes []string.
func Strings(addrs []Addr) []string {
	out := make([]string, 0, len(addrs))
	for _, a := range addrs {
		out = append(out, a.IP.String())
	}
	return out
}

// Interfaces is the deduplicated set of interfaces the kept addresses came from, used to bind
// the announcement's multicast sockets to only those interfaces -- so we do not even send mDNS
// traffic out of an adapter whose addresses we just decided not to publish.
func Interfaces(addrs []Addr, from []net.Interface) []net.Interface {
	if len(from) == 0 {
		all, err := net.Interfaces()
		if err != nil {
			return nil
		}
		from = all
	}
	names := map[string]bool{}
	for _, a := range addrs {
		names[a.Interface] = true
	}
	var out []net.Interface
	for _, iface := range from {
		if names[iface.Name] {
			out = append(out, iface)
		}
	}
	return out
}
