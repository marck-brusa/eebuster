package report

import (
	"fmt"
	"strings"

	"github.com/marck-brusa/eebuster/internal/scenario"
)

// JUnit renders the run in the shape the scenario runner always wrote: one testsuite per
// test case, one testcase per step. Each testsuite carries the run's identification as
// properties, and each testcase's classname is <use case>.<test case id>, so CI groups the
// rows by use case.
func JUnit(r *Run) []byte {
	var b strings.Builder
	b.WriteString("<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n")
	fmt.Fprintf(&b, "<testsuites name=\"%s\" tests=\"%d\" failures=\"%d\" skipped=\"%d\" time=\"%.2f\">\n",
		xmlEscape("eebus-testbench run "+r.ID), r.Summary.Total, r.Summary.Failed, r.Summary.Skipped, r.DurationS)
	properties := junitProperties(r)
	for _, tc := range r.TestCases {
		writeSuite(&b, tc, properties)
	}
	b.WriteString("</testsuites>\n")
	return []byte(b.String())
}

func junitProperties(r *Run) string {
	pairs := [][2]string{
		{"run_id", r.ID}, {"run_status", r.Status}, {"started_at", formatTime(r.StartedAt)},
		{"testbench_version", r.Testbench.Version}, {"device_ski", r.Device.SKI}, {"device_label", deviceTitle(r)},
		{"device_serial", r.Device.Serial}, {"device_software_revision", softwareRevision(r)},
		{"scenario_library_sha256", r.Library.SHA256}, {"tester", r.Tester},
	}
	var b strings.Builder
	b.WriteString("    <properties>\n")
	for _, p := range pairs {
		if p[1] != "" {
			fmt.Fprintf(&b, "      <property name=\"%s\" value=\"%s\"/>\n", p[0], xmlEscape(p[1]))
		}
	}
	b.WriteString("    </properties>\n")
	return b.String()
}

func writeSuite(b *strings.Builder, tc scenario.ScenarioResult, properties string) {
	failures, skipped := 0, 0
	for _, s := range tc.Steps {
		switch s.Status {
		case scenario.StatusFailed:
			failures++
		case scenario.StatusSkipped:
			skipped++
		}
	}
	id := tc.ID
	if id == "" {
		id = tc.Name
	}
	class := GroupOf(tc) + "." + id
	fmt.Fprintf(b, "  <testsuite name=\"%s\" tests=\"%d\" failures=\"%d\" skipped=\"%d\" time=\"%.2f\">\n",
		xmlEscape(id), len(tc.Steps), failures, skipped, tc.DurationS)
	b.WriteString(properties)
	for _, s := range tc.Steps {
		body := ""
		switch s.Status {
		case scenario.StatusFailed:
			body = fmt.Sprintf("<failure message=\"%s\"/>", xmlEscape(s.Detail))
		case scenario.StatusSkipped:
			message := s.Detail
			if tc.Reason != "" {
				message = ReasonText(tc.Reason) + ": " + message
			}
			body = fmt.Sprintf("<skipped message=\"%s\"/>", xmlEscape(message))
		}
		fmt.Fprintf(b, "    <testcase classname=\"%s\" name=\"%s\" time=\"%.2f\">%s</testcase>\n", xmlEscape(class), xmlEscape(s.Name), s.DurationS, body)
	}
	b.WriteString("  </testsuite>\n")
}

// xmlEscape escapes text for an XML attribute and drops characters XML 1.0 cannot carry.
func xmlEscape(s string) string {
	var b strings.Builder
	for _, c := range s {
		switch {
		case c == '&':
			b.WriteString("&amp;")
		case c == '<':
			b.WriteString("&lt;")
		case c == '>':
			b.WriteString("&gt;")
		case c == '"':
			b.WriteString("&quot;")
		case c == '\n':
			b.WriteString("&#10;")
		case c == '\t' || c == '\r' || c >= 0x20 && c != 0xFFFE && c != 0xFFFF:
			b.WriteRune(c)
		}
	}
	return b.String()
}
