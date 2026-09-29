package eebusgo

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	spinemodel "github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

func limitDesc(limitId, measurementId uint) spinemodel.LoadControlLimitDescriptionDataType {
	return spinemodel.LoadControlLimitDescriptionDataType{
		LimitId:       util.Ptr(spinemodel.LoadControlLimitIdType(limitId)),
		MeasurementId: util.Ptr(spinemodel.MeasurementIdType(measurementId)),
	}
}

func paramDesc(parameterId, measurementId uint, phase spinemodel.ElectricalConnectionPhaseNameType) spinemodel.ElectricalConnectionParameterDescriptionDataType {
	return spinemodel.ElectricalConnectionParameterDescriptionDataType{
		ParameterId:      util.Ptr(spinemodel.ElectricalConnectionParameterIdType(parameterId)),
		MeasurementId:    util.Ptr(spinemodel.MeasurementIdType(measurementId)),
		AcMeasuredPhases: util.Ptr(phase),
	}
}

func TestMatchLimitSlotsCombinedPhase(t *testing.T) {
	// A combined-phase device: one limit on the current measurement, whose parameter declares
	// "abc"; a power parameter on another measurement must not be picked up.
	slots := matchLimitSlots(
		[]spinemodel.LoadControlLimitDescriptionDataType{limitDesc(0, 0)},
		[]spinemodel.ElectricalConnectionParameterDescriptionDataType{
			paramDesc(7, 1, spinemodel.ElectricalConnectionPhaseNameTypeAbc),
			paramDesc(1, 0, spinemodel.ElectricalConnectionPhaseNameTypeAbc),
		})
	want := []limitSlot{{phase: spinemodel.ElectricalConnectionPhaseNameTypeAbc, limitId: 0, parameterId: 1}}
	if !reflect.DeepEqual(slots, want) {
		t.Fatalf("slots = %+v, want %+v", slots, want)
	}
}

func TestMatchLimitSlotsOrdersPerPhaseFirst(t *testing.T) {
	slots := matchLimitSlots(
		[]spinemodel.LoadControlLimitDescriptionDataType{limitDesc(0, 10), limitDesc(1, 11), limitDesc(2, 12)},
		[]spinemodel.ElectricalConnectionParameterDescriptionDataType{
			paramDesc(3, 12, spinemodel.ElectricalConnectionPhaseNameTypeC),
			paramDesc(1, 10, spinemodel.ElectricalConnectionPhaseNameTypeA),
			paramDesc(2, 11, spinemodel.ElectricalConnectionPhaseNameTypeB),
		})
	if got := slotPhases(slots); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("phases = %v, want a, b, c", got)
	}
	if slots[0].limitId != 0 || slots[0].parameterId != 1 {
		t.Errorf("phase a paired wrongly: %+v", slots[0])
	}
}

func TestMatchLimitSlotsSkipsIncompleteDescriptions(t *testing.T) {
	noPhase := paramDesc(1, 0, spinemodel.ElectricalConnectionPhaseNameTypeA)
	noPhase.AcMeasuredPhases = nil
	noLimitId := limitDesc(0, 0)
	noLimitId.LimitId = nil
	if slots := matchLimitSlots([]spinemodel.LoadControlLimitDescriptionDataType{limitDesc(0, 0)},
		[]spinemodel.ElectricalConnectionParameterDescriptionDataType{noPhase}); len(slots) != 0 {
		t.Errorf("parameter without a phase must not form a slot: %+v", slots)
	}
	if slots := matchLimitSlots([]spinemodel.LoadControlLimitDescriptionDataType{noLimitId},
		[]spinemodel.ElectricalConnectionParameterDescriptionDataType{paramDesc(1, 0, spinemodel.ElectricalConnectionPhaseNameTypeA)}); len(slots) != 0 {
		t.Errorf("limit without an id must not form a slot: %+v", slots)
	}
	if slots := matchLimitSlots(nil, nil); slots == nil || len(slots) != 0 {
		t.Errorf("no descriptions must give an empty, non-nil list: %#v", slots)
	}
}

func TestExpandEachPhase(t *testing.T) {
	got, err := expandEachPhase([]PhaseLimit{{Phase: "Each", ValueA: 10, IsActive: true}}, []string{"abc"})
	if err != nil || !reflect.DeepEqual(got, []PhaseLimit{{Phase: "abc", ValueA: 10, IsActive: true}}) {
		t.Fatalf("expand to abc = %+v, %v", got, err)
	}
	got, err = expandEachPhase([]PhaseLimit{{Phase: "each", ValueA: 6}}, []string{"a", "b", "c"})
	if err != nil || len(got) != 3 || got[2].Phase != "c" || got[2].ValueA != 6 || got[2].IsActive {
		t.Fatalf("expand to a/b/c = %+v, %v", got, err)
	}
	explicit := []PhaseLimit{{Phase: "a", ValueA: 6}, {Phase: "b", ValueA: 10}}
	if got, err := expandEachPhase(explicit, []string{"abc"}); err != nil || !reflect.DeepEqual(got, explicit) {
		t.Errorf("explicit phases must pass through unchanged: %+v, %v", got, err)
	}
	if _, err := expandEachPhase([]PhaseLimit{{Phase: "each"}, {Phase: "a"}}, []string{"a"}); err == nil {
		t.Error("each combined with an explicit phase must fail")
	}
	if _, err := expandEachPhase([]PhaseLimit{{Phase: "each"}}, nil); err == nil {
		t.Error("each without declared phases must fail")
	}
}

func TestCheckDeclared(t *testing.T) {
	abc := []limitSlot{{phase: spinemodel.ElectricalConnectionPhaseNameTypeAbc}}
	perPhase := []ucapi.LoadLimitsPhase{
		{Phase: spinemodel.ElectricalConnectionPhaseNameTypeA},
		{Phase: spinemodel.ElectricalConnectionPhaseNameTypeB},
		{Phase: spinemodel.ElectricalConnectionPhaseNameTypeC},
	}

	err := checkDeclared(perPhase, abc)
	var notDeclared *PhaseNotDeclaredError
	if !errors.As(err, &notDeclared) {
		t.Fatalf("a/b/c against an abc-only device must fail with PhaseNotDeclaredError, got %v", err)
	}
	if !reflect.DeepEqual(notDeclared.Requested, []string{"a", "b", "c"}) || !reflect.DeepEqual(notDeclared.Declared, []string{"abc"}) {
		t.Errorf("error carries %+v", notDeclared)
	}
	if !strings.Contains(err.Error(), `"abc"`) || !strings.Contains(err.Error(), `"each"`) {
		t.Errorf("message must name the phase to use: %s", err)
	}

	if err := checkDeclared([]ucapi.LoadLimitsPhase{{Phase: spinemodel.ElectricalConnectionPhaseNameTypeAbc}}, abc); err != nil {
		t.Errorf("abc against an abc device must pass: %v", err)
	}
	// Upstream writes the declared phase and drops the others; that stays allowed.
	onePhase := []limitSlot{{phase: spinemodel.ElectricalConnectionPhaseNameTypeA}}
	if err := checkDeclared(perPhase, onePhase); err != nil {
		t.Errorf("a partly declared write must pass: %v", err)
	}
	// Without descriptions there is nothing to check against; upstream reports the failure.
	if err := checkDeclared(perPhase, nil); err != nil {
		t.Errorf("unknown phases must not fail here: %v", err)
	}
}

func TestPhaseNameCombined(t *testing.T) {
	for _, input := range []string{"abc", "ABC", "all", "total"} {
		if got, err := phaseName(input); err != nil || got != spinemodel.ElectricalConnectionPhaseNameTypeAbc {
			t.Errorf("phaseName(%q) = %q, %v; want abc", input, got, err)
		}
	}
}
