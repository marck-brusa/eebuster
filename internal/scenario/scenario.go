// Package scenario is a YAML-driven test-case runner. It drives the same REST API the
// dashboard uses -- one HTTP client, no access to server internals -- so it can equally be
// pointed at a remote instance.
package scenario

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// round2 matches cli/scenario.py's round(x, 2) on every duration_s field, so JSON output is
// byte-for-byte comparable between the two implementations, not just semantically similar.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

type StepResult struct {
	Name      string  `json:"step"`
	Status    string  `json:"status"` // "passed" | "failed" | "skipped"
	DurationS float64 `json:"duration_s"`
	Detail    string  `json:"detail,omitempty"`
}

type ScenarioResult struct {
	Name        string         `json:"name"`
	Status      string         `json:"status"`
	DurationS   float64        `json:"duration_s"`
	Steps       []StepResult   `json:"steps"`
	Description string         `json:"description"`
	Category    string         `json:"category"`
	Risk        string         `json:"risk"`
	Requires    map[string]any `json:"requires"`
}

type SuiteResult struct {
	Results []ScenarioResult `json:"-"`
}

// MarshalJSON matches SuiteResult.to_dict()'s exact shape (status/passed/failed/skipped
// alongside scenarios) -- Passed/Failed/Skipped/Status are Go methods, which encoding/json
// never includes on their own, and a nil Results would encode as `scenarios: null`. Both
// would break examples/run_scenarios_and_report.py in practice (KeyError, then a TypeError
// on `for result in suite["scenarios"]`), the same class of bug PeerUseCases had.
func (r SuiteResult) MarshalJSON() ([]byte, error) {
	results := r.Results
	if results == nil {
		results = []ScenarioResult{}
	}
	return json.Marshal(struct {
		Status    string           `json:"status"`
		Passed    int              `json:"passed"`
		Failed    int              `json:"failed"`
		Skipped   int              `json:"skipped"`
		Scenarios []ScenarioResult `json:"scenarios"`
	}{
		Status: r.Status(), Passed: r.Passed(), Failed: r.Failed(), Skipped: r.Skipped(),
		Scenarios: results,
	})
}

func (r SuiteResult) Passed() int  { return r.count("passed") }
func (r SuiteResult) Failed() int  { return r.count("failed") }
func (r SuiteResult) Skipped() int { return r.count("skipped") }
func (r SuiteResult) Status() string {
	if r.Failed() > 0 {
		return "failed"
	}
	return "passed"
}
func (r SuiteResult) count(status string) int {
	n := 0
	for _, s := range r.Results {
		if s.Status == status {
			n++
		}
	}
	return n
}

// spec is the raw YAML shape of a scenario file.
type spec struct {
	Name        string           `yaml:"name"`
	Description string           `yaml:"description"`
	Category    string           `yaml:"category"`
	Risk        string           `yaml:"risk"`
	Requires    requirementsSpec `yaml:"requires"`
	Peer        string           `yaml:"peer"`
	Steps       []map[string]any `yaml:"steps"`
}

type requirementsSpec struct {
	Capabilities []string `yaml:"capabilities"`
	UseCases     []string `yaml:"use_cases"`
}

func (r requirementsSpec) asMap() map[string]any {
	return map[string]any{"capabilities": r.Capabilities, "use_cases": r.UseCases}
}

type Runner struct {
	client  *http.Client
	baseURL string

	peersOnce  sync.Once
	peersByCfg map[string]string // configured peer name -> SKI
}

func NewRunner(baseURL string) *Runner {
	return &Runner{client: &http.Client{Timeout: 30 * time.Second}, baseURL: strings.TrimRight(baseURL, "/")}
}

// configPeers maps each configured peer's name to its SKI, so a scenario can say
// `peer: device-under-test` instead of pasting a 40-hex SKI into every file. Fetched once per
// runner: the config does not change underneath a suite run, and RunAll would otherwise
// re-request it for every scenario. Returns an empty map if the config is unreadable, which
// leaves selectPeer to fail with a clear message rather than guessing.
func (rn *Runner) configPeers() map[string]string {
	rn.peersOnce.Do(func() {
		rn.peersByCfg = map[string]string{}
		resp, err := rn.client.Get(rn.baseURL + "/api/v1/config")
		if err != nil {
			return
		}
		defer resp.Body.Close()
		var cfg struct {
			Peers []struct {
				Name string `json:"name"`
				SKI  string `json:"ski"`
			} `json:"peers"`
		}
		if json.NewDecoder(resp.Body).Decode(&cfg) != nil {
			return
		}
		for _, p := range cfg.Peers {
			if p.Name != "" && p.SKI != "" {
				rn.peersByCfg[p.Name] = p.SKI
			}
		}
	})
	return rn.peersByCfg
}

// RunScenario loads and runs one scenario YAML file, matching cli/scenario.py's run_scenario.
func (rn *Runner) RunScenario(path string) (ScenarioResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ScenarioResult{}, err
	}
	var sp spec
	if err := yaml.Unmarshal(data, &sp); err != nil {
		return ScenarioResult{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if sp.Name == "" {
		sp.Name = path
	}

	start := time.Now()
	context := map[string]any{}
	steps := []StepResult{}

	if sp.Peer != "" {
		peers, _ := rn.getJSON("/api/v1/peers")
		peer, err := selectPeer(peers, rn.configPeers(), sp.Peer)
		if err != nil {
			// Hard failure, not a skip: the scenario asked for a specific device and we cannot
			// tell which one it means. Running it against something else would report a pass or
			// fail for a device nobody asked about.
			return ScenarioResult{
				Name:      sp.Name,
				Status:    "failed",
				DurationS: round2(time.Since(start).Seconds()),
				Steps: []StepResult{{
					Name:   "select_peer",
					Status: "failed",
					Detail: err.Error(),
				}},
			}, nil
		}
		context["peer"] = peer
	}

	stepSpecs := sp.Steps
	// Connection is a prerequisite for live use-case discovery: run a leading wait_connected
	// before evaluating advertised-use-case requirements, or a slow-but-healthy peer reads as
	// "not supported" -- matches run_scenario's own leading-step special case.
	if len(stepSpecs) > 0 && firstKey(stepSpecs[0]) == "wait_connected" {
		stepStart := time.Now()
		result := rn.runStep(stepSpecs[0], context)
		result.DurationS = round2(time.Since(stepStart).Seconds())
		steps = append(steps, result)
		stepSpecs = stepSpecs[1:]
		if result.Status == "failed" {
			return ScenarioResult{
				Name: sp.Name, Status: "failed", DurationS: round2(time.Since(start).Seconds()), Steps: steps,
				Description: sp.Description, Category: sp.Category, Risk: sp.Risk, Requires: sp.Requires.asMap(),
			}, nil
		}
	}

	if missing := rn.missingRequirements(sp.Requires, context); len(missing) > 0 {
		steps = append(steps, StepResult{Name: "requirements", Status: "skipped", Detail: strings.Join(missing, "; ")})
		return ScenarioResult{
			Name: sp.Name, Status: "skipped", DurationS: round2(time.Since(start).Seconds()), Steps: steps,
			Description: sp.Description, Category: sp.Category, Risk: sp.Risk, Requires: sp.Requires.asMap(),
		}, nil
	}

	overall := "passed"
	for _, stepSpec := range stepSpecs {
		stepStart := time.Now()
		result := rn.runStep(stepSpec, context)
		result.DurationS = round2(time.Since(stepStart).Seconds())
		steps = append(steps, result)
		if result.Status == "failed" {
			overall = "failed"
			break
		}
	}

	return ScenarioResult{
		Name: sp.Name, Status: overall, DurationS: round2(time.Since(start).Seconds()), Steps: steps,
		Description: sp.Description, Category: sp.Category, Risk: sp.Risk, Requires: sp.Requires.asMap(),
	}, nil
}

// RunAll runs every *.yaml scenario in dir, smoke/discovery scenarios first, matching
// cli/scenario.py's run_all_scenarios priority ordering. One malformed scenario fails without
// aborting the rest.
func (rn *Runner) RunAll(dir string) (SuiteResult, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return SuiteResult{}, err
	}
	priority := map[string]int{"smoke-pairing": 0, "device-profile-discovery": 1}
	sort.Slice(matches, func(i, j int) bool {
		pi, pj := priorityFor(matches[i], priority), priorityFor(matches[j], priority)
		if pi != pj {
			return pi < pj
		}
		return matches[i] < matches[j]
	})

	var results []ScenarioResult
	for _, path := range matches {
		result, err := rn.RunScenario(path)
		if err != nil {
			results = append(results, ScenarioResult{
				Name: strings.TrimSuffix(filepath.Base(path), ".yaml"), Status: "failed",
				Steps: []StepResult{{Name: "load", Status: "failed", Detail: err.Error()}},
			})
			continue
		}
		results = append(results, result)
	}
	return SuiteResult{Results: results}, nil
}

func priorityFor(path string, priority map[string]int) int {
	stem := strings.TrimSuffix(filepath.Base(path), ".yaml")
	if p, ok := priority[stem]; ok {
		return p
	}
	return 10
}

// selectPeer resolves a scenario's `peer:` reference to a connected peer.
//
// The reference is a name from the config's peers: list (or a literal 40-hex SKI). It is NOT
// the name reported by GET /api/v1/peers -- that is the mDNS instance name, e.g.
// "device-under-test._ship._tcp.local.", which never equals the configured name. An earlier
// version compared against it and then silently fell back to peers[0] on no match, so every
// scenario ran against whichever peer happened to be listed first: with a simulator enabled,
// lpc-failsafe asserted against the simulator's 5500 W while claiming to test the real device.
// A scenario that cannot identify its target must fail loudly instead.
//
// configPeers maps configured name -> SKI; pass nil if unavailable.
func selectPeer(peers []any, configPeers map[string]string, peerName string) (map[string]any, error) {
	want := strings.ToLower(peerName)
	if ski, ok := configPeers[peerName]; ok && ski != "" {
		want = strings.ToLower(ski)
	}

	for _, p := range peers {
		m, ok := p.(map[string]any)
		if !ok {
			continue
		}
		if ski, _ := m["ski"].(string); strings.EqualFold(ski, want) {
			return m, nil
		}
	}

	if _, configured := configPeers[peerName]; configured {
		return nil, fmt.Errorf("peer %q (ski %s) is configured but not connected", peerName, want)
	}
	if isSKI(peerName) {
		return nil, fmt.Errorf("peer ski %s is not connected", peerName)
	}
	// Name the configured peers. Every shipped scenario targets "device-under-test", so the usual
	// cause is a peers: entry named something else, and the fix is invisible unless the available
	// names are shown: rename the entry, or set the scenario's peer: to one of these.
	if len(configPeers) == 0 {
		return nil, fmt.Errorf("peer %q cannot be resolved: the config has no peers: entries, so no name maps to a ski. "+
			"Add the device under peers: (name it %q to match the bundled scenarios), or set this scenario's peer: to a 40-hex ski", peerName, peerName)
	}
	names := make([]string, 0, len(configPeers))
	for name := range configPeers {
		names = append(names, name)
	}
	sort.Strings(names)
	return nil, fmt.Errorf("peer %q is not in the config's peers: list, so its ski is unknown. Configured names: %s. "+
		"The bundled scenarios all target %q, so renaming the peers: entry to that is usually what you want",
		peerName, strings.Join(names, ", "), peerName)
}

// isSKI reports whether s is a 40-character hex SKI, so a scenario may name a peer directly
// without a config entry.
func isSKI(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

func firstKey(m map[string]any) string {
	for k := range m {
		return k
	}
	return ""
}

func (rn *Runner) getJSON(path string) ([]any, error) {
	resp, err := rn.client.Get(rn.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var out []any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out, nil
}

var refRe = regexp.MustCompile(`\{([\w.]+)\}`)

// resolve substitutes {peer.ski}-style references against context, matching
// cli/scenario.py's _resolve: a value that is *exactly* one reference resolves to the
// referenced value's own type; a reference embedded in a larger string is substituted in
// place as a string.
func resolve(value any, context map[string]any) any {
	switch v := value.(type) {
	case string:
		if m := refRe.FindStringSubmatch(v); m != nil && m[0] == v {
			resolved, _ := lookup(m[1], context)
			return resolved
		}
		if refRe.MatchString(v) {
			return refRe.ReplaceAllStringFunc(v, func(match string) string {
				path := refRe.FindStringSubmatch(match)[1]
				resolved, _ := lookup(path, context)
				return fmt.Sprint(resolved)
			})
		}
		return v
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, val := range v {
			out[k] = resolve(val, context)
		}
		return out
	default:
		return value
	}
}

func lookup(dottedPath string, context map[string]any) (any, bool) {
	var node any = context
	for _, part := range strings.Split(dottedPath, ".") {
		m, ok := node.(map[string]any)
		if !ok {
			return nil, false
		}
		node, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return node, true
}

func parseTimeout(value any) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case float64:
		return v
	case string:
		v = strings.TrimSpace(v)
		if strings.HasSuffix(v, "ms") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "ms"), 64)
			return n / 1000
		}
		if strings.HasSuffix(v, "s") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "s"), 64)
			return n
		}
		n, _ := strconv.ParseFloat(v, 64)
		return n
	default:
		return 0
	}
}

func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
