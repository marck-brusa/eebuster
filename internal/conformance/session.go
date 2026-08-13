package conformance

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Session adds the checks that need memory of earlier frames from the same peer. Two things
// only become visible across messages:
//
//   - value plausibility: a measurement value is only interpretable against the unit the
//     device itself declared in measurementDescriptionListData. Correlating the two catches
//     the classic scale fault where a milli-unit is encoded with scale 0, so a 16 A phase
//     current leaves the device as 16000 A.
//   - msgCounter uniqueness per direction. Strict monotonicity would be wrong: senders
//     allocate the counter when they build a message, and heavy replies (detailed discovery)
//     legitimately leave later than messages numbered after them. Reuse of a counter is the
//     real fault.
//
// A Session is per peer (SKI). It is safe for concurrent use.
type Session struct {
	mu       sync.Mutex
	units    map[float64]string        // measurementId -> unit, as declared by the peer
	counters map[string]*counterWindow // direction -> recently seen msgCounters
}

func NewSession() *Session {
	return &Session{units: map[float64]string{}, counters: map[string]*counterWindow{}}
}

// counterWindow remembers the last counterWindowSize msgCounters per direction, enough to
// catch immediate reuse without growing forever. A counter far below the maximum restarts
// the window: the sender rebooted and will legitimately replay small numbers.
type counterWindow struct {
	seen  map[float64]struct{}
	order []float64
	max   float64
}

const counterWindowSize = 256

func (w *counterWindow) observe(counter float64) (duplicate bool) {
	if counter < w.max/4 && counter <= 64 {
		w.seen = nil
		w.order = nil
		w.max = 0
	}
	if w.seen == nil {
		w.seen = make(map[float64]struct{}, counterWindowSize)
	}
	if _, dup := w.seen[counter]; dup {
		return true
	}
	w.seen[counter] = struct{}{}
	w.order = append(w.order, counter)
	if len(w.order) > counterWindowSize {
		delete(w.seen, w.order[0])
		w.order = w.order[1:]
	}
	if counter > w.max {
		w.max = counter
	}
	return false
}

// Plausibility bounds per declared unit. Deliberately generous: the point is to catch
// thousand-fold scale faults, not to argue about a 30 kW wallbox.
var unitBounds = map[string]float64{
	"A":  1000,    // no residential conductor carries a kiloampere
	"V":  1000,    // LV network
	"W":  1000000, // 1 MW
	"Hz": 1000,
}

// Check runs the stateless frame checks plus the session-stateful ones. dir is "send" or
// "recv" as seen from the testbench.
func (s *Session) Check(dir string, payload []byte) []Finding {
	findings := CheckFrame(payload)

	norm := normalizeFrame(payload)
	if norm == nil {
		return findings
	}
	datagram := dig(norm, "data", "payload", "datagram")
	if datagram == nil {
		return findings
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if header, ok := dig(datagram, "header").(map[string]any); ok {
		if counter, ok := header["msgCounter"].(float64); ok {
			window, exists := s.counters[dir]
			if !exists {
				window = &counterWindow{}
				s.counters[dir] = window
			}
			if window.observe(counter) {
				findings = append(findings, Finding{
					Rule: "msg-counter-duplicate", Severity: SeverityWarning,
					Path: "$.datagram.header.msgCounter",
					Message: fmt.Sprintf("msgCounter %.0f was already used by an earlier message from the same direction; counters identify messages and must not repeat",
						counter),
					SpecRef: refCounter,
				})
			}
		}
	}

	cmd := dig(datagram, "payload", "cmd")
	for _, one := range elementList(cmd) {
		s.learnUnits(one)
		findings = append(findings, s.checkMeasurements(one)...)
	}
	return findings
}

// learnUnits remembers the unit each measurementId was declared with. The declaration and the
// values usually arrive in separate messages (description read vs. data notifications).
func (s *Session) learnUnits(cmd map[string]any) {
	descriptions := dig(cmd, "measurementDescriptionListData", "measurementDescriptionData")
	for _, entry := range elementList(descriptions) {
		id, okID := entry["measurementId"].(float64)
		unit, okUnit := entry["unit"].(string)
		if okID && okUnit {
			s.units[id] = unit
		}
	}
}

func (s *Session) checkMeasurements(cmd map[string]any) []Finding {
	var findings []Finding
	data := dig(cmd, "measurementListData", "measurementData")
	for _, entry := range elementList(data) {
		id, okID := entry["measurementId"].(float64)
		value, okValue := scaledNumber(entry["value"])
		if !okID || !okValue {
			continue
		}
		unit, declared := s.units[id]
		if !declared {
			continue
		}
		bound, bounded := unitBounds[unit]
		if !bounded {
			continue
		}
		if value > bound || value < -bound {
			findings = append(findings, Finding{
				Rule: "value-magnitude", Severity: SeverityWarning,
				Path: fmt.Sprintf("$.datagram.payload.cmd.measurementListData.measurementData[measurementId=%.0f]", id),
				Message: fmt.Sprintf("measurement %.0f reports %g %s, which is implausible for that unit — a value this large usually means a milli-unit was encoded with scale 0",
					id, value, unit),
				SpecRef: refScaledNum,
			})
		}
	}
	return findings
}

// normalizeFrame parses and normalizes one frame; nil when it is not a JSON message.
func normalizeFrame(payload []byte) any {
	trimmed := payload
	for len(trimmed) > 0 && trimmed[len(trimmed)-1] == 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil
	}
	var root any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return nil
	}
	return Normalize(root)
}
