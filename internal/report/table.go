package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"strconv"
	"strings"

	"github.com/marck-brusa/eebuster/internal/scenario"
)

// The tabular formats (CSV and the workbook) share these tables.

// table is a header row and data rows of cells; a cell is a string or a float64.
type table struct {
	Name   string
	Header []string
	Rows   [][]any
	Widths []float64
}

func caseTable(r *Run) table {
	t := table{Name: "Test cases", Header: []string{
		"run_id", "order", "id", "name", "use_case", "scenario", "test_case", "requirements", "verification", "risk",
		"status", "reason", "duration_s", "started_at", "detail", "covers",
	}, Widths: []float64{22, 7, 30, 30, 9, 9, 28, 24, 13, 12, 9, 22, 11, 22, 60, 60}}
	for i, tc := range r.TestCases {
		scenarioNumber, testCase, requirements, verification := "", "", "", ""
		if tc.Spec != nil {
			if tc.Spec.Scenario > 0 {
				scenarioNumber = strconv.FormatUint(uint64(tc.Spec.Scenario), 10)
			}
			testCase, requirements, verification = tc.Spec.TestCase, strings.Join(tc.Spec.Requirements, " "), tc.Spec.Verification
		}
		t.Rows = append(t.Rows, []any{
			r.ID, float64(i + 1), tc.ID, tc.Name, GroupOf(tc), scenarioNumber, testCase, requirements, verification, tc.Risk,
			tc.Status, tc.Reason, tc.DurationS, formatUnix(tc.StartedAt), FirstFailure(tc), tc.Covers,
		})
	}
	return t
}

func stepTable(r *Run) table {
	t := table{Name: "Steps", Header: []string{
		"run_id", "test_case", "index", "step", "status", "duration_s", "detail", "method", "path", "http_status",
		"assertions", "trace_from", "trace_to", "findings",
	}, Widths: []float64{22, 30, 7, 40, 9, 11, 50, 8, 40, 11, 70, 11, 11, 9}}
	for _, tc := range r.TestCases {
		for i, s := range tc.Steps {
			method, path, status := "", "", ""
			if s.Request != nil {
				method, path = s.Request.Method, s.Request.Path
				if s.Request.Status > 0 {
					status = strconv.Itoa(s.Request.Status)
				}
			}
			from, to := "", ""
			if s.Trace != nil {
				from, to = strconv.FormatInt(s.Trace.From, 10), strconv.FormatInt(s.Trace.To, 10)
			}
			t.Rows = append(t.Rows, []any{
				r.ID, tc.ID, float64(i + 1), s.Name, s.Status, s.DurationS, s.Detail, method, path, status,
				assertionText(s.Assertions), from, to, float64(s.FindingsTotal),
			})
		}
	}
	return t
}

func assertionText(comparisons []scenario.Comparison) string {
	parts := make([]string, 0, len(comparisons))
	for _, c := range comparisons {
		verdict := "ok"
		if !c.OK {
			verdict = "FAILED"
		}
		expected := ""
		if c.Expected != nil {
			expected = " " + formatValue(c.Expected)
		}
		parts = append(parts, fmt.Sprintf("%s %s%s: actual %s (%s)", c.Key, c.Op, expected, formatValue(c.Actual), verdict))
	}
	return strings.Join(parts, "; ")
}

func summaryTable(r *Run) table {
	t := table{Name: "Summary", Header: []string{"field", "value"}, Widths: []float64{32, 90}}
	add := func(k string, v any) {
		if s, ok := v.(string); !ok || s != "" {
			t.Rows = append(t.Rows, []any{k, v})
		}
	}
	add("Headline", r.Summary.Headline)
	add("Run id", r.ID)
	add("Status", r.Status)
	add("Started", formatTime(r.StartedAt))
	if r.FinishedAt != nil {
		add("Finished", formatTime(*r.FinishedAt))
	}
	add("Duration (s)", r.DurationS)
	add("Test cases", float64(r.Summary.Total))
	add("Passed", float64(r.Summary.Passed))
	add("Failed", float64(r.Summary.Failed))
	add("Skipped", float64(r.Summary.Skipped))
	for _, reason := range sortedKeys(r.Summary.SkippedByReason) {
		add("Skipped: "+ReasonText(reason), float64(r.Summary.SkippedByReason[reason]))
	}
	add("Tester", r.Tester)
	add("Notes", r.Notes)
	add("Selection", r.Selection.Describe())
	add("Testbench version", r.Testbench.Version)
	add("Scenario library sha256", r.Library.SHA256)
	for _, c := range r.Checks {
		add("Check: "+c.Title, c.Status+" -- "+c.Detail)
	}
	for _, u := range r.UseCases {
		add("Use case "+u.Acronym, fmt.Sprintf("advertised %s; %d tests, %d passed, %d failed, %d skipped", yesNo(u.Advertised), u.Total, u.Passed, u.Failed, u.Skipped))
	}
	return t
}

func deviceTable(r *Run) table {
	t := table{Name: "Device", Header: []string{"section", "field", "value"}, Widths: []float64{26, 34, 80}}
	for _, c := range append(append([]card{}, buildCardsForTable(r)...), vehicleCards(r)...) {
		for _, entry := range c.Rows {
			t.Rows = append(t.Rows, []any{c.Title, entry.Key, entry.Value})
		}
	}
	for _, row := range conditionRows(r.ConditionsStart, r.ConditionsEnd) {
		t.Rows = append(t.Rows, []any{"Conditions", row.Field, "start " + row.Start + ", end " + row.End})
	}
	return t
}

func buildCardsForTable(r *Run) []card {
	return []card{deviceCard(r), runCard(r), testbenchCard(r)}
}

func conformanceTable(r *Run) table {
	t := table{Name: "Conformance", Header: []string{"rule", "severity", "frames", "first_seq", "spec_ref", "example"}, Widths: []float64{28, 10, 9, 11, 60, 60}}
	for _, rule := range r.Conformance.Rules {
		t.Rows = append(t.Rows, []any{rule.Rule, rule.Severity, float64(rule.Count), float64(rule.FirstSeq), rule.SpecRef, rule.Example})
	}
	return t
}

// CSV renders the test cases ("cases") or the steps ("steps") as CSV with a header row.
func CSV(r *Run, which string) ([]byte, error) {
	t := caseTable(r)
	if which == "steps" {
		t = stepTable(r)
	}
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	err := w.Write(t.Header)
	for _, row := range t.Rows {
		if err == nil {
			err = w.Write(cellStrings(row))
		}
	}
	w.Flush()
	if err == nil {
		err = w.Error()
	}
	return buf.Bytes(), err
}

func cellStrings(row []any) []string {
	out := make([]string, len(row))
	for i, cell := range row {
		switch v := cell.(type) {
		case float64:
			out[i] = strconv.FormatFloat(v, 'f', -1, 64)
		default:
			out[i] = neutralizeFormula(fmt.Sprint(v))
		}
	}
	return out
}

// neutralizeFormula keeps a spreadsheet from evaluating a device-provided string as a
// formula: text that starts with =, +, -, @ or a control character is prefixed with an
// apostrophe. Text that is a number is left alone.
func neutralizeFormula(s string) string {
	if len(s) > 0 && strings.ContainsRune("=+-@\t\r", rune(s[0])) {
		if _, err := strconv.ParseFloat(s, 64); err != nil {
			s = "'" + s
		}
	}
	return s
}
