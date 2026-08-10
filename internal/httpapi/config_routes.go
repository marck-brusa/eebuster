package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/marck-brusa/eebuster/internal/config"
	"github.com/marck-brusa/eebuster/internal/staticmdns"
)

// toStaticPeers mirrors cmd/eebus-testbench/serve.go's own conversion of the same shape at
// boot -- kept as a separate copy rather than shared, since importing the cmd package from
// here would be backwards (internal/ shouldn't depend on cmd/).
func toStaticPeers(peers []config.Peer) []staticmdns.Peer {
	out := make([]staticmdns.Peer, 0, len(peers))
	for _, p := range peers {
		out = append(out, staticmdns.Peer{
			Name: p.Name, SKI: p.SKI, Host: p.Host, Port: p.Port, Path: p.Path,
			AutoAccept: p.Trust != "manual",
		})
	}
	return out
}

func (s *Server) handleConfigValidate(w http.ResponseWriter, r *http.Request) {
	problems := s.config().Validate()
	writeJSON(w, http.StatusOK, map[string]any{"valid": len(problems) == 0, "errors": problems})
}

// handleConfigReload re-reads config/eebus.yaml from disk and, if it parses and validates,
// swaps it in and pushes the new peer list into the running stack's static mDNS provider --
// matching ConfigStore's read path (parse, validate) plus what actually needs to happen
// live in this rewrite (there's no supervisor to restart, just the peer list to refresh).
func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	newCfg, err := config.Load(s.configPath)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_config", "errors": []string{err.Error()}})
		return
	}
	if problems := newCfg.Validate(); len(problems) > 0 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid_config", "errors": problems})
		return
	}

	s.cfgMu.Lock()
	s.cfg = newCfg
	s.cfgMu.Unlock()
	s.stack.SetPeers(toStaticPeers(newCfg.Peers))

	writeJSON(w, http.StatusOK, map[string]bool{"reloaded": true})
}

type activeStackIn struct {
	Stack   string `json:"stack"`
	Persist bool   `json:"persist"`
}

// handleActiveStack matches PUT /api/v1/config/active-stack's contract from the dashboard's
// stack toggle (ui.html calls this, not POST /stacks/{id}/start, whenever a counterparty
// isn't running -- see routes_config.py's set_active_stack). openeebus-hems was cut from
// scope, so there's only ever one counterparty to switch to or away from: this reduces to
// "start eebus-go-remote", never anything to stop first.
func (s *Server) handleActiveStack(w http.ResponseWriter, r *http.Request) {
	var body activeStackIn
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
		return
	}
	if body.Stack != stackId {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown_stack", "stack": body.Stack})
		return
	}
	if err := s.stack.Start(); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "start_failed", "detail": err.Error()})
		return
	}
	if body.Persist {
		// No comment-preserving YAML writer in this rewrite yet (see docs/ for the open
		// "surgical edit vs. split state file" decision) -- honest 501 instead of silently
		// no-op-ing and claiming it persisted. The dashboard toggle itself always sends
		// persist:false, so this never fires from normal use.
		writeJSON(w, http.StatusNotImplemented, map[string]string{"error": "persist_not_implemented", "detail": "config write-back isn't built in this rewrite yet"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"stack": body.Stack, "running": true, "persisted": false})
}
