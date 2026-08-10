package eebusgo

import "github.com/enbility/eebus-go/usecases/cem/vapd"

// VAPD wraps eebus-go's cem/vapd use case (visualizationOfAggregatedPhotovoltaicData).
type VAPD struct{ uc *vapd.VAPD }

type pvRecord struct {
	Entity       []uint   `json:"entity"`
	PowerW       *float64 `json:"power_w,omitempty"`
	NominalPeakW *float64 `json:"nominal_peak_w,omitempty"`
	YieldTotalWh *float64 `json:"yield_total_wh,omitempty"`
}
