package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

// handleEventsRecent matches src/facade/api/routes_events.py's GET /events/recent -- the
// ring buffer as plain JSON, for inspecting traffic without leaving the Swagger tab (Swagger
// cannot render a live SSE stream).
func (s *Server) handleEventsRecent(w http.ResponseWriter, r *http.Request) {
	limit := 200
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil {
			limit = n
		}
	}
	writeJSON(w, http.StatusOK, s.stack.Events().Recent(limit, r.URL.Query().Get("level"), r.URL.Query().Get("ski")))
}

func (s *Server) handleEventsClear(w http.ResponseWriter, r *http.Request) {
	s.stack.Events().Clear()
	writeJSON(w, http.StatusOK, map[string]bool{"cleared": true})
}

// handleEventsStream is the live view's Server-Sent Events feed, matching
// src/facade/api/routes_events.py's GET /events/stream. No compression middleware sits in
// front of this (see docs/01-architecture.md) -- gzip buffering would defeat SSE's whole
// point of delivering each event as it happens.
func (s *Server) handleEventsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, cancel := s.stack.Events().Subscribe()
	defer cancel()

	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-ch:
			data, err := json.Marshal(event)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}
