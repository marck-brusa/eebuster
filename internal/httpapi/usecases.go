package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/marck-brusa/eebuster/internal/eebusgo"
)

func (s *Server) registerUsecaseRoutes() {
	s.mux.HandleFunc("GET /api/v1/lpc/{ski}/limit", s.handleLPCReadLimit)
	s.mux.HandleFunc("PUT /api/v1/lpc/{ski}/limit", s.handleLPCWriteLimit)
	s.mux.HandleFunc("GET /api/v1/lpc/{ski}/failsafe", s.handleLPCReadFailsafe)
	s.mux.HandleFunc("PUT /api/v1/lpc/{ski}/failsafe", s.handleLPCWriteFailsafe)
	s.mux.HandleFunc("GET /api/v1/lpc/{ski}/nominal-max", s.handleLPCNominalMax)
	s.mux.HandleFunc("POST /api/v1/lpc/heartbeat/start", s.handleLPCHeartbeatStart)
	s.mux.HandleFunc("POST /api/v1/lpc/heartbeat/stop", s.handleLPCHeartbeatStop)
	s.mux.HandleFunc("GET /api/v1/lpc/{ski}/heartbeat", s.handleLPCHeartbeatStatus)

	s.mux.HandleFunc("GET /api/v1/lpp/{ski}/limit", s.handleLPPReadLimit)
	s.mux.HandleFunc("PUT /api/v1/lpp/{ski}/limit", s.handleLPPWriteLimit)
	s.mux.HandleFunc("GET /api/v1/lpp/{ski}/failsafe", s.handleLPPReadFailsafe)
	s.mux.HandleFunc("PUT /api/v1/lpp/{ski}/failsafe", s.handleLPPWriteFailsafe)
	s.mux.HandleFunc("GET /api/v1/lpp/{ski}/nominal-max", s.handleLPPNominalMax)

	s.mux.HandleFunc("GET /api/v1/mpc/{ski}", s.handleMPCRead)
	s.mux.HandleFunc("GET /api/v1/opev/{ski}", s.handleOPEVRead)
	s.mux.HandleFunc("PUT /api/v1/opev/{ski}/limits", s.handleOPEVWriteLimits)
	s.mux.HandleFunc("GET /api/v1/oscev/{ski}", s.handleOSCEVRead)
	s.mux.HandleFunc("PUT /api/v1/oscev/{ski}/limits", s.handleOSCEVWriteLimits)
	s.mux.HandleFunc("GET /api/v1/ohpcf/{ski}", s.handleOHPCFRead)
	s.mux.HandleFunc("GET /api/v1/evsecc/{ski}", s.handleEVSECCRead)
	s.mux.HandleFunc("POST /api/v1/opev/heartbeat/start", s.handleOPEVHeartbeatStart)
	s.mux.HandleFunc("POST /api/v1/opev/heartbeat/stop", s.handleOPEVHeartbeatStop)
	s.mux.HandleFunc("PUT /api/v1/opev/operating-state", s.handleOPEVOperatingState)
	s.mux.HandleFunc("POST /api/v1/oscev/heartbeat/start", s.handleOSCEVHeartbeatStart)
	s.mux.HandleFunc("POST /api/v1/oscev/heartbeat/stop", s.handleOSCEVHeartbeatStop)
	s.mux.HandleFunc("PUT /api/v1/oscev/operating-state", s.handleOSCEVOperatingState)
	s.mux.HandleFunc("GET /api/v1/mgcp/{ski}", s.handleMGCPRead)
}

// entityHint parses ?entity=1 or ?entity=1,2, matching routes_lpc.py's _parse_entity_hint.
func entityHint(r *http.Request) ([]uint, error) {
	raw := r.URL.Query().Get("entity")
	if raw == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	out := make([]uint, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		n, err := strconv.ParseUint(p, 10, 64)
		if err != nil {
			return nil, err
		}
		out = append(out, uint(n))
	}
	return out, nil
}

// writeUsecaseError maps use-case errors to the same status codes app.py's exception
// handlers used: entity ambiguity -> 409 with candidates, not-found -> 404, anything else
// (a real upstream/SPINE error) -> 502, matching "peer_rpc_error" semantics.
func writeUsecaseError(w http.ResponseWriter, err error) {
	if e, ok := err.(*eebusgo.EntityAmbiguousError); ok {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "entity_ambiguous", "ski": e.SKI, "candidates": e.Candidates,
		})
		return
	}
	if e, ok := err.(*eebusgo.NotFoundError); ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "detail": e.Detail})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]string{"error": "peer_rpc_error", "detail": err.Error()})
}

func decodeBody(r *http.Request, v any) error {
	return json.NewDecoder(r.Body).Decode(v)
}

func (s *Server) handleLPCReadLimit(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	limit, err := s.stack.LPC().ReadLimit(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}

func (s *Server) handleLPCWriteLimit(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	var body eebusgo.LoadLimit
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.LPC().WriteLimit(r.PathValue("ski"), body, hint); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "sent": body})
}

func (s *Server) handleLPCReadFailsafe(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	failsafe, err := s.stack.LPC().ReadFailsafe(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, failsafe)
}

func (s *Server) handleLPCWriteFailsafe(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	var body eebusgo.FailsafeLimit
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.LPC().WriteFailsafe(r.PathValue("ski"), body, hint); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "sent": body})
}

func (s *Server) handleLPCNominalMax(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	value, err := s.stack.LPC().NominalMax(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]float64{"value_w": value})
}

func (s *Server) handleLPCHeartbeatStart(w http.ResponseWriter, r *http.Request) {
	s.stack.LPC().StartHeartbeat()
	writeJSON(w, http.StatusOK, map[string]string{"heartbeat": "started"})
}

func (s *Server) handleLPCHeartbeatStop(w http.ResponseWriter, r *http.Request) {
	s.stack.LPC().StopHeartbeat()
	writeJSON(w, http.StatusOK, map[string]string{"heartbeat": "stopped"})
}

func (s *Server) handleLPCHeartbeatStatus(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	within, err := s.stack.LPC().HeartbeatStatus(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"within_duration": within})
}

func (s *Server) handleLPPReadLimit(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	limit, err := s.stack.LPP().ReadLimit(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, limit)
}

func (s *Server) handleLPPWriteLimit(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	var body eebusgo.LoadLimit
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.LPP().WriteLimit(r.PathValue("ski"), body, hint); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "sent": body})
}

func (s *Server) handleLPPReadFailsafe(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	failsafe, err := s.stack.LPP().ReadFailsafe(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, failsafe)
}

func (s *Server) handleLPPWriteFailsafe(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	var body eebusgo.FailsafeLimit
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.LPP().WriteFailsafe(r.PathValue("ski"), body, hint); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "sent": body})
}

func (s *Server) handleLPPNominalMax(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	value, err := s.stack.LPP().NominalMax(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]float64{"value_w": value})
}

func (s *Server) handleMPCRead(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	result, err := s.stack.MPC().Read(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleMGCPRead(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	result, err := s.stack.MGCP().Read(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// Per-phase charging-current limits. OPEV writes obligations (overload protection: the EV
// must not exceed them), OSCEV writes recommendations (self-consumption optimization: the EV
// may follow them). Same request/response shapes, deliberately.

type phaseLimitsBody struct {
	Limits []eebusgo.PhaseLimit `json:"limits"`
}

func (s *Server) handleOPEVRead(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	result, err := s.stack.OPEV().Read(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleOPEVWriteLimits(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	var body phaseLimitsBody
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.OPEV().WriteLimits(r.PathValue("ski"), body.Limits, hint); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "sent": body.Limits})
}

func (s *Server) handleOSCEVRead(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	result, err := s.stack.OSCEV().Read(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleOSCEVWriteLimits(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	var body phaseLimitsBody
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.OSCEV().WriteLimits(r.PathValue("ski"), body.Limits, hint); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "sent": body.Limits})
}

func (s *Server) handleOHPCFRead(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	result, err := s.stack.OHPCF().Read(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// EVSE station identity (EVSECC scenarios 1+2): manufacturer data and operating state.

func (s *Server) handleEVSECCRead(w http.ResponseWriter, r *http.Request) {
	hint, err := entityHint(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_entity_hint", "detail": err.Error()})
		return
	}
	result, err := s.stack.EVSECC().Read(r.PathValue("ski"), hint)
	if err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// OPEV/OSCEV scenario 2 and 3 controls: our heartbeat towards the EV, and our announced
// operating state. Both are local-side operations (no target entity): the EV observes them
// and must fall back to a safe current when the heartbeat stays away >4s (OPEV-005) or a
// failure state is announced (OPEV-007).

type operatingStateBody struct {
	Failure bool `json:"failure"`
}

func (s *Server) handleOPEVHeartbeatStart(w http.ResponseWriter, r *http.Request) {
	s.stack.OPEV().StartHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{"heartbeat": "started"})
}

func (s *Server) handleOPEVHeartbeatStop(w http.ResponseWriter, r *http.Request) {
	s.stack.OPEV().StopHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{"heartbeat": "stopped"})
}

func (s *Server) handleOPEVOperatingState(w http.ResponseWriter, r *http.Request) {
	var body operatingStateBody
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.OPEV().SetOperatingState(body.Failure); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "failure": body.Failure})
}

func (s *Server) handleOSCEVHeartbeatStart(w http.ResponseWriter, r *http.Request) {
	s.stack.OSCEV().StartHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{"heartbeat": "started"})
}

func (s *Server) handleOSCEVHeartbeatStop(w http.ResponseWriter, r *http.Request) {
	s.stack.OSCEV().StopHeartbeat()
	writeJSON(w, http.StatusOK, map[string]any{"heartbeat": "stopped"})
}

func (s *Server) handleOSCEVOperatingState(w http.ResponseWriter, r *http.Request) {
	var body operatingStateBody
	if err := decodeBody(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if err := s.stack.OSCEV().SetOperatingState(body.Failure); err != nil {
		writeUsecaseError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"accepted": true, "failure": body.Failure})
}
