package simulator

import (
	"testing"
	"time"

	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/spine"

	"github.com/marck-brusa/eebuster/internal/config"
)

// newTestEV builds a vehicle on a throwaway local device, with no service or network: enough
// to assert what the SPINE features actually hold, which is what a CEM will read.
func newTestEV(t *testing.T, cfg config.SimulatedEV) *evSim {
	t.Helper()
	device := spine.NewDeviceLocal("SIM", "test", "test", "test", "test",
		model.DeviceTypeTypeChargingStation, model.NetworkManagementFeatureSetTypeSmart)
	evse := spine.NewEntityLocal(device, model.EntityTypeTypeEVSE, []model.AddressEntityType{1}, time.Second*4)
	device.AddEntity(evse)
	cfg.Enabled = true
	ev, err := newEVSim("test", cfg, device, evse)
	if err != nil {
		t.Fatalf("building the simulated EV: %v", err)
	}
	return ev
}

// measurementValues reads the Measurement server feature back the way a remote CEM does.
func measurementValues(t *testing.T, ev *evSim) map[model.MeasurementIdType]float64 {
	t.Helper()
	feature := ev.entity.FeatureOfTypeAndRole(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	if feature == nil {
		t.Fatal("the EV has no Measurement server feature")
	}
	data, err := spine.LocalFeatureDataCopyOfType[*model.MeasurementListDataType](feature, model.FunctionTypeMeasurementListData)
	if err != nil || data == nil {
		t.Fatalf("reading measurement data: %v", err)
	}
	out := map[model.MeasurementIdType]float64{}
	for _, item := range data.MeasurementData {
		if item.MeasurementId != nil && item.Value != nil {
			out[*item.MeasurementId] = item.Value.GetValue()
		}
	}
	return out
}

// The vehicle must actually publish what a CEM reads: a state of charge, a charged energy,
// and a current per phase. Without this the EV announces four use cases and answers every
// read with an empty list -- which is exactly the failure this test was written to catch.
func TestEVPublishesMeasurements(t *testing.T) {
	ev := newTestEV(t, config.SimulatedEV{SoCStartPercent: 42, MaxCurrentA: 16, Phases: 3})
	values := measurementValues(t, ev)

	if len(values) == 0 {
		t.Fatal("the EV published no measurements at all")
	}
	if soc, ok := values[ev.socID]; !ok || soc != 42 {
		t.Errorf("state of charge: got %v (present=%v), want 42", values[ev.socID], ok)
	}
	if _, ok := values[ev.energyID]; !ok {
		t.Error("charged energy is missing")
	}
	for i, id := range ev.currentIDs {
		current, ok := values[id]
		if !ok {
			t.Errorf("phase %d current is missing", i)
			continue
		}
		if current != 16 {
			t.Errorf("phase %d current: got %v, want 16", i, current)
		}
	}
}

// A curtailment the CEM writes has to reach the battery: below the vehicle's own minimum it
// pauses instead of undercutting, and the station's own limit applies the same way.
func TestEVFollowsLimits(t *testing.T) {
	ev := newTestEV(t, config.SimulatedEV{MaxCurrentA: 16, MinCurrentA: 6, Phases: 3})

	ev.mu.Lock()
	ev.obligations.on[0], ev.obligations.valueA[0] = true, 10 // obligation on L1 only
	ev.mu.Unlock()
	got := ev.Currents()
	if got[0] != 10 || got[1] != 16 {
		t.Errorf("asymmetric obligation: got %v, want L1 10A and the others 16A", got)
	}

	ev.mu.Lock()
	ev.obligations.on[0], ev.obligations.valueA[0] = true, 3 // below the vehicle's minimum
	ev.mu.Unlock()
	if got := ev.Currents(); got[0] != 0 {
		t.Errorf("a curtailment under the minimum must pause the phase, got %v", got)
	}

	ev.mu.Lock()
	ev.obligations.on[0] = false
	ev.stationA = 8 // the station's own LPC limit, shared per phase
	ev.mu.Unlock()
	for i, a := range ev.Currents() {
		if a != 8 {
			t.Errorf("station limit: phase %d got %v, want 8", i, a)
		}
	}
}

// Without a trustworthy Energy Guard the vehicle must not keep drawing what its curtailment
// allowed: once the guard's heartbeat has stayed away for more than guardTimeout after having
// been seen, or while the guard announces a failure, the vehicle holds its safe current, its
// minimum, and follows the limits again once the guard is back (OPEV scenarios 2 and 3).
func TestEVHoldsSafeCurrentWithoutGuard(t *testing.T) {
	ev := newTestEV(t, config.SimulatedEV{MaxCurrentA: 16, MinCurrentA: 6, Phases: 3})
	expect := func(what string, want float64) {
		t.Helper()
		for i, a := range ev.Currents() {
			if a != want {
				t.Errorf("%s: phase %d got %v A, want %v", what, i, a, want)
			}
		}
	}
	expect("no guard seen yet", 16)

	ev.mu.Lock()
	ev.guardSeen = time.Now().Add(-guardTimeout - time.Second)
	ev.mu.Unlock()
	expect("heartbeat lost", 6)
	if reason := ev.guardMissing(); reason != "heartbeat" {
		t.Errorf("reason = %q, want heartbeat", reason)
	}

	ev.guardHeartbeat()
	expect("heartbeat back", 16)

	ev.mu.Lock()
	ev.guardFailed = true
	ev.mu.Unlock()
	expect("guard failed", 6)

	ev.mu.Lock()
	ev.guardFailed = false
	ev.obligations.on[0], ev.obligations.valueA[0] = true, 0 // the pause signal outranks the safe current
	ev.guardSeen = time.Now().Add(-guardTimeout - time.Second)
	ev.mu.Unlock()
	if got := ev.Currents(); got[0] != 0 || got[1] != 6 {
		t.Errorf("pause under heartbeat loss: got %v, want L1 0A and the others 6A", got)
	}
}

// The default vehicle charges in real time, so it is still charging after a long test run.
func TestEVDefaultsChargeInRealTime(t *testing.T) {
	if cfg := evDefaults(config.SimulatedEV{}); cfg.ChargeSpeedup != 1 {
		t.Errorf("charge_speedup default = %v, want 1", cfg.ChargeSpeedup)
	}
}

// A recommendation is the self-produced current the vehicle should charge with, as long as it
// trusts the CEM; an obligation still caps it, and a CEM that is gone is not followed.
func TestEVFollowsRecommendationsWhileTheCEMIsTrusted(t *testing.T) {
	ev := newTestEV(t, config.SimulatedEV{MaxCurrentA: 16, MinCurrentA: 6, Phases: 3})
	ev.mu.Lock()
	ev.recommendations.on[0], ev.recommendations.valueA[0] = true, 9
	ev.recommendations.on[1], ev.recommendations.valueA[1] = true, 12
	ev.obligations.on[1], ev.obligations.valueA[1] = true, 10
	ev.mu.Unlock()
	if got := ev.Currents(); got[0] != 9 || got[1] != 10 || got[2] != 16 {
		t.Errorf("recommendations: got %v, want L1 9A, L2 10A (obligation), L3 16A", got)
	}

	ev.mu.Lock()
	ev.obligations.on[1] = false
	ev.guardFailed = true
	ev.mu.Unlock()
	if got := ev.Currents(); got[0] != 6 || got[1] != 6 {
		t.Errorf("failed CEM: got %v, want the safe current of 6A, recommendations ignored", got)
	}
}
