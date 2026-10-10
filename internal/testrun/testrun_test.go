package testrun

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/scenario"
)

// Synthetic identifiers only: this repository is public.
const deviceSKI = "0123456789abcdef0123456789abcdef01234567"

// fakeTestbench answers the REST calls a run makes, for a connected device advertising LPC.
type fakeTestbench struct {
	mu        sync.Mutex
	requests  []string
	sleepPath string
}

func (f *fakeTestbench) handler(t *testing.T) http.Handler {
	responses := map[string]string{
		"GET /api/v1/version":                              `{"name":"eebus-testbench","version":"9.9.9","modules":{"github.com/enbility/eebus-go":"v0.0.1"},"host":"bench","platform":"linux/amd64"}`,
		"GET /api/v1/identity":                             `{"eebus-go-remote":{"ski":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}}`,
		"GET /api/v1/config":                               `{"api":{"bind":"127.0.0.1","port":8080},"network":{"mode":"static","interface":"lo"},"peers":[{"name":"device-under-test","label":"Bench","ski":"` + deviceSKI + `","host":"192.0.2.10","port":4712,"path":"/ship/","parameters":{"limits_w":[1000,500]}}]}`,
		"GET /api/v1/diagnostics/network":                  `{"ship_port":4712,"announced":[{"ip":"192.0.2.1","interface":"eth0"}]}`,
		"GET /api/v1/peers":                                `[{"ski":"` + deviceSKI + `","connected":true,"label":"Bench","serial":"SN-1","device_address":"d:_i:test"}]`,
		"GET /api/v1/peers/" + deviceSKI + "/profile":      `{"device_type":"Generic","entities":[{"address":[1],"type":"EVSE"}],"use_case_details":[{"name":"limitationOfPowerConsumption","acronym":"LPC","actor":"ControllableSystem","version":"1.0.0","scenarios":[1,2,3],"available":true}]}`,
		"GET /api/v1/peers/" + deviceSKI + "/usecases":     `[{"useCaseSupport":[{"useCaseName":"limitationOfPowerConsumption","useCaseAvailable":true,"scenarioSupport":[1,2,3]}]}]`,
		"GET /api/v1/peers/" + deviceSKI + "/manufacturer": `{"entities":[{"entity":[1],"entity_type":"EVSE","data":{"software_revision":"1.2.3"}}]}`,
		"GET /api/v1/energy/" + deviceSKI + "/snapshot":    `{"power":{"consumption_w":900},"limits":{"failsafe_w":2000},"ev":{"connected_count":0,"charging_count":0,"vehicles":[]}}`,
		"GET /api/v1/lpc/" + deviceSKI + "/failsafe":       `{"value_w":2000,"duration":"PT2H"}`,
		"GET /api/v1/lpc/" + deviceSKI + "/heartbeat":      `{"within_duration":true,"heartbeat_timeout_s":60}`,
		"GET /api/v1/lpc/" + deviceSKI + "/nominal-max":    `{"value_w":11000}`,
		"GET /api/v1/lpc/" + deviceSKI + "/limit":          `{"value_w":1000,"is_active":true}`,
		"PUT /api/v1/lpc/" + deviceSKI + "/limit":          `{"accepted":true}`,
		"GET /api/v1/trace":                                `{"entries":[],"latest_seq":10}`,
		"GET /api/v1/trace/summary":                        `{"errors":0,"warnings":0}`,
		"GET /api/v1/events/recent":                        `[{"seq":5}]`,
		"POST /api/v1/discover":                            `{"found":[]}`,
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Method + " " + r.URL.Path
		f.mu.Lock()
		f.requests = append(f.requests, key)
		sleep := f.sleepPath
		f.mu.Unlock()
		if key == sleep {
			time.Sleep(300 * time.Millisecond)
		}
		body, ok := responses[key]
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	})
}

func (f *fakeTestbench) count(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, r := range f.requests {
		if r == key {
			n++
		}
	}
	return n
}

func write(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// library writes a small scenario library: a read-only check, a write that reads back a
// device parameter, a long-running one, and a cleanup.
func library(t *testing.T) string {
	dir := t.TempDir()
	write(t, dir, "smoke-pairing.yaml", "name: smoke-pairing\npeer: device-under-test\nsteps: [{wait_connected: {timeout: 2s}}]\n")
	write(t, dir, "lpc-read.yaml", `name: lpc-read
spec: {use_case: LPC, scenario: 1}
risk: read-only
peer: device-under-test
steps:
  - wait_connected: {timeout: 2s}
  - assert: {get: "/api/v1/lpc/{peer.ski}/limit", greater_than: {value_w: 0}}
`)
	write(t, dir, "lpc-write.yaml", `name: lpc-write
spec: {use_case: LPC, scenario: 1}
risk: live-control
peer: device-under-test
requires: {parameters: [limits_w]}
steps:
  - wait_connected: {timeout: 2s}
  - put: {path: "/api/v1/lpc/{peer.ski}/limit", body: {value_w: "{params.limits_w.0}", is_active: true}}
  - assert: {get: "/api/v1/lpc/{peer.ski}/limit", equals: {value_w: "{params.limits_w.0}"}}
`)
	write(t, dir, "lpc-slow.yaml", "name: lpc-slow\nspec: {use_case: LPC}\nrisk: read-only\nlong_running: true\nsteps: [{sleep: 60}]\n")
	write(t, dir, "lpc-cleanup.yaml", "name: lpc-cleanup\nspec: {use_case: LPC}\nrisk: live-control\ncleanup: true\nsteps: [{log: cleanup}]\n")
	write(t, dir, "mpc-read.yaml", "name: mpc-read\nspec: {use_case: MPC, scenario: 1}\nrisk: read-only\nsteps: [{log: x}]\n")
	return dir
}

func ids(metas []scenario.Meta) string {
	var out []string
	for _, m := range metas {
		out = append(out, m.ID)
	}
	return strings.Join(out, " ")
}

func TestSelect(t *testing.T) {
	metas, err := scenario.Catalog(library(t))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		sel  report.Selection
		want string
	}{
		{"all", report.Selection{}, "smoke-pairing lpc-read lpc-write mpc-read lpc-cleanup"},
		{"read only has no cleanup", report.Selection{ReadOnly: true}, "smoke-pairing lpc-read mpc-read"},
		{"long running on request", report.Selection{ReadOnly: true, IncludeLongRunning: true}, "smoke-pairing lpc-read lpc-slow mpc-read"},
		{"by use case name", report.Selection{UseCases: []string{"limitationOfPowerConsumption"}}, "lpc-read lpc-write lpc-cleanup"},
		{"common group", report.Selection{UseCases: []string{"common"}}, "smoke-pairing"},
		{"ids are exact", report.Selection{IDs: []string{"lpc-write"}}, "lpc-write"},
		{"risk", report.Selection{Risks: []string{"live-control"}}, "lpc-write lpc-cleanup"},
	}
	for _, c := range cases {
		if got := ids(Select(metas, c.sel)); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func newManager(t *testing.T, fake *fakeTestbench, reports string) (*Manager, func()) {
	srv := httptest.NewServer(fake.handler(t))
	var events []string
	var mu sync.Mutex
	m := NewManager(func() string { return srv.URL }, library(t), reports, "9.9.9", func(event, _ string, _ map[string]any) {
		mu.Lock()
		events = append(events, event)
		mu.Unlock()
	})
	return m, srv.Close
}

func TestRunRecordsIdentificationAndPersists(t *testing.T) {
	fake := &fakeTestbench{}
	reports := t.TempDir()
	m, stop := newManager(t, fake, reports)
	defer stop()
	run, err := m.RunAndWait(Request{Tester: "QA"})
	if err != nil {
		t.Fatal(err)
	}
	// The fake device advertises LPC only, so the MPC test case is left out with a notice.
	if run.Status != report.StatusPassed || run.Summary.Total != 4 || run.Summary.Failed != 0 {
		t.Fatalf("status %s, summary %+v, cases %+v", run.Status, run.Summary, run.TestCases)
	}
	if run.Device.SKI != deviceSKI || run.Device.Serial != "SN-1" || run.Device.Host != "192.0.2.10" || len(run.Device.UseCases) != 1 ||
		len(run.Device.Manufacturer) != 1 || run.Testbench.Version != "9.9.9" || run.Testbench.SKI == "" || run.Library.Files != 6 {
		t.Errorf("identification: device %+v testbench %+v library %+v", run.Device, run.Testbench, run.Library)
	}
	if run.ConditionsStart == nil || run.ConditionsEnd == nil || run.ConditionsStart.FailsafeDuration != "PT2H" {
		t.Errorf("conditions: %+v %+v", run.ConditionsStart, run.ConditionsEnd)
	}
	// limits_w is configured; failsafe_w is derived from the 11000 W nominal maximum.
	if v := run.Parameters.Values["limits_w"]; v.([]any)[0] != 1000.0 {
		t.Errorf("configured limits_w = %v", v)
	}
	if v := run.Parameters.Values["failsafe_w"]; v.([]any)[0] != 5500.0 || !contains(run.Parameters.Derived, "failsafe_w") || contains(run.Parameters.Derived, "limits_w") {
		t.Errorf("failsafe_w = %v, derived %v", v, run.Parameters.Derived)
	}
	if fake.count("PUT /api/v1/lpc/"+deviceSKI+"/limit") != 1 {
		t.Error("the write test case did not run")
	}
	if len(run.Checks) < 3 {
		t.Errorf("checks = %+v", run.Checks)
	}
	if len(run.Notices) != 1 || !strings.Contains(run.Notices[0], "MPC") {
		t.Errorf("notices = %v", run.Notices)
	}
	if _, err := os.Stat(filepath.Join(reports, run.ID+".json")); err != nil {
		t.Errorf("run not persisted: %v", err)
	}
	if list := m.List(); len(list) != 1 || list[0].ID != run.ID || list[0].Total != 4 {
		t.Errorf("list = %+v", list)
	}
	stored, err := m.Get(run.ID)
	if err != nil || stored.Summary.Headline != run.Summary.Headline {
		t.Errorf("get = %v, %v", stored, err)
	}
}

// The listing is the folder as it is: a copied report shows up under its file name, a
// deleted one disappears, and a file that is not a run is ignored.
func TestListFollowsTheReportsFolder(t *testing.T) {
	fake := &fakeTestbench{}
	reports := t.TempDir()
	m, stop := newManager(t, fake, reports)
	defer stop()
	run, err := m.RunAndWait(Request{Selection: report.Selection{IDs: []string{"smoke-pairing"}}})
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(reports, run.ID+".json"))
	write(t, reports, "copied-from-the-lab.json", string(data))
	write(t, reports, "notes.json", `{"hello":"world"}`)
	write(t, reports, "broken.json", `{`)
	ids := map[string]bool{}
	for _, e := range m.List() {
		ids[e.ID] = true
	}
	if len(ids) != 2 || !ids[run.ID] || !ids["copied-from-the-lab"] {
		t.Errorf("listing = %v", ids)
	}
	if copied, err := m.Get("copied-from-the-lab"); err != nil || copied.Summary.Total != 1 {
		t.Errorf("copied report: %v %v", copied, err)
	}
	if _, err := os.Stat(filepath.Join(reports, run.ID+".html")); err != nil {
		t.Errorf("no HTML report next to the JSON: %v", err)
	}
	if file := m.ReportFile(run.ID); !strings.HasSuffix(file, ".html") {
		t.Errorf("ReportFile = %q, want the HTML report", file)
	}
	// An HTML report alone, copied in from elsewhere, is listed too.
	page, _ := os.ReadFile(filepath.Join(reports, run.ID+".html"))
	write(t, reports, "from-a-colleague.html", string(page))
	if _, err := m.Get("from-a-colleague"); err != nil {
		t.Errorf("HTML-only report: %v", err)
	}
	if err := os.Remove(filepath.Join(reports, "from-a-colleague.html")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(reports, run.ID+".json")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(reports, run.ID+".html")); err != nil {
		t.Fatal(err)
	}
	if list := m.List(); len(list) != 1 {
		t.Errorf("after deleting a file the listing has %d entries", len(list))
	}
	if _, err := m.Get(run.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted report: %v", err)
	}
}

func TestSecondRunIsRefusedWhileOneIsActive(t *testing.T) {
	fake := &fakeTestbench{sleepPath: "GET /api/v1/lpc/" + deviceSKI + "/limit"}
	m, stop := newManager(t, fake, "")
	defer stop()
	first, err := m.Start(Request{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Start(Request{}); !errors.Is(err, ErrRunning) {
		t.Errorf("second start: %v, want ErrRunning", err)
	}
	if m.ActiveID() != first.ID {
		t.Errorf("active = %q, want %q", m.ActiveID(), first.ID)
	}
	waitFinished(t, m, first.ID)
}

func TestCancelRunsCleanupAndMarksTheRestNotRun(t *testing.T) {
	fake := &fakeTestbench{sleepPath: "GET /api/v1/lpc/" + deviceSKI + "/limit"}
	m, stop := newManager(t, fake, "")
	defer stop()
	run, err := m.Start(Request{IncludeUnadvertised: true})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if err := m.Cancel(run.ID); err != nil {
		t.Fatal(err)
	}
	final := waitFinished(t, m, run.ID)
	if final.Status != report.StatusCancelled {
		t.Errorf("status = %s", final.Status)
	}
	statuses := map[string]string{}
	for _, tc := range final.TestCases {
		statuses[tc.ID] = tc.Status + "/" + tc.Reason
	}
	if statuses["lpc-cleanup"] != "passed/" {
		t.Errorf("cleanup = %q, want passed", statuses["lpc-cleanup"])
	}
	if statuses["mpc-read"] != "skipped/"+scenario.ReasonNotRun {
		t.Errorf("mpc-read = %q, want not run", statuses["mpc-read"])
	}
	if len(final.TestCases) != 5 || !strings.Contains(final.Summary.Headline, "cancelled") {
		t.Errorf("%d test cases, headline %q", len(final.TestCases), final.Summary.Headline)
	}
}

func TestNothingSelected(t *testing.T) {
	m, stop := newManager(t, &fakeTestbench{}, "")
	defer stop()
	if _, err := m.Start(Request{Selection: report.Selection{IDs: []string{"does-not-exist"}}}); !errors.Is(err, ErrNothingSelected) {
		t.Errorf("err = %v", err)
	}
}

func TestGetRejectsPathTraversal(t *testing.T) {
	m := NewManager(func() string { return "" }, t.TempDir(), t.TempDir(), "", nil)
	if _, err := m.Get("../../etc/passwd"); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v", err)
	}
}

func waitFinished(t *testing.T, m *Manager, id string) *report.Run {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for m.ActiveID() == id && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	run, err := m.Get(id)
	if err != nil || run.Status == report.StatusRunning {
		t.Fatalf("run did not finish: %v %+v", err, run)
	}
	encoded, _ := json.Marshal(run)
	if !json.Valid(encoded) {
		t.Fatal("run does not encode")
	}
	return run
}

func TestHeartbeatCheckMeasuresGaps(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(s float64) float64 { return float64(start.Unix()) + s }
	timeout := 120.0
	run := func(duration float64) *report.Run {
		return &report.Run{StartedAt: start, DurationS: duration, ConditionsEnd: &report.Conditions{HeartbeatTimeoutS: &timeout}}
	}
	cases := []struct {
		name     string
		times    []float64
		duration float64
		want     string
	}{
		{"short run before the first heartbeat", nil, 12, scenario.StatusSkipped},
		{"every 60 s", []float64{at(35), at(95), at(155)}, 180, scenario.StatusPassed},
		{"silent for longer than the timeout", []float64{at(10)}, 300, scenario.StatusFailed},
		{"jitter around the timeout", []float64{at(118.5), at(239)}, 240, scenario.StatusPassed},
		{"nothing in a long run", nil, 200, scenario.StatusFailed},
	}
	for _, c := range cases {
		check := heartbeatCheck(&heartbeatMonitor{active: true, times: c.times}, run(c.duration))
		if check.Status != c.want {
			t.Errorf("%s: %s (%s), want %s", c.name, check.Status, check.Detail, c.want)
		}
	}
}

// A device announcing a 4 s timeout and sending every 2 s, observed from a run that started
// just after one of its heartbeats.
func TestHeartbeatCheckToleratesJitterOnShortTimeouts(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	timeout := 4.0
	base := float64(start.Unix())
	r := &report.Run{StartedAt: start, DurationS: 17, ConditionsStart: &report.Conditions{HeartbeatTimeoutS: &timeout}}
	times := []float64{base + 4.3, base + 6.3, base + 8.3, base + 10.3, base + 12.3, base + 14.3, base + 16.3}
	if check := heartbeatCheck(&heartbeatMonitor{active: true, times: times}, r); check.Status != scenario.StatusPassed {
		t.Errorf("%s: %s", check.Status, check.Detail)
	}
}
