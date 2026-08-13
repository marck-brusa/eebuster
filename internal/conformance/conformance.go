// Package conformance checks raw SHIP/SPINE wire frames against the EEBUS JSON encoding and
// datagram rules, so a device that emits structurally broken messages can be caught from the
// outside. It must run on the frame exactly as it arrived from the websocket: the vendored
// ship-go "repairs" malformed JSON with byte-level rewrites (helper.go, JsonFromEEBUSJson)
// before unmarshalling, which destroys the evidence this package exists to report.
//
// EEBUS transmits SPINE datagrams in the "JSON-UTF8" representation defined by SHIP TS 1.0.1
// §11.4: complex-type children map to a JSON array of single-member objects so element order
// survives (§11.4.5 Table 6), an empty element is an empty array (§11.4.6 rule 4), and
// repeated elements become array items (§11.4.6 rule 6). From those rules three structural
// fault classes follow, all of which have been seen from real devices:
//
//   - an object carrying more than one member (element order is lost),
//   - an empty object where an empty array is required,
//   - an empty array as an instance of a repeated element — an instance whose mandatory
//     children are all absent, e.g. an empty value set serialised as
//     "permittedValueSet":[[]] instead of being omitted. Receivers are required to ignore
//     such instances (§11.5.2 rule 1), so the data the device meant to publish never
//     arrives — "a JSON array where an object is expected".
//
// Two shapes are deliberately accepted: a non-empty array directly inside an array is the
// canonical encoding of a repeated complex element (each inner array is one instance), and a
// single-member object where an array-of-one would be canonical carries identical
// information — mainstream stacks (including the vendored eebus-go) emit it for wrappers.
package conformance

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one detected deviation. SpecRef names the requirement it violates so an engineer
// can argue the case with the device vendor from the standard, not from this tool's opinion.
type Finding struct {
	Rule     string   `json:"rule"`
	Severity Severity `json:"severity"`
	Path     string   `json:"path,omitempty"`
	Message  string   `json:"message"`
	SpecRef  string   `json:"spec_ref,omitempty"`
}

const (
	refJSONFormat    = "SHIP TS 1.0.1 §11.4 (XML to JSON transformation)"
	refEmptyElem     = "SHIP TS 1.0.1 §11.4.6 rule 4 (empty elements get an empty JSON array)"
	refCompositors   = "SHIP TS 1.0.1 §11.4.5 Table 6 (complex types map to arrays)"
	refEmptyInstance = "SHIP TS 1.0.1 §11.5.2 rule 1 (receivers MUST ignore empty arrays of repeatable elements)"
	refDatagram      = "SPINE TS ProtocolSpecification 1.3.0 §5.1.2 Table 1 (datagram structure)"
	refHeader        = "SPINE TS ProtocolSpecification 1.3.0 §5.2.7 (header structure)"
	refClassifier    = "SPINE TS ProtocolSpecification 1.3.0 §5.2.4 (cmdClassifier)"
	refCounterRef    = "SPINE TS ProtocolSpecification 1.3.0 §5.2.3.2 (msgCounterReference)"
	refCounter       = "SPINE TS ProtocolSpecification 1.3.0 §5.2.3 (message counter)"
	refOneCmd        = "SPINE TS ProtocolSpecification 1.3.0 §5.3 (payload SHALL contain exactly one cmd)"
	refScaledNum     = "SPINE TS ResourceSpecification 1.3.0 (ScaledNumberType, MeasurementDescriptionData.unit)"
)

var validClassifiers = map[string]bool{
	"read": true, "reply": true, "notify": true, "write": true, "call": true, "result": true,
}

// CheckFrame runs the stateless checks on one raw frame (the websocket message minus its
// 1-byte SHIP header, exactly as logged by ship-go's ws layer). SHIP control messages that
// are not JSON ("ship init") return no findings.
func CheckFrame(payload []byte) []Finding {
	var findings []Finding

	trimmed := payload
	if n := len(trimmed); n > 0 && trimmed[n-1] == 0 {
		stripped := 0
		for len(trimmed) > 0 && trimmed[len(trimmed)-1] == 0 {
			trimmed = trimmed[:len(trimmed)-1]
			stripped++
		}
		findings = append(findings, Finding{
			Rule: "trailing-nul", Severity: SeverityError,
			Message: fmt.Sprintf("message carries %d trailing NUL byte(s) after the JSON document", stripped),
			SpecRef: refJSONFormat,
		})
	}
	if !utf8.Valid(trimmed) {
		return append(findings, Finding{
			Rule: "encoding", Severity: SeverityError,
			Message: "message is not valid UTF-8",
			SpecRef: refJSONFormat,
		})
	}
	if len(trimmed) == 0 || trimmed[0] != '{' {
		// SHIP control payloads like "ship init" are not JSON; nothing to check.
		return findings
	}

	var root any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return append(findings, Finding{
			Rule: "invalid-json", Severity: SeverityError,
			Message: "message is not parseable JSON: " + err.Error(),
			SpecRef: refJSONFormat,
		})
	}

	rootObj, ok := root.(map[string]any)
	if !ok || len(rootObj) != 1 {
		findings = append(findings, Finding{
			Rule: "root-shape", Severity: SeverityError,
			Message: "the message root must be a JSON object carrying exactly one element",
			SpecRef: refJSONFormat,
		})
	}

	findings = append(findings, checkShape(root, "$", true)...)
	findings = append(findings, checkDatagram(Normalize(root))...)
	return findings
}

// checkShape walks the raw wire JSON and reports the structural fault classes. isRoot exempts
// the outermost object from the empty-object rule (an empty message would already fail
// root-shape).
func checkShape(v any, path string, isRoot bool) []Finding {
	var findings []Finding
	switch x := v.(type) {
	case map[string]any:
		if len(x) > 1 {
			findings = append(findings, Finding{
				Rule: "multi-member-object", Severity: SeverityError, Path: path,
				Message: fmt.Sprintf("object carries %d elements; the wire format requires an array of single-member objects so element order is preserved", len(x)),
				SpecRef: refCompositors,
			})
		}
		if len(x) == 0 && !isRoot {
			findings = append(findings, Finding{
				Rule: "empty-object", Severity: SeverityError, Path: path,
				Message: "empty JSON object; an empty element must be encoded as an empty array",
				SpecRef: refEmptyElem,
			})
		}
		for name, child := range x {
			findings = append(findings, checkShape(child, path+"."+name, false)...)
		}
	case []any:
		for i, item := range x {
			itemPath := fmt.Sprintf("%s[%d]", path, i)
			if nested, isArray := item.([]any); isArray && len(nested) == 0 {
				findings = append(findings, Finding{
					Rule: "empty-array-instance", Severity: SeverityError, Path: itemPath,
					Message: "empty JSON array as an instance of a repeated element; an instance with no content must be omitted entirely — receivers are required to ignore it, so whatever the device meant to publish here never arrives",
					SpecRef: refEmptyInstance,
				})
			}
			findings = append(findings, checkShape(item, itemPath, false)...)
		}
	}
	return findings
}

// checkDatagram validates SPINE header/payload semantics on the normalized message. Non-SPINE
// frames (SHIP handshake, access methods) have no "data" member and return nil.
func checkDatagram(norm any) []Finding {
	datagram := dig(norm, "data", "payload", "datagram")
	if datagram == nil {
		return nil
	}
	var findings []Finding

	header, _ := dig(datagram, "header").(map[string]any)
	if header == nil {
		findings = append(findings, Finding{
			Rule: "datagram-header", Severity: SeverityError, Path: "$.datagram",
			Message: "datagram carries no header element",
			SpecRef: refDatagram,
		})
	} else {
		for _, required := range []string{"specificationVersion", "addressSource", "addressDestination", "msgCounter", "cmdClassifier"} {
			if _, present := header[required]; !present {
				findings = append(findings, Finding{
					Rule: "datagram-header", Severity: SeverityError, Path: "$.datagram.header." + required,
					Message: "mandatory header element " + required + " is missing",
					SpecRef: refHeader,
				})
			}
		}
		classifier, _ := header["cmdClassifier"].(string)
		if classifier != "" && !validClassifiers[classifier] {
			findings = append(findings, Finding{
				Rule: "cmd-classifier", Severity: SeverityError, Path: "$.datagram.header.cmdClassifier",
				Message: fmt.Sprintf("cmdClassifier %q is not a permitted value (read, reply, notify, write, call, result)", classifier),
				SpecRef: refClassifier,
			})
		}
		if _, hasRef := header["msgCounterReference"]; (classifier == "reply" || classifier == "result") && !hasRef {
			findings = append(findings, Finding{
				Rule: "msg-counter-ref", Severity: SeverityError, Path: "$.datagram.header.msgCounterReference",
				Message: "a " + classifier + " message must reference the msgCounter of the message it answers",
				SpecRef: refCounterRef,
			})
		}
	}

	payload, _ := dig(datagram, "payload").(map[string]any)
	if payload == nil {
		findings = append(findings, Finding{
			Rule: "datagram-payload", Severity: SeverityError, Path: "$.datagram",
			Message: "datagram carries no payload element",
			SpecRef: refDatagram,
		})
		return findings
	}
	if cmds, ok := payload["cmd"].([]any); ok && len(cmds) > 1 {
		findings = append(findings, Finding{
			Rule: "multi-cmd", Severity: SeverityError, Path: "$.datagram.payload.cmd",
			Message: fmt.Sprintf("payload carries %d cmd instances; this protocol version requires exactly one", len(cmds)),
			SpecRef: refOneCmd,
		})
	}
	return findings
}

// Normalize converts wire-format JSON (arrays of single-member objects) into plain nested
// maps for navigation. An array whose items are all single-member objects with distinct
// names merges into one object; anything else (primitive lists like entity addresses, or
// repeated elements like the entries of a ListData) stays a slice of normalized items.
// Structurally broken input passes through unchanged — checkShape reports it separately.
func Normalize(v any) any {
	switch x := v.(type) {
	case []any:
		if merged, ok := mergeElementList(x); ok {
			return merged
		}
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = Normalize(item)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for name, child := range x {
			out[name] = Normalize(child)
		}
		return out
	default:
		return v
	}
}

func mergeElementList(items []any) (map[string]any, bool) {
	if len(items) == 0 {
		return nil, false
	}
	merged := make(map[string]any, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]any)
		if !ok || len(obj) != 1 {
			return nil, false
		}
		for name, child := range obj {
			if _, duplicate := merged[name]; duplicate {
				return nil, false // repeated element: a real list, keep it as one
			}
			merged[name] = Normalize(child)
		}
	}
	return merged, true
}

// dig walks nested normalized maps; any missing or non-map step returns nil.
func dig(v any, path ...string) any {
	current := v
	for _, name := range path {
		obj, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current, ok = obj[name]
		if !ok {
			return nil
		}
	}
	return current
}

// scaledNumber extracts number*10^scale from a normalized ScaledNumberType map.
func scaledNumber(v any) (float64, bool) {
	obj, ok := v.(map[string]any)
	if !ok {
		return 0, false
	}
	number, ok := obj["number"].(float64)
	if !ok {
		return 0, false
	}
	scale := 0.0
	if s, ok := obj["scale"].(float64); ok {
		scale = s
	}
	return number * pow10(scale), true
}

func pow10(exp float64) float64 {
	result := 1.0
	step := 10.0
	if exp < 0 {
		step = 0.1
		exp = -exp
	}
	for i := 0; i < int(exp); i++ {
		result *= step
	}
	return result
}

// elementList returns the entries of a normalized ListData child in both shapes normalize can
// produce: a single entry (merged map) or repeated entries (slice of maps).
func elementList(v any) []map[string]any {
	switch x := v.(type) {
	case map[string]any:
		return []map[string]any{x}
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, item := range x {
			normalized := Normalize(item)
			if obj, ok := normalized.(map[string]any); ok {
				out = append(out, obj)
			}
		}
		return out
	}
	return nil
}
