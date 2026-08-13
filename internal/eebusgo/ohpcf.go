package eebusgo

import (
	"github.com/enbility/eebus-go/usecases/cem/ohpcf"
)

// OHPCF wraps cem/ohpcf (optimizationOfSelfConsumptionByHeatPumpCompressorFlexibility):
// reading a heat pump compressor's flexibility offer -- optional power consumption windows,
// requested power, whether the run is pausable/stoppable, and the minimal run/pause
// durations. Reading is best-effort per field, like the other measurement-style wrappers: a
// field the device doesn't deliver is omitted rather than failing the whole read.
type OHPCF struct{ uc *ohpcf.OHPCF }

func (h *OHPCF) Read(ski string, entityHint []uint) (map[string]any, error) {
	entity, err := resolveEntity(h.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	if info, err := h.uc.OptionalPowerConsumption(entity); err == nil && info != nil {
		out["optional_power_consumption"] = map[string]any{
			"power_sequence_id": info.PowerSequenceId,
			"power_w":           info.Power,
			"max_power_w":       info.MaxPower,
			"state":             info.State,
			"is_pausable":       info.IsPausable,
			"is_stoppable":      info.IsStoppable,
			"start_time":        info.StartTime,
		}
	}
	if v, err := h.uc.OptionalPowerConsumptionAvailable(entity); err == nil {
		out["optional_power_consumption_available"] = v
	}
	if v, err := h.uc.RequestedPowerEstimate(entity); err == nil {
		out["requested_power_estimate_w"] = v
	}
	if v, err := h.uc.RequestedPowerMax(entity); err == nil {
		out["requested_power_max_w"] = v
	}
	if v, err := h.uc.ConsumptionIsStoppable(entity); err == nil {
		out["consumption_stoppable"] = v
	}
	if v, err := h.uc.ConsumptionIsPausable(entity); err == nil {
		out["consumption_pausable"] = v
	}
	if v, err := h.uc.PowerConsumptionProcessStartTime(entity); err == nil {
		out["process_start_time"] = v
	}
	if v, err := h.uc.PowerConsumptionProcessState(entity); err == nil {
		out["process_state"] = v
	}
	if v, err := h.uc.PowerConsumptionMinimalRunDuration(entity); err == nil {
		out["minimal_run_duration_s"] = v.Seconds()
	}
	if v, err := h.uc.PowerConsumptionMinimalPauseDuration(entity); err == nil {
		out["minimal_pause_duration_s"] = v.Seconds()
	}
	return out, nil
}
