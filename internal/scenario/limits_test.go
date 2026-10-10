package scenario

import (
	"strings"
	"testing"
)

func limitFixture(phase string, permitted string, min, max float64) limitDescription {
	id := uint(1)
	return limitDescription{
		LimitID: 1, LimitType: "maxValueLimit", LimitCategory: "obligation", Unit: "A", ScopeType: "overloadProtection",
		MeasurementID: &id, Phase: phase, PermittedValues: permitted, PermittedMinA: &min, PermittedMaxA: &max,
	}
}

func TestJudgeLimitDescriptionsPassesAConformantEV(t *testing.T) {
	yes := true
	read := limitRead{Descriptions: []limitDescription{
		limitFixture("a", "range", 6, 16), limitFixture("b", "range", 6, 16), limitFixture("c", "range", 6, 16),
	}}
	status, detail, comparisons := judgeLimitDescriptions(limitRules["OPEV"], read, &yes)
	if status != StatusPassed {
		t.Fatalf("status %s: %s", status, detail)
	}
	for _, c := range comparisons {
		if !c.OK {
			t.Errorf("comparison not ok: %+v", c)
		}
	}
	if len(comparisons) != 8 { // 3 descriptions, 3 permitted ranges, phases, asymmetric
		t.Errorf("%d comparisons: %+v", len(comparisons), comparisons)
	}
}

// Only a combined "abc" limit, with the EV claiming asymmetric support: the device fault of
// the field ticket this check exists for.
func TestJudgeLimitDescriptionsFailsCombinedPhaseOnly(t *testing.T) {
	yes := true
	read := limitRead{Descriptions: []limitDescription{limitFixture("abc", "range", 6, 16)}}
	status, detail, _ := judgeLimitDescriptions(limitRules["OPEV"], read, &yes)
	if status != StatusFailed || !strings.Contains(detail, "only on the combined phase abc") || !strings.Contains(detail, "asymmetric") {
		t.Errorf("status %s: %s", status, detail)
	}
}

// A combined limit next to the per-phase ones is allowed.
func TestJudgeLimitDescriptionsAllowsCombinedInAddition(t *testing.T) {
	read := limitRead{Descriptions: []limitDescription{
		limitFixture("a", "range", 6, 16), limitFixture("b", "range", 6, 16), limitFixture("c", "range", 6, 16), limitFixture("abc", "range", 6, 16),
	}}
	if status, detail, _ := judgeLimitDescriptions(limitRules["OPEV"], read, nil); status != StatusPassed || !strings.Contains(detail, "abc (combined, in addition)") || !strings.Contains(detail, "unknown") {
		t.Errorf("status %s: %s", status, detail)
	}
}

// A permitted-value-set entry without any set, and descriptions with the wrong content.
func TestJudgeLimitDescriptionsFailsOnContent(t *testing.T) {
	empty := limitFixture("a", "empty", 0, 0)
	wrong := limitFixture("b", "range", 6, 16)
	wrong.LimitID, wrong.Unit, wrong.ScopeType = 2, "mA", "acCurrent"
	unlinked := limitFixture("c", "none", 0, 0)
	unlinked.LimitID, unlinked.MeasurementID = 3, nil
	read := limitRead{Descriptions: []limitDescription{empty, wrong, unlinked}}
	status, detail, comparisons := judgeLimitDescriptions(limitRules["OPEV"], read, nil)
	if status != StatusFailed {
		t.Fatalf("status %s: %s", status, detail)
	}
	for _, want := range []string{"entry without any set", `unit "mA", want A`, `scopeType "acCurrent"`, "no measurementId"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail lacks %q: %s", want, detail)
		}
	}
	bad := 0
	for _, c := range comparisons {
		if !c.OK {
			bad++
		}
	}
	if bad != 3 {
		t.Errorf("%d failed comparisons, want 3: %+v", bad, comparisons)
	}
}

func TestJudgeLimitDescriptionsFailsWithoutDescriptions(t *testing.T) {
	if status, detail, _ := judgeLimitDescriptions(limitRules["OSCEV"], limitRead{}, nil); status != StatusFailed || !strings.Contains(detail, "category recommendation") {
		t.Errorf("status %s: %s", status, detail)
	}
}
