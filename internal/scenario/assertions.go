package scenario

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/marck-brusa/eebuster/internal/iso8601"
)

// Assertion operators, in evaluation order. The first block mirrors cli/scenario.py's
// _check_assertions; the rest were added for plausibility and specification checks.
// greater_than/less_than exist for device-specific real values (e.g. ConsumptionNominalMax's
// nameplate wattage) where hardcoding one exact number would only ever be right for one peer.
var assertionOps = []string{
	"equals", "not_equals", "greater_than", "greater_or_equal", "less_than", "less_or_equal",
	"not_null", "contains", "length_greater_than", "each_less_than", "sum_matches",
	"duration_at_least", "duration_at_most",
	"any_not_null", "between", "each_between", "one_of", "each_not_null", "some_not_null", "length_at_least",
	"each_matches",
}

// evaluateAssertions checks every assertion of an assert step against the response object
// and returns one comparison per checked value, passing or not, in a stable order.
func evaluateAssertions(args map[string]any, actual map[string]any) []Comparison {
	comparisons := []Comparison{}
	for _, op := range assertionOps {
		if spec, ok := args[op]; ok {
			comparisons = append(comparisons, evaluateOp(op, spec, actual)...)
		}
	}
	return comparisons
}

// valueAt walks a dotted path through the response; a numeric segment indexes a list:
// "ev.vehicles.0.power_w".
func valueAt(actual map[string]any, path string) any {
	var node any = actual
	found := true
	for _, part := range strings.Split(path, ".") {
		if found {
			node, found = child(node, part)
		}
	}
	if !found {
		node = nil
	}
	return node
}

func evaluateOp(op string, spec any, actual map[string]any) []Comparison {
	var out []Comparison
	switch op {
	case "not_null":
		for _, k := range asSlice(spec) {
			key, _ := k.(string)
			v := valueAt(actual, key)
			out = append(out, compared(key, op, nil, v, v != nil, "expected a non-null value"))
		}
	case "any_not_null":
		out = append(out, anyNotNull(spec, actual))
	default:
		for _, key := range sortedKeys(asMap(spec)) {
			out = append(out, evaluateKey(op, key, asMap(spec)[key], actual)...)
		}
	}
	return out
}

func compared(key, op string, expected, actual any, ok bool, message string) Comparison {
	c := Comparison{Key: key, Op: op, Expected: expected, Actual: actual, OK: ok}
	if !ok {
		c.Message = message
	}
	return c
}

func evaluateKey(op, key string, expected any, actual map[string]any) []Comparison {
	v := valueAt(actual, key)
	var out []Comparison
	switch op {
	case "equals":
		out = append(out, compared(key, op, expected, v, looseEqual(v, expected), fmt.Sprintf("expected == %#v, got %#v", expected, v)))
	case "not_equals":
		out = append(out, compared(key, op, expected, v, !looseEqual(v, expected), fmt.Sprintf("expected != %#v, got %#v", expected, v)))
	case "greater_than", "greater_or_equal", "less_than", "less_or_equal":
		out = append(out, compared(key, op, expected, v, numericCompare(op, v, expected), fmt.Sprintf("expected %s %#v, got %#v", opSymbol(op), expected, v)))
	case "contains":
		out = append(out, compared(key, op, expected, v, containsValue(v, expected), fmt.Sprintf("expected to contain %#v, got %#v", expected, v)))
	case "length_greater_than", "length_at_least":
		length, ok := lengthOf(v)
		threshold, _ := toFloat64(expected)
		pass := ok && (float64(length) > threshold || op == "length_at_least" && float64(length) == threshold)
		out = append(out, compared(key, op, expected, v, pass, fmt.Sprintf("expected length %s %#v, got %#v", opSymbol(op), expected, v)))
	case "each_less_than":
		// A bound on every numeric element of an array -- built for plausibility checks on
		// per-phase arrays, where any single phase exceeding the bound is the fault (e.g. a
		// phase current above 1000 A means a milli-unit reached the wire).
		out = append(out, eachElement(key, op, expected, v, func(item any) bool {
			bound, _ := toFloat64(expected)
			f, ok := toFloat64(item)
			return ok && f < bound
		})...)
	case "each_between":
		out = append(out, eachElement(key, op, expected, v, func(item any) bool { return inRange(item, expected) })...)
	case "each_matches":
		// Every element is a string matching the regular expression as a whole, as the
		// specifications state formats: `each_matches: {identifications: "([0-9A-F]{2}-){5}[0-9A-F]{2}"}`.
		pattern, err := regexp.Compile("^(?:" + fmt.Sprint(expected) + ")$")
		out = append(out, eachElement(key, op, expected, v, func(item any) bool {
			text, isString := item.(string)
			return err == nil && isString && pattern.MatchString(text)
		})...)
	case "between":
		out = append(out, compared(key, op, expected, v, inRange(v, expected), fmt.Sprintf("expected within %v, got %#v", expected, v)))
	case "one_of":
		out = append(out, compared(key, op, expected, v, containsValue(expected, v), fmt.Sprintf("expected one of %v, got %#v", expected, v)))
	case "each_not_null":
		out = append(out, eachField(key, expected, v)...)
	case "some_not_null":
		out = append(out, someField(key, expected, v)...)
	case "sum_matches":
		out = append(out, sumMatches(key, expected, actual))
	case "duration_at_least", "duration_at_most":
		msg := compareDuration(v, expected, op == "duration_at_least")
		out = append(out, compared(key, op, expected, v, msg == "", msg))
	}
	return out
}

func opSymbol(op string) string {
	symbols := map[string]string{
		"greater_than": ">", "greater_or_equal": ">=", "less_than": "<", "less_or_equal": "<=",
		"length_greater_than": ">", "length_at_least": ">=",
	}
	return symbols[op]
}

func numericCompare(op string, actual, expected any) bool {
	a, aok := toFloat64(actual)
	b, _ := toFloat64(expected)
	pass := false
	switch op {
	case "greater_than":
		pass = aok && a > b
	case "greater_or_equal":
		pass = aok && a >= b
	case "less_than":
		pass = aok && a < b
	case "less_or_equal":
		pass = aok && a <= b
	}
	return pass
}

// inRange reports whether v lies within [lo, hi] given as a two-element list.
func inRange(v any, bounds any) bool {
	limits := asSlice(bounds)
	f, ok := toFloat64(v)
	pass := false
	if ok && len(limits) == 2 {
		lo, lok := toFloat64(limits[0])
		hi, hok := toFloat64(limits[1])
		pass = lok && hok && f >= lo && f <= hi
	}
	return pass
}

func eachElement(key, op string, expected, v any, check func(any) bool) []Comparison {
	var out []Comparison
	values, ok := v.([]any)
	if !ok || len(values) == 0 {
		out = append(out, compared(key, op, expected, v, false, fmt.Sprintf("expected a non-empty array, got %#v", v)))
	} else {
		for i, item := range values {
			out = append(out, compared(fmt.Sprintf("%s.%d", key, i), op, expected, item, check(item),
				fmt.Sprintf("expected %s %v, got %#v", op, expected, item)))
		}
	}
	return out
}

// eachField requires every object of an array to carry the named field(s):
// `each_not_null: {use_case_details: version}`.
func eachField(key string, fields, v any) []Comparison {
	var out []Comparison
	names := asSlice(fields)
	if name, ok := fields.(string); ok {
		names = []any{name}
	}
	values, ok := v.([]any)
	if !ok || len(values) == 0 {
		out = append(out, compared(key, "each_not_null", fields, v, false, fmt.Sprintf("expected a non-empty array, got %#v", v)))
	} else {
		for i, item := range values {
			for _, n := range names {
				field, _ := n.(string)
				value := valueAt(asMap(item), field)
				out = append(out, compared(fmt.Sprintf("%s.%d.%s", key, i, field), "each_not_null", nil, value, value != nil && value != "",
					"expected a non-empty value"))
			}
		}
	}
	return out
}

// someField requires, for each named field, at least one object of an array to carry it:
// `some_not_null: {entities: [data.serial_number, "data.brand_name|data.vendor_name"]}`.
// Alternatives separated by | are satisfied by any one of them.
func someField(key string, fields, v any) []Comparison {
	var out []Comparison
	names := asSlice(fields)
	if name, ok := fields.(string); ok {
		names = []any{name}
	}
	values, _ := v.([]any)
	for _, n := range names {
		field, _ := n.(string)
		var found any
		for _, item := range values {
			for _, alternative := range strings.Split(field, "|") {
				if value := valueAt(asMap(item), alternative); found == nil && value != nil && value != "" {
					found = value
				}
			}
		}
		out = append(out, compared(key+".*."+field, "some_not_null", nil, found, found != nil,
			fmt.Sprintf("no element of %s carries %s", key, field)))
	}
	return out
}

// anyNotNull passes when at least one of the listed keys has a value.
func anyNotNull(spec any, actual map[string]any) Comparison {
	var keys []string
	found := map[string]any{}
	for _, k := range asSlice(spec) {
		key, _ := k.(string)
		keys = append(keys, key)
		if v := valueAt(actual, key); v != nil && v != "" {
			found[key] = v
		}
	}
	return compared(strings.Join(keys, " | "), "any_not_null", nil, found, len(found) > 0, "expected at least one non-null value")
}

// sumMatches requires the elements of an array to add up to another field within a
// percentage tolerance: {power_per_phase_w: {total: power_w, tolerance_percent: 30}}. Built
// for total-vs-per-phase consistency, where a device reporting 11 kW total while every phase
// reads 0 W is publishing decoration, not measurements.
func sumMatches(key string, expected any, actual map[string]any) Comparison {
	spec := asMap(expected)
	totalKey, _ := spec["total"].(string)
	tolerance, _ := toFloat64(spec["tolerance_percent"])
	v := valueAt(actual, key)
	values, isList := v.([]any)
	total, totalOK := toFloat64(valueAt(actual, totalKey))
	var c Comparison
	switch {
	case !isList:
		c = compared(key, "sum_matches", expected, v, false, fmt.Sprintf("expected an array, got %#v", v))
	case !totalOK:
		c = compared(key, "sum_matches", expected, v, false, fmt.Sprintf("total field %q is not numeric: %#v", totalKey, valueAt(actual, totalKey)))
	default:
		sum := 0.0
		for _, item := range values {
			f, _ := toFloat64(item)
			sum += f
		}
		allowed := abs(total) * tolerance / 100
		c = compared(key, "sum_matches", expected, sum, abs(sum-total) <= allowed,
			fmt.Sprintf("phases sum to %.1f but %s reports %.1f (allowed deviation %.1f)", sum, totalKey, total, allowed))
	}
	return c
}

func abs(v float64) float64 {
	if v < 0 {
		v = -v
	}
	return v
}

// compareDuration compares ISO-8601 durations ("PT2H"), for announced timing values like the
// LPC failsafe duration window. It returns an empty string when the comparison holds.
func compareDuration(actual, expected any, atLeast bool) string {
	actualStr, _ := actual.(string)
	expectedStr, _ := expected.(string)
	a, errA := iso8601.Parse(actualStr)
	b, errB := iso8601.Parse(expectedStr)
	msg := ""
	switch {
	case errA != nil || errB != nil:
		msg = fmt.Sprintf("expected ISO-8601 durations, got %#v vs %#v", actual, expected)
	case atLeast && a < b:
		msg = fmt.Sprintf("expected duration >= %s, got %s", expectedStr, actualStr)
	case !atLeast && a > b:
		msg = fmt.Sprintf("expected duration <= %s, got %s", expectedStr, actualStr)
	}
	return msg
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func asSlice(v any) []any {
	s, _ := v.([]any)
	return s
}

// looseEqual matches Python's == across YAML-typed expected values (int, bool, string) and
// JSON-decoded actual values (float64, bool, string): 4200 == 4200.0 is true in Python and
// must stay true here even though YAML gives an int and JSON gives a float64.
func looseEqual(actual, expected any) bool {
	af, aok := toFloat64(actual)
	ef, eok := toFloat64(expected)
	equal := false
	if aok && eok {
		equal = af == ef
	} else {
		equal = fmt.Sprint(actual) == fmt.Sprint(expected) && sameKind(actual, expected)
	}
	return equal
}

func sameKind(a, b any) bool {
	_, aBool := a.(bool)
	_, bBool := b.(bool)
	return aBool == bBool
}

func containsValue(container, needle any) bool {
	found := false
	switch c := container.(type) {
	case string:
		s, _ := needle.(string)
		found = strings.Contains(c, s)
	case []any:
		for _, item := range c {
			found = found || looseEqual(item, needle)
		}
	}
	return found
}

func lengthOf(v any) (int, bool) {
	length, ok := 0, true
	switch x := v.(type) {
	case []any:
		length = len(x)
	case string:
		length = len(x)
	case map[string]any:
		length = len(x)
	default:
		ok = false
	}
	return length, ok
}
