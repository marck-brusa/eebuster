// Package telemetry is an in-memory dashboard history collected while the process runs,
// matching src/facade/telemetry/store.py: twelve hours at the dashboard's five-second
// polling interval, per peer, gone on restart.
package telemetry

import (
	"sync"
	"time"
)

const maxSamplesPerPeer = 8640 // 12h at 5s intervals

// Sample is one recorded point, matching TelemetryStore.record()'s sample shape.
type Sample struct {
	Ts                float64  `json:"ts"`
	PowerW            *float64 `json:"power_w"`
	GridPowerW        *float64 `json:"grid_power_w"`
	PVPowerW          *float64 `json:"pv_power_w"`
	BatteryPowerW     *float64 `json:"battery_power_w"`
	EVPowerW          *float64 `json:"ev_power_w"`
	ConsumptionLimitW *float64 `json:"consumption_limit_w"`
	ProductionLimitW  *float64 `json:"production_limit_w"`
	EVConnected       int      `json:"ev_connected"`
	EVCharging        int      `json:"ev_charging"`
	// Per-phase electrical detail and state of charge, for the dashboard's second chart
	// panel. Nil-safe: a device that does not report them simply has no line to draw.
	CurrentPerPhaseA []float64 `json:"current_per_phase_a,omitempty"`
	VoltagePerPhaseV []float64 `json:"voltage_per_phase_v,omitempty"`
	StateOfCharge    *float64  `json:"state_of_charge,omitempty"`
}

// SnapshotSource is the minimal shape Record needs from an eebusgo.Snapshot, kept as its own
// interface so this package doesn't import eebusgo (which would create a dependency the
// telemetry store has no other reason to have).
type SnapshotSource struct {
	Ts                float64
	ConsumptionW      *float64
	GridW             *float64
	PVW               *float64
	BatteryW          *float64
	EVW               *float64
	ConsumptionLimitW *float64
	ProductionLimitW  *float64
	EVConnectedCount  int
	EVChargingCount   int
	CurrentPerPhaseA  []float64
	VoltagePerPhaseV  []float64
	StateOfCharge     *float64
}

type Store struct {
	mu      sync.Mutex
	samples map[string][]Sample
}

func New() *Store {
	return &Store{samples: map[string][]Sample{}}
}

func (s *Store) Record(ski string, snap SnapshotSource) Sample {
	sample := Sample{
		Ts: snap.Ts, PowerW: snap.ConsumptionW, GridPowerW: snap.GridW, PVPowerW: snap.PVW,
		BatteryPowerW: snap.BatteryW, EVPowerW: snap.EVW,
		ConsumptionLimitW: snap.ConsumptionLimitW, ProductionLimitW: snap.ProductionLimitW,
		EVConnected: snap.EVConnectedCount, EVCharging: snap.EVChargingCount,
		CurrentPerPhaseA: snap.CurrentPerPhaseA, VoltagePerPhaseV: snap.VoltagePerPhaseV,
		StateOfCharge: snap.StateOfCharge,
	}
	if sample.Ts == 0 {
		sample.Ts = float64(time.Now().UnixNano()) / 1e9
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	list := append(s.samples[ski], sample)
	if len(list) > maxSamplesPerPeer {
		list = list[len(list)-maxSamplesPerPeer:]
	}
	s.samples[ski] = list
	return sample
}

func (s *Store) History(ski string, seconds float64) []Sample {
	if seconds < 1 {
		seconds = 1
	}
	cutoff := float64(time.Now().UnixNano())/1e9 - seconds

	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Sample
	for _, sample := range s.samples[ski] {
		if sample.Ts >= cutoff {
			out = append(out, sample)
		}
	}
	return out
}

func (s *Store) Clear(ski string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.samples, ski)
}
