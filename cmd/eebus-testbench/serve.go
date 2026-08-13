package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/marck-brusa/eebuster/internal/config"
	"github.com/marck-brusa/eebuster/internal/eebusgo"
	"github.com/marck-brusa/eebuster/internal/httpapi"
	"github.com/marck-brusa/eebuster/internal/identity"
	"github.com/marck-brusa/eebuster/internal/logbuf"
	"github.com/marck-brusa/eebuster/internal/simulator"
	"github.com/marck-brusa/eebuster/internal/staticmdns"
	"github.com/marck-brusa/eebuster/internal/trace"
	"github.com/marck-brusa/eebuster/internal/truststore"
)

// configCandidates are tried in order when -config is not given explicitly.
//
// The release archive unpacks eebus.yaml next to the executable, but the historical default was
// config/eebus.yaml -- so a bare `./eebus-testbench serve` read neither and silently started on
// zero-config defaults, ignoring the very file the archive tells you to edit. Peers configured
// there, including address pins, were dropped without a word. Both layouts now work.
var configCandidates = []string{"config/eebus.yaml", "eebus.yaml"}

// defaultConfigPath returns the first candidate that exists, or the last one so the
// "no config file at ..." message names something meaningful.
func defaultConfigPath() string {
	for _, candidate := range configCandidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return configCandidates[len(configCandidates)-1]
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "config/eebus.yaml", "path to eebus.yaml (default: config/eebus.yaml, then eebus.yaml)")
	dataDir := fs.String("data-dir", "./data", "directory for identity keys and other persistent state")
	scenariosDir := fs.String("scenarios", "scenarios", "directory of *.yaml scenario files, for the dashboard's Scenarios tab")
	simulatorFlag := fs.Bool("simulator", true, "run the simulated devices from config's simulator.devices (both this flag and simulator.enabled must be true)")
	autoAcceptFlag := fs.Bool("auto-accept", true, "announce register=true in mDNS, i.e. present this testbench as available for pairing (overrides config auto_accept when passed)")
	requireApprovalFlag := fs.Bool("require-approval", false, "stop each incoming pairing request for an approve/deny decision in the dashboard, while still announcing availability (overrides config require_approval when passed)")
	logLevelFlag := fs.String("log-level", "", "EEBUS stack log verbosity: trace|debug|info|error (default debug; trace dumps every SPINE datagram as JSON)")
	announceAddrFlag := fs.String("announce-address", "", "comma-separated IPs to publish in our own mDNS records, overriding auto-detection (e.g. 192.168.9.100)")
	noFilterFlag := fs.Bool("no-announce-filter", false, "publish every local address, including IPv6 link-local/unique-local and virtual adapters (upstream zeroconf behaviour)")
	frameLogFlag := fs.String("frame-log", "", "append every raw SHIP frame to this file in EEBus Hub log format, ready for `eebustracer serve --log-file <file>`")
	fs.Parse(args)
	autoAcceptSet, requireApprovalSet, noFilterSet, configSet := false, false, false, false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "auto-accept":
			autoAcceptSet = true
		case "require-approval":
			requireApprovalSet = true
		case "no-announce-filter":
			noFilterSet = true
		case "config":
			configSet = true
		}
	})
	if !configSet {
		*configPath = defaultConfigPath()
	}

	// Every log.Printf lands both in this console (what a terminal user watches directly)
	// and in an in-memory ring buffer the dashboard's Diagnostics log viewer reads from --
	// there's no more per-process supervisor writing separate log files to tail.
	logs := logbuf.New(4000)
	log.SetOutput(io.MultiWriter(os.Stdout, logs))

	cfg, err := config.Load(*configPath)
	if err == config.ErrUsingZeroConfigDefaults {
		log.Printf("no config file at %s -- %v", *configPath, err)
	} else if err != nil {
		log.Fatalf("config: %v", err)
	}

	identityDir := filepath.Join(*dataDir, "identity")
	certificate, identityCreated, err := identity.LoadOrCreate(
		filepath.Join(identityDir, "cert.pem"),
		filepath.Join(identityDir, "key.pem"),
		cfg.Identity.VendorCode, cfg.Identity.Model, cfg.Identity.Country, cfg.Identity.Serial,
	)
	if err != nil {
		log.Fatalf("identity: %v", err)
	}
	localSKI, err := identity.SKI(certificate)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// A new identity is a new SKI, and a device paired with the previous one will refuse this
	// one outright. Say so loudly and unmissably at startup: the failure it causes otherwise
	// shows up much later as a pairing that the device reports as successful while nothing ever
	// connects, and the device's own log is the only place the refusal is visible.
	if identityCreated {
		log.Printf("identity: generated a NEW identity in %s -- ski %s", identityDir, localSKI)
		log.Printf("identity: any device already paired with a previous identity will REFUSE this one " +
			"(SHIP close 4452) until you pair it again. To keep an existing pairing instead, stop and " +
			"restart with -data-dir pointing at the directory holding the old identity.")
	} else {
		log.Printf("identity: loaded from %s -- ski %s", identityDir, localSKI)
	}

	// Announce a serial that carries a SKI fragment, so two identities built from the same
	// config are distinguishable in a device's paired-device list. See identity.AnnouncedSerial.
	announcedSerial := identity.AnnouncedSerial(cfg.Identity.Serial, localSKI)

	shipPort := 4712
	if raw, ok := cfg.Stacks["eebus-go-remote"].Extra["ship_port"]; ok {
		if p, ok := raw.(int); ok {
			shipPort = p
		}
	}

	// An explicitly-passed flag wins over the config file; otherwise the config value stands, so
	// a flag's own `false` default never silently overrides the file.
	advertise := cfg.Advertised()
	if autoAcceptSet {
		advertise = *autoAcceptFlag
	}
	requireApproval := cfg.RequireApproval
	if requireApprovalSet {
		requireApproval = *requireApprovalFlag
	}
	logLevelStr := cfg.LogLevel
	if *logLevelFlag != "" {
		logLevelStr = *logLevelFlag
	}
	logLevel := eebusgo.ParseLogLevel(logLevelStr)

	// -no-announce-filter and -announce-address both override the config file when passed, same
	// precedence rule as -auto-accept above.
	if noFilterSet {
		cfg.Network.AnnounceFilter = !*noFilterFlag
	}
	if *announceAddrFlag != "" {
		cfg.Network.AnnounceAddresses = strings.Split(*announceAddrFlag, ",")
	}
	announceIPs, err := cfg.Network.AnnounceIPs()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Simulated devices are set up (identity loaded/generated, service Setup()'d) before the
	// main stack boots, so their SKIs are already known and can go straight into the main
	// stack's *initial* peer list as AutoAccept entries -- simulated devices are trusted
	// automatically, no manual approval step, because they're simulated (see
	// internal/simulator's doc comment). This now works in both network modes: the provider
	// chain composes real zeroconf with these synthetic entries in either case, so the
	// simulator runs under the zero-config mdns default a double-click gets, not just static
	// mode. Verified by a full SPINE handshake in mdns mode.
	// One trace store for every stack in the process: the primary stack's frames and the
	// simulated devices' frames land in the same ring, tagged by stack id.
	frames := trace.New()
	if *frameLogFlag != "" {
		frameLog, err := os.OpenFile(*frameLogFlag, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("frame log: %v", err)
		}
		defer frameLog.Close()
		frames.SetLogWriter(frameLog)
		log.Printf("frame log: appending every raw SHIP frame to %s (EEBus Hub format; feed it to EEBusTracer with --log-file)", *frameLogFlag)
	}

	var simDevices []*simulator.Device
	var simPeers []staticmdns.Peer
	if *simulatorFlag && cfg.Simulator.Enabled {
		for _, devCfg := range cfg.Simulator.Devices {
			dev, err := simulator.New(devCfg, *dataDir, logLevel, cfg.Network.AnnounceRules(), frames)
			if err != nil {
				log.Fatalf("simulator %s: %v", devCfg.ID, err)
			}
			simDevices = append(simDevices, dev)
			simPeers = append(simPeers, staticmdns.Peer{
				Name: devCfg.ID, SKI: dev.LocalSKI(), Host: "127.0.0.1", Port: devCfg.ShipPort,
				Path: "/ship/", AutoAccept: true,
			})
		}
	}

	stack, err := eebusgo.New(eebusgo.BootConfig{
		VendorCode:      cfg.Identity.VendorCode,
		Brand:           cfg.Identity.Brand,
		Model:           cfg.Identity.Model,
		Serial:          announcedSerial,
		Certificate:     certificate,
		ShipPort:        shipPort,
		NetworkMode:     cfg.Network.Mode,
		Peers:           append(toStaticPeers(cfg.Peers), simPeers...),
		MdnsProvider:    cfg.Network.MdnsProvider,
		Interface:       cfg.Network.Interface,
		Advertise:       advertise,
		RequireApproval: requireApproval,
		LogLevel:        logLevel,

		AnnounceRules:     cfg.Network.AnnounceRules(),
		AnnounceAddresses: announceIPs,
		Frames:            frames,
	})
	if err != nil {
		log.Fatalf("eebus stack: %v", err)
	}
	if err := stack.Start(); err != nil {
		log.Fatalf("eebus stack start: %v", err)
	}
	pairingMode := "automatic"
	if requireApproval {
		pairingMode = "waits for approve/deny in the dashboard"
	} else if !advertise {
		pairingMode = "declines incoming pairing"
	}
	log.Printf("eebus-go-remote started, SHIP port %d, SKI %s, network.mode=%s, announces register=%v, incoming pairing: %s, log_level=%s",
		shipPort, stack.LocalSKI(), cfg.Network.Mode, advertise, pairingMode, logLevel)

	// What we publish and what we dropped, with reasons. A peer that can discover us but never
	// connect is almost always looking at an address it cannot route to, and that failure is
	// only visible in the peer's own log -- printing our side of it is what makes the problem
	// diagnosable from here. Immediately followed by the inbound-reachability warning, since the
	// other half of the same failure is a host firewall dropping the SHIP port.
	stack.LogAnnounceDecision()
	logInboundReachabilityHint(shipPort)

	// Being in the static peer list (even with an AutoAccept/"register" TXT flag set) only
	// governs whether an *incoming* connection from that SKI is silently accepted -- it does
	// NOT make this stack dial out. Discovered by testing the simulator handshake end to end:
	// ship-go's hub only ever attempts an outbound connection for a SKI that already has a
	// registered ServiceIdentity (ReportMdnsEntries looks it up via ServiceForIdentifier and
	// skips anything not already known), and the only thing that creates that registration is
	// Trust() (RegisterRemoteService). Static "trust: auto" peers therefore need an explicit
	// Trust() call at boot to actually connect -- this was true for real configured peers all
	// along, just never exercised: every config used so far had trust: manual. Do it for both
	// real auto-trust peers and every simulated device (always trusted, per request).
	//
	// No longer gated on network.mode: static peers are injected in both modes now (the
	// provider chain is the same, mdns mode just has an empty peers: list), so a configured
	// trust: auto peer should be dialled in either mode.
	for _, p := range cfg.Peers {
		if p.Trust != "manual" {
			stack.Trust(p.SKI)
		}
	}

	// Re-trust everything trusted through the API or dashboard in an earlier run. Config peers
	// above cover devices declared up front; this covers the ones trusted interactively, which
	// used to be forgotten on restart. Kept separate from the config so a re-imaged device (new
	// SKI) can simply be trusted again without editing any file.
	trustStore, err := truststore.Load(*dataDir)
	if err != nil {
		// A damaged store costs re-pairing, not startup.
		log.Printf("truststore: %v", err)
	}
	for _, ski := range trustStore.SKIs() {
		log.Printf("trust: restoring %s from %s", ski, truststore.FileName)
		stack.Trust(ski)
	}

	for i, dev := range simDevices {
		if err := dev.Start(); err != nil {
			log.Fatalf("simulator %s: start: %v", dev.ID(), err)
		}
		log.Printf("simulator %s started, SHIP port %d, SKI %s, baseline %.0fW",
			dev.ID(), cfg.Simulator.Devices[i].ShipPort, dev.LocalSKI(), effectiveBaseline(cfg.Simulator.Devices[i]))
		stack.Trust(dev.LocalSKI())
	}

	server := httpapi.New(cfg, *configPath, *scenariosDir, logs, stack, trustStore, frames)
	httpServer := &http.Server{Addr: cfg.API.Bind + ":" + strconv.Itoa(cfg.API.Port), Handler: server.Handler()}
	go func() {
		log.Printf("dashboard listening on http://%s/ui", httpServer.Addr)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("http server: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	log.Println("shutting down")
	for _, dev := range simDevices {
		dev.Shutdown()
	}
	stack.Shutdown()
	_ = httpServer.Close()
}

func effectiveBaseline(d config.SimulatedDevice) float64 {
	if d.BaselineW <= 0 {
		return 11000
	}
	return d.BaselineW
}

func toStaticPeers(peers []config.Peer) []staticmdns.Peer {
	out := make([]staticmdns.Peer, 0, len(peers))
	for _, p := range peers {
		out = append(out, staticmdns.Peer{
			Name: p.Name, SKI: p.SKI, Host: p.Host, Port: p.Port, Path: p.Path,
			AutoAccept: p.Trust != "manual",
		})
	}
	return out
}
