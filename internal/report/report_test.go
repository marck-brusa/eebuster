package report

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"encoding/xml"
	"io"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/marck-brusa/eebuster/internal/scenario"
)

// Synthetic identifiers only: this repository is public.
const fixtureSKI = "0123456789abcdef0123456789abcdef01234567"

func fixtureRun() *Run {
	started := time.Date(2026, 1, 9, 10, 15, 0, 0, time.UTC)
	finished := started.Add(95 * time.Second)
	consumption := 4100.0
	r := &Run{
		Schema: Schema, ID: "20260109T101500Z-3f9a", Status: StatusFailed, StartedAt: started, FinishedAt: &finished, DurationS: 95,
		Tester: "Test Engineer <script>", Notes: "bench & lab",
		Testbench: Testbench{Version: "1.0.0-rc14", Modules: map[string]string{"github.com/enbility/eebus-go": "v0.7.1"}, SKI: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},
		Device: Device{
			Peer: "device-under-test", Label: "Bench device", SKI: fixtureSKI, Serial: "SN-0001", Host: "192.0.2.10", Port: 4712, Path: "/ship/",
			Connected: true, Entities: []Entity{{Address: []uint{1}, Type: "EVSE"}},
			UseCases: []AdvertisedUseCase{
				{Name: "limitationOfPowerConsumption", Acronym: "LPC", Actor: "ControllableSystem", Version: "1.0.0", Scenarios: []uint{1, 2, 3, 4}, Available: true},
				{Name: "overloadProtectionByEvChargingCurrentCurtailment", Acronym: "OPEV", Actor: "EV", Version: "1.0.1", Scenarios: []uint{1, 2, 2}, Available: true},
			},
			Manufacturer: []EntityManufacturer{{Entity: []uint{1}, EntityType: "EVSE", Data: map[string]string{"software_revision": "4.2.0", "brand_name": "Example"}}},
		},
		VehiclesStart:   []map[string]any{{"entity": []any{1.0, 1.0}, "identifications": []any{"02:00:00:00:00:01"}}},
		ConditionsStart: &Conditions{At: started, Connected: true, ConsumptionW: &consumption},
		ConditionsEnd:   &Conditions{At: finished, Connected: true},
		Checks:          []Check{{ID: "conformance", Title: "Wire conformance", Status: "passed", Detail: "no errors"}},
		Notices:         []string{},
		Frames: []map[string]any{
			{"seq": 41.0, "ts": float64(started.Unix()) + 1.5, "dir": "send", "ski": fixtureSKI, "function": "loadControlLimitListData", "classifier": "write", "size": 120.0, "findings": []any{}, "raw": `{"datagram":{"header":{"msgCounter":7}}}`},
			{"seq": 42.0, "ts": float64(started.Unix()) + 1.7, "dir": "recv", "ski": fixtureSKI, "function": "loadControlLimitListData", "classifier": "reply", "size": 80.0,
				"findings": []any{map[string]any{"rule": "spine.scaled_number", "severity": "warning", "message": "scale missing"}}, "raw": `{"datagram":{"header":{"msgCounterReference":7}}}`},
		},
		FramesCount: 2,
		TestCases: []scenario.ScenarioResult{
			{
				ID: "lpc-basic-limit", Name: "lpc-basic-limit", Status: scenario.StatusPassed, UseCase: "LPC", Risk: "live-control",
				Covers: "LPC scenario 1: a limit written and read back.", DurationS: 1.2, Trace: &scenario.SeqRange{From: 40, To: 42},
				Spec: &scenario.Spec{UseCase: "LPC", Scenario: 1, TestCase: "ATC_COM_PT_CSConnection_002", Verification: scenario.VerificationReadback},
				Steps: []scenario.StepResult{{
					Name: "assert /api/v1/lpc/x/limit", Status: scenario.StatusPassed,
					Request:    &scenario.RequestEvidence{Method: "GET", Path: "/api/v1/lpc/x/limit", Status: 200, Response: `{"value_w":4200}`},
					Assertions: []scenario.Comparison{{Key: "value_w", Op: "equals", Expected: 4200, Actual: 4200.0, OK: true}},
				}},
			},
			{
				ID: "opev-scenarios-advertised", Name: "opev-scenarios-advertised", Status: scenario.StatusFailed, UseCase: "OPEV", Risk: "read-only",
				Steps: []scenario.StepResult{{Name: "scenarios_advertised OPEV", Status: scenario.StatusFailed, Detail: "mandatory scenario 3 not advertised"}},
			},
			{
				ID: "mpc-live-power", Name: "mpc-live-power", Status: scenario.StatusSkipped, Reason: scenario.ReasonUseCaseNotAdvertised, UseCase: "MPC",
				Steps: []scenario.StepResult{{Name: "requirements", Status: scenario.StatusSkipped, Detail: "peer does not advertise monitoringOfPowerConsumption"}},
			},
			{ID: "smoke-pairing", Name: "smoke-pairing", Status: scenario.StatusPassed, Steps: []scenario.StepResult{{Name: "wait_connected", Status: scenario.StatusPassed}}},
		},
	}
	r.Summarize()
	return r
}

func TestSummarizeCountsAndHeadline(t *testing.T) {
	r := fixtureRun()
	if r.Summary.Total != 4 || r.Summary.Passed != 2 || r.Summary.Failed != 1 || r.Summary.Skipped != 1 {
		t.Fatalf("summary = %+v", r.Summary)
	}
	if r.Summary.Headline != "4 test cases: 2 passed, 1 failed, 1 skipped." {
		t.Errorf("headline = %q", r.Summary.Headline)
	}
	if r.Summary.SkippedByReason[scenario.ReasonUseCaseNotAdvertised] != 1 {
		t.Errorf("skipped by reason = %v", r.Summary.SkippedByReason)
	}
	var keys []string
	for _, u := range r.UseCases {
		keys = append(keys, u.Acronym)
	}
	if strings.Join(keys, ",") != "Common,LPC,MPC,OPEV" {
		t.Errorf("use case order = %v", keys)
	}
	for _, u := range r.UseCases {
		if u.Acronym == "OPEV" {
			if len(u.Scenarios) != 3 || u.Scenarios[2].Advertised || u.Scenarios[2].Level != "M" {
				t.Errorf("OPEV scenarios = %+v", u.Scenarios)
			}
		}
		if u.Acronym == "MPC" && u.Advertised {
			t.Error("MPC is not advertised")
		}
	}
}

func TestHTMLIsSelfContainedAndCarriesTheRun(t *testing.T) {
	r := fixtureRun()
	page, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	text := string(page)
	for _, want := range []string{r.Summary.Headline, "lpc-basic-limit", "opev-scenarios-advertised", "mpc-live-power", "SN-0001", "4.2.0", Disclaimer, "@media print",
		"Wire frames: 2 recorded during this test case", "spine.scaled_number (warning)", "testbench → device"} {
		if !strings.Contains(text, want) {
			t.Errorf("HTML lacks %q", want)
		}
	}
	if regexp.MustCompile(`(?i)(src|href)\s*=\s*["']?\s*(https?:)?//`).MatchString(text) {
		t.Error("HTML references an external resource")
	}
	if strings.Contains(text, "<link") || strings.Contains(text, "Test Engineer <script>") {
		t.Error("HTML has a link element or unescaped data")
	}
	if strings.Contains(text, "PLACEHOLDER") {
		t.Error("a placeholder was not replaced")
	}
	start := strings.Index(text, `id="report-data">`) + len(`id="report-data">`)
	end := strings.Index(text[start:], "</script>")
	var embedded Run
	if err := json.Unmarshal([]byte(text[start:start+end]), &embedded); err != nil {
		t.Fatalf("embedded JSON: %v", err)
	}
	if embedded.ID != r.ID || len(embedded.TestCases) != 4 || embedded.Tester != r.Tester || embedded.Summary.Headline != r.Summary.Headline {
		t.Errorf("embedded run differs: %+v", embedded.Summary)
	}
	if len(page) > 16<<20 {
		t.Errorf("HTML is %d bytes", len(page))
	}
}

func TestJUnitIsWellFormedWithProperties(t *testing.T) {
	out := JUnit(fixtureRun())
	if err := wellFormed(out); err != nil {
		t.Fatalf("JUnit: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{`name="device_ski" value="` + fixtureSKI + `"`, `classname="LPC.lpc-basic-limit"`, `classname="Common.smoke-pairing"`, "<failure", "<skipped"} {
		if !strings.Contains(text, want) {
			t.Errorf("JUnit lacks %s", want)
		}
	}
}

func TestCSVTables(t *testing.T) {
	r := fixtureRun()
	for which, rows := range map[string]int{"cases": 4, "steps": 4} {
		out, err := CSV(r, which)
		if err != nil {
			t.Fatal(err)
		}
		records, err := csv.NewReader(bytes.NewReader(out)).ReadAll()
		if err != nil || len(records) != rows+1 {
			t.Fatalf("%s: %d records, err %v", which, len(records), err)
		}
	}
	out, _ := CSV(r, "cases")
	if !strings.Contains(string(out), "ATC_COM_PT_CSConnection_002") {
		t.Error("cases CSV lacks the test case id")
	}
}

func TestXLSXPackage(t *testing.T) {
	out, err := XLSX(fixtureRun())
	if err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(out), int64(len(out)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
		rc, _ := f.Open()
		data, _ := io.ReadAll(rc)
		rc.Close()
		if err := wellFormed(data); err != nil {
			t.Errorf("%s: %v", f.Name, err)
		}
		if f.Name == "xl/worksheets/sheet1.xml" && !strings.Contains(string(data), "4 test cases: 2 passed") {
			t.Error("summary sheet lacks the headline")
		}
	}
	for _, want := range []string{"[Content_Types].xml", "xl/workbook.xml", "xl/styles.xml", "xl/worksheets/sheet5.xml"} {
		if !names[want] {
			t.Errorf("workbook lacks %s", want)
		}
	}
}

func TestColumnName(t *testing.T) {
	for index, want := range map[int]string{0: "A", 25: "Z", 26: "AA", 27: "AB", 701: "ZZ", 702: "AAA"} {
		if got := columnName(index); got != want {
			t.Errorf("columnName(%d) = %s, want %s", index, got, want)
		}
	}
}

func TestFileBaseIsSafe(t *testing.T) {
	r := &Run{ID: "20260109T101500Z-3f9a", Device: Device{Serial: "A/B C"}}
	if got := r.FileBase(); got != "20260109T101500Z-3f9a-A_B_C" {
		t.Errorf("FileBase = %q", got)
	}
}

func wellFormed(data []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(data))
	var err error
	for err == nil {
		_, err = decoder.Token()
	}
	if err == io.EOF {
		err = nil
	}
	return err
}

func TestDecodeReadsJSONAndHTML(t *testing.T) {
	r := fixtureRun()
	page, _ := HTML(r)
	fromHTML, err := Decode(page)
	if err != nil || fromHTML.ID != r.ID || len(fromHTML.TestCases) != len(r.TestCases) {
		t.Fatalf("from HTML: %v %v", fromHTML, err)
	}
	if _, err := Decode([]byte(`{"hello":"world"}`)); err != ErrNoRun {
		t.Errorf("not a run: %v", err)
	}
	if _, err := Decode([]byte(`<html><body>no data</body></html>`)); err != ErrNoRun {
		t.Errorf("HTML without data: %v", err)
	}
}

func TestFramesLogIsInHubFormat(t *testing.T) {
	out, err := FramesLog(fixtureRun())
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 || !strings.Contains(lines[0], "    [Send] "+fixtureSKI+`{"datagram"`) || !strings.Contains(lines[1], "    [Recv] "+fixtureSKI) {
		t.Errorf("frames log:\n%s", out)
	}
	empty, _ := FramesLog(&Run{ID: "x"})
	if !strings.HasPrefix(string(empty), "# run x embeds no wire frames") {
		t.Errorf("empty log: %s", empty)
	}
}

func TestNeutralizeFormula(t *testing.T) {
	for in, want := range map[string]string{"=1+1": "'=1+1", "@cmd": "'@cmd", "-1000": "-1000", "+5": "+5", "-DANGER": "'-DANGER", "plain": "plain", "": ""} {
		if got := neutralizeFormula(in); got != want {
			t.Errorf("neutralizeFormula(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSpliceLastReplacesTheLastOccurrence(t *testing.T) {
	r := fixtureRun()
	r.Notes = "notes that spell REPORT_DATA_PLACEHOLDER and REPORT_JS_PLACEHOLDER"
	page, err := HTML(r)
	if err != nil {
		t.Fatal(err)
	}
	// The notes appear in the run card and inside the embedded JSON; the real placeholders,
	// the sole content of their script elements, must be gone.
	if strings.Contains(string(page), ">REPORT_DATA_PLACEHOLDER</script>") || strings.Contains(string(page), "<script>REPORT_JS_PLACEHOLDER</script>") {
		t.Error("the placeholders in the notes were spliced instead of the real ones")
	}
	if decoded, err := Decode(page); err != nil || decoded.Notes != r.Notes {
		t.Errorf("decode after notes with placeholder names: %v %v", err, decoded)
	}
}
