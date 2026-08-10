package eebusgo

import (
	"fmt"

	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/cem/cevc"
	"github.com/enbility/eebus-go/usecases/cem/evcc"
	"github.com/enbility/eebus-go/usecases/cem/evcem"
	"github.com/enbility/eebus-go/usecases/cem/evsecc"
	"github.com/enbility/eebus-go/usecases/cem/evsoc"
	spineapi "github.com/enbility/spine-go/api"
)

// EVCC wraps eebus-go's cem/evcc use case (evCommissioningAndConfiguration): connection
// state, charge state, and identification data for an EV entity.
type EVCC struct{ uc *evcc.EVCC }

// EVSECC wraps cem/evsecc (evseCommissioningAndConfiguration): manufacturer data and
// operating state for an EVSE entity.
type EVSECC struct{ uc *evsecc.EVSECC }

// CEVC wraps cem/cevc (coordinatedEvCharging): charge strategy, energy demand, and plan.
type CEVC struct{ uc *cevc.CEVC }

// EVCEM wraps cem/evcem (measurementOfElectricityDuringEvCharging): live per-phase current/
// power and cumulative energy charged.
type EVCEM struct{ uc *evcem.EVCEM }

// EVSOC wraps cem/evsoc (evStateOfCharge).
type EVSOC struct{ uc *evsoc.EVSOC }

// evRecord is the JSON shape for one EV, matching intelligence_snapshot()'s ev_by_key merge
// across EVCC/EVCEM/EVSOC/CEVC entries for the same (device, entity) key.
type evRecord struct {
	Entity                []uint       `json:"entity"`
	Device                string       `json:"device,omitempty"`
	Connected             *bool        `json:"connected,omitempty"`
	ChargeState           string       `json:"charge_state,omitempty"`
	CommunicationStandard string       `json:"communication_standard,omitempty"`
	AsymmetricCharging    *bool        `json:"asymmetric_charging,omitempty"`
	Identifications       []string     `json:"identifications,omitempty"`
	ChargingPowerLimitsW  *powerLimits `json:"charging_power_limits_w,omitempty"`
	// InSleepMode distinguishes "the vehicle is asleep" from "the vehicle is gone": a sleeping
	// EV keeps reporting connected while all its measurements read null, which otherwise looks
	// identical to a fault.
	InSleepMode *bool `json:"in_sleep_mode,omitempty"`
	// Manufacturer is the EV's own nameplate. Separate fields, not one string: identifying a
	// vehicle in a test report needs the serial number on its own.
	Manufacturer     *manufacturer `json:"manufacturer,omitempty"`
	PhasesConnected  *uint         `json:"phases_connected,omitempty"`
	CurrentPerPhaseA []float64     `json:"current_per_phase_a,omitempty"`
	PowerPerPhaseW   []float64     `json:"power_per_phase_w,omitempty"`
	EnergyChargedWh  *float64      `json:"energy_charged_wh,omitempty"`
	StateOfCharge    *float64      `json:"state_of_charge,omitempty"`
	ChargeStrategy   string        `json:"charge_strategy,omitempty"`
	EnergyDemand     *demand       `json:"energy_demand,omitempty"`
	ChargePlan       *chargePlan   `json:"charge_plan,omitempty"`
}

// manufacturer is the subset of the upstream nameplate that identifies a device in a report.
// The remaining fields (power source, node identification, labels) are omitted deliberately --
// every device leaves most of them empty.
type manufacturer struct {
	DeviceName       string `json:"device_name,omitempty"`
	DeviceCode       string `json:"device_code,omitempty"`
	SerialNumber     string `json:"serial_number,omitempty"`
	BrandName        string `json:"brand_name,omitempty"`
	VendorName       string `json:"vendor_name,omitempty"`
	SoftwareRevision string `json:"software_revision,omitempty"`
	HardwareRevision string `json:"hardware_revision,omitempty"`
}

// manufacturerFrom returns nil when the device answered with an entirely empty nameplate, so
// the field is omitted rather than rendering as an object of blank strings.
func manufacturerFrom(data ucapi.ManufacturerData) *manufacturer {
	m := manufacturer{
		DeviceName:       data.DeviceName,
		DeviceCode:       data.DeviceCode,
		SerialNumber:     data.SerialNumber,
		BrandName:        data.BrandName,
		VendorName:       data.VendorName,
		SoftwareRevision: data.SoftwareRevision,
		HardwareRevision: data.HardwareRevision,
	}
	if m == (manufacturer{}) {
		return nil
	}
	return &m
}

type powerLimits struct {
	Minimum float64 `json:"minimum"`
	Maximum float64 `json:"maximum"`
	Standby float64 `json:"standby"`
}

type demand struct {
	MinDemand          float64 `json:"min_demand"`
	OptDemand          float64 `json:"opt_demand"`
	MaxDemand          float64 `json:"max_demand"`
	DurationUntilStart float64 `json:"duration_until_start"`
	DurationUntilEnd   float64 `json:"duration_until_end"`
}

type chargePlanSlot struct {
	Value    float64 `json:"value"`
	MinValue float64 `json:"min_value"`
	MaxValue float64 `json:"max_value"`
}

type chargePlan struct {
	Slots []chargePlanSlot `json:"slots"`
}

type evseRecord struct {
	Entity         []uint  `json:"entity"`
	Manufacturer   *string `json:"manufacturer,omitempty"`
	OperatingState string  `json:"operating_state,omitempty"`
	LastError      string  `json:"last_error,omitempty"`
}

// evKey identifies one EV/EVSE entity across use cases, matching intelligence_snapshot()'s
// ev_by_key dict key of (device, tuple(entity)).
func evKey(e spineapi.EntityRemoteInterface) string {
	return fmt.Sprintf("%s:%v", e.Device().Ski(), entityAddress(e))
}

// collectEV merges EVCC/EVCEM/EVSOC/CEVC data for every EV entity on ski into one record per
// entity, matching intelligence_snapshot()'s ev_by_key merge. Order is first-seen across
// EVCC, then EVCEM, then EVSOC, then CEVC, mirroring the Python loop order exactly so two
// runs against the same peer produce vehicles in the same order.
func (s *Stack) collectEV(ski string) []evRecord {
	byKey := map[string]*evRecord{}
	var order []string
	get := func(entity spineapi.EntityRemoteInterface) *evRecord {
		key := evKey(entity)
		if rec, ok := byKey[key]; ok {
			return rec
		}
		rec := &evRecord{Entity: entityAddress(entity), Device: entity.Device().Ski()}
		byKey[key] = rec
		order = append(order, key)
		return rec
	}

	for _, entity := range entitiesForSKI(s.evcc.uc.RemoteEntitiesScenarios(), ski) {
		rec := get(entity)
		connected := s.evcc.uc.EVConnected(entity)
		rec.Connected = &connected
		if v, err := s.evcc.uc.ChargeState(entity); err == nil {
			rec.ChargeState = string(v)
		}
		if v, err := s.evcc.uc.CommunicationStandard(entity); err == nil {
			rec.CommunicationStandard = string(v)
		}
		if v, err := s.evcc.uc.AsymmetricChargingSupport(entity); err == nil {
			rec.AsymmetricCharging = &v
		}
		if v, err := s.evcc.uc.Identifications(entity); err == nil {
			for _, id := range v {
				rec.Identifications = append(rec.Identifications, id.Value)
			}
		}
		if minW, maxW, standbyW, err := s.evcc.uc.ChargingPowerLimits(entity); err == nil {
			rec.ChargingPowerLimitsW = &powerLimits{Minimum: minW, Maximum: maxW, Standby: standbyW}
		}
		if v, err := s.evcc.uc.IsInSleepMode(entity); err == nil {
			rec.InSleepMode = &v
		}
		if v, err := s.evcc.uc.ManufacturerData(entity); err == nil {
			if m := manufacturerFrom(v); m != nil {
				rec.Manufacturer = m
			}
		}
	}

	for _, entity := range entitiesForSKI(s.evcem.uc.RemoteEntitiesScenarios(), ski) {
		rec := get(entity)
		if v, err := s.evcem.uc.PhasesConnected(entity); err == nil {
			rec.PhasesConnected = &v
		}
		if v, err := s.evcem.uc.CurrentPerPhase(entity); err == nil {
			rec.CurrentPerPhaseA = v
		}
		if v, err := s.evcem.uc.PowerPerPhase(entity); err == nil {
			rec.PowerPerPhaseW = v
		}
		if v, err := s.evcem.uc.EnergyCharged(entity); err == nil {
			rec.EnergyChargedWh = &v
		}
	}

	for _, entity := range entitiesForSKI(s.evsoc.uc.RemoteEntitiesScenarios(), ski) {
		rec := get(entity)
		if v, err := s.evsoc.uc.StateOfCharge(entity); err == nil {
			rec.StateOfCharge = &v
		}
	}

	for _, entity := range entitiesForSKI(s.cevc.uc.RemoteEntitiesScenarios(), ski) {
		rec := get(entity)
		rec.ChargeStrategy = string(s.cevc.uc.ChargeStrategy(entity))
		if d, err := s.cevc.uc.EnergyDemand(entity); err == nil {
			rec.EnergyDemand = &demand{
				MinDemand: d.MinDemand, OptDemand: d.OptDemand, MaxDemand: d.MaxDemand,
				DurationUntilStart: d.DurationUntilStart, DurationUntilEnd: d.DurationUntilEnd,
			}
		}
		if plan, err := s.cevc.uc.ChargePlan(entity); err == nil {
			slots := make([]chargePlanSlot, 0, len(plan.Slots))
			for _, slot := range plan.Slots {
				slots = append(slots, chargePlanSlot{Value: slot.Value, MinValue: slot.MinValue, MaxValue: slot.MaxValue})
			}
			rec.ChargePlan = &chargePlan{Slots: slots}
		}
	}

	out := make([]evRecord, 0, len(order))
	for _, key := range order {
		out = append(out, *byKey[key])
	}
	return out
}
