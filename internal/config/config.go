// Package config loads config/eebus.yaml. Schema matches src/facade/config/models.py so the
// same config file works for either implementation during the migration.
package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/marck-brusa/eebuster/internal/netfilter"
)

var skiRe = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

type Identity struct {
	VendorCode string `yaml:"vendor_code" json:"vendor_code"`
	Brand      string `yaml:"brand" json:"brand"`
	Model      string `yaml:"model" json:"model"`
	Serial     string `yaml:"serial" json:"serial"`
	Country    string `yaml:"country" json:"country"`
}

type API struct {
	Bind string `yaml:"bind" json:"bind"`
	Port int    `yaml:"port" json:"port"`
}

type Network struct {
	Mode         string `yaml:"mode" json:"mode"` // "static" | "mdns"
	Interface    string `yaml:"interface" json:"interface"`
	MdnsProvider string `yaml:"mdns_provider" json:"mdns_provider"` // "zeroconf" | "avahi" | "all"

	// AnnounceAddresses, when non-empty, is the exact list of IPs published in our own mDNS
	// A/AAAA records, bypassing auto-detection. The escape hatch for a multi-homed host where
	// only you know which address the device under test can actually reach.
	AnnounceAddresses []string `yaml:"announce_addresses" json:"announce_addresses"`
	// AnnounceFilter drops local addresses no peer could reach before publishing them: IPv6
	// link-local and unique-local, IPv4 169.254/16, and the addresses of virtual/NAT host
	// adapters (Hyper-V, WSL2, Docker Desktop, VPN tunnels). Default true.
	//
	// This is not a cosmetic tidy-up. A SHIP peer dials the published addresses serially and
	// waits for a full TCP connect timeout on each dead one, so a Windows host with WSL2
	// installed used to publish eight addresses of which exactly one was reachable, costing the
	// device ~70 seconds per pairing attempt. See internal/netfilter for the captured evidence.
	// Set false to publish everything, exactly as upstream zeroconf does.
	AnnounceFilter bool `yaml:"announce_filter" json:"announce_filter"`
	// AnnounceIPv6 publishes IPv6 addresses at all. Default true -- a genuine global IPv6
	// address is reachable, and the problematic ones (link-local, unique-local) are already
	// excluded by AnnounceFilter. Set false on a network where IPv6 is present but broken.
	AnnounceIPv6 bool `yaml:"announce_ipv6" json:"announce_ipv6"`
}

// AnnounceRules turns the announce_* settings into a netfilter policy.
func (n Network) AnnounceRules() netfilter.Rules {
	if !n.AnnounceFilter {
		return netfilter.PermissiveRules()
	}
	rules := netfilter.DefaultRules()
	rules.AllowIPv6 = n.AnnounceIPv6
	return rules
}

// AnnounceIPs parses announce_addresses, ignoring blanks. Invalid entries are returned as an
// error rather than silently dropped: a typo'd override that quietly falls back to
// auto-detection is the kind of thing that costs an afternoon.
func (n Network) AnnounceIPs() ([]net.IP, error) {
	var out []net.IP
	for _, raw := range n.AnnounceAddresses {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		ip := net.ParseIP(raw)
		if ip == nil {
			return nil, fmt.Errorf("network.announce_addresses: %q is not a valid IP address", raw)
		}
		out = append(out, ip)
	}
	return out, nil
}

type Peer struct {
	Name  string `yaml:"name" json:"name"`
	Label string `yaml:"label" json:"label"`
	SKI   string `yaml:"ski" json:"ski"`
	Host  string `yaml:"host" json:"host"`
	Port  int    `yaml:"port" json:"port"`
	Path  string `yaml:"path" json:"path"`
	Trust string `yaml:"trust" json:"trust"` // "auto" | "manual"
}

func (p Peer) DisplayLabel() string {
	if p.Label != "" {
		return p.Label
	}
	return p.Name
}

// Stack holds the one field every stack entry has (enabled) plus whatever else the stack's
// own YAML block contains, mirroring the Python model's extra="allow".
type Stack struct {
	Enabled bool           `yaml:"enabled" json:"enabled"`
	Extra   map[string]any `yaml:",inline" json:"-"`
}

// SimulatedDevice is one simulated device the simulator boots as its own tiny EEBUS peer:
// its own identity, its own SHIP port, responding to LPC (accepts a consumption limit) and
// MPC (reports power) like a real device would. Type picks the SPINE entity/device type and
// the default baseline if BaselineW is zero -- "keep it simple" per the request that created
// this: one number, drop when limited, no per-phase detail, no other use cases.
type SimulatedDevice struct {
	ID        string  `yaml:"id" json:"id"`
	Type      string  `yaml:"type" json:"type"` // "generic" | "ev" | "heat_pump"
	ShipPort  int     `yaml:"ship_port" json:"ship_port"`
	BaselineW float64 `yaml:"baseline_w" json:"baseline_w"`
}

type Simulator struct {
	Enabled bool              `yaml:"enabled" json:"enabled"`
	Devices []SimulatedDevice `yaml:"devices" json:"devices"`
}

type Config struct {
	Identity Identity `yaml:"identity" json:"identity"`
	API      API      `yaml:"api" json:"api"`
	Network  Network  `yaml:"network" json:"network"`
	// AutoAccept advertises this testbench as commissionable: it sets the `register` flag in our
	// own mDNS TXT record, which is SHIP's "I am available for pairing" signal (SHIP 7.3.2).
	// Devices commonly list *only* peers advertising register=true, so with this off the
	// testbench can be perfectly correct on the wire and still be missing from the device's
	// pairing list, showing a built-in simulated device while hiding the energy manager itself.
	// That is confusing enough that the default is true. Set false only to test how a device
	// behaves towards a peer that declares itself unavailable.
	//
	// Defaulting via a pointer: a plain bool cannot distinguish "absent" from "explicitly
	// false" in YAML, and this default is true.
	AutoAccept *bool `yaml:"auto_accept" json:"auto_accept"`
	// RequireApproval keeps the advertisement above while still stopping each incoming pairing
	// request for a human decision (dashboard, or POST /peers/{ski}/trust and /deny). Use it
	// when the point of the test is the pairing dialogue itself.
	//
	// Upstream ties these together in one setter -- turning off automatic acceptance also clears
	// the advertised `register` flag -- so with only that control, testing manual approval means
	// disappearing from the device's list. Separating them is deliberate: "a human approves" and
	// "not available for pairing" are different statements, and only the first is what anyone
	// actually wants here.
	RequireApproval bool `yaml:"require_approval" json:"require_approval"`
	// LogLevel controls how verbose the EEBUS stack's own logging is:
	// trace|debug|info|error, default debug. Trace dumps every SPINE datagram as raw JSON in
	// both directions, which with a simulated device running is a continuous firehose that
	// buries the few lines explaining a real connection problem -- so it is opt-in.
	LogLevel string `yaml:"log_level" json:"log_level"`
	// TracerURL, when set, adds an "EEBusTracer" link to the dashboard sidebar that opens in
	// a new tab (it is a separate tool with its own UI, and the new tab keeps that visible).
	// The bundled eebustracer binary serves on http://127.0.0.1:8090 with
	// `eebustracer serve --port 8090`; feed it frames via `serve -frame-log` + `import`.
	TracerURL string           `yaml:"tracer_url" json:"tracer_url,omitempty"`
	Peers     []Peer           `yaml:"peers" json:"peers"`
	Stacks    map[string]Stack `yaml:"stacks" json:"stacks"`
	Simulator Simulator        `yaml:"simulator" json:"simulator"`
}

func defaults() Config {
	return Config{
		Identity:  Identity{VendorCode: "LAB", Brand: "EEBUS testbench", Model: "eebus-testbench", Serial: "testbench-01", Country: "DE"},
		API:       API{Bind: "0.0.0.0", Port: 8080},
		Network:   Network{Mode: "static", MdnsProvider: "zeroconf", AnnounceFilter: true, AnnounceIPv6: true},
		Simulator: Simulator{Enabled: true},
		LogLevel:  "debug",
		// Advertise as commissionable by default -- see Config.AutoAccept for why anything else
		// makes the testbench invisible in a device's pairing list.
		AutoAccept: boolPtr(true),
	}
}

func boolPtr(v bool) *bool { return &v }

// AcceptsPairingAutomatically reports whether an incoming pairing request is accepted with no
// human decision. Advertising availability (AutoAccept) and accepting silently are separate:
// RequireApproval keeps the advertisement and stops for a decision.
func (c *Config) AcceptsPairingAutomatically() bool {
	return c.Advertised() && !c.RequireApproval
}

// Advertised reports whether we set the mDNS `register` flag, defaulting to true when the key
// is absent from the file.
func (c *Config) Advertised() bool {
	return c.AutoAccept == nil || *c.AutoAccept
}

// ErrUsingZeroConfigDefaults is returned alongside a valid Config, not as a load failure --
// callers can log it as an informational message ("no config file found, starting in
// zero-config mDNS mode") without treating it as an error.
var ErrUsingZeroConfigDefaults = fmt.Errorf("no config file found; starting with zero-config defaults (real mDNS discovery, no peers required)")

// zeroConfigDefaults is what a plain double-click launch gets when no config file exists at
// all: most people don't want to hand-write a peers: list before they can see anything work,
// and unlike static mode (which has nothing to dial without at least one configured peer),
// mdns mode with interface "*" is valid with zero peers -- it just starts listening for
// whatever it finds.
func zeroConfigDefaults() Config {
	cfg := defaults()
	cfg.Network.Mode = "mdns"
	cfg.Network.Interface = "*"
	cfg.Simulator.Devices = defaultSimulatedDevices()
	return cfg
}

// defaultSimulatedDevices is what --simulator (default on) gets with no simulator.devices:
// list in the config file: one generic device at the 11kW/2kW baseline from the request that
// asked for this ("always at 11kW, then with LPC at 2kW it drops until it's clear").
func defaultSimulatedDevices() []SimulatedDevice {
	return []SimulatedDevice{
		{ID: "sim-load-1", Type: "generic", ShipPort: 4713, BaselineW: 11000},
	}
}

// Load reads and validates path, matching the invariants in
// src/facade/config/models.py: TestbenchConfig. If path doesn't exist at all, returns
// zeroConfigDefaults() and ErrUsingZeroConfigDefaults instead of failing -- a missing file is
// the expected first-run state for someone who just downloaded and double-clicked the
// executable, not a configuration error.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg := zeroConfigDefaults()
		return &cfg, ErrUsingZeroConfigDefaults
	}
	if err != nil {
		return nil, err
	}
	cfg := defaults()
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}

	for i, p := range cfg.Peers {
		if !skiRe.MatchString(p.SKI) {
			return nil, fmt.Errorf("peers[%d] (%s): ski must be exactly 40 hex characters, got %q", i, p.Name, p.SKI)
		}
		cfg.Peers[i].SKI = strings.ToLower(p.SKI)
		if cfg.Peers[i].Path == "" {
			cfg.Peers[i].Path = "/ship/"
		}
		if cfg.Peers[i].Trust == "" {
			cfg.Peers[i].Trust = "auto"
		}
	}

	if cfg.Simulator.Enabled && len(cfg.Simulator.Devices) == 0 {
		cfg.Simulator.Devices = defaultSimulatedDevices()
	}
	// Simulated devices become real static peers at boot (see cmd/eebus-testbench/serve.go),
	// so they count as "something to dial" too -- static mode with zero real peers: but the
	// simulator on is a completely reasonable "just let me see it work" first run.
	hasDialTarget := len(cfg.Peers) > 0 || (cfg.Simulator.Enabled && len(cfg.Simulator.Devices) > 0)
	if cfg.Network.Mode == "static" && !hasDialTarget {
		return nil, fmt.Errorf("network.mode is 'static' but no peers are configured and the simulator is off -- static mode has nothing to dial without at least one entry under peers: or a simulated device")
	}
	if cfg.Network.Mode == "mdns" && strings.TrimSpace(cfg.Network.Interface) == "" {
		return nil, fmt.Errorf(`network.interface is required when network.mode is 'mdns'; use a real interface name, a comma-separated list, or "*"`)
	}
	if _, err := cfg.Network.AnnounceIPs(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// Validate re-checks the invariants Load enforces, matching ConfigStore.validate()'s use for
// POST /api/v1/config/validate and /reload -- returns human-readable problems, empty means
// valid.
func (c *Config) Validate() []string {
	var problems []string
	for i, p := range c.Peers {
		if !skiRe.MatchString(p.SKI) {
			problems = append(problems, fmt.Sprintf("peers[%d] (%s): ski must be exactly 40 hex characters, got %q", i, p.Name, p.SKI))
		}
	}
	hasDialTarget := len(c.Peers) > 0 || (c.Simulator.Enabled && len(c.Simulator.Devices) > 0)
	if c.Network.Mode == "static" && !hasDialTarget {
		problems = append(problems, "network.mode is 'static' but no peers are configured and the simulator is off")
	}
	if c.Network.Mode == "mdns" && strings.TrimSpace(c.Network.Interface) == "" {
		problems = append(problems, "network.interface is required when network.mode is 'mdns'")
	}
	if _, err := c.Network.AnnounceIPs(); err != nil {
		problems = append(problems, err.Error())
	}
	return problems
}

func (c *Config) PeerBySKI(ski string) *Peer {
	ski = strings.ToLower(ski)
	for i := range c.Peers {
		if c.Peers[i].SKI == ski {
			return &c.Peers[i]
		}
	}
	return nil
}
