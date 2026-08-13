package eebusgo

import "testing"

func TestBuildUseCaseDetails(t *testing.T) {
	raw := []UseCaseEntry{
		{
			Address: []uint{1},
			Actor:   "ControllableSystem",
			UseCaseSupport: []UseCaseSupport{
				{UseCaseName: "limitationOfPowerConsumption", UseCaseAvailable: true,
					UseCaseVersion: "1.0.0", ScenarioSupport: []uint{1, 2, 3, 4}},
				{UseCaseName: "somethingBrandNew", UseCaseAvailable: false},
			},
		},
		{
			Address: []uint{1, 1},
			Actor:   "EV",
			UseCaseSupport: []UseCaseSupport{
				{UseCaseName: "measurementOfElectricityDuringEvCharging", UseCaseAvailable: true,
					UseCaseVersion: "1.0.1", ScenarioSupport: []uint{1, 2, 3}},
			},
		},
	}
	details := buildUseCaseDetails(raw)
	if len(details) != 3 {
		t.Fatalf("expected 3 details, got %d", len(details))
	}

	lpc := details[0]
	if lpc.Acronym != "LPC" || lpc.Title != "Limitation of Power Consumption" {
		t.Fatalf("catalog labels missing: %+v", lpc)
	}
	if lpc.Actor != "ControllableSystem" || lpc.Version != "1.0.0" || !lpc.Available {
		t.Fatalf("live discovery data missing: %+v", lpc)
	}
	if len(lpc.Scenarios) != 4 || len(lpc.Address) != 1 || lpc.Address[0] != 1 {
		t.Fatalf("scenarios/address wrong: %+v", lpc)
	}
	if len(lpc.TypedOperations) == 0 {
		t.Fatalf("LPC must list typed operations: %+v", lpc)
	}

	// A use case outside the catalog still appears, keeping its wire name as the title and
	// its advertised availability -- the browser reports what the peer says, always.
	unknown := details[1]
	if unknown.Title != "somethingBrandNew" || unknown.Acronym != "" || unknown.Available {
		t.Fatalf("unknown use case mishandled: %+v", unknown)
	}
	if unknown.Scenarios == nil || unknown.TypedOperations == nil {
		t.Fatalf("nil slices would JSON-encode as null: %+v", unknown)
	}

	evcem := details[2]
	if evcem.Acronym != "EVCEM" || evcem.Actor != "EV" || len(evcem.Address) != 2 {
		t.Fatalf("second entity entry wrong: %+v", evcem)
	}
}
