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
	ev.limitOn[0], ev.curtailedA[0] = true, 10 // obligation on L1 only
	ev.mu.Unlock()
	got := ev.Currents()
	if got[0] != 10 || got[1] != 16 {
		t.Errorf("asymmetric obligation: got %v, want L1 10A and the others 16A", got)
	}

	ev.mu.Lock()
	ev.limitOn[0], ev.curtailedA[0] = true, 3 // below the vehicle's minimum
	ev.mu.Unlock()
	if got := ev.Currents(); got[0] != 0 {
		t.Errorf("a curtailment under the minimum must pause the phase, got %v", got)
	}

	ev.mu.Lock()
	ev.limitOn[0] = false
	ev.stationA = 8 // the station's own LPC limit, shared per phase
	ev.mu.Unlock()
	for i, a := range ev.Currents() {
		if a != 8 {
			t.Errorf("station limit: phase %d got %v, want 8", i, a)
		}
	}
}
