package simulator

import (
	"fmt"
	"log"
	"sync"
	"time"

	eebusapi "github.com/enbility/eebus-go/api"
	"github.com/enbility/eebus-go/features/client"
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

	mu       sync.Mutex
	soc      float64 // percent
	energyWh float64 // charged this session
	// obligations (OPEV) cap what the vehicle draws per phase; recommendations (OSCEV) give it
	// the self-produced current per phase to charge with.
	obligations     limitSet
	recommendations limitSet
	finished        bool
	stationA        float64 // last per-phase share of the station's own LPC limit
	lastTick        time.Time
	stop            chan struct{}

	// The Energy Guard or CEM the vehicle watches (OPEV and OSCEV scenarios 2 and 3): its DeviceDiagnosis
	// feature once subscribed, when its heartbeat last arrived, and whether it announced a
	// failure. safeReason remembers why the vehicle last held its safe current, for the log.
	guard       *client.DeviceDiagnosis
	guardEntity spineapi.EntityRemoteInterface
	guardSeen   time.Time
	guardFailed bool
	safeReason  string
}

// guardTimeout is how long the Energy Guard's heartbeat may stay away before the vehicle
// falls to its safe current (OPEV-005).
const guardTimeout = 4 * time.Second

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
		cfg.ChargeSpeedup = 1
	}
	return cfg
}

var evPhaseNames = []model.ElectricalConnectionPhaseNameType{
	model.ElectricalConnectionPhaseNameTypeA,
	model.ElectricalConnectionPhaseNameTypeB,
	model.ElectricalConnectionPhaseNameTypeC,
}

// measuredPhases names the phase of each current measurement, parameter and limit the vehicle
// publishes: one per connected phase, or the single combined "abc".
func measuredPhases(cfg config.SimulatedEV) []model.ElectricalConnectionPhaseNameType {
	if cfg.CombinedPhase {
		return []model.ElectricalConnectionPhaseNameType{model.ElectricalConnectionPhaseNameTypeAbc}
	}
	return evPhaseNames[:cfg.Phases]
}

// phasesPerMeasurement is how many connected phases each published current flows on.
func phasesPerMeasurement(cfg config.SimulatedEV) float64 {
	if cfg.CombinedPhase {
		return float64(cfg.Phases)
	}
	return 1
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
		soc:             cfg.SoCStartPercent,
		obligations:     newLimitSet("obligation", len(measuredPhases(cfg))),
		recommendations: newLimitSet("recommendation", len(measuredPhases(cfg))),
		lastTick:        time.Now(),
		stop:            make(chan struct{}),
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

	// EVCC scenario 4: the vehicle's identification, as a locally administered EUI-48 that
	// cannot collide with a real vehicle's address.
	ident := e.entity.GetOrAddFeature(model.FeatureTypeTypeIdentification, model.RoleTypeServer)
	if ident == nil {
		return fmt.Errorf("simulator %s: EV Identification feature", e.id)
	}
	ident.AddFunctionType(model.FunctionTypeIdentificationListData, true, false)
	ident.SetData(model.FunctionTypeIdentificationListData, &model.IdentificationListDataType{
		IdentificationData: []model.IdentificationDataType{{
			IdentificationId:    util.Ptr(model.IdentificationIdType(0)),
			IdentificationType:  util.Ptr(model.IdentificationTypeTypeEui48),
			IdentificationValue: util.Ptr(model.IdentificationValueType("02-00-00-00-00-01")),
		}},
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
	// The client side of DeviceDiagnosis watches the Energy Guard's heartbeat and state.
	if e.entity.GetOrAddFeature(model.FeatureTypeTypeDeviceDiagnosis, model.RoleTypeClient) == nil {
		return fmt.Errorf("simulator %s: EV DeviceDiagnosis client feature", e.id)
	}

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
	for i, phase := range measuredPhases(e.cfg) {
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
			AcMeasuredPhases:       util.Ptr(phase),
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
			AcMeasuredPhases:       util.Ptr(phase),
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

// limitSet is one kind of per-phase current limit the vehicle takes from a CEM, one limit per
// measured phase, each pointing at that phase's current measurement.
type limitSet struct {
	kind   string
	ids    []model.LoadControlLimitIdType
	on     []bool
	valueA []float64
}

func newLimitSet(kind string, phases int) limitSet {
	return limitSet{kind: kind, on: make([]bool, phases), valueA: make([]float64, phases)}
}

// capA lowers want to the limit of phase i when that limit is active and lower.
func (l *limitSet) capA(i int, want float64) float64 {
	if l.on[i] && l.valueA[i] < want {
		want = l.valueA[i]
	}
	return want
}

// OPEV and OSCEV scenario 1 on the receiving side: one obligation (overload protection) and
// one recommendation (self-consumption) per phase, writable by a CEM.
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

	kinds := []struct {
		set      *limitSet
		category model.LoadControlCategoryType
		scope    model.ScopeTypeType
	}{
		{&e.obligations, model.LoadControlCategoryTypeObligation, model.ScopeTypeTypeOverloadProtection},
		{&e.recommendations, model.LoadControlCategoryTypeRecommendation, model.ScopeTypeTypeSelfConsumption},
	}
	for _, kind := range kinds {
		for i := range measuredPhases(e.cfg) {
			id := lc.AddLimitDescription(model.LoadControlLimitDescriptionDataType{
				LimitType:      util.Ptr(model.LoadControlLimitTypeTypeMaxValueLimit),
				LimitCategory:  util.Ptr(kind.category),
				LimitDirection: util.Ptr(model.EnergyDirectionTypeConsume),
				MeasurementId:  util.Ptr(e.currentIDs[i]),
				Unit:           util.Ptr(model.UnitOfMeasurementTypeA),
				ScopeType:      util.Ptr(kind.scope),
			})
			if id == nil {
				return fmt.Errorf("simulator %s: EV %s description %d", e.id, kind.set.kind, i)
			}
			kind.set.ids = append(kind.set.ids, *id)
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
	e.entity.AddUseCaseSupport(model.UseCaseActorTypeEV, model.UseCaseNameTypeOptimizationOfSelfConsumptionDuringEVCharging,
		model.SpecificationVersionType("1.0.1"), "", true,
		[]model.UseCaseScenarioSupportType{1, 2, 3})
}

// chargingCurrentA is the whole vehicle-side behaviour: charge at the maximum the battery
// accepts, or at the self-produced current a trusted CEM recommends, unless something
// curtails it. A current below the vehicle's own minimum pauses charging rather than
// undercutting it -- what a real EV does, and the reason a 0 A obligation is a pause signal
// rather than a trickle.
func (e *evSim) chargingCurrentA(stationLimitA float64) []float64 {
	out := make([]float64, len(e.obligations.on))
	if e.finished {
		return out
	}
	safe := e.guardMissing() != ""
	for i := range out {
		want := e.cfg.MaxCurrentA
		if !safe {
			// A CEM that is gone or failed is not trusted with self-consumption (OSCEV-007).
			want = e.recommendations.capA(i, want)
		}
		want = e.obligations.capA(i, want)
		if stationLimitA > 0 && stationLimitA < want {
			want = stationLimitA
		}
		if safe && want > e.cfg.MinCurrentA {
			// Without a trustworthy Energy Guard the vehicle holds its safe current, the
			// minimum it charges with, so no overload can occur meanwhile (OPEV-005, OPEV-007).
			want = e.cfg.MinCurrentA
		}
		if want < e.cfg.MinCurrentA {
			want = 0
		}
		out[i] = want
	}
	return out
}

// guardMissing says why the vehicle cannot trust its Energy Guard right now: "heartbeat"
// once the guard's heartbeat has stayed away for more than guardTimeout after having been
// seen, "failure" while the guard announces a failure, or "" while all is well. Called with
// the mutex held.
func (e *evSim) guardMissing() string {
	reason := ""
	switch {
	case e.guardFailed:
		reason = "failure"
	case !e.guardSeen.IsZero() && time.Since(e.guardSeen) > guardTimeout:
		reason = "heartbeat"
	}
	return reason
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
	if reason := e.guardMissing(); reason != e.safeReason {
		e.safeReason = reason
		switch reason {
		case "heartbeat":
			log.Printf("simulator[%s]: EV: no Energy Guard heartbeat for more than %s, holding the safe current of %.0fA (OPEV-005)", e.id, guardTimeout, e.cfg.MinCurrentA)
		case "failure":
			log.Printf("simulator[%s]: EV: the Energy Guard announced a failure, holding the safe current of %.0fA (OPEV-007)", e.id, e.cfg.MinCurrentA)
		default:
			log.Printf("simulator[%s]: EV: the Energy Guard is back, following its limits again", e.id)
		}
	}
	currents := e.chargingCurrentA(stationLimitA)
	for _, a := range currents {
		powerW += a * evNominalV * phasesPerMeasurement(e.cfg)
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
	// healthy. Setting all three is also what a real device sends. valueSource is mandatory in
	// the EVCEM and EVSOC content tables.
	now := model.NewAbsoluteOrRelativeTimeTypeFromTime(time.Now())
	measurement := func(id model.MeasurementIdType, value float64) eebusapi.MeasurementDataForID {
		return eebusapi.MeasurementDataForID{
			Data: model.MeasurementDataType{
				ValueType:   util.Ptr(model.MeasurementValueTypeTypeValue),
				Timestamp:   now,
				Value:       model.NewScaledNumberType(value),
				ValueSource: util.Ptr(model.MeasurementValueSourceTypeMeasuredValue),
			},
			Id: id,
		}
	}
	// The state of charge is a calculated value (EVSOC Table 9); the rest is measured.
	stateOfCharge := measurement(e.socID, soc)
	stateOfCharge.Data.ValueSource = util.Ptr(model.MeasurementValueSourceTypeCalculatedValue)
	data := []eebusapi.MeasurementDataForID{
		measurement(e.energyID, energy),
		stateOfCharge,
	}
	for i := range currents {
		data = append(data,
			measurement(e.currentIDs[i], currents[i]),
			measurement(e.powerIDs[i], currents[i]*evNominalV*phasesPerMeasurement(e.cfg)),
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

// applyWrittenLimits reads back the per-phase obligations and recommendations a CEM has
// written into our own LoadControl feature. Polling the published data rather than hooking
// the write callback keeps one code path for "what is the limit now", whether it arrived a
// moment ago or was standing before this vehicle plugged in.
func (e *evSim) applyWrittenLimits() {
	if e.lc != nil {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.pickUp(&e.obligations)
		e.pickUp(&e.recommendations)
	}
}

// pickUp copies the written state of one limit set. Called with the mutex held.
func (e *evSim) pickUp(set *limitSet) {
	for i, id := range set.ids {
		if data, err := e.lc.GetLimitDataForId(id); err == nil && data != nil {
			active := data.IsLimitActive != nil && *data.IsLimitActive
			value := e.cfg.MaxCurrentA
			if data.Value != nil {
				value = data.Value.GetValue()
			}
			if set.on[i] != active || set.valueA[i] != value {
				log.Printf("simulator[%s]: EV phase %s %s -> %.1fA active=%v",
					e.id, measuredPhases(e.cfg)[i], set.kind, value, active)
			}
			set.on[i] = active
			set.valueA[i] = value
		}
	}
}

// SoC reports the battery state for logging.
func (e *evSim) SoC() float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.soc
}

// Currents reports the per-phase charging current for the station's own measurements, so
// what the station meters and what the vehicle reports cannot drift apart. A combined-phase
// vehicle draws its one current on every connected phase.
func (e *evSim) Currents() []float64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	currents := e.chargingCurrentA(e.stationA)
	if !e.cfg.CombinedPhase {
		return currents
	}
	out := make([]float64, e.cfg.Phases)
	for i := range out {
		out[i] = currents[0]
	}
	return out
}

// The Energy Guard side of OPEV scenarios 2 and 3: the vehicle subscribes to the guard's
// DeviceDiagnosis feature as soon as a CEM entity appears, so its heartbeat and operating
// state arrive as notifications, and reacts to their absence or to a failure in
// chargingCurrentA.

func (e *evSim) handleEvent(payload spineapi.EventPayload) {
	switch {
	case payload.Entity != nil && payload.EventType == spineapi.EventTypeEntityChange && payload.ChangeType == spineapi.ElementChangeAdd &&
		payload.Entity.EntityType() == model.EntityTypeTypeCEM:
		e.guardAppeared(payload.Entity)
	case payload.EventType == spineapi.EventTypeDeviceChange && payload.ChangeType == spineapi.ElementChangeRemove:
		e.guardGone(payload.Ski)
	case payload.EventType == spineapi.EventTypeEntityChange && payload.ChangeType == spineapi.ElementChangeRemove && e.isGuard(payload.Entity):
		e.guardGone(payload.Ski)
	case payload.EventType == spineapi.EventTypeDataChange && e.isGuard(payload.Entity):
		switch payload.Data.(type) {
		case *model.DeviceDiagnosisHeartbeatDataType:
			e.guardHeartbeat()
		case *model.DeviceDiagnosisStateDataType:
			e.guardState()
		}
	}
}

func (e *evSim) guardAppeared(entity spineapi.EntityRemoteInterface) {
	dd, err := client.NewDeviceDiagnosis(e.entity, entity)
	if err == nil {
		_, err = dd.Subscribe()
	}
	if err != nil {
		log.Printf("simulator[%s]: EV: subscribing to the Energy Guard's DeviceDiagnosis failed: %v", e.id, err)
	} else {
		e.mu.Lock()
		e.guard, e.guardEntity = dd, entity
		e.guardSeen, e.guardFailed = time.Time{}, false
		e.mu.Unlock()
		_, _ = dd.RequestHeartbeat()
		_, _ = dd.RequestState()
		log.Printf("simulator[%s]: EV: watching the Energy Guard %s (heartbeat and operating state)", e.id, entity.Device().Ski())
	}
}

func (e *evSim) guardGone(ski string) {
	e.mu.Lock()
	if e.guardEntity != nil && e.guardEntity.Device().Ski() == ski {
		e.guard, e.guardEntity = nil, nil
		e.guardSeen, e.guardFailed = time.Time{}, false
	}
	e.mu.Unlock()
}

func (e *evSim) isGuard(entity spineapi.EntityRemoteInterface) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return entity != nil && e.guardEntity != nil && entity.Device().Ski() == e.guardEntity.Device().Ski() &&
		fmt.Sprint(entity.Address().Entity) == fmt.Sprint(e.guardEntity.Address().Entity)
}

func (e *evSim) guardHeartbeat() {
	e.mu.Lock()
	e.guardSeen = time.Now()
	e.mu.Unlock()
}

func (e *evSim) guardState() {
	e.mu.Lock()
	guard := e.guard
	e.mu.Unlock()
	if guard != nil {
		if state, err := guard.GetState(); err == nil && state != nil && state.OperatingState != nil {
			e.mu.Lock()
			e.guardFailed = *state.OperatingState == model.DeviceDiagnosisOperatingStateTypeFailure
			e.mu.Unlock()
		}
	}
}
