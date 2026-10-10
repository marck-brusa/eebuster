package scenario

import (
	"fmt"
	"net/url"
)

// Bounds on the evidence kept per test case and per step. A device that puts the same
// non-conformant frame on the wire every few seconds would otherwise fill a report with
// copies; the totals stay exact.
const (
	maxFindingsPerCase = 50
	maxFindingsPerStep = 20
	maxEventsPerStep   = 20
)

// mark is the newest trace frame and event sequence number at one moment. -1 means the
// testbench did not answer, and no range is recorded.
type mark struct {
	trace  int64
	events int64
}

// tracker brackets steps with trace and event sequence numbers, so a report can say which
// frames and events a step produced.
type tracker struct {
	rn   *Runner
	last *mark
}

func newTracker(rn *Runner) *tracker { return &tracker{rn: rn} }

// mark returns the position before a step: the position after the previous step when there
// was one, so consecutive steps cost one pair of requests each.
func (t *tracker) mark() mark {
	var m mark
	if t.last != nil {
		m = *t.last
	} else {
		m = t.fetch()
	}
	return m
}

// markAfter returns the position after a step and remembers it for the next one.
func (t *tracker) markAfter() mark {
	m := t.fetch()
	t.last = &m
	return m
}

func (t *tracker) fetch() mark {
	m := mark{trace: -1, events: -1}
	var trace struct {
		Latest int64 `json:"latest_seq"`
	}
	if t.rn.getInto("/api/v1/trace?limit=1", &trace) == nil {
		m.trace = trace.Latest
	}
	var events []struct {
		Seq int64 `json:"seq"`
	}
	if t.rn.getInto("/api/v1/events/recent?limit=1", &events) == nil {
		m.events = 0
		if len(events) > 0 {
			m.events = events[0].Seq
		}
	}
	return m
}

func rangeBetween(before, after int64) *SeqRange {
	var r *SeqRange
	if before >= 0 && after >= 0 {
		r = &SeqRange{From: before + 1, To: after}
	}
	return r
}

// attach sets the test case's trace window and distributes the conformance findings and
// use-case events of that window over the steps they occurred in.
func (t *tracker) attach(result *ScenarioResult, from mark, ski string) {
	end := t.fetch()
	result.Trace = rangeBetween(from.trace, end.trace)
	if result.Trace != nil && result.Trace.To >= result.Trace.From {
		t.attachFindings(result, from.trace, end.trace, ski)
	}
	if from.events >= 0 && end.events > from.events {
		t.attachEvents(result, from.events, end.events, ski)
	}
}

func (t *tracker) attachFindings(result *ScenarioResult, after, until int64, ski string) {
	var page struct {
		Entries []struct {
			Seq      int64  `json:"seq"`
			Dir      string `json:"dir"`
			Function string `json:"function"`
			Findings []struct {
				Rule     string `json:"rule"`
				Severity string `json:"severity"`
				Message  string `json:"message"`
				SpecRef  string `json:"spec_ref"`
			} `json:"findings"`
		} `json:"entries"`
	}
	// The device's own frames only: findings on what we or another peer sent are not its.
	if t.rn.getInto(fmt.Sprintf("/api/v1/trace?after=%d&findings=only&limit=2000&dir=recv&ski=%s", after, url.QueryEscape(ski)), &page) == nil {
		for _, entry := range page.Entries {
			for _, f := range entry.Findings {
				if entry.Seq <= until {
					ref := FindingRef{Seq: entry.Seq, Dir: entry.Dir, Function: entry.Function,
						Rule: f.Rule, Severity: f.Severity, Message: f.Message, SpecRef: f.SpecRef}
					result.FindingsTotal++
					if len(result.Findings) < maxFindingsPerCase {
						result.Findings = append(result.Findings, ref)
					}
					addStepFinding(result.Steps, ref)
				}
			}
		}
	}
}

func addStepFinding(steps []StepResult, ref FindingRef) {
	for i := range steps {
		if steps[i].Trace.contains(ref.Seq) {
			steps[i].FindingsTotal++
			if len(steps[i].Findings) < maxFindingsPerStep {
				steps[i].Findings = append(steps[i].Findings, ref)
			}
		}
	}
}

func (t *tracker) attachEvents(result *ScenarioResult, after, until int64, ski string) {
	query := url.Values{"limit": {"2000"}, "level": {"usecase"}}
	if ski != "" {
		query.Set("ski", ski)
	}
	var events []struct {
		Seq   int64  `json:"seq"`
		Ts    int64  `json:"ts"`
		Event string `json:"event"`
	}
	if t.rn.getInto("/api/v1/events/recent?"+query.Encode(), &events) == nil {
		for _, e := range events {
			if e.Seq > after && e.Seq <= until {
				for i := range result.Steps {
					step := &result.Steps[i]
					if step.eventRange.contains(e.Seq) && len(step.Events) < maxEventsPerStep {
						step.Events = append(step.Events, EventRef{Seq: e.Seq, Ts: e.Ts, Event: e.Event})
					}
				}
			}
		}
	}
}
