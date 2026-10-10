// Package ucspec holds what the EEBUS use-case specifications say about each use case the
// testbench knows: its scenarios, and for each scenario whether the device-side actor and the
// testbench-side actor must (M), should (R) or may (O) implement it. The values come from the
// "Scenario implementation requirements for Actors" table of each UC TS. Only identifiers,
// scenario titles and requirement levels are recorded here; the specifications themselves are
// not reproduced.
package ucspec

import "strings"

// Level is one cell of a scenario implementation table.
type Level string

const (
	Mandatory     Level = "M"
	Recommended   Level = "R"
	Optional      Level = "O"
	NotApplicable Level = "-"
)

// Scenario is one row of a scenario implementation table.
type Scenario struct {
	Number    uint   `json:"number"`
	Title     string `json:"title"`
	Device    Level  `json:"device"`
	Testbench Level  `json:"testbench"`
}

// UseCase is one use case as its technical specification defines it.
type UseCase struct {
	Name     string `json:"name"`
	Acronym  string `json:"acronym"`
	Title    string `json:"title"`
	Document string `json:"document"`
	// DeviceActor is the actor the device under test plays, as SPINE spells it in use-case
	// discovery. TestbenchActor is the side the testbench plays.
	DeviceActor    string     `json:"device_actor"`
	TestbenchActor string     `json:"testbench_actor"`
	Table          string     `json:"table"`
	Scenarios      []Scenario `json:"scenarios"`
	// TestSpecification names the EEBUS High-Level Test Specification, where one exists.
	TestSpecification string `json:"test_specification,omitempty"`
}

var catalog = []UseCase{
	{
		Name: "limitationOfPowerConsumption", Acronym: "LPC", Title: "Limitation of Power Consumption",
		Document: "EEBus UC TS Limitation of Power Consumption V1.0.0", Table: "Table 2",
		DeviceActor: "ControllableSystem", TestbenchActor: "EnergyGuard",
		TestSpecification: "EEBus LPC High-Level TestSpec V1.0.0",
		Scenarios: []Scenario{
			{1, "Control active power consumption limit", Mandatory, Mandatory},
			{2, "Failsafe values", Mandatory, Mandatory},
			{3, "Heartbeat", Mandatory, Mandatory},
			{4, "Constraints", Recommended, Mandatory},
		},
	},
	{
		Name: "limitationOfPowerProduction", Acronym: "LPP", Title: "Limitation of Power Production",
		Document: "EEBus UC TS Limitation of Power Production V1.0.0", Table: "Table 2",
		DeviceActor: "ControllableSystem", TestbenchActor: "EnergyGuard",
		TestSpecification: "EEBus LPP High-Level TestSpec V1.0.0",
		Scenarios: []Scenario{
			{1, "Control active power production limit", Mandatory, Mandatory},
			{2, "Failsafe values", Mandatory, Mandatory},
			{3, "Heartbeat", Mandatory, Mandatory},
			{4, "Constraints", Recommended, Mandatory},
		},
	},
	{
		Name: "monitoringOfPowerConsumption", Acronym: "MPC", Title: "Monitoring of Power Consumption",
		Document: "EEBus UC TS Monitoring of Power Consumption V1.0.0", Table: "Table 1",
		DeviceActor: "MonitoredUnit", TestbenchActor: "MonitoringAppliance",
		TestSpecification: "EEBus MPC High-Level TestSpec V1.0.0",
		Scenarios: []Scenario{
			{1, "Monitor power", Mandatory, Mandatory},
			{2, "Monitor energy", Optional, Optional},
			{3, "Monitor current", Recommended, Recommended},
			{4, "Monitor voltage", Optional, Optional},
			{5, "Monitor frequency", Optional, Optional},
		},
	},
	{
		Name: "monitoringOfGridConnectionPoint", Acronym: "MGCP", Title: "Monitoring of Grid Connection Point",
		Document: "EEBus UC TS Monitoring of Grid Connection Point V1.0.0", Table: "Table 1",
		DeviceActor: "GridConnectionPoint", TestbenchActor: "MonitoringAppliance",
		TestSpecification: "EEBus MGCP High-Level TestSpec V1.0.0",
		Scenarios: []Scenario{
			{1, "Monitor PV feed-in power limitation factor", Optional, Optional},
			{2, "Monitor momentary power consumption/production", Mandatory, Recommended},
			{3, "Monitor total feed-in energy", Mandatory, Optional},
			{4, "Monitor total consumed energy", Mandatory, Optional},
			{5, "Monitor momentary current consumption/production phase details", Recommended, Optional},
			{6, "Monitor voltage phase details", Optional, Optional},
			{7, "Monitor frequency", Optional, Optional},
		},
	},
	{
		Name: "evCommissioningAndConfiguration", Acronym: "EVCC", Title: "EV Commissioning and Configuration",
		Document: "EEBus UC TS EV Commissioning and Configuration V1.0.1", Table: "Table 1",
		DeviceActor: "EV", TestbenchActor: "CEM",
		Scenarios: []Scenario{
			{1, "EV connected", Mandatory, Mandatory},
			{2, "EV sends communication standard", Mandatory, Mandatory},
			{3, "EV sends support of asymmetric charging", Mandatory, Mandatory},
			{4, "EV sends identification", Recommended, Recommended},
			{5, "EV sends manufacturer information", Recommended, Recommended},
			{6, "EV sends charging power limits", Recommended, Mandatory},
			{7, "EV sleep mode", Recommended, Mandatory},
			{8, "EV disconnected", Mandatory, Mandatory},
		},
	},
	{
		Name: "evseCommissioningAndConfiguration", Acronym: "EVSECC", Title: "EVSE Commissioning and Configuration",
		Document: "EEBus UC TS EVSE Commissioning and Configuration V1.0.1", Table: "Table 1",
		DeviceActor: "EVSE", TestbenchActor: "CEM",
		Scenarios: []Scenario{
			{1, "EVSE sends manufacturer information", Recommended, Recommended},
			{2, "EVSE sends error state", Mandatory, Mandatory},
		},
	},
	{
		Name: "evChargingSummary", Acronym: "EVCS", Title: "EV Charging Summary",
		Document: "EEBus UC TS EV Charging Summary V1.0.1", Table: "Table 1",
		DeviceActor: "EVSE", TestbenchActor: "EnergyBroker",
		Scenarios: []Scenario{
			{1, "Energy Broker sends charging session summary to EVSE", Mandatory, Mandatory},
		},
	},
	{
		Name: "coordinatedEvCharging", Acronym: "CEVC", Title: "Coordinated EV Charging",
		Document: "EEBus UC TS Coordinated EV Charging V1.0.1", Table: "Table 1",
		DeviceActor: "EV", TestbenchActor: "EnergyGuard",
		Scenarios: []Scenario{
			{1, "EV sends charging energy demand", Recommended, Mandatory},
			{2, "Energy Guard sends maximum power limitation curve", Mandatory, Mandatory},
			{3, "Energy Broker sends incentive table", Mandatory, NotApplicable},
			{4, "EV sends charging plan curve", Mandatory, Mandatory},
			{5, "Energy Guard heartbeat", Mandatory, Mandatory},
			{6, "Energy Broker heartbeat", Mandatory, NotApplicable},
			{7, "Energy Guard error state", Mandatory, Mandatory},
			{8, "Energy Broker error state", Mandatory, NotApplicable},
		},
	},
	{
		Name: "measurementOfElectricityDuringEvCharging", Acronym: "EVCEM", Title: "Measurement of Electricity During EV Charging",
		Document: "EEBus UC TS EV Charging Electricity Measurement V1.0.1", Table: "Table 1",
		DeviceActor: "EV", TestbenchActor: "CEM",
		Scenarios: []Scenario{
			{1, "Measure EV charging current", Recommended, Mandatory},
			{2, "Measure EV charging power", Optional, Mandatory},
			{3, "Measure EV charged energy", Optional, Mandatory},
		},
	},
	{
		Name: "evStateOfCharge", Acronym: "EVSOC", Title: "EV State of Charge",
		Document: "EEBus UC TS EV State of Charge V1.0.0 RC1", Table: "Table 1",
		DeviceActor: "EV", TestbenchActor: "MonitoringAppliance",
		Scenarios: []Scenario{
			{1, "Monitor EV state of charge", Mandatory, Mandatory},
			{2, "Monitor EV nominal capacity", Optional, Optional},
			{3, "Monitor EV state of health", Optional, Optional},
			{4, "Monitor EV actual travel range", Optional, Optional},
		},
	},
	{
		Name: "visualizationOfAggregatedPhotovoltaicData", Acronym: "VAPD", Title: "Visualization of Aggregated Photovoltaic Data",
		Document: "EEBus UC TS Visualization of Aggregated Photovoltaic Data V1.0.0 RC1", Table: "Table 1",
		DeviceActor: "PVSystem", TestbenchActor: "VisualizationAppliance",
		Scenarios: []Scenario{
			{1, "Monitor nominal peak power", Mandatory, Recommended},
			{2, "Monitor current photovoltaic power production", Mandatory, Mandatory},
			{3, "Monitor cumulated photovoltaic yield", Mandatory, Mandatory},
		},
	},
	{
		Name: "visualizationOfAggregatedBatteryData", Acronym: "VABD", Title: "Visualization of Aggregated Battery Data",
		Document: "EEBus UC TS Visualization of Aggregated Battery Data V1.0.0 RC1", Table: "Table 1",
		DeviceActor: "BatterySystem", TestbenchActor: "VisualizationAppliance",
		Scenarios: []Scenario{
			{1, "Monitor current battery system (dis)charge power", Mandatory, Mandatory},
			{2, "Monitor cumulated battery system charge energy", Recommended, Recommended},
			{3, "Monitor cumulated battery system discharge energy", Recommended, Recommended},
			{4, "Monitor current state of charge of the battery system", Mandatory, Recommended},
		},
	},
	{
		Name: "overloadProtectionByEvChargingCurrentCurtailment", Acronym: "OPEV", Title: "Overload Protection by EV Charging Current Curtailment",
		Document: "EEBus UC TS Overload Protection by EV Charging Current Curtailment V1.0.1b", Table: "Table 1",
		DeviceActor: "EV", TestbenchActor: "EnergyGuard",
		Scenarios: []Scenario{
			{1, "Energy Guard curtails charging current of EV", Mandatory, Mandatory},
			{2, "EV checks Energy Guard availability", Mandatory, Mandatory},
			{3, "Energy Guard sends error state", Mandatory, Mandatory},
		},
	},
	{
		Name: "optimizationOfSelfConsumptionDuringEvCharging", Acronym: "OSCEV", Title: "Optimization of Self-Consumption During EV Charging",
		Document: "EEBus UC TS Optimization of Self-Consumption During EV Charging V1.0.1b", Table: "Table 1",
		DeviceActor: "EV", TestbenchActor: "CEM",
		Scenarios: []Scenario{
			{1, "CEM informs EV about self-produced current", Mandatory, Mandatory},
			{2, "EV checks CEM availability", Mandatory, Mandatory},
			{3, "CEM sends error state", Mandatory, Mandatory},
		},
	},
	{
		Name: "optimizationOfSelfConsumptionByHeatPumpCompressorFlexibility", Acronym: "OHPCF", Title: "Optimization of Self-Consumption by Heat Pump Compressor Flexibility",
		Document: "EEBus UC TS Optimization of Self-Consumption by Heat Pump Compressor Flexibility V1.0.0", Table: "Table 1",
		DeviceActor: "Compressor", TestbenchActor: "CEM",
		Scenarios: []Scenario{
			{1, "Monitor heat pump compressor's power consumption flexibility", Mandatory, Mandatory},
			{2, "Control heat pump compressor's power consumption flexibility", Mandatory, Mandatory},
		},
	},
}

// All returns every use case in catalog order: limitation, monitoring, e-mobility, then the
// rest. Reports and run ordering follow this order.
func All() []UseCase {
	out := make([]UseCase, len(catalog))
	copy(out, catalog)
	return out
}

// Lookup finds a use case by acronym ("LPC") or SPINE name ("limitationOfPowerConsumption"),
// ignoring case.
func Lookup(key string) (UseCase, bool) {
	var found UseCase
	ok := false
	for _, uc := range catalog {
		if !ok && (strings.EqualFold(uc.Acronym, key) || strings.EqualFold(uc.Name, key)) {
			found, ok = uc, true
		}
	}
	return found, ok
}

// Order returns the catalog position of a use case, or the catalog length for an unknown one,
// so unknown use cases sort last.
func Order(key string) int {
	position := len(catalog)
	for i, uc := range catalog {
		if position == len(catalog) && (strings.EqualFold(uc.Acronym, key) || strings.EqualFold(uc.Name, key)) {
			position = i
		}
	}
	return position
}

// Scenario returns one scenario of the use case.
func (uc UseCase) Scenario(number uint) (Scenario, bool) {
	var found Scenario
	ok := false
	for _, s := range uc.Scenarios {
		if s.Number == number {
			found, ok = s, true
		}
	}
	return found, ok
}

// MissingScenarios compares the scenarios a device advertises for this use case with the
// specification table. mandatory lists the scenarios the device must implement and does not
// advertise; others lists the recommended and optional ones it does not advertise.
func (uc UseCase) MissingScenarios(advertised []uint) (mandatory, others []Scenario) {
	have := map[uint]bool{}
	for _, n := range advertised {
		have[n] = true
	}
	for _, s := range uc.Scenarios {
		if !have[s.Number] {
			if s.Device == Mandatory {
				mandatory = append(mandatory, s)
			} else if s.Device != NotApplicable {
				others = append(others, s)
			}
		}
	}
	return mandatory, others
}
