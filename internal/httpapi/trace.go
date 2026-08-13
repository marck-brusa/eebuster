package httpapi

import (
	"net/http"
	"strconv"
)

// The message trace endpoints expose the raw-frame ring (internal/trace): what was actually
// on the wire, annotated with conformance findings. List responses strip the raw payload;
// the single-entry lookup returns it in full.

func (s *Server) handleTraceList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	after, _ := strconv.ParseInt(q.Get("after"), 10, 64)
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, latest := s.frames.Recent(after, limit, q.Get("ski"), q.Get("dir"), q.Get("findings") == "only")
	writeJSON(w, http.StatusOK, map[string]any{"entries": entries, "latest_seq": latest})
}

func (s *Server) handleTraceEntry(w http.ResponseWriter, r *http.Request) {
	seq, err := strconv.ParseInt(r.PathValue("seq"), 10, 64)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"detail": "seq must be an integer"})
		return
	}
	entry, ok := s.frames.Get(seq)
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]any{"detail": "frame not retained (the trace keeps a bounded window)"})
		return
	}
	writeJSON(w, http.StatusOK, entry)
}

func (s *Server) handleTraceSummary(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.frames.Summary())
}

func (s *Server) handleTraceClear(w http.ResponseWriter, r *http.Request) {
	s.frames.Clear()
	writeJSON(w, http.StatusOK, map[string]any{"status": "cleared"})
}
