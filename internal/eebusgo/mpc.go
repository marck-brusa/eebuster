package eebusgo

import (
	"github.com/enbility/eebus-go/usecases/ma/mpc"
	spineapi "github.com/enbility/spine-go/api"
)

// MPC wraps eebus-go's ma/mpc use case (monitoringOfPowerConsumption), matching
// JsonRpcAdapter._mpc_read_entity's field set.
type MPC struct{ uc *mpc.MPC }

// Reading is best-effort per field, mirroring _best_effort_call: a field the peer doesn't
// support (or that errors) is omitted from the map rather than failing the whole read, since
// docs/10-eebus-go.md says to "treat the energy snapshot as best-effort".
func (m *MPC) Read(ski string, entityHint []uint) (map[string]any, error) {
	entity, err := resolveEntity(m.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return nil, err
	}
	return mpcFields(m.uc, entity), nil
}

// mpcFields is shared between MPC.Read (one resolved entity) and Stack.EnergySnapshot
// (every entity for a peer), so the field set can't drift between the two.
func mpcFields(uc *mpc.MPC, entity spineapi.EntityRemoteInterface) map[string]any {
	out := map[string]any{}
	if v, err := uc.Power(entity); err == nil {
		out["power_w"] = v
	}
	if v, err := uc.PowerPerPhase(entity); err == nil {
		out["power_per_phase_w"] = v
	}
	if v, err := uc.EnergyConsumed(entity); err == nil {
		out["energy_consumed_wh"] = v
	}
	if v, err := uc.EnergyProduced(entity); err == nil {
		out["energy_produced_wh"] = v
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
