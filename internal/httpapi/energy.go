package httpapi

import (
	"net/http"
	"strconv"

	"github.com/marck-brusa/eebuster/internal/eebusgo"
	"github.com/marck-brusa/eebuster/internal/telemetry"
)

func (s *Server) handlePeerProfile(w http.ResponseWriter, r *http.Request) {
	profile, err := s.stack.PeerProfile(r.PathValue("ski"))
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, profile)
}

func (s *Server) handleEnergySnapshot(w http.ResponseWriter, r *http.Request) {
	ski := r.PathValue("ski")
	snap, err := s.stack.EnergySnapshot(ski)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	s.telemetry.Record(ski, snapshotSource(snap))
	writeJSON(w, http.StatusOK, snap)
}

// snapshotSource adapts an eebusgo.Snapshot to telemetry.SnapshotSource -- kept as an
// explicit mapping, not a shared type, so telemetry stays independent of eebusgo (see
// telemetry.SnapshotSource's doc comment).
func snapshotSource(snap eebusgo.Snapshot) telemetry.SnapshotSource {
	current, voltage, soc := phaseSeries(snap)
	return telemetry.SnapshotSource{
		Ts: snap.Ts, ConsumptionW: snap.Power.ConsumptionW, GridW: snap.Power.GridW,
		PVW: snap.Power.PVW, BatteryW: snap.Power.BatteryW, EVW: snap.Power.EVW,
		ConsumptionLimitW: snap.Limits.ConsumptionW, ProductionLimitW: snap.Limits.ProductionW,
		EVConnectedCount: snap.EV.ConnectedCount, EVChargingCount: snap.EV.ChargingCount,
		CurrentPerPhaseA: current, VoltagePerPhaseV: voltage, StateOfCharge: soc,
	}
}

// phaseSeries picks the per-phase current and voltage to record, preferring the metered
// side (MPC/MGCP, present whenever the device meters at all) and falling back to the EV's
// own charging measurements (EVCEM), which exist only during a session. Recording one
// series per sample keeps the history a flat time series -- the chart draws phases, not
// entities, and a device metering the same phases twice would otherwise double the lines.
func phaseSeries(snap eebusgo.Snapshot) (current, voltage []float64, soc *float64) {
	take := func(entries []map[string]any) {
		for _, fields := range entries {
			if current == nil {
				if v, ok := fields["current_per_phase_a"].([]float64); ok && len(v) > 0 {
					current = v
				}
			}
			if voltage == nil {
				if v, ok := fields["voltage_per_phase_v"].([]float64); ok && len(v) > 0 {
					voltage = v
				}
			}
		}
	}
	take(snap.Monitoring)
	take(snap.Grid)
	for _, ev := range snap.EV.Vehicles {
		if current == nil && len(ev.CurrentPerPhaseA) > 0 {
			current = ev.CurrentPerPhaseA
		}
		// One SoC per sample: the first vehicle reporting one. Several EVs on one peer is
		// rare enough that a single line beats an unbounded set of them.
		if soc == nil && ev.StateOfCharge != nil {
			soc = ev.StateOfCharge
		}
	}
	return current, voltage, soc
}

func (s *Server) handleEnergyHistory(w http.ResponseWriter, r *http.Request) {
	ski := r.PathValue("ski")
	seconds := 3600.0
	if raw := r.URL.Query().Get("seconds"); raw != "" {
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			seconds = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ski": ski, "seconds": seconds, "samples": s.telemetry.History(ski, seconds),
	})
}

func (s *Server) handleEnergyHistoryClear(w http.ResponseWriter, r *http.Request) {
	ski := r.PathValue("ski")
	s.telemetry.Clear(ski)
	writeJSON(w, http.StatusOK, map[string]string{"cleared": ski})
}
