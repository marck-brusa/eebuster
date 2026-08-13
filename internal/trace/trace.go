// Package trace keeps a bounded in-memory record of every raw SHIP frame the embedded stacks
// send or receive, annotated with conformance findings. The capture point is ship-go's
// websocket layer — before the vendored stack's JSON "repairs" — so entries show what was
// actually on the wire, which is the whole point: everything downstream has already been
// rewritten. See internal/conformance for the rules.
package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/marck-brusa/eebuster/internal/conformance"
)

// Entry is one captured frame. Raw is stripped from list responses (it can be tens of
// kilobytes per frame) and only returned by the single-entry lookup.
type Entry struct {
	Seq        int64                 `json:"seq"`
	Ts         float64               `json:"ts"`
	Stack      string                `json:"stack"`
	SKI        string                `json:"ski"`
	Dir        string                `json:"dir"`  // "send" | "recv", as seen from the testbench
	Kind       string                `json:"kind"` // "spine" | "ship"
	Function   string                `json:"function,omitempty"`
	Classifier string                `json:"classifier,omitempty"`
	MsgCounter *float64              `json:"msg_counter,omitempty"`
	CounterRef *float64              `json:"msg_counter_reference,omitempty"`
	Source     string                `json:"source,omitempty"`
	Dest       string                `json:"dest,omitempty"`
	Size       int                   `json:"size"`
	Findings   []conformance.Finding `json:"findings"`
	Raw        string                `json:"raw,omitempty"`
}

// RuleCount aggregates one rule across the retained window for the summary endpoint.
type RuleCount struct {
	Rule     string               `json:"rule"`
	Severity conformance.Severity `json:"severity"`
	Count    int                  `json:"count"`
	SpecRef  string               `json:"spec_ref,omitempty"`
	// LastSeq points at a concrete offending frame so the UI can jump straight to evidence.
	LastSeq int64  `json:"last_seq"`
	LastSKI string `json:"last_ski,omitempty"`
}

type Summary struct {
	Frames        int `json:"frames"`
	NonConformant int `json:"non_conformant"`
	// Errors and Warnings count frames by their worst finding, so an automated gate can
	// require errors == 0 while tolerating advisory warnings.
	Errors   int         `json:"errors"`
	Warnings int         `json:"warnings"`
	Rules    []RuleCount `json:"rules"`
	// PerSKI counts non-conformant frames per peer, so a bench with several devices shows
	// which one is at fault.
	PerSKI map[string]int `json:"per_ski"`
}

const defaultCapacity = 2000

// Store is safe for concurrent use. One conformance.Session is kept per SKI so cross-message
// checks (declared units, counter monotonicity) survive across frames.
type Store struct {
	mu       sync.Mutex
	ring     []Entry
	capacity int
	seq      int64
	sessions map[string]*conformance.Session
	// logWriter, when set, additionally appends every frame as one line in the "EEBus Hub"
	// log format: `2026-03-16 05:19:57    [Send] <40-hex-ski><payload>`. That exact shape is
	// what EEBusTracer's log tailing auto-detects, so pointing its --log-file at this file
	// gives the deep-dive tracer a live feed with no protocol glue at all.
	logWriter io.Writer
}

func New() *Store {
	return &Store{capacity: defaultCapacity, sessions: map[string]*conformance.Session{}}
}

// SetLogWriter attaches the frame log sink (see logWriter). Pass nil to detach. Writes are
// serialized; write errors are ignored on purpose -- a full disk must not take down capture.
func (s *Store) SetLogWriter(w io.Writer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logWriter = w
}

// Add records one frame and returns the stored entry (including findings), so the caller can
// publish notification events for non-conformant traffic.
func (s *Store) Add(stack, dir, ski, payload string) Entry {
	s.mu.Lock()
	session, ok := s.sessions[ski]
	if !ok {
		session = conformance.NewSession()
		s.sessions[ski] = session
	}
	s.mu.Unlock()

	// Checked outside the store lock: Session has its own, and JSON parsing is the
	// expensive part of the path.
	findings := session.Check(dir, []byte(payload))
	if findings == nil {
		findings = []conformance.Finding{}
	}
	entry := Entry{
		Ts: float64(time.Now().UnixMilli()) / 1000.0, Stack: stack, SKI: ski, Dir: dir,
		Size: len(payload), Findings: findings, Raw: payload,
	}
	summarize(&entry, payload)

	s.mu.Lock()
	s.seq++
	entry.Seq = s.seq
	s.ring = append(s.ring, entry)
	if len(s.ring) > s.capacity {
		s.ring = s.ring[len(s.ring)-s.capacity:]
	}
	if s.logWriter != nil {
		direction := "Recv"
		if dir == "send" {
			direction = "Send"
		}
		_, _ = fmt.Fprintf(s.logWriter, "%s    [%s] %s%s\n",
			time.Now().Format("2006-01-02 15:04:05"), direction, ski, payload)
	}
	s.mu.Unlock()
	return entry
}

// Recent returns entries after the given sequence number, oldest first, with Raw stripped.
// latest is the newest sequence in the store regardless of filtering, so pollers can advance
// their cursor even when every new frame is filtered out.
func (s *Store) Recent(after int64, limit int, ski, dir string, findingsOnly bool) (entries []Entry, latest int64) {
	if limit <= 0 || limit > defaultCapacity {
		limit = defaultCapacity
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	latest = s.seq
	out := make([]Entry, 0, 64)
	for _, e := range s.ring {
		if e.Seq <= after {
			continue
		}
		if ski != "" && e.SKI != ski {
			continue
		}
		if dir != "" && e.Dir != dir {
			continue
		}
		if findingsOnly && len(e.Findings) == 0 {
			continue
		}
		e.Raw = ""
		out = append(out, e)
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, latest
}

func (s *Store) Get(seq int64) (Entry, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range s.ring {
		if e.Seq == seq {
			return e, true
		}
	}
	return Entry{}, false
}

// Clear drops the retained frames and the per-peer sessions; the next frame from each peer
// starts a fresh correlation state.
func (s *Store) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ring = nil
	s.sessions = map[string]*conformance.Session{}
}

func (s *Store) Summary() Summary {
	s.mu.Lock()
	defer s.mu.Unlock()
	summary := Summary{PerSKI: map[string]int{}, Rules: []RuleCount{}}
	byRule := map[string]*RuleCount{}
	for _, e := range s.ring {
		summary.Frames++
		if len(e.Findings) == 0 {
			continue
		}
		summary.NonConformant++
		summary.PerSKI[e.SKI]++
		worstIsError := false
		for _, f := range e.Findings {
			if f.Severity == conformance.SeverityError {
				worstIsError = true
			}
		}
		if worstIsError {
			summary.Errors++
		} else {
			summary.Warnings++
		}
		for _, f := range e.Findings {
			rc, ok := byRule[f.Rule]
			if !ok {
				rc = &RuleCount{Rule: f.Rule, Severity: f.Severity, SpecRef: f.SpecRef}
				byRule[f.Rule] = rc
			}
			rc.Count++
			rc.LastSeq = e.Seq
			rc.LastSKI = e.SKI
		}
	}
	for _, rc := range byRule {
		summary.Rules = append(summary.Rules, *rc)
	}
	// Deterministic order: errors first, then by count descending, then rule name.
	for i := 0; i < len(summary.Rules); i++ {
		for j := i + 1; j < len(summary.Rules); j++ {
			a, b := summary.Rules[i], summary.Rules[j]
			aErr, bErr := a.Severity == conformance.SeverityError, b.Severity == conformance.SeverityError
			if bErr && !aErr || (aErr == bErr && (b.Count > a.Count || (b.Count == a.Count && b.Rule < a.Rule))) {
				summary.Rules[i], summary.Rules[j] = b, a
			}
		}
	}
	return summary
}

// summarize extracts the row-level fields from the wire JSON. Best effort: a frame that
// cannot be parsed still gets stored (with its findings explaining why).
func summarize(entry *Entry, payload string) {
	entry.Kind = "ship"
	trimmed := []byte(payload)
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		entry.Function = payload
		return
	}
	var root any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return
	}
	norm := conformance.Normalize(root)
	rootObj, ok := norm.(map[string]any)
	if !ok {
		return
	}

	datagram, isMap := digMap(rootObj, "data", "payload", "datagram")
	if !isMap {
		// SHIP control message: name the row after its single root element.
		for name := range rootObj {
			entry.Function = name
		}
		return
	}
	entry.Kind = "spine"
	if header, ok := digMap(datagram, "header"); ok {
		entry.Classifier, _ = header["cmdClassifier"].(string)
		if counter, ok := header["msgCounter"].(float64); ok {
			entry.MsgCounter = &counter
		}
		if ref, ok := header["msgCounterReference"].(float64); ok {
			entry.CounterRef = &ref
		}
		if src, ok := digMap(header, "addressSource"); ok {
			entry.Source, _ = src["device"].(string)
		}
		if dst, ok := digMap(header, "addressDestination"); ok {
			entry.Dest, _ = dst["device"].(string)
		}
	}
	entry.Function = cmdFunction(datagram)
}

// cmdFunction names the SPINE function carried by the (single) cmd instance: the child
// element that is not one of the generic cmd controls.
func cmdFunction(datagram map[string]any) string {
	payload, ok := digMap(datagram, "payload")
	if !ok {
		return ""
	}
	instances := payload["cmd"]
	var first map[string]any
	switch x := instances.(type) {
	case map[string]any:
		first = x
	case []any:
		if len(x) > 0 {
			first, _ = x[0].(map[string]any)
		}
	}
	for name := range first {
		if name != "function" && name != "filter" && name != "cmdControl" {
			return name
		}
	}
	return ""
}

func digMap(v map[string]any, path ...string) (map[string]any, bool) {
	current := v
	for _, name := range path {
		next, ok := current[name].(map[string]any)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}
