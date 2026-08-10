package scenario

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// runStep executes one step spec ({"verb": args}), matching cli/scenario.py's _run_step. A
// step failing must not crash the runner -- every branch returns a StepResult, never panics
// past this function (Go doesn't have Python's blanket "except Exception", so each branch is
// deliberately defensive rather than relying on a single recover()).
func (rn *Runner) runStep(stepSpec map[string]any, context map[string]any) StepResult {
	verb := firstKey(stepSpec)
	args := stepSpec[verb]

	switch verb {
	case "wait_connected":
		return rn.stepWaitConnected(args, context)
	case "sleep":
		return rn.stepSleep(args)
	case "log":
		return StepResult{Name: verb, Status: "passed", Detail: fmt.Sprint(args)}
	case "call":
		return rn.stepCall(args, context)
	case "put":
		return rn.stepPut(args, context)
	case "assert":
		return rn.stepAssert(args, context)
	case "expect_event":
		return rn.stepExpectEvent(args)
	default:
		return StepResult{Name: verb, Status: "failed", Detail: "unknown step verb: " + verb}
	}
}

func argsMap(args any) map[string]any {
	if m, ok := args.(map[string]any); ok {
		return m
	}
	return map[string]any{}
}

func (rn *Runner) stepWaitConnected(args any, context map[string]any) StepResult {
	a := argsMap(args)
	timeout := 10.0
	if t, ok := a["timeout"]; ok {
		timeout = parseTimeout(t)
	}
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for time.Now().Before(deadline) {
		peers, err := rn.getJSON("/api/v1/peers")
		if err == nil {
			var connected []any
			for _, p := range peers {
				if m, ok := p.(map[string]any); ok && m["connected"] == true {
					connected = append(connected, m)
				}
			}
			if len(connected) > 0 {
				requestedName, _ := lookup("peer.name", context)
				match := connected[0]
				for _, c := range connected {
					if m, ok := c.(map[string]any); ok && m["name"] == requestedName {
						match = m
						break
					}
				}
				context["peer"] = match
				return StepResult{Name: "wait_connected", Status: "passed"}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return StepResult{Name: "wait_connected", Status: "failed", Detail: fmt.Sprintf("no connected peer within %.0fs", timeout)}
}

func (rn *Runner) stepSleep(args any) StepResult {
	var seconds any = args
	if m, ok := args.(map[string]any); ok {
		seconds = m["seconds"]
		if seconds == nil {
			seconds = 1
		}
	}
	time.Sleep(time.Duration(parseTimeout(seconds) * float64(time.Second)))
	return StepResult{Name: "sleep", Status: "passed"}
}

// stepCall covers the one legacy `call:` verb still used by a scenario (lpc-heartbeat-loss.yaml):
// the old generic JSON-RPC passthrough (POST /api/v1/raw) doesn't exist in this rewrite --
// there is no more untyped RPC surface to proxy -- so known method names map onto their real
// typed REST equivalent instead. An unrecognized method fails with a clear message rather
// than silently no-op-ing.
func (rn *Runner) stepCall(args any, context map[string]any) StepResult {
	a := argsMap(args)
	method, _ := a["method"].(string)
	switch method {
	case "eg-lpc/StartHeartbeat":
		return rn.postNoBody("/api/v1/lpc/heartbeat/start", "call "+method)
	case "eg-lpc/StopHeartbeat":
		return rn.postNoBody("/api/v1/lpc/heartbeat/stop", "call "+method)
	default:
		return StepResult{Name: "call " + method, Status: "failed",
			Detail: fmt.Sprintf("%q has no typed REST equivalent in this rewrite (the generic RPC passthrough it used doesn't exist anymore)", method)}
	}
}

func (rn *Runner) postNoBody(path, name string) StepResult {
	resp, err := rn.client.Post(rn.baseURL+path, "application/json", bytes.NewReader(nil))
	if err != nil {
		return StepResult{Name: name, Status: "failed", Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return StepResult{Name: name, Status: "failed", Detail: readBody(resp)}
	}
	return StepResult{Name: name, Status: "passed"}
}

func (rn *Runner) stepPut(args any, context map[string]any) StepResult {
	a := argsMap(args)
	path, _ := resolve(a["path"], context).(string)
	name := "put " + path

	var body any
	if templateName, ok := a["template"].(string); ok {
		resp, err := rn.client.Get(rn.baseURL + "/api/v1/templates")
		if err != nil {
			return StepResult{Name: name, Status: "failed", Detail: err.Error()}
		}
		defer resp.Body.Close()
		var store map[string]map[string]map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&store); err != nil {
			return StepResult{Name: name, Status: "failed", Detail: err.Error()}
		}
		category, key := splitLastDot(templateName)
		entry, ok := store[category][key]
		if !ok {
			return StepResult{Name: name, Status: "failed", Detail: "no such template: " + templateName}
		}
		body = entry["value"]
	} else {
		body = a["body"]
	}

	payload, err := json.Marshal(resolve(body, context))
	if err != nil {
		return StepResult{Name: name, Status: "failed", Detail: err.Error()}
	}
	req, err := http.NewRequest(http.MethodPut, rn.baseURL+path, bytes.NewReader(payload))
	if err != nil {
		return StepResult{Name: name, Status: "failed", Detail: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := rn.client.Do(req)
	if err != nil {
		return StepResult{Name: name, Status: "failed", Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return StepResult{Name: name, Status: "failed", Detail: readBody(resp)}
	}
	return StepResult{Name: name, Status: "passed"}
}

func (rn *Runner) stepAssert(args any, context map[string]any) StepResult {
	a := argsMap(args)
	path, _ := resolve(a["get"], context).(string)
	resp, err := rn.client.Get(rn.baseURL + path)
	if err != nil {
		return StepResult{Name: "assert", Status: "failed", Detail: err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return StepResult{Name: "assert", Status: "failed", Detail: readBody(resp)}
	}
	var actual map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&actual); err != nil {
		return StepResult{Name: "assert", Status: "failed", Detail: err.Error()}
	}
	if mismatches := checkAssertions(a, actual); len(mismatches) > 0 {
		return StepResult{Name: "assert", Status: "failed", Detail: fmt.Sprintf("mismatches: %v", mismatches)}
	}
	return StepResult{Name: "assert", Status: "passed"}
}

func (rn *Runner) stepExpectEvent(args any) StepResult {
	a := argsMap(args)
	eventName, _ := a["event"].(string)
	timeout := 5.0
	if t, ok := a["within"]; ok {
		timeout = parseTimeout(t)
	}
	stepStartedAt := time.Now().Unix()
	deadline := time.Now().Add(time.Duration(timeout * float64(time.Second)))
	for time.Now().Before(deadline) {
		resp, err := rn.client.Get(rn.baseURL + "/api/v1/events/recent?limit=100")
		if err == nil {
			var recent []map[string]any
			if json.NewDecoder(resp.Body).Decode(&recent) == nil {
				for _, e := range recent {
					ts, _ := toFloat64(e["ts"])
					if e["event"] == eventName && int64(ts) >= stepStartedAt {
						resp.Body.Close()
						return StepResult{Name: "expect_event", Status: "passed"}
					}
				}
			}
			resp.Body.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
	return StepResult{Name: "expect_event", Status: "failed", Detail: fmt.Sprintf("%s not observed within %.0fs", eventName, timeout)}
}

// missingRequirements returns unmet live requirements so a non-applicable scenario is
// skipped, not failed, matching cli/scenario.py's _missing_requirements.
func (rn *Runner) missingRequirements(req requirementsSpec, context map[string]any) []string {
	var missing []string

	if len(req.Capabilities) > 0 {
		stacks, err := rn.getJSON("/api/v1/stacks")
		var available map[string]any
		if err == nil {
			for _, s := range stacks {
				m, ok := s.(map[string]any)
				if ok && m["status"] == "running" {
					if caps, ok := m["capabilities"].(map[string]any); ok && caps["live_control"] == true {
						available = caps
						break
					}
				}
			}
		}
		for _, cap := range req.Capabilities {
			if available == nil || available[cap] != true {
				missing = append(missing, "active stack lacks "+cap)
			}
		}
	}

	if len(req.UseCases) > 0 {
		ski, _ := lookup("peer.ski", context)
		skiStr, _ := ski.(string)
		if skiStr == "" {
			missing = append(missing, "no peer selected for use-case requirements")
		} else {
			resp, err := rn.client.Get(rn.baseURL + "/api/v1/peers/" + skiStr + "/usecases")
			if err != nil {
				missing = append(missing, "could not browse peer use cases: "+err.Error())
			} else {
				defer resp.Body.Close()
				if resp.StatusCode >= 400 {
					missing = append(missing, fmt.Sprintf("could not browse peer use cases: HTTP %d", resp.StatusCode))
				} else {
					var advertised []map[string]any
					_ = json.NewDecoder(resp.Body).Decode(&advertised)
					supported := map[string]bool{}
					for _, entry := range advertised {
						support, _ := entry["useCaseSupport"].([]any)
						for _, s := range support {
							sm, ok := s.(map[string]any)
							if !ok || sm["useCaseAvailable"] == false {
								continue
							}
							if name, ok := sm["useCaseName"].(string); ok {
								supported[name] = true
							}
						}
					}
					for _, uc := range req.UseCases {
						if !supported[uc] {
							missing = append(missing, "peer does not advertise "+uc)
						}
					}
				}
			}
		}
	}

	return missing
}

func splitLastDot(s string) (string, string) {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[:i], s[i+1:]
		}
	}
	return "", s
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}
