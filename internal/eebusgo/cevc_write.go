package eebusgo

import (
	"fmt"
	"time"

	"github.com/enbility/eebus-go/features/client"
	spinemodel "github.com/enbility/spine-go/model"
	"github.com/enbility/spine-go/util"
)

// PowerLimitSlot is one step of a CEVC power limitation curve (scenario 2): the EV may draw at
// most PowerW for DurationS seconds, slots following each other from the curve's start.
type PowerLimitSlot struct {
	DurationS float64 `json:"duration_s"`
	PowerW    float64 `json:"power_w"`
}

// WritePowerLimits sends a power limitation curve to the EV entity behind ski.
//
// The SPINE write is built here instead of calling eebus-go's WritePowerLimits, which two
// things make unusable against a KEO device:
//
//   - it reads constraints[0] blindly. On a device whose first constraint belongs to the
//     singleDemand series -- one slot by definition -- every curve of more than one slot is
//     refused locally, before anything reaches the wire.
//   - it describes a slot only by its time period, while keo_uc_api treats a slot's duration as
//     mandatory and refuses the whole curve ("A mandatory element was not set for
//     PowerLimitationCurve").
//
// So the constraints of the writeable constraints series are used, and every slot carries its
// duration alongside the time period.
func (c *CEVC) WritePowerLimits(ski string, slots []PowerLimitSlot, entityHint []uint) error {
	entity, err := resolveEntity(c.uc.RemoteEntitiesScenarios(), ski, entityHint)
	if err != nil {
		return err
	}
	if len(slots) == 0 {
		return fmt.Errorf("slots must carry at least one entry")
	}
	for _, slot := range slots {
		if slot.DurationS <= 0 {
			return fmt.Errorf("duration_s must be positive")
		}
	}

	timeSeries, err := client.NewTimeSeries(c.uc.LocalEntity, entity)
	if err != nil {
		return fmt.Errorf("time series feature unavailable: %w", err)
	}
	filter := spinemodel.TimeSeriesDescriptionDataType{
		TimeSeriesType: util.Ptr(spinemodel.TimeSeriesTypeTypeConstraints),
	}
	descriptions, err := timeSeries.GetDescriptionsForFilter(filter)
	if err != nil || len(descriptions) == 0 {
		return fmt.Errorf("device announces no writeable constraints time series")
	}
	seriesID := descriptions[0].TimeSeriesId
	if err := checkSlotCount(timeSeries, seriesID, len(slots)); err != nil {
		return err
	}

	timeSeriesSlots := make([]spinemodel.TimeSeriesSlotType, 0, len(slots))
	total := time.Duration(0)
	for index, slot := range slots {
		duration := time.Duration(slot.DurationS * float64(time.Second))
		timeSeriesSlots = append(timeSeriesSlots, spinemodel.TimeSeriesSlotType{
			TimeSeriesSlotId: util.Ptr(spinemodel.TimeSeriesSlotIdType(index)), //nolint:gosec // slot count is bounded by the device's own constraint
			TimePeriod: &spinemodel.TimePeriodType{
				StartTime: spinemodel.NewAbsoluteOrRelativeTimeTypeFromDuration(total),
				EndTime:   spinemodel.NewAbsoluteOrRelativeTimeTypeFromDuration(total + duration),
			},
			Duration: spinemodel.NewDurationType(duration),
			MaxValue: spinemodel.NewScaledNumberType(slot.PowerW),
		})
		total += duration
	}

	_, err = timeSeries.WriteData([]spinemodel.TimeSeriesDataType{{
		TimeSeriesId: seriesID,
		TimePeriod: &spinemodel.TimePeriodType{
			StartTime: spinemodel.NewAbsoluteOrRelativeTimeType("PT0S"),
			EndTime:   spinemodel.NewAbsoluteOrRelativeTimeTypeFromDuration(total),
		},
		TimeSeriesSlot: timeSeriesSlots,
	}})
	return err
}

// checkSlotCount enforces the slot count the device announced for this very series, so a curve
// that the device cannot hold is reported here rather than as an opaque refusal on the wire. A
// series without its own constraint is left unchecked: silence is not a limit of one.
func checkSlotCount(timeSeries *client.TimeSeries, seriesID *spinemodel.TimeSeriesIdType, count int) error {
	constraints, err := timeSeries.GetConstraints()
	if err != nil {
		return nil //nolint:nilerr // no constraints published means nothing to enforce
	}
	for _, constraint := range constraints {
		if constraint.TimeSeriesId != nil && seriesID != nil && *constraint.TimeSeriesId == *seriesID {
			if constraint.SlotCountMin != nil && count < int(*constraint.SlotCountMin) {
				return fmt.Errorf("device wants at least %d slots, %d given", *constraint.SlotCountMin, count)
			}
			if constraint.SlotCountMax != nil && count > int(*constraint.SlotCountMax) {
				return fmt.Errorf("device accepts at most %d slots, %d given", *constraint.SlotCountMax, count)
			}
		}
	}
	return nil
}
