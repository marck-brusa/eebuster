package eebusgo

import (
	"fmt"
	"sort"
	"strings"

	"github.com/enbility/eebus-go/features/client"
	ucapi "github.com/enbility/eebus-go/usecases/api"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// eebus-go's per-phase readers and writers only iterate phases a, b and c. A device that
// declares its current limit on the combined phase "abc" (a wireless pad, a DC charger) has
// limits on the wire that those readers cannot see, and a write naming a/b/c resolves to
// nothing. The helpers here find the phases a device actually declares, with the same
// limit-description <-> electrical-parameter pairing eebus-go's writer uses.

// The limit-description filters upstream's cem/opev and cem/oscev use.
var (
	opevLimitFilter = spinemodel.LoadControlLimitDescriptionDataType{
		LimitType:     util.Ptr(spinemodel.LoadControlLimitTypeTypeMaxValueLimit),
		LimitCategory: util.Ptr(spinemodel.LoadControlCategoryTypeObligation),
		Unit:          util.Ptr(spinemodel.UnitOfMeasurementTypeA),
		ScopeType:     util.Ptr(spinemodel.ScopeTypeTypeOverloadProtection),
	}
	oscevLimitFilter = spinemodel.LoadControlLimitDescriptionDataType{
		LimitType:     util.Ptr(spinemodel.LoadControlLimitTypeTypeMaxValueLimit),
		LimitCategory: util.Ptr(spinemodel.LoadControlCategoryTypeRecommendation),
		Unit:          util.Ptr(spinemodel.UnitOfMeasurementTypeA),
		ScopeType:     util.Ptr(spinemodel.ScopeTypeTypeSelfConsumption),
	}
)

// eachPhase in a write means "every phase the device declares for this limit".
const eachPhase = "each"

// limitSlot is one limit the device declares: the phase it applies to, its limit id, and the
// electrical parameter that carries its permitted values.
type limitSlot struct {
	phase       spinemodel.ElectricalConnectionPhaseNameType
	limitId     spinemodel.LoadControlLimitIdType
	parameterId spinemodel.ElectricalConnectionParameterIdType
}

// PhaseNotDeclaredError reports a write none of whose phases the device declares.
type PhaseNotDeclaredError struct {
	Requested []string
	Declared  []string
}

func (e *PhaseNotDeclaredError) Error() string {
	return fmt.Sprintf("the device declares this limit on phase(s) %s, not on %s; write it with phase %q, or %q for every declared phase",
		strings.Join(e.Declared, ", "), strings.Join(e.Requested, ", "), e.Declared[0], eachPhase)
}

// matchLimitSlots pairs each limit description with the electrical parameter that shares its
// measurement id. Like upstream, the first pairing found for a phase wins. The result is
// ordered a, b, c, then any other phase name.
func matchLimitSlots(limitDescs []spinemodel.LoadControlLimitDescriptionDataType,
	paramDescs []spinemodel.ElectricalConnectionParameterDescriptionDataType) []limitSlot {
	seen := map[spinemodel.ElectricalConnectionPhaseNameType]bool{}
	slots := []limitSlot{}
	for _, pd := range paramDescs {
		if pd.AcMeasuredPhases == nil || pd.MeasurementId == nil || pd.ParameterId == nil || seen[*pd.AcMeasuredPhases] {
			continue
		}
		for _, ld := range limitDescs {
			if ld.MeasurementId == nil || ld.LimitId == nil || *ld.MeasurementId != *pd.MeasurementId {
				continue
			}
			seen[*pd.AcMeasuredPhases] = true
			slots = append(slots, limitSlot{phase: *pd.AcMeasuredPhases, limitId: *ld.LimitId, parameterId: *pd.ParameterId})
			break
		}
	}
	sort.SliceStable(slots, func(i, j int) bool { return phaseRank(slots[i].phase) < phaseRank(slots[j].phase) })
	return slots
}

func phaseRank(phase spinemodel.ElectricalConnectionPhaseNameType) int {
	for i, p := range ucapi.PhaseNameMapping {
		if p == phase {
			return i
		}
	}
	return len(ucapi.PhaseNameMapping)
}

func isPerPhase(phase spinemodel.ElectricalConnectionPhaseNameType) bool {
	return phaseRank(phase) < len(ucapi.PhaseNameMapping)
}

func slotPhases(slots []limitSlot) []string {
	out := make([]string, 0, len(slots))
	for _, s := range slots {
		out = append(out, string(s.phase))
	}
	return out
}

// declaredLimitSlots reads the limits the remote entity declares for the given filter from
// the local cache of its data. Nil if the entity has not published the descriptions (yet).
func declaredLimitSlots(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface,
	filter spinemodel.LoadControlLimitDescriptionDataType) []limitSlot {
	lc, err := client.NewLoadControl(localEntity, entity)
	ec, err2 := client.NewElectricalConnection(localEntity, entity)
	if err != nil || err2 != nil {
		return nil
	}
	limitDescs, err := lc.GetLimitDescriptionsForFilter(filter)
	if err != nil {
		return nil
	}
	paramDescs, err := ec.GetParameterDescriptionsForFilter(spinemodel.ElectricalConnectionParameterDescriptionDataType{})
	if err != nil {
		return nil
	}
	return matchLimitSlots(limitDescs, paramDescs)
}

// combinedPhaseLimits reads the limits upstream's reader skips: those on a phase outside
// a/b/c. Upstream's reporting rule applies -- an inactive or valueless limit reads as the
// permitted maximum, and is left out when there is none.
func combinedPhaseLimits(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface, slots []limitSlot) []PhaseLimit {
	lc, err := client.NewLoadControl(localEntity, entity)
	ec, err2 := client.NewElectricalConnection(localEntity, entity)
	if err != nil || err2 != nil {
		return nil
	}
	out := []PhaseLimit{}
	for _, slot := range slots {
		if isPerPhase(slot.phase) {
			continue
		}
		data, err := lc.GetLimitDataForId(slot.limitId)
		if err != nil {
			continue
		}
		active := data.IsLimitActive != nil && *data.IsLimitActive
		var value float64
		if data.Value == nil || !active {
			_, max, _, err := ec.GetPermittedValueDataForFilter(spinemodel.ElectricalConnectionPermittedValueSetDataType{ParameterId: util.Ptr(slot.parameterId)})
			if err != nil {
				continue
			}
			value = max
		} else {
			value = data.Value.GetValue()
		}
		out = append(out, PhaseLimit{
			Phase: string(slot.phase), ValueA: value, IsActive: active,
			IsChangeable: data.IsLimitChangeable != nil && *data.IsLimitChangeable,
		})
	}
	return out
}

// combinedPhaseConstraints reads the permitted min/max/default currents of the limits on a
// phase outside a/b/c, index-aligned with those phases.
func combinedPhaseConstraints(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface, slots []limitSlot) (CurrentConstraints, bool) {
	c := CurrentConstraints{MinA: []float64{}, MaxA: []float64{}, DefaultA: []float64{}}
	ec, err := client.NewElectricalConnection(localEntity, entity)
	if err != nil {
		return c, false
	}
	for _, slot := range slots {
		if isPerPhase(slot.phase) {
			continue
		}
		min, max, def, err := ec.GetPermittedValueDataForFilter(spinemodel.ElectricalConnectionPermittedValueSetDataType{ParameterId: util.Ptr(slot.parameterId)})
		if err != nil {
			continue
		}
		c.MinA, c.MaxA, c.DefaultA = append(c.MinA, min), append(c.MaxA, max), append(c.DefaultA, def)
	}
	return c, len(c.MaxA) > 0
}

// expandEachPhase replaces a single {phase: "each"} entry by one entry per declared phase,
// carrying its value and state. Other requests pass through unchanged.
func expandEachPhase(limits []PhaseLimit, declared []string) ([]PhaseLimit, error) {
	eachAt := -1
	for i, l := range limits {
		if strings.EqualFold(strings.TrimSpace(l.Phase), eachPhase) {
			eachAt = i
		}
	}
	if eachAt < 0 {
		return limits, nil
	}
	if len(limits) != 1 {
		return nil, fmt.Errorf("phase %q stands for every declared phase and cannot be combined with other entries", eachPhase)
	}
	if len(declared) == 0 {
		return nil, fmt.Errorf("phase %q needs the device's limit descriptions, and it has published none on this entity (is a vehicle connected?)", eachPhase)
	}
	out := make([]PhaseLimit, 0, len(declared))
	for _, phase := range declared {
		out = append(out, PhaseLimit{Phase: phase, ValueA: limits[eachAt].ValueA, IsActive: limits[eachAt].IsActive})
	}
	return out, nil
}

// checkDeclared fails a write none of whose phases the device declares: upstream would drop
// every entry and report only "missing data".
func checkDeclared(in []ucapi.LoadLimitsPhase, slots []limitSlot) error {
	if len(slots) == 0 {
		return nil
	}
	requested := make([]string, 0, len(in))
	for _, l := range in {
		for _, s := range slots {
			if s.phase == l.Phase {
				return nil
			}
		}
		requested = append(requested, string(l.Phase))
	}
	return &PhaseNotDeclaredError{Requested: requested, Declared: slotPhases(slots)}
}

// readPhaseLimits builds the shared OPEV/OSCEV read: upstream's per-phase view, falling back
// to the combined phase when upstream sees nothing, plus the phases the device declares.
func readPhaseLimits(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface,
	filter spinemodel.LoadControlLimitDescriptionDataType,
	limits func() ([]ucapi.LoadLimitsPhase, error),
	constraints func() ([]float64, []float64, []float64, error)) map[string]any {
	refresh(append(limitDataRead(localEntity, entity), permittedValuesRead(localEntity, entity)...)...)
	slots := declaredLimitSlots(localEntity, entity, filter)
	out := map[string]any{"phases": slotPhases(slots)}
	if l, err := limits(); err == nil && len(l) > 0 {
		out["limits"] = phaseLimitsOut(l)
	} else if combined := combinedPhaseLimits(localEntity, entity, slots); len(combined) > 0 {
		out["limits"] = combined
	}
	if min, max, def, err := constraints(); err == nil {
		out["constraints"] = CurrentConstraints{MinA: min, MaxA: max, DefaultA: def}
	} else if c, ok := combinedPhaseConstraints(localEntity, entity, slots); ok {
		out["constraints"] = c
	}
	return out
}

// writePhaseLimits builds the shared OPEV/OSCEV write and returns the entries it sent.
func writePhaseLimits(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface,
	filter spinemodel.LoadControlLimitDescriptionDataType, limits []PhaseLimit,
	write func([]ucapi.LoadLimitsPhase) error) ([]PhaseLimit, error) {
	slots := declaredLimitSlots(localEntity, entity, filter)
	expanded, err := expandEachPhase(limits, slotPhases(slots))
	if err != nil {
		return nil, err
	}
	in, err := phaseLimitsIn(expanded)
	if err != nil {
		return nil, err
	}
	if err := checkDeclared(in, slots); err != nil {
		return nil, err
	}
	if err := write(in); err != nil {
		return nil, err
	}
	return expanded, nil
}
