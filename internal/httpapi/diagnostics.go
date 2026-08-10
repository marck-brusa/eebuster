package httpapi

import (
	"net"
	"net/http"
	"runtime"
	"strconv"
	"strings"

	"github.com/marck-brusa/eebuster/internal/eebusgo"
	"github.com/marck-brusa/eebuster/internal/netfilter"
)

// networkDiagnostics answers the single hardest question this tool has to answer: "the device
// can see me, so why can't it connect?"
//
// Both halves of that failure are invisible from this side. The addresses we publish live in our
// own mDNS records, which nothing here ever prints; and inbound reachability cannot be probed
// from inside the host at all, because binding the SHIP port succeeds whether or not the
// firewall drops packets arriving on it. So this reports our published address decision with a
// reason for every rejection, plus the exact firewall command for this platform.
type networkDiagnostics struct {
	ShipPort int    `json:"ship_port"`
	Hostname string `json:"mdns_hostname"`
	Platform string `json:"platform"`

	// Announced is what a peer will find in our A/AAAA records, in the order it will dial them.
	Announced []netfilter.Addr `json:"announced"`
	// Rejected is every local address deliberately left out, each with the reason why.
	Rejected []netfilter.Rejection `json:"rejected"`
	// Overridden is true when network.announce_addresses / -announce-address is in force.
	Overridden bool `json:"announce_overridden"`
	// FilterDisabled is true under network.announce_filter: false (upstream zeroconf behaviour).
	FilterDisabled bool `json:"announce_filter_disabled"`

	// Interfaces is every interface on this host with its addresses, so a multi-homed or
	// WSL/Hyper-V machine can be understood at a glance without leaving the dashboard.
	Interfaces []interfaceInfo `json:"interfaces"`

	// Discovery reports whether inbound mDNS reaches this host at all -- the difference between
	// "no devices here" and "multicast never arrives", which look identical otherwise.
	Discovery eebusgo.DiscoveryHealth `json:"discovery"`

	// Warnings are plain-language problems detected in the above.
	Warnings []string `json:"warnings"`
	// FirewallCommands allow inbound TCP on the SHIP port, most-likely first.
	FirewallCommands []string `json:"firewall_commands"`
}

type interfaceInfo struct {
	Name      string   `json:"name"`
	Up        bool     `json:"up"`
	Virtual   bool     `json:"virtual"`
	Loopback  bool     `json:"loopback"`
	Addresses []string `json:"addresses"`
}

func (s *Server) handleNetworkDiagnostics(w http.ResponseWriter, r *http.Request) {
	d := s.stack.AnnounceDiagnostics()
	port := s.stack.ShipPort()

	out := networkDiagnostics{
		ShipPort:         port,
		Hostname:         d.Hostname,
		Platform:         runtime.GOOS + "/" + runtime.GOARCH,
		Announced:        d.Announced,
		Rejected:         d.Rejected,
		Overridden:       d.Overridden,
		FilterDisabled:   d.FilterOff,
		Interfaces:       collectInterfaces(),
		Discovery:        s.stack.DiscoveryHealth(),
		FirewallCommands: inboundFirewallCommands(port),
	}
	// Never nil: ui.html iterates these directly, and a JSON null would throw in the browser
	// the same way it broke the Python example scripts.
	if out.Announced == nil {
		out.Announced = []netfilter.Addr{}
	}
	if out.Rejected == nil {
		out.Rejected = []netfilter.Rejection{}
	}
	out.Warnings = diagnoseWarnings(out)

	writeJSON(w, http.StatusOK, out)
}

func collectInterfaces() []interfaceInfo {
	ifaces, err := net.Interfaces()
	if err != nil {
		return []interfaceInfo{}
	}
	out := make([]interfaceInfo, 0, len(ifaces))
	for _, iface := range ifaces {
		info := interfaceInfo{
			Name:      iface.Name,
			Up:        iface.Flags&net.FlagUp != 0,
			Virtual:   netfilter.IsVirtualInterface(iface.Name),
			Loopback:  iface.Flags&net.FlagLoopback != 0,
			Addresses: []string{},
		}
		addrs, err := iface.Addrs()
		if err == nil {
			for _, a := range addrs {
				if ipnet, ok := a.(*net.IPNet); ok {
					info.Addresses = append(info.Addresses, ipnet.IP.String())
				}
			}
		}
		out = append(out, info)
	}
	return out
}

// diagnoseWarnings turns the raw address decision into the sentences someone actually needs.
// Ordered by how likely each is to be the real problem.
func diagnoseWarnings(d networkDiagnostics) []string {
	warnings := []string{}

	switch {
	case len(d.Announced) == 0:
		warnings = append(warnings, "No address is being announced, so no peer can connect to this host. Set network.announce_addresses to the IP the device under test can reach.")
	case len(d.Announced) > 3:
		warnings = append(warnings, strconv.Itoa(len(d.Announced))+" addresses are announced. A SHIP peer dials them serially and waits for a full TCP timeout on each unreachable one, which can delay pairing by a minute or more. Consider setting network.announce_addresses to just the reachable IP.")
	}

	if d.FilterDisabled {
		warnings = append(warnings, "network.announce_filter is off, so unreachable addresses (IPv6 link-local and unique-local, virtual adapters) are being published. This reproduces the upstream behaviour that makes peers time out.")
	}

	// The receive half of discovery. Distinct from every warning above, which all concern what
	// we publish: this one fires when nothing at all is arriving, which no amount of correct
	// announcing can fix.
	if d.Discovery.Warning != "" {
		warnings = append(warnings, d.Discovery.Warning)
	}

	// The inbound half. Always worth saying on Windows, where the default deny on a Public
	// network profile is the single most common cause of "trusted, then never connects".
	if strings.HasPrefix(d.Platform, "windows/") {
		warnings = append(warnings, "Windows Firewall blocks inbound TCP by default on networks profiled as Public, which includes most device access points. If a peer discovers this testbench but its connection times out, add the inbound rule below.")
	}

	var virtualUp int
	for _, iface := range d.Interfaces {
		if iface.Virtual && iface.Up && len(iface.Addresses) > 0 {
			virtualUp++
		}
	}
	if virtualUp > 0 {
		warnings = append(warnings, strconv.Itoa(virtualUp)+" virtual or NAT adapter(s) are up (WSL, Hyper-V, Docker Desktop, VPN). Their addresses are excluded from the announcement because no peer on the real network can route to them.")
	}

	return warnings
}

// inboundFirewallCommands mirrors cmd/eebus-testbench/firewall.go's list. Duplicated rather than
// shared because internal/ may not import cmd/, and one short platform switch is a smaller cost
// than a package existing only to hold it.
func inboundFirewallCommands(port int) []string {
	p := strconv.Itoa(port)
	switch runtime.GOOS {
	case "windows":
		return []string{`netsh advfirewall firewall add rule name="EEBUS testbench SHIP" dir=in action=allow protocol=TCP localport=` + p}
	case "darwin":
		return []string{"sudo /usr/libexec/ApplicationFirewall/socketfilterfw --unblockapp <path to eebus-testbench>"}
	default:
		return []string{
			"sudo ufw allow " + p + "/tcp",
			"sudo firewall-cmd --add-port=" + p + "/tcp",
			"sudo nft insert rule inet filter INPUT tcp dport " + p + " counter accept",
			"sudo iptables -I INPUT -p tcp --dport " + p + " -j ACCEPT",
		}
	}
}
