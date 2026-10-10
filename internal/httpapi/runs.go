package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/testrun"
)

// Test runs: a selection of scenarios executed as one documented run, persisted, and
// rendered on request as HTML, JSON, JUnit, CSV or an Excel workbook.

func (s *Server) registerRunRoutes() {
	s.mux.HandleFunc("POST /api/v1/runs", s.handleRunStart)
	s.mux.HandleFunc("GET /api/v1/runs", s.handleRunList)
	s.mux.HandleFunc("GET /api/v1/runs/{id}", s.handleRunGet)
	s.mux.HandleFunc("POST /api/v1/runs/{id}/cancel", s.handleRunCancel)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/report.html", s.handleRunHTML)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/report.json", s.handleRunJSON)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/junit.xml", s.handleRunJUnit)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/report.csv", s.handleRunCSV)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/report.xlsx", s.handleRunXLSX)
	s.mux.HandleFunc("GET /api/v1/runs/{id}/frames.log", s.handleRunFramesLog)
	s.mux.HandleFunc("POST /api/v1/reports/open", s.handleReportsOpen)
}

// SetReportsDir makes finished runs persist as JSON files in dir. Without it they are kept in
// memory until the process exits.
func (s *Server) SetReportsDir(dir string) {
	s.runs = testrun.NewManager(s.loopbackBaseURL, s.scenariosDir, dir, Version, s.publishRunEvent)
}

func (s *Server) publishRunEvent(event, ski string, data map[string]any) {
	if s.stack != nil {
		s.stack.Events().Publish("lifecycle", stackId, event, ski, data)
	}
}

func writeRunError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, testrun.ErrRunning):
		writeJSON(w, http.StatusConflict, map[string]string{"error": "run_in_progress", "detail": err.Error()})
	case errors.Is(err, testrun.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not_found", "detail": err.Error()})
	case errors.Is(err, testrun.ErrNothingSelected):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "nothing_selected", "detail": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "run_failed", "detail": err.Error()})
	}
}

func (s *Server) handleRunStart(w http.ResponseWriter, r *http.Request) {
	var req testrun.Request
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err == nil && len(strings.TrimSpace(string(body))) > 0 {
		err = json.Unmarshal(body, &req)
	}
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request", "detail": err.Error()})
	} else if run, startErr := s.runs.Start(req); startErr != nil {
		writeRunError(w, startErr)
	} else {
		writeJSON(w, http.StatusAccepted, map[string]any{"run_id": run.ID, "status": run.Status, "run": run})
	}
}

// handleRunList lists the reports folder, says where it is -- also as Windows sees it when the
// testbench runs in WSL -- and whether this browser may ask to open it.
func (s *Server) handleRunList(w http.ResponseWriter, r *http.Request) {
	dir := s.absReportsDir()
	writeJSON(w, http.StatusOK, map[string]any{
		"runs": s.runs.List(), "active": s.runs.ActiveID(), "reports_dir": dir,
		"reports_dir_windows": windowsPath(dir, wslDistro()), "can_open_folder": dir != "" && isLoopback(r),
	})
}

func (s *Server) handleRunGet(w http.ResponseWriter, r *http.Request) {
	if run, err := s.runs.Get(r.PathValue("id")); err != nil {
		writeRunError(w, err)
	} else {
		writeJSON(w, http.StatusOK, withoutFrames(run, r.URL.Query().Get("frames") == "1"))
	}
}

// withoutFrames leaves the embedded wire frames out of a run record unless asked for with
// ?frames=1: the dashboard polls this while a run is in progress, and the frames can be
// megabytes. The report formats and the frames log carry them.
func withoutFrames(run *report.Run, keep bool) *report.Run {
	if !keep && len(run.Frames) > 0 {
		trimmed := *run
		trimmed.Frames = nil
		run = &trimmed
	}
	return run
}

func (s *Server) handleRunCancel(w http.ResponseWriter, r *http.Request) {
	if err := s.runs.Cancel(r.PathValue("id")); err != nil {
		writeRunError(w, err)
	} else {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "cancelling", "run_id": r.PathValue("id")})
	}
}

// serveRendered writes a rendered report. HTML opens in the browser unless ?download=1;
// every other format is offered as a file.
func (s *Server) serveRendered(w http.ResponseWriter, r *http.Request, ext, contentType string, render func(*report.Run) ([]byte, error)) {
	run, err := s.runs.Get(r.PathValue("id"))
	var data []byte
	if err == nil {
		data, err = render(run)
	}
	if err != nil {
		writeRunError(w, err)
	} else {
		disposition := "attachment"
		if ext == "html" && r.URL.Query().Get("download") != "1" {
			disposition = "inline"
		}
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("Content-Disposition", fmt.Sprintf("%s; filename=%q", disposition, run.FileBase()+"."+ext))
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(data)
	}
}

func (s *Server) handleRunHTML(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, "html", "text/html; charset=utf-8", report.HTML)
}

func (s *Server) handleRunJSON(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, "json", "application/json", func(run *report.Run) ([]byte, error) {
		return json.MarshalIndent(run, "", "  ")
	})
}

func (s *Server) handleRunJUnit(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, "junit.xml", "application/xml", func(run *report.Run) ([]byte, error) {
		return report.JUnit(run), nil
	})
}

func (s *Server) handleRunCSV(w http.ResponseWriter, r *http.Request) {
	table := r.URL.Query().Get("table")
	if table != "steps" {
		table = "cases"
	}
	s.serveRendered(w, r, table+".csv", "text/csv; charset=utf-8", func(run *report.Run) ([]byte, error) {
		return report.CSV(run, table)
	})
}

func (s *Server) handleRunFramesLog(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, "frames.log", "text/plain; charset=utf-8", report.FramesLog)
}

func (s *Server) handleRunXLSX(w http.ResponseWriter, r *http.Request) {
	s.serveRendered(w, r, "xlsx", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", report.XLSX)
}
