package eebusgo

import (
	"encoding/json"
	"testing"
)

func TestFlattenUseCaseNames(t *testing.T) {
	entries := []UseCaseEntry{
		{Address: []uint{1}, UseCaseSupport: []UseCaseSupport{
			{UseCaseName: "limitationOfPowerConsumption", UseCaseAvailable: true},
			{UseCaseName: "monitoringOfPowerConsumption", UseCaseAvailable: true},
		}},
		{Address: []uint{1}, UseCaseSupport: []UseCaseSupport{
			// Duplicate across entities: a real device advertises the same use case from more
			// than one entity, and the flat list should name it once.
			{UseCaseName: "limitationOfPowerConsumption", UseCaseAvailable: true},
			{UseCaseName: "limitationOfPowerProduction", UseCaseAvailable: false},
			{UseCaseName: "", UseCaseAvailable: true},
		}},
	}

	got := flattenUseCaseNames(entries)
	want := []string{"limitationOfPowerConsumption", "monitoringOfPowerConsumption"}
	if len(got) != len(want) {
		t.Fatalf("flattenUseCaseNames() = %v; want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("index %d = %q; want %q (first-seen order must be preserved)", i, got[i], want[i])
		}
	}
}

// An unavailable use case must not be reported as supported: a scenario gates on this to decide
// between running and skipping.
func TestFlattenUseCaseNamesExcludesUnavailable(t *testing.T) {
	got := flattenUseCaseNames([]UseCaseEntry{{UseCaseSupport: []UseCaseSupport{
		{UseCaseName: "limitationOfPowerProduction", UseCaseAvailable: false},
	}}})
	if len(got) != 0 {
		t.Errorf("got %v; want no names", got)
	}
}

// `null` is not iterable in most clients, so an empty result has to encode as [].
func TestUseCasesEncodesAsEmptyArrayNotNull(t *testing.T) {
	data, err := json.Marshal(PeerProfile{UseCases: flattenUseCaseNames(nil)})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["use_cases"] == nil {
		t.Errorf("use_cases encoded as null; want []. JSON: %s", data)
	}
}

// The profile must carry both shapes: the flat list callers usually want, and the nested
// per-entity form. Omitting the flat one was the regression that made device-profile-discovery
// assert on a field that did not exist.
func TestPeerProfileExposesBothUseCaseShapes(t *testing.T) {
	raw := []UseCaseEntry{{Address: []uint{1}, UseCaseSupport: []UseCaseSupport{
		{UseCaseName: "limitationOfPowerConsumption", UseCaseAvailable: true},
	}}}
	data, err := json.Marshal(PeerProfile{UseCases: flattenUseCaseNames(raw), RawUseCases: raw})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"use_cases", "raw_use_cases"} {
		if decoded[field] == nil {
			t.Errorf("%s missing from profile JSON: %s", field, data)
		}
	}
}
