// Package testrun executes a selection of scenarios against one device as a documented test
// run: it records who and what was tested before the first test case, runs the test cases in
// a fixed order, checks the run as a whole, and persists the record after every test case so
// an interrupted run still leaves a readable report. Like the scenario runner it only talks
// to the testbench through its REST API.
package testrun

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marck-brusa/eebuster/internal/report"
	"github.com/marck-brusa/eebuster/internal/scenario"
	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// Request starts a run.
type Request struct {
	Peer                string           `json:"peer"`
	Selection           report.Selection `json:"selection"`
	IncludeLongRunning  bool             `json:"include_long_running"`
	IncludeFrames       bool             `json:"include_frames"`
	IncludeUnadvertised bool             `json:"include_unadvertised"`
	Tester              string           `json:"tester"`
	Notes               string           `json:"notes"`
}

// DefaultPeer is the configured peer name every bundled scenario targets.
const DefaultPeer = "device-under-test"

var (
	// ErrRunning is returned while another run is active.
	ErrRunning = errors.New("a run is in progress")
	// ErrNotFound is returned for an unknown run id.
	ErrNotFound = errors.New("no such run")
	// ErrNothingSelected is returned when the selection matches no test case.
	ErrNothingSelected = errors.New("no test case matches the selection")
)

// Publisher receives progress events.
type Publisher func(event, ski string, data map[string]any)

// Manager runs one test run at a time and keeps the records.
type Manager struct {
	baseURL      func() string
	scenariosDir string
	reportsDir   string
	version      string
	publish      Publisher

	mu      sync.Mutex
	active  *report.Run
	cancel  context.CancelFunc
	done    chan struct{}
	memory  map[string][]byte // finished runs when no reports directory is set
	running bool
}

// NewManager builds a manager. reportsDir may be empty, in which case finished runs are kept
// in memory only. baseURL is asked at the start of each run, so a reloaded API port is used.
func NewManager(baseURL func() string, scenariosDir, reportsDir, version string, publish Publisher) *Manager {
	if publish == nil {
		publish = func(string, string, map[string]any) {}
	}
	return &Manager{baseURL: baseURL, scenariosDir: scenariosDir, reportsDir: reportsDir, version: version, publish: publish, memory: map[string][]byte{}}
}

// ReportsDir is where finished runs are stored, or empty.
func (m *Manager) ReportsDir() string { return m.reportsDir }

func newID(now time.Time) string {
	suffix := make([]byte, 2)
	_, _ = rand.Read(suffix)
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(suffix)
}

// ActiveID returns the id of the running run, or empty.
func (m *Manager) ActiveID() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	id := ""
	if m.active != nil {
		id = m.active.ID
	}
	return id
}

// Start begins a run and returns its initial record. The run executes in the background.
func (m *Manager) Start(req Request) (*report.Run, error) {
	run, _, err := m.start(req)
	return run, err
}

// RunAndWait executes a run and returns its final record.
func (m *Manager) RunAndWait(req Request) (*report.Run, error) {
	run, done, err := m.start(req)
	if err == nil {
		<-done
		run, err = m.Get(run.ID)
	}
	return run, err
}

func (m *Manager) start(req Request) (*report.Run, chan struct{}, error) {
	metas, catalogErr := scenario.Catalog(m.scenariosDir)
	req.Selection.IncludeLongRunning = req.Selection.IncludeLongRunning || req.IncludeLongRunning
	req.Selection.IncludeFrames = req.Selection.IncludeFrames || req.IncludeFrames
	req.Selection.IncludeUnadvertised = req.Selection.IncludeUnadvertised || req.IncludeUnadvertised
	if strings.TrimSpace(req.Peer) == "" {
		req.Peer = DefaultPeer
	}
	selected := Select(metas, req.Selection)

	m.mu.Lock()
	defer m.mu.Unlock()
	var err error
	var run *report.Run
	var done chan struct{}
	switch {
	case m.running:
		err = fmt.Errorf("%w: %s", ErrRunning, m.active.ID)
	case catalogErr != nil:
		err = catalogErr
	case len(selected) == 0:
		err = ErrNothingSelected
	default:
		now := time.Now()
		run = &report.Run{
			Schema: report.Schema, ID: newID(now), Status: report.StatusRunning, StartedAt: now,
			Tester: strings.TrimSpace(req.Tester), Notes: strings.TrimSpace(req.Notes), Selection: req.Selection,
			Testbench: report.Testbench{Version: m.version}, Device: report.Device{Peer: req.Peer},
			Notices: []string{}, TestCases: []scenario.ScenarioResult{},
			VehiclesStart: []map[string]any{}, VehiclesEnd: []map[string]any{}, Checks: []report.Check{},
			Progress: &report.Progress{Total: len(selected)},
		}
		run.Summarize()
		ctx, cancel := context.WithCancel(context.Background())
		m.active, m.cancel, m.running = run, cancel, true
		done = make(chan struct{})
		m.done = done
		m.persistLocked(run)
		go m.execute(ctx, run, req, selected, done)
		run = cloneRun(run)
	}
	return run, done, err
}

// Select applies a selection to the catalog and returns the test cases in run order. The
// cleanup test cases come along whenever anything but read-only test cases is selected,
// except for an explicit list of test case ids, which runs exactly those.
func Select(metas []scenario.Meta, sel report.Selection) []scenario.Meta {
	ids := setOf(sel.IDs, false)
	useCases := setOf(sel.UseCases, true)
	risks := setOf(sel.Risks, false)
	var chosen []scenario.Meta
	changesDevice := false
	for _, meta := range metas {
		include := meta.Category != "Invalid"
		if len(ids) > 0 {
			include = include && ids[meta.ID]
		} else {
			include = include && !meta.Cleanup && (!meta.LongRunning || sel.IncludeLongRunning)
		}
		if sel.ReadOnly {
			include = include && meta.Risk == "read-only"
		}
		if len(useCases) > 0 {
			include = include && useCases[strings.ToUpper(groupKey(meta))]
		}
		if len(risks) > 0 {
			include = include && risks[meta.Risk]
		}
		if include {
			chosen = append(chosen, meta)
			changesDevice = changesDevice || meta.Risk != "read-only"
		}
	}
	for _, meta := range metas {
		if meta.Cleanup && changesDevice && len(ids) == 0 && (len(useCases) == 0 || useCases[strings.ToUpper(groupKey(meta))]) {
			chosen = append(chosen, meta)
		}
	}
	scenario.Order(chosen)
	return chosen
}

func groupKey(meta scenario.Meta) string {
	key := meta.UseCase
	if key == "" {
		key = report.CommonGroup
	}
	return key
}

func setOf(values []string, upper bool) map[string]bool {
	set := map[string]bool{}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if upper {
			if uc, ok := ucspec.Lookup(v); ok {
				v = uc.Acronym
			}
			v = strings.ToUpper(v)
		}
		if v != "" {
			set[v] = true
		}
	}
	return set
}

// update applies a change to the active run under the lock and persists it.
func (m *Manager) update(run *report.Run, change func(*report.Run)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	change(run)
	run.Summarize()
	m.persistLocked(run)
}

func (m *Manager) execute(ctx context.Context, run *report.Run, req Request, selected []scenario.Meta, done chan struct{}) {
	defer close(done)
	base := m.baseURL()
	c := newCollector(ctx, base)
	m.publish("run_started", "", map[string]any{"run_id": run.ID, "total": len(selected)})

	identification := c.identify(req.Peer, m.scenariosDir, len(req.Selection.IDs) == 0)
	m.update(run, func(r *report.Run) {
		r.Testbench, r.Device, r.Library = identification.testbench, identification.device, identification.library
		if r.Testbench.Version == "" {
			r.Testbench.Version = m.version
		}
		r.Parameters = identification.parameters
		r.VehiclesStart, r.ConditionsStart = identification.vehicles, identification.conditions
		r.Notices = append(r.Notices, identification.notices...)
	})
	ski := identification.device.SKI
	selected, leftOut := applicable(selected, req.Selection, identification.device)
	if leftOut != "" {
		m.update(run, func(r *report.Run) { r.Notices = append(r.Notices, leftOut) })
	}

	runner := scenario.NewRunnerWith(base, scenario.Options{Context: ctx, Params: identification.parameters.Values})
	// Bookkeeping between test cases must keep working after a cancellation, so it does not
	// share the run's context.
	probe := newCollector(context.Background(), base)
	conformance := newConformanceTracker(probe, identification.device, identification.conditions)
	monitor := newHeartbeatMonitor(identification.device, identification.conditions)
	// Sample the wire during test cases too, not only between them: the trace keeps a bounded
	// number of frames, and a long test case on a chatty device would otherwise lose some.
	stopSampling := make(chan struct{})
	go func() {
		ticker := time.NewTicker(sampleInterval)
		defer ticker.Stop()
		for sampling := true; sampling; {
			select {
			case <-stopSampling:
				sampling = false
			case <-ticker.C:
				conformance.collect()
				monitor.sample(probe)
			}
		}
	}()
	var regular, cleanup []scenario.Meta
	for _, meta := range selected {
		if meta.Cleanup {
			cleanup = append(cleanup, meta)
		} else {
			regular = append(regular, meta)
		}
	}

	m.update(run, func(r *report.Run) { r.Progress = &report.Progress{Total: len(selected)} })
	aborted := ""
	for i, meta := range regular {
		if ctx.Err() == nil && aborted == "" {
			m.update(run, func(r *report.Run) { r.Progress = &report.Progress{Index: i, Total: len(selected), Current: meta.ID} })
			result := runOne(runner, meta)
			result.RunID = run.ID
			conformance.collect()
			monitor.sample(probe)
			frames, dropped := probe.frames(result.Trace, req.Selection.IncludeFrames)
			if ski != "" && ctx.Err() == nil && !probe.awaitConnected(ski, 15*time.Second) {
				aborted = fmt.Sprintf("The device disconnected during %s and did not reconnect within 15 s; the remaining test cases were not run.", meta.ID)
			}
			m.update(run, func(r *report.Run) {
				r.TestCases = append(r.TestCases, result)
				r.Frames = append(r.Frames, frames...)
				r.FramesCount = len(r.Frames)
				r.FramesDropped += dropped
				r.Progress = &report.Progress{Index: i + 1, Total: len(selected), Current: meta.ID}
			})
			m.publish("run_progress", ski, map[string]any{"run_id": run.ID, "index": i + 1, "total": len(selected), "test_case": meta.ID, "status": result.Status})
		} else {
			detail := "the run was cancelled before this test case"
			if aborted != "" {
				detail = "the device disconnected before this test case"
			}
			notRun := scenario.NotRun(meta, detail)
			notRun.RunID = run.ID
			m.update(run, func(r *report.Run) { r.TestCases = append(r.TestCases, notRun) })
		}
	}

	// Cleanup always runs, also after a cancellation: it returns the device to a neutral
	// state. Only a device that is gone is spared it.
	cleanupRunner := scenario.NewRunnerWith(base, scenario.Options{Params: identification.parameters.Values})
	for _, meta := range cleanup {
		var result scenario.ScenarioResult
		if aborted != "" {
			result = scenario.NotRun(meta, "the device disconnected")
		} else {
			result = runOne(cleanupRunner, meta)
			conformance.collect()
		}
		result.RunID = run.ID
		m.update(run, func(r *report.Run) { r.TestCases = append(r.TestCases, result) })
	}

	close(stopSampling)
	final := newCollector(context.Background(), base)
	vehicles, conditions := final.vehiclesAndConditions(identification.device)
	conformance.collect()
	monitor.sample(final)
	m.update(run, func(r *report.Run) {
		finished := time.Now()
		r.FinishedAt = &finished
		r.DurationS = roundSeconds(finished.Sub(r.StartedAt).Seconds())
		r.VehiclesEnd, r.ConditionsEnd = vehicles, conditions
		r.Conformance = conformance.result()
		r.Checks = runChecks(final, r, conformance, monitor)
		if aborted != "" {
			r.Notices = append(r.Notices, aborted)
		}
		r.Progress = nil
		r.Status = finalStatus(ctx.Err() != nil, aborted != "", r)
	})
	m.writeHTML(run)
	m.publish("run_finished", ski, map[string]any{"run_id": run.ID, "status": run.Status, "headline": run.Summary.Headline})

	m.mu.Lock()
	m.running, m.active, m.cancel = false, nil, nil
	m.mu.Unlock()
}

// applicable leaves out the test cases of use cases the device does not advertise, so a run
// describes the device and not the library. Named test cases and use cases always run, and so
// does everything when the device could not be described.
func applicable(selected []scenario.Meta, sel report.Selection, d report.Device) ([]scenario.Meta, string) {
	keep := selected
	notice := ""
	if !sel.IncludeUnadvertised && len(sel.IDs) == 0 && len(sel.UseCases) == 0 && d.Connected && len(d.UseCases) > 0 {
		keep = nil
		var leftOut []string
		dropped := 0
		for _, meta := range selected {
			uc, known := ucspec.Lookup(meta.UseCase)
			if known && !advertises(d, uc.Name) {
				dropped++
				if !contains(leftOut, uc.Acronym) {
					leftOut = append(leftOut, uc.Acronym)
				}
			} else {
				keep = append(keep, meta)
			}
		}
		if dropped > 0 {
			notice = fmt.Sprintf("%d test %s of use cases the device does not advertise were left out: %s.",
				dropped, pluralWord(dropped, "case", "cases"), strings.Join(leftOut, ", "))
		}
	}
	return keep, notice
}

func pluralWord(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return word
}

func runOne(runner *scenario.Runner, meta scenario.Meta) scenario.ScenarioResult {
	result, err := runner.RunScenario(meta.Path)
	if err != nil {
		result = scenario.NotRun(meta, "")
		result.Status, result.Reason = scenario.StatusFailed, ""
		result.Steps = []scenario.StepResult{{Name: "load", Status: scenario.StatusFailed, Detail: err.Error()}}
	}
	return result
}

func finalStatus(cancelled, aborted bool, r *report.Run) string {
	status := report.StatusPassed
	switch {
	case cancelled:
		status = report.StatusCancelled
	case aborted:
		status = report.StatusAborted
	case r.Summary.Failed > 0 || failedChecks(r.Checks) > 0:
		status = report.StatusFailed
	}
	return status
}

func failedChecks(checks []report.Check) int {
	n := 0
	for _, c := range checks {
		if c.Status == scenario.StatusFailed {
			n++
		}
	}
	return n
}

func roundSeconds(s float64) float64 { return float64(int64(s*100+0.5)) / 100 }

// Cancel stops the active run after its current step. The cleanup test cases still run.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := ErrNotFound
	if m.active != nil && m.active.ID == id {
		m.cancel()
		m.publish("run_cancelled", m.active.Device.SKI, map[string]any{"run_id": id})
		err = nil
	}
	return err
}

// Get returns a copy of a run: the active one, or a run file in the reports folder named
// <id>.json. Files are read as they are on disk, so a file copied into the folder is found
// and a deleted one is simply not found.
func (m *Manager) Get(id string) (*report.Run, error) {
	m.mu.Lock()
	var data []byte
	err := ErrNotFound
	if m.active != nil && m.active.ID == id {
		data, err = json.Marshal(m.active)
	} else if stored, ok := m.memory[id]; ok {
		data, err = stored, nil
	}
	m.mu.Unlock()
	if data == nil && err == ErrNotFound && m.reportsDir != "" && fileID.MatchString(id) {
		data, err = os.ReadFile(filepath.Join(m.reportsDir, id+".json"))
		if err != nil {
			data, err = os.ReadFile(filepath.Join(m.reportsDir, id+".html"))
		}
		if err != nil {
			err = ErrNotFound
		}
	}
	var run *report.Run
	if err == nil {
		if run, err = report.Decode(data); err != nil {
			err = ErrNotFound
		}
	}
	return run, err
}

// ReportFile is the file in the reports folder a person opens for a run: its HTML report,
// else its JSON record. Empty when the run has no file there.
func (m *Manager) ReportFile(id string) string {
	file := ""
	if m.reportsDir != "" && fileID.MatchString(id) {
		for _, ext := range []string{".json", ".html"} {
			if path := filepath.Join(m.reportsDir, id+ext); fileExists(path) {
				file = path
			}
		}
	}
	return file
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// writeHTML puts the rendered report next to the run's JSON, so the folder can be browsed
// without the testbench.
func (m *Manager) writeHTML(run *report.Run) {
	if m.reportsDir != "" {
		m.mu.Lock()
		snapshot := cloneRun(run)
		m.mu.Unlock()
		page, err := report.HTML(snapshot)
		if err == nil {
			tmp := filepath.Join(m.reportsDir, "."+run.ID+".html.tmp")
			if err = os.WriteFile(tmp, page, 0o644); err == nil {
				err = os.Rename(tmp, filepath.Join(m.reportsDir, run.ID+".html"))
			}
		}
		if err != nil {
			m.publish("run_persist_failed", "", map[string]any{"run_id": run.ID, "detail": err.Error()})
		}
	}
}

// fileID is a report file name without its extension: no path separators, no leading dot.
// Spaces and parentheses are allowed, so a report a browser saved as "<name> (1)" is listed.
var fileID = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_. ()-]{0,127}$`)

// sampleInterval is how often the run samples the wire during a test case.
const sampleInterval = 10 * time.Second

// Entry is one report in the listing.
type Entry struct {
	ID        string    `json:"id"`
	File      string    `json:"file,omitempty"`
	Status    string    `json:"status"`
	StartedAt time.Time `json:"started_at"`
	DurationS float64   `json:"duration_s"`
	Headline  string    `json:"headline"`
	Total     int       `json:"total"`
	Passed    int       `json:"passed"`
	Failed    int       `json:"failed"`
	Skipped   int       `json:"skipped"`
	PeerSKI   string    `json:"peer_ski,omitempty"`
	PeerLabel string    `json:"peer_label,omitempty"`
	Tester    string    `json:"tester,omitempty"`
	Selection string    `json:"selection"`
	Frames    int       `json:"frames"`
}

// List shows what the reports folder holds right now, newest first, plus the active run. A
// file that is not a run record is left out; nothing is cached.
func (m *Manager) List() []Entry {
	entries := []Entry{}
	seen := map[string]bool{}
	add := func(id, file string, r *report.Run) {
		if !seen[id] {
			seen[id] = true
			entries = append(entries, Entry{
				ID: id, File: file, Status: r.Status, StartedAt: r.StartedAt, DurationS: r.DurationS, Headline: r.Summary.Headline,
				Total: r.Summary.Total, Passed: r.Summary.Passed, Failed: r.Summary.Failed, Skipped: r.Summary.Skipped,
				PeerSKI: r.Device.SKI, PeerLabel: r.Device.Label, Tester: r.Tester, Selection: r.Selection.Describe(), Frames: r.FramesCount,
			})
		}
	}
	if activeID := m.ActiveID(); activeID != "" {
		if run, err := m.Get(activeID); err == nil {
			add(activeID, "", run)
		}
	}
	m.mu.Lock()
	memoryIDs := make([]string, 0, len(m.memory))
	for id := range m.memory {
		memoryIDs = append(memoryIDs, id)
	}
	m.mu.Unlock()
	for _, id := range memoryIDs {
		if run, err := m.Get(id); err == nil {
			add(id, "", run)
		}
	}
	if m.reportsDir != "" {
		jsonPaths, _ := filepath.Glob(filepath.Join(m.reportsDir, "*.json"))
		htmlPaths, _ := filepath.Glob(filepath.Join(m.reportsDir, "*.html"))
		for _, p := range append(jsonPaths, htmlPaths...) {
			id := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(p), ".json"), ".html")
			if run, err := m.Get(id); err == nil {
				add(id, filepath.Base(p), run)
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].StartedAt.After(entries[j].StartedAt) })
	return entries
}

// persistLocked writes the run atomically. A run whose file cannot be written stays in
// memory, so its result is still served. Must be called with m.mu held.
func (m *Manager) persistLocked(run *report.Run) {
	data, err := json.MarshalIndent(run, "", " ")
	if err == nil && m.reportsDir == "" {
		m.memory[run.ID] = data
	} else if err == nil {
		err = os.MkdirAll(m.reportsDir, 0o755)
		if err == nil {
			tmp := filepath.Join(m.reportsDir, "."+run.ID+".json.tmp")
			err = os.WriteFile(tmp, data, 0o644)
			if err == nil {
				err = os.Rename(tmp, filepath.Join(m.reportsDir, run.ID+".json"))
			}
		}
	}
	if err != nil {
		if data != nil {
			m.memory[run.ID] = data
		}
		m.publish("run_persist_failed", "", map[string]any{"run_id": run.ID, "detail": err.Error()})
	}
}

func cloneRun(run *report.Run) *report.Run {
	clone := &report.Run{}
	if data, err := json.Marshal(run); err == nil {
		_ = json.Unmarshal(data, clone)
	}
	return clone
}
