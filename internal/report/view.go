package report

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/marck-brusa/eebuster/internal/scenario"
	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// The view is the run reduced to display strings, so the template only lays them out.

type kv struct {
	Key        string
	Value      string
	Mono       bool
	ScreenOnly bool
}

type card struct {
	Title string
	Rows  []kv
	Note  string
}

type condRow struct {
	Field   string
	Start   string
	End     string
	Changed bool
}

type chip struct {
	Label string
	Class string
	Title string
}

type ucRow struct {
	Acronym    string
	Title      string
	Advertised string
	Detail     string
	Chips      []chip
	Counts
}

type assertView struct {
	Key      string
	Op       string
	Expected string
	Actual   string
	OK       bool
}

type requestView struct {
	Line      string
	Body      string
	Response  string
	Truncated bool
}

type stepView struct {
	Index       int
	Name        string
	Status      string
	Duration    string
	Detail      string
	Request     *requestView
	Assertions  []assertView
	Events      string
	Findings    []string
	MoreFinding int
	Trace       string
}

type caseView struct {
	Anchor     string
	ID         string
	Title      string
	Status     string
	StatusWord string
	Group      string
	Reason     string
	Failure    string
	SpecLine   string
	Duration   string
	Facts      []kv
	Steps      []stepView
	Frames     []frameView
	Search     string
}

// frameView is one wire frame recorded during a test case, as the report lists it.
type frameView struct {
	Seq        int64
	Time       string
	Dir        string
	Function   string
	Classifier string
	Size       int
	Findings   string
	Raw        string
}

type groupView struct {
	Key   string
	Title string
	Cases []caseView
	Counts
}

type view struct {
	ID          string
	Title       string
	Subtitle    string
	Status      string
	StatusWord  string
	Headline    string
	Summary     Summary
	SkipReasons []kv
	Cards       []card
	Vehicles    []card
	Conditions  []condRow
	UseCases    []ucRow
	Checks      []Check
	Groups      []groupView
	GroupKeys   []string
	Conformance Conformance
	Parameters  []kv
	ParamNotes  []string
	Glossary    []kv
	Notices     []string
	Version     string
	Generated   string
	Disclaimer  string
	PrintHeader string
}

const missing = "—"

func buildView(r *Run, generated time.Time) view {
	v := view{
		ID: r.ID, Title: deviceTitle(r), Subtitle: deviceSubtitle(r), Status: r.Status, StatusWord: statusWord(r.Status),
		Headline: r.Summary.Headline, Summary: r.Summary, Checks: r.Checks, Conformance: r.Conformance,
		Notices: r.Notices, Version: r.Testbench.Version, Generated: formatTime(generated), Disclaimer: Disclaimer,
	}
	v.PrintHeader = fmt.Sprintf("Run %s · %s", r.ID, v.Title)
	for _, reason := range sortedKeys(r.Summary.SkippedByReason) {
		v.SkipReasons = append(v.SkipReasons, kv{Key: ReasonText(reason), Value: strconv.Itoa(r.Summary.SkippedByReason[reason])})
	}
	v.Cards = append([]card{deviceCard(r)}, identityCards(r)...)
	v.Cards = append(v.Cards, runCard(r), testbenchCard(r))
	v.Vehicles = vehicleCards(r)
	v.Conditions = conditionRows(r.ConditionsStart, r.ConditionsEnd)
	for _, u := range r.UseCases {
		v.UseCases = append(v.UseCases, useCaseRow(u))
	}
	for _, g := range r.Groups() {
		gv := groupView{Key: g.Key, Title: g.Title, Counts: g.Counts}
		for _, tc := range g.TestCases {
			gv.Cases = append(gv.Cases, buildCase(tc, g.Key, r.Frames))
		}
		v.Groups = append(v.Groups, gv)
		v.GroupKeys = append(v.GroupKeys, g.Key)
	}
	v.Parameters, v.ParamNotes = parameterRows(r.Parameters), r.Parameters.Notes
	v.Glossary = glossary()
	return v
}

func deviceTitle(r *Run) string {
	title := r.Device.Label
	for _, candidate := range []string{strings.TrimSpace(r.Device.Brand + " " + r.Device.Model), r.Device.Name, r.Device.SKI} {
		if title == "" {
			title = candidate
		}
	}
	if title == "" {
		title = "No device"
	}
	return title
}

func deviceSubtitle(r *Run) string {
	var parts []string
	if r.Device.Serial != "" {
		parts = append(parts, "Serial "+r.Device.Serial)
	}
	if sw := softwareRevision(r); sw != "" {
		parts = append(parts, "Software "+sw)
	}
	if r.Device.SKI != "" {
		parts = append(parts, "SKI "+r.Device.SKI)
	}
	return strings.Join(parts, " · ")
}

// softwareRevision is the first software revision any entity of the device reports.
func softwareRevision(r *Run) string {
	revision := ""
	for _, m := range r.Device.Manufacturer {
		if revision == "" {
			revision = m.Data["software_revision"]
		}
	}
	if revision == "" && r.Device.EVSE != nil {
		if identity, ok := r.Device.EVSE["identity"].(map[string]any); ok {
			revision, _ = identity["software_revision"].(string)
		}
	}
	return revision
}

func statusWord(status string) string {
	words := map[string]string{
		StatusRunning: "Running", StatusPassed: "Passed", StatusFailed: "Failed",
		StatusAborted: "Aborted", StatusCancelled: "Cancelled", scenario.StatusSkipped: "Skipped",
	}
	word, ok := words[status]
	if !ok {
		word = status
	}
	return word
}

// ReasonText is the reader-facing wording of a skip reason.
func ReasonText(reason string) string {
	texts := map[string]string{
		scenario.ReasonUseCaseNotAdvertised:  "Use case not advertised",
		scenario.ReasonScenarioNotAdvertised: "Optional scenario not advertised",
		scenario.ReasonCapabilityMissing:     "Testbench capability missing",
		scenario.ReasonPhasesNotDeclared:     "Phases not declared",
		scenario.ReasonPreconditionNotMet:    "Precondition not met",
		scenario.ReasonNotVerifiableOnWire:   "Not verifiable on the wire",
		scenario.ReasonNotRun:                "Not run",
	}
	text, ok := texts[reason]
	if !ok {
		text = reason
	}
	return text
}

func glossary() []kv {
	return []kv{
		{Key: "Passed", Value: "Every step of the test case held."},
		{Key: "Failed", Value: "A step did not hold. The step's detail and the recorded request and response show why."},
		{Key: ReasonText(scenario.ReasonUseCaseNotAdvertised), Value: "The device does not advertise a use case the test case needs. Not counted against the device."},
		{Key: ReasonText(scenario.ReasonScenarioNotAdvertised), Value: "The use case is advertised without a recommended or optional scenario the test case checks. Mandatory scenarios are always tested."},
		{Key: ReasonText(scenario.ReasonPreconditionNotMet), Value: "A condition the test case needs was absent, such as a connected vehicle, a charging session or a device parameter."},
		{Key: ReasonText(scenario.ReasonNotVerifiableOnWire), Value: "The expected result is internal device state that EEBUS does not expose."},
		{Key: ReasonText(scenario.ReasonCapabilityMissing), Value: "The testbench stack cannot perform an operation the test case needs."},
		{Key: ReasonText(scenario.ReasonPhasesNotDeclared), Value: "The device declares its limits on other phases than the test case writes."},
		{Key: ReasonText(scenario.ReasonNotRun), Value: "The run was cancelled or aborted before this test case."},
	}
}

func row(key, value string) kv  { return kv{Key: key, Value: value} }
func mono(key, value string) kv { return kv{Key: key, Value: value, Mono: true} }

// rows keeps only the rows that have a value.
func rows(in ...kv) []kv {
	var out []kv
	for _, r := range in {
		if strings.TrimSpace(r.Value) != "" {
			out = append(out, r)
		}
	}
	return out
}

func deviceCard(r *Run) card {
	d := r.Device
	address := ""
	if d.Host != "" {
		address = d.Host
		if d.Port > 0 {
			address += ":" + strconv.Itoa(d.Port)
		}
		address += d.Path
	}
	c := card{Title: "Device under test", Rows: rows(
		row("Label", d.Label), row("Configured peer", d.Peer), mono("SKI", d.SKI), mono("SHIP id", d.ShipID),
		row("Brand", d.Brand), row("Model", d.Model), row("Serial (announced)", d.Serial), row("Type", d.Type),
		mono("Address", address), mono("IP addresses", strings.Join(d.Addresses, ", ")), row("Address source", d.AddressSource),
		mono("SPINE device", d.DeviceAddress), row("Device type", d.DeviceType), row("Feature set", d.FeatureSet),
		row("Entities", entityList(d.Entities)), row("Connected at start", yesNo(d.Connected)),
	)}
	if len(d.Errors) > 0 {
		c.Note = "Not available: " + strings.Join(d.Errors, "; ")
	}
	return c
}

// identityCards are the manufacturer data of each entity and the charging station's identity.
func identityCards(r *Run) []card {
	var cards []card
	for _, m := range r.Device.Manufacturer {
		title := "Manufacturer data, entity " + entityAddress(m.Entity)
		if m.EntityType != "" {
			title += " " + m.EntityType
		}
		cards = append(cards, card{Title: title, Rows: manufacturerRows("", m.Data)})
	}
	if evse := r.Device.EVSE; evse != nil {
		c := card{Title: "Charging station (EVSECC)"}
		if identity, ok := evse["identity"].(map[string]any); ok {
			strs := map[string]string{}
			for k, val := range identity {
				strs[k] = fmt.Sprint(val)
			}
			c.Rows = manufacturerRows("", strs)
		}
		c.Rows = append(c.Rows, rows(row("Operating state", str(evse["operating_state"])), row("Last error", str(evse["last_error_code"])))...)
		cards = append(cards, c)
	}
	return cards
}

var manufacturerLabels = []struct{ key, label string }{
	{"brand_name", "brand"}, {"vendor_name", "vendor"}, {"vendor_code", "vendor code"}, {"device_name", "device name"},
	{"device_code", "device code"}, {"serial_number", "serial number"}, {"software_revision", "software revision"},
	{"hardware_revision", "hardware revision"}, {"power_source", "power source"},
	{"manufacturer_node_identification", "node identification"}, {"manufacturer_label", "label"},
	{"manufacturer_description", "description"},
}

func manufacturerRows(prefix string, data map[string]string) []kv {
	var out []kv
	for _, l := range manufacturerLabels {
		if value := data[l.key]; value != "" {
			label := strings.ToUpper(l.label[:1]) + l.label[1:]
			if prefix != "" {
				label = prefix + " " + l.label
			}
			out = append(out, kv{Key: label, Value: value, Mono: l.key == "serial_number" || strings.HasSuffix(l.key, "revision")})
		}
	}
	return out
}

func runCard(r *Run) card {
	finished := ""
	if r.FinishedAt != nil {
		finished = formatTime(*r.FinishedAt)
	}
	library := ""
	if r.Library.SHA256 != "" {
		library = fmt.Sprintf("%d files, sha256 %s", r.Library.Files, shortHash(r.Library.SHA256))
	}
	return card{Title: "Run", Rows: rows(
		mono("Run id", r.ID), row("Status", statusWord(r.Status)), row("Started", formatTime(r.StartedAt)),
		row("Finished", finished), row("Duration", formatDuration(r.DurationS)), row("Tester", r.Tester),
		row("Notes", r.Notes), row("Selection", r.Selection.Describe()), mono("Scenario library", library),
		row("Library directory", r.Library.Dir), row("Wire frames", framesSummary(r)),
	)}
}

// framesSummary says how many wire frames the record embeds, and how many the size budget
// left out.
func framesSummary(r *Run) string {
	text := ""
	if r.FramesCount > 0 || r.FramesDropped > 0 {
		text = fmt.Sprintf("%d embedded", r.FramesCount)
		if r.FramesDropped > 0 {
			text += fmt.Sprintf(", %d left out over the size budget", r.FramesDropped)
		}
	}
	return text
}

func testbenchCard(r *Run) card {
	t := r.Testbench
	var modules []string
	for _, name := range sortedKeys(t.Modules) {
		modules = append(modules, name+" "+t.Modules[name])
	}
	return card{Title: "Testbench", Rows: rows(
		mono("Version", t.Version), mono("Stack modules", strings.Join(modules, ", ")), row("Go", t.GoVersion),
		row("Host", t.Host), row("Platform", t.Platform), mono("SKI", t.SKI), row("SHIP port", intString(t.ShipPort)),
		row("Network mode", t.NetworkMode), row("Interface", t.Interface), mono("Announced addresses", strings.Join(t.Announced, ", ")),
		mono("API", t.API), row("Simulated devices", strings.Join(t.Simulators, ", ")),
	)}
}

var vehicleLabels = []struct{ key, label string }{
	{"entity", "Entity"}, {"connected", "Connected"}, {"charge_state", "Charge state"},
	{"identifications", "Identifications"}, {"communication_standard", "Communication standard"},
	{"asymmetric_charging", "Asymmetric charging"}, {"charging_power_limits_w", "Charging power limits (W)"},
	{"in_sleep_mode", "Sleep mode"}, {"phases_connected", "Phases connected"}, {"state_of_charge", "State of charge (%)"},
	{"energy_charged_wh", "Energy charged (Wh)"}, {"current_per_phase_a", "Current per phase (A)"},
	{"power_per_phase_w", "Power per phase (W)"}, {"charge_strategy", "Charge strategy"},
}

func vehicleCards(r *Run) []card {
	var cards []card
	add := func(when string, vehicles []map[string]any) {
		for i, vehicle := range vehicles {
			c := card{Title: fmt.Sprintf("Vehicle %d at %s", i+1, when)}
			for _, l := range vehicleLabels {
				if val, ok := vehicle[l.key]; ok && val != nil {
					c.Rows = append(c.Rows, kv{Key: l.label, Value: vehicleValue(l.key, val), Mono: l.key == "identifications" || l.key == "entity"})
				}
			}
			if m, ok := vehicle["manufacturer"].(map[string]any); ok {
				strs := map[string]string{}
				for k, val := range m {
					strs[k] = fmt.Sprint(val)
				}
				c.Rows = append(c.Rows, manufacturerRows("Manufacturer", strs)...)
			}
			cards = append(cards, c)
		}
	}
	add("start", r.VehiclesStart)
	add("end", r.VehiclesEnd)
	return cards
}

func vehicleValue(key string, v any) string {
	text := formatValue(v)
	if m, ok := v.(map[string]any); ok && key == "charging_power_limits_w" {
		text = fmt.Sprintf("minimum %s, maximum %s, standby %s", formatValue(m["minimum"]), formatValue(m["maximum"]), formatValue(m["standby"]))
	}
	return text
}

func conditionRows(start, end *Conditions) []condRow {
	var out []condRow
	if start != nil || end != nil {
		field := func(name string, get func(*Conditions) string) {
			s, e := "", ""
			if start != nil {
				s = get(start)
			}
			if end != nil {
				e = get(end)
			}
			if s != "" || e != "" {
				out = append(out, condRow{Field: name, Start: orMissing(s), End: orMissing(e), Changed: start != nil && end != nil && s != e && name != "Time"})
			}
		}
		field("Time", func(c *Conditions) string { return formatTime(c.At) })
		field("Connected", func(c *Conditions) string { return yesNo(c.Connected) })
		field("Vehicles connected", func(c *Conditions) string { return strconv.Itoa(c.VehiclesConnected) })
		field("Vehicles charging", func(c *Conditions) string { return strconv.Itoa(c.VehiclesCharging) })
		field("Consumption (W)", func(c *Conditions) string { return floatPtr(c.ConsumptionW) })
		field("Consumption limit active", func(c *Conditions) string { return boolPtr(c.LimitActive) })
		field("Consumption limit (W)", func(c *Conditions) string { return floatPtr(c.LimitW) })
		field("Failsafe limit (W)", func(c *Conditions) string { return floatPtr(c.FailsafeW) })
		field("Failsafe duration", func(c *Conditions) string { return c.FailsafeDuration })
		field("Device heartbeat within timeout", func(c *Conditions) string { return boolPtr(c.HeartbeatWithin) })
		field("Device heartbeat timeout (s)", func(c *Conditions) string { return floatPtr(c.HeartbeatTimeoutS) })
		field("Trace position", func(c *Conditions) string { return strconv.FormatInt(c.TraceSeq, 10) })
		field("Frames with conformance errors", func(c *Conditions) string { return strconv.Itoa(c.ConformanceErrors) })
		field("Frames with warnings only", func(c *Conditions) string { return strconv.Itoa(c.ConformanceWarnings) })
	}
	return out
}

func useCaseRow(u UseCaseSummary) ucRow {
	r := ucRow{Acronym: u.Acronym, Title: u.Title, Counts: u.Counts, Advertised: "no"}
	if u.Acronym == CommonGroup {
		r.Title, r.Advertised = "Connection, discovery and wire format", ""
	} else if u.Advertised {
		r.Advertised = "yes"
		r.Detail = strings.TrimSpace(u.Actor + " " + u.Version)
	}
	for _, s := range u.Scenarios {
		class := "chip-off"
		switch {
		case s.Advertised:
			class = "chip-on"
		case u.Advertised && s.Level == string(ucspec.Mandatory):
			class = "chip-missing"
		}
		title := fmt.Sprintf("Scenario %d %s: %s for the device, %s", s.Number, s.Title, levelName(s.Level), advertisedWord(s.Advertised))
		r.Chips = append(r.Chips, chip{Label: fmt.Sprintf("%d %s", s.Number, s.Level), Class: class, Title: title})
	}
	return r
}

func levelName(level string) string {
	names := map[string]string{"M": "mandatory", "R": "recommended", "O": "optional", "-": "not applicable"}
	name, ok := names[level]
	if !ok {
		name = level
	}
	return name
}

func advertisedWord(advertised bool) string {
	word := "not advertised"
	if advertised {
		word = "advertised"
	}
	return word
}

func buildCase(tc scenario.ScenarioResult, group string, frames []map[string]any) caseView {
	title := tc.Covers
	if title == "" {
		title = tc.Name
	}
	c := caseView{
		Anchor: "tc-" + tc.ID, ID: tc.ID, Title: title, Status: tc.Status, StatusWord: statusWord(tc.Status),
		Group: group, Duration: formatDuration(tc.DurationS), SpecLine: specLine(tc.Spec),
	}
	if c.ID == "" {
		c.ID, c.Anchor = tc.Name, "tc-"+tc.Name
	}
	if tc.Status == scenario.StatusSkipped {
		c.Reason = ReasonText(tc.Reason)
	}
	if tc.Status != scenario.StatusPassed {
		c.Failure = FirstFailure(tc)
	}
	name := ""
	if tc.Name != c.ID {
		name = tc.Name
	}
	c.Facts = rows(
		row("Goal", tc.Goal), row("Hint", tc.Hint), row("Name", name),
	)
	c.Facts = append(c.Facts, specFacts(tc.Spec)...)
	c.Facts = append(c.Facts, rows(
		row("Risk", tc.Risk), row("Started", formatUnix(tc.StartedAt)), row("Duration", formatDuration(tc.DurationS)),
		mono("Trace frames", seqRange(tc.Trace)), row("Conformance findings", intString(tc.FindingsTotal)),
		row("Long-running", trueOnly(tc.LongRunning)), row("Cleanup", trueOnly(tc.Cleanup)),
	)...)
	if tc.Description != "" {
		c.Facts = append(c.Facts, kv{Key: "Description", Value: tc.Description, ScreenOnly: true})
	}
	for i, step := range tc.Steps {
		c.Steps = append(c.Steps, buildStep(i+1, step))
	}
	c.Frames = framesIn(frames, tc.Trace)
	c.Search = strings.ToLower(strings.Join([]string{tc.ID, tc.Name, tc.Covers, c.SpecLine, group, c.Failure}, " "))
	return c
}

// framesIn picks the embedded wire frames of one test case: those recorded between the
// first and the last frame sequence of its trace window.
func framesIn(frames []map[string]any, window *scenario.SeqRange) []frameView {
	var out []frameView
	if window != nil && window.To >= window.From {
		for _, f := range frames {
			seq := int64(frameNumber(f["seq"]))
			if seq >= window.From && seq <= window.To {
				out = append(out, frameView{
					Seq: seq, Time: frameTime(f).Format("15:04:05.000"), Dir: frameDirection(frameString(f["dir"])),
					Function: frameString(f["function"]), Classifier: frameString(f["classifier"]),
					Size: int(frameNumber(f["size"])), Findings: frameFindings(f["findings"]), Raw: frameString(f["raw"]),
				})
			}
		}
	}
	return out
}

func frameDirection(dir string) string {
	text := "device → testbench"
	if dir == "send" {
		text = "testbench → device"
	}
	return text
}

func frameFindings(v any) string {
	var parts []string
	if list, ok := v.([]any); ok {
		for _, item := range list {
			if m, ok := item.(map[string]any); ok {
				parts = append(parts, fmt.Sprintf("%s (%s)", frameString(m["rule"]), frameString(m["severity"])))
			}
		}
	}
	return strings.Join(parts, "; ")
}

func specLine(spec *scenario.Spec) string {
	var parts []string
	if spec != nil {
		if spec.UseCase != "" && spec.Scenario > 0 {
			parts = append(parts, fmt.Sprintf("%s scenario %d", spec.UseCase, spec.Scenario))
		} else if spec.UseCase != "" {
			parts = append(parts, spec.UseCase)
		}
		if spec.TestCase != "" {
			parts = append(parts, spec.TestCase)
		}
		if len(spec.Requirements) > 0 {
			parts = append(parts, strings.Join(spec.Requirements, ", "))
		}
	}
	return strings.Join(parts, " · ")
}

func specFacts(spec *scenario.Spec) []kv {
	var out []kv
	if spec != nil {
		scenarioText := ""
		if spec.Scenario > 0 {
			scenarioText = fmt.Sprintf("%d %s", spec.Scenario, spec.ScenarioTitle)
			if spec.DeviceLevel != "" {
				scenarioText += " (" + levelName(spec.DeviceLevel) + " for the device)"
			}
		}
		where := strings.TrimSpace(strings.Join(nonEmpty(spec.Document, prefixed("section ", spec.Section), spec.Table), ", "))
		out = rows(
			row("Specification", where), row("Scenario", scenarioText),
			mono("Requirements", strings.Join(spec.Requirements, ", ")), mono("Test case id", spec.TestCase),
			row("Verification", verificationText(spec.Verification)),
		)
	}
	return out
}

func verificationText(v string) string {
	texts := map[string]string{
		scenario.VerificationWire:     "observed on the wire",
		scenario.VerificationReadback: "written, then read back from the device",
		scenario.VerificationProbe:    "needs a device probe",
	}
	text, ok := texts[v]
	if !ok {
		text = v
	}
	return text
}

func buildStep(index int, s scenario.StepResult) stepView {
	v := stepView{Index: index, Name: s.Name, Status: s.Status, Duration: formatDuration(s.DurationS), Detail: s.Detail}
	if s.Trace != nil && s.Trace.To >= s.Trace.From {
		v.Trace = seqRange(s.Trace)
	}
	if s.Request != nil {
		req := &requestView{Line: strings.TrimSpace(fmt.Sprintf("%s %s", s.Request.Method, s.Request.Path)), Response: prettyJSON(s.Request.Response), Truncated: s.Request.Truncated}
		if s.Request.Status > 0 {
			req.Line += fmt.Sprintf(" → HTTP %d", s.Request.Status)
		}
		if s.Request.Body != nil {
			body, _ := json.MarshalIndent(s.Request.Body, "", "  ")
			req.Body = string(body)
		}
		v.Request = req
	}
	for _, a := range s.Assertions {
		expected := formatValue(a.Expected)
		if a.Expected == nil {
			expected = ""
		}
		v.Assertions = append(v.Assertions, assertView{Key: a.Key, Op: strings.ReplaceAll(a.Op, "_", " "), Expected: expected, Actual: formatValue(a.Actual), OK: a.OK})
	}
	var events []string
	for _, e := range s.Events {
		events = append(events, fmt.Sprintf("%s (#%d)", e.Event, e.Seq))
	}
	v.Events = strings.Join(events, ", ")
	for _, f := range s.Findings {
		v.Findings = append(v.Findings, fmt.Sprintf("#%d %s %s %s: %s", f.Seq, f.Dir, f.Function, f.Rule, f.Message))
	}
	v.MoreFinding = s.FindingsTotal - len(s.Findings)
	return v
}

func parameterRows(p Parameters) []kv {
	derived := map[string]bool{}
	for _, name := range p.Derived {
		derived[name] = true
	}
	var out []kv
	for _, name := range sortedKeys(p.Values) {
		source := "configured"
		if derived[name] {
			source = "derived from the device"
		}
		out = append(out, kv{Key: name, Value: formatValue(p.Values[name]) + "  (" + source + ")", Mono: true})
	}
	return out
}

// Formatting helpers.

func formatTime(t time.Time) string {
	text := ""
	if !t.IsZero() {
		text = t.Format("2006-01-02 15:04:05 -07:00")
		if _, offset := t.Zone(); offset != 0 {
			text += " (" + t.UTC().Format("15:04:05") + " UTC)"
		}
	}
	return text
}

func formatUnix(seconds float64) string {
	text := ""
	if seconds > 0 {
		text = formatTime(time.Unix(0, int64(seconds*1e9)))
	}
	return text
}

func formatDuration(seconds float64) string {
	text := ""
	switch {
	case seconds <= 0:
		text = "0 s"
	case seconds < 10:
		text = strconv.FormatFloat(seconds, 'f', 2, 64) + " s"
	case seconds < 120:
		text = strconv.FormatFloat(seconds, 'f', 0, 64) + " s"
	default:
		d := time.Duration(seconds * float64(time.Second)).Round(time.Second)
		text = fmt.Sprintf("%d min %02d s", int(d.Minutes()), int(d.Seconds())%60)
	}
	return text
}

// formatValue renders a JSON value compactly: numbers without trailing zeros, lists and
// objects as JSON, and a dash for null.
func formatValue(v any) string {
	text := missing
	switch x := v.(type) {
	case nil:
	case string:
		text = x
	case float64:
		text = strconv.FormatFloat(math.Round(x*1000)/1000, 'f', -1, 64)
	case bool:
		text = yesNo(x)
	case []any:
		parts := make([]string, len(x))
		for i, item := range x {
			parts[i] = formatValue(item)
		}
		text = "[" + strings.Join(parts, ", ") + "]"
	default:
		if encoded, err := json.Marshal(x); err == nil {
			text = string(encoded)
		} else {
			text = fmt.Sprint(x)
		}
	}
	return text
}

func prettyJSON(raw string) string {
	text := raw
	var decoded any
	if json.Unmarshal([]byte(raw), &decoded) == nil {
		if encoded, err := json.MarshalIndent(decoded, "", "  "); err == nil {
			text = string(encoded)
		}
	}
	return text
}

func yesNo(b bool) string {
	text := "no"
	if b {
		text = "yes"
	}
	return text
}

func trueOnly(b bool) string {
	text := ""
	if b {
		text = "yes"
	}
	return text
}

func floatPtr(f *float64) string {
	text := ""
	if f != nil {
		text = strconv.FormatFloat(*f, 'f', -1, 64)
	}
	return text
}

func boolPtr(b *bool) string {
	text := ""
	if b != nil {
		text = yesNo(*b)
	}
	return text
}

func intString(n int) string {
	text := ""
	if n != 0 {
		text = strconv.Itoa(n)
	}
	return text
}

func orMissing(s string) string {
	if s == "" {
		s = missing
	}
	return s
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func seqRange(r *scenario.SeqRange) string {
	text := ""
	if r != nil && r.To >= r.From {
		text = fmt.Sprintf("#%d – #%d", r.From, r.To)
	} else if r != nil {
		text = "none"
	}
	return text
}

func shortHash(h string) string {
	if len(h) > 16 {
		h = h[:16]
	}
	return h
}

func entityAddress(address []uint) string {
	parts := make([]string, len(address))
	for i, n := range address {
		parts[i] = strconv.FormatUint(uint64(n), 10)
	}
	return "[" + strings.Join(parts, ",") + "]"
}

func entityList(entities []Entity) string {
	parts := make([]string, 0, len(entities))
	for _, e := range entities {
		parts = append(parts, entityAddress(e.Address)+" "+e.Type)
	}
	return strings.Join(parts, ", ")
}

func nonEmpty(values ...string) []string {
	var out []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func prefixed(prefix, value string) string {
	text := ""
	if value != "" {
		text = prefix + value
	}
	return text
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
