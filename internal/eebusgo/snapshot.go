package eebusgo

import "time"

func nowUnix() float64 { return float64(time.Now().UnixNano()) / 1e9 }

// Snapshot is the JSON shape for GET /energy/{ski}/snapshot, matching
// JsonRpcAdapter.intelligence_snapshot() field-for-field: partial data and per-field errors
// rather than hiding what's unavailable (docs/10-eebus-go.md: "treat the energy snapshot as
// best-effort").
type Snapshot struct {
	Ts         float64          `json:"ts"`
	SKI        string           `json:"ski"`
	Power      powerTotals      `json:"power"`
	Limits     limitsSummary    `json:"limits"`
	Monitoring []map[string]any `json:"monitoring"`
	Grid       []map[string]any `json:"grid"`
	EV         evSummary        `json:"ev"`
	PV         []pvRecord       `json:"pv"`
	Battery    []batteryRecord  `json:"battery"`
	Errors     []snapshotError  `json:"errors"`
}

type powerTotals struct {
	ConsumptionW *float64 `json:"consumption_w"`
	GridW        *float64 `json:"grid_w"`
	PVW          *float64 `json:"pv_w"`
	BatteryW     *float64 `json:"battery_w"`
	EVW          *float64 `json:"ev_w"`
}

type limitsSummary struct {
	ConsumptionW      *float64   `json:"consumption_w"`
	ConsumptionActive *bool      `json:"consumption_active"`
	ProductionW       *float64   `json:"production_w"`
	ProductionActive  *bool      `json:"production_active"`
	FailsafeW         *float64   `json:"failsafe_w"`
	NominalMaxW       *float64   `json:"nominal_max_w"`
	Consumption       *LoadLimit `json:"consumption,omitempty"`
	Production        *LoadLimit `json:"production,omitempty"`
}

type evSummary struct {
	ConnectedCount   int          `json:"connected_count"`
	ChargingCount    int          `json:"charging_count"`
	Vehicles         []evRecord   `json:"vehicles"`
	ChargingStations []evseRecord `json:"charging_stations"`
}

type snapshotError struct {
	Method string `json:"method"`
	Detail string `json:"detail"`
}

// EnergySnapshot aggregates every registered use case's live state for one connected peer,
// matching intelligence_snapshot()'s shape exactly so the existing dashboard JS renders it
// unchanged. Returns a *NotFoundError if the peer isn't currently connected -- a snapshot for
// an unreachable peer is meaningless, unlike the per-field best-effort errors below that.
func (s *Stack) EnergySnapshot(ski string) (Snapshot, error) {
	connected := false
	for _, p := range s.Peers() {
		if p.SKI == ski {
			connected = true
			break
		}
	}
	if !connected {
		return Snapshot{}, &NotFoundError{Detail: "peer " + ski + " is not connected"}
	}

	// Every slice field starts non-nil: a Go nil slice JSON-encodes as `null`, and Python's
	// `for x in null` is a TypeError, not a no-op the way `for x in []` is -- confirmed by
	// actually running examples/explore_peer.py against a live instance and watching it crash
	// on exactly this for an unrelated field (PeerUseCases' empty case). intelligence_snapshot
	// always builds these as [] in Python; match that unconditionally here too.
	snap := Snapshot{
		Ts: nowUnix(), SKI: ski,
		Monitoring: []map[string]any{}, Grid: []map[string]any{},
		PV: []pvRecord{}, Battery: []batteryRecord{},
		EV: evSummary{Vehicles: []evRecord{}, ChargingStations: []evseRecord{}},
	}
	errs := []snapshotError{}
	record := func(method string, err error) {
		if err != nil {
			errs = append(errs, snapshotError{Method: method, Detail: err.Error()})
		}
	}

	// Limits -----------------------------------------------------------------------
	if entities := entitiesForSKI(s.lpc.uc.RemoteEntitiesScenarios(), ski); len(entities) > 0 {
		entity := entities[0]
		if limit, err := s.lpc.uc.ConsumptionLimit(entity); err == nil {
			out := loadLimitOut(limit)
			snap.Limits.Consumption = &out
			if limit.IsActive {
				v := limit.Value
				snap.Limits.ConsumptionW = &v
			}
			active := limit.IsActive
			snap.Limits.ConsumptionActive = &active
		} else {
			record("eg-lpc/ConsumptionLimit", err)
		}
		if v, err := s.lpc.uc.FailsafeConsumptionActivePowerLimit(entity); err == nil {
			snap.Limits.FailsafeW = &v
		} else {
			record("eg-lpc/FailsafeConsumptionActivePowerLimit", err)
		}
		if v, err := s.lpc.uc.ConsumptionNominalMax(entity); err == nil {
			snap.Limits.NominalMaxW = &v
		} else {
			record("eg-lpc/ConsumptionNominalMax", err)
		}
	}
	if entities := entitiesForSKI(s.lpp.uc.RemoteEntitiesScenarios(), ski); len(entities) > 0 {
		entity := entities[0]
		if limit, err := s.lpp.uc.ProductionLimit(entity); err == nil {
			out := loadLimitOut(limit)
			snap.Limits.Production = &out
			if limit.IsActive {
				v := limit.Value
				snap.Limits.ProductionW = &v
			}
			active := limit.IsActive
			snap.Limits.ProductionActive = &active
		} else {
			record("eg-lpp/ProductionLimit", err)
		}
	}

	// Monitoring and grid ------------------------------------------------------------
	for _, entity := range entitiesForSKI(s.mpc.uc.RemoteEntitiesScenarios(), ski) {
		fields := mpcFields(s.mpc.uc, entity)
		fields["entity"] = entityAddress(entity)
		snap.Monitoring = append(snap.Monitoring, fields)
	}
	for _, entity := range entitiesForSKI(s.mgcp.uc.RemoteEntitiesScenarios(), ski) {
		fields := mgcpFields(s.mgcp.uc, entity)
		fields["entity"] = entityAddress(entity)
		snap.Grid = append(snap.Grid, fields)
	}

	// EVs and EVSEs -------------------------------------------------------------------
	snap.EV.Vehicles = s.collectEV(ski)
	for _, ev := range snap.EV.Vehicles {
		if ev.Connected != nil && *ev.Connected {
			snap.EV.ConnectedCount++
		}
		if ev.ChargeState == "active" {
			snap.EV.ChargingCount++
		}
	}
	for _, entity := range entitiesForSKI(s.evsecc.uc.RemoteEntitiesScenarios(), ski) {
		rec := evseRecord{Entity: entityAddress(entity)}
		if md, err := s.evsecc.uc.ManufacturerData(entity); err == nil {
			name := md.DeviceName
			rec.Manufacturer = &name
			rec.Brand = md.BrandName
			rec.VendorName = md.VendorName
			rec.VendorCode = md.VendorCode
			rec.SerialNumber = md.SerialNumber
			rec.SoftwareRevision = md.SoftwareRevision
			rec.HardwareRevision = md.HardwareRevision
			rec.DeviceCode = md.DeviceCode
		}
		if state, lastErr, err := s.evsecc.uc.OperatingState(entity); err == nil {
			rec.OperatingState = string(state)
			rec.LastError = lastErr
		}
		snap.EV.ChargingStations = append(snap.EV.ChargingStations, rec)
	}

	// PV and batteries -----------------------------------------------------------------
	for _, entity := range entitiesForSKI(s.vapd.uc.RemoteEntitiesScenarios(), ski) {
		rec := pvRecord{Entity: entityAddress(entity)}
		if v, err := s.vapd.uc.Power(entity); err == nil {
			rec.PowerW = &v
		}
		if v, err := s.vapd.uc.PowerNominalPeak(entity); err == nil {
			rec.NominalPeakW = &v
		}
		if v, err := s.vapd.uc.PVYieldTotal(entity); err == nil {
			rec.YieldTotalWh = &v
		}
		snap.PV = append(snap.PV, rec)
	}
	for _, entity := range entitiesForSKI(s.vabd.uc.RemoteEntitiesScenarios(), ski) {
		rec := batteryRecord{Entity: entityAddress(entity)}
		if v, err := s.vabd.uc.Power(entity); err == nil {
			rec.PowerW = &v
		}
		if v, err := s.vabd.uc.StateOfCharge(entity); err == nil {
			rec.StateOfCharge = &v
		}
		if v, err := s.vabd.uc.EnergyCharged(entity); err == nil {
			rec.EnergyChargedWh = &v
		}
		if v, err := s.vabd.uc.EnergyDischarged(entity); err == nil {
			rec.EnergyDischargedWh = &v
		}
		snap.Battery = append(snap.Battery, rec)
	}

	// Power totals ----------------------------------------------------------------------
	snap.Power.ConsumptionW = sumField(snap.Monitoring, "power_w")
	snap.Power.GridW = sumField(snap.Grid, "power_w")
	snap.Power.PVW = sumPV(snap.PV)
	snap.Power.BatteryW = sumBattery(snap.Battery)
	snap.Power.EVW = sumEVPower(snap.EV.Vehicles)

	snap.Errors = errs
	return snap, nil
}

func sumField(records []map[string]any, key string) *float64 {
	var total float64
	found := false
	for _, r := range records {
		if v, ok := r[key].(float64); ok {
			total += v
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}

func sumPV(records []pvRecord) *float64 {
	var total float64
	found := false
	for _, r := range records {
		if r.PowerW != nil {
			total += *r.PowerW
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}

func sumBattery(records []batteryRecord) *float64 {
	var total float64
	found := false
	for _, r := range records {
		if r.PowerW != nil {
			total += *r.PowerW
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}

func sumEVPower(records []evRecord) *float64 {
	var total float64
	found := false
	for _, r := range records {
		for _, p := range r.PowerPerPhaseW {
			total += p
			found = true
		}
	}
	if !found {
		return nil
	}
	return &total
}
