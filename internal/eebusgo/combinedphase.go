package eebusgo

import (
	"github.com/enbility/eebus-go/features/client"
	spineapi "github.com/enbility/spine-go/api"
	spinemodel "github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// combinedPhaseValue reads one electricity measurement of the given type and scope from the
// remote entity without a per-phase mapping. eebus-go's per-phase getters only know phases
// a, b and c, so a device that measures a single combined phase ("abc" -- a wireless pad, a
// DC charger) is invisible to them although the value is on the wire.
func combinedPhaseValue(localEntity spineapi.EntityLocalInterface, entity spineapi.EntityRemoteInterface,
	measurementType spinemodel.MeasurementTypeType, scope spinemodel.ScopeTypeType) (float64, bool) {
	measurement, err := client.NewMeasurement(localEntity, entity)
	if err != nil {
		return 0, false
	}
	filter := spinemodel.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(measurementType),
		CommodityType:   util.Ptr(spinemodel.CommodityTypeTypeElectricity),
		ScopeType:       util.Ptr(scope),
	}
	data, err := measurement.GetDataForFilter(filter)
	if err != nil {
		return 0, false
	}
	for _, item := range data {
		if item.Value != nil {
			return item.Value.GetValue(), true
		}
	}
	return 0, false
}
