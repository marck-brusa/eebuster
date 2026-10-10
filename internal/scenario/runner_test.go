package scenario

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marck-brusa/eebuster/internal/ucspec"
)

func TestEvaluateAssertionsReportsPassingAndFailingValues(t *testing.T) {
	actual := map[string]any{
		"value_w": 4200.0, "is_active": true, "phases": []any{6.0, 10.0, 16.0},
		"details": []any{map[string]any{"version": "1.0.0"}, map[string]any{"name": "x"}},
	}
	args := map[string]any{
		"equals":        map[string]any{"value_w": 4200, "is_active": false},
		"between":       map[string]any{"value_w": []any{0, 5000}},
		"each_between":  map[string]any{"phases": []any{6, 16}},
		"one_of":        map[string]any{"value_w": []any{1000, 4200}},
		"any_not_null":  []any{"missing", "value_w"},
		"each_not_null": map[string]any{"details": "version"},
		"not_null":      []any{"absent"},
		"some_not_null": map[string]any{"details": []any{"name", "missing|version"}},
	}
	got := map[string]bool{}
	for _, c := range evaluateAssertions(args, actual) {
		got[c.Op+" "+c.Key] = c.OK
	}
	want := map[string]bool{
		"equals value_w": true, "equals is_active": false,
		"between value_w":       true,
		"each_between phases.0": true, "each_between phases.1": true, "each_between phases.2": true,
		"one_of value_w":                  true,
		"any_not_null missing | value_w":  true,
		"each_not_null details.0.version": true, "each_not_null details.1.version": false,
		"not_null absent":              false,
		"some_not_null details.*.name": true, "some_not_null details.*.missing|version": true,
	}
	for key, ok := range want {
		if v, present := got[key]; !present || v != ok {
			t.Errorf("%s: got %v (present %v), want %v", key, v, present, ok)
		}
	}
}

// each_matches requires every element to match the whole pattern: the EVCC identification
// formats, where a lowercase or colon-separated MAC address is not the specified format.
func TestEachMatchesAppliesTheWholePattern(t *testing.T) {
	mac := "([0-9A-F]{2}-){5}[0-9A-F]{2}|([0-9A-F]{2}-){7}[0-9A-F]{2}"
	cases := []struct {
		values []any
		want   []bool
	}{
		{[]any{"02-00-00-00-00-01", "02-00-00-FF-FE-00-00-01"}, []bool{true, true}},
		{[]any{"02:00:00:00:00:01", "02-00-00-00-00-0a", "x02-00-00-00-00-01"}, []bool{false, false, false}},
		{[]any{42.0}, []bool{false}},
	}
	for _, c := range cases {
		comparisons := evaluateAssertions(map[string]any{"each_matches": map[string]any{"ids": mac}}, map[string]any{"ids": c.values})
		if len(comparisons) != len(c.want) {
			t.Fatalf("%v: %d comparisons", c.values, len(comparisons))
		}
		for i, comparison := range comparisons {
			if comparison.OK != c.want[i] {
				t.Errorf("%v: got %v, want %v", c.values[i], comparison.OK, c.want[i])
			}
		}
	}
	if comparisons := evaluateAssertions(map[string]any{"each_matches": map[string]any{"ids": mac}}, map[string]any{"ids": []any{}}); len(comparisons) != 1 || comparisons[0].OK {
		t.Errorf("an empty list must fail: %+v", comparisons)
	}
}

func TestEvaluateAssertionsKeepsActualValue(t *testing.T) {
	comparisons := evaluateAssertions(map[string]any{"less_or_equal": map[string]any{"limit": 60}},
		map[string]any{"limit": 120.0})
	if len(comparisons) != 1 || comparisons[0].OK || comparisons[0].Actual != 120.0 || comparisons[0].Expected != 60 {
		t.Fatalf("comparisons = %+v", comparisons)
	}
}

func TestResolveListsAndIndexedParameters(t *testing.T) {
	ctx := map[string]any{"params": map[string]any{"limits_w": []any{11000.0, 4200.0}}}
	if v := resolve("{params.limits_w.1}", ctx); v != 4200.0 {
		t.Errorf("indexed parameter = %#v", v)
	}
	body := resolve(map[string]any{"limits": []any{map[string]any{"value": "{params.limits_w.0}"}}}, ctx)
	value := body.(map[string]any)["limits"].([]any)[0].(map[string]any)["value"]
	if value != 11000.0 {
		t.Errorf("list element not resolved: %#v", body)
	}
	if v := resolve("{params.limits_w.9}", ctx); v != nil {
		t.Errorf("out-of-range index = %#v, want nil", v)
	}
}

func TestExpandForEachBindsEveryItem(t *testing.T) {
	ctx := map[string]any{"params": map[string]any{"values": []any{1.0, 2.0}}}
	expanded, step := expandForEach(map[string]any{
		"items": "{params.values}", "as": "v",
		"steps": []any{map[string]any{"log": "a"}, map[string]any{"log": "b"}},
	}, ctx)
	if step.Status != StatusPassed || len(expanded) != 4 {
		t.Fatalf("step = %+v, expanded = %d", step, len(expanded))
	}
	if expanded[2].bindings["v"] != 2.0 || expanded[2].bindings["v_index"] != 1 {
		t.Errorf("third step bindings = %v", expanded[2].bindings)
	}
	_, empty := expandForEach(map[string]any{"items": "{params.none}", "steps": []any{}}, ctx)
	if empty.Status != StatusFailed {
		t.Errorf("unresolved items should fail, got %+v", empty)
	}
}

func TestJudgeScenariosFailsOnlyOnMandatory(t *testing.T) {
	uc, _ := ucspec.Lookup("LPC")
	status, detail, comparisons := judgeScenarios(uc, map[string][]uint{uc.Name: {1, 2, 3}})
	if status != StatusPassed || !strings.Contains(detail, "4 Constraints") || len(comparisons) != 4 {
		t.Errorf("recommended scenario missing: status %s, detail %q", status, detail)
	}
	opev, _ := ucspec.Lookup("OPEV")
	// A device that lists scenario 2 twice and never 3.
	status, detail, _ = judgeScenarios(opev, map[string][]uint{opev.Name: {1, 2, 2}})
	if status != StatusFailed || !strings.Contains(detail, "3 Energy Guard sends error state") {
		t.Errorf("mandatory scenario missing: status %s, detail %q", status, detail)
	}
	status, _, _ = judgeScenarios(opev, map[string][]uint{})
	if status != StatusFailed {
		t.Errorf("use case not advertised should fail, got %s", status)
	}
}

func TestMissingScenarioGapsSkipOnlyNonMandatory(t *testing.T) {
	mpc, _ := ucspec.Lookup("MPC")
	support := map[string][]uint{mpc.Name: {1}}
	if gaps := missingScenarioGaps(requirementsSpec{Scenarios: []uint{2}, useCase: "MPC"}, support); len(gaps) != 1 || gaps[0].Reason != ReasonScenarioNotAdvertised {
		t.Errorf("optional scenario 2: gaps = %+v", gaps)
	}
	// Scenario 1 is mandatory: the test runs and finds out whether the data is there.
	if gaps := missingScenarioGaps(requirementsSpec{Scenarios: []uint{1}, useCase: "MPC"}, map[string][]uint{mpc.Name: {}}); len(gaps) != 0 {
		t.Errorf("mandatory scenario must not skip: gaps = %+v", gaps)
	}
}

func TestOrderGroupsByUseCaseAndRisk(t *testing.T) {
	metas := []Meta{
		{ID: "lpc-cleanup", UseCase: "LPC", Risk: "live-control", Cleanup: true},
		{ID: "mpc-live-power", UseCase: "MPC", Risk: "read-only"},
		{ID: "lpc-heartbeat-loss", UseCase: "LPC", Risk: "disruptive"},
		{ID: "lpc-basic-limit", UseCase: "LPC", Risk: "live-control"},
		{ID: "lpc-read-current", UseCase: "LPC", Risk: "read-only"},
		{ID: "conformance-window", Risk: "read-only"},
		{ID: "device-profile-discovery", Risk: "read-only"},
		{ID: "smoke-pairing", Risk: "read-only"},
	}
	Order(metas)
	var ids []string
	for _, m := range metas {
		ids = append(ids, m.ID)
	}
	want := "smoke-pairing device-profile-discovery conformance-window lpc-read-current lpc-basic-limit lpc-heartbeat-loss mpc-live-power lpc-cleanup"
	if strings.Join(ids, " ") != want {
		t.Errorf("order = %s\nwant    %s", strings.Join(ids, " "), want)
	}
}

func TestLoadMetaFillsSpecFromCatalog(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "mpc-energy.yaml")
	writeFile(t, path, "name: mpc-energy\nspec: {use_case: monitoringOfPowerConsumption, scenario: 2}\nsteps: []\n")
	meta, err := LoadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	if meta.UseCase != "MPC" || meta.Spec.UseCase != "MPC" || meta.Spec.ScenarioTitle != "Monitor energy" ||
		meta.Spec.DeviceLevel != "O" || !strings.Contains(meta.Spec.Document, "Monitoring of Power Consumption") {
		t.Errorf("meta = %+v spec = %+v", meta, meta.Spec)
	}
}

// fakeAPI serves the endpoints a scenario run touches.
func fakeAPI(t *testing.T, handlers map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := handlers[r.Method+" "+r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunRecordsRequestEvidence(t *testing.T) {
	srv := fakeAPI(t, map[string]string{
		"GET /api/v1/peers":                     `[{"ski":"` + testSKI + `","connected":true}]`,
		"GET /api/v1/config":                    `{"peers":[{"name":"device-under-test","ski":"` + testSKI + `"}]}`,
		"PUT /api/v1/lpc/" + testSKI + "/limit": `{"accepted":true}`,
		"GET /api/v1/lpc/" + testSKI + "/limit": `{"value_w":4200,"is_active":true}`,
		"GET /api/v1/trace":                     `{"entries":[],"latest_seq":7}`,
		"GET /api/v1/events/recent":             `[{"seq":3}]`,
	})
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "lpc.yaml")
	writeFile(t, path, `name: lpc
spec: {use_case: LPC, scenario: 1}
peer: device-under-test
steps:
  - wait_connected: {timeout: 2s}
  - put: {path: "/api/v1/lpc/{peer.ski}/limit", body: {value_w: "{params.limit}", is_active: true}}
  - assert: {get: "/api/v1/lpc/{peer.ski}/limit", equals: {value_w: "{params.limit}"}}
`)
	result, err := NewRunnerWith(srv.URL, Options{Params: map[string]any{"limit": 4200}}).RunScenario(path)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != StatusPassed || len(result.Steps) != 3 {
		t.Fatalf("result = %+v", result)
	}
	put := result.Steps[1]
	if put.Request == nil || put.Request.Status != 200 || put.Request.Body.(map[string]any)["value_w"] != 4200 {
		t.Errorf("put evidence = %+v", put.Request)
	}
	check := result.Steps[2]
	if len(check.Assertions) != 1 || !check.Assertions[0].OK || check.Assertions[0].Actual != 4200.0 {
		t.Errorf("assertions = %+v", check.Assertions)
	}
	if result.UseCase != "LPC" || result.Spec == nil || result.Spec.ScenarioTitle == "" || result.Trace == nil {
		t.Errorf("metadata = %+v, trace %+v", result.Spec, result.Trace)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "eventRange") {
		t.Error("internal fields leaked into JSON")
	}
}

func TestRunSkipsWithReason(t *testing.T) {
	srv := fakeAPI(t, map[string]string{
		"GET /api/v1/peers":                          `[{"ski":"` + testSKI + `","connected":true}]`,
		"GET /api/v1/config":                         `{"peers":[{"name":"device-under-test","ski":"` + testSKI + `"}]}`,
		"GET /api/v1/peers/" + testSKI + "/usecases": `[{"useCaseSupport":[{"useCaseName":"monitoringOfPowerConsumption","useCaseAvailable":true,"scenarioSupport":[1]}]}]`,
	})
	defer srv.Close()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "a.yaml"), `name: a
spec: {use_case: MPC, scenario: 2}
peer: device-under-test
requires: {scenarios: [2]}
steps: [{wait_connected: {timeout: 2s}}, {log: never}]
`)
	writeFile(t, filepath.Join(dir, "b.yaml"), `name: b
peer: device-under-test
requires: {use_cases: [limitationOfPowerConsumption]}
steps: [{wait_connected: {timeout: 2s}}]
`)
	writeFile(t, filepath.Join(dir, "c.yaml"), `name: c
spec: {use_case: LPC, verification: needs-device-probe}
peer: device-under-test
steps: [{wait_connected: {timeout: 2s}}]
`)
	writeFile(t, filepath.Join(dir, "d.yaml"), `name: d
peer: device-under-test
requires: {parameters: [limits_w]}
steps: [{wait_connected: {timeout: 2s}}]
`)
	suite, err := NewRunner(srv.URL).RunAll(dir)
	if err != nil {
		t.Fatal(err)
	}
	reasons := map[string]string{}
	for _, r := range suite.Results {
		reasons[r.ID] = r.Status + "/" + r.Reason
	}
	want := map[string]string{
		"a": "skipped/" + ReasonScenarioNotAdvertised,
		"b": "skipped/" + ReasonUseCaseNotAdvertised,
		"c": "skipped/" + ReasonNotVerifiableOnWire,
		"d": "skipped/" + ReasonPreconditionNotMet,
	}
	for id, w := range want {
		if reasons[id] != w {
			t.Errorf("%s = %s, want %s", id, reasons[id], w)
		}
	}
}

func TestCancelledRunMarksTestCaseNotRun(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.NotFound(w, r)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "slow.yaml")
	writeFile(t, path, "name: slow\nsteps: [{sleep: 30}, {log: after}]\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	result, err := NewRunnerWith(srv.URL, Options{Context: ctx}).RunScenario(path)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(start) > 10*time.Second {
		t.Fatal("sleep was not interrupted by the cancellation")
	}
	if result.Status != StatusSkipped || result.Reason != ReasonNotRun {
		t.Errorf("result = %s/%s, steps %+v", result.Status, result.Reason, result.Steps)
	}
}

func TestWaitForRepeatsUntilTheAssertionHolds(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := `{"within_duration":false}`
		if r.URL.Path == "/api/v1/hb" && calls.Add(1) >= 3 {
			body = `{"within_duration":true,"heartbeat_timeout_s":60}`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	rn := NewRunner(srv.URL)
	step := rn.stepWaitFor(map[string]any{"get": "/api/v1/hb", "not_null": []any{"heartbeat_timeout_s"}, "timeout": "10s"}, map[string]any{})
	if step.Status != StatusPassed || calls.Load() < 3 {
		t.Fatalf("step = %+v after %d calls", step, calls.Load())
	}
	never := rn.stepWaitFor(map[string]any{"get": "/api/v1/other", "not_null": []any{"heartbeat_timeout_s"}, "timeout": "1s"}, map[string]any{})
	if never.Status != StatusFailed || !strings.Contains(never.Detail, "still not holding") {
		t.Errorf("never = %+v", never)
	}
}
