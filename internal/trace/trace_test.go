package trace

import (
	"strings"
	"testing"
)

const cleanFrame = `{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[` +
	`{"header":[{"specificationVersion":"1.3.0"},{"addressSource":[{"device":"d:_i:example_dut"},{"entity":[1]},{"feature":6}]},` +
	`{"addressDestination":[{"device":"d:_i:example_cem"},{"entity":[1]},{"feature":1}]},{"msgCounter":42},{"cmdClassifier":"notify"}]},` +
	`{"payload":[{"cmd":[[{"measurementListData":[{"measurementData":[[{"measurementId":1},{"value":[{"number":230},{"scale":0}]}]]}]}]]}]}]}}]}`

const brokenFrame = `{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[` +
	`{"header":[{"specificationVersion":"1.3.0"},{"addressSource":[{"device":"d:_i:example_dut"}]},` +
	`{"addressDestination":[{"device":"d:_i:example_cem"}]},{"msgCounter":43},{"cmdClassifier":"notify"}]},` +
	`{"payload":[{"cmd":[[{"electricalConnectionPermittedValueSetListData":[{"electricalConnectionPermittedValueSetData":[[{"electricalConnectionId":0},{"permittedValueSet":[[]]}]]}]}]]}]}]}}]}`

func TestAddSummarizesSpineFrames(t *testing.T) {
	s := New()
	e := s.Add("eebus-go-remote", "recv", "abcd1234", cleanFrame)
	if e.Kind != "spine" {
		t.Fatalf("kind = %q", e.Kind)
	}
	if e.Function != "measurementListData" {
		t.Fatalf("function = %q", e.Function)
	}
	if e.Classifier != "notify" {
		t.Fatalf("classifier = %q", e.Classifier)
	}
	if e.MsgCounter == nil || *e.MsgCounter != 42 {
		t.Fatalf("msgCounter = %v", e.MsgCounter)
	}
	if e.Source != "d:_i:example_dut" || e.Dest != "d:_i:example_cem" {
		t.Fatalf("addresses = %q -> %q", e.Source, e.Dest)
	}
	if len(e.Findings) != 0 {
		t.Fatalf("clean frame has findings: %v", e.Findings)
	}
}

func TestShipControlFramesAreNamed(t *testing.T) {
	s := New()
	e := s.Add("stack", "send", "abcd1234", `{"connectionHello":[{"phase":"ready"}]}`)
	if e.Kind != "ship" || e.Function != "connectionHello" {
		t.Fatalf("kind=%q function=%q", e.Kind, e.Function)
	}
}

func TestFindingsAndSummary(t *testing.T) {
	s := New()
	s.Add("stack", "recv", "abcd1234", cleanFrame)
	broken := s.Add("stack", "recv", "abcd1234", brokenFrame)
	if len(broken.Findings) == 0 {
		t.Fatal("broken frame has no findings")
	}

	summary := s.Summary()
	if summary.Frames != 2 || summary.NonConformant != 1 {
		t.Fatalf("summary = %+v", summary)
	}
	if summary.PerSKI["abcd1234"] != 1 {
		t.Fatalf("per-ski = %v", summary.PerSKI)
	}
	found := false
	for _, r := range summary.Rules {
		if r.Rule == "empty-array-instance" {
			found = true
			if r.LastSeq != broken.Seq {
				t.Fatalf("rule points at seq %d, want %d", r.LastSeq, broken.Seq)
			}
			if r.SpecRef == "" {
				t.Fatal("rule has no spec reference")
			}
		}
	}
	if !found {
		t.Fatalf("empty-array-instance missing from summary: %+v", summary.Rules)
	}
}

func TestRecentStripsRawAndFilters(t *testing.T) {
	s := New()
	s.Add("stack", "send", "aaaa", cleanFrame)
	s.Add("stack", "recv", "bbbb", strings.Replace(brokenFrame, `{"msgCounter":43}`, `{"msgCounter":44}`, 1))

	entries, latest := s.Recent(0, 100, "", "", false)
	if len(entries) != 2 || latest != 2 {
		t.Fatalf("entries=%d latest=%d", len(entries), latest)
	}
	if entries[0].Raw != "" {
		t.Fatal("list response carries raw payload")
	}

	onlyBad, _ := s.Recent(0, 100, "", "", true)
	if len(onlyBad) != 1 || onlyBad[0].SKI != "bbbb" {
		t.Fatalf("findings filter returned %+v", onlyBad)
	}

	afterAll, latest := s.Recent(latest, 100, "", "", false)
	if len(afterAll) != 0 || latest != 2 {
		t.Fatalf("cursor poll returned %d entries, latest=%d", len(afterAll), latest)
	}

	full, ok := s.Get(1)
	if !ok || full.Raw == "" {
		t.Fatal("single-entry lookup must include raw payload")
	}
}

func TestClearResetsSessions(t *testing.T) {
	s := New()
	s.Add("stack", "recv", "aaaa", cleanFrame)
	// Same counter would warn if the session survived the clear.
	s.Clear()
	e := s.Add("stack", "recv", "aaaa", cleanFrame)
	if len(e.Findings) != 0 {
		t.Fatalf("session state survived Clear: %v", e.Findings)
	}
	if summary := s.Summary(); summary.Frames != 1 {
		t.Fatalf("frames after clear = %d", summary.Frames)
	}
}

func TestRingBounds(t *testing.T) {
	s := New()
	s.capacity = 5
	for range 12 {
		s.Add("stack", "send", "aaaa", `{"connectionHello":[{"phase":"ready"}]}`)
	}
	entries, latest := s.Recent(0, 100, "", "", false)
	if len(entries) != 5 || latest != 12 {
		t.Fatalf("ring kept %d entries, latest %d", len(entries), latest)
	}
	if entries[0].Seq != 8 {
		t.Fatalf("oldest retained seq = %d", entries[0].Seq)
	}
}
