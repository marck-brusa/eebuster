// Package report holds the record of one test run and renders it: a self-contained HTML
// report (which prints to PDF), JUnit XML for CI, CSV and an Excel workbook. Every format is
// rendered from the same Run, so they never disagree.
package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/marck-brusa/eebuster/internal/scenario"
	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// Schema is the version of the Run JSON shape. It changes only when a field is removed or
// changes meaning; added fields keep it.
const Schema = 1

// Run statuses.
const (
	StatusRunning   = "running"
	StatusPassed    = "passed"
	StatusFailed    = "failed"
	StatusAborted   = "aborted"
	StatusCancelled = "cancelled"
)

// Disclaimer closes every rendered report.
const Disclaimer = "This report records what the testbench observed on the EEBUS wire and through readback. " +
	"It is not an EEBUS certification or conformance statement."

// Run is the complete record of one test run.
type Run struct {
	Schema     int        `json:"report_schema"`
	ID         string     `json:"id"`
	Status     string     `json:"status"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	DurationS  float64    `json:"duration_s"`
	Tester     string     `json:"tester,omitempty"`
	Notes      string     `json:"notes,omitempty"`

	Selection  Selection  `json:"selection"`
	Testbench  Testbench  `json:"testbench"`
	Device     Device     `json:"device"`
	Parameters Parameters `json:"parameters"`
	Library    Library    `json:"library"`

	VehiclesStart   []map[string]any `json:"vehicles_start"`
	VehiclesEnd     []map[string]any `json:"vehicles_end"`
	ConditionsStart *Conditions      `json:"conditions_start,omitempty"`
	ConditionsEnd   *Conditions      `json:"conditions_end,omitempty"`

	Summary     Summary          `json:"summary"`
	UseCases    []UseCaseSummary `json:"use_cases"`
	Checks      []Check          `json:"checks"`
	Conformance Conformance      `json:"conformance"`
	Notices     []string         `json:"notices"`
	Progress    *Progress        `json:"progress,omitempty"`

	TestCases []scenario.ScenarioResult `json:"test_cases"`

	Frames        []map[string]any `json:"frames,omitempty"`
	FramesCount   int              `json:"frames_count,omitempty"`
	FramesDropped int              `json:"frames_dropped,omitempty"`
}

// Selection is which test cases a run was asked to execute. Empty lists mean no restriction.
type Selection struct {
	ReadOnly           bool     `json:"read_only,omitempty"`
	UseCases           []string `json:"use_cases,omitempty"`
	Risks              []string `json:"risks,omitempty"`
	IDs                []string `json:"ids,omitempty"`
	IncludeLongRunning bool     `json:"include_long_running,omitempty"`
	IncludeFrames      bool     `json:"include_frames,omitempty"`
	// IncludeUnadvertised keeps the test cases of use cases the device does not advertise.
	// Without it they are left out, unless test cases or use cases are named explicitly.
	IncludeUnadvertised bool `json:"include_unadvertised,omitempty"`
}

// Describe is a one-line description of the selection.
func (s Selection) Describe() string {
	var parts []string
	if len(s.IDs) > 0 {
		parts = append(parts, "test cases "+strings.Join(s.IDs, ", "))
	}
	if s.ReadOnly {
		parts = append(parts, "read-only test cases")
	}
	if len(s.UseCases) > 0 {
		parts = append(parts, "use cases "+strings.Join(s.UseCases, ", "))
	}
	if len(s.Risks) > 0 {
		parts = append(parts, "risk "+strings.Join(s.Risks, ", "))
	}
	text := "complete library"
	if len(parts) > 0 {
		text = strings.Join(parts, "; ")
	}
	if s.IncludeLongRunning {
		text += ", including long-running test cases"
	}
	if s.IncludeUnadvertised {
		text += ", including use cases the device does not advertise"
	}
	return text
}

// Testbench identifies the tool that ran the test.
type Testbench struct {
	Version     string            `json:"version"`
	Modules     map[string]string `json:"modules,omitempty"`
	GoVersion   string            `json:"go_version,omitempty"`
	Host        string            `json:"host,omitempty"`
	Platform    string            `json:"platform,omitempty"`
	SKI         string            `json:"ski,omitempty"`
	ShipPort    int               `json:"ship_port,omitempty"`
	NetworkMode string            `json:"network_mode,omitempty"`
	Interface   string            `json:"interface,omitempty"`
	Announced   []string          `json:"announced_addresses,omitempty"`
	API         string            `json:"api,omitempty"`
	Simulators  []string          `json:"simulated_devices,omitempty"`
}

// Device identifies the device under test.
type Device struct {
	Peer          string               `json:"peer,omitempty"`
	Label         string               `json:"label,omitempty"`
	SKI           string               `json:"ski"`
	Connected     bool                 `json:"connected"`
	ShipID        string               `json:"ship_id,omitempty"`
	Name          string               `json:"name,omitempty"`
	Brand         string               `json:"brand,omitempty"`
	Model         string               `json:"model,omitempty"`
	Serial        string               `json:"serial,omitempty"`
	Type          string               `json:"type,omitempty"`
	Host          string               `json:"host,omitempty"`
	Port          int                  `json:"port,omitempty"`
	Path          string               `json:"path,omitempty"`
	Addresses     []string             `json:"addresses,omitempty"`
	AddressSource string               `json:"address_source,omitempty"`
	DeviceAddress string               `json:"device_address,omitempty"`
	DeviceType    string               `json:"device_type,omitempty"`
	FeatureSet    string               `json:"feature_set,omitempty"`
	Entities      []Entity             `json:"entities"`
	UseCases      []AdvertisedUseCase  `json:"use_cases"`
	Manufacturer  []EntityManufacturer `json:"manufacturer"`
	EVSE          map[string]any       `json:"evse,omitempty"`
	Errors        []string             `json:"errors,omitempty"`
}

type Entity struct {
	Address []uint `json:"address"`
	Type    string `json:"type"`
}

type AdvertisedUseCase struct {
	Name      string `json:"name"`
	Acronym   string `json:"acronym,omitempty"`
	Title     string `json:"title,omitempty"`
	Actor     string `json:"actor,omitempty"`
	Version   string `json:"version,omitempty"`
	Scenarios []uint `json:"scenarios"`
	Address   []uint `json:"address,omitempty"`
	Available bool   `json:"available"`
}

// EntityManufacturer is the device classification an entity reports.
type EntityManufacturer struct {
	Entity     []uint            `json:"entity"`
	EntityType string            `json:"entity_type,omitempty"`
	Data       map[string]string `json:"data"`
}

// Conditions is the state of the device at the start or end of a run.
type Conditions struct {
	At                  time.Time `json:"at"`
	Connected           bool      `json:"connected"`
	VehiclesConnected   int       `json:"vehicles_connected"`
	VehiclesCharging    int       `json:"vehicles_charging"`
	ConsumptionW        *float64  `json:"consumption_w,omitempty"`
	LimitActive         *bool     `json:"limit_active,omitempty"`
	LimitW              *float64  `json:"limit_w,omitempty"`
	FailsafeW           *float64  `json:"failsafe_w,omitempty"`
	FailsafeDuration    string    `json:"failsafe_duration,omitempty"`
	HeartbeatWithin     *bool     `json:"heartbeat_within_duration,omitempty"`
	HeartbeatTimeoutS   *float64  `json:"heartbeat_timeout_s,omitempty"`
	TraceSeq            int64     `json:"trace_seq"`
	EventSeq            int64     `json:"event_seq"`
	ConformanceErrors   int       `json:"conformance_errors"`
	ConformanceWarnings int       `json:"conformance_warnings"`
}

// Parameters are the device-specific values the test cases were instantiated with.
type Parameters struct {
	Values     map[string]any `json:"values"`
	Configured []string       `json:"configured,omitempty"`
	Derived    []string       `json:"derived,omitempty"`
	Notes      []string       `json:"notes,omitempty"`
}

// Library identifies the scenario files a run used. Two runs with the same hash ran the same
// criteria.
type Library struct {
	Dir    string `json:"dir"`
	Files  int    `json:"files"`
	SHA256 string `json:"sha256"`
}

type Counts struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

func (c *Counts) add(status string) {
	c.Total++
	switch status {
	case scenario.StatusPassed:
		c.Passed++
	case scenario.StatusFailed:
		c.Failed++
	default:
		c.Skipped++
	}
}

type Summary struct {
	Counts
	NotRun          int               `json:"not_run"`
	SkippedByReason map[string]int    `json:"skipped_by_reason"`
	ByRisk          map[string]Counts `json:"by_risk"`
	Headline        string            `json:"headline"`
}

// UseCaseSummary is one use case in the run: what the device advertises and how its test
// cases ended. The test cases of no use case are listed under CommonGroup.
type UseCaseSummary struct {
	Acronym    string        `json:"acronym"`
	Name       string        `json:"name,omitempty"`
	Title      string        `json:"title"`
	Document   string        `json:"document,omitempty"`
	Advertised bool          `json:"advertised"`
	Actor      string        `json:"actor,omitempty"`
	Version    string        `json:"version,omitempty"`
	Scenarios  []ScenarioRow `json:"scenarios,omitempty"`
	Counts
}

// ScenarioRow is one scenario of a use case: its level for the device, and whether the
// device advertises it.
type ScenarioRow struct {
	Number     uint   `json:"number"`
	Title      string `json:"title"`
	Level      string `json:"level"`
	Advertised bool   `json:"advertised"`
}

// Check is a run-level verdict computed over the whole run rather than one test case.
type Check struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
	Detail string `json:"detail"`
}

// Conformance aggregates the wire conformance findings of the run window.
type Conformance struct {
	Frames        int         `json:"frames"`
	ErrorFrames   int         `json:"error_frames"`
	WarningFrames int         `json:"warning_frames"`
	Rules         []RuleCount `json:"rules"`
	// Overrun is set when more frames arrived between two samples than the trace keeps, so
	// some were never seen and the counts are incomplete.
	Overrun bool `json:"overrun,omitempty"`
}

type RuleCount struct {
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Count    int    `json:"count"`
	SpecRef  string `json:"spec_ref,omitempty"`
	FirstSeq int64  `json:"first_seq"`
	Example  string `json:"example,omitempty"`
}

// Progress is the position of a running run.
type Progress struct {
	Index   int    `json:"index"`
	Total   int    `json:"total"`
	Current string `json:"current,omitempty"`
}

// CommonGroup is the title of the test cases that belong to no use case.
const CommonGroup = "Common"

// GroupOf is the group a test case is listed under.
func GroupOf(tc scenario.ScenarioResult) string {
	group := tc.UseCase
	if group == "" {
		group = CommonGroup
	}
	return group
}

// Summarize recomputes the summary, the headline and the use-case table from the test cases
// and the device's advertisement.
func (r *Run) Summarize() {
	summary := Summary{SkippedByReason: map[string]int{}, ByRisk: map[string]Counts{}}
	for _, tc := range r.TestCases {
		summary.add(tc.Status)
		if tc.Status == scenario.StatusSkipped {
			reason := tc.Reason
			if reason == "" {
				reason = "unspecified"
			}
			summary.SkippedByReason[reason]++
			if reason == scenario.ReasonNotRun {
				summary.NotRun++
			}
		}
		risk := tc.Risk
		if risk == "" {
			risk = "read-only"
		}
		counts := summary.ByRisk[risk]
		counts.add(tc.Status)
		summary.ByRisk[risk] = counts
	}
	summary.Headline = r.headline(summary)
	r.Summary = summary
	r.UseCases = r.useCaseTable()
}

func (r *Run) headline(s Summary) string {
	text := fmt.Sprintf("%d test %s: %d passed, %d failed, %d skipped.", s.Total, plural(s.Total, "case", "cases"), s.Passed, s.Failed, s.Skipped)
	if n := failedChecks(r.Checks); n > 0 {
		text += fmt.Sprintf(" %d run-level %s failed.", n, plural(n, "check", "checks"))
	}
	switch r.Status {
	case StatusRunning:
		if r.Progress != nil {
			text = fmt.Sprintf("Running: %d of %d test cases done. %s", r.Progress.Index, r.Progress.Total, text)
		}
	case StatusCancelled:
		text += fmt.Sprintf(" The run was cancelled; %d not run.", s.NotRun)
	case StatusAborted:
		text += fmt.Sprintf(" The run was aborted; %d not run.", s.NotRun)
	}
	return text
}

func plural(n int, one, many string) string {
	word := many
	if n == 1 {
		word = one
	}
	return word
}

// useCaseTable lists the common group, every catalogued use case the device advertises or
// the run tested, and any other advertised use case.
func (r *Run) useCaseTable() []UseCaseSummary {
	byKey := map[string]*UseCaseSummary{}
	var order []string
	get := func(key string) *UseCaseSummary {
		row, ok := byKey[key]
		if !ok {
			row = &UseCaseSummary{Acronym: key, Title: key}
			if uc, known := ucspec.Lookup(key); known {
				row.Name, row.Title, row.Document = uc.Name, uc.Title, uc.Document
			}
			byKey[key] = row
			order = append(order, key)
		}
		return row
	}
	for _, adv := range r.Device.UseCases {
		key := adv.Acronym
		if uc, known := ucspec.Lookup(adv.Name); known {
			key = uc.Acronym
		} else if key == "" {
			key = adv.Name
		}
		row := get(key)
		row.Advertised = row.Advertised || adv.Available
		row.Actor, row.Version = adv.Actor, adv.Version
		if adv.Title != "" && row.Name == "" {
			row.Title = adv.Title
		}
		row.Scenarios = scenarioRows(key, append(advertisedNumbers(row.Scenarios), adv.Scenarios...))
	}
	for _, tc := range r.TestCases {
		key := tc.UseCase
		if key == "" {
			key = CommonGroup
		}
		row := get(key)
		row.add(tc.Status)
		if row.Scenarios == nil && key != CommonGroup {
			row.Scenarios = scenarioRows(key, nil)
		}
	}
	rows := make([]UseCaseSummary, 0, len(order))
	for _, key := range order {
		rows = append(rows, *byKey[key])
	}
	sort.SliceStable(rows, func(i, j int) bool { return groupRank(rows[i].Acronym) < groupRank(rows[j].Acronym) })
	return rows
}

func groupRank(acronym string) int {
	rank := -1
	if acronym != CommonGroup {
		rank = ucspec.Order(acronym)
	}
	return rank
}

func advertisedNumbers(rows []ScenarioRow) []uint {
	var numbers []uint
	for _, row := range rows {
		if row.Advertised {
			numbers = append(numbers, row.Number)
		}
	}
	return numbers
}

func scenarioRows(key string, advertised []uint) []ScenarioRow {
	have := map[uint]bool{}
	for _, n := range advertised {
		have[n] = true
	}
	var rows []ScenarioRow
	if uc, ok := ucspec.Lookup(key); ok {
		for _, s := range uc.Scenarios {
			rows = append(rows, ScenarioRow{Number: s.Number, Title: s.Title, Level: string(s.Device), Advertised: have[s.Number]})
		}
	}
	return rows
}

// Groups returns the test cases grouped as the report lists them: the common group first,
// then the use cases in catalog order.
func (r *Run) Groups() []Group {
	byKey := map[string]*Group{}
	var keys []string
	for _, tc := range r.TestCases {
		key := GroupOf(tc)
		g, ok := byKey[key]
		if !ok {
			g = &Group{Key: key, Title: key}
			if uc, known := ucspec.Lookup(key); known {
				g.Title = uc.Acronym + " -- " + uc.Title
			}
			byKey[key] = g
			keys = append(keys, key)
		}
		g.TestCases = append(g.TestCases, tc)
		g.Counts.add(tc.Status)
	}
	sort.SliceStable(keys, func(i, j int) bool { return groupRank(keys[i]) < groupRank(keys[j]) })
	groups := make([]Group, 0, len(keys))
	for _, key := range keys {
		groups = append(groups, *byKey[key])
	}
	return groups
}

// Group is one use case's test cases.
type Group struct {
	Key       string
	Title     string
	TestCases []scenario.ScenarioResult
	Counts
}

// FileBase is the file name a rendered report is offered under: run id and device serial or
// SKI, safe for every file system.
func (r *Run) FileBase() string {
	device := r.Device.Serial
	if device == "" {
		device = r.Device.SKI
	}
	if len(device) > 40 {
		device = device[:40]
	}
	base := r.ID
	if device != "" {
		base += "-" + device
	}
	return safeFileName(base)
}

func safeFileName(s string) string {
	var b strings.Builder
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_' || c == '.' {
			b.WriteRune(c)
		} else {
			b.WriteRune('_')
		}
	}
	return b.String()
}

// FirstFailure is the detail of the step that decided a failed or skipped test case.
func FirstFailure(tc scenario.ScenarioResult) string {
	detail := ""
	for _, step := range tc.Steps {
		if detail == "" && step.Status != scenario.StatusPassed {
			detail = step.Detail
			if detail == "" {
				detail = step.Name
			}
		}
	}
	return detail
}

func failedChecks(checks []Check) int {
	n := 0
	for _, c := range checks {
		if c.Status == scenario.StatusFailed {
			n++
		}
	}
	return n
}
