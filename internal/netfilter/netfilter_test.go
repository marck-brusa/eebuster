package netfilter

import (
	"net"
	"testing"
)

// theEightAddresses is verbatim what a Windows host running WSL2 + Hyper-V announced, taken
// from the real device-side SHIP log that motivated this package. The device tried all eight
// serially, one full TCP connect timeout each. Only 192.168.9.100 was reachable.
var theEightAddresses = []string{
	"fc00:192:168:9:2d4c:bbd:6466:3aea",
	"fc00:192:168:9:1e86:806b:c7b6:917c",
	"192.168.9.100",
	"172.20.160.1",
	"172.22.112.1",
	"fe80::dc81:764b:9969:64a0",
	"fe80::e14f:160b:eca:5009",
	"fe80::38eb:4ff4:ee28:846b",
}

// TestPeerDropsEverythingUnreachable is the regression test for the actual bug: given that
// announcement, only the one usable address should survive. 172.20/172.22 are kept here on
// purpose -- Peer() has no interface context, so it cannot know they are virtual adapters, and
// the announce side (Local) is what removes them.
func TestPeerDropsEverythingUnreachable(t *testing.T) {
	var addrs []net.IP
	for _, s := range theEightAddresses {
		ip := net.ParseIP(s)
		if ip == nil {
			t.Fatalf("test data %q is not a valid IP", s)
		}
		addrs = append(addrs, ip)
	}

	kept, rejected := Peer(addrs, DefaultRules())

	want := map[string]bool{"192.168.9.100": true, "172.20.160.1": true, "172.22.112.1": true}
	if len(kept) != len(want) {
		t.Fatalf("kept %v, want exactly the %d IPv4 addresses", kept, len(want))
	}
	for _, ip := range kept {
		if !want[ip.String()] {
			t.Errorf("kept unreachable address %s", ip)
		}
	}
	if len(rejected) != 5 {
		t.Fatalf("rejected %d addresses, want 5 (2 ULA + 3 link-local); got %v", len(rejected), rejected)
	}
	for _, r := range rejected {
		if r.Reason == "" {
			t.Errorf("rejection of %s carries no reason", r.IP)
		}
	}
}

func TestClassify(t *testing.T) {
	for _, tc := range []struct {
		ip     string
		rules  Rules
		reject bool
		name   string
	}{
		{"192.168.9.100", DefaultRules(), false, "private IPv4 is the normal case"},
		{"10.0.0.14", DefaultRules(), false, "another private IPv4 range"},
		{"203.0.113.5", DefaultRules(), false, "public IPv4"},
		{"169.254.10.1", DefaultRules(), true, "IPv4 link-local means no DHCP lease"},
		{"127.0.0.1", DefaultRules(), true, "loopback"},
		{"::1", DefaultRules(), true, "IPv6 loopback"},
		{"fe80::1", DefaultRules(), true, "IPv6 link-local has no scope zone in a DNS record"},
		{"fc00:192:168:9::1", DefaultRules(), true, "IPv6 ULA"},
		{"fd12:3456::1", DefaultRules(), true, "the other half of fc00::/7"},
		{"2001:db8::1", DefaultRules(), false, "global IPv6 is genuinely reachable"},
		{"2001:db8::1", Rules{}, true, "global IPv6 dropped when IPv6 is off"},
		{"fc00::1", Rules{AllowIPv6: true, AllowULA: true}, false, "ULA kept when explicitly allowed"},
		{"fe80::1", Rules{AllowIPv6: true, AllowLinkLocal: true}, false, "link-local kept when explicitly allowed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(net.ParseIP(tc.ip), tc.rules)
			if (got != "") != tc.reject {
				t.Errorf("classify(%s) = %q, want rejected=%v", tc.ip, got, tc.reject)
			}
		})
	}
}

func TestPermissiveRulesKeepEverything(t *testing.T) {
	var addrs []net.IP
	for _, s := range theEightAddresses {
		addrs = append(addrs, net.ParseIP(s))
	}
	kept, rejected := Peer(addrs, PermissiveRules())
	if len(kept) != len(theEightAddresses) {
		t.Errorf("kept %d of %d, want all with the filter disabled", len(kept), len(theEightAddresses))
	}
	if len(rejected) != 0 {
		t.Errorf("rejected %v with the filter disabled", rejected)
	}
}

// TestPeerNeverReturnsEmpty guards the deliberate asymmetry with Local: silently deleting every
// address of a peer would make a reachable device undiscoverable, which is worse than the
// problem this package solves.
func TestPeerNeverReturnsEmpty(t *testing.T) {
	onlyBad := []net.IP{net.ParseIP("fe80::1"), net.ParseIP("fc00::1")}
	kept, rejected := Peer(onlyBad, DefaultRules())
	if len(kept) != 2 {
		t.Errorf("kept %v, want the originals back when filtering would empty the list", kept)
	}
	if rejected != nil {
		t.Errorf("rejected %v, want none reported on the fallback path", rejected)
	}
}

func TestPeerOrdersIPv4First(t *testing.T) {
	addrs := []net.IP{net.ParseIP("2001:db8::1"), net.ParseIP("192.168.9.100")}
	kept, _ := Peer(addrs, DefaultRules())
	if len(kept) != 2 || kept[0].To4() == nil {
		t.Fatalf("kept = %v, want the IPv4 address first", kept)
	}
}

func TestIsVirtualInterface(t *testing.T) {
	virtual := []string{
		"vEthernet (WSL)", "vEthernet (WSL (Hyper-V firewall))", "vEthernet (Default Switch)",
		"veth1a2b3c", "docker0", "br-1a2b3c4d", "virbr0", "tailscale0", "wg0",
		"VMware Network Adapter VMnet8", "VirtualBox Host-Only Network", "utun3", "awdl0",
	}
	for _, name := range virtual {
		if !IsVirtualInterface(name) {
			t.Errorf("IsVirtualInterface(%q) = false, want true", name)
		}
	}

	// Over-filtering removes the one address that works, so these must never match.
	physical := []string{"Wi-Fi", "Ethernet", "Ethernet 2", "eth0", "wlan0", "enp3s0", "wlp2s0", "br0", "eno1", "mlan0", "uap1"}
	for _, name := range physical {
		if IsVirtualInterface(name) {
			t.Errorf("IsVirtualInterface(%q) = true, want false -- this would drop a real address", name)
		}
	}
}

// TestLocalOnThisHost cannot assert specific addresses (it runs wherever it runs), but it does
// assert the invariants that must hold everywhere.
func TestLocalOnThisHost(t *testing.T) {
	kept, rejected := Local(nil, DefaultRules())
	for _, a := range kept {
		if a.IP.IsLoopback() {
			t.Errorf("kept loopback %s", a.IP)
		}
		if a.IP.To4() == nil && (a.IP.IsLinkLocalUnicast() || a.IP.IsPrivate()) {
			t.Errorf("kept unreachable IPv6 %s", a.IP)
		}
		if a.Interface == "" {
			t.Errorf("kept %s without an interface name", a.IP)
		}
	}
	for _, r := range rejected {
		if r.Reason == "" {
			t.Errorf("rejection of %s carries no reason", r.IP)
		}
	}
	t.Logf("this host announces %d address(es), dropped %d", len(kept), len(rejected))
}

func TestInterfacesDedupes(t *testing.T) {
	from := []net.Interface{{Name: "Wi-Fi"}, {Name: "vEthernet (WSL)"}, {Name: "Ethernet"}}
	addrs := []Addr{
		{IP: net.ParseIP("192.168.9.100"), Interface: "Wi-Fi"},
		{IP: net.ParseIP("2001:db8::1"), Interface: "Wi-Fi"},
	}
	got := Interfaces(addrs, from)
	if len(got) != 1 || got[0].Name != "Wi-Fi" {
		t.Errorf("Interfaces() = %v, want just Wi-Fi", got)
	}
}
