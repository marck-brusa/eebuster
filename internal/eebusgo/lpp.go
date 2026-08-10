package eebusgo

import (
	"github.com/enbility/eebus-go/usecases/eg/lpp"

	"github.com/marck-brusa/eebuster/internal/iso8601"
)

// LPP mirrors LPC but for production limits -- see docs/10-eebus-go.md, same shape as LPC
// with Consumption* renamed to Production* upstream.
type LPP struct{ uc *lpp.LPP }

func (l *LPP) ReadLimit(ski string, entityHint []uint) (LoadLimit, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return LoadLimit{}, err
	}
	limit, err := l.uc.ProductionLimit(entity)
	if err != nil {
		return LoadLimit{}, err
	}
	return loadLimitOut(limit), nil
}

func (l *LPP) WriteLimit(ski string, in LoadLimit, entityHint []uint) error {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return err
	}
	limit, err := loadLimitIn(in)
	if err != nil {
		return err
	}
	_, err = l.uc.WriteProductionLimit(entity, limit, nil)
	return err
}

func (l *LPP) ReadFailsafe(ski string, entityHint []uint) (FailsafeLimit, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return FailsafeLimit{}, err
	}
	value, err := l.uc.FailsafeProductionActivePowerLimit(entity)
	if err != nil {
		return FailsafeLimit{}, err
	}
	duration, err := l.uc.FailsafeDurationMinimum(entity)
	if err != nil {
		return FailsafeLimit{}, err
	}
	return FailsafeLimit{ValueW: &value, Duration: iso8601.Format(duration)}, nil
}

func (l *LPP) WriteFailsafe(ski string, in FailsafeLimit, entityHint []uint) error {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return err
	}
	if in.ValueW != nil {
		if _, err := l.uc.WriteFailsafeProductionActivePowerLimit(entity, *in.ValueW); err != nil {
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

func (l *LPP) NominalMax(ski string, entityHint []uint) (float64, error) {
	entity, err := resolveEntity(l.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return 0, err
	}
	return l.uc.ProductionNominalMax(entity)
}
