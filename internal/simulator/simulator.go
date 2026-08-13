// Package simulator implements simulated EEBUS devices for testing without real hardware: each
// one is its own tiny EEBUS peer (own identity, own SHIP port) that accepts an LPC
// consumption limit and reports MPC power accordingly. Keep it simple, as requested: one
// number (a baseline), dropping to whatever limit is currently active, nothing per-phase, no
// other use cases.
package simulator

import (
	"fmt"
	"log"
	"path/filepath"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/service"
	cslpc "github.com/enbility/eebus-go/usecases/cs/lpc"
	mumpc "github.com/enbility/eebus-go/usecases/mu/mpc"
	shipapi "github.com/enbility/ship-go/api"
	"github.com/enbility/ship-go/mdns"
	"github.com/enbility/ship-go/util"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"

	"github.com/marck-brusa/eebuster/internal/announce"
	"github.com/marck-brusa/eebuster/internal/config"
	"github.com/marck-brusa/eebuster/internal/eebusgo"
	"github.com/marck-brusa/eebuster/internal/identity"
	"github.com/marck-brusa/eebuster/internal/netfilter"
	"github.com/marck-brusa/eebuster/internal/trace"
)

// Device is one simulated peer: its own EEBUS service, an LPC (Controllable System side --
// accepts limits) and an MPC (Measurement Unit side -- reports power).
type Device struct {
	id        string
	baselineW float64
	service   *service.Service
	announcer *announce.Provider
	lpc       *cslpc.LPC
	mpc       *mumpc.MPC

	mu          sync.Mutex
	limitW      float64
	limitActive bool
}

var _ eebusapi.ServiceReaderInterface = (*Device)(nil)

// entityAndDeviceType maps the config's "type" string to SPINE types. Unrecognized or blank
// falls back to "generic" (an EVSE/charging-station shape) since LPC accepts that entity
// type and it's a reasonable stand-in for "some load."
func entityAndDeviceType(t string) (spinemodel.EntityTypeType, spinemodel.DeviceTypeType, shipapi.DeviceCategoryType) {
	switch t {
	case "heat_pump":
		return spinemodel.EntityTypeTypeHeatPumpAppliance, spinemodel.DeviceTypeTypeHeatgenerationSystem, shipapi.DeviceCategoryTypeHVAC
	default: // "generic", "ev"
		return spinemodel.EntityTypeTypeEVSE, spinemodel.DeviceTypeTypeChargingStation, shipapi.DeviceCategoryTypeEMobility
	}
}

// New configures and Setup()s (but does not Start) one simulated device. dataDir is the same
// data directory the primary stack uses -- each simulated device gets its own identity under
// <dataDir>/identity/sim-<id>/, never shared with the primary stack's own identity. frames
// may be nil; when set, the simulated device's raw frames land in the same trace store as the
// primary stack's, which is what lets the Trace tab (and its tests) run without hardware.
func New(cfg config.SimulatedDevice, dataDir string, logLevel eebusgo.LogLevel, rules netfilter.Rules, frames *trace.Store) (*Device, error) {
	if cfg.ID == "" {
		return nil, fmt.Errorf("simulator device is missing an id")
	}
	baseline := cfg.BaselineW
	if baseline <= 0 {
		baseline = 11000
	}
	entityType, deviceType, category := entityAndDeviceType(cfg.Type)

	identityDir := filepath.Join(dataDir, "identity", "sim-"+cfg.ID)
	// A regenerated simulator identity needs no warning: simulated devices are trusted
	// automatically by the stack that hosts them, so a new SKI costs nothing.
	cert, _, err := identity.LoadOrCreate(
		filepath.Join(identityDir, "cert.pem"), filepath.Join(identityDir, "key.pem"),
		"SIM", cfg.ID, "DE", cfg.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("simulator %s: identity: %w", cfg.ID, err)
	}

	configuration, err := eebusapi.NewConfiguration(
		"SIM", "EEBUS simulator", cfg.ID, cfg.ID,
		[]shipapi.DeviceCategoryType{category},
		deviceType,
		[]spinemodel.EntityTypeType{entityType},
		cfg.ShipPort, cert, 4*time.Second, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("simulator %s: configuration: %w", cfg.ID, err)
	}
	// TestSetup so the announcement can be routed through internal/announce, exactly as the main
	// stack does. This is not optional tidiness: a simulated device announces its own
	// _ship._tcp record, and until this was injected it used ship-go's plain zeroconf provider,
	// which publishes every address of every interface. Because every announcer on a host shares
	// the machine hostname by default, those unfiltered A/AAAA records merged with the main
	// stack's carefully filtered ones under the same name -- so a real device resolving the main
	// stack's SRV target got the IPv6 link-local back regardless. Caught only by querying the
	// wire directly with an independent mDNS probe; both announcers looked correct in isolation.
	// The distinct host label below is the belt to this braces.
	configuration.SetMdnsProviderSelection(mdns.MdnsProviderSelectionTestSetup)

	d := &Device{id: cfg.ID, baselineW: baseline}
	svc := service.NewService(configuration, d)
	if err := svc.Setup(); err != nil {
		return nil, fmt.Errorf("simulator %s: setup: %w", cfg.ID, err)
	}
	simLogger := eebusgo.StdLogger{Prefix: "[sim-" + cfg.ID + "]", Level: logLevel}
	if frames != nil {
		simStack := "sim-" + cfg.ID
		simLogger.ObserveFrame = func(dir, ski, payload string) {
			frames.Add(simStack, dir, ski, payload)
		}
	}
	svc.SetLogging(simLogger)
	d.service = svc
	// Its own host label, so this device's address records can never merge with the main stack's
	// or with another simulated device's.
	d.announcer = announce.New(mdns.NewZeroconfProvider(nil), nil, rules, nil,
		announce.HostLabel(svc.LocalService().SKI()))
	// It's simulated -- no pairing prompt on the simulator's own side either. The main stack's
	// end of the trust decision is handled by giving it an AutoAccept static peer entry
	// (see cmd/eebus-testbench/serve.go), not by anything in this package.
	svc.SetAutoAccept(true)

	localEntity := svc.LocalDevice().EntityForType(entityType)

	lpcUC := cslpc.NewLPC(localEntity, d.onLPCEvent)
	if err := svc.AddUseCase(lpcUC); err != nil {
		return nil, fmt.Errorf("simulator %s: adding cs/lpc: %w", cfg.ID, err)
	}
	d.lpc = lpcUC
	_ = lpcUC.SetConsumptionNominalMax(baseline)
	_ = lpcUC.SetFailsafeConsumptionActivePowerLimit(baseline/2, true)
	_ = lpcUC.SetFailsafeDurationMinimum(2*time.Hour, true)

	mpcUC, err := mumpc.NewMPC(localEntity, d.onMPCEvent,
		&mumpc.MonitorPowerConfig{ValueSourceTotal: util.Ptr(spinemodel.MeasurementValueSourceTypeMeasuredValue)},
		nil, nil, nil, nil,
	)
	if err != nil {
		return nil, fmt.Errorf("simulator %s: configuring mu/mpc: %w", cfg.ID, err)
	}
	if err := svc.AddUseCase(mpcUC); err != nil {
		return nil, fmt.Errorf("simulator %s: adding mu/mpc: %w", cfg.ID, err)
	}
	d.mpc = mpcUC

	return d, nil
}

func (d *Device) ID() string { return d.id }

func (d *Device) Start() error {
	// Re-injected on every Start, not once at Setup: ship-go's mdns manager nils its provider
	// slot on Shutdown and never re-arms it -- see eebusgo.Stack.Start for the same fix.
	if err := d.service.SetMdnsProvider(d.announcer); err != nil {
		return err
	}
	if err := d.service.Start(); err != nil {
		return err
	}
	d.reportPower() // publish the baseline immediately, not just after the first limit write
	return nil
}

func (d *Device) Shutdown() { d.service.Shutdown() }

// LocalSKI is the real SKI, not GetLocalCertificateFingerprint()'s certificate fingerprint --
// see eebusgo.Stack.LocalSKI's comment for how that distinction was found.
func (d *Device) LocalSKI() string {
	return d.service.LocalService().SKI()
}

// onLPCEvent auto-approves every incoming limit write (a simulator has no reason to refuse
// one) and recomputes the reported power whenever the active limit changes -- matches
// examples/evse/main.go's own LimitWriteApprovalRequired/DataUpdateLimit handling.
func (d *Device) onLPCEvent(ski string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, event eebusapi.EventType) {
	switch event {
	case cslpc.LimitWriteApprovalRequired:
		for msgCounter := range d.lpc.PendingConsumptionLimits() {
			d.lpc.ApproveOrDenyConsumptionLimit(msgCounter, true, "")
		}
	case cslpc.DataUpdateLimit:
		limit, err := d.lpc.ConsumptionLimit()
		if err != nil {
			return
		}
		d.mu.Lock()
		d.limitW = limit.Value
		d.limitActive = limit.IsActive
		d.mu.Unlock()
		log.Printf("simulator[%s]: limit from ski %s -> %.0fW active=%v", d.id, ski, limit.Value, limit.IsActive)
		d.reportPower()
	}
}

func (d *Device) onMPCEvent(_ string, _ spineapi.DeviceRemoteInterface, _ spineapi.EntityRemoteInterface, _ eebusapi.EventType) {
	// No action needed: MPC is read-only from the peer's side, this device is only ever the
	// one being read from, never the reader.
}

// reportPower is the whole simulation: at baseline unless a limit is active and lower, in
// which case it drops to the limit -- exactly "always 11kW, LPC at 2kW drops it down until
// it's clear" from the request that created this.
func (d *Device) reportPower() {
	d.mu.Lock()
	power := d.baselineW
	if d.limitActive && d.limitW < power {
		power = d.limitW
	}
	d.mu.Unlock()

	if err := d.mpc.Update(d.mpc.UpdateDataPowerTotal(power, nil, nil)); err != nil {
		log.Printf("simulator[%s]: reporting %.0fW failed: %v", d.id, power, err)
		return
	}
	log.Printf("simulator[%s]: reporting %.0fW", d.id, power)
}

// The rest of api.ServiceReaderInterface: a simulator has no pairing UI, so these are
// log-only -- there's no dashboard tab watching a simulated device's own connection state.

func (d *Device) RemoteServiceConnected(_ eebusapi.ServiceInterface, identity shipapi.ServiceIdentity) {
	log.Printf("simulator[%s]: connected to ski %s", d.id, identity.SKI)
}
func (d *Device) RemoteServiceDisconnected(_ eebusapi.ServiceInterface, identity shipapi.ServiceIdentity) {
	log.Printf("simulator[%s]: disconnected from ski %s", d.id, identity.SKI)
}
func (d *Device) VisibleRemoteMdnsServicesUpdated(_ eebusapi.ServiceInterface, _ []shipapi.RemoteMdnsService) {
}
func (d *Device) ServiceUpdated(_ shipapi.ServiceIdentity) {}
func (d *Device) ServicePairingDetailUpdate(_ shipapi.ServiceIdentity, _ *shipapi.ConnectionStateDetail) {
}
func (d *Device) ServiceAutoTrusted(_ eebusapi.ServiceInterface, _ shipapi.ServiceIdentity) {}
func (d *Device) ServiceAutoTrustFailed(_ eebusapi.ServiceInterface, _ shipapi.ServiceIdentity, _ error) {
}
func (d *Device) ServiceAutoTrustRemoved(_ eebusapi.ServiceInterface, _ shipapi.ServiceIdentity, _ string) {
}
