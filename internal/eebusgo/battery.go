package eebusgo

import "github.com/enbility/eebus-go/usecases/cem/vabd"

// VABD wraps eebus-go's cem/vabd use case (visualizationOfAggregatedBatteryData).
type VABD struct{ uc *vabd.VABD }

type batteryRecord struct {
	Entity             []uint   `json:"entity"`
	PowerW             *float64 `json:"power_w,omitempty"`
	StateOfCharge      *float64 `json:"state_of_charge,omitempty"`
	EnergyChargedWh    *float64 `json:"energy_charged_wh,omitempty"`
	EnergyDischargedWh *float64 `json:"energy_discharged_wh,omitempty"`
}
