package scenario

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestUseCaseInstancesCountsEntitiesPerUseCase(t *testing.T) {
	// MPC on two entities, LPC on one entity listed twice, LPP withdrawn, MGCP absent.
	usecases := `[
		{"address":[1],"actor":"ControllableSystem","useCaseSupport":[
			{"useCaseName":"limitationOfPowerConsumption","useCaseAvailable":true,"scenarioSupport":[1,2,3]},
			{"useCaseName":"monitoringOfPowerConsumption","useCaseAvailable":true,"scenarioSupport":[1]}]},
		{"address":[1],"actor":"ControllableSystem","useCaseSupport":[
			{"useCaseName":"limitationOfPowerConsumption","useCaseAvailable":true,"scenarioSupport":[4]},
			{"useCaseName":"limitationOfPowerProduction","useCaseAvailable":false,"scenarioSupport":[1]}]},
		{"address":[1,1],"actor":"MonitoredUnit","useCaseSupport":[
			{"useCaseName":"monitoringOfPowerConsumption","useCaseAvailable":true,"scenarioSupport":[1]}]}
	]`
	srv := fakeAPI(t, map[string]string{"GET /api/v1/peers/" + testSKI + "/usecases": usecases})
	defer srv.Close()
	ctx := map[string]any{"peer": map[string]any{"ski": testSKI}}
	args := map[string]any{"use_cases": []any{"LPC", "LPP", "MPC", "MGCP"}}

	step := NewRunner(srv.URL).stepUseCaseInstances(args, ctx)
	if step.Status != StatusFailed || len(step.Assertions) != 4 {
		t.Fatalf("two MPC instances must fail: %+v", step)
	}
	want := map[string]int{"LPC instances": 1, "LPP instances": 0, "MPC instances": 2, "MGCP instances": 0}
	for _, c := range step.Assertions {
		if c.Actual != want[c.Key] || c.OK != (want[c.Key] <= 1) {
			t.Errorf("%s: %+v", c.Key, c)
		}
	}
	if !strings.Contains(step.Detail, "MPC 2 (entity [1], [1 1])") {
		t.Errorf("detail %q does not name the entities", step.Detail)
	}

	args["at_most"] = 2
	if step := NewRunner(srv.URL).stepUseCaseInstances(args, ctx); step.Status != StatusPassed {
		t.Errorf("at_most 2: %+v", step)
	}
	missing := NewRunner(srv.URL).stepUseCaseInstances(args, map[string]any{"peer": map[string]any{"ski": "unknown"}})
	if missing.Status != StatusFailed || !strings.Contains(missing.Detail, "404") {
		t.Errorf("unknown peer: %+v", missing)
	}
}

func TestLoadMetaResolvesGuidelineReferences(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lpc-heartbeat.yaml")
	writeFile(t, path, `name: lpc-heartbeat
spec: {use_case: LPC, scenario: 3, guidelines: {VDE FNN: [EEBUS_014, EEBUS_015, EEBUS_023], other-doc: [X_1]}}
steps: []
`)
	meta, err := LoadMeta(path)
	if err != nil {
		t.Fatal(err)
	}
	refs := meta.Spec.References
	if len(refs) != 2 {
		t.Fatalf("references = %+v", refs)
	}
	fnn := refs[0]
	if fnn.Guideline != "vde-fnn-steuerbox" || fnn.Label != "VDE FNN" || !strings.Contains(fnn.Document, "Steuerbox") ||
		strings.Join(fnn.Requirements, " ") != "EEBUS_014 EEBUS_015 EEBUS_023" || strings.Join(fnn.Sections, " ") != "A.5.1.2 A.5.1.3" {
		t.Errorf("VDE FNN reference = %+v", fnn)
	}
	if refs[1].Guideline != "other-doc" || len(refs[1].Sections) != 0 {
		t.Errorf("an unknown guideline is kept as written: %+v", refs[1])
	}
	if !meta.Spec.Cites("vde-fnn-steuerbox") || !meta.Spec.Cites("vde fnn") || meta.Spec.Cites("LPC") {
		t.Error("Cites does not match the guideline by id and label only")
	}
	var none *Spec
	if none.Cites("VDE FNN") {
		t.Error("a test case without a spec cites nothing")
	}
}
