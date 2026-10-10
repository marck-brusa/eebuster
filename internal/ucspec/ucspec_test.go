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
