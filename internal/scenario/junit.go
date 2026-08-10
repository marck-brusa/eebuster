package scenario

import (
	"fmt"
	"strings"
)

// ToJUnitXML matches cli/scenario.py's ScenarioResult.to_junit_xml, for the --junit flag's
// existing CI consumers.
func (r ScenarioResult) ToJUnitXML() string {
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n%s\n", testsuiteBlock(r.Name, r.Steps, r.DurationS))
}

// ToJUnitXML matches cli/scenario.py's SuiteResult.to_junit_xml.
func (r SuiteResult) ToJUnitXML() string {
	var blocks []string
	for _, res := range r.Results {
		blocks = append(blocks, testsuiteBlock(res.Name, res.Steps, res.DurationS))
	}
	return fmt.Sprintf("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<testsuites>\n%s\n</testsuites>\n", strings.Join(blocks, "\n"))
}

func testsuiteBlock(name string, steps []StepResult, durationS float64) string {
	failures, skipped := 0, 0
	var cases []string
	for _, s := range steps {
		if s.Status == "failed" {
			failures++
		}
		if s.Status == "skipped" {
			skipped++
		}
		body := ""
		switch s.Status {
		case "failed":
			body = fmt.Sprintf(`<failure message="%s"/>`, xmlEscape(s.Detail))
		case "skipped":
			body = fmt.Sprintf(`<skipped message="%s"/>`, xmlEscape(s.Detail))
		}
		cases = append(cases, fmt.Sprintf(`  <testcase name="%s" time="%.2f">%s</testcase>`, xmlEscape(s.Name), s.DurationS, body))
	}
	return fmt.Sprintf(
		"<testsuite name=\"%s\" tests=\"%d\" failures=\"%d\" skipped=\"%d\" time=\"%.2f\">\n%s\n</testsuite>",
		xmlEscape(name), len(steps), failures, skipped, durationS, strings.Join(cases, "\n"),
	)
}

// xmlEscape matches cli/scenario.py's _xml_escape exactly (only &, <, >, " -- not full XML
// entity escaping, deliberately kept simple).
func xmlEscape(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}
