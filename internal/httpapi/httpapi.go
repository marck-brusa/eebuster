// Package httpapi is the REST surface the dashboard (internal/webui) talks to, and the same
// API the sample scripts and any external tooling drive. Route shapes are stable: treat them
// as the tool's public contract.
package httpapi

import (
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"

	"github.com/marck-brusa/eebuster/internal/config"
	"github.com/marck-brusa/eebuster/internal/eebusgo"
	"github.com/marck-brusa/eebuster/internal/logbuf"
	"github.com/marck-brusa/eebuster/internal/openapi"
	"github.com/marck-brusa/eebuster/internal/telemetry"
	"github.com/marck-brusa/eebuster/internal/templates"
	"github.com/marck-brusa/eebuster/internal/trace"
	"github.com/marck-brusa/eebuster/internal/truststore"
	"github.com/marck-brusa/eebuster/internal/webui"
)

type Server struct {
	cfgMu        sync.RWMutex
	cfg          *config.Config
	configPath   string
	scenariosDir string
	stack        *eebusgo.Stack
	trust        *truststore.Store
	telemetry    *telemetry.Store
	logs         *logbuf.Buffer
	frames       *trace.Store
	mux          *http.ServeMux
}

// New builds the server. trust may be nil, in which case runtime trust decisions are not
// persisted and behave as they did before (forgotten on restart). frames may be nil, in which
// case the trace endpoints report an empty store.
func New(cfg *config.Config, configPath, scenariosDir string, logs *logbuf.Buffer, stack *eebusgo.Stack, trust *truststore.Store, frames *trace.Store) *Server {
	if frames == nil {
		frames = trace.New()
	}
	s := &Server{
		cfg: cfg, configPath: configPath, scenariosDir: scenariosDir, stack: stack,
		telemetry: telemetry.New(), logs: logs, mux: http.NewServeMux(), trust: trust, frames: frames,
	}
	s.routes()
	return s
}

// config returns the currently loaded config, safe to call concurrently with a reload.
func (s *Server) config() *config.Config {
	s.cfgMu.RLock()
	defer s.cfgMu.RUnlock()
	return s.cfg
}

func (s *Server) Handler() http.Handler { return s.mux }

func (s *Server) routes() {
	// "/{$}" anchors to the exact root path -- a bare "GET /" is a subtree match in Go's
	// mux (net/http, 1.22+) and silently swallows every unregistered path as this handler,
	// turning what should be a 404 into a 302, and turning any non-GET request to that same
	// unregistered path into a misleading 405 "Method Not Allowed" instead of a 404. Caught
	// by a real request to an unimplemented endpoint (POST /api/v1/discover), not by reading
	// this code.
	s.mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui", http.StatusFound)
	})
	s.mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(webui.UI)
	})
	openapi.RegisterRoutes(s.mux)

	s.mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	s.mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	s.mux.HandleFunc("GET /api/v1/config", s.handleConfig)
	s.mux.HandleFunc("POST /api/v1/config/validate", s.handleConfigValidate)
	s.mux.HandleFunc("POST /api/v1/config/reload", s.handleConfigReload)
	s.mux.HandleFunc("PUT /api/v1/config/active-stack", s.handleActiveStack)
	s.mux.HandleFunc("GET /api/v1/stacks", s.handleStacks)
	s.mux.HandleFunc("GET /api/v1/stacks/{id}", s.handleStackGet)
	s.mux.HandleFunc("POST /api/v1/stacks/{id}/start", s.handleStackStart)
	s.mux.HandleFunc("POST /api/v1/stacks/{id}/stop", s.handleStackStop)
	s.mux.HandleFunc("GET /api/v1/stacks/{id}/logs", s.handleStackLogs)
	s.mux.HandleFunc("GET /api/v1/identity", s.handleIdentity)
	s.mux.HandleFunc("GET /api/v1/peers", s.handlePeers)
	s.mux.HandleFunc("GET /api/v1/peers/pending", s.handlePeersPending)
	s.mux.HandleFunc("GET /api/v1/peers/visible", s.handlePeersVisible)
	s.mux.HandleFunc("GET /api/v1/peers/{ski}/usecases", s.handlePeerUseCases)
	s.mux.HandleFunc("GET /api/v1/peers/{ski}/profile", s.handlePeerProfile)
	s.mux.HandleFunc("GET /api/v1/energy/{ski}/snapshot", s.handleEnergySnapshot)
	s.mux.HandleFunc("GET /api/v1/energy/{ski}/history", s.handleEnergyHistory)
	s.mux.HandleFunc("DELETE /api/v1/energy/{ski}/history", s.handleEnergyHistoryClear)
	s.mux.HandleFunc("GET /api/v1/templates", s.handleTemplates)
	s.mux.HandleFunc("POST /api/v1/peers/{ski}/trust", s.handleTrust)
	s.mux.HandleFunc("DELETE /api/v1/peers/{ski}/trust", s.handleUntrust)
	s.mux.HandleFunc("POST /api/v1/peers/{ski}/deny", s.handleDenyPairing)
	s.mux.HandleFunc("POST /api/v1/discover", s.handleDiscover)
	s.mux.HandleFunc("GET /api/v1/diagnostics/network", s.handleNetworkDiagnostics)

	s.mux.HandleFunc("GET /api/v1/events/recent", s.handleEventsRecent)
	s.mux.HandleFunc("DELETE /api/v1/events", s.handleEventsClear)
	s.mux.HandleFunc("GET /api/v1/events/stream", s.handleEventsStream)

	s.mux.HandleFunc("GET /api/v1/trace", s.handleTraceList)
	s.mux.HandleFunc("GET /api/v1/trace/summary", s.handleTraceSummary)
	s.mux.HandleFunc("GET /api/v1/trace/{seq}", s.handleTraceEntry)
	s.mux.HandleFunc("DELETE /api/v1/trace", s.handleTraceClear)

	s.mux.HandleFunc("GET /api/v1/scenarios", s.handleScenariosList)
	s.mux.HandleFunc("GET /api/v1/scenarios/catalog", s.handleScenariosCatalog)
	s.mux.HandleFunc("POST /api/v1/scenarios/{name}/run", s.handleScenarioRun)
	s.mux.HandleFunc("POST /api/v1/scenarios/run-all", s.handleScenariosRunAll)

	s.registerUsecaseRoutes()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("httpapi: encoding response: %v", err)
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Version is stamped at build time by the release workflow:
//
//	-ldflags "-X github.com/marck-brusa/eebuster/internal/httpapi.Version=1.0.0"
//
// It stays "dev" for a plain `go build`, so a locally built binary is never mistaken for a
// released one in a bug report.
var Version = "dev"

func (s *Server) handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"name": "eebus-testbench", "version": Version})
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.config())
}

func (s *Server) handleIdentity(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		stackId: map[string]string{"ski": s.stack.LocalSKI()},
	})
}

// stackId is the only stack this rewrite has: openeebus-hems and EEBusTracer were cut from
// scope, so there is exactly one counterparty and no supervisor -- it's the process itself.
const stackId = "eebus-go-remote"

// stackSummary matches the JSON shape _stack_summary() in src/facade/api/routes_stacks.py
// produced, so the existing dashboard code renders it without changes.
type stackSummary struct {
	ID           string          `json:"id"`
	Role         string          `json:"role"`
	Primary      bool            `json:"primary"`
	Drivable     bool            `json:"drivable"`
	Enabled      bool            `json:"enabled"`
	Status       string          `json:"status"`
	LocalSKI     *string         `json:"local_ski"`
	Ports        map[string]int  `json:"ports"`
	Capabilities map[string]bool `json:"capabilities"`
}

// capabilities matches JsonRpcAdapter.capabilities().as_dict()'s key set and values exactly.
// approve_deny stays false for the same reason it was in the original: cs/lpc's
// ApproveOrDenyConsumptionLimit needs a CS-role entity, which this stack (playing EG) never
// registers.
var capabilities = map[string]bool{
	"live_control": true,
	"lpc.read":     true, "lpc.write": true, "lpc.failsafe": true, "lpc.nominal_max": true,
	"lpp.read": true, "lpp.write": true,
	"mpc.read": true, "mgcp.read": true,
	"opev.read": true, "opev.write": true,
	"oscev.read": true, "oscev.write": true,
	"ohpcf.read":       true,
	"heartbeat":        true,
	"approve_deny":     false,
	"multi_peer":       true,
	"pairing_approval": true,
	"device_profile":   true,
	"energy_snapshot":  true,
}

func (s *Server) stackSummary() stackSummary {
	ski := s.stack.LocalSKI()
	status := "stopped"
	if s.stack.Running() {
		status = "running"
	}
	return stackSummary{
		ID: stackId, Role: "counterparty", Primary: true, Drivable: true,
		Enabled: true, Status: status, LocalSKI: &ski,
		Ports:        map[string]int{"ship_port": s.stack.ShipPort()},
		Capabilities: capabilities,
	}
}

func (s *Server) handleStacks(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, []stackSummary{s.stackSummary()})
}

func (s *Server) handleStackGet(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != stackId {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_stack", "stack": r.PathValue("id")})
		return
	}
	writeJSON(w, http.StatusOK, s.stackSummary())
}

// handleStackStart/Stop actually start and shut down the embedded eebus-go stack -- not a
// no-op placeholder -- since Service.Start()/Shutdown() are idempotent and safe to call
// again (see service.go's isRunning guard), unlike the old per-process supervisor this
// replaces, there's no subprocess to spawn: the whole SHIP/SPINE stack pauses and resumes in
// place.
func (s *Server) handleStackStart(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != stackId {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_stack", "stack": r.PathValue("id")})
		return
	}
	if err := s.stack.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "start_failed", "detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, s.stackSummary())
}

func (s *Server) handleStackStop(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != stackId {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_stack", "stack": r.PathValue("id")})
		return
	}
	s.stack.Shutdown()
	writeJSON(w, http.StatusOK, s.stackSummary())
}

// handleStackLogs matches routes_stacks.py's GET /stacks/{id}/logs?tail=N contract, but reads
// from this process's own in-memory ring buffer instead of tailing a per-process log file --
// there's only one process now, no supervisor writing separate files per stack.
func (s *Server) handleStackLogs(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != stackId {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_stack", "stack": r.PathValue("id")})
		return
	}
	tail := 200
	if raw := r.URL.Query().Get("tail"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			tail = n
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": s.logs.Tail(tail)})
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stack.Peers())
}

func (s *Server) handlePeersPending(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stack.PendingPairings())
}

func (s *Server) handlePeersVisible(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stack.VisiblePeers())
}

func (s *Server) handleTrust(w http.ResponseWriter, r *http.Request) {
	ski := r.PathValue("ski")
	s.stack.Trust(ski)
	// Trust only *starts* the dial, so a 200 here means "accepted", not "connected". Watch in
	// the background and publish a lifecycle event carrying the real dial error if it never
	// connects -- otherwise the UI shows a bare success and the silent failure that follows
	// looks like the app lying. Mirrors the Python facade's watch_connection_after_trust.
	go s.stack.WatchConnectionAfterTrust(ski)
	// Persist so the decision survives a restart. Without this, trusting through the API or
	// dashboard was in-memory only and a restart left the tool idle beside a device it had
	// just paired with; the config's peers: list was the only durable route, and that needs a
	// literal SKI, which changes whenever the device is re-imaged.
	persisted := false
	if s.trust != nil {
		if _, err := s.trust.Add(ski); err != nil {
			log.Printf("truststore: could not persist trust for %s: %v", ski, err)
		} else {
			persisted = true
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"trusted":   ski,
		"persisted": persisted,
		"note":      "connection attempt started; watch GET /api/v1/events/stream for the outcome",
	})
}

func (s *Server) handleUntrust(w http.ResponseWriter, r *http.Request) {
	ski := r.PathValue("ski")
	s.stack.Untrust(ski)
	if s.trust != nil {
		if _, err := s.trust.Remove(ski); err != nil {
			log.Printf("truststore: could not persist untrust for %s: %v", ski, err)
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"untrusted": ski})
}

func (s *Server) handleDenyPairing(w http.ResponseWriter, r *http.Request) {
	ski := r.PathValue("ski")
	s.stack.DenyPairing(ski)
	writeJSON(w, http.StatusOK, map[string]string{"denied": ski})
}

func (s *Server) handlePeerUseCases(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.stack.PeerUseCases(r.PathValue("ski")))
}

func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, templates.All())
}
