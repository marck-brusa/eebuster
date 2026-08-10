package httpapi

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/marck-brusa/eebuster/internal/scenario"
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

type scenarioCatalogEntry struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Risk        string         `json:"risk"`
	Requires    map[string]any `json:"requires"`
	StepCount   int            `json:"step_count"`
}

type rawScenarioSpec struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Category    string           `yaml:"category"`
	Risk        string           `yaml:"risk"`
	Requires    map[string]any   `yaml:"requires"`
	Steps       []map[string]any `yaml:"steps"`
}

func (s *Server) handleScenariosCatalog(w http.ResponseWriter, r *http.Request) {
	paths := scenarioPaths(s.scenariosDir)
	result := make([]scenarioCatalogEntry, 0, len(paths))
	for _, p := range paths {
		id := strings.TrimSuffix(filepath.Base(p), ".yaml")
		data, err := os.ReadFile(p)
		if err != nil {
			result = append(result, invalidCatalogEntry(id, err))
			continue
		}
		var spec rawScenarioSpec
		if err := yaml.Unmarshal(data, &spec); err != nil {
			result = append(result, invalidCatalogEntry(id, err))
			continue
		}
		name := spec.Name
		if name == "" {
			name = id
		}
		category := spec.Category
		if category == "" {
			category = "General"
		}
		risk := spec.Risk
		if risk == "" {
			risk = "read-only"
		}
		requires := spec.Requires
		if requires == nil {
			requires = map[string]any{}
		}
		result = append(result, scenarioCatalogEntry{
			ID: id, Name: name, Description: spec.Description, Category: category,
			Risk: risk, Requires: requires, StepCount: len(spec.Steps),
		})
	}
	writeJSON(w, http.StatusOK, result)
}

func invalidCatalogEntry(id string, err error) scenarioCatalogEntry {
	return scenarioCatalogEntry{
		ID: id, Name: id, Description: "Invalid scenario: " + err.Error(),
		Category: "Invalid", Risk: "read-only", Requires: map[string]any{},
	}
}

// loopbackBaseURL matches routes_scenarios.py's own rationale exactly: always loopback,
// regardless of api.bind (0.0.0.0 isn't dialable as a client target), but the port must
// match whatever this process actually bound.
func (s *Server) loopbackBaseURL() string {
	return "http://127.0.0.1:" + strconv.Itoa(s.config().API.Port)
}

func (s *Server) handleScenarioRun(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	path := filepath.Join(s.scenariosDir, name+".yaml")
	if _, err := os.Stat(path); err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"detail": "no scenario named " + strconv.Quote(name)})
		return
	}
	result, err := scenario.NewRunner(s.loopbackBaseURL()).RunScenario(path)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleScenariosRunAll(w http.ResponseWriter, r *http.Request) {
	if _, err := os.Stat(s.scenariosDir); err != nil {
		writeJSON(w, http.StatusOK, scenario.SuiteResult{})
		return
	}
	suite, err := scenario.NewRunner(s.loopbackBaseURL()).RunAll(s.scenariosDir)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"detail": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, suite)
}
