package simulator

import (
	"fmt"
	"log"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/server"
	spineapi "github.com/enbility/spine-go/api"
	"github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/spine"
	"github.com/enbility/spine-go/util"

	"github.com/marck-brusa/eebuster/internal/config"
)

// A vehicle plugged into a simulated charging station: a battery that fills, per-phase
// currents that follow whatever limit is in force, and a state of charge.
//
// Built from SPINE server features directly, not from a use case object, because eebus-go
// implements EVCC/EVCEM/EVSOC/OPEV only on the CEM (reading) side -- the EV half of those
// use cases is firmware in a real vehicle, so there is nothing upstream to reuse. What each
// client reads is therefore what this has to publish, and the ids have to line up the way a
// real device's do: the phase current measurements, the electrical-connection parameters
// that name their phase, and the load-control limits that curtail them are all tied together
// by MeasurementId.
type evSim struct {
	cfg    config.SimulatedEV
	id     string
	entity spineapi.EntityLocalInterface

	ec   *server.ElectricalConnection
	meas *server.Measurement
	lc   *server.LoadControl
	dd   *server.DeviceDiagnosis

	ecID       model.ElectricalConnectionIdType
	currentIDs []model.MeasurementIdType
	powerIDs   []model.MeasurementIdType
	energyID   model.MeasurementIdType
	socID      model.MeasurementIdType
	limitIDs   []model.LoadControlLimitIdType

	mu         sync.Mutex
	soc        float64 // percent
	energyWh   float64 // charged this session
	curtailedA []float64
	limitOn    []bool
	finished   bool
	stationA   float64 // last per-phase share of the station's own LPC limit
	lastTick   time.Time
	stop       chan struct{}
}

const evNominalV = 230.0

// evDefaults fills in the blanks so `ev: {enabled: true}` alone produces a sensible vehicle.
func evDefaults(cfg config.SimulatedEV) config.SimulatedEV {
	if cfg.Name == "" {
		cfg.Name = "Simulated EV"
	}
	if cfg.Brand == "" {
		cfg.Brand = "SIMCAR"
	}
	if cfg.Model == "" {
		cfg.Model = "e-Sim"
	}
	if cfg.Serial == "" {
		cfg.Serial = "SIM-EV-0001"
	}
	if cfg.BatteryKWh <= 0 {
		cfg.BatteryKWh = 60
	}
	if cfg.SoCStartPercent <= 0 {
		cfg.SoCStartPercent = 20
	}
	if cfg.MaxCurrentA <= 0 {
		cfg.MaxCurrentA = 16
	}
	if cfg.MinCurrentA <= 0 {
		cfg.MinCurrentA = 6
	}
	if cfg.Phases <= 0 || cfg.Phases > 3 {
		cfg.Phases = 3
	}
	if cfg.ChargeSpeedup <= 0 {
		cfg.ChargeSpeedup = 60
	}
	return cfg
}

var evPhaseNames = []model.ElectricalConnectionPhaseNameType{
	model.ElectricalConnectionPhaseNameTypeA,
	model.ElectricalConnectionPhaseNameTypeB,
	model.ElectricalConnectionPhaseNameTypeC,
}

// newEVSim attaches the vehicle as a sub-entity of the station's own entity, which is how
// SPINE models a car plugged into a charger: the EVSE is entity [1], the EV it currently
// holds is [1,1]. A CEM resolves the EV use cases to that address.
func newEVSim(id string, cfg config.SimulatedEV, device spineapi.DeviceLocalInterface, evse spineapi.EntityLocalInterface) (*evSim, error) {
	cfg = evDefaults(cfg)
	address := append(append([]model.AddressEntityType{}, evse.Address().Entity...), model.AddressEntityType(1))
	entity := spine.NewEntityLocal(device, model.EntityTypeTypeEV, address, time.Second*4)
	device.AddEntity(entity)

	e := &evSim{
		cfg: cfg, id: id, entity: entity,
		soc:        cfg.SoCStartPercent,
		curtailedA: make([]float64, cfg.Phases),
		limitOn:    make([]bool, cfg.Phases),
		lastTick:   time.Now(),
		stop:       make(chan struct{}),
	}

	if err := e.addIdentityFeatures(); err != nil {
		return nil, err
	}
	if err := e.addElectricalFeatures(); err != nil {
		return nil, err
	}
	if err := e.addLoadControl(); err != nil {
		return nil, err
	}
	e.announceUseCases()
	e.publish()
	return e, nil
}

// EVCC scenarios 1-6: who the vehicle is, whether it is charging, and how it may charge.
func (e *evSim) addIdentityFeatures() error {
	dc := e.entity.GetOrAddFeature(model.FeatureTypeTypeDeviceClassification, model.RoleTypeServer)
	if dc == nil {
		return fmt.Errorf("simulator %s: EV DeviceClassification feature", e.id)
	}
	dc.AddFunctionType(model.FunctionTypeDeviceClassificationManufacturerData, true, false)
	dc.SetData(model.FunctionTypeDeviceClassificationManufacturerData, &model.DeviceClassificationManufacturerDataType{
		BrandName:    util.Ptr(model.DeviceClassificationStringType(e.cfg.Brand)),
		VendorName:   util.Ptr(model.DeviceClassificationStringType(e.cfg.Brand)),
		DeviceName:   util.Ptr(model.DeviceClassificationStringType(e.cfg.Name)),
		DeviceCode:   util.Ptr(model.DeviceClassificationStringType(e.cfg.Model)),
		SerialNumber: util.Ptr(model.DeviceClassificationStringType(e.cfg.Serial)),
	})

	diag := e.entity.GetOrAddFeature(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeServer)
	if diag == nil {
		return fmt.Errorf("simulator %s: EV DeviceDiagnosis feature", e.id)
	}
	diag.AddFunctionType(model.FunctionTypeDeviceDiagnosisStateData, true, false)
	dd, err := server.NewDeviceDiagnosis(e.entity)
	if err != nil {
		return err
	}
	e.dd = dd
	dd.SetLocalOperatingState(model.DeviceDiagnosisOperatingStateTypeNormalOperation)

	// The two configuration keys a CEM reads before it curtails: which communication standard
	// is in use (ISO 15118 or the far more limited IEC 61851), and whether the phases may be
	// curtailed independently -- the precondition for asymmetric charging (OPEV-002).
	cfgFeature := e.entity.GetOrAddFeature(model.FeatureTypeTypeDeviceConfiguration, model.RoleTypeServer)
	if cfgFeature == nil {
		return fmt.Errorf("simulator %s: EV DeviceConfiguration feature", e.id)
	}
	cfgFeature.AddFunctionType(model.FunctionTypeDeviceConfigurationKeyValueDescriptionListData, true, false)
	cfgFeature.AddFunctionType(model.FunctionTypeDeviceConfigurationKeyValueListData, true, false)
	dcfg, err := server.NewDeviceConfiguration(e.entity)
	if err != nil {
		return err
	}
	commID := dcfg.AddKeyValueDescription(model.DeviceConfigurationKeyValueDescriptionDataType{
		KeyName:   util.Ptr(model.DeviceConfigurationKeyNameTypeCommunicationsStandard),
		ValueType: util.Ptr(model.DeviceConfigurationKeyValueTypeTypeString),
	})
	if commID != nil {
		_ = dcfg.UpdateKeyValueDataForKeyId(model.DeviceConfigurationKeyValueDataType{
			Value: &model.DeviceConfigurationKeyValueValueType{
				String: util.Ptr(model.DeviceConfigurationKeyValueStringType(model.DeviceConfigurationKeyValueStringTypeISO151182ED2)),
			},
		}, nil, *commID)
	}
	asymID := dcfg.AddKeyValueDescription(model.DeviceConfigurationKeyValueDescriptionDataType{
		KeyName:   util.Ptr(model.DeviceConfigurationKeyNameTypeAsymmetricChargingSupported),
		ValueType: util.Ptr(model.DeviceConfigurationKeyValueTypeTypeBoolean),
	})
	if asymID != nil {
		_ = dcfg.UpdateKeyValueDataForKeyId(model.DeviceConfigurationKeyValueDataType{
			Value: &model.DeviceConfigurationKeyValueValueType{Boolean: util.Ptr(true)},
		}, nil, *asymID)
	}
	return nil
}

// The measurements a CEM reads during a session (EVCEM 1-3, EVSOC 1) and the electrical
// connection that gives them their phase and their permitted range (EVCC 6, OPEV 1).
func (e *evSim) addElectricalFeatures() error {
	measFeature := e.entity.GetOrAddFeature(model.FeatureTypeTypeMeasurement, model.RoleTypeServer)
	if measFeature == nil {
		return fmt.Errorf("simulator %s: EV Measurement feature", e.id)
	}
	measFeature.AddFunctionType(model.FunctionTypeMeasurementDescriptionListData, true, false)
	measFeature.AddFunctionType(model.FunctionTypeMeasurementListData, true, false)

	ecFeature := e.entity.GetOrAddFeature(model.FeatureTypeTypeElectricalConnection, model.RoleTypeServer)
	if ecFeature == nil {
		return fmt.Errorf("simulator %s: EV ElectricalConnection feature", e.id)
	}
	ecFeature.AddFunctionType(model.FunctionTypeElectricalConnectionDescriptionListData, true, false)
	ecFeature.AddFunctionType(model.FunctionTypeElectricalConnectionParameterDescriptionListData, true, false)
	ecFeature.AddFunctionType(model.FunctionTypeElectricalConnectionPermittedValueSetListData, true, false)

	meas, err := server.NewMeasurement(e.entity)
	if err != nil {
		return err
	}
	ec, err := server.NewElectricalConnection(e.entity)
	if err != nil {
		return err
	}
	e.meas, e.ec = meas, ec

	e.ecID = model.ElectricalConnectionIdType(0)
	if err := ec.AddDescription(model.ElectricalConnectionDescriptionDataType{
		ElectricalConnectionId: &e.ecID,
		PowerSupplyType:        util.Ptr(model.ElectricalConnectionVoltageTypeTypeAc),
		AcConnectedPhases:      util.Ptr(uint(e.cfg.Phases)),
	}); err != nil {
		return err
	}

	// One current measurement per phase, each paired with the electrical-connection parameter
	// that names its phase and carries its permitted range. OPEV curtails by pointing a limit
	// at these same MeasurementIds, so the pairing is what makes curtailment addressable.
	for i := 0; i < e.cfg.Phases; i++ {
		id := meas.AddDescription(model.MeasurementDescriptionDataType{
			MeasurementType: util.Ptr(model.MeasurementTypeTypeCurrent),
			CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
			Unit:            util.Ptr(model.UnitOfMeasurementTypeA),
			ScopeType:       util.Ptr(model.ScopeTypeTypeACCurrent),
		})
		if id == nil {
			return fmt.Errorf("simulator %s: EV current measurement %d", e.id, i)
		}
		e.currentIDs = append(e.currentIDs, *id)

		paramID := ec.AddParameterDescription(model.ElectricalConnectionParameterDescriptionDataType{
			ElectricalConnectionId: &e.ecID,
			MeasurementId:          id,
			AcMeasuredPhases:       util.Ptr(evPhaseNames[i]),
			ScopeType:              util.Ptr(model.ScopeTypeTypeACCurrent),
		})
		if paramID == nil {
			return fmt.Errorf("simulator %s: EV current parameter %d", e.id, i)
		}
		// What the vehicle will accept on this phase: a CEM must stay inside it, and the
		// testbench renders it as the min/max under the current inputs.
		if err := ec.UpdatePermittedValueSetForIds([]eebusapi.ElectricalConnectionPermittedValueSetForID{{
			Data: model.ElectricalConnectionPermittedValueSetDataType{
				ElectricalConnectionId: &e.ecID,
				ParameterId:            paramID,
				PermittedValueSet: []model.ScaledNumberSetType{{
					Range: []model.ScaledNumberRangeType{{
						Min: model.NewScaledNumberType(e.cfg.MinCurrentA),
						Max: model.NewScaledNumberType(e.cfg.MaxCurrentA),
					}},
				}},
			},
			ElectricalConnectionId: e.ecID,
			ParameterId:            *paramID,
		}}); err != nil {
			return err
		}

		powerID := meas.AddDescription(model.MeasurementDescriptionDataType{
			MeasurementType: util.Ptr(model.MeasurementTypeTypePower),
			CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
			Unit:            util.Ptr(model.UnitOfMeasurementTypeW),
			ScopeType:       util.Ptr(model.ScopeTypeTypeACPower),
		})
		if powerID == nil {
			return fmt.Errorf("simulator %s: EV power measurement %d", e.id, i)
		}
		e.powerIDs = append(e.powerIDs, *powerID)
		if ec.AddParameterDescription(model.ElectricalConnectionParameterDescriptionDataType{
			ElectricalConnectionId: &e.ecID,
			MeasurementId:          powerID,
			AcMeasuredPhases:       util.Ptr(evPhaseNames[i]),
			ScopeType:              util.Ptr(model.ScopeTypeTypeACPower),
		}) == nil {
			return fmt.Errorf("simulator %s: EV power parameter %d", e.id, i)
		}
	}

	// Total charging power, whose permitted range is what EVCC reports as the vehicle's
	// charging power limits (min / max / standby).
	totalParam := ec.AddParameterDescription(model.ElectricalConnectionParameterDescriptionDataType{
		ElectricalConnectionId: &e.ecID,
		ScopeType:              util.Ptr(model.ScopeTypeTypeACPowerTotal),
	})
	if totalParam == nil {
		return fmt.Errorf("simulator %s: EV total power parameter", e.id)
	}
	phases := float64(e.cfg.Phases)
	if err := ec.UpdatePermittedValueSetForIds([]eebusapi.ElectricalConnectionPermittedValueSetForID{{
		Data: model.ElectricalConnectionPermittedValueSetDataType{
			ElectricalConnectionId: &e.ecID,
			ParameterId:            totalParam,
			PermittedValueSet: []model.ScaledNumberSetType{{
				Value: []model.ScaledNumberType{*model.NewScaledNumberType(0)}, // standby
				Range: []model.ScaledNumberRangeType{{
					Min: model.NewScaledNumberType(e.cfg.MinCurrentA * evNominalV * phases),
					Max: model.NewScaledNumberType(e.cfg.MaxCurrentA * evNominalV * phases),
				}},
			}},
		},
		ElectricalConnectionId: e.ecID,
		ParameterId:            *totalParam,
	}}); err != nil {
		return err
	}

	// Charged energy (EVCEM 3) and state of charge (EVSOC 1).
	energyID := meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypeEnergy),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		Unit:            util.Ptr(model.UnitOfMeasurementTypeWh),
		ScopeType:       util.Ptr(model.ScopeTypeTypeCharge),
	})
	if energyID == nil {
		return fmt.Errorf("simulator %s: EV energy measurement", e.id)
	}
	e.energyID = *energyID

	socID := meas.AddDescription(model.MeasurementDescriptionDataType{
		MeasurementType: util.Ptr(model.MeasurementTypeTypePercentage),
		CommodityType:   util.Ptr(model.CommodityTypeTypeElectricity),
		Unit:            util.Ptr(model.UnitOfMeasurementTypepct),
		ScopeType:       util.Ptr(model.ScopeTypeTypeStateOfCharge),
	})
	if socID == nil {
		return fmt.Errorf("simulator %s: EV state-of-charge measurement", e.id)
	}
	e.socID = *socID
	return nil
}

// OPEV scenario 1 on the receiving side: one obligation per phase, writable by a CEM, each
// pointing at that phase's current measurement.
func (e *evSim) addLoadControl() error {
	f := e.entity.GetOrAddFeature(model.FeatureTypeTypeLoadControl, model.RoleTypeServer)
	if f == nil {
		return fmt.Errorf("simulator %s: EV LoadControl feature", e.id)
	}
	f.AddFunctionType(model.FunctionTypeLoadControlLimitDescriptionListData, true, false)
	f.AddFunctionType(model.FunctionTypeLoadControlLimitListData, true, true)

	lc, err := server.NewLoadControl(e.entity)
	if err != nil {
		return err
	}
	e.lc = lc

	for i := 0; i < e.cfg.Phases; i++ {
		id := lc.AddLimitDescription(model.LoadControlLimitDescriptionDataType{
			LimitType:      util.Ptr(model.LoadControlLimitTypeTypeMaxValueLimit),
			LimitCategory:  util.Ptr(model.LoadControlCategoryTypeObligation),
			LimitDirection: util.Ptr(model.EnergyDirectionTypeConsume),
			MeasurementId:  util.Ptr(e.currentIDs[i]),
			Unit:           util.Ptr(model.UnitOfMeasurementTypeA),
			ScopeType:      util.Ptr(model.ScopeTypeTypeOverloadProtection),
		})
		if id == nil {
			return fmt.Errorf("simulator %s: EV limit description %d", e.id, i)
		}
		e.limitIDs = append(e.limitIDs, *id)
		if err := lc.UpdateLimitDataForIds([]eebusapi.LoadControlLimitDataForID{{
			Data: model.LoadControlLimitDataType{
				Value:             model.NewScaledNumberType(e.cfg.MaxCurrentA),
				IsLimitChangeable: util.Ptr(true),
				IsLimitActive:     util.Ptr(false),
			},
			Id: *id,
		}}); err != nil {
			return err
		}
	}
	return nil
}

func (e *evSim) announceUseCases() {
	e.entity.AddUseCaseSupport(model.UseCaseActorTypeEV, model.UseCaseNameTypeEVCommissioningAndConfiguration,
		model.SpecificationVersionType("1.0.1"), "", true,
		[]model.UseCaseScenarioSupportType{1, 2, 3, 4, 5, 6, 7, 8})
	e.entity.AddUseCaseSupport(model.UseCaseActorTypeEV, model.UseCaseNameTypeMeasurementOfElectricityDuringEVCharging,
		model.SpecificationVersionType("1.0.1"), "", true,
		[]model.UseCaseScenarioSupportType{1, 2, 3})
	e.entity.AddUseCaseSupport(model.UseCaseActorTypeEV, model.UseCaseNameTypeEVStateOfCharge,
		model.SpecificationVersionType("1.0.0"), "", true,
		[]model.UseCaseScenarioSupportType{1})
	e.entity.AddUseCaseSupport(model.UseCaseActorTypeEV, model.UseCaseNameTypeOverloadProtectionByEVChargingCurrentCurtailment,
		model.SpecificationVersionType("1.0.1"), "", true,
		[]model.UseCaseScenarioSupportType{1, 2, 3})
}

// chargingCurrentA is the whole vehicle-side behaviour: charge at the maximum the battery
// accepts, unless something curtails it. A curtailment below the vehicle's own minimum
// pauses charging rather than undercutting it -- what a real EV does, and the reason a 0 A
// obligation is a pause signal rather than a trickle.
func (e *evSim) chargingCurrentA(stationLimitA float64) []float64 {
	out := make([]float64, e.cfg.Phases)
	if e.finished {
		return out
	}
	for i := range out {
		want := e.cfg.MaxCurrentA
		if e.limitOn[i] && e.curtailedA[i] < want {
			want = e.curtailedA[i]
		}
		if stationLimitA > 0 && stationLimitA < want {
			want = stationLimitA
		}
		if want < e.cfg.MinCurrentA {
			want = 0
		}
		out[i] = want
	}
	return out
}

// tick advances the battery and republishes. stationLimitA is the per-phase share of any
// active station-level LPC limit, so a consumption limit written to the charging station
// reaches the vehicle exactly as it would in a real installation.
func (e *evSim) tick(stationLimitA float64) (powerW float64) {
	e.mu.Lock()
	now := time.Now()
	elapsed := now.Sub(e.lastTick).Seconds()
	e.lastTick = now
	e.stationA = stationLimitA
	currents := e.chargingCurrentA(stationLimitA)
	for _, a := range currents {
		powerW += a * evNominalV
	}
	if elapsed > 0 && powerW > 0 {
		// Simulated time runs faster than the wall clock so a charge is watchable.
		deltaWh := powerW * (elapsed * e.cfg.ChargeSpeedup) / 3600
		e.energyWh += deltaWh
		e.soc += deltaWh / (e.cfg.BatteryKWh * 1000) * 100
		if e.soc >= 100 {
			e.soc = 100
			e.finished = true
			powerW = 0
			log.Printf("simulator[%s]: EV battery full, charging finished", e.id)
		}
	}
	e.mu.Unlock()
	e.publish()
	return powerW
}

// publish writes the current vehicle state into the SPINE features a CEM reads.
func (e *evSim) publish() {
	e.mu.Lock()
	currents := e.chargingCurrentA(e.stationA)
	soc, energy, finished := e.soc, e.energyWh, e.finished
	e.mu.Unlock()

	// valueType and timestamp are not decoration: spine-go treats a measurement whose key
	// fields (measurementId, valueType, timestamp) are not all set as an "incomplete
	// identifier" and, per SPINE Table 7, broadcasts it over the *existing* entries instead of
	// adding it. Into an empty data set that stores nothing at all -- and the update still
	// reports success, so the device answers every read with an empty list while looking
	// healthy. Setting all three is also what a real device sends.
	now := model.NewAbsoluteOrRelativeTimeTypeFromTime(time.Now())
	measurement := func(id model.MeasurementIdType, value float64) eebusapi.MeasurementDataForID {
		return eebusapi.MeasurementDataForID{
			Data: model.MeasurementDataType{
				ValueType: util.Ptr(model.MeasurementValueTypeTypeValue),
				Timestamp: now,
				Value:     model.NewScaledNumberType(value),
			},
			Id: id,
		}
	}
	data := []eebusapi.MeasurementDataForID{
		measurement(e.energyID, energy),
		measurement(e.socID, soc),
	}
	for i := range currents {
		data = append(data,
			measurement(e.currentIDs[i], currents[i]),
			measurement(e.powerIDs[i], currents[i]*evNominalV),
		)
	}
	if err := e.meas.UpdateDataForIds(data); err != nil {
		log.Printf("simulator[%s]: publishing EV measurements failed: %v", e.id, err)
	}

	if e.dd != nil {
		state := model.DeviceDiagnosisOperatingStateTypeNormalOperation
		switch {
		case finished:
			state = model.DeviceDiagnosisOperatingStateTypeFinished
		case sumOf(currents) == 0:
			// Curtailed to a stop: paused, not failed -- a CEM distinguishes the two.
			state = model.DeviceDiagnosisOperatingStateTypeStandby
		}
		e.dd.SetLocalOperatingState(state)
	}
}

func sumOf(values []float64) (total float64) {
	for _, v := range values {
		total += v
	}
	return total
}

// applyWrittenLimits reads back the per-phase obligations a CEM has written into our own
// LoadControl feature. Polling the published data rather than hooking the write callback
// keeps one code path for "what is the limit now", whether it arrived a moment ago or was
// standing before this vehicle plugged in.
func (e *evSim) applyWrittenLimits() {
	if e.lc == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, id := range e.limitIDs {
		data, err := e.lc.GetLimitDataForId(id)
		if err != nil || data == nil {
			continue
		}
		active := data.IsLimitActive != nil && *data.IsLimitActive
		value := e.cfg.MaxCurrentA
		if data.Value != nil {
			value = data.Value.GetValue()
		}
		if e.limitOn[i] != active || e.curtailedA[i] != value {
			log.Printf("simulator[%s]: EV phase %s obligation -> %.1fA active=%v",
				e.id, evPhaseNames[i], value, active)
		}
		e.limitOn[i] = active
		e.curtailedA[i] = value
	}
}

// SoC reports the battery state for logging.
func (e *evSim) SoC() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.soc
}

// Currents reports the per-phase charging current for the station's own measurements, so
// what the station meters and what the vehicle reports cannot drift apart.
func (e *evSim) Currents() []float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.chargingCurrentA(e.stationA)
}
