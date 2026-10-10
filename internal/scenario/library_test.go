package scenario

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// knownParameters are the device parameters a run provides: configured per peer or derived
// from the device (internal/testrun/params.go).
var knownParameters = map[string]bool{
	"nominal_max_w": true, "limits_w": true, "failsafe_w": true, "limit_durations": true, "failsafe_durations": true,
	"production_nominal_max_w": true, "production_limits_w": true,
}

var knownVerbs = map[string]bool{
	"wait_connected": true, "sleep": true, "log": true, "call": true, "put": true, "post": true, "delete": true,
	"assert": true, "expect_event": true, "capture": true, "scenarios_advertised": true, "conformance": true, "for_each": true,
	"wait_for": true, "limit_descriptions": true, "skip_unless": true,
}

var paramRef = regexp.MustCompile(`\{params\.([a-z_]+)`)

// TestScenarioLibrary validates every bundled scenario beyond YAML syntax: known keys, known
// verbs, a specification reference that matches the use-case catalog, and parameters a run
// can provide.
func TestScenarioLibrary(t *testing.T) {
	paths, _ := filepath.Glob(filepath.Join("..", "..", "scenarios", "*.yaml"))
	if len(paths) == 0 {
		t.Fatal("no scenarios found")
	}
	ids := map[string]bool{}
	for _, path := range paths {
		id := strings.TrimSuffix(filepath.Base(path), ".yaml")
		ids[id] = true
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var f fileSpec
		decoder := yaml.NewDecoder(bytes.NewReader(data))
		decoder.KnownFields(true)
		if err := decoder.Decode(&f); err != nil {
			t.Errorf("%s: %v", id, err)
			continue
		}
		checkScenarioFile(t, id, f, string(data))
	}
	for _, leading := range []string{"smoke-pairing", "device-profile-discovery"} {
		if !ids[leading] {
			t.Errorf("the library lacks %s", leading)
		}
	}
}

func checkScenarioFile(t *testing.T, id string, f fileSpec, raw string) {
	t.Helper()
	if f.Name != id {
		t.Errorf("%s: name %q does not match the file name", id, f.Name)
	}
	if f.Covers == "" || f.Goal == "" {
		t.Errorf("%s: covers and goal are required", id)
	}
	switch f.Risk {
	case "read-only", "live-control", "disruptive":
	default:
		t.Errorf("%s: unknown risk %q", id, f.Risk)
	}
	if f.Cleanup && f.Risk == "read-only" {
		t.Errorf("%s: a cleanup test case changes the device; its risk cannot be read-only", id)
	}
	if f.Spec != nil {
		checkSpec(t, id, f)
	} else if len(f.Requires.Scenarios) > 0 {
		t.Errorf("%s: requires.scenarios needs spec.use_case", id)
	}
	for _, step := range append(append([]map[string]any{}, f.Steps...), f.Finally...) {
		checkStep(t, id, step)
	}
	checkReferences(t, id, append(append([]map[string]any{}, f.Steps...), f.Finally...))
	declared := map[string]bool{}
	for _, name := range f.Requires.Parameters {
		if !knownParameters[name] {
			t.Errorf("%s: requires unknown parameter %s", id, name)
		}
		declared[name] = true
	}
	for _, m := range paramRef.FindAllStringSubmatch(raw, -1) {
		if !knownParameters[m[1]] {
			t.Errorf("%s: references unknown parameter %s", id, m[1])
		} else if !declared[m[1]] {
			t.Errorf("%s: uses params.%s without listing it in requires.parameters", id, m[1])
		}
	}
}

func checkSpec(t *testing.T, id string, f fileSpec) {
	t.Helper()
	spec := f.Spec
	switch spec.Verification {
	case VerificationWire, VerificationReadback, VerificationProbe:
	default:
		t.Errorf("%s: spec.verification %q is not wire, readback or needs-device-probe", id, spec.Verification)
	}
	uc, known := ucspec.Lookup(spec.UseCase)
	switch {
	case spec.UseCase == "" && spec.Document == "":
		t.Errorf("%s: a spec without use_case needs a document", id)
	case spec.UseCase != "" && !known:
		t.Errorf("%s: unknown use case %q", id, spec.UseCase)
	case known:
		checkUseCaseSpec(t, id, uc, f)
	}
}

func checkUseCaseSpec(t *testing.T, id string, uc ucspec.UseCase, f fileSpec) {
	t.Helper()
	spec := f.Spec
	if _, ok := uc.Scenario(spec.Scenario); spec.Scenario > 0 && !ok {
		t.Errorf("%s: %s has no scenario %d", id, uc.Acronym, spec.Scenario)
	}
	for _, n := range f.Requires.Scenarios {
		if _, ok := uc.Scenario(n); !ok {
			t.Errorf("%s: requires scenario %d that %s does not define", id, n, uc.Acronym)
		}
	}
	if spec.TestCase != "" && !strings.HasPrefix(spec.TestCase, "ATC_") {
		t.Errorf("%s: test_case %q is not an abstract test case id", id, spec.TestCase)
	}
	if spec.TestCase != "" && uc.TestSpecification == "" {
		t.Errorf("%s: %s has no test specification to take %s from", id, uc.Acronym, spec.TestCase)
	}
}

func checkStep(t *testing.T, id string, step map[string]any) {
	t.Helper()
	verb := firstKey(step)
	switch {
	case len(step) != 1:
		t.Errorf("%s: a step has %d verbs, want one: %v", id, len(step), step)
	case !knownVerbs[verb]:
		t.Errorf("%s: unknown step verb %q", id, verb)
	case verb == "for_each":
		for _, nested := range asSlice(argsMap(step[verb])["steps"]) {
			if m, ok := nested.(map[string]any); ok {
				checkStep(t, id, m)
			}
		}
	case verb == "assert" || verb == "wait_for" || verb == "skip_unless":
		for key := range argsMap(step[verb]) {
			known := key == "get" || (verb == "wait_for" && key == "timeout") || (verb == "skip_unless" && key == "reason")
			for _, op := range assertionOps {
				known = known || key == op
			}
			if !known {
				t.Errorf("%s: unknown assertion %q", id, key)
			}
		}
	case verb == "call":
		if method, _ := argsMap(step[verb])["method"].(string); callPaths[method] == "" {
			t.Errorf("%s: call of unknown method %q", id, method)
		}
	}
}

// referenceRoots are what a {name} reference in a step argument can start with, besides the
// names for_each binds. Anything else is substituted with <nil> at run time -- a regular
// expression's {n} repetition, for example.
var referenceRoots = map[string]bool{"peer": true, "params": true, "captured": true}

func checkReferences(t *testing.T, id string, steps []map[string]any) {
	t.Helper()
	bound := map[string]bool{}
	var collect func(v any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if each, ok := x["for_each"].(map[string]any); ok {
				name, _ := each["as"].(string)
				if name == "" {
					name = "item"
				}
				bound[name], bound[name+"_index"] = true, true
			}
			for _, item := range x {
				collect(item)
			}
		case []any:
			for _, item := range x {
				collect(item)
			}
		}
	}
	var check func(v any)
	check = func(v any) {
		switch x := v.(type) {
		case string:
			for _, m := range refRe.FindAllStringSubmatch(x, -1) {
				root := strings.SplitN(m[1], ".", 2)[0]
				if !referenceRoots[root] && !bound[root] {
					t.Errorf("%s: %q is not a reference the runner can resolve; it would become <nil>", id, m[0])
				}
			}
		case map[string]any:
			for _, item := range x {
				check(item)
			}
		case []any:
			for _, item := range x {
				check(item)
			}
		}
	}
	for _, step := range steps {
		collect(step)
	}
	for _, step := range steps {
		check(step)
	}
}
