package main

import (
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
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
//
// The same applied one directory level up: every default here was resolved against the
// CURRENT WORKING directory, so launching the release binary from anywhere else (double-click,
// a shortcut, a different terminal directory) silently ignored the eebus.yaml sitting next to
// the executable -- and, far worse, put the data directory somewhere new, which mints a new
// identity/SKI and breaks every existing pairing "for no reason". Defaults therefore fall back
// to the executable's own directory whenever the working directory has nothing.
var configCandidates = []string{"config/eebus.yaml", "eebus.yaml"}

// executableDir is where the release archive puts eebus.yaml, scenarios/ and the data
// directory -- next to the binary. Empty when it cannot be determined (never fatal).
func executableDir() string {
	self, err := os.Executable()
	if err != nil {
		return ""
	}
	return filepath.Dir(self)
}

// defaultConfigPath returns the first candidate that exists -- working directory first (the
// development layout), then next to the executable (the release layout) -- or the last
// working-directory candidate so the "no config file at ..." message names something
// meaningful.
func defaultConfigPath() string {
	candidates := configCandidates
	if dir := executableDir(); dir != "" {
		for _, candidate := range configCandidates {
			candidates = append(candidates, filepath.Join(dir, candidate))
		}
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return configCandidates[len(configCandidates)-1]
}

// defaultDataDir keeps an existing working-directory data/ (the historical location, and the
// development layout), and otherwise anchors next to the executable, so where a run happens
// to be started from can never silently change the identity.
func defaultDataDir() string {
	if info, err := os.Stat("data"); err == nil && info.IsDir() {
		return "./data"
	}
	if dir := executableDir(); dir != "" {
		return filepath.Join(dir, "data")
	}
	return "./data"
}

// defaultScenariosDir mirrors defaultDataDir for the scenario library.
func defaultScenariosDir() string {
	if info, err := os.Stat("scenarios"); err == nil && info.IsDir() {
		return "scenarios"
	}
	if dir := executableDir(); dir != "" {
		if info, err := os.Stat(filepath.Join(dir, "scenarios")); err == nil && info.IsDir() {
			return filepath.Join(dir, "scenarios")
		}
	}
	return "scenarios"
}

func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	configPath := fs.String("config", "config/eebus.yaml", "path to eebus.yaml (default: config/eebus.yaml, then eebus.yaml, then the same names next to the executable)")
	dataDir := fs.String("data-dir", "./data", "directory for identity keys and other persistent state (default: ./data if it exists, else data/ next to the executable)")
	scenariosDir := fs.String("scenarios", "scenarios", "directory of *.yaml scenario files, for the dashboard's Scenarios tab")
	simulatorFlag := fs.Bool("simulator", true, "run the simulated devices from config's simulator.devices (both this flag and simulator.enabled must be true)")
	autoAcceptFlag := fs.Bool("auto-accept", true, "announce register=true in mDNS, i.e. present this testbench as available for pairing (overrides config auto_accept when passed)")
	requireApprovalFlag := fs.Bool("require-approval", false, "stop each incoming pairing request for an approve/deny decision in the dashboard, while still announcing availability (overrides config require_approval when passed)")
	logLevelFlag := fs.String("log-level", "", "EEBUS stack log verbosity: trace|debug|info|error (default debug; trace dumps every SPINE datagram as JSON)")
	announceAddrFlag := fs.String("announce-address", "", "comma-separated IPs to publish in our own mDNS records, overriding auto-detection (e.g. 192.168.9.100)")
	noFilterFlag := fs.Bool("no-announce-filter", false, "publish every local address, including IPv6 link-local/unique-local and virtual adapters (upstream zeroconf behaviour)")
	frameLogFlag := fs.String("frame-log", "", "append every raw SHIP frame to this file in EEBus Hub log format, ready for EEBusTracer to import (defaults to <data-dir>/frames.log when the bundled tracer runs)")
	tracerFlag := fs.Bool("tracer", true, "run the bundled eebustracer web UI on localhost and link it from the dashboard sidebar (skipped when the binary is not next to this executable, or when config sets tracer_url)")
	tracerPortFlag := fs.Int("tracer-port", 8090, "port for the bundled eebustracer UI")
	fs.Parse(args)
	autoAcceptSet, requireApprovalSet, noFilterSet, configSet, dataDirSet, scenariosSet := false, false, false, false, false, false
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
		case "data-dir":
			dataDirSet = true
		case "scenarios":
			scenariosSet = true
		}
	})
	if !configSet {
		*configPath = defaultConfigPath()
	}
	if !dataDirSet {
		*dataDir = defaultDataDir()
	}
	if !scenariosSet {
		*scenariosDir = defaultScenariosDir()
	}

	// Every log.Printf lands both in this console (what a terminal user watches directly)
	// and in an in-memory ring buffer the dashboard's Diagnostics log viewer reads from --
	// there's no more per-process supervisor writing separate log files to tail.
	logs := logbuf.New(4000)
	log.SetOutput(io.MultiWriter(os.Stdout, logs))

	cfg, err := config.Load(*configPath)
	if err == config.ErrUsingZeroConfigDefaults {
		tried := strings.Join(configCandidates, ", ")
		if dir := executableDir(); dir != "" {
			tried += ", " + filepath.Join(dir, "eebus.yaml")
		}
		log.Printf("config: NO file found (tried %s) -- running on zero-config defaults; "+
			"an edited eebus.yaml elsewhere is NOT being read", tried)
	} else if err != nil {
		log.Fatalf("config: %v", err)
	} else {
		abs, _ := filepath.Abs(*configPath)
		log.Printf("config: loaded %s", abs)
	}
	if absData, err := filepath.Abs(*dataDir); err == nil {
		// The data directory decides the identity, and the identity decides every pairing.
		// Name it unmissably, so "why did my pairing break" is answerable from one log line.
		log.Printf("data dir: %s (holds the identity -- changing it changes this testbench's SKI)", absData)
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
	// The bundled EEBusTracer, spawned automatically when its binary sits next to ours. It is
	// a separate product with its own web UI; the sidebar link (via cfg.TracerURL) opens it in
	// a new tab. An explicit tracer_url in the config means the user manages their own
	// instance, so nothing is spawned. Failure to start is logged and otherwise ignored --
	// a broken sidecar must never take the testbench down.
	tracerProcess := startBundledTracer(*tracerFlag, *tracerPortFlag, *dataDir, cfg)

	// One trace store for every stack in the process: the primary stack's frames and the
	// simulated devices' frames land in the same ring, tagged by stack id.
	frames := trace.New()
	frameLogPath := *frameLogFlag
	if frameLogPath == "" && tracerProcess != nil {
		// The tracer without data is an empty page; default the frame log next to the
		// identity so there is always a session file to import in its UI.
		frameLogPath = filepath.Join(*dataDir, "frames.log")
	}
	if frameLogPath != "" {
		if err := os.MkdirAll(filepath.Dir(frameLogPath), 0o755); err != nil {
			log.Fatalf("frame log: %v", err)
		}
		frameLog, err := os.OpenFile(frameLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			log.Fatalf("frame log: %v", err)
		}
		defer frameLog.Close()
		frames.SetLogWriter(frameLog)
		log.Printf("frame log: appending every raw SHIP frame to %s (EEBus Hub format; import it in EEBusTracer)", frameLogPath)
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
	// The truststore must exist and the connected-peer callback must be registered BEFORE the
	// stack starts: a device can dial in the instant the SHIP server is up, and that first
	// connection is exactly the one that needs persisting. Config peers cover devices declared
	// up front; the store covers everything trusted interactively or paired from the device's
	// side (auto-accepted here, never visible to the trust API). Kept separate from the config
	// so a re-imaged device (new SKI) can simply be trusted again without editing any file.
	trustStore, err := truststore.Load(*dataDir)
	if err != nil {
		// A damaged store costs re-pairing, not startup.
		log.Printf("truststore: %v", err)
	}
	simSKIs := map[string]bool{}
	for _, dev := range simDevices {
		simSKIs[dev.LocalSKI()] = true
	}
	stack.OnPeerConnected = func(ski string) {
		if simSKIs[ski] {
			return
		}
		added, addErr := trustStore.Add(ski)
		if addErr != nil {
			log.Printf("trust: persisting %s failed: %v", ski, addErr)
			return
		}
		if added {
			log.Printf("trust: %s connected and is now persisted in %s -- it will be re-dialled after a restart", ski, truststore.FileName)
		}
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

	// Re-dial everything trusted in an earlier run (loaded and registered before Start; the
	// dial itself needs the started stack).
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
	if tracerProcess != nil {
		_ = tracerProcess.Kill()
	}
	for _, dev := range simDevices {
		dev.Shutdown()
	}
	stack.Shutdown()
	_ = httpServer.Close()
}

// startBundledTracer spawns the eebustracer binary shipped in the release archive, if present
// next to our own executable, and points cfg.TracerURL at it so the dashboard sidebar links
// it. Returns the child process for shutdown, or nil when nothing was started.
func startBundledTracer(enabled bool, port int, dataDir string, cfg *config.Config) *os.Process {
	if !enabled || cfg.TracerURL != "" {
		return nil
	}
	self, err := os.Executable()
	if err != nil {
		return nil
	}
	name := "eebustracer"
	if strings.HasSuffix(strings.ToLower(self), ".exe") {
		name += ".exe"
	}
	binary := filepath.Join(filepath.Dir(self), name)
	if _, err := os.Stat(binary); err != nil {
		return nil // not bundled here (e.g. a `go run` build); nothing to do
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Printf("eebustracer: cannot prepare data dir: %v", err)
		return nil
	}
	logFile, err := os.OpenFile(filepath.Join(dataDir, "eebustracer.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("eebustracer: cannot open its log file: %v", err)
		return nil
	}
	cmd := exec.Command(binary, "--db", filepath.Join(dataDir, "eebustracer.db"), "serve", "--port", strconv.Itoa(port))
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		log.Printf("eebustracer: start failed: %v", err)
		return nil
	}
	// Reap in the background so a crashed tracer never leaves a zombie; the testbench keeps
	// running either way.
	go func() {
		err := cmd.Wait()
		logFile.Close()
		if err != nil {
			log.Printf("eebustracer: exited: %v (dashboard link may be dead; see %s)", err, logFile.Name())
		}
	}()
	cfg.TracerURL = "http://127.0.0.1:" + strconv.Itoa(port)
	log.Printf("eebustracer: bundled tracer running at %s (pid %d), linked from the dashboard sidebar", cfg.TracerURL, cmd.Process.Pid)
	return cmd.Process
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
