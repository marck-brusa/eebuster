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

	// ev is the vehicle plugged into this station, when the config asks for one. It owns the
	// EV-side use cases and the battery; the station meters whatever it draws.
	ev     *evSim
	evStop chan struct{}

	mu          sync.Mutex
	limitW      float64
	limitActive bool
	// limitGen counts limit writes, so an expiry timer can tell whether the limit it was
	// started for is still the current one. Without it a timer started for an earlier limit
	// deactivates a newer one that arrived in the meantime -- and, because expiry republishes
	// the value it captured, silently restores the older value too.
	limitGen uint64
	// limitTimer expires a limit that arrived with a duration: a spec-correct device stops
	// following the limit AND reports it inactive afterwards. Devices in the field get the
	// first half right and forget the second -- the lpc-limit-expiry scenario exists to catch
	// exactly that, so the simulator has to model the correct behaviour.
	limitTimer *time.Timer
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

	// Publish per-phase values too, on a deliberately asymmetric two-phase split (see
	// reportPower): the dashboard's phase-balance panel and the asymmetry scenarios need a
	// device that shows the effect without hardware.
	measured := mumpc.PhaseMeasurementSourceMap{
		spinemodel.ElectricalConnectionPhaseNameTypeA: util.Ptr(spinemodel.MeasurementValueSourceTypeMeasuredValue),
		spinemodel.ElectricalConnectionPhaseNameTypeB: util.Ptr(spinemodel.MeasurementValueSourceTypeMeasuredValue),
		spinemodel.ElectricalConnectionPhaseNameTypeC: util.Ptr(spinemodel.MeasurementValueSourceTypeMeasuredValue),
	}
	mpcUC, err := mumpc.NewMPC(localEntity, d.onMPCEvent,
		&mumpc.MonitorPowerConfig{
			ConnectedPhases:     spinemodel.ElectricalConnectionPhaseNameTypeAbc,
			ValueSourceTotal:    util.Ptr(spinemodel.MeasurementValueSourceTypeMeasuredValue),
			ValueSourcePerPhase: measured,
		},
		nil,
		&mumpc.MonitorCurrentConfig{ValueSourcePerPhase: measured},
		&mumpc.MonitorVoltageConfig{ValueSourcePerPhase: measured},
		&mumpc.MonitorFrequencyConfig{ValueSource: util.Ptr(spinemodel.MeasurementValueSourceTypeMeasuredValue)},
	)
	if err != nil {
		return nil, fmt.Errorf("simulator %s: configuring mu/mpc: %w", cfg.ID, err)
	}
	if err := svc.AddUseCase(mpcUC); err != nil {
		return nil, fmt.Errorf("simulator %s: adding mu/mpc: %w", cfg.ID, err)
	}
	d.mpc = mpcUC

	if cfg.EV.Enabled {
		ev, err := newEVSim(cfg.ID, cfg.EV, svc.LocalDevice(), localEntity)
		if err != nil {
			return nil, fmt.Errorf("simulator %s: attaching the simulated EV: %w", cfg.ID, err)
		}
		d.ev = ev
		log.Printf("simulator[%s]: simulated EV attached -- %s %s, %.0f kWh battery at %.0f%%, up to %.0fA on %d phase(s)",
			cfg.ID, ev.cfg.Brand, ev.cfg.Model, ev.cfg.BatteryKWh, ev.cfg.SoCStartPercent, ev.cfg.MaxCurrentA, ev.cfg.Phases)
	}

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
	if d.ev != nil {
		d.evStop = make(chan struct{})
		go d.runCharging(d.evStop)
	}
	return nil
}

// runCharging advances the vehicle every second: pick up any obligation a CEM has written,
// let the battery take what it is allowed to, and re-meter the station from what the vehicle
// actually drew. One second is slow enough to be cheap and fast enough that a limit write
// visibly takes effect while you watch.
func (d *Device) runCharging(stop <-chan struct{}) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			d.ev.applyWrittenLimits()
			d.ev.tick(d.stationLimitPerPhaseA())
			d.reportPower()
		}
	}
}

// stationLimitPerPhaseA turns an active station-level LPC limit (watts for the whole
// station) into the per-phase current share the vehicle has to respect -- the path a real
// installation takes from "the house may draw 4 kW" to "each phase may pull 5.8 A".
func (d *Device) stationLimitPerPhaseA() float64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.limitActive || d.ev == nil {
		return 0
	}
	phases := float64(d.ev.cfg.Phases)
	if phases <= 0 {
		return 0
	}
	return d.limitW / phases / evNominalV
}

func (d *Device) Shutdown() {
	if d.evStop != nil {
		close(d.evStop)
		d.evStop = nil
	}
	d.service.Shutdown()
}

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
	case cslpc.ConfigurationWriteApprovalRequired:
		// Failsafe writes go through the device-configuration approval path, separate from
		// limit approval. Without this branch a written failsafe stays pending forever and
		// reads keep returning the old value.
		for msgCounter := range d.lpc.PendingDeviceConfigurations() {
			d.lpc.ApproveOrDenyDeviceConfiguration(msgCounter, true, "")
		}
	case cslpc.DataUpdateLimit:
		limit, err := d.lpc.ConsumptionLimit()
		if err != nil {
			return
		}
		d.mu.Lock()
		d.limitW = limit.Value
		d.limitActive = limit.IsActive
		if d.limitTimer != nil {
			d.limitTimer.Stop()
			d.limitTimer = nil
		}
		d.limitGen++
		if limit.IsActive && limit.Duration > 0 {
			gen := d.limitGen
			d.limitTimer = time.AfterFunc(limit.Duration, func() { d.expireLimit(gen) })
		}
		d.mu.Unlock()
		log.Printf("simulator[%s]: limit from ski %s -> %.0fW active=%v duration=%s", d.id, ski, limit.Value, limit.IsActive, limit.Duration)
		d.reportPower()
	}
}

// expireLimit is the duration timer firing: deactivate the limit in our own published data,
// so a controller reading back after expiry sees is_active=false, then return to baseline.
func (d *Device) expireLimit(gen uint64) {
	d.mu.Lock()
	stale := gen != d.limitGen
	d.mu.Unlock()
	if stale {
		// A newer limit was written after this timer was started; it owns the state now.
		return
	}
	limit, err := d.lpc.ConsumptionLimit()
	if err != nil {
		return
	}
	limit.IsActive = false
	limit.Duration = 0
	if err := d.lpc.SetConsumptionLimit(limit); err != nil {
		log.Printf("simulator[%s]: expiring the limit failed: %v", d.id, err)
		return
	}
	d.mu.Lock()
	d.limitActive = false
	d.limitTimer = nil
	d.mu.Unlock()
	log.Printf("simulator[%s]: limit expired, back to baseline", d.id)
	d.reportPower()
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

	// With a vehicle plugged in, the station meters the vehicle: the EV decides what it draws
	// (its own maximum, any obligation written to it, the station's limit) and the station
	// reports that, so the power chart, the phase currents and the EV's own EVCEM values all
	// tell the same story instead of three plausible but unrelated ones.
	if d.ev != nil {
		evCurrents := d.ev.Currents()
		phaseW := make([]float64, 3)
		total := 0.0
		for i, a := range evCurrents {
			if i < len(phaseW) {
				phaseW[i] = a * evNominalV
				total += phaseW[i]
			}
		}
		d.publishPhases(total, phaseW)
		log.Printf("simulator[%s]: EV drawing %.0fW (%.1f/%.1f/%.1fA), battery %.1f%%",
			d.id, total, currentAt(evCurrents, 0), currentAt(evCurrents, 1), currentAt(evCurrents, 2), d.ev.SoC())
		return
	}

	// Two-phase split: L1 and L2 carry the load, L3 idles. A real single/two-phase EV charger
	// on a three-phase connection looks exactly like this, and a deliberately asymmetric
	// simulated device is what lets the phase-balance panel and the asymmetry test scenarios
	// demonstrate anything without hardware. 230 V nominal per phase.
	const nominalV = 230.0
	d.publishPhases(power, []float64{power / 2, power / 2, 0})
	log.Printf("simulator[%s]: reporting %.0fW (L1 %.0fW / L2 %.0fW / L3 %.0fW)", d.id, power, power/2, power/2, 0.0)
}

func currentAt(values []float64, i int) float64 {
	if i < len(values) {
		return values[i]
	}
	return 0
}

// publishPhases writes one power/current/voltage picture into MPC.
func (d *Device) publishPhases(power float64, phaseW []float64) {
	const nominalV = 230.0
	for len(phaseW) < 3 {
		phaseW = append(phaseW, 0)
	}
	if err := d.mpc.Update(
		d.mpc.UpdateDataPowerTotal(power, nil, nil),
		d.mpc.UpdateDataPowerPhaseA(phaseW[0], nil, nil),
		d.mpc.UpdateDataPowerPhaseB(phaseW[1], nil, nil),
		d.mpc.UpdateDataPowerPhaseC(phaseW[2], nil, nil),
		d.mpc.UpdateDataCurrentPhaseA(phaseW[0]/nominalV, nil, nil),
		d.mpc.UpdateDataCurrentPhaseB(phaseW[1]/nominalV, nil, nil),
		d.mpc.UpdateDataCurrentPhaseC(phaseW[2]/nominalV, nil, nil),
		d.mpc.UpdateDataVoltagePhaseA(nominalV, nil, nil),
		d.mpc.UpdateDataVoltagePhaseB(nominalV, nil, nil),
		d.mpc.UpdateDataVoltagePhaseC(nominalV, nil, nil),
		d.mpc.UpdateDataFrequency(50, nil, nil),
	); err != nil {
		log.Printf("simulator[%s]: reporting %.0fW failed: %v", d.id, power, err)
	}
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
