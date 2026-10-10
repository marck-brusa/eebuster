package eebusgo

import (
	"github.com/enbility/eebus-go/features/client"
	spinemodel "github.com/enbility/spine-go/model"
)

// EVSOCMeasurement is one EV State of Charge value together with the description the vehicle
// published for it, so a test can check the description against the use case's content
// tables and not only the number.
type EVSOCMeasurement struct {
	Value           *float64 `json:"value"`
	Unit            string   `json:"unit,omitempty"`
	MeasurementType string   `json:"measurement_type,omitempty"`
	CommodityType   string   `json:"commodity_type,omitempty"`
	Scope           string   `json:"scope,omitempty"`
	ValueSource     string   `json:"value_source,omitempty"`
	ValueState      string   `json:"value_state,omitempty"`
	Timestamp       string   `json:"timestamp,omitempty"`
	// Descriptions counts the descriptions with this scope; Values counts the measurement
	// values published for it. The use case allows exactly one of each.
	Descriptions int `json:"descriptions"`
	Values       int `json:"values"`
}

// EVSOCCapacity is the nominal battery capacity of EVSOC scenario 2.
type EVSOCCapacity struct {
	Value *float64 `json:"value"`
	Unit  string   `json:"unit,omitempty"`
}

// EVSOCReading is the EV State of Charge data of one vehicle, one field per scenario: state
// of charge (1), nominal capacity (2), state of health (3) and travel range (4). A scenario
// the vehicle does not publish is null.
type EVSOCReading struct {
	Entity          []uint            `json:"entity"`
	StateOfCharge   *EVSOCMeasurement `json:"state_of_charge"`
	NominalCapacity *EVSOCCapacity    `json:"nominal_capacity"`
	StateOfHealth   *EVSOCMeasurement `json:"state_of_health"`
	TravelRange     *EVSOCMeasurement `json:"travel_range"`
}

func (s *Stack) EVSOC() *EVSOC { return s.evsoc }

// Read requests the vehicle's measurement descriptions, values and characteristics, then
// reports them per scenario.
func (e *EVSOC) Read(ski string, entityHint []uint) (EVSOCReading, error) {
	var reading EVSOCReading
	entity, err := resolveEntity(e.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err == nil {
		reading.Entity = entityAddress(entity)
		measurement, measurementErr := client.NewMeasurement(e.uc.LocalEntity, entity)
		connection, connectionErr := client.NewElectricalConnection(e.uc.LocalEntity, entity)
		var requests []readRequest
		if measurementErr == nil {
			requests = append(requests,
				readRequest{feature: measurement, send: func() (*spinemodel.MsgCounterType, error) { return measurement.RequestDescriptions(nil, nil) }},
				readRequest{feature: measurement, send: func() (*spinemodel.MsgCounterType, error) { return measurement.RequestData(nil, nil) }})
		}
		if connectionErr == nil {
			requests = append(requests, readRequest{feature: connection, send: func() (*spinemodel.MsgCounterType, error) { return connection.RequestCharacteristics(nil, nil) }})
		}
		refresh(requests...)
		if measurementErr == nil {
			reading.StateOfCharge = evsocMeasurement(measurement, spinemodel.ScopeTypeTypeStateOfCharge)
			reading.StateOfHealth = evsocMeasurement(measurement, spinemodel.ScopeTypeTypeStateOfHealth)
			reading.TravelRange = evsocMeasurement(measurement, spinemodel.ScopeTypeTypeTravelRange)
		}
		if connectionErr == nil {
			reading.NominalCapacity = evsocCapacity(connection)
		}
	}
	return reading, err
}

func evsocMeasurement(m *client.Measurement, scope spinemodel.ScopeTypeType) *EVSOCMeasurement {
	var out *EVSOCMeasurement
	descriptions, err := m.GetDescriptionsForFilter(spinemodel.MeasurementDescriptionDataType{ScopeType: &scope})
	if err == nil && len(descriptions) > 0 {
		d := descriptions[0]
		out = &EVSOCMeasurement{
			Unit: deref(d.Unit), MeasurementType: deref(d.MeasurementType), CommodityType: deref(d.CommodityType),
			Scope: deref(d.ScopeType), Descriptions: len(descriptions),
		}
		if d.MeasurementId != nil {
			data, dataErr := m.GetDataForFilter(spinemodel.MeasurementDescriptionDataType{MeasurementId: d.MeasurementId})
			if dataErr == nil && len(data) > 0 {
				out.Values = len(data)
				value := data[0]
				if value.Value != nil {
					v := value.Value.GetValue()
					out.Value = &v
				}
				out.ValueSource, out.ValueState, out.Timestamp = deref(value.ValueSource), deref(value.ValueState), deref(value.Timestamp)
			}
		}
	}
	return out
}

func evsocCapacity(ec *client.ElectricalConnection) *EVSOCCapacity {
	var out *EVSOCCapacity
	characteristic := spinemodel.ElectricalConnectionCharacteristicTypeTypeEnergyCapacityNominalMax
	found, err := ec.GetCharacteristicsForFilter(spinemodel.ElectricalConnectionCharacteristicDataType{CharacteristicType: &characteristic})
	if err == nil && len(found) > 0 {
		out = &EVSOCCapacity{Unit: deref(found[0].Unit)}
		if found[0].Value != nil {
			v := found[0].Value.GetValue()
			out.Value = &v
		}
	}
	return out
}
