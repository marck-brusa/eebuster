package eebusgo

import (
	"github.com/enbility/eebus-go/usecases/ma/mgcp"
	spineapi "github.com/enbility/spine-go/api"
)

// MGCP wraps eebus-go's ma/mgcp use case (monitoringOfGridConnectionPoint), matching
// JsonRpcAdapter._mgcp_read_entity's field set.
type MGCP struct{ uc *mgcp.MGCP }

func (m *MGCP) Read(ski string, entityHint []uint) (map[string]any, error) {
	entity, err := resolveEntity(m.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	return mgcpFields(m.uc, entity), nil
}

// mgcpFields is shared between MGCP.Read and Stack.EnergySnapshot -- see mpcFields.
func mgcpFields(uc *mgcp.MGCP, entity spineapi.EntityRemoteInterface) map[string]any {
	out := map[string]any{}
	if v, err := uc.Power(entity); err == nil {
		out["power_w"] = v
	}
	if v, err := uc.PowerLimitationFactor(entity); err == nil {
		out["power_limitation_factor"] = v
	}
	if v, err := uc.EnergyFeedIn(entity); err == nil {
		out["energy_feed_in_wh"] = v
	}
	if v, err := uc.EnergyConsumed(entity); err == nil {
		out["energy_consumed_wh"] = v
	}
	if v, err := uc.CurrentPerPhase(entity); err == nil {
		out["current_per_phase_a"] = v
	}
	if v, err := uc.VoltagePerPhase(entity); err == nil {
		out["voltage_per_phase_v"] = v
	}
	if v, err := uc.Frequency(entity); err == nil {
		out["frequency_hz"] = v
	}
	return out
}
