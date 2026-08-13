package scenario

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/marck-brusa/eebuster/internal/iso8601"
)

// checkAssertions mirrors cli/scenario.py's _check_assertions comparisons exactly, including
// which operators exist. greater_than/less_than exist for device-specific real values (e.g.
// ConsumptionNominalMax's nameplate wattage) where hardcoding one exact number would only
// ever be right for one peer.
func checkAssertions(args map[string]any, actual map[string]any) map[string]string {
	mismatches := map[string]string{}

	current := func(path string) any {
		var node any = actual
		for _, part := range strings.Split(path, ".") {
			switch container := node.(type) {
			case map[string]any:
				var ok bool
				node, ok = container[part]
				if !ok {
					return nil
				}
			case []any:
				// A numeric segment indexes an array: "ev.vehicles.0.power_w".
				index, err := strconv.Atoi(part)
				if err != nil || index < 0 || index >= len(container) {
					return nil
				}
				node = container[index]
			default:
				return nil
			}
		}
		return node
	}

	for k, v := range asMap(args["equals"]) {
		if !looseEqual(current(k), v) {
			mismatches[k] = fmt.Sprintf("expected == %#v, got %#v", v, current(k))
		}
	}
	for k, v := range asMap(args["not_equals"]) {
		if looseEqual(current(k), v) {
			mismatches[k] = fmt.Sprintf("expected != %#v, got %#v", v, current(k))
		}
	}
	for k, v := range asMap(args["greater_than"]) {
		a, aok := toFloat64(current(k))
		b, _ := toFloat64(v)
		if !aok || !(a > b) {
			mismatches[k] = fmt.Sprintf("expected > %#v, got %#v", v, current(k))
		}
	}
	for k, v := range asMap(args["greater_or_equal"]) {
		a, aok := toFloat64(current(k))
		b, _ := toFloat64(v)
		if !aok || !(a >= b) {
			mismatches[k] = fmt.Sprintf("expected >= %#v, got %#v", v, current(k))
		}
	}
	for k, v := range asMap(args["less_than"]) {
		a, aok := toFloat64(current(k))
		b, _ := toFloat64(v)
		if !aok || !(a < b) {
			mismatches[k] = fmt.Sprintf("expected < %#v, got %#v", v, current(k))
		}
	}
	for k, v := range asMap(args["less_or_equal"]) {
		a, aok := toFloat64(current(k))
		b, _ := toFloat64(v)
		if !aok || !(a <= b) {
			mismatches[k] = fmt.Sprintf("expected <= %#v, got %#v", v, current(k))
		}
	}
	for _, k := range asSlice(args["not_null"]) {
		key, _ := k.(string)
		if current(key) == nil {
			mismatches[key] = "expected a non-null value"
		}
	}
	for k, v := range asMap(args["contains"]) {
		if !containsValue(current(k), v) {
			mismatches[k] = fmt.Sprintf("expected to contain %#v, got %#v", v, current(k))
		}
	}
	for k, v := range asMap(args["length_greater_than"]) {
		length, ok := lengthOf(current(k))
		threshold, _ := toFloat64(v)
		if !ok || !(float64(length) > threshold) {
			mismatches[k] = fmt.Sprintf("expected length > %#v, got %#v", v, current(k))
		}
	}
	// each_less_than applies a bound to every numeric element of an array -- built for
	// plausibility checks on per-phase arrays, where any single phase exceeding the bound is
	// the fault (e.g. a phase current above 1000 A means a milli-unit reached the wire).
	for k, v := range asMap(args["each_less_than"]) {
		bound, _ := toFloat64(v)
		values, ok := current(k).([]any)
		if !ok {
			mismatches[k] = fmt.Sprintf("expected an array, got %#v", current(k))
			continue
		}
		for i, item := range values {
			f, fok := toFloat64(item)
			if !fok || !(f < bound) {
				mismatches[fmt.Sprintf("%s.%d", k, i)] = fmt.Sprintf("expected < %#v, got %#v", v, item)
			}
		}
	}
	// sum_matches requires the elements of an array to add up to another field within a
	// percentage tolerance: {power_per_phase_w: {total: power_w, tolerance_percent: 30}}.
	// Built for total-vs-per-phase consistency, where a device reporting 11 kW total while
	// every phase reads 0 W is publishing decoration, not measurements.
	for k, v := range asMap(args["sum_matches"]) {
		spec := asMap(v)
		totalKey, _ := spec["total"].(string)
		tolerance, _ := toFloat64(spec["tolerance_percent"])
		values, ok := current(k).([]any)
		if !ok {
			mismatches[k] = fmt.Sprintf("expected an array, got %#v", current(k))
			continue
		}
		total, tok := toFloat64(current(totalKey))
		if !tok {
			mismatches[k] = fmt.Sprintf("total field %q is not numeric: %#v", totalKey, current(totalKey))
			continue
		}
		sum := 0.0
		for _, item := range values {
			f, _ := toFloat64(item)
			sum += f
		}
		allowed := abs(total) * tolerance / 100
		if diff := abs(sum - total); diff > allowed {
			mismatches[k] = fmt.Sprintf("phases sum to %.1f but %s reports %.1f (allowed deviation %.1f)", sum, totalKey, total, allowed)
		}
	}
	// duration_at_least / duration_at_most compare ISO-8601 durations ("PT2H"), for asserting
	// announced timing values like the LPC failsafe duration window.
	for k, v := range asMap(args["duration_at_least"]) {
		if msg := compareDuration(current(k), v, true); msg != "" {
			mismatches[k] = msg
		}
	}
	for k, v := range asMap(args["duration_at_most"]) {
		if msg := compareDuration(current(k), v, false); msg != "" {
			mismatches[k] = msg
		}
	}

	return mismatches
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

func compareDuration(actual, expected any, atLeast bool) string {
	actualStr, _ := actual.(string)
	expectedStr, _ := expected.(string)
	a, errA := iso8601.Parse(actualStr)
	b, errB := iso8601.Parse(expectedStr)
	if errA != nil || errB != nil {
		return fmt.Sprintf("expected ISO-8601 durations, got %#v vs %#v", actual, expected)
	}
	if atLeast && a < b {
		return fmt.Sprintf("expected duration >= %s, got %s", expectedStr, actualStr)
	}
	if !atLeast && a > b {
		return fmt.Sprintf("expected duration <= %s, got %s", expectedStr, actualStr)
	}
	return ""
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}

func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}

// looseEqual matches Python's == across YAML-typed expected values (int, bool, string) and
// JSON-decoded actual values (float64, bool, string): 4200 == 4200.0 is true in Python and
// must stay true here even though YAML gives an int and JSON gives a float64.
func looseEqual(actual, expected any) bool {
	if af, aok := toFloat64(actual); aok {
		if ef, eok := toFloat64(expected); eok {
			return af == ef
		}
	}
	return fmt.Sprint(actual) == fmt.Sprint(expected) && sameKind(actual, expected)
}

func sameKind(a, b any) bool {
	_, aBool := a.(bool)
	_, bBool := b.(bool)
	if aBool != bBool {
		return false
	}
	return true
}

func containsValue(container, needle any) bool {
	switch c := container.(type) {
	case string:
		s, _ := needle.(string)
		return strings.Contains(c, s)
	case []any:
		for _, item := range c {
			if looseEqual(item, needle) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func lengthOf(v any) (int, bool) {
	switch x := v.(type) {
	case []any:
		return len(x), true
	case string:
		return len(x), true
	case map[string]any:
		return len(x), true
	default:
		return 0, false
	}
}
