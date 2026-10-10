package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// maxResponseEvidence bounds the response body a step records.
const maxResponseEvidence = 4096

// runStep executes one step spec ({"verb": args}), matching cli/scenario.py's _run_step. A
// step failing must not crash the runner -- every branch returns a StepResult, never panics
// past this function (Go doesn't have Python's blanket "except Exception", so each branch is
// deliberately defensive rather than relying on a single recover()).
func (rn *Runner) runStep(stepSpec map[string]any, scenarioContext map[string]any) StepResult {
	verb := firstKey(stepSpec)
	args := stepSpec[verb]
	var step StepResult
	switch verb {
	case "wait_connected":
		step = rn.stepWaitConnected(args, scenarioContext)
	case "sleep":
		step = rn.stepSleep(args)
	case "log":
		step = StepResult{Name: verb, Status: StatusPassed, Detail: fmt.Sprint(resolve(args, scenarioContext))}
	case "call":
		step = rn.stepCall(args)
	case "put":
		step = rn.stepRequest(http.MethodPut, args, scenarioContext)
	case "post":
		step = rn.stepRequest(http.MethodPost, args, scenarioContext)
	case "delete":
		step = rn.stepRequest(http.MethodDelete, args, scenarioContext)
	case "assert":
		step = rn.stepAssert(args, scenarioContext)
	case "expect_event":
		step = rn.stepExpectEvent(args, scenarioContext)
	case "capture":
		step = rn.stepCapture(args, scenarioContext)
	case "scenarios_advertised":
		step = rn.stepScenariosAdvertised(args, scenarioContext)
	case "limit_descriptions":
		step = rn.stepLimitDescriptions(args, scenarioContext)
	case "skip_unless":
		step = rn.stepSkipUnless(args, scenarioContext)
	case "conformance":
		step = rn.stepConformance(args, scenarioContext)
	case "wait_for":
		step = rn.stepWaitFor(args, scenarioContext)
	default:
		step = StepResult{Name: verb, Status: StatusFailed, Detail: "unknown step verb: " + verb}
	}
	return step
}

func argsMap(args any) map[string]any {
	m, ok := args.(map[string]any)
	if !ok {
		m = map[string]any{}
	}
	return m
}

// pause waits for d or until the run is cancelled.
func (rn *Runner) pause(d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-rn.ctx.Done():
	}
}

func (rn *Runner) stepWaitConnected(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(args)
	timeout := 10.0
	if t, ok := a["timeout"]; ok {
		timeout = parseTimeout(t)
	}
	step := StepResult{Name: "wait_connected", Status: StatusFailed, Detail: fmt.Sprintf("no connected peer within %.0fs", timeout)}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for step.Status == StatusFailed && time.Now().Before(deadline) && !rn.cancelled() {
		if match := rn.connectedPeer(scenarioContext); match != nil {
			scenarioContext["peer"] = match
			step = StepResult{Name: "wait_connected", Status: StatusPassed}
		} else {
			rn.pause(500 * time.Millisecond)
		}
	}
	return step
}

// connectedPeer returns the selected peer when it is connected; without a selected peer,
// the first connected one.
func (rn *Runner) connectedPeer(scenarioContext map[string]any) map[string]any {
	var match map[string]any
	peers, err := rn.getJSON("/api/v1/peers")
	wanted, _ := lookup("peer.ski", scenarioContext)
	if err == nil {
		for _, p := range peers {
			m, ok := p.(map[string]any)
			if ok && m["connected"] == true && match == nil && (wanted == nil || m["ski"] == wanted) {
				match = m
			}
		}
	}
	return match
}

func (rn *Runner) stepSleep(args any) StepResult {
	var seconds any = args
	if m, ok := args.(map[string]any); ok {
		seconds = m["seconds"]
		if seconds == nil {
			seconds = 1
		}
	}
	d := parseTimeout(seconds)
	rn.pause(time.Duration(d * float64(time.Second)))
	return StepResult{Name: fmt.Sprintf("sleep %gs", d), Status: StatusPassed}
}

// callPaths maps the legacy `call:` method names onto their typed REST equivalents. The old
// generic JSON-RPC passthrough (POST /api/v1/raw) doesn't exist in this rewrite -- there is
// no more untyped RPC surface to proxy -- so an unrecognized method fails with a clear
// message rather than silently no-op-ing.
var callPaths = map[string]string{
	"eg-lpc/StartHeartbeat":    "/api/v1/lpc/heartbeat/start",
	"eg-lpc/StopHeartbeat":     "/api/v1/lpc/heartbeat/stop",
	"cem-opev/StartHeartbeat":  "/api/v1/opev/heartbeat/start",
	"cem-opev/StopHeartbeat":   "/api/v1/opev/heartbeat/stop",
	"cem-oscev/StartHeartbeat": "/api/v1/oscev/heartbeat/start",
	"cem-oscev/StopHeartbeat":  "/api/v1/oscev/heartbeat/stop",
}

func (rn *Runner) stepCall(args any) StepResult {
	method, _ := argsMap(args)["method"].(string)
	step := StepResult{Name: "call " + method}
	if path, ok := callPaths[method]; ok {
		evidence, _, err := rn.do(http.MethodPost, path, nil)
		step.Request = evidence
		step.Status, step.Detail = judge(evidence, err, nil)
	} else {
		step.Status = StatusFailed
		step.Detail = fmt.Sprintf("%q has no typed REST equivalent in this rewrite (the generic RPC passthrough it used doesn't exist anymore)", method)
	}
	return step
}

// do sends one request with the run's context and records it. The returned bytes are the
// full response body; the evidence keeps at most maxResponseEvidence of it.
func (rn *Runner) do(method, path string, body any) (*RequestEvidence, []byte, error) {
	evidence := &RequestEvidence{Method: method, Path: path, Body: body}
	var raw []byte
	var payload io.Reader
	var err error
	if body != nil {
		var encoded []byte
		encoded, err = json.Marshal(body)
		payload = bytes.NewReader(encoded)
	}
	var req *http.Request
	if err == nil {
		req, err = http.NewRequestWithContext(rn.ctx, method, rn.baseURL+path, payload)
	}
	if err == nil {
		req.Header.Set("Content-Type", "application/json")
		var resp *http.Response
		resp, err = rn.client.Do(req)
		if err == nil {
			defer resp.Body.Close()
			evidence.Status = resp.StatusCode
			raw, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
			evidence.Response = string(raw)
			if len(raw) > maxResponseEvidence {
				evidence.Response = string(raw[:maxResponseEvidence])
				evidence.Truncated = true
			}
		}
	}
	return evidence, raw, err
}

// judge turns a request outcome into a step verdict. expectStatus, when set, asserts the
// exact HTTP status instead of the default any-2xx rule, which is what lets a scenario state
// that a device MUST refuse an operation.
func judge(evidence *RequestEvidence, err error, expectStatus any) (string, string) {
	status, detail := StatusPassed, ""
	want, hasWant := toFloat64(expectStatus)
	switch {
	case err != nil:
		status, detail = StatusFailed, err.Error()
	case hasWant && want > 0 && evidence.Status != int(want):
		status, detail = StatusFailed, fmt.Sprintf("expected HTTP %d, got %d: %s", int(want), evidence.Status, evidence.Response)
	case hasWant && want > 0:
		detail = fmt.Sprintf("HTTP %d as expected", evidence.Status)
	case evidence.Status >= 400:
		status, detail = StatusFailed, evidence.Response
	}
	return status, detail
}

// stepRequest backs the put/post/delete verbs.
func (rn *Runner) stepRequest(method string, args any, scenarioContext map[string]any) StepResult {
	a := argsMap(args)
	path, _ := resolve(a["path"], scenarioContext).(string)
	step := StepResult{Name: strings.ToLower(method) + " " + path}
	body, err := rn.requestBody(a, scenarioContext)
	if err != nil {
		step.Status, step.Detail = StatusFailed, err.Error()
	} else {
		evidence, _, reqErr := rn.do(method, path, body)
		step.Request = evidence
		step.Status, step.Detail = judge(evidence, reqErr, a["expect_status"])
	}
	return step
}

// requestBody is the step's body: a named template from the built-in library, or the inline
// body, with references resolved.
func (rn *Runner) requestBody(a map[string]any, scenarioContext map[string]any) (any, error) {
	var body any
	var err error
	if templateName, ok := a["template"].(string); ok {
		var store map[string]map[string]map[string]any
		err = rn.getInto("/api/v1/templates", &store)
		if err == nil {
			category, key := splitLastDot(templateName)
			entry, found := store[category][key]
			if found {
				body = entry["value"]
			} else {
				err = fmt.Errorf("no such template: %s", templateName)
			}
		}
	} else if a["body"] != nil {
		body = resolve(a["body"], scenarioContext)
	}
	return body, err
}

// fetchObject GETs path and returns the JSON body as an object. A list body is wrapped as
// {"items": [...], "count": n} so assertions can address it.
func (rn *Runner) fetchObject(path string) (*RequestEvidence, map[string]any, error) {
	evidence, raw, err := rn.do(http.MethodGet, path, nil)
	var object map[string]any
	if err == nil && evidence.Status >= 400 {
		err = fmt.Errorf("HTTP %d: %s", evidence.Status, evidence.Response)
	} else if err == nil {
		var decoded any
		if err = json.Unmarshal(raw, &decoded); err == nil {
			switch v := decoded.(type) {
			case map[string]any:
				object = v
			case []any:
				object = map[string]any{"items": v, "count": len(v)}
			default:
				err = fmt.Errorf("expected a JSON object, got %T", decoded)
			}
		}
	}
	return evidence, object, err
}

func (rn *Runner) stepAssert(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(resolve(args, scenarioContext))
	path, _ := a["get"].(string)
	step := StepResult{Name: "assert " + path}
	evidence, actual, err := rn.fetchObject(path)
	step.Request = evidence
	if err != nil {
		step.Status, step.Detail = StatusFailed, err.Error()
	} else {
		step.Assertions = evaluateAssertions(a, actual)
		var mismatches []string
		for _, c := range step.Assertions {
			if !c.OK {
				mismatches = append(mismatches, c.Key+": "+c.Message)
			}
		}
		step.Status = StatusPassed
		if len(mismatches) > 0 {
			step.Status, step.Detail = StatusFailed, "mismatches: "+strings.Join(mismatches, "; ")
		}
	}
	return step
}

func (rn *Runner) stepExpectEvent(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(args)
	eventName, _ := a["event"].(string)
	timeout := 5.0
	if t, ok := a["within"]; ok {
		timeout = parseTimeout(t)
	}
	step := StepResult{Name: "expect_event " + eventName, Status: StatusFailed,
		Detail: fmt.Sprintf("%s not observed within %.0fs", eventName, timeout)}
	// The window starts at the step that wrote, not at this step: the event a write causes can
	// arrive before this step starts. The previous step's end position is the start of this one.
	after, hasAfter := toFloat64(scenarioContext["events_before_step"])
	stepStartedAt := time.Now().Unix()
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	// Only the scenario's own peer counts: another device, or a simulated one, publishing the
	// same event must not satisfy the expectation.
	ski := peerSKI(scenarioContext)
	for step.Status == StatusFailed && time.Now().Before(deadline) && !rn.cancelled() {
		var recent []map[string]any
		if rn.getInto("/api/v1/events/recent?limit=100&ski="+url.QueryEscape(ski), &recent) == nil {
			for _, e := range recent {
				ts, _ := toFloat64(e["ts"])
				seq, _ := toFloat64(e["seq"])
				fresh := (hasAfter && seq > after) || (!hasAfter && int64(ts) >= stepStartedAt)
				fromPeer := ski == "" || strings.EqualFold(fmt.Sprint(e["ski"]), ski)
				if e["event"] == eventName && fresh && fromPeer && step.Status == StatusFailed {
					step.Status, step.Detail, step.Event = StatusPassed, "", e
				}
			}
		}
		if step.Status == StatusFailed {
			rn.pause(300 * time.Millisecond)
		}
	}
	return step
}

// stepWaitFor repeats an assertion once a second until it holds or its timeout passes:
// `wait_for: {get: path, not_null: [heartbeat_timeout_s], timeout: 70s}`. It is for values a
// device publishes on its own schedule, such as the first heartbeat after a connection.
func (rn *Runner) stepWaitFor(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(resolve(args, scenarioContext))
	timeout := 30.0
	if t, ok := a["timeout"]; ok {
		timeout = parseTimeout(t)
	}
	start := time.Now()
	deadline := start.Add(time.Duration(timeout * float64(time.Second)))
	var step StepResult
	for done := false; !done; {
		step = rn.stepAssert(a, scenarioContext)
		done = step.Status == StatusPassed || !time.Now().Before(deadline) || rn.cancelled()
		if !done {
			rn.pause(time.Second)
		}
	}
	path, _ := a["get"].(string)
	step.Name = "wait_for " + path
	if step.Status == StatusPassed {
		step.Detail = fmt.Sprintf("held after %.0f s", time.Since(start).Seconds())
	} else {
		step.Detail = fmt.Sprintf("still not holding after %.0f s: %s", time.Since(start).Seconds(), step.Detail)
	}
	return step
}

// stepCapture stores values from a response for later steps:
// `capture: {get: path, values: {energy: energy_consumed_wh}}` makes {captured.energy}
// available. A value that is absent fails the step, unless `optional: true`, which records it
// as null -- for remembering a device's settings in order to restore them.
func (rn *Runner) stepCapture(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(resolve(args, scenarioContext))
	path, _ := a["get"].(string)
	step := StepResult{Name: "capture " + path, Status: StatusPassed}
	evidence, actual, err := rn.fetchObject(path)
	step.Request = evidence
	captured, _ := scenarioContext["captured"].(map[string]any)
	if captured == nil {
		captured = map[string]any{}
		scenarioContext["captured"] = captured
	}
	if err != nil {
		step.Status, step.Detail = StatusFailed, err.Error()
	} else {
		var parts, missing []string
		for _, name := range sortedKeys(asMap(a["values"])) {
			key, _ := asMap(a["values"])[name].(string)
			value := valueAt(actual, key)
			captured[name] = value
			if value == nil {
				missing = append(missing, key)
			}
			parts = append(parts, fmt.Sprintf("%s=%v", name, value))
		}
		step.Detail = strings.Join(parts, ", ")
		if optional, _ := a["optional"].(bool); len(missing) > 0 && !optional {
			step.Status, step.Detail = StatusFailed, "absent: "+strings.Join(missing, ", ")
		}
	}
	return step
}

// stepScenariosAdvertised compares the scenarios the device advertises for a use case with
// the specification's scenario implementation table: a mandatory scenario that is missing
// fails, a recommended or optional one is reported.
func (rn *Runner) stepScenariosAdvertised(args any, scenarioContext map[string]any) StepResult {
	key, _ := argsMap(args)["use_case"].(string)
	uc, known := ucspec.Lookup(key)
	ski, _ := lookup("peer.ski", scenarioContext)
	path := fmt.Sprintf("/api/v1/peers/%v/usecases", ski)
	step := StepResult{Name: "scenarios_advertised " + key}
	if !known {
		step.Status, step.Detail = StatusFailed, "unknown use case "+key
	} else {
		evidence, raw, err := rn.do(http.MethodGet, path, nil)
		step.Request = evidence
		var advertised []map[string]any
		if err == nil && evidence.Status < 400 {
			err = json.Unmarshal(raw, &advertised)
		} else if err == nil {
			err = fmt.Errorf("HTTP %d", evidence.Status)
		}
		if err != nil {
			step.Status, step.Detail = StatusFailed, err.Error()
		} else {
			step.Status, step.Detail, step.Assertions = judgeScenarios(uc, advertisedScenarios(advertised))
		}
	}
	return step
}

func judgeScenarios(uc ucspec.UseCase, support map[string][]uint) (string, string, []Comparison) {
	numbers, advertised := support[uc.Name]
	status, detail := StatusPassed, ""
	var comparisons []Comparison
	if !advertised {
		status, detail = StatusFailed, "the device does not advertise "+uc.Name
	} else {
		have := map[uint]bool{}
		for _, n := range numbers {
			have[n] = true
		}
		for _, s := range uc.Scenarios {
			comparisons = append(comparisons, Comparison{
				Key: fmt.Sprintf("scenario %d %s", s.Number, s.Title), Op: "advertised",
				Expected: levelWord(s.Device), Actual: have[s.Number],
				OK: have[s.Number] || s.Device != ucspec.Mandatory,
			})
		}
		mandatory, others := uc.MissingScenarios(numbers)
		detail = fmt.Sprintf("advertises scenarios %v", numbers)
		if len(mandatory) > 0 {
			status = StatusFailed
			detail += fmt.Sprintf("; mandatory for the %s per %s %s but not advertised: %s",
				uc.DeviceActor, uc.Document, uc.Table, scenarioList(mandatory))
		}
		if len(others) > 0 {
			detail += "; not advertised (recommended or optional): " + scenarioList(others)
		}
	}
	return status, detail, comparisons
}

func scenarioList(scenarios []ucspec.Scenario) string {
	parts := make([]string, 0, len(scenarios))
	for _, s := range scenarios {
		parts = append(parts, fmt.Sprintf("%d %s", s.Number, s.Title))
	}
	return strings.Join(parts, ", ")
}

// stepConformance requires the frames of this test case so far to carry no conformance
// finding of severity error: `conformance: {max_errors: 0}`.
func (rn *Runner) stepConformance(args any, scenarioContext map[string]any) StepResult {
	maxErrors, _ := toFloat64(argsMap(args)["max_errors"])
	from, _ := toFloat64(scenarioContext["trace_from"])
	// The device's own frames: what it sent to us, not what we or another peer put on the wire.
	path := fmt.Sprintf("/api/v1/trace?after=%d&findings=only&limit=2000&dir=recv&ski=%s", int64(from), url.QueryEscape(peerSKI(scenarioContext)))
	step := StepResult{Name: "conformance"}
	var page struct {
		Entries []struct {
			Findings []struct {
				Rule     string `json:"rule"`
				Severity string `json:"severity"`
			} `json:"findings"`
		} `json:"entries"`
	}
	if err := rn.getInto(path, &page); err != nil {
		step.Status, step.Detail = StatusFailed, err.Error()
	} else {
		errorFrames, warnings := 0, 0
		rules := map[string]int{}
		for _, entry := range page.Entries {
			isError := false
			for _, f := range entry.Findings {
				rules[f.Rule]++
				isError = isError || f.Severity == "error"
			}
			if isError {
				errorFrames++
			} else {
				warnings++
			}
		}
		step.Assertions = []Comparison{{Key: "frames with errors", Op: "less_or_equal", Expected: maxErrors,
			Actual: errorFrames, OK: float64(errorFrames) <= maxErrors}}
		step.Status = StatusPassed
		step.Detail = fmt.Sprintf("%d frame(s) with errors, %d with warnings only", errorFrames, warnings)
		if len(rules) > 0 {
			step.Detail += "; rules: " + ruleList(rules)
		}
		if float64(errorFrames) > maxErrors {
			step.Status = StatusFailed
		}
	}
	return step
}

func ruleList(rules map[string]int) string {
	parts := make([]string, 0, len(rules))
	for _, rule := range sortedKeys(rules) {
		parts = append(parts, fmt.Sprintf("%s x%d", rule, rules[rule]))
	}
	return strings.Join(parts, ", ")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func splitLastDot(s string) (string, string) {
	head, tail := "", s
	if i := strings.LastIndexByte(s, '.'); i >= 0 {
		head, tail = s[:i], s[i+1:]
	}
	return head, tail
}

func toFloat64(v any) (float64, bool) {
	f, ok := 0.0, true
	switch n := v.(type) {
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	case uint:
		f = float64(n)
	default:
		ok = false
	}
	return f, ok
}

// peerSKI is the SKI of the scenario's resolved peer, or "" when the scenario names none.
func peerSKI(scenarioContext map[string]any) string {
	ski, _ := lookup("peer.ski", scenarioContext)
	text, _ := ski.(string)
	return text
}

// stepSkipUnless skips the scenario, as a precondition not met, when an assertion does not
// hold: `skip_unless: {get: path, not_null: [power_per_phase_w], reason: "..."}`. For
// values a specification makes optional: their absence is no failure, and the checks that
// need them cannot run.
func (rn *Runner) stepSkipUnless(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(resolve(args, scenarioContext))
	reason, _ := a["reason"].(string)
	delete(a, "reason")
	step := rn.stepAssert(a, scenarioContext)
	path, _ := a["get"].(string)
	step.Name = "skip_unless " + path
	if step.Status == StatusFailed {
		step.Status = StatusSkipped
		step.Detail = reason
		if reason == "" {
			step.Detail = "the values this test case needs are not published"
		}
	}
	return step
}
