// Package scenario is a YAML-driven test-case runner. It drives the same REST API the
// dashboard uses -- one HTTP client, no access to server internals -- so it can equally be
// pointed at a remote instance.
package scenario

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// round2 matches cli/scenario.py's round(x, 2) on every duration_s field, so JSON output is
// byte-for-byte comparable between the two implementations, not just semantically similar.
func round2(v float64) float64 { return math.Round(v*100) / 100 }

func unixSeconds(t time.Time) float64 { return round2(float64(t.UnixNano()) / 1e9) }

// Verdicts of a step and of a test case.
const (
	StatusPassed  = "passed"
	StatusFailed  = "failed"
	StatusSkipped = "skipped"
)

// Reasons a test case is skipped.
const (
	ReasonUseCaseNotAdvertised  = "use_case_not_advertised"
	ReasonScenarioNotAdvertised = "scenario_not_advertised"
	ReasonCapabilityMissing     = "capability_missing"
	ReasonPhasesNotDeclared     = "phases_not_declared"
	ReasonPreconditionNotMet    = "precondition_not_met"
	ReasonNotVerifiableOnWire   = "not_verifiable_on_wire"
	ReasonNotRun                = "not_run"
)

// How a test case reaches its verdict.
const (
	VerificationWire     = "wire"
	VerificationReadback = "readback"
	VerificationProbe    = "needs-device-probe"
)

// SeqRange is an inclusive range of trace or event sequence numbers. To < From means the
// range is empty.
type SeqRange struct {
	From int64 `json:"from"`
	To   int64 `json:"to"`
}

func (r *SeqRange) contains(seq int64) bool {
	return r != nil && seq >= r.From && seq <= r.To
}

// RequestEvidence is one REST request a step made and what came back.
type RequestEvidence struct {
	Method    string `json:"method"`
	Path      string `json:"path"`
	Body      any    `json:"body,omitempty"`
	Status    int    `json:"status,omitempty"`
	Response  string `json:"response,omitempty"`
	Truncated bool   `json:"response_truncated,omitempty"`
}

// Comparison is one evaluated assertion: the key, the operator, what was expected, what the
// device reported, and the outcome.
type Comparison struct {
	Key      string `json:"key"`
	Op       string `json:"op"`
	Expected any    `json:"expected,omitempty"`
	Actual   any    `json:"actual"`
	OK       bool   `json:"ok"`
	Message  string `json:"message,omitempty"`
}

// FindingRef is a conformance finding on a frame inside a step or test case window.
type FindingRef struct {
	Seq      int64  `json:"seq"`
	Dir      string `json:"dir,omitempty"`
	Function string `json:"function,omitempty"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Message  string `json:"message"`
	SpecRef  string `json:"spec_ref,omitempty"`
}

// EventRef is a use-case event published during a step.
type EventRef struct {
	Seq   int64  `json:"seq"`
	Ts    int64  `json:"ts"`
	Event string `json:"event"`
}

type StepResult struct {
	Name      string  `json:"step"`
	Status    string  `json:"status"` // "passed" | "failed" | "skipped"
	DurationS float64 `json:"duration_s"`
	Detail    string  `json:"detail,omitempty"`

	StartedAt     float64          `json:"started_at,omitempty"`
	Request       *RequestEvidence `json:"request,omitempty"`
	Assertions    []Comparison     `json:"assertions,omitempty"`
	Event         map[string]any   `json:"event,omitempty"`
	Trace         *SeqRange        `json:"trace,omitempty"`
	Findings      []FindingRef     `json:"findings,omitempty"`
	FindingsTotal int              `json:"findings_total,omitempty"`
	Events        []EventRef       `json:"events,omitempty"`

	eventRange *SeqRange
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

	ID            string       `json:"id,omitempty"`
	Reason        string       `json:"reason,omitempty"`
	Covers        string       `json:"covers,omitempty"`
	Goal          string       `json:"goal,omitempty"`
	Hint          string       `json:"hint,omitempty"`
	Spec          *Spec        `json:"spec,omitempty"`
	UseCase       string       `json:"use_case,omitempty"`
	LongRunning   bool         `json:"long_running,omitempty"`
	Cleanup       bool         `json:"cleanup,omitempty"`
	StartedAt     float64      `json:"started_at,omitempty"`
	Trace         *SeqRange    `json:"trace,omitempty"`
	Findings      []FindingRef `json:"findings,omitempty"`
	FindingsTotal int          `json:"findings_total,omitempty"`
	RunID         string       `json:"run_id,omitempty"`
}

type SuiteResult struct {
	Results []ScenarioResult `json:"-"`
	RunID   string           `json:"-"`
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
		RunID     string           `json:"run_id,omitempty"`
	}{
		Status: r.Status(), Passed: r.Passed(), Failed: r.Failed(), Skipped: r.Skipped(),
		Scenarios: results, RunID: r.RunID,
	})
}

func (r SuiteResult) Passed() int  { return r.count(StatusPassed) }
func (r SuiteResult) Failed() int  { return r.count(StatusFailed) }
func (r SuiteResult) Skipped() int { return r.count(StatusSkipped) }
func (r SuiteResult) Status() string {
	status := StatusPassed
	if r.Failed() > 0 {
		status = StatusFailed
	}
	return status
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

// Options configure a Runner beyond its base URL.
type Options struct {
	// Context cancels a run: a pending sleep or wait ends early and the test case is reported
	// as not run.
	Context context.Context
	// Params are the device-specific parameters scenarios reference as {params.name}.
	Params map[string]any
}

type Runner struct {
	client  *http.Client
	baseURL string
	ctx     context.Context
	params  map[string]any

	peersOnce  sync.Once
	peersByCfg map[string]string // configured peer name -> SKI
}

func NewRunner(baseURL string) *Runner {
	return NewRunnerWith(baseURL, Options{})
}

func NewRunnerWith(baseURL string, opts Options) *Runner {
	ctx := opts.Context
	if ctx == nil {
		ctx = context.Background()
	}
	params := opts.Params
	if params == nil {
		params = map[string]any{}
	}
	return &Runner{
		client: &http.Client{Timeout: 30 * time.Second}, baseURL: strings.TrimRight(baseURL, "/"),
		ctx: ctx, params: params,
	}
}

func (rn *Runner) cancelled() bool { return rn.ctx.Err() != nil }

// configPeers maps each configured peer's name to its SKI, so a scenario can say
// `peer: device-under-test` instead of pasting a 40-hex SKI into every file. Fetched once per
// runner: the config does not change underneath a suite run, and RunAll would otherwise
// re-request it for every scenario. Returns an empty map if the config is unreadable, which
// leaves selectPeer to fail with a clear message rather than guessing.
func (rn *Runner) configPeers() map[string]string {
	rn.peersOnce.Do(func() {
		rn.peersByCfg = map[string]string{}
		var cfg struct {
			Peers []struct {
				Name string `json:"name"`
				SKI  string `json:"ski"`
			} `json:"peers"`
		}
		if rn.getInto("/api/v1/config", &cfg) == nil {
			for _, p := range cfg.Peers {
				if p.Name != "" && p.SKI != "" {
					rn.peersByCfg[p.Name] = p.SKI
				}
			}
		}
	})
	return rn.peersByCfg
}

// RunScenario loads and runs one scenario YAML file, matching cli/scenario.py's run_scenario.
func (rn *Runner) RunScenario(path string) (ScenarioResult, error) {
	var result ScenarioResult
	sp, err := loadFile(path)
	if err == nil {
		result = rn.run(sp)
	}
	return result, err
}

// NotRun is the result of a test case that the run never started.
func NotRun(meta Meta, detail string) ScenarioResult {
	result := meta.newResult()
	result.Status, result.Reason = StatusSkipped, ReasonNotRun
	result.Steps = []StepResult{{Name: "not run", Status: StatusSkipped, Detail: detail}}
	return result
}

func (rn *Runner) run(sp loaded) ScenarioResult {
	start := time.Now()
	result := sp.meta.newResult()
	result.StartedAt = unixSeconds(start)
	tracker := newTracker(rn)
	from := tracker.markAfter()
	scenarioContext := map[string]any{"params": rn.params, "captured": map[string]any{}, "trace_from": from.trace}

	if sp.file.Peer != "" {
		peer, err := rn.awaitPeer(sp)
		if err != nil {
			// Hard failure, not a skip: the scenario asked for a specific device and we cannot
			// tell which one it means. Running it against something else would report a pass or
			// fail for a device nobody asked about.
			result.Status = StatusFailed
			result.Steps = append(result.Steps, StepResult{Name: "select_peer", Status: StatusFailed, Detail: err.Error()})
		} else {
			scenarioContext["peer"] = peer
		}
	}
	if result.Status == StatusPassed {
		rn.execute(sp, scenarioContext, &result, tracker)
	}

	result.DurationS = round2(time.Since(start).Seconds())
	ski, _ := lookup("peer.ski", scenarioContext)
	skiStr, _ := ski.(string)
	tracker.attach(&result, from, skiStr)
	return result
}

// execute runs the leading wait_connected, the verification and requirement checks, and
// then the steps, recording everything into result.
func (rn *Runner) execute(sp loaded, scenarioContext map[string]any, result *ScenarioResult, tracker *tracker) {
	steps := sp.file.Steps
	proceed := true
	// Connection is a prerequisite for live use-case discovery: run a leading wait_connected
	// before evaluating advertised-use-case requirements, or a slow-but-healthy peer reads as
	// "not supported" -- matches run_scenario's own leading-step special case.
	if len(steps) > 0 && firstKey(steps[0]) == "wait_connected" {
		step := rn.timedStep(steps[0], scenarioContext, tracker)
		result.Steps = append(result.Steps, step)
		steps = steps[1:]
		if step.Status == StatusFailed {
			proceed = false
			result.Status = StatusFailed
		}
	}
	if proceed && sp.meta.Spec != nil && sp.meta.Spec.Verification == VerificationProbe {
		proceed = false
		result.Status, result.Reason = StatusSkipped, ReasonNotVerifiableOnWire
		result.Steps = append(result.Steps, StepResult{Name: "verification", Status: StatusSkipped,
			Detail: "the expected result is internal device state that EEBUS does not expose, and no device probe is configured"})
	}
	if proceed {
		if gaps := rn.missingRequirements(sp.file.Requires, scenarioContext); len(gaps) > 0 {
			proceed = false
			details := make([]string, 0, len(gaps))
			seen := map[string]bool{}
			for _, g := range gaps {
				if !seen[g.Detail] {
					seen[g.Detail] = true
					details = append(details, g.Detail)
				}
			}
			result.Status, result.Reason = StatusSkipped, gaps[0].Reason
			result.Steps = append(result.Steps, StepResult{Name: "requirements", Status: StatusSkipped, Detail: strings.Join(details, "; ")})
		}
	}
	if proceed {
		rn.runSteps(steps, scenarioContext, result, tracker)
		rn.runFinally(sp.file.Finally, scenarioContext, result, tracker)
	}
}

// runFinally runs the finally steps with a context of their own, so a cancelled run still
// releases what the test case set. A failing finally step fails a test case that had passed.
func (rn *Runner) runFinally(steps []map[string]any, scenarioContext map[string]any, result *ScenarioResult, tracker *tracker) {
	if len(steps) > 0 {
		runCtx := rn.ctx
		rn.ctx = context.Background()
		for _, stepSpec := range steps {
			step := rn.timedStep(stepSpec, scenarioContext, tracker)
			step.Name = "finally: " + step.Name
			if step.Status == StatusFailed && result.Status == StatusPassed {
				result.Status = StatusFailed
			}
			result.Steps = append(result.Steps, step)
		}
		rn.ctx = runCtx
	}
}

// boundStep is a step together with the loop variables a for_each bound for it.
type boundStep struct {
	spec     map[string]any
	bindings map[string]any
	label    string
}

func (rn *Runner) runSteps(steps []map[string]any, scenarioContext map[string]any, result *ScenarioResult, tracker *tracker) {
	queue := make([]boundStep, 0, len(steps))
	for _, s := range steps {
		queue = append(queue, boundStep{spec: s})
	}
	for i := 0; i < len(queue) && result.Status == StatusPassed; i++ {
		item := queue[i]
		for k, v := range item.bindings {
			scenarioContext[k] = v
		}
		if rn.cancelled() {
			result.Status, result.Reason = StatusSkipped, ReasonNotRun
			result.Steps = append(result.Steps, StepResult{Name: "cancelled", Status: StatusSkipped, Detail: "the run was cancelled before this step"})
		} else if firstKey(item.spec) == "for_each" {
			expanded, step := expandForEach(item.spec["for_each"], scenarioContext)
			result.Steps = append(result.Steps, step)
			if step.Status == StatusFailed {
				result.Status = StatusFailed
			} else {
				rest := append([]boundStep{}, queue[i+1:]...)
				queue = append(append(queue[:i+1], expanded...), rest...)
			}
		} else {
			step := rn.timedStep(item.spec, scenarioContext, tracker)
			if item.label != "" {
				step.Name += " [" + item.label + "]"
			}
			if step.Status == StatusFailed && rn.cancelled() {
				step.Status = StatusSkipped
				step.Detail = "interrupted by cancellation: " + step.Detail
				result.Status, result.Reason = StatusSkipped, ReasonNotRun
			} else if step.Status == StatusFailed {
				result.Status = StatusFailed
			} else if step.Status == StatusSkipped {
				// skip_unless found the optional values the remaining steps need absent.
				result.Status, result.Reason = StatusSkipped, ReasonPreconditionNotMet
			}
			result.Steps = append(result.Steps, step)
		}
	}
}

// expandForEach turns `for_each: {items: "{params.limits_w}", as: value, steps: [...]}` into
// one copy of the steps per item, each bound to the item under the given name (and its
// position under <name>_index).
func expandForEach(args any, scenarioContext map[string]any) ([]boundStep, StepResult) {
	a := argsMap(args)
	name, _ := a["as"].(string)
	if name == "" {
		name = "item"
	}
	items, isList := resolve(a["items"], scenarioContext).([]any)
	steps := asSlice(a["steps"])
	step := StepResult{Name: fmt.Sprintf("for_each %s in %v", name, a["items"]), Status: StatusPassed}
	var expanded []boundStep
	if !isList || len(items) == 0 {
		step.Status = StatusFailed
		step.Detail = fmt.Sprintf("%v did not resolve to a non-empty list", a["items"])
	} else {
		step.Detail = fmt.Sprintf("%d item(s): %v", len(items), items)
		for index, item := range items {
			for _, s := range steps {
				if m, ok := s.(map[string]any); ok {
					expanded = append(expanded, boundStep{
						spec:     m,
						bindings: map[string]any{name: item, name + "_index": index},
						label:    fmt.Sprintf("%s=%v", name, item),
					})
				}
			}
		}
	}
	return expanded, step
}

// timedStep runs one step and records when it ran and which trace frames and events fall
// into it.
func (rn *Runner) timedStep(stepSpec map[string]any, scenarioContext map[string]any, tracker *tracker) StepResult {
	before := tracker.mark()
	if firstKey(stepSpec) != "expect_event" {
		// expect_event looks for events since the step before it began: that step caused them.
		if before.events >= 0 {
			scenarioContext["events_before_step"] = before.events
		} else {
			delete(scenarioContext, "events_before_step")
		}
	}
	start := time.Now()
	step := rn.runStep(stepSpec, scenarioContext)
	step.DurationS = round2(time.Since(start).Seconds())
	step.StartedAt = unixSeconds(start)
	after := tracker.markAfter()
	step.Trace = rangeBetween(before.trace, after.trace)
	step.eventRange = rangeBetween(before.events, after.events)
	return step
}

// RunAll runs every *.yaml scenario in dir in run order (see Order). One malformed scenario
// fails without aborting the rest.
func (rn *Runner) RunAll(dir string) (SuiteResult, error) {
	metas, err := Catalog(dir)
	var results []ScenarioResult
	if err == nil {
		for _, meta := range metas {
			result, loadErr := rn.RunScenario(meta.Path)
			if loadErr != nil {
				result = ScenarioResult{
					Name: meta.ID, ID: meta.ID, Status: StatusFailed,
					Steps: []StepResult{{Name: "load", Status: StatusFailed, Detail: loadErr.Error()}},
				}
			}
			results = append(results, result)
		}
	}
	return SuiteResult{Results: results}, err
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
// awaitPeer resolves the scenario's peer. A configured peer that is not connected right now
// is given the leading wait_connected step's timeout to appear: that step exists for a device
// in a reconnect, and the peer has to be resolved before it can run.
func (rn *Runner) awaitPeer(sp loaded) (map[string]any, error) {
	deadline := time.Now()
	if len(sp.file.Steps) > 0 && firstKey(sp.file.Steps[0]) == "wait_connected" {
		timeout := 30.0
		if t, ok := argsMap(sp.file.Steps[0]["wait_connected"])["timeout"]; ok {
			timeout = parseTimeout(t)
		}
		deadline = time.Now().Add(time.Duration(timeout * float64(time.Second)))
	}
	var peer map[string]any
	var err error
	for done := false; !done; {
		peers, _ := rn.getJSON("/api/v1/peers")
		peer, err = selectPeer(peers, rn.configPeers(), sp.file.Peer)
		var notConnected *PeerNotConnectedError
		done = err == nil || !errors.As(err, &notConnected) || !time.Now().Before(deadline) || rn.cancelled()
		if !done {
			rn.pause(time.Second)
		}
	}
	return peer, err
}

// PeerNotConnectedError says the scenario's peer is known but not connected at the moment.
type PeerNotConnectedError struct{ Detail string }

func (e *PeerNotConnectedError) Error() string { return e.Detail }

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
		return nil, &PeerNotConnectedError{Detail: fmt.Sprintf("peer %q (ski %s) is configured but not connected", peerName, want)}
	}
	if isSKI(peerName) {
		return nil, &PeerNotConnectedError{Detail: fmt.Sprintf("peer ski %s is not connected", peerName)}
	}
	// Name the configured peers. Every shipped scenario targets "device-under-test", so the usual
	// cause is a peers: entry named something else, and the fix is invisible unless the available
	// names are shown: rename the entry, or set the scenario's peer: to one of these.
	if len(configPeers) == 0 {
		// With no peers: entries at all, exactly one connected peer is unambiguous -- this is
		// the truststore workflow, where the device paired at runtime and the config never
		// names it. Targeting the wrong device is impossible with a single candidate; two or
		// more still fail loudly below, preserving the guarantee that made resolution strict
		// in the first place (scenarios once silently ran against the simulator instead of
		// the real device).
		var connected []map[string]any
		for _, p := range peers {
			if m, ok := p.(map[string]any); ok && m["connected"] == true {
				connected = append(connected, m)
			}
		}
		if len(connected) == 1 {
			return connected[0], nil
		}
		return nil, fmt.Errorf("peer %q cannot be resolved: the config has no peers: entries, and %d peers are connected (exactly one would be used automatically). "+
			"Add the device under peers: (name it %q to match the bundled scenarios), or set this scenario's peer: to a 40-hex ski", peerName, len(connected), peerName)
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
	valid := len(s) == 40
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			valid = false
		}
	}
	return valid
}

func firstKey(m map[string]any) string {
	key := ""
	for k := range m {
		key = k
	}
	return key
}

// getInto GETs path and decodes the JSON body into out.
func (rn *Runner) getInto(path string, out any) error {
	req, err := http.NewRequestWithContext(rn.ctx, http.MethodGet, rn.baseURL+path, nil)
	if err == nil {
		var resp *http.Response
		resp, err = rn.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				err = fmt.Errorf("HTTP %d: %s", resp.StatusCode, readBody(resp))
			} else {
				err = json.NewDecoder(resp.Body).Decode(out)
			}
		}
	}
	return err
}

func (rn *Runner) getJSON(path string) ([]any, error) {
	var out []any
	err := rn.getInto(path, &out)
	return out, err
}

var refRe = regexp.MustCompile(`\{([\w.]+)\}`)

// resolve substitutes {peer.ski}-style references against context, matching
// cli/scenario.py's _resolve: a value that is *exactly* one reference resolves to the
// referenced value's own type; a reference embedded in a larger string is substituted in
// place as a string. Maps and lists are resolved element by element.
func resolve(value any, scenarioContext map[string]any) any {
	out := value
	switch v := value.(type) {
	case string:
		if m := refRe.FindStringSubmatch(v); m != nil && m[0] == v {
			out, _ = lookup(m[1], scenarioContext)
		} else if refRe.MatchString(v) {
			out = refRe.ReplaceAllStringFunc(v, func(match string) string {
				path := refRe.FindStringSubmatch(match)[1]
				resolved, _ := lookup(path, scenarioContext)
				return fmt.Sprint(resolved)
			})
		}
	case map[string]any:
		m := make(map[string]any, len(v))
		for k, val := range v {
			m[k] = resolve(val, scenarioContext)
		}
		out = m
	case []any:
		list := make([]any, len(v))
		for i, val := range v {
			list[i] = resolve(val, scenarioContext)
		}
		out = list
	}
	return out
}

// lookup walks a dotted path through maps; a numeric segment indexes a list.
func lookup(dottedPath string, scenarioContext map[string]any) (any, bool) {
	var node any = scenarioContext
	found := true
	for _, part := range strings.Split(dottedPath, ".") {
		if found {
			node, found = child(node, part)
		}
	}
	if !found {
		node = nil
	}
	return node, found
}

func child(node any, part string) (any, bool) {
	var next any
	found := false
	switch container := node.(type) {
	case map[string]any:
		next, found = container[part]
	case []any:
		if index, err := strconv.Atoi(part); err == nil && index >= 0 && index < len(container) {
			next, found = container[index], true
		}
	}
	return next, found
}

func parseTimeout(value any) float64 {
	seconds := 0.0
	switch v := value.(type) {
	case int:
		seconds = float64(v)
	case float64:
		seconds = v
	case string:
		v = strings.TrimSpace(v)
		if strings.HasSuffix(v, "ms") {
			n, _ := strconv.ParseFloat(strings.TrimSuffix(v, "ms"), 64)
			seconds = n / 1000
		} else if strings.HasSuffix(v, "s") {
			seconds, _ = strconv.ParseFloat(strings.TrimSuffix(v, "s"), 64)
		} else {
			seconds, _ = strconv.ParseFloat(v, 64)
		}
	}
	return seconds
}

func readBody(resp *http.Response) string {
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}
