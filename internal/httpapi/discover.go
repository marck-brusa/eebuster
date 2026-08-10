package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/marck-brusa/eebuster/internal/eebusgo"
)

// handleDiscover matches routes_misc.py's POST /discover contract ({"found": [...],
// "timeout_s": ...}), but runs a real ship-go zeroconf browse directly instead of shelling
// out to EEBusTracer's discover subcommand, which doesn't exist in this rewrite -- see
// eebusgo.Discover's doc comment for why an empty result under WSL2/Docker Desktop is the
// network boundary, not a bug.
func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	timeoutS := 5.0
	if raw := r.URL.Query().Get("timeout_s"); raw != "" {
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			timeoutS = n
		}
	}

	found, err := eebusgo.Discover(s.config().Network.Interface, time.Duration(timeoutS*float64(time.Second)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	// Feed the scan's identity metadata into the stack's cache so a device found here is still
	// named properly in GET /peers after it connects (a connection itself carries no
	// brand/model/serial -- see eebusgo.DeviceMeta).
	s.stack.RecordDiscovered(found)
	writeJSON(w, http.StatusOK, map[string]any{"found": found, "timeout_s": timeoutS})
}
