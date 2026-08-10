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
	return telemetry.SnapshotSource{
		Ts: snap.Ts, ConsumptionW: snap.Power.ConsumptionW, GridW: snap.Power.GridW,
		PVW: snap.Power.PVW, BatteryW: snap.Power.BatteryW, EVW: snap.Power.EVW,
		ConsumptionLimitW: snap.Limits.ConsumptionW, ProductionLimitW: snap.Limits.ProductionW,
		EVConnectedCount: snap.EV.ConnectedCount, EVChargingCount: snap.EV.ChargingCount,
	}
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
