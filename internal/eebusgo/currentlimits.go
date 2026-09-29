package eebusgo

import (
	"fmt"
	"strings"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/cem/opev"
	"github.com/enbility/eebus-go/usecases/cem/oscev"
	spinemodel "github.com/enbility/spine-go/model"
)

// OPEV and OSCEV are twins: per-phase charging-current limits towards an EV entity, OPEV
// writing *obligations* (overload protection -- the EV must not exceed them) and OSCEV
// writing *recommendations* (self-consumption optimization -- the EV may follow them).
// Both wrap the upstream cem/{opev,oscev} clients; the REST shapes are shared.

// PhaseLimit is one per-phase current limit in the REST shape. Phase is "a", "b", "c" or the
// combined "abc"; a write may also use "each" for every phase the device declares.
type PhaseLimit struct {
	Phase        string  `json:"phase"`
	ValueA       float64 `json:"value_a"`
	IsActive     bool    `json:"is_active"`
	IsChangeable bool    `json:"is_changeable,omitempty"` // read side only; ignored on write
}

// CurrentConstraints carries the EV's declared per-phase current boundaries, index-aligned
// with the declared phases as far as the device reports them.
type CurrentConstraints struct {
	MinA     []float64 `json:"min_a"`
	MaxA     []float64 `json:"max_a"`
	DefaultA []float64 `json:"default_a"`
}

type OPEV struct{ uc *opev.OPEV }

func (o *OPEV) Read(ski string, entityHint []uint) (map[string]any, error) {
	entity, err := resolveEntity(o.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	limits := func() ([]ucapi.LoadLimitsPhase, error) { return o.uc.LoadControlLimits(entity) }
	constraints := func() ([]float64, []float64, []float64, error) { return o.uc.CurrentLimits(entity) }
	return readPhaseLimits(o.uc.LocalEntity, entity, opevLimitFilter, limits, constraints), nil
}

// WriteLimits writes the limits and returns the entries it sent, with a {phase: "each"}
// entry expanded to the phases the device declares.
func (o *OPEV) WriteLimits(ski string, limits []PhaseLimit, entityHint []uint) ([]PhaseLimit, error) {
	entity, err := resolveEntity(o.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	write := func(in []ucapi.LoadLimitsPhase) error {
		_, err := o.uc.WriteLoadControlLimits(entity, in, nil)
		return err
	}
	return writePhaseLimits(o.uc.LocalEntity, entity, opevLimitFilter, limits, write)
}

// Heartbeat controls OPEV scenario 2 ("EV checks Energy Guard availability"): the EV must
// fall to a safe current when our heartbeat stays away for more than 4 s (OPEV-005).
func (o *OPEV) StartHeartbeat() { o.uc.StartHeartbeat() }
func (o *OPEV) StopHeartbeat()  { o.uc.StopHeartbeat() }

// SetOperatingState drives OPEV scenario 3 ("Energy Guard sends error state"): announcing
// failure means the EV must stop trusting our curtailment and fall to a safe current
// (OPEV-007). Announced device-wide, so it also affects other use cases we serve.
func (o *OPEV) SetOperatingState(failure bool) error { return o.uc.SetOperatingState(failure) }

type OSCEV struct{ uc *oscev.OSCEV }

func (o *OSCEV) Read(ski string, entityHint []uint) (map[string]any, error) {
	entity, err := resolveEntity(o.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	limits := func() ([]ucapi.LoadLimitsPhase, error) { return o.uc.LoadControlLimits(entity) }
	constraints := func() ([]float64, []float64, []float64, error) { return o.uc.CurrentLimits(entity) }
	return readPhaseLimits(o.uc.LocalEntity, entity, oscevLimitFilter, limits, constraints), nil
}

// WriteLimits writes the limits and returns the entries it sent, with a {phase: "each"}
// entry expanded to the phases the device declares.
func (o *OSCEV) WriteLimits(ski string, limits []PhaseLimit, entityHint []uint) ([]PhaseLimit, error) {
	entity, err := resolveEntity(o.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	write := func(in []ucapi.LoadLimitsPhase) error {
		_, err := o.uc.WriteLoadControlLimits(entity, in, nil)
		return err
	}
	return writePhaseLimits(o.uc.LocalEntity, entity, oscevLimitFilter, limits, write)
}

func phaseLimitsOut(limits []ucapi.LoadLimitsPhase) []PhaseLimit {
	out := make([]PhaseLimit, 0, len(limits))
	for _, l := range limits {
		out = append(out, PhaseLimit{
			Phase: string(l.Phase), ValueA: l.Value,
			IsActive: l.IsActive, IsChangeable: l.IsChangeable,
		})
	}
	return out
}

func phaseLimitsIn(limits []PhaseLimit) ([]ucapi.LoadLimitsPhase, error) {
	if len(limits) == 0 {
		return nil, fmt.Errorf("limits must carry at least one phase entry")
	}
	in := make([]ucapi.LoadLimitsPhase, 0, len(limits))
	for _, l := range limits {
		phase, err := phaseName(l.Phase)
		if err != nil {
			return nil, err
		}
		if l.ValueA < 0 {
			return nil, fmt.Errorf("phase %s: a negative current limit is not a thing", l.Phase)
		}
		in = append(in, ucapi.LoadLimitsPhase{Phase: phase, Value: l.ValueA, IsActive: l.IsActive})
	}
	return in, nil
}

func phaseName(s string) (spinemodel.ElectricalConnectionPhaseNameType, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "a", "l1", "1":
		return spinemodel.ElectricalConnectionPhaseNameTypeA, nil
	case "b", "l2", "2":
		return spinemodel.ElectricalConnectionPhaseNameTypeB, nil
	case "c", "l3", "3":
		return spinemodel.ElectricalConnectionPhaseNameTypeC, nil
	case "abc", "all", "total":
		// A device that measures one combined phase (a wireless pad, a DC charger) declares
		// acMeasuredPhases "abc"; a limit has to be written against that same phase name.
		return spinemodel.ElectricalConnectionPhaseNameTypeAbc, nil
	}
	return "", fmt.Errorf("unknown phase %q (use a, b, c, abc or each)", s)
}

// The OSCEV twin of the scenario 2/3 controls above.
func (o *OSCEV) StartHeartbeat() { o.uc.StartHeartbeat() }
func (o *OSCEV) StopHeartbeat()  { o.uc.StopHeartbeat() }

func (o *OSCEV) SetOperatingState(failure bool) error { return o.uc.SetOperatingState(failure) }
