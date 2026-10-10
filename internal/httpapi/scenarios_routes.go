package httpapi

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/scenario"
	"github.com/marck-brusa/eebuster/internal/testrun"
)

// scenarioPaths lists *.yaml files in dir, smoke/discovery first, matching
// routes_scenarios.py's _scenario_paths ordering exactly (the dashboard's list should render
// in the same order the runner executes them in).
func scenarioPaths(dir string) []string {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.yaml"))
	priority := map[string]int{"smoke-pairing": 0, "device-profile-discovery": 1}
	sort.Slice(matches, func(i, j int) bool {
		pi, pj := priorityFor(matches[i], priority), priorityFor(matches[j], priority)
		if pi != pj {
			return pi < pj
		}
		return matches[i] < matches[j]
	})
	return matches
}

func priorityFor(path string, priority map[string]int) int {
	stem := strings.TrimSuffix(filepath.Base(path), ".yaml")
	if p, ok := priority[stem]; ok {
		return p
	}
	return 10
}

func (s *Server) handleScenariosList(w http.ResponseWriter, r *http.Request) {
	paths := scenarioPaths(s.scenariosDir)
	names := make([]string, 0, len(paths))
	for _, p := range paths {
		names = append(names, strings.TrimSuffix(filepath.Base(p), ".yaml"))
	}
	sort.Strings(names)
	writeJSON(w, http.StatusOK, names)
}

// handleScenariosCatalog lists every scenario in run order with what it checks and the
// specification it refers to. A file that does not parse is listed as invalid.
func (s *Server) handleScenariosCatalog(w http.ResponseWriter, r *http.Request) {
	metas, _ := scenario.Catalog(s.scenariosDir)
	writeJSON(w, http.StatusOK, metas)
}

// loopbackBaseURL matches routes_scenarios.py's own rationale exactly: always loopback,
// regardless of api.bind (0.0.0.0 isn't dialable as a client target), but the port must
// match whatever this process actually bound.
func (s *Server) loopbackBaseURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(s.config().API.Port)
}

// handleScenarioRun runs one scenario as a run of its own, so the result also has a report;
// the response is that scenario's result, as before, plus the run id.
func (s *Server) handleScenarioRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := filepath.Join(s.scenariosDir, name+".yaml")
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "no scenario named " + strconv.Quote(name)})
	} else if run, runErr := s.runs.RunAndWait(testrun.Request{Selection: report.Selection{IDs: []string{name}}}); runErr != nil {
		writeRunError(w, runErr)
	} else {
		result := scenario.ScenarioResult{Name: name, ID: name, Status: scenario.StatusFailed, Steps: []scenario.StepResult{}, RunID: run.ID}
		for _, tc := range run.TestCases {
			if tc.ID == name {
				result = tc
			}
		}
		writeJSON(w, http.StatusOK, result)
	}
}

// handleScenariosRunAll runs the library as one run (long-running test cases excluded, as
// in every run that does not ask for them) and answers with the suite result and run id.
func (s *Server) handleScenariosRunAll(w http.ResponseWriter, r *http.Request) {
	run, err := s.runs.RunAndWait(testrun.Request{})
	switch {
	case errors.Is(err, testrun.ErrNothingSelected):
		writeJSON(w, http.StatusOK, scenario.SuiteResult{})
	case err != nil:
		writeRunError(w, err)
	default:
		writeJSON(w, http.StatusOK, scenario.SuiteResult{Results: run.TestCases, RunID: run.ID})
	}
}
