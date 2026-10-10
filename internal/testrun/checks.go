package testrun

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/scenario"
	"github.com/marck-brusa/eebuster/internal/trace"
)

// conformanceTracker aggregates the conformance findings of the device's own frames (what it
// sent to us) from the start of the run. It is sampled between test cases and, by the run's
// sampler, during them, so the bounded trace window does not drop frames unseen; if it does,
// the tracker says so.
type conformanceTracker struct {
	mu                   sync.Mutex
	c                    *collector
	ski                  string
	start, last          int64
	frames               int
	errorFrames, warning int
	overrun              bool
	rules                map[string]*report.RuleCount
}

func newConformanceTracker(c *collector, device report.Device, start *report.Conditions) *conformanceTracker {
	seq := int64(0)
	if start != nil {
		seq = start.TraceSeq
	}
	return &conformanceTracker{c: c, ski: device.SKI, start: seq, last: seq, rules: map[string]*report.RuleCount{}}
}

func (t *conformanceTracker) collect() {
	t.mu.Lock()
	defer t.mu.Unlock()
	var page struct {
		Latest  int64 `json:"latest_seq"`
		Entries []struct {
			Seq      int64 `json:"seq"`
			Findings []struct {
				Rule     string `json:"rule"`
				Severity string `json:"severity"`
				Message  string `json:"message"`
				SpecRef  string `json:"spec_ref"`
			} `json:"findings"`
		} `json:"entries"`
	}
	query := url.Values{"after": {fmt.Sprint(t.last)}, "limit": {fmt.Sprint(trace.RingCapacity)}, "dir": {"recv"}, "ski": {t.ski}}
	if t.ski != "" && t.c.get("/api/v1/trace?"+query.Encode(), &page) == nil {
		t.overrun = t.overrun || page.Latest-t.last > trace.RingCapacity
		for _, entry := range page.Entries {
			t.frames++
			isError := false
			seen := map[string]bool{}
			for _, f := range entry.Findings {
				isError = isError || f.Severity == "error"
				rc, ok := t.rules[f.Rule]
				if !ok {
					rc = &report.RuleCount{Rule: f.Rule, Severity: f.Severity, SpecRef: f.SpecRef, FirstSeq: entry.Seq, Example: f.Message}
					t.rules[f.Rule] = rc
				}
				if !seen[f.Rule] {
					rc.Count++
					seen[f.Rule] = true
				}
			}
			if isError {
				t.errorFrames++
			} else if len(entry.Findings) > 0 {
				t.warning++
			}
		}
		if page.Latest > t.last {
			t.last = page.Latest
		}
	}
}

func (t *conformanceTracker) result() report.Conformance {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := report.Conformance{Frames: t.frames, ErrorFrames: t.errorFrames, WarningFrames: t.warning, Overrun: t.overrun, Rules: []report.RuleCount{}}
	for _, rc := range t.rules {
		out.Rules = append(out.Rules, *rc)
	}
	sort.Slice(out.Rules, func(i, j int) bool {
		a, b := out.Rules[i], out.Rules[j]
		less := a.Rule < b.Rule
		if a.Severity != b.Severity {
			less = a.Severity == "error"
		} else if a.Count != b.Count {
			less = a.Count > b.Count
		}
		return less
	})
	return out
}

// heartbeatMonitor collects the device's LPC heartbeat frames from the trace after every test
// case, so the check can measure the gaps between them instead of sampling a state that only
// turns true once the first heartbeat after a connection arrived.
type heartbeatMonitor struct {
	mu      sync.Mutex
	ski     string
	active  bool
	cursor  int64
	overrun bool
	times   []float64
}

func newHeartbeatMonitor(d report.Device, start *report.Conditions) *heartbeatMonitor {
	cursor := int64(0)
	if start != nil {
		cursor = start.TraceSeq
	}
	return &heartbeatMonitor{ski: d.SKI, active: d.Connected && advertises(d, "limitationOfPowerConsumption"), cursor: cursor}
}

func (h *heartbeatMonitor) sample(c *collector) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.active {
		var page struct {
			Latest  int64 `json:"latest_seq"`
			Entries []struct {
				Ts         float64 `json:"ts"`
				Function   string  `json:"function"`
				Classifier string  `json:"classifier"`
			} `json:"entries"`
		}
		query := url.Values{"after": {fmt.Sprint(h.cursor)}, "dir": {"recv"}, "ski": {h.ski}, "limit": {fmt.Sprint(trace.RingCapacity)}}
		if c.get("/api/v1/trace?"+query.Encode(), &page) == nil {
			h.overrun = h.overrun || page.Latest-h.cursor > trace.RingCapacity
			for _, e := range page.Entries {
				if e.Function == "deviceDiagnosisHeartbeatData" && (e.Classifier == "notify" || e.Classifier == "reply") {
					h.times = append(h.times, e.Ts)
				}
			}
			if page.Latest > h.cursor {
				h.cursor = page.Latest
			}
		}
	}
}

// runChecks computes the run-level verdicts.
func runChecks(c *collector, r *report.Run, conformance *conformanceTracker, heartbeat *heartbeatMonitor) []report.Check {
	checks := []report.Check{conformanceCheck(conformance.result()), connectionCheck(c, r)}
	checks = append(checks, vehicleCheck(r))
	if heartbeat.active {
		checks = append(checks, heartbeatCheck(heartbeat, r))
	}
	return checks
}

func conformanceCheck(conf report.Conformance) report.Check {
	check := report.Check{ID: "wire-conformance", Title: "Wire conformance", Status: scenario.StatusPassed,
		Detail: fmt.Sprintf("%d frames from the device, none with a conformance error; %d with warnings only.", conf.Frames, conf.WarningFrames)}
	if conf.Overrun {
		check.Status = scenario.StatusSkipped
		check.Detail = fmt.Sprintf("More than %d frames arrived between two samples, so some were never checked; %d of the %d frames seen carry conformance errors.", trace.RingCapacity, conf.ErrorFrames, conf.Frames)
	} else if conf.ErrorFrames > 0 {
		var rules []string
		for _, rc := range conf.Rules {
			if rc.Severity == "error" {
				rules = append(rules, fmt.Sprintf("%s (%d)", rc.Rule, rc.Count))
			}
		}
		check.Status = scenario.StatusFailed
		check.Detail = fmt.Sprintf("%d of %d frames carry conformance errors: %s.", conf.ErrorFrames, conf.Frames, strings.Join(rules, ", "))
	}
	return check
}

// connectionCheck counts the disconnections of the device during the run.
func connectionCheck(c *collector, r *report.Run) report.Check {
	check := report.Check{ID: "connection", Title: "Connection stability", Status: scenario.StatusPassed,
		Detail: "The device stayed connected from the first to the last test case."}
	from := int64(0)
	if r.ConditionsStart != nil {
		from = r.ConditionsStart.EventSeq
	}
	var events []struct {
		Seq   int64  `json:"seq"`
		Event string `json:"event"`
	}
	query := url.Values{"level": {"lifecycle"}, "ski": {r.Device.SKI}, "limit": {"2000"}}
	disconnects := 0
	if r.Device.SKI != "" && c.get("/api/v1/events/recent?"+query.Encode(), &events) == nil {
		for _, e := range events {
			if e.Seq > from && e.Event == "remote_disconnected" {
				disconnects++
			}
		}
	}
	connectedAtEnd := r.ConditionsEnd != nil && r.ConditionsEnd.Connected
	switch {
	case r.Device.SKI == "":
		check.Status, check.Detail = scenario.StatusFailed, "No device was resolved for the run."
	case disconnects > 0 || !connectedAtEnd:
		check.Status = scenario.StatusFailed
		check.Detail = fmt.Sprintf("The device disconnected %d time(s) during the run; connected at the end: %s.", disconnects, yesNo(connectedAtEnd))
	}
	return check
}

func vehicleCheck(r *report.Run) report.Check {
	check := report.Check{ID: "vehicle", Title: "Vehicle stability", Status: scenario.StatusSkipped,
		Detail: "No vehicle was connected at the start or the end of the run."}
	start, end := vehicleKeys(r.VehiclesStart), vehicleKeys(r.VehiclesEnd)
	if len(start) > 0 || len(end) > 0 {
		check.Status, check.Detail = scenario.StatusPassed, fmt.Sprintf("The same %d vehicle(s) were present at the start and the end.", len(start))
		if vehiclesChanged(r.VehiclesStart, r.VehiclesEnd) {
			check.Status = scenario.StatusFailed
			check.Detail = fmt.Sprintf("The vehicles changed during the run: at start %s, at end %s. Test cases after the change may describe another vehicle.",
				orNone(start), orNone(end))
		}
	}
	return check
}

// vehiclesChanged compares the vehicles by entity address, and by identification only when
// both snapshots carry one: the identification arrives a moment after the vehicle itself.
func vehiclesChanged(start, end []map[string]any) bool {
	byEntity := func(list []map[string]any) map[string]string {
		out := map[string]string{}
		for _, v := range list {
			out[fmt.Sprint(v["entity"])] = fmt.Sprint(v["identifications"])
		}
		return out
	}
	a, b := byEntity(start), byEntity(end)
	changed := len(a) != len(b)
	for entity, idA := range a {
		idB, ok := b[entity]
		changed = changed || !ok || (idA != "<nil>" && idB != "<nil>" && idA != idB)
	}
	return changed
}

func vehicleKeys(vehicles []map[string]any) []string {
	var keys []string
	for _, v := range vehicles {
		keys = append(keys, fmt.Sprintf("%v %v", v["entity"], v["identifications"]))
	}
	sort.Strings(keys)
	return keys
}

func orNone(list []string) string {
	text := "none"
	if len(list) > 0 {
		text = strings.Join(list, ", ")
	}
	return text
}

// heartbeatCheck requires the device's heartbeats to follow each other, and the run's start
// and end, within the timeout the device announces (60 s when it announces none). Frames carry
// transmission and processing jitter, so the timeout is allowed 10 %, at least 2 s, on top.
func heartbeatCheck(h *heartbeatMonitor, r *report.Run) report.Check {
	timeout := 60.0
	for _, c := range []*report.Conditions{r.ConditionsStart, r.ConditionsEnd} {
		if c != nil && c.HeartbeatTimeoutS != nil && *c.HeartbeatTimeoutS > 0 {
			timeout = *c.HeartbeatTimeoutS
		}
	}
	// The window is where the heartbeat frames were collected from: the first sample was
	// taken at the start conditions, after the device was identified, the last at the end
	// conditions.
	start := float64(r.StartedAt.UnixNano()) / 1e9
	end := start + r.DurationS
	if r.ConditionsStart != nil && !r.ConditionsStart.At.IsZero() {
		start = unixSeconds(r.ConditionsStart.At)
	}
	if r.ConditionsEnd != nil && !r.ConditionsEnd.At.IsZero() {
		end = unixSeconds(r.ConditionsEnd.At)
	}
	h.mu.Lock()
	times, overrun := append([]float64{}, h.times...), h.overrun
	h.mu.Unlock()
	points := append(append([]float64{start}, times...), end)
	sort.Float64s(points)
	longest := 0.0
	for i := 1; i < len(points); i++ {
		longest = max(longest, points[i]-points[i-1])
	}
	allowed := timeout + max(2, timeout*0.1)
	check := report.Check{ID: "device-heartbeat", Title: "Device heartbeat (LPC)", Status: scenario.StatusPassed,
		Detail: fmt.Sprintf("The device sent %d heartbeat(s) in the %.0f s run; the longest time without one was %.1f s, within the announced timeout of %.0f s (%.0f s allowed for jitter).",
			len(times), end-start, longest, timeout, allowed-timeout)}
	switch {
	case overrun:
		check.Status = scenario.StatusSkipped
		check.Detail = fmt.Sprintf("More than %d frames arrived between two samples, so heartbeats may have gone unseen; %d were seen in the %.0f s run.", trace.RingCapacity, len(times), end-start)
	case len(times) == 0 && end-start <= timeout:
		check.Status = scenario.StatusSkipped
		check.Detail = fmt.Sprintf("No device heartbeat arrived during the %.0f s run, which is shorter than the announced timeout of %.0f s.", end-start, timeout)
	case longest > allowed:
		check.Status = scenario.StatusFailed
		check.Detail = fmt.Sprintf("The device sent %d heartbeat(s) in the %.0f s run; the longest time without one was %.1f s, more than the announced timeout of %.0f s plus %.0f s for jitter.",
			len(times), end-start, longest, timeout, allowed-timeout)
	}
	return check
}

func unixSeconds(t time.Time) float64 {
	return float64(t.UnixNano()) / 1e9
}

func yesNo(b bool) string {
	text := "no"
	if b {
		text = "yes"
	}
	return text
}
