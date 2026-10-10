package testrun

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"

	"github.com/marck-brusa/eebuster/internal/report"
)

// Defaults for parameters that do not depend on the device.
var defaultParameters = map[string]any{
	// LPC limit durations: the 2 h curtailment window and a short one that ends inside a run.
	"limit_durations": []any{"PT2H", "PT15M"},
	// LPC failsafe minimum durations the device must accept: the 2 h minimum and the 24 h
	// maximum of the specified window.
	"failsafe_durations": []any{"PT2H", "PT24H"},
}

// parameters merges the configured parameters with values derived from what the device
// announces. Configured values always win; every derived or default value is listed.
func (c *collector) parameters(configured map[string]any, d report.Device) report.Parameters {
	p := report.Parameters{Values: normalize(configured)}
	for name := range p.Values {
		p.Configured = append(p.Configured, name)
	}
	sort.Strings(p.Configured)
	derive := func(name string, value any) {
		if _, set := p.Values[name]; !set && value != nil {
			p.Values[name] = value
			p.Derived = append(p.Derived, name)
		}
	}
	if d.Connected && advertises(d, "limitationOfPowerConsumption") {
		nominal := c.nominalMax("/api/v1/lpc/" + d.SKI + "/nominal-max")
		derive("nominal_max_w", nominal)
		if n, ok := toNumber(p.Values["nominal_max_w"]); ok && n > 0 {
			derive("limits_w", []any{round(n), round(n / 2), round(n / 4)})
			derive("failsafe_w", []any{round(n / 2), round(n / 4)})
			if contains(p.Derived, "limits_w") || contains(p.Derived, "failsafe_w") {
				p.Notes = append(p.Notes, fmt.Sprintf("limits_w is 100 %%, 50 %% and 25 %%, failsafe_w 50 %% and 25 %% of the nominal maximum %g W.", n))
			}
		}
	}
	if d.Connected && advertises(d, "limitationOfPowerProduction") {
		nominal := c.nominalMax("/api/v1/lpp/" + d.SKI + "/nominal-max")
		derive("production_nominal_max_w", nominal)
		if n, ok := toNumber(p.Values["production_nominal_max_w"]); ok && n > 0 {
			derive("production_limits_w", []any{round(n), round(n / 2), round(n / 4)})
		}
	}
	for _, name := range sortedKeys(defaultParameters) {
		derive(name, defaultParameters[name])
	}
	return p
}

func (c *collector) nominalMax(path string) any {
	var body struct {
		ValueW *float64 `json:"value_w"`
	}
	var value any
	if c.get(path, &body) == nil && body.ValueW != nil && *body.ValueW > 0 {
		value = *body.ValueW
	}
	return value
}

// normalize gives configured values the shapes JSON decoding produces (float64 numbers,
// []any lists), so scenario references resolve the same way for every source.
func normalize(in map[string]any) map[string]any {
	out := map[string]any{}
	if data, err := json.Marshal(in); err == nil && in != nil {
		_ = json.Unmarshal(data, &out)
	}
	return out
}

func toNumber(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

func round(v float64) float64 { return math.Round(v) }

func contains(list []string, s string) bool {
	found := false
	for _, item := range list {
		found = found || item == s
	}
	return found
}
