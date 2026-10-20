package ucspec

import "testing"

func TestLookupByAcronymAndName(t *testing.T) {
	byAcronym, ok := Lookup("lpc")
	if !ok || byAcronym.Name != "limitationOfPowerConsumption" {
		t.Fatalf("Lookup(lpc) = %+v, %v", byAcronym, ok)
	}
	byName, ok := Lookup("evStateOfCharge")
	if !ok || byName.Acronym != "EVSOC" {
		t.Fatalf("Lookup(evStateOfCharge) = %+v, %v", byName, ok)
	}
	if _, ok := Lookup("nonexistent"); ok {
		t.Fatal("Lookup(nonexistent) found something")
	}
}

func TestCatalogIsConsistent(t *testing.T) {
	seen := map[string]bool{}
	for _, uc := range All() {
		if seen[uc.Acronym] || seen[uc.Name] {
			t.Errorf("%s listed twice", uc.Acronym)
		}
		seen[uc.Acronym], seen[uc.Name] = true, true
		if uc.DeviceActor == "" || uc.TestbenchActor == "" || uc.Document == "" || len(uc.Scenarios) == 0 {
			t.Errorf("%s is incomplete: %+v", uc.Acronym, uc)
		}
		for i, s := range uc.Scenarios {
			if s.Number != uint(i+1) {
				t.Errorf("%s scenario %d is numbered %d", uc.Acronym, i+1, s.Number)
			}
			for _, level := range []Level{s.Device, s.Testbench} {
				if level != Mandatory && level != Recommended && level != Optional && level != NotApplicable {
					t.Errorf("%s scenario %d has level %q", uc.Acronym, s.Number, level)
				}
			}
		}
	}
}

// The column order of these tables differs between documents; these rows were the ones most
// easily transposed when reading them.
func TestDeviceColumnOfTransposableTables(t *testing.T) {
	cases := []struct {
		acronym  string
		scenario uint
		device   Level
	}{
		{"LPC", 4, Recommended},
		{"MGCP", 2, Mandatory},
		{"MGCP", 5, Recommended},
		{"VAPD", 1, Mandatory},
		{"VABD", 4, Mandatory},
		{"EVCEM", 1, Recommended},
		{"CEVC", 1, Recommended},
	}
	for _, c := range cases {
		uc, _ := Lookup(c.acronym)
		s, ok := uc.Scenario(c.scenario)
		if !ok || s.Device != c.device {
			t.Errorf("%s scenario %d device level = %q, want %q", c.acronym, c.scenario, s.Device, c.device)
		}
	}
}

func TestMissingScenarios(t *testing.T) {
	uc, _ := Lookup("LPC")
	mandatory, others := uc.MissingScenarios([]uint{1, 2})
	if len(mandatory) != 1 || mandatory[0].Number != 3 {
		t.Errorf("mandatory = %+v, want scenario 3", mandatory)
	}
	if len(others) != 1 || others[0].Number != 4 {
		t.Errorf("others = %+v, want scenario 4", others)
	}
	mandatory, others = uc.MissingScenarios([]uint{1, 2, 3, 4})
	if len(mandatory) != 0 || len(others) != 0 {
		t.Errorf("complete advertisement reported missing %v %v", mandatory, others)
	}
}

func TestOrderPutsUnknownLast(t *testing.T) {
	if Order("LPC") != 0 || Order("MPC") >= Order("EVCC") {
		t.Errorf("unexpected order LPC=%d MPC=%d EVCC=%d", Order("LPC"), Order("MPC"), Order("EVCC"))
	}
	if Order("unknown") != len(All()) {
		t.Errorf("Order(unknown) = %d, want %d", Order("unknown"), len(All()))
	}
}

func TestGuidelineLookupAndSections(t *testing.T) {
	for _, key := range []string{"vde-fnn-steuerbox", "VDE FNN", "vde fnn"} {
		g, ok := LookupGuideline(key)
		if !ok || g.ID != "vde-fnn-steuerbox" {
			t.Errorf("LookupGuideline(%q) = %+v, %v", key, g, ok)
		}
	}
	if _, ok := LookupGuideline("nonexistent"); ok {
		t.Error("LookupGuideline(nonexistent) found something")
	}
	g, _ := LookupGuideline("VDE FNN")
	if section, ok := g.Section("EEBUS_013"); !ok || section != "A.5.1.2" {
		t.Errorf("EEBUS_013 is in section %q, %v", section, ok)
	}
	if _, ok := g.Section("EEBUS_005"); ok {
		t.Error("EEBUS_005 has no text in V1.5 and must not be citable")
	}
	for _, guideline := range Guidelines() {
		if guideline.ID == "" || guideline.Label == "" || guideline.Title == "" || len(guideline.Sections) == 0 {
			t.Errorf("guideline incomplete: %+v", guideline)
		}
	}
}

func TestBk6GuidelineCitesAnlage1Clauses(t *testing.T) {
	g, ok := LookupGuideline("bk6-22-300")
	if !ok || g.Label != "BNetzA BK6-22-300" {
		t.Fatalf("LookupGuideline(bk6-22-300) = %+v, %v", g, ok)
	}
	if section, ok := g.Section("4.6"); !ok || section != "4.6" {
		t.Errorf("clause 4.6 is in section %q, %v", section, ok)
	}
}
