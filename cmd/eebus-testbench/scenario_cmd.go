package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/marck-brusa/eebuster/internal/scenario"
)

func defaultBaseURL() string {
	if v := os.Getenv("EEBUS_API_URL"); v != "" {
		return v
	}
	return "http://127.0.0.1:8080"
}

var scenarioValueFlags = map[string]bool{"junit": true, "base-url": true}

func runScenarioCmd(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	junit := fs.String("junit", "", "write a JUnit XML report here")
	baseURL := fs.String("base-url", defaultBaseURL(), "base URL of a running eebus-testbench serve instance")
	fs.Parse(reorderArgs(args, scenarioValueFlags))
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: eebus-testbench run <scenario.yaml> [-junit path] [-base-url url]")
		os.Exit(2)
	}

	result, err := scenario.NewRunner(*baseURL).RunScenario(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	printJSON(result)
	if *junit != "" {
		if err := os.WriteFile(*junit, []byte(result.ToJUnitXML()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if result.Status == "failed" {
		os.Exit(1)
	}
}

func runAllScenariosCmd(args []string) {
	fs := flag.NewFlagSet("run-all", flag.ExitOnError)
	junit := fs.String("junit", "", "write a combined JUnit XML report here")
	baseURL := fs.String("base-url", defaultBaseURL(), "base URL of a running eebus-testbench serve instance")
	fs.Parse(reorderArgs(args, scenarioValueFlags))
	dir := "scenarios"
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	suite, err := scenario.NewRunner(*baseURL).RunAll(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, r := range suite.Results {
		fmt.Printf("%-8s %s (%.1fs)\n", strings.ToUpper(r.Status), r.Name, r.DurationS)
		if r.Status == "failed" {
			for _, s := range r.Steps {
				if s.Status == "failed" {
					fmt.Printf("           step %q: %s\n", s.Name, s.Detail)
				}
			}
		}
	}
	fmt.Printf("\n%d passed, %d failed, %d skipped\n", suite.Passed(), suite.Failed(), suite.Skipped())
	if *junit != "" {
		if err := os.WriteFile(*junit, []byte(suite.ToJUnitXML()), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if suite.Status() == "failed" {
		os.Exit(1)
	}
}

func printJSON(v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Println(string(b))
}
