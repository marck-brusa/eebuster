package scenario

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
)

// The limit_descriptions verb judges the load-control limits an EV declares for OPEV or
// OSCEV against the use case's content tables: `limit_descriptions: {use_case: OPEV}`.
// It reads the testbench's OPEV/OSCEV view of the entity, which lists every limit
// description of the use case's category with the phase and permitted range its
// measurement id links it to.

type limitRuleSet struct {
	path, document, category, scope, descriptionTable, phasesTable, permittedTable, asymmetricReq string
}

var limitRules = map[string]limitRuleSet{
	"OPEV": {
		path: "opev", document: "EEBus UC TS OPEV V1.0.1b", category: "obligation", scope: "overloadProtection",
		descriptionTable: "Table 6", phasesTable: "Table 8", permittedTable: "Table 9", asymmetricReq: "OPEV-002",
	},
	"OSCEV": {
		path: "oscev", document: "EEBus UC TS OSCEV V1.0.1b", category: "recommendation", scope: "selfConsumption",
		descriptionTable: "Table 6", phasesTable: "Table 8", permittedTable: "Table 9", asymmetricReq: "OSCEV-002",
	},
}

// limitRead is the part of the OPEV/OSCEV read the judgement needs.
type limitRead struct {
	Phases       []string           `json:"phases"`
	Descriptions []limitDescription `json:"descriptions"`
}

type limitDescription struct {
	LimitID         uint     `json:"limit_id"`
	LimitType       string   `json:"limit_type"`
	LimitCategory   string   `json:"limit_category"`
	LimitDirection  string   `json:"limit_direction"`
	Unit            string   `json:"unit"`
	ScopeType       string   `json:"scope_type"`
	MeasurementID   *uint    `json:"measurement_id"`
	Phase           string   `json:"phase"`
	PermittedValues string   `json:"permitted_values"`
	PermittedMinA   *float64 `json:"permitted_min_a"`
	PermittedMaxA   *float64 `json:"permitted_max_a"`
}

func (rn *Runner) stepLimitDescriptions(args any, scenarioContext map[string]any) StepResult {
	key, _ := argsMap(args)["use_case"].(string)
	key = strings.ToUpper(key)
	ski, _ := lookup("peer.ski", scenarioContext)
	step := StepResult{Name: "limit_descriptions " + key}
	rules, known := limitRules[key]
	if !known {
		step.Status, step.Detail = StatusFailed, "limit_descriptions judges OPEV and OSCEV, not "+key
	} else {
		evidence, raw, err := rn.do(http.MethodGet, fmt.Sprintf("/api/v1/%s/%v", rules.path, ski), nil)
		step.Request = evidence
		var read limitRead
		if err == nil && evidence.Status < 400 {
			err = json.Unmarshal(raw, &read)
		} else if err == nil {
			err = fmt.Errorf("HTTP %d: %s", evidence.Status, strings.TrimSpace(string(raw)))
		}
		if err != nil {
			step.Status, step.Detail = StatusFailed, err.Error()
		} else {
			step.Status, step.Detail, step.Assertions = judgeLimitDescriptions(rules, read, rn.asymmetricCharging(fmt.Sprint(ski)))
		}
	}
	return step
}

// asymmetricCharging is the EV's own answer to EVCC scenario 3, when a vehicle is known.
func (rn *Runner) asymmetricCharging(ski string) *bool {
	var out *bool
	var snapshot struct {
		EV struct {
			Vehicles []struct {
				Asymmetric *bool `json:"asymmetric_charging"`
			} `json:"vehicles"`
		} `json:"ev"`
	}
	evidence, raw, err := rn.do(http.MethodGet, "/api/v1/energy/"+ski+"/snapshot", nil)
	if err == nil && evidence.Status < 400 && json.Unmarshal(raw, &snapshot) == nil && len(snapshot.EV.Vehicles) > 0 {
		out = snapshot.EV.Vehicles[0].Asymmetric
	}
	return out
}

// judgeLimitDescriptions applies the description table (limit type, category, unit, scope,
// link to a measurement), the phases table (a, b and c, each with its own limit; a combined
// "abc" limit may exist in addition) and the permitted-value-set table (an entry carries a
// range or values) of the use case, plus its asymmetric-curtailment requirement when the EV
// says it supports asymmetric charging.
func judgeLimitDescriptions(rules limitRuleSet, read limitRead, asymmetric *bool) (string, string, []Comparison) {
	var comparisons []Comparison
	perPhase := map[string]bool{}
	combined := false
	var problems []string
	for _, d := range read.Descriptions {
		var wrong []string
		if d.LimitType != "maxValueLimit" {
			wrong = append(wrong, fmt.Sprintf("limitType %q, want maxValueLimit", d.LimitType))
		}
		if d.Unit != "A" {
			wrong = append(wrong, fmt.Sprintf("unit %q, want A", d.Unit))
		}
		if d.ScopeType != rules.scope {
			wrong = append(wrong, fmt.Sprintf("scopeType %q, want %s", d.ScopeType, rules.scope))
		}
		if d.LimitDirection != "" && d.LimitDirection != "consume" {
			wrong = append(wrong, fmt.Sprintf("limitDirection %q, want consume", d.LimitDirection))
		}
		switch {
		case d.MeasurementID == nil:
			wrong = append(wrong, "no measurementId, so the limit is not linked to a phase")
		case d.Phase == "":
			wrong = append(wrong, fmt.Sprintf("measurementId %d matches no electrical parameter with acMeasuredPhases (%s)", *d.MeasurementID, rules.phasesTable))
		case d.Phase == "a" || d.Phase == "b" || d.Phase == "c":
			perPhase[d.Phase] = true
		case d.Phase == "abc":
			combined = true
		default:
			wrong = append(wrong, fmt.Sprintf("acMeasuredPhases %q is not a, b or c (%s)", d.Phase, rules.phasesTable))
		}
		label := fmt.Sprintf("limit %d (phase %s)", d.LimitID, orMissing(d.Phase))
		comparisons = append(comparisons, Comparison{
			Key: label, Op: rules.descriptionTable, Expected: fmt.Sprintf("maxValueLimit, %s, A, %s, linked to a phase", rules.category, rules.scope),
			Actual: okOr(wrong), OK: len(wrong) == 0,
		})
		problems = append(problems, prefixed(label, wrong)...)
		if c, bad := judgePermitted(rules, d, label); c.Key != "" {
			comparisons = append(comparisons, c)
			problems = append(problems, bad...)
		}
	}

	phases := sortedPhases(perPhase)
	phaseText := strings.Join(phases, ", ")
	if combined {
		phaseText = strings.TrimPrefix(phaseText+", abc (combined, in addition)", ", ")
	}
	switch {
	case len(read.Descriptions) == 0:
		problems = append(problems, fmt.Sprintf("no limit description of category %s is published (%s %s)", rules.category, rules.document, rules.descriptionTable))
	case len(perPhase) == 0 && combined:
		problems = append(problems, fmt.Sprintf("limits only on the combined phase abc; %s names the phases a, b and c, each with its own limit [%s]", rules.phasesTable, rules.asymmetricReq))
	case len(perPhase) == 0:
		problems = append(problems, "no limit is linked to a phase a, b or c")
	}
	comparisons = append(comparisons, Comparison{
		Key: "phases with their own limit", Op: rules.phasesTable, Expected: "a, b, c", Actual: orMissing(phaseText), OK: len(perPhase) > 0,
	})
	if asymmetric != nil && *asymmetric {
		ok := len(perPhase) == 3
		comparisons = append(comparisons, Comparison{
			Key: "asymmetric charging supported (EVCC)", Op: rules.asymmetricReq, Expected: "a limit on each of a, b and c", Actual: orMissing(strings.Join(phases, ", ")), OK: ok,
		})
		if !ok {
			problems = append(problems, fmt.Sprintf("the EV supports asymmetric charging but declares limits for %s only; asymmetric curtailment needs one per phase [%s]", orMissing(strings.Join(phases, ", ")), rules.asymmetricReq))
		}
	}

	status := StatusPassed
	detail := fmt.Sprintf("%d limit description(s) of category %s; phases %s", len(read.Descriptions), rules.category, orMissing(phaseText))
	if asymmetric == nil {
		detail += "; asymmetric charging support unknown (no vehicle record)"
	}
	if len(problems) > 0 {
		status = StatusFailed
		detail += "; " + strings.Join(problems, "; ")
	}
	return status, detail, comparisons
}

// judgePermitted checks the permitted value set behind one limit. The set is optional, but
// a published entry has to carry a range or values, and a range has to be usable.
func judgePermitted(rules limitRuleSet, d limitDescription, label string) (Comparison, []string) {
	var c Comparison
	var wrong []string
	switch d.PermittedValues {
	case "empty":
		wrong = append(wrong, "a permitted-value-set entry without any set")
		c = Comparison{Key: label + " permitted values", Op: rules.permittedTable, Expected: "at least one range or value set", Actual: "entry without any set", OK: false}
	case "range":
		min, max := 0.0, 0.0
		if d.PermittedMinA != nil {
			min = *d.PermittedMinA
		}
		if d.PermittedMaxA != nil {
			max = *d.PermittedMaxA
		}
		if max <= 0 || min > max {
			wrong = append(wrong, fmt.Sprintf("permitted range %g–%g A is not usable", min, max))
		}
		c = Comparison{Key: label + " permitted values", Op: rules.permittedTable, Expected: "0 ≤ min ≤ max, max > 0 A", Actual: fmt.Sprintf("%g–%g A", min, max), OK: len(wrong) == 0}
	case "values":
		c = Comparison{Key: label + " permitted values", Op: rules.permittedTable, Expected: "at least one range or value set", Actual: "value set", OK: true}
	}
	return c, prefixed(label, wrong)
}

func sortedPhases(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for p := range set {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func orMissing(s string) string {
	if s == "" {
		s = "none"
	}
	return s
}

func okOr(wrong []string) string {
	text := "ok"
	if len(wrong) > 0 {
		text = strings.Join(wrong, "; ")
	}
	return text
}

func prefixed(label string, items []string) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, label+": "+item)
	}
	return out
}
