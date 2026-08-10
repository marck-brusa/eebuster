package scenario

import (
	"fmt"
	"strings"
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
			m, ok := node.(map[string]any)
			if !ok {
				return nil
			}
			node, ok = m[part]
			if !ok {
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

	return mismatches
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
