package eebusgo

import (
	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// LimitDescription is one load-control limit of a category the device declares, with the
// phase and the permitted current range of the electrical-connection parameter it links to:
// the content a test case checks against the use case's description tables.
type LimitDescription struct {
	LimitID        uint   `json:"limit_id"`
	LimitType      string `json:"limit_type,omitempty"`
	LimitCategory  string `json:"limit_category,omitempty"`
	LimitDirection string `json:"limit_direction,omitempty"`
	Unit           string `json:"unit,omitempty"`
	ScopeType      string `json:"scope_type,omitempty"`
	MeasurementID  *uint  `json:"measurement_id,omitempty"`
	ParameterID    *uint  `json:"parameter_id,omitempty"`
	Phase          string `json:"phase,omitempty"`
	// PermittedValues says what the linked parameter's permitted value set carries: "range",
	// "values", "empty" for an entry without any set, or "none" when no entry is published.
	PermittedValues string   `json:"permitted_values"`
	PermittedMinA   *float64 `json:"permitted_min_a,omitempty"`
	PermittedMaxA   *float64 `json:"permitted_max_a,omitempty"`
}

const (
	PermittedRange  = "range"
	PermittedValues = "values"
	PermittedEmpty  = "empty"
	PermittedNone   = "none"
)

// describeLimits lists the limit descriptions of one category the remote entity has
// published, each resolved to its phase and permitted range through the measurement id.
func describeLimits(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface,
	category spinemodel.LoadControlCategoryType) []LimitDescription {
	out := []LimitDescription{}
	lc, err := client.NewLoadControl(localEntity, entity)
	ec, err2 := client.NewElectricalConnection(localEntity, entity)
	if err == nil && err2 == nil {
		limitDescs, _ := lc.GetLimitDescriptionsForFilter(spinemodel.LoadControlLimitDescriptionDataType{LimitCategory: util.Ptr(category)})
		paramDescs, _ := ec.GetParameterDescriptionsForFilter(spinemodel.ElectricalConnectionParameterDescriptionDataType{})
		permitted, _ := ec.GetPermittedValueSetForFilter(spinemodel.ElectricalConnectionPermittedValueSetDataType{})
		for _, ld := range limitDescs {
			if ld.LimitId != nil {
				out = append(out, describeLimit(ld, paramDescs, permitted))
			}
		}
	}
	return out
}

func describeLimit(ld spinemodel.LoadControlLimitDescriptionDataType,
	params []spinemodel.ElectricalConnectionParameterDescriptionDataType,
	permitted []spinemodel.ElectricalConnectionPermittedValueSetDataType) LimitDescription {
	d := LimitDescription{LimitID: uint(*ld.LimitId), PermittedValues: PermittedNone}
	if ld.LimitType != nil {
		d.LimitType = string(*ld.LimitType)
	}
	if ld.LimitCategory != nil {
		d.LimitCategory = string(*ld.LimitCategory)
	}
	if ld.LimitDirection != nil {
		d.LimitDirection = string(*ld.LimitDirection)
	}
	if ld.Unit != nil {
		d.Unit = string(*ld.Unit)
	}
	if ld.ScopeType != nil {
		d.ScopeType = string(*ld.ScopeType)
	}
	if ld.MeasurementId != nil {
		id := uint(*ld.MeasurementId)
		d.MeasurementID = &id
		if param := parameterForMeasurement(params, *ld.MeasurementId); param != nil {
			pid := uint(*param.ParameterId)
			d.ParameterID = &pid
			if param.AcMeasuredPhases != nil {
				d.Phase = string(*param.AcMeasuredPhases)
			}
			describePermitted(&d, *param.ParameterId, permitted)
		}
	}
	return d
}

// parameterForMeasurement finds the electrical parameter a limit's measurement id points
// at, preferring one that names its phase when several share the measurement.
func parameterForMeasurement(params []spinemodel.ElectricalConnectionParameterDescriptionDataType,
	id spinemodel.MeasurementIdType) *spinemodel.ElectricalConnectionParameterDescriptionDataType {
	var found *spinemodel.ElectricalConnectionParameterDescriptionDataType
	for i := range params {
		p := &params[i]
		matches := p.MeasurementId != nil && *p.MeasurementId == id && p.ParameterId != nil
		if matches && (found == nil || (found.AcMeasuredPhases == nil && p.AcMeasuredPhases != nil)) {
			found = p
		}
	}
	return found
}

func describePermitted(d *LimitDescription, parameterID spinemodel.ElectricalConnectionParameterIdType,
	permitted []spinemodel.ElectricalConnectionPermittedValueSetDataType) {
	for _, p := range permitted {
		if p.ParameterId != nil && *p.ParameterId == parameterID {
			d.PermittedValues = PermittedEmpty
			for _, set := range p.PermittedValueSet {
				if len(set.Value) > 0 && d.PermittedValues != PermittedRange {
					d.PermittedValues = PermittedValues
				}
				for _, r := range set.Range {
					if r.Min != nil {
						d.PermittedValues = PermittedRange
						d.PermittedMinA = lowest(d.PermittedMinA, r.Min.GetValue())
					}
					if r.Max != nil {
						d.PermittedValues = PermittedRange
						d.PermittedMaxA = highest(d.PermittedMaxA, r.Max.GetValue())
					}
				}
			}
		}
	}
}

func lowest(current *float64, v float64) *float64 {
	if current == nil || v < *current {
		current = &v
	}
	return current
}

func highest(current *float64, v float64) *float64 {
	if current == nil || v > *current {
		current = &v
	}
	return current
}
