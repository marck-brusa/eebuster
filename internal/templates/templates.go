// Package templates embeds templates.yaml (named request-body examples like
// "lpc.limit.dim_4200w_2h") and exposes it the same way src/facade/api/templates_store.py did:
// GET /api/v1/templates and the scenario runner's put.template lookups share one definition.
package templates

import (
	_ "embed"

	"gopkg.in/yaml.v3"
)

//go:embed templates.yaml
var raw []byte

// Entry is one named template, e.g. lpc.limit.dim_4200w_2h.
type Entry struct {
	Summary     string `yaml:"summary" json:"summary"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	Value       any    `yaml:"value" json:"value"`
}

// Store is category -> name -> Entry, e.g. Store["lpc.limit"]["dim_4200w_2h"].
type Store map[string]map[string]Entry

var store Store

func init() {
	if err := yaml.Unmarshal(raw, &store); err != nil {
		panic("templates: embedded templates.yaml is invalid: " + err.Error())
	}
}

// All returns the full store, matching GET /api/v1/templates' shape exactly.
func All() Store { return store }

// Lookup resolves a dotted name like "lpc.limit.dim_4200w_2h" (category "lpc.limit", key
// "dim_4200w_2h") to its Value, matching cli/scenario.py's _lookup_template.
func Lookup(dottedName string) (any, bool) {
	i := lastDot(dottedName)
	if i < 0 {
		return nil, false
	}
	category, key := dottedName[:i], dottedName[i+1:]
	entry, ok := store[category][key]
	if !ok {
		return nil, false
	}
	return entry.Value, true
}

func lastDot(s string) int {
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return i
		}
	}
	return -1
}
