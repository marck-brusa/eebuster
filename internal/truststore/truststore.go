// Package truststore persists the set of SKIs trusted at runtime.
//
// Trusting a peer through the API or the dashboard used to be in-memory only, so a restart
// silently forgot it and the tool sat idle next to a device it had been paired with moments
// earlier. The only durable alternative was a peers: entry with a literal SKI in the config --
// which does not survive the device being re-imaged, because that changes its SKI. Writing the
// runtime decision to the data directory makes "trust once" mean what it says.
//
// The config's peers: list is unaffected and remains the way to declare a device up front;
// this file is strictly additive to it.
package truststore

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// FileName is the store's name inside the data directory.
const FileName = "trusted-skis.json"

type Store struct {
	mu   sync.Mutex
	path string
	skis map[string]bool
}

type fileFormat struct {
	// Comment is written for the benefit of whoever finds this file in a data directory and
	// wonders whether it is safe to delete. It is ignored on read.
	Comment string   `json:"_comment"`
	SKIs    []string `json:"trusted_skis"`
}

const fileComment = "SKIs trusted at runtime via the API or dashboard. Safe to delete; you will simply have to trust those peers again."

// Load reads the store from dir, returning an empty store if the file does not exist. A
// corrupt file is also treated as empty rather than fatal: losing trust decisions costs one
// re-pair, whereas refusing to start strands the tool completely.
func Load(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, FileName), skis: map[string]bool{}}
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return s, fmt.Errorf("reading %s: %w", s.path, err)
	}
	var f fileFormat
	if err := json.Unmarshal(data, &f); err != nil {
		return s, fmt.Errorf("parsing %s (ignoring its contents): %w", s.path, err)
	}
	for _, ski := range f.SKIs {
		if ski = normalise(ski); ski != "" {
			s.skis[ski] = true
		}
	}
	return s, nil
}

// Add records a SKI and writes the file. Returns true if this SKI was not already present.
func (s *Store) Add(ski string) (bool, error) {
	ski = normalise(ski)
	if ski == "" {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.skis[ski] {
		return false, nil
	}
	s.skis[ski] = true
	return true, s.saveLocked()
}

// Remove forgets a SKI and writes the file. Returns true if it was present.
func (s *Store) Remove(ski string) (bool, error) {
	ski = normalise(ski)
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.skis[ski] {
		return false, nil
	}
	delete(s.skis, ski)
	return true, s.saveLocked()
}

// SKIs returns the trusted SKIs in a stable order.
func (s *Store) SKIs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.skis))
	for ski := range s.skis {
		out = append(out, ski)
	}
	sort.Strings(out)
	return out
}

// Has reports whether the SKI is in the store.
func (s *Store) Has(ski string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skis[normalise(ski)]
}

// saveLocked writes via a temporary file and rename, so an interrupted write cannot leave a
// half-written store behind. Caller holds the mutex.
func (s *Store) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	out := fileFormat{Comment: fileComment}
	for ski := range s.skis {
		out.SKIs = append(out.SKIs, ski)
	}
	sort.Strings(out.SKIs)
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// normalise lower-cases and trims a SKI so the same identity compared two ways always matches;
// SKIs arrive from mDNS, certificates and hand-edited config with inconsistent case.
func normalise(ski string) string { return strings.ToLower(strings.TrimSpace(ski)) }
