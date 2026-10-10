package scenario

import (
	"fmt"
	"strings"
	"time"

	"github.com/marck-brusa/eebuster/internal/ucspec"
)

// gap is one unmet requirement of a test case, and why it skips.
type gap struct {
	Reason string
	Detail string
}

// missingRequirements returns unmet live requirements so a non-applicable scenario is
// skipped, not failed, matching cli/scenario.py's _missing_requirements.
func (rn *Runner) missingRequirements(req requirementsSpec, scenarioContext map[string]any) []gap {
	var gaps []gap
	ski, _ := lookup("peer.ski", scenarioContext)
	skiStr, _ := ski.(string)

	if len(req.Capabilities) > 0 {
		gaps = append(gaps, rn.missingCapabilities(req.Capabilities)...)
	}
	needsDiscovery := len(req.UseCases) > 0 || len(req.Scenarios) > 0
	if needsDiscovery && skiStr == "" {
		gaps = append(gaps, gap{ReasonPreconditionNotMet, "no peer selected for use-case requirements"})
	} else if needsDiscovery {
		gaps = append(gaps, rn.missingAdvertisement(req, skiStr)...)
	}
	if len(req.OpevPhases) > 0 && skiStr == "" {
		gaps = append(gaps, gap{ReasonPreconditionNotMet, "no peer selected for phase requirements"})
	} else if len(req.OpevPhases) > 0 {
		if declared, ok := rn.declaredOpevPhases(skiStr); ok {
			gaps = append(gaps, missingPhases(req.OpevPhases, declared)...)
		}
	}
	if (req.Vehicle || req.Charging) && skiStr != "" {
		gaps = append(gaps, rn.missingVehicle(req, skiStr)...)
	}
	for _, name := range req.Parameters {
		if v, _ := lookup("params."+name, scenarioContext); v == nil {
			gaps = append(gaps, gap{ReasonPreconditionNotMet,
				fmt.Sprintf("parameter %s is not configured for this peer and could not be derived from the device", name)})
		}
	}
	return gaps
}

func (rn *Runner) missingCapabilities(required []string) []gap {
	var gaps []gap
	stacks, err := rn.getJSON("/api/v1/stacks")
	var available map[string]any
	if err == nil {
		for _, s := range stacks {
			m, ok := s.(map[string]any)
			if ok && m["status"] == "running" && available == nil {
				if caps, ok := m["capabilities"].(map[string]any); ok && caps["live_control"] == true {
					available = caps
				}
			}
		}
	}
	for _, capability := range required {
		if available == nil || available[capability] != true {
			gaps = append(gaps, gap{ReasonCapabilityMissing, "active stack lacks " + capability})
		}
	}
	return gaps
}

// missingAdvertisement checks the use cases a test requires, and the scenarios of its
// spec.use_case it exercises, against what the peer advertises.
func (rn *Runner) missingAdvertisement(req requirementsSpec, ski string) []gap {
	var gaps []gap
	var advertised []map[string]any
	if err := rn.getInto("/api/v1/peers/"+ski+"/usecases", &advertised); err != nil {
		gaps = append(gaps, gap{ReasonUseCaseNotAdvertised, "could not browse peer use cases: " + err.Error()})
	} else {
		support := advertisedScenarios(advertised)
		for _, name := range req.UseCases {
			if _, ok := support[name]; !ok {
				gaps = append(gaps, gap{ReasonUseCaseNotAdvertised, "peer does not advertise " + name})
			}
		}
		gaps = append(gaps, missingScenarioGaps(req, support)...)
	}
	return gaps
}

func missingScenarioGaps(req requirementsSpec, support map[string][]uint) []gap {
	var gaps []gap
	uc, known := ucspec.Lookup(req.useCase)
	scenarios, advertisedUC := support[uc.Name]
	if len(req.Scenarios) > 0 && !known {
		gaps = append(gaps, gap{ReasonPreconditionNotMet, "requires.scenarios needs spec.use_case to name a known use case"})
	} else if len(req.Scenarios) > 0 && !advertisedUC {
		gaps = append(gaps, gap{ReasonUseCaseNotAdvertised, "peer does not advertise " + uc.Name})
	} else if len(req.Scenarios) > 0 {
		have := map[uint]bool{}
		for _, n := range scenarios {
			have[n] = true
		}
		for _, n := range req.Scenarios {
			s, _ := uc.Scenario(n)
			// A mandatory scenario is checked even when it is not advertised: the device has to
			// implement it, and whether the data is there is exactly what the test finds out.
			if !have[n] && s.Device != ucspec.Mandatory {
				gaps = append(gaps, gap{ReasonScenarioNotAdvertised,
					fmt.Sprintf("%s scenario %d (%s) is not advertised; it is %s for the %s",
						uc.Acronym, n, s.Title, levelWord(s.Device), uc.DeviceActor)})
			}
		}
	}
	return gaps
}

// advertisedScenarios maps every available advertised use case to the scenario numbers its
// entities claim.
func advertisedScenarios(advertised []map[string]any) map[string][]uint {
	support := map[string][]uint{}
	for _, entry := range advertised {
		list, _ := entry["useCaseSupport"].([]any)
		for _, s := range list {
			sm, ok := s.(map[string]any)
			name, _ := sm["useCaseName"].(string)
			if ok && sm["useCaseAvailable"] != false && name != "" {
				numbers := support[name]
				for _, n := range asSlice(sm["scenarioSupport"]) {
					if f, ok := toFloat64(n); ok {
						numbers = append(numbers, uint(f))
					}
				}
				if numbers == nil {
					numbers = []uint{}
				}
				support[name] = numbers
			}
		}
	}
	return support
}

func levelWord(level ucspec.Level) string {
	word := "optional"
	switch level {
	case ucspec.Mandatory:
		word = "mandatory"
	case ucspec.Recommended:
		word = "recommended"
	case ucspec.NotApplicable:
		word = "not applicable"
	}
	return word
}

// chargingResumeWait is how long a connected vehicle that is not charging gets to resume: the
// previous test case may just have released a pause, and the vehicle's charge state arrives
// a moment after its current does.
const chargingResumeWait = 10 * time.Second

func (rn *Runner) missingVehicle(req requirementsSpec, ski string) []gap {
	var gaps []gap
	var snapshot struct {
		EV struct {
			ConnectedCount int `json:"connected_count"`
			ChargingCount  int `json:"charging_count"`
		} `json:"ev"`
	}
	deadline := time.Now().Add(chargingResumeWait)
	err := rn.getInto("/api/v1/energy/"+ski+"/snapshot", &snapshot)
	for err == nil && req.Charging && snapshot.EV.ConnectedCount > 0 && snapshot.EV.ChargingCount == 0 && time.Now().Before(deadline) && !rn.cancelled() {
		rn.pause(time.Second)
		err = rn.getInto("/api/v1/energy/"+ski+"/snapshot", &snapshot)
	}
	switch {
	case err != nil:
		gaps = append(gaps, gap{ReasonPreconditionNotMet, "could not read the energy snapshot: " + err.Error()})
	case req.Vehicle && snapshot.EV.ConnectedCount == 0:
		gaps = append(gaps, gap{ReasonPreconditionNotMet, "no vehicle is connected"})
	case req.Charging && snapshot.EV.ChargingCount == 0:
		gaps = append(gaps, gap{ReasonPreconditionNotMet, "no charging session is running"})
	}
	return gaps
}

// declaredOpevPhases asks for the phases the peer declares its OPEV limits on. Not knowing
// them (no vehicle, no descriptions yet) is no reason to skip: the scenario then runs and
// fails on its own, which is what it documents.
func (rn *Runner) declaredOpevPhases(ski string) ([]string, bool) {
	var body struct {
		Phases []string `json:"phases"`
	}
	err := rn.getInto("/api/v1/opev/"+ski, &body)
	return body.Phases, err == nil && len(body.Phases) > 0
}

func missingPhases(required, declared []string) []gap {
	have := map[string]bool{}
	for _, p := range declared {
		have[p] = true
	}
	var absent []string
	for _, p := range required {
		if !have[p] {
			absent = append(absent, p)
		}
	}
	var gaps []gap
	if len(absent) > 0 {
		gaps = append(gaps, gap{ReasonPhasesNotDeclared,
			fmt.Sprintf("peer declares its OPEV limits on phase(s) %s, not %s", strings.Join(declared, ", "), strings.Join(absent, ", "))})
	}
	return gaps
}
