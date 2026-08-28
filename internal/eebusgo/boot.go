package eebusgo

import (
	"crypto/tls"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/service"
	"github.com/enbility/eebus-go/usecases/cem/cevc"
	"github.com/enbility/eebus-go/usecases/cem/evcc"
	"github.com/enbility/eebus-go/usecases/cem/evcem"
	"github.com/enbility/eebus-go/usecases/cem/evsecc"
	"github.com/enbility/eebus-go/usecases/cem/evsoc"
	"github.com/enbility/eebus-go/usecases/cem/ohpcf"
	"github.com/enbility/eebus-go/usecases/cem/opev"
	"github.com/enbility/eebus-go/usecases/cem/oscev"
	"github.com/enbility/eebus-go/usecases/cem/vabd"
	"github.com/enbility/eebus-go/usecases/cem/vapd"
	"github.com/enbility/eebus-go/usecases/eg/lpc"
	"github.com/enbility/eebus-go/usecases/eg/lpp"
	"github.com/enbility/eebus-go/usecases/ma/mgcp"
	"github.com/enbility/eebus-go/usecases/ma/mpc"
	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mdns"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"

	"github.com/marck-brusa/eebuster/internal/announce"
	"github.com/marck-brusa/eebuster/internal/netfilter"
	"github.com/marck-brusa/eebuster/internal/staticmdns"
	"github.com/marck-brusa/eebuster/internal/trace"
)

// stackID is the event bus's stack identifier -- there is exactly one stack in this rewrite
// (openeebus-hems and EEBusTracer were cut from scope), so it's a constant, not a field.
const stackID = "eebus-go-remote"

// BootConfig is everything needed to boot the embedded primary stack, gathered from
// config.Config by the caller (kept independent of the config package to avoid an import
// cycle).
type BootConfig struct {
	VendorCode  string
	Brand       string
	Model       string
	Serial      string
	Certificate tls.Certificate
	ShipPort    int

	// NetworkMode is "static" or "mdns", matching config.Network.Mode exactly.
	NetworkMode string
	// Peers is only used when NetworkMode is "static".
	Peers []staticmdns.Peer
	// MdnsProvider ("zeroconf" | "avahi" | "all") and Interface (blank/"*"/one name/a
	// comma-separated list) are only used when NetworkMode is "mdns".
	MdnsProvider string
	Interface    string
	// Advertise announces register=true in our mDNS TXT ("available for pairing", SHIP 7.3.2),
	// mirroring config.Config.AutoAccept. RequireApproval keeps that advertisement but stops each
	// incoming request for an approve/deny decision instead of accepting it silently.
	Advertise       bool
	RequireApproval bool
	// LogLevel controls the embedded stack's own log verbosity -- see eebusgo.LogLevel.
	LogLevel LogLevel
	// AnnounceRules and AnnounceAddresses control which of our local IPs are published in our
	// own mDNS records, and which of a peer's advertised IPs we bother dialling -- see
	// internal/netfilter and internal/announce.
	AnnounceRules     netfilter.Rules
	AnnounceAddresses []net.IP
	// Frames, if set, receives every raw SHIP frame for the message trace and conformance
	// checks -- captured at the websocket layer, before the vendored stack's JSON repairs.
	Frames *trace.Store
}

// Stack wraps the embedded eebus-go service.Service plus the static mDNS provider feeding it
// (see patches/eebus-go-service-mdns-hook.patch and docs/ for why static mode needs the
// injection hook at all). One Stack is the entire primary counterparty -- no supervisor, no
// subprocess, no JSON-RPC boundary between this and the rest of the binary. It also *is* the
// api.ServiceReaderInterface handler (see handler.go) since it owns the state those callbacks
// update (pending pairings, visible peers) and the event bus they publish to.
type Stack struct {
	service     *service.Service
	networkMode string
	// injected is false only when the user explicitly asked for the avahi provider, in which
	// case ship-go builds and owns its own provider and provider/announcer stay nil.
	injected  bool
	provider  *staticmdns.Provider
	announcer *announce.Provider
	shipPort  int
	pairing   *pairingState
	events    *EventBus
	// startedAt is when Start() ran, so DiscoveryHealth can tell "nothing yet, too early to
	// judge" from "nothing, long enough that multicast is clearly not arriving".
	startedAt time.Time

	// addressPins maps SKI -> IPv4 literal, taken from the configured peers' hosts. See
	// applyAddressPin for what it is for and why it is needed at all.
	pinMu       sync.Mutex
	addressPins map[string]string

	// OnPeerConnected, if set (before Start), is called with the SKI of every peer that
	// completes a SHIP connection -- including peers whose pairing was initiated remotely and
	// auto-accepted, which no API call ever sees. Used to persist those into the truststore.
	OnPeerConnected func(ski string)

	lpc  *LPC
	lpp  *LPP
	mpc  *MPC
	mgcp *MGCP

	evcc   *EVCC
	evsecc *EVSECC
	cevc   *CEVC
	evcem  *EVCEM
	evsoc  *EVSOC
	vapd   *VAPD
	vabd   *VABD

	opev  *OPEV
	oscev *OSCEV
	ohpcf *OHPCF
}

// New configures and Setup()s (but does not Start) the embedded stack in either static or
// real mDNS mode, per cfg.NetworkMode.
func New(cfg BootConfig) (*Stack, error) {
	stack := &Stack{
		networkMode: cfg.NetworkMode,
		shipPort:    cfg.ShipPort,
		pairing:     newPairingState(),
		events:      NewEventBus(),
		addressPins: addressPins(cfg.Peers),
	}

	configuration, err := eebusapi.NewConfiguration(
		cfg.VendorCode, cfg.Brand, cfg.Model, cfg.Serial,
		[]shipapi.DeviceCategoryType{shipapi.DeviceCategoryTypeEnergyManagementSystem},
		spinemodel.DeviceTypeTypeEnergyManagementSystem,
		[]spinemodel.EntityTypeType{spinemodel.EntityTypeTypeCEM},
		cfg.ShipPort, cfg.Certificate, 4*time.Second, nil, nil,
	)
	if err != nil {
		return nil, err
	}

	// Interface pinning applies to both modes: it is what ship-go's own manager resolves and
	// then pushes into whatever provider is active, ours included. Blank/"*" needs no
	// special-casing -- ship-go already treats an empty list as "every usable interface" (see
	// mdns.go's resolveInterfaces).
	if cfg.Interface != "" && cfg.Interface != "*" {
		ifaces := strings.Split(cfg.Interface, ",")
		for i := range ifaces {
			ifaces[i] = strings.TrimSpace(ifaces[i])
		}
		configuration.SetInterfaces(ifaces)
	}

	// Both modes normally run through our own injected provider, because that is the only way
	// to control which of our local addresses end up in the published A/AAAA records --
	// ship-go announces via zeroconf.Register, which takes every address of every interface and
	// has no filtering hook. See internal/netfilter for why that broke pairing against real
	// hardware. The two modes differ only in whether static peers are injected on top.
	//
	// The exception is an explicit request for avahi: that is a genuinely different daemon with
	// its own announcement path, so honour it and fall back to ship-go's internal provider
	// selection, with the address filter inactive. Avahi does not exist on Windows, which is
	// where the address problem actually bites, so this is a safe trade.
	stack.injected = cfg.NetworkMode != "mdns" || (cfg.MdnsProvider != "avahi" && cfg.MdnsProvider != "all")
	if !stack.injected {
		selection := mdns.MdnsProviderSelectionAvahiOnly
		if cfg.MdnsProvider == "all" {
			selection = mdns.MdnsProviderSelectionAll
		}
		configuration.SetMdnsProviderSelection(selection)
		log.Printf("mdns: network.mdns_provider=%s uses ship-go's own provider, so the announce address filter is NOT applied -- "+
			"use mdns_provider: zeroconf (the default) if a peer cannot reach this host", cfg.MdnsProvider)
	} else {
		configuration.SetMdnsProviderSelection(mdns.MdnsProviderSelectionTestSetup)
	}

	svc := service.NewService(configuration, stack)
	if err := svc.Setup(); err != nil {
		return nil, err
	}
	logger := StdLogger{Prefix: "[" + stackID + "]", Level: cfg.LogLevel, Observe: stack.observeLogLine}
	if cfg.Frames != nil {
		frames := cfg.Frames
		logger.ObserveFrame = func(dir, ski, payload string) {
			entry := frames.Add(stackID, dir, ski, payload)
			// Only non-conformant traffic becomes an event: the event ring is the "something
			// needs your attention" channel, the full frame flow lives in the trace store.
			if len(entry.Findings) > 0 {
				stack.events.Publish("spine", stackID, "spine-nonconformant", ski, map[string]any{
					"seq": entry.Seq, "function": entry.Function, "findings": len(entry.Findings),
				})
			}
		}
	}
	svc.SetLogging(logger)
	stack.service = svc

	if stack.injected {
		// Not injected via SetMdnsProvider here -- Start() always (re-)injects it, since
		// ship-go's mdns manager clears its provider slot on every Shutdown (see Start's
		// doc comment) and this must work uniformly for both the first Start() and any
		// restart.
		//
		// Three layers, each with one job:
		//
		//   staticmdns.Provider  injects static peers (the simulator, hand-configured hosts)
		//   announce.Provider    publishes *our* record with a filtered address list, and
		//                        filters a discovered peer's addresses on the way in
		//   mdns.ZeroconfProvider  real multicast browse/resolve
		//
		// Hybrid, not pure-static: the wrapped real provider both announces us on the LAN and
		// feeds real discoveries into the Service's own entry table, which is what makes
		// Trust() on a scan result able to resolve a host:port at all. See
		// staticmdns.NewHybrid for the two bugs that fixed. In mdns mode the static peer list
		// is simply empty, so the same chain serves both modes.
		ifaces, err := resolveInterfaces(cfg.Interface)
		if err != nil {
			return nil, err
		}
		announcer := announce.New(mdns.NewZeroconfProvider(ifaces), ifaces, cfg.AnnounceRules,
			cfg.AnnounceAddresses, announce.HostLabel(svc.LocalService().SKI()))
		stack.announcer = announcer

		provider := staticmdns.NewHybrid(svc.LocalService().SKI(), announcer)
		provider.SetPeers(cfg.Peers)
		stack.provider = provider
	}

	// Upstream's SetAutoAccept does double duty: it decides whether an incoming pairing request is
	// accepted with no human decision, AND it sets the `register` flag in our announced mDNS TXT
	// record ("available for pairing", SHIP 7.3.2). Devices commonly list only peers advertising
	// register=true, so with one control you must choose between being listed at all and testing
	// the approval dialogue -- see config.Config.RequireApproval.
	//
	// So they are separated here. Acceptance goes through the upstream setter; the advertisement
	// is re-asserted afterwards by our own announcer, which owns the TXT record it publishes
	// (announce.Provider.SetAdvertiseRegister). Nothing is faked by doing so: "a human will
	// approve" is genuinely available for pairing, which is what the flag means.
	accept := cfg.Advertise && !cfg.RequireApproval
	svc.SetAutoAccept(accept)
	svc.UserIsAbleToApproveOrCancelPairingRequests(!accept)
	if stack.announcer != nil {
		stack.announcer.SetAdvertiseRegister(cfg.Advertise)
	}

	// CEM is the local entity every use case attaches to -- matches configuration's own
	// entityTypes above and examples/remote/ucs.go's RegisterUseCase pattern.
	localEntity := svc.LocalDevice().EntityForType(spinemodel.EntityTypeTypeCEM)

	lpcUC := lpc.NewLPC(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(lpcUC); err != nil {
		return nil, err
	}
	stack.lpc = &LPC{uc: lpcUC}

	lppUC := lpp.NewLPP(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(lppUC); err != nil {
		return nil, err
	}
	stack.lpp = &LPP{uc: lppUC}

	mpcUC := mpc.NewMPC(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(mpcUC); err != nil {
		return nil, err
	}
	stack.mpc = &MPC{uc: mpcUC}

	mgcpUC := mgcp.NewMGCP(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(mgcpUC); err != nil {
		return nil, err
	}
	stack.mgcp = &MGCP{uc: mgcpUC}

	// EV/PV/battery -- EVCC and EVCEM additionally need the service itself (used to look up
	// the remote device by SKI for EVConnected's liveness check), unlike every other use case
	// registered here which only ever needs the local entity + event callback.
	evccUC := evcc.NewEVCC(svc, localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(evccUC); err != nil {
		return nil, err
	}
	stack.evcc = &EVCC{uc: evccUC}

	evseccUC := evsecc.NewEVSECC(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(evseccUC); err != nil {
		return nil, err
	}
	stack.evsecc = &EVSECC{uc: evseccUC}

	cevcUC := cevc.NewCEVC(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(cevcUC); err != nil {
		return nil, err
	}
	stack.cevc = &CEVC{uc: cevcUC}

	evcemUC := evcem.NewEVCEM(svc, localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(evcemUC); err != nil {
		return nil, err
	}
	stack.evcem = &EVCEM{uc: evcemUC}

	// Per-phase charging-current control towards the EV entity: OPEV writes obligations
	// (overload protection), OSCEV writes recommendations (self-consumption optimization).
	opevUC := opev.NewOPEV(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(opevUC); err != nil {
		return nil, err
	}
	stack.opev = &OPEV{uc: opevUC}

	oscevUC := oscev.NewOSCEV(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(oscevUC); err != nil {
		return nil, err
	}
	stack.oscev = &OSCEV{uc: oscevUC}

	ohpcfUC := ohpcf.NewOHPCF(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(ohpcfUC); err != nil {
		return nil, err
	}
	stack.ohpcf = &OHPCF{uc: ohpcfUC}

	evsocUC := evsoc.NewEVSOC(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(evsocUC); err != nil {
		return nil, err
	}
	stack.evsoc = &EVSOC{uc: evsocUC}

	vapdUC := vapd.NewVAPD(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(vapdUC); err != nil {
		return nil, err
	}
	stack.vapd = &VAPD{uc: vapdUC}

	vabdUC := vabd.NewVABD(localEntity, stack.propagateEvent)
	if err := svc.AddUseCase(vabdUC); err != nil {
		return nil, err
	}
	stack.vabd = &VABD{uc: vabdUC}

	return stack, nil
}

func (s *Stack) LPC() *LPC       { return s.lpc }
func (s *Stack) LPP() *LPP       { return s.lpp }
func (s *Stack) MPC() *MPC       { return s.mpc }
func (s *Stack) MGCP() *MGCP     { return s.mgcp }
func (s *Stack) OPEV() *OPEV     { return s.opev }
func (s *Stack) EVSECC() *EVSECC { return s.evsecc }
func (s *Stack) OSCEV() *OSCEV   { return s.oscev }
func (s *Stack) OHPCF() *OHPCF   { return s.ohpcf }

// Start (re-)starts the embedded stack. ship-go's mDNS manager clears its injected test
// provider on every Shutdown (mdns.go: "m.mdnsProvider = nil", to guarantee a clean state for
// the *next* run) but never re-arms it -- discovered by actually testing the restart path
// end-to-end, not by reading the source: a bare second Start() after a Stop() fails with
// "test provider must be set before starting with MdnsProviderSelectionTestSetup". Re-inject
// before every Start() rather than only once at boot.
func (s *Stack) Start() error {
	if s.provider != nil { // nil in real mdns mode -- nothing to (re-)inject
		if err := s.service.SetMdnsProvider(s.provider); err != nil {
			return err
		}
	}
	s.startedAt = time.Now()
	if err := s.service.Start(); err != nil {
		return err
	}

	// One-shot: report a discovery blackout once, in the log, without waiting for someone to
	// open the diagnostics endpoint. Silence here is otherwise indistinguishable from "no
	// devices on this network", and that ambiguity has cost real debugging time.
	go func() {
		time.Sleep(discoveryGrace + time.Second)
		if health := s.DiscoveryHealth(); health.Warning != "" {
			log.Printf("mdns: %s", health.Warning)
		}
	}()
	return nil
}

func (s *Stack) Shutdown()     { s.service.Shutdown() }
func (s *Stack) Running() bool { return s.service.IsRunning() }

// ShipPort is the port actually passed to Configuration at boot, independent of whatever the
// config file's stacks.eebus-go-remote.ship_port entry happens to contain (which may be
// absent and defaulted) -- see httpapi.handleStacks, which reports this instead of reflecting
// through the raw config map.
func (s *Stack) ShipPort() int { return s.shipPort }

// LocalSKI is the SKI peers must trust to accept connections from this instance -- what you
// paste into the device-under-test's trust config, matching GET /api/v1/identity's old
// meaning. Note the real fix here: Service.GetLocalCertificateFingerprint() returns the
// certificate *fingerprint*, a different value from the SKI despite the very similar name --
// this used that method and reported the wrong identifier all session, invisible until the
// simulator's own "Local SKI:" log line (from Service.SetLogging, added specifically to debug
// this) didn't match what this method returned. LocalService().SKI() is the real one.
func (s *Stack) LocalSKI() string {
	return s.service.LocalService().SKI()
}

// SetPeers replaces the static peer list live, backing config reload the same way
// staticmdns.Provider.SetPeers always has. A no-op when ship-go owns the provider (avahi).
// A reload also refreshes the address pins, so correcting a peer's host in the config and
// reloading is enough to redirect it -- the pin itself is only re-read on the next Trust().
func (s *Stack) SetPeers(peers []staticmdns.Peer) {
	s.pinMu.Lock()
	s.addressPins = addressPins(peers)
	s.pinMu.Unlock()

	if s.provider != nil {
		s.provider.SetPeers(peers)
	}
}

// AnnounceDiagnostics reports which local addresses are published in our own mDNS records and,
// for each one dropped, why. This is the answer to "the device can see us but cannot connect":
// the failure lives entirely in the peer's log, so exposing our own decision is the only way to
// see it from this side. Zero value when ship-go owns the provider (avahi), where we do not
// control the announcement.
func (s *Stack) AnnounceDiagnostics() announce.Diagnostics {
	if s.announcer == nil {
		return announce.Diagnostics{}
	}
	return s.announcer.Diagnostics()
}

// LogAnnounceDecision prints the published address set at startup. Called by serve after the
// stack starts.
func (s *Stack) LogAnnounceDecision() {
	if s.announcer == nil {
		log.Printf("mdns: ship-go owns the mDNS provider (network.mdns_provider=avahi/all), announced addresses are not under our control")
		return
	}
	s.announcer.LogDecision(s.shipPort)
}

func (s *Stack) Trust(ski string) {
	log.Printf("ship: connecting to ski %s", ski)
	s.service.RegisterRemoteService(shipapi.ServiceIdentity{SKI: ski})
	s.applyAddressPin(ski)
}

// addressPins extracts the SKI -> IPv4 pins from a peer list. Only IPv4 literals qualify: the
// field ship-go exposes holds a single IPv4 address, and a hostname would have to be resolved
// first -- which is exactly the step that fails on the networks this pin exists for.
func addressPins(peers []staticmdns.Peer) map[string]string {
	pins := make(map[string]string)
	for _, peer := range peers {
		ip := net.ParseIP(peer.Host)
		if ip == nil || ip.To4() == nil {
			continue
		}
		pins[strings.ToLower(peer.SKI)] = ip.String()
	}
	return pins
}

// applyAddressPin fixes the dial address for one SKI, when a configured peer supplied an IP.
//
// Without a pin the dial target is whatever address is attached to the mDNS entry the hub
// happens to pick, and it iterates its entry map in Go's randomised order. That is fine while
// every device announces a distinct SHIP id. It is not fine otherwise: devices that share a
// SHIP id collide in the mDNS namespace, get renamed in a loop, and their records end up
// cross-linked -- one device's SKI carrying another device's hostname and addresses. The same
// SKI then resolves to a different host from one attempt to the next, and the attempts that
// land on the wrong host hang until the TCP timeout expires, which is long enough to blow the
// trust window. Observed against real hardware where several units shipped the same placeholder
// serial number.
//
// ship-go honours ServiceDetails.IPv4() by replacing the entry's entire address list before
// dialling (hub/hub_mdns.go), so setting it once makes every later attempt deterministic.
// eebus-go does not expose the setter, but RemoteServiceFor hands back the live
// *ServiceDetails, so this needs no addition to the vendor patch. Must run after
// RegisterRemoteService -- there is no ServiceDetails to set anything on before that.
//
// The hostname is still tried ahead of the address (hub.tryConnectionViaHost), so a pin does
// not prevent one failed hostname lookup per attempt. That lookup fails fast, and cannot be
// avoided from here without patching upstream.
func (s *Stack) applyAddressPin(ski string) {
	s.pinMu.Lock()
	pin := s.addressPins[strings.ToLower(ski)]
	s.pinMu.Unlock()
	if pin == "" {
		return
	}

	details := s.service.RemoteServiceFor(shipapi.ServiceIdentity{SKI: ski})
	if details == nil {
		log.Printf("ship: cannot pin ski %s to %s -- not registered", ski, pin)
		return
	}
	details.SetIPv4(pin)
	log.Printf("ship: dial address for ski %s pinned to %s from its configured peer host, "+
		"so discovery cannot redirect it to another device", ski, pin)
}

func (s *Stack) Untrust(ski string) {
	log.Printf("ship: untrusting ski %s", ski)
	s.service.UnregisterRemoteService(shipapi.ServiceIdentity{SKI: ski})
}

// propagateEvent is every use case's api.EntityEventCallback, matching
// examples/remote/ucs.go's PropagateEvent -- publishes onto the "usecase" topic of the same
// event bus GET /events/stream reads from.
func (s *Stack) propagateEvent(ski string, device spineapi.DeviceRemoteInterface, entity spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	data := map[string]any{}
	if entity != nil {
		data["entity"] = entityAddress(entity)
	}
	s.events.Publish("usecase", stackID, string(event), ski, data)
}
