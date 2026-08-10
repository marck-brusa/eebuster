package eebusgo

import (
	ucapi "github.com/enbility/eebus-go/usecases/api"
	"github.com/enbility/eebus-go/usecases/eg/lpc"

	"github.com/marck-brusa/eebuster/internal/iso8601"
)

// LoadLimit is the JSON shape for LPC/LPP limit reads and writes, matching
// src/facade/api/schemas.py's LoadLimitIn/LoadLimitOut (a single struct here since Go has no
// pydantic request/response split to justify two).
type LoadLimit struct {
	ValueW         float64 `json:"value_w"`
	IsActive       bool    `json:"is_active"`
	IsChangeable   bool    `json:"is_changeable"`
	Duration       string  `json:"duration,omitempty"`
	DeleteDuration bool    `json:"delete_duration,omitempty"`
}

// FailsafeLimit is the JSON shape for LPC/LPP failsafe reads and writes. Both fields are
// optional on write -- eebus-go exposes the failsafe value and its minimum hold duration as
// two independent upstream calls (verified against usecases/eg/lpc/public.go), so a write may
// touch just one.
type FailsafeLimit struct {
	ValueW   *float64 `json:"value_w,omitempty"`
	Duration string   `json:"duration,omitempty"`
}

// LPC wraps eebus-go's eg/lpc use case with SKI-based entity resolution, matching what
// JsonRpcAdapter's lpc_* methods did over the old RPC wire -- see docs/10-eebus-go.md.
type LPC struct{ uc *lpc.LPC }

func (l *LPC) ReadLimit(ski string, entityHint []uint) (LoadLimit, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return LoadLimit{}, err
	}
	limit, err := l.uc.ConsumptionLimit(entity)
	if err != nil {
		return LoadLimit{}, err
	}
	return loadLimitOut(limit), nil
}

func (l *LPC) WriteLimit(ski string, in LoadLimit, entityHint []uint) error {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return err
	}
	limit, err := loadLimitIn(in)
	if err != nil {
		return err
	}
	_, err = l.uc.WriteConsumptionLimit(entity, limit, nil)
	return err
}

func (l *LPC) ReadFailsafe(ski string, entityHint []uint) (FailsafeLimit, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return FailsafeLimit{}, err
	}
	value, err := l.uc.FailsafeConsumptionActivePowerLimit(entity)
	if err != nil {
		return FailsafeLimit{}, err
	}
	duration, err := l.uc.FailsafeDurationMinimum(entity)
	if err != nil {
		return FailsafeLimit{}, err
	}
	return FailsafeLimit{ValueW: &value, Duration: iso8601.Format(duration)}, nil
}

func (l *LPC) WriteFailsafe(ski string, in FailsafeLimit, entityHint []uint) error {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return err
	}
	if in.ValueW != nil {
		if _, err := l.uc.WriteFailsafeConsumptionActivePowerLimit(entity, *in.ValueW); err != nil {
			return err
		}
	}
	if in.Duration != "" {
		d, err := iso8601.Parse(in.Duration)
		if err != nil {
			return err
		}
		if _, err := l.uc.WriteFailsafeDurationMinimum(entity, d); err != nil {
			return err
		}
	}
	return nil
}

func (l *LPC) NominalMax(ski string, entityHint []uint) (float64, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return 0, err
	}
	return l.uc.ConsumptionNominalMax(entity)
}

func (l *LPC) StartHeartbeat() { l.uc.StartHeartbeat() }
func (l *LPC) StopHeartbeat()  { l.uc.StopHeartbeat() }

func (l *LPC) HeartbeatStatus(ski string, entityHint []uint) (bool, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return false, err
	}
	return l.uc.IsHeartbeatWithinDuration(entity), nil
}

func loadLimitOut(limit ucapi.LoadLimit) LoadLimit {
	out := LoadLimit{
		ValueW:       limit.Value,
		IsActive:     limit.IsActive,
		IsChangeable: limit.IsChangeable,
	}
	if limit.Duration > 0 {
		out.Duration = iso8601.Format(limit.Duration)
	}
	return out
}

func loadLimitIn(in LoadLimit) (ucapi.LoadLimit, error) {
	out := ucapi.LoadLimit{
		Value:          in.ValueW,
		IsActive:       in.IsActive,
		IsChangeable:   in.IsChangeable,
		DeleteDuration: in.DeleteDuration,
	}
	if in.Duration != "" {
		d, err := iso8601.Parse(in.Duration)
		if err != nil {
			return ucapi.LoadLimit{}, err
		}
		out.Duration = d
	}
	return out, nil
}
