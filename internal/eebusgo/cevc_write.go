package eebusgo

import (
	"fmt"
	"time"

	ucapi "github.com/enbility/eebus-go/usecases/api"
)

// PowerLimitSlot is one step of a CEVC power limitation curve (scenario 2): the EV may draw at
// most PowerW for DurationS seconds, slots following each other from the curve's start.
type PowerLimitSlot struct {
	DurationS float64 `json:"duration_s"`
	PowerW    float64 `json:"power_w"`
}

// WritePowerLimits sends a power limitation curve to the EV entity behind ski.
func (c *CEVC) WritePowerLimits(ski string, slots []PowerLimitSlot, entityHint []uint) error {
	entity, err := resolveEntity(c.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return err
	}
	if len(slots) == 0 {
		return fmt.Errorf("slots must carry at least one entry")
	}
	in := make([]ucapi.DurationSlotValue, 0, len(slots))
	for _, s := range slots {
		if s.DurationS <= 0 {
			return fmt.Errorf("duration_s must be positive")
		}
		in = append(in, ucapi.DurationSlotValue{Duration: time.Duration(s.DurationS * float64(time.Second)), Value: s.PowerW})
	}
	return c.uc.WritePowerLimits(entity, in)
}
