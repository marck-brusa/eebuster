package truststore

import (
	"os"
	"path/filepath"
	"testing"
)

// Synthetic SKIs: 40 hex characters, deliberately not any real device's.
const skiA = "aaaaaaaa1111111111111111111111111111beef"
const skiB = "bbbbbbbb2222222222222222222222222222cafe"

func TestAddPersistsAcrossReload(t *testing.T) {
	dir := t.TempDir()

	s, err := Load(dir)
	if err != nil {
		t.Fatalf("Load on empty dir: %v", err)
	}
	if got := s.SKIs(); len(got) != 0 {
		t.Fatalf("fresh store should be empty, got %v", got)
	}

	added, err := s.Add(skiA)
	if err != nil || !added {
		t.Fatalf("Add(%s) = %v, %v; want true, nil", skiA, added, err)
	}

	// The whole point of the package: a new process must see the same decision.
	reloaded, err := Load(dir)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Has(skiA) {
		t.Errorf("reloaded store lost %s; SKIs=%v", skiA, reloaded.SKIs())
	}
}

func TestAddIsIdempotentAndCaseInsensitive(t *testing.T) {
	s, _ := Load(t.TempDir())
	if added, _ := s.Add(skiA); !added {
		t.Fatal("first Add should report true")
	}
	if added, _ := s.Add(skiA); added {
		t.Error("re-adding the same SKI should report false")
	}
	// SKIs reach us from mDNS, certificates and hand-edited config with inconsistent case;
	// treating those as different identities would double-trust the same device.
	if added, _ := s.Add("AAAAAAAA1111111111111111111111111111BEEF"); added {
		t.Error("uppercase form of a known SKI should not be treated as new")
	}
	if got := len(s.SKIs()); got != 1 {
		t.Errorf("expected 1 stored SKI, got %d (%v)", got, s.SKIs())
	}
}

func TestRemove(t *testing.T) {
	dir := t.TempDir()
	s, _ := Load(dir)
	_, _ = s.Add(skiA)
	_, _ = s.Add(skiB)

	removed, err := s.Remove(skiA)
	if err != nil || !removed {
		t.Fatalf("Remove(%s) = %v, %v; want true, nil", skiA, removed, err)
	}
	if removed, _ := s.Remove(skiA); removed {
		t.Error("removing an absent SKI should report false")
	}

	reloaded, _ := Load(dir)
	if reloaded.Has(skiA) {
		t.Error("removal did not persist")
	}
	if !reloaded.Has(skiB) {
		t.Error("removing one SKI dropped the other")
	}
}

func TestSKIsIsSorted(t *testing.T) {
	s, _ := Load(t.TempDir())
	_, _ = s.Add(skiB)
	_, _ = s.Add(skiA)
	got := s.SKIs()
	if len(got) != 2 || got[0] != skiA || got[1] != skiB {
		t.Errorf("SKIs() = %v; want stable sorted order [%s %s]", got, skiA, skiB)
	}
}

func TestLoadToleratesCorruptFile(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Refusing to start would strand the tool entirely; losing trust costs one re-pair.
	s, err := Load(dir)
	if err == nil {
		t.Error("expected a reported error for a corrupt file")
	}
	if s == nil {
		t.Fatal("Load must still return a usable store")
	}
	if added, err := s.Add(skiA); err != nil || !added {
		t.Errorf("store should be writable after a corrupt read: %v, %v", added, err)
	}
}

func TestBlankSKIIgnored(t *testing.T) {
	s, _ := Load(t.TempDir())
	if added, _ := s.Add("   "); added {
		t.Error("blank SKI should not be stored")
	}
	if got := len(s.SKIs()); got != 0 {
		t.Errorf("expected empty store, got %v", s.SKIs())
	}
}
