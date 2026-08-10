package announce

import (
	"strings"
	"testing"
)

func boolPtr(v bool) *bool { return &v }

// The register entry says "available for pairing" (SHIP 7.3.2) and devices commonly list only
// peers advertising true, so overriding it must be exact: replace in place, never duplicate, and
// never disturb the other entries a peer parses.
func TestApplyRegisterOverride(t *testing.T) {
	base := []string{"txtvers=1", "id=LAB-testbench", "path=/ship/", "register=false", "ski=abc", "cat=2"}

	cases := []struct {
		name     string
		txt      []string
		override *bool
		want     string
	}{
		{"forces true", base, boolPtr(true), "register=true"},
		{"forces false", []string{"register=true", "cat=2"}, boolPtr(false), "register=false"},
		{"true stays true", []string{"register=true"}, boolPtr(true), "register=true"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyRegisterOverride(tc.txt, tc.override)
			if n := countPrefix(got, "register="); n != 1 {
				t.Fatalf("expected exactly one register entry, got %d: %v", n, got)
			}
			if !contains(got, tc.want) {
				t.Errorf("expected %q in %v", tc.want, got)
			}
			if len(got) != len(tc.txt) {
				t.Errorf("length changed: %d -> %d (%v)", len(tc.txt), len(got), got)
			}
		})
	}
}

func TestApplyRegisterOverrideNilLeavesTxtAlone(t *testing.T) {
	txt := []string{"txtvers=1", "register=false"}
	got := applyRegisterOverride(txt, nil)
	if len(got) != len(txt) || got[1] != "register=false" {
		t.Errorf("nil override must not change the record, got %v", got)
	}
}

// A stack that supplied no register entry at all still has to end up advertising one, otherwise
// the override silently does nothing.
func TestApplyRegisterOverrideAppendsWhenMissing(t *testing.T) {
	got := applyRegisterOverride([]string{"txtvers=1", "cat=2"}, boolPtr(true))
	if !contains(got, "register=true") {
		t.Errorf("expected register=true to be appended, got %v", got)
	}
	if len(got) != 3 {
		t.Errorf("expected one entry added, got %v", got)
	}
}

// Other entries must survive untouched -- a peer parses all of them.
func TestApplyRegisterOverridePreservesOtherEntries(t *testing.T) {
	txt := []string{"txtvers=1", "id=LAB-testbench", "register=false", "ski=abc"}
	got := applyRegisterOverride(txt, boolPtr(true))
	for _, want := range []string{"txtvers=1", "id=LAB-testbench", "ski=abc"} {
		if !contains(got, want) {
			t.Errorf("lost entry %q from %v", want, got)
		}
	}
	if got[0] != "txtvers=1" {
		t.Errorf("txtvers must stay first, got %v", got)
	}
}

func contains(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func countPrefix(list []string, prefix string) int {
	n := 0
	for _, item := range list {
		if strings.HasPrefix(item, prefix) {
			n++
		}
	}
	return n
}
