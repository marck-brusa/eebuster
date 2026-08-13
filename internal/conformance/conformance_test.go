package conformance

import (
	"fmt"
	"strings"
	"testing"
)

// wrap builds a syntactically complete SHIP data message around a cmd body in wire format.
func wrap(cmd string) []byte {
	return []byte(`{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[` +
		`{"header":[{"specificationVersion":"1.3.0"},{"addressSource":[{"device":"d:_i:example_dut"},{"entity":[1]},{"feature":6}]},` +
		`{"addressDestination":[{"device":"d:_i:example_cem"},{"entity":[1]},{"feature":1}]},{"msgCounter":42},{"cmdClassifier":"notify"}]},` +
		`{"payload":[{"cmd":[[` + cmd + `]]}]}]}}]}`)
}

func rules(findings []Finding) []string {
	var out []string
	for _, f := range findings {
		out = append(out, f.Rule)
	}
	return out
}

func hasRule(findings []Finding, rule string) bool {
	for _, f := range findings {
		if f.Rule == rule {
			return true
		}
	}
	return false
}

func TestCleanFrameHasNoFindings(t *testing.T) {
	frame := wrap(`{"loadControlLimitListData":[{"loadControlLimitData":[[{"limitId":1},{"isLimitActive":true},{"value":[{"number":4200},{"scale":0}]}]]}]}`)
	if f := CheckFrame(frame); len(f) != 0 {
		t.Fatalf("clean frame reported findings: %v", rules(f))
	}
}

func TestShipControlMessagesAreIgnored(t *testing.T) {
	for _, payload := range []string{"ship init", `{"connectionHello":[{"phase":"ready"},{"waiting":60000}]}`} {
		if f := CheckFrame([]byte(payload)); len(f) != 0 {
			t.Errorf("%q reported findings: %v", payload, rules(f))
		}
	}
}

// The customer-reported fault class: an empty value set serialised as [[]] -- an array where
// an object carrying exactly one element is expected.
func TestEmptyValueSetInsideArray(t *testing.T) {
	frame := wrap(`{"electricalConnectionPermittedValueSetListData":[{"electricalConnectionPermittedValueSetData":[[{"electricalConnectionId":0},{"parameterId":1},{"permittedValueSet":[[]]}]]}]}`)
	findings := CheckFrame(frame)
	if !hasRule(findings, "empty-array-instance") {
		t.Fatalf("expected empty-array-instance, got %v", rules(findings))
	}
	for _, f := range findings {
		if f.Rule == "empty-array-instance" && !strings.Contains(f.Path, "permittedValueSet") {
			t.Errorf("finding path %q does not point into permittedValueSet", f.Path)
		}
	}
}

func TestMultiMemberObject(t *testing.T) {
	// Order-losing encoding: several elements folded into one JSON object.
	frame := wrap(`{"loadControlLimitListData":[{"loadControlLimitData":[{"limitId":1,"isLimitActive":true}]}]}`)
	if findings := CheckFrame(frame); !hasRule(findings, "multi-member-object") {
		t.Fatalf("expected multi-member-object, got %v", rules(findings))
	}
}

func TestEmptyObject(t *testing.T) {
	frame := wrap(`{"loadControlLimitListData":[{"loadControlLimitData":{}}]}`)
	if findings := CheckFrame(frame); !hasRule(findings, "empty-object") {
		t.Fatalf("expected empty-object, got %v", rules(findings))
	}
}

func TestTrailingNul(t *testing.T) {
	frame := append(wrap(`{"deviceDiagnosisHeartbeatData":[{"timeout":"PT2M"}]}`), 0)
	findings := CheckFrame(frame)
	if !hasRule(findings, "trailing-nul") {
		t.Fatalf("expected trailing-nul, got %v", rules(findings))
	}
	// The JSON itself is fine, so nothing else may fire.
	if len(findings) != 1 {
		t.Fatalf("expected only trailing-nul, got %v", rules(findings))
	}
}

func TestInvalidJSON(t *testing.T) {
	if findings := CheckFrame([]byte(`{"data":[`)); !hasRule(findings, "invalid-json") {
		t.Fatalf("expected invalid-json, got %v", rules(findings))
	}
}

func TestMissingHeaderElements(t *testing.T) {
	frame := []byte(`{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[` +
		`{"header":[{"specificationVersion":"1.3.0"},{"msgCounter":42},{"cmdClassifier":"notify"}]},` +
		`{"payload":[{"cmd":[[{"nodeManagementUseCaseData":[]}]]}]}]}}]}`)
	findings := CheckFrame(frame)
	missing := 0
	for _, f := range findings {
		if f.Rule == "datagram-header" {
			missing++
		}
	}
	if missing != 2 { // addressSource and addressDestination
		t.Fatalf("expected 2 datagram-header findings, got %v", rules(findings))
	}
}

func TestReplyWithoutCounterReference(t *testing.T) {
	frame := []byte(`{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[` +
		`{"header":[{"specificationVersion":"1.3.0"},{"addressSource":[{"device":"d:_i:a"}]},` +
		`{"addressDestination":[{"device":"d:_i:b"}]},{"msgCounter":43},{"cmdClassifier":"reply"}]},` +
		`{"payload":[{"cmd":[[{"measurementListData":[]}]]}]}]}}]}`)
	if findings := CheckFrame(frame); !hasRule(findings, "msg-counter-ref") {
		t.Fatalf("expected msg-counter-ref, got %v", rules(findings))
	}
}

func TestInvalidClassifier(t *testing.T) {
	frame := []byte(`{"data":[{"header":[{"protocolId":"ee1.0"}]},{"payload":{"datagram":[` +
		`{"header":[{"specificationVersion":"1.3.0"},{"addressSource":[{"device":"d:_i:a"}]},` +
		`{"addressDestination":[{"device":"d:_i:b"}]},{"msgCounter":44},{"cmdClassifier":"push"}]},` +
		`{"payload":[{"cmd":[[{"measurementListData":[]}]]}]}]}}]}`)
	if findings := CheckFrame(frame); !hasRule(findings, "cmd-classifier") {
		t.Fatalf("expected cmd-classifier, got %v", rules(findings))
	}
}

// The mA-encoded-as-A fault: the device declares unit A in its own description, then reports
// a phase current 1000x too large. Only visible by correlating the two messages.
func TestSessionCorrelatesUnitsAcrossMessages(t *testing.T) {
	session := NewSession()

	description := wrap(`{"measurementDescriptionListData":[{"measurementDescriptionData":[[{"measurementId":3},{"measurementType":"current"},{"commodityType":"electricity"},{"unit":"A"},{"scopeType":"acCurrent"}]]}]}`)
	if f := session.Check("recv", description); len(f) != 0 {
		t.Fatalf("description frame reported findings: %v", rules(f))
	}

	implausible := strings.Replace(
		string(wrap(`{"measurementListData":[{"measurementData":[[{"measurementId":3},{"value":[{"number":16000},{"scale":0}]}]]}]}`)),
		`{"msgCounter":42}`, `{"msgCounter":43}`, 1)
	findings := session.Check("recv", []byte(implausible))
	if !hasRule(findings, "value-magnitude") {
		t.Fatalf("expected value-magnitude, got %v", rules(findings))
	}

	// The same magnitude with a milli scale is a perfectly good 16 A.
	plausible := strings.Replace(
		string(wrap(`{"measurementListData":[{"measurementData":[[{"measurementId":3},{"value":[{"number":16000},{"scale":-3}]}]]}]}`)),
		`{"msgCounter":42}`, `{"msgCounter":44}`, 1)
	if f := session.Check("recv", []byte(plausible)); len(f) != 0 {
		t.Fatalf("plausible value reported findings: %v", rules(f))
	}
}

func TestSessionMsgCounterDuplicates(t *testing.T) {
	session := NewSession()
	withCounter := func(n int) []byte {
		return []byte(strings.Replace(string(wrap(`{"deviceDiagnosisHeartbeatData":[{"timeout":"PT2M"}]}`)),
			`{"msgCounter":42}`, fmt.Sprintf(`{"msgCounter":%d}`, n), 1))
	}
	if f := session.Check("recv", withCounter(500)); len(f) != 0 {
		t.Fatalf("first frame reported findings: %v", rules(f))
	}
	// Out-of-order arrival is legitimate: senders allocate counters at build time and heavy
	// replies leave late. No finding.
	if f := session.Check("recv", withCounter(497)); len(f) != 0 {
		t.Fatalf("out-of-order counter reported findings: %v", rules(f))
	}
	// Reuse of a counter is the fault.
	if f := session.Check("recv", withCounter(500)); !hasRule(f, "msg-counter-duplicate") {
		t.Fatalf("expected msg-counter-duplicate, got %v", rules(f))
	}
	// Directions are tracked independently.
	if f := session.Check("send", withCounter(500)); len(f) != 0 {
		t.Fatalf("send direction inherited recv counters: %v", rules(f))
	}
	// A device restart replays small counters; that is not reuse.
	if f := session.Check("recv", withCounter(1)); len(f) != 0 {
		t.Fatalf("restart counter reported findings: %v", rules(f))
	}
	if f := session.Check("recv", withCounter(2)); len(f) != 0 {
		t.Fatalf("post-restart counter reported findings: %v", rules(f))
	}
}

func TestNormalizeMergesAndKeepsLists(t *testing.T) {
	// Repeated element names stay a list; distinct single-member objects merge.
	frame := wrap(`{"measurementListData":[{"measurementData":[[{"measurementId":1},{"value":[{"number":10},{"scale":0}]}]]},{"measurementData":[[{"measurementId":2},{"value":[{"number":11},{"scale":0}]}]]}]}`)
	if f := CheckFrame(frame); len(f) != 0 {
		t.Fatalf("repeated-element frame reported findings: %v", rules(f))
	}
}

// Primitive arrays (entity addresses) must not be mistaken for broken element lists.
func TestPrimitiveArraysAreConformant(t *testing.T) {
	frame := wrap(`{"nodeManagementDetailedDiscoveryData":[{"specificationVersionList":[{"specificationVersion":"1.3.0"}]}]}`)
	if f := CheckFrame(frame); len(f) != 0 {
		t.Fatalf("frame reported findings: %v", rules(f))
	}
	// wrap() itself already carries "entity":[1] primitive arrays in both addresses.
}

func TestFindingsCarrySpecReferences(t *testing.T) {
	frame := wrap(`{"electricalConnectionPermittedValueSetListData":[{"permittedValueSet":[[]]}]}`)
	for _, f := range CheckFrame(frame) {
		if f.SpecRef == "" {
			t.Errorf("finding %s has no spec reference", f.Rule)
		}
	}
}

func ExampleCheckFrame() {
	findings := CheckFrame(wrap(`{"electricalConnectionPermittedValueSetListData":[{"electricalConnectionPermittedValueSetData":[[{"electricalConnectionId":0},{"permittedValueSet":[[]]}]]}]}`))
	for _, f := range findings {
		fmt.Println(f.Rule, f.Severity)
	}
	// Output: empty-array-instance error
}
