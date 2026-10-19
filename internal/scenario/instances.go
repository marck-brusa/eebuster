package scenario

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// stepUseCaseInstances counts the entities of the device that advertise each named use case
// and allows at most at_most of them (default 1):
// `use_case_instances: {use_cases: [LPC, MPC], at_most: 1}`. A counterpart that controls or
// monitors through one instance only cannot tell which of several to use.
func (rn *Runner) stepUseCaseInstances(args any, scenarioContext map[string]any) StepResult {
	a := argsMap(args)
	atMost, ok := toFloat64(a["at_most"])
	if !ok {
		atMost = 1
	}
	ski, _ := lookup("peer.ski", scenarioContext)
	step := StepResult{Name: "use_case_instances"}
	evidence, raw, err := rn.do(http.MethodGet, fmt.Sprintf("/api/v1/peers/%v/usecases", ski), nil)
	step.Request = evidence
	var advertised []map[string]any
	if err == nil && evidence.Status < 400 {
		err = json.Unmarshal(raw, &advertised)
	} else if err == nil {
		err = fmt.Errorf("HTTP %d", evidence.Status)
	}
	if err != nil {
		step.Status, step.Detail = StatusFailed, err.Error()
	} else {
		step.Status, step.Detail, step.Assertions = judgeInstances(asSlice(a["use_cases"]), advertisedInstances(advertised), atMost)
	}
	return step
}

func judgeInstances(useCases []any, instances map[string][]string, atMost float64) (string, string, []Comparison) {
	status := StatusPassed
	var parts []string
	var comparisons []Comparison
	for _, item := range useCases {
		key := fmt.Sprint(item)
		name, label := key, key
		if uc, known := ucspec.Lookup(key); known {
			name, label = uc.Name, uc.Acronym
		}
		entities := instances[name]
		ok := float64(len(entities)) <= atMost
		comparisons = append(comparisons, Comparison{Key: label + " instances", Op: "less_or_equal", Expected: atMost, Actual: len(entities), OK: ok})
		part := fmt.Sprintf("%s %d", label, len(entities))
		if len(entities) > 0 {
			part += " (entity " + strings.Join(entities, ", ") + ")"
		}
		parts = append(parts, part)
		if !ok {
			status = StatusFailed
		}
	}
	return status, "instances: " + strings.Join(parts, "; "), comparisons
}

// advertisedInstances maps each available use case to the addresses of the entities that
// advertise it, each address once.
func advertisedInstances(advertised []map[string]any) map[string][]string {
	instances := map[string][]string{}
	for _, entry := range advertised {
		address := fmt.Sprint(entry["address"])
		for _, s := range asSlice(entry["useCaseSupport"]) {
			sm, ok := s.(map[string]any)
			name, _ := sm["useCaseName"].(string)
			if ok && sm["useCaseAvailable"] != false && name != "" && !containsString(instances[name], address) {
				instances[name] = append(instances[name], address)
			}
		}
	}
	return instances
}

func containsString(list []string, s string) bool {
	found := false
	for _, item := range list {
		found = found || item == s
	}
	return found
}
