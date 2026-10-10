package scenario

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// Spec ties a test case to the specification it checks. Only identifiers are recorded; the
// criterion itself is paraphrased in the scenario's covers line.
type Spec struct {
	UseCase      string   `yaml:"use_case" json:"use_case,omitempty"`
	Scenario     uint     `yaml:"scenario" json:"scenario,omitempty"`
	Document     string   `yaml:"document" json:"document,omitempty"`
	Section      string   `yaml:"section" json:"section,omitempty"`
	Table        string   `yaml:"table" json:"table,omitempty"`
	Requirements []string `yaml:"requirements" json:"requirements,omitempty"`
	TestCase     string   `yaml:"test_case" json:"test_case,omitempty"`
	Verification string   `yaml:"verification" json:"verification,omitempty"`

	// Filled from the use-case catalog, never from the file.
	UseCaseTitle  string `yaml:"-" json:"use_case_title,omitempty"`
	ScenarioTitle string `yaml:"-" json:"scenario_title,omitempty"`
	DeviceLevel   string `yaml:"-" json:"device_level,omitempty"`
}

// fileSpec is the raw YAML shape of a scenario file.
type fileSpec struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Covers      string           `yaml:"covers"`
	Goal        string           `yaml:"goal"`
	Hint        string           `yaml:"hint"`
	Category    string           `yaml:"category"`
	Risk        string           `yaml:"risk"`
	Spec        *Spec            `yaml:"spec"`
	LongRunning bool             `yaml:"long_running"`
	Cleanup     bool             `yaml:"cleanup"`
	Requires    requirementsSpec `yaml:"requires"`
	Peer        string           `yaml:"peer"`
	Steps       []map[string]any `yaml:"steps"`
	// Finally runs after the steps whenever they started, also after a failure or a
	// cancellation, to leave the device as the test found it.
	Finally []map[string]any `yaml:"finally"`
}

type requirementsSpec struct {
	Capabilities []string `yaml:"capabilities"`
	UseCases     []string `yaml:"use_cases"`
	// OpevPhases skips the scenario when the peer declares its OPEV limits on other phases,
	// e.g. per-phase tests against a device that declares only the combined "abc".
	OpevPhases []string `yaml:"opev_phases"`
	// Scenarios are scenario numbers of spec.use_case the test exercises. A recommended or
	// optional one the device does not advertise skips the test; a mandatory one never does.
	Scenarios []uint `yaml:"scenarios"`
	// Vehicle and Charging require a connected vehicle, or a running charging session.
	Vehicle  bool `yaml:"vehicle"`
	Charging bool `yaml:"charging"`
	// Parameters names the device parameters (params.<name>) the steps use.
	Parameters []string `yaml:"parameters"`

	useCase string // spec.use_case, for the scenario check
}

func (r requirementsSpec) asMap() map[string]any {
	out := map[string]any{"capabilities": r.Capabilities, "use_cases": r.UseCases, "opev_phases": r.OpevPhases}
	if len(r.Scenarios) > 0 {
		out["scenarios"] = r.Scenarios
	}
	if r.Vehicle {
		out["vehicle"] = true
	}
	if r.Charging {
		out["charging"] = true
	}
	if len(r.Parameters) > 0 {
		out["parameters"] = r.Parameters
	}
	return out
}

// Meta is what the catalog shows about a test case, and what a run needs to select and
// order it without running it.
type Meta struct {
	ID          string         `json:"id"`
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Covers      string         `json:"covers"`
	Goal        string         `json:"goal"`
	Hint        string         `json:"hint"`
	Category    string         `json:"category"`
	Risk        string         `json:"risk"`
	Requires    map[string]any `json:"requires"`
	StepCount   int            `json:"step_count"`
	Spec        *Spec          `json:"spec,omitempty"`
	// UseCase is the acronym the test case is grouped under: spec.use_case, else the first
	// required use case, else empty for the common group.
	UseCase     string `json:"use_case,omitempty"`
	LongRunning bool   `json:"long_running"`
	Cleanup     bool   `json:"cleanup"`
	Path        string `json:"-"`
}

func (m Meta) newResult() ScenarioResult {
	return ScenarioResult{
		Name: m.Name, ID: m.ID, Status: StatusPassed, Steps: []StepResult{},
		Description: m.Description, Category: m.Category, Risk: m.Risk, Requires: m.Requires,
		Covers: m.Covers, Goal: m.Goal, Hint: m.Hint, Spec: m.Spec, UseCase: m.UseCase,
		LongRunning: m.LongRunning, Cleanup: m.Cleanup,
	}
}

// loaded is a parsed scenario file and its catalog view.
type loaded struct {
	file fileSpec
	meta Meta
}

func loadFile(path string) (loaded, error) {
	var out loaded
	data, err := os.ReadFile(path)
	if err == nil {
		if err = yaml.Unmarshal(data, &out.file); err != nil {
			err = fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	if err == nil {
		out.meta = buildMeta(path, out.file)
		if out.meta.Spec != nil {
			out.file.Requires.useCase = out.meta.Spec.UseCase
		}
	}
	return out, err
}

func buildMeta(path string, f fileSpec) Meta {
	id := strings.TrimSuffix(filepath.Base(path), ".yaml")
	name := f.Name
	if name == "" {
		name = id
	}
	spec := normalizeSpec(f.Spec)
	return Meta{
		ID: id, Name: name, Description: f.Description, Covers: f.Covers, Goal: f.Goal, Hint: f.Hint,
		Category: f.Category, Risk: f.Risk, Requires: f.Requires.asMap(), StepCount: len(f.Steps),
		Spec: spec, UseCase: groupOf(spec, f.Requires), LongRunning: f.LongRunning, Cleanup: f.Cleanup,
		Path: path,
	}
}

// normalizeSpec fills the document, titles and requirement level from the use-case catalog,
// and spells the use case as its acronym.
func normalizeSpec(in *Spec) *Spec {
	var out *Spec
	if in != nil {
		spec := *in
		if uc, ok := ucspec.Lookup(spec.UseCase); ok {
			spec.UseCase = uc.Acronym
			spec.UseCaseTitle = uc.Title
			if spec.Document == "" {
				spec.Document = uc.Document
			}
			if s, ok := uc.Scenario(spec.Scenario); ok {
				spec.ScenarioTitle = s.Title
				spec.DeviceLevel = string(s.Device)
			}
		}
		out = &spec
	}
	return out
}

func groupOf(spec *Spec, req requirementsSpec) string {
	group := ""
	if spec != nil && spec.UseCase != "" {
		group = spec.UseCase
	} else if len(req.UseCases) > 0 {
		if uc, ok := ucspec.Lookup(req.UseCases[0]); ok {
			group = uc.Acronym
		}
	}
	return group
}

// LoadMeta reads the catalog view of one scenario file.
func LoadMeta(path string) (Meta, error) {
	sp, err := loadFile(path)
	return sp.meta, err
}

// InvalidMeta is the catalog entry for a file that does not parse.
func InvalidMeta(path string, err error) Meta {
	id := strings.TrimSuffix(filepath.Base(path), ".yaml")
	return Meta{
		ID: id, Name: id, Description: "Invalid scenario: " + err.Error(),
		Category: "Invalid", Risk: "read-only", Requires: map[string]any{}, Path: path,
	}
}

// Catalog lists every scenario in dir in run order. A file that does not parse is listed as
// invalid rather than dropped, so the dashboard shows it.
func Catalog(dir string) ([]Meta, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	metas := make([]Meta, 0, len(paths))
	for _, p := range paths {
		meta, loadErr := LoadMeta(p)
		if loadErr != nil {
			meta = InvalidMeta(p, loadErr)
		}
		if meta.Category == "" {
			meta.Category = "General"
		}
		if meta.Risk == "" {
			meta.Risk = "read-only"
		}
		metas = append(metas, meta)
	}
	Order(metas)
	return metas, err
}

// Order sorts test cases into run order: connection and discovery first, then the common
// checks, then one use case after the other in catalog order -- read-only before live-control
// before disruptive, long-running last -- and the cleanup test cases at the very end.
func Order(metas []Meta) {
	sort.SliceStable(metas, func(i, j int) bool {
		return orderKey(metas[i]) < orderKey(metas[j])
	})
}

var leading = map[string]int{"smoke-pairing": 0, "device-profile-discovery": 1}

func orderKey(m Meta) string {
	first, isLeading := leading[m.ID]
	if !isLeading {
		first = 2
	}
	cleanup := 0
	if m.Cleanup {
		cleanup = 1
	}
	group := -1
	if m.UseCase != "" {
		group = ucspec.Order(m.UseCase)
	}
	long := 0
	if m.LongRunning {
		long = 1
	}
	// Placeholders for criteria that need a device probe go to the end of their use case.
	if m.Spec != nil && m.Spec.Verification == VerificationProbe {
		long = 2
	}
	return fmt.Sprintf("%d|%d|%03d|%d|%d|%s", cleanup, first, group+1, riskOrder(m.Risk), long, m.ID)
}

func riskOrder(risk string) int {
	order := 0
	switch risk {
	case "live-control":
		order = 1
	case "disruptive":
		order = 2
	}
	return order
}
