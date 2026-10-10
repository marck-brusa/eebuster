package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/scenario"
	"github.com/marck-brusa/eebuster/internal/testrun"
)

func defaultBaseURL() string {
	url := os.Getenv("EEBUS_API_URL")
	if url == "" {
		url = "http://127.0.0.1:8080"
	}
	return url
}

var scenarioValueFlags = map[string]bool{
	"junit": true, "base-url": true, "html": true, "json": true, "csv": true, "csv-steps": true, "xlsx": true, "frames-log": true,
	"peer": true, "tester": true, "notes": true, "use-case": true, "ids": true,
}

// outputs are the report files a run command writes.
type outputs struct {
	junit, html, json, csv, csvSteps, xlsx, framesLog *string
}

func addOutputFlags(fs *flag.FlagSet) outputs {
	return outputs{
		junit:     fs.String("junit", "", "write a JUnit XML report here"),
		html:      fs.String("html", "", "write the self-contained HTML report here"),
		json:      fs.String("json", "", "write the run record (JSON) here"),
		csv:       fs.String("csv", "", "write the test cases as CSV here"),
		csvSteps:  fs.String("csv-steps", "", "write the steps as CSV here"),
		xlsx:      fs.String("xlsx", "", "write an Excel workbook here"),
		framesLog: fs.String("frames-log", "", "write the run's wire frames here in EEBus Hub log format (needs -include-frames)"),
	}
}

func (o outputs) write(run *report.Run) error {
	writers := []struct {
		path   string
		render func() ([]byte, error)
	}{
		{*o.junit, func() ([]byte, error) { return report.JUnit(run), nil }},
		{*o.html, func() ([]byte, error) { return report.HTML(run) }},
		{*o.json, func() ([]byte, error) { return json.MarshalIndent(run, "", "  ") }},
		{*o.csv, func() ([]byte, error) { return report.CSV(run, "cases") }},
		{*o.csvSteps, func() ([]byte, error) { return report.CSV(run, "steps") }},
		{*o.xlsx, func() ([]byte, error) { return report.XLSX(run) }},
		{*o.framesLog, func() ([]byte, error) { return report.FramesLog(run) }},
	}
	var err error
	for _, w := range writers {
		if w.path != "" && err == nil {
			var data []byte
			if data, err = w.render(); err == nil {
				err = os.WriteFile(w.path, data, 0o644)
			}
		}
	}
	return err
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// runLocal executes a run from scenario files on this machine against the instance at
// baseURL. The run record stays in memory; the outputs are written from it.
func runLocal(baseURL, dir string, req testrun.Request) (*report.Run, error) {
	manager := testrun.NewManager(func() string { return baseURL }, dir, "", "", nil)
	return manager.RunAndWait(req)
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(1)
}

func runScenarioCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	out := addOutputFlags(fs)
	baseURL := fs.String("base-url", defaultBaseURL(), "base URL of a running eebus-testbench serve instance")
	peer := fs.String("peer", testrun.DefaultPeer, "configured peer the report describes")
	tester := fs.String("tester", "", "name of the person running the test, for the report")
	notes := fs.String("notes", "", "free text for the report")
	_ = fs.Parse(reorderArgs(args, scenarioValueFlags))
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: eebus-testbench run <scenario.yaml> [-base-url url] [-junit|-html|-json|-csv|-csv-steps|-xlsx|-frames-log path]")
		os.Exit(2)
	}
	path := fs.Arg(0)
	if _, err := scenario.LoadMeta(path); err != nil {
		fail(err)
	}
	id := strings.TrimSuffix(filepath.Base(path), ".yaml")
	run, err := runLocal(*baseURL, filepath.Dir(path), testrun.Request{
		Peer: *peer, Tester: *tester, Notes: *notes, Selection: report.Selection{IDs: []string{id}},
	})
	if err != nil {
		fail(err)
	}
	var result scenario.ScenarioResult
	for _, tc := range run.TestCases {
		if tc.ID == id {
			result = tc
		}
	}
	printJSON(result)
	if err := out.write(run); err != nil {
		fail(err)
	}
	if result.Status == scenario.StatusFailed {
		os.Exit(1)
	}
}

func runAllScenariosCmd(args []string) {
	fs := flag.NewFlagSet("run-all", flag.ExitOnError)
	out := addOutputFlags(fs)
	baseURL := fs.String("base-url", defaultBaseURL(), "base URL of a running eebus-testbench serve instance")
	peer := fs.String("peer", testrun.DefaultPeer, "configured peer to test")
	tester := fs.String("tester", "", "name of the person running the test, for the report")
	notes := fs.String("notes", "", "free text for the report")
	useCases := fs.String("use-case", "", "only these use cases, comma-separated acronyms (LPC,MPC); Common selects the use-case independent checks")
	ids := fs.String("ids", "", "only these test cases, comma-separated ids")
	readOnly := fs.Bool("read-only", false, "only test cases that do not change the device")
	long := fs.Bool("include-long-running", false, "also run test cases that wait on specification timers (minutes each)")
	frames := fs.Bool("include-frames", false, "embed the raw wire frames of the run in the report")
	unadvertised := fs.Bool("all-use-cases", false, "also run the test cases of use cases the device does not advertise")
	_ = fs.Parse(reorderArgs(args, scenarioValueFlags))
	dir := "scenarios"
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}
	run, err := runLocal(*baseURL, dir, testrun.Request{
		Peer: *peer, Tester: *tester, Notes: *notes, IncludeLongRunning: *long, IncludeFrames: *frames, IncludeUnadvertised: *unadvertised,
		Selection: report.Selection{ReadOnly: *readOnly, UseCases: splitList(*useCases), IDs: splitList(*ids)},
	})
	if errors.Is(err, testrun.ErrNothingSelected) {
		fmt.Println("no test case matches the selection")
		os.Exit(1)
	}
	if err != nil {
		fail(err)
	}
	printRun(run)
	if err := out.write(run); err != nil {
		fail(err)
	}
	if run.Status != report.StatusPassed {
		os.Exit(1)
	}
}

func printRun(run *report.Run) {
	for _, r := range run.TestCases {
		line := fmt.Sprintf("%-8s %-9s %s (%.1fs)", strings.ToUpper(r.Status), report.GroupOf(r), r.ID, r.DurationS)
		if r.Reason != "" {
			line += " -- " + report.ReasonText(r.Reason)
		}
		fmt.Println(line)
		if r.Status == scenario.StatusFailed {
			for _, s := range r.Steps {
				if s.Status == scenario.StatusFailed {
					fmt.Printf("           step %q: %s\n", s.Name, s.Detail)
				}
			}
		}
	}
	for _, c := range run.Checks {
		fmt.Printf("CHECK    %-8s %s: %s\n", strings.ToUpper(c.Status), c.Title, c.Detail)
	}
	fmt.Printf("\n%s\nrun %s: %s\n", run.Summary.Headline, run.ID, run.Status)
}

// runReportCmd renders a stored run again, from its JSON record or from an HTML report that
// embeds it.
func runReportCmd(args []string) {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	out := addOutputFlags(fs)
	_ = fs.Parse(reorderArgs(args, scenarioValueFlags))
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: eebus-testbench report <run.json|report.html> [-html|-json|-junit|-csv|-csv-steps|-xlsx|-frames-log path]")
		os.Exit(2)
	}
	data, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		fail(err)
	}
	run, err := report.Decode(data)
	if err != nil {
		fail(err)
	}
	if err := out.write(run); err != nil {
		fail(err)
	}
	fmt.Println(run.Summary.Headline)
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
