package eebusgo

import "testing"

func TestPhaseName(t *testing.T) {
	for input, want := range map[string]string{
		"a": "a", "A": "a", "l1": "a", "1": "a",
		"b": "b", "L2": "b", "c": "c", "3": "c",
	} {
		got, err := phaseName(input)
		if err != nil || string(got) != want {
			t.Errorf("phaseName(%q) = %q, %v; want %q", input, got, err, want)
		}
	}
	if _, err := phaseName("d"); err == nil {
		t.Error("phaseName(d) must fail")
	}
}

func TestPhaseLimitsIn(t *testing.T) {
	in, err := phaseLimitsIn([]PhaseLimit{
		{Phase: "a", ValueA: 6, IsActive: true},
		{Phase: "L2", ValueA: 10, IsActive: false},
	})
	if err != nil || len(in) != 2 {
		t.Fatalf("unexpected: %v, %v", in, err)
	}
	if string(in[0].Phase) != "a" || in[0].Value != 6 || !in[0].IsActive {
		t.Fatalf("first limit mangled: %+v", in[0])
	}
	if string(in[1].Phase) != "b" || in[1].IsActive {
		t.Fatalf("second limit mangled: %+v", in[1])
	}
	if _, err := phaseLimitsIn(nil); err == nil {
		t.Error("empty limits must fail")
	}
	if _, err := phaseLimitsIn([]PhaseLimit{{Phase: "a", ValueA: -1}}); err == nil {
		t.Error("negative current must fail")
	}
}
