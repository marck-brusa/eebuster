package eebusgo

// UseCaseDetail is one advertised use case enriched for the dashboard: live discovery data
// (name, availability, entity address, actor, version, scenarios) plus catalog labels.
// The catalog adds human-readable context only -- it never invents support: availability,
// actor, version and scenarios come exclusively from what the peer itself advertised.
// A use case outside the catalog still appears, with its wire name as the title.
type UseCaseDetail struct {
	Name            string   `json:"name"`
	Acronym         string   `json:"acronym,omitempty"`
	Title           string   `json:"title"`
	Description     string   `json:"description,omitempty"`
	Domain          string   `json:"domain,omitempty"`
	Actor           string   `json:"actor,omitempty"`
	Version         string   `json:"version,omitempty"`
	Scenarios       []uint   `json:"scenarios"`
	Address         []uint   `json:"address,omitempty"`
	Available       bool     `json:"available"`
	TypedOperations []string `json:"typed_operations"`
}

type useCaseCatalogEntry struct {
	Acronym         string
	Title           string
	Description     string
	Domain          string
	TypedOperations []string
}

// useCaseCatalog mirrors docs/20-use-case-matrix.md. TypedOperations names the typed access
// this tool offers for the use case; empty means the device browser lists it without any
// typed read or write (OPEV/OSCEV/OHPCF have no eebus-go client implementation).
var useCaseCatalog = map[string]useCaseCatalogEntry{
	"limitationOfPowerConsumption": {
		Acronym: "LPC", Title: "Limitation of Power Consumption", Domain: "limitation",
		Description:     "An energy guard limits the device's active power consumption.",
		TypedOperations: []string{"limit read/write", "failsafe", "nominal max", "heartbeat"},
	},
	"limitationOfPowerProduction": {
		Acronym: "LPP", Title: "Limitation of Power Production", Domain: "limitation",
		Description:     "An energy guard limits the device's active power production.",
		TypedOperations: []string{"limit read/write", "failsafe", "nominal max"},
	},
	"monitoringOfPowerConsumption": {
		Acronym: "MPC", Title: "Monitoring of Power Consumption", Domain: "monitoring",
		Description:     "Power, energy, per-phase values, and frequency.",
		TypedOperations: []string{"measurements"},
	},
	"monitoringOfGridConnectionPoint": {
		Acronym: "MGCP", Title: "Monitoring of Grid Connection Point", Domain: "monitoring",
		Description:     "Grid power, energy, per-phase values, and frequency.",
		TypedOperations: []string{"measurements"},
	},
	"evCommissioningAndConfiguration": {
		Acronym: "EVCC", Title: "EV Commissioning and Configuration", Domain: "e-mobility",
		Description:     "Connected state, charge state, communication standard, identity, and limits.",
		TypedOperations: []string{"snapshot"},
	},
	"evseCommissioningAndConfiguration": {
		Acronym: "EVSECC", Title: "EVSE Commissioning and Configuration", Domain: "e-mobility",
		Description:     "Charging-station identity and operating state.",
		TypedOperations: []string{"snapshot"},
	},
	"coordinatedEvCharging": {
		Acronym: "CEVC", Title: "Coordinated EV Charging", Domain: "e-mobility",
		Description:     "Charge strategy, energy demand, and charge plan.",
		TypedOperations: []string{"snapshot"},
	},
	"measurementOfElectricityDuringEvCharging": {
		Acronym: "EVCEM", Title: "Measurement of Electricity During EV Charging", Domain: "e-mobility",
		Description:     "Phases, current, power, and charged energy during a session.",
		TypedOperations: []string{"snapshot"},
	},
	"evStateOfCharge": {
		Acronym: "EVSOC", Title: "EV State of Charge", Domain: "e-mobility",
		Description:     "The vehicle's state of charge.",
		TypedOperations: []string{"snapshot"},
	},
	"visualizationOfAggregatedPhotovoltaicData": {
		Acronym: "VAPD", Title: "Visualization of Aggregated Photovoltaic Data", Domain: "visualization",
		Description:     "Photovoltaic power, peak power, and total yield.",
		TypedOperations: []string{"snapshot"},
	},
	"visualizationOfAggregatedBatteryData": {
		Acronym: "VABD", Title: "Visualization of Aggregated Battery Data", Domain: "visualization",
		Description:     "Battery power, state of charge, and energy.",
		TypedOperations: []string{"snapshot"},
	},
	"overloadProtectionByEvChargingCurrentCurtailment": {
		Acronym: "OPEV", Title: "Overload Protection by EV Charging Current Curtailment", Domain: "e-mobility",
		Description:     "Per-phase charging-current limits. No typed access in this tool (no eebus-go client implementation).",
		TypedOperations: []string{},
	},
	"optimizationOfSelfConsumptionDuringEvCharging": {
		Acronym: "OSCEV", Title: "Optimization of Self-Consumption During EV Charging", Domain: "e-mobility",
		Description:     "No typed access in this tool (no eebus-go client implementation).",
		TypedOperations: []string{},
	},
	"optimizationOfSelfConsumptionByHeatPumpCompressorFlexibility": {
		Acronym: "OHPCF", Title: "Optimization of Self-Consumption by Heat Pump Compressor Flexibility", Domain: "hvac",
		Description:     "No typed access in this tool (no eebus-go client implementation).",
		TypedOperations: []string{},
	},
}

// buildUseCaseDetails flattens the per-entity advertisement into one row per (entity,
// use case), enriched from the catalog. Order follows the advertisement, so output is
// stable between calls to the same device.
func buildUseCaseDetails(entries []UseCaseEntry) []UseCaseDetail {
	details := []UseCaseDetail{}
	for _, entry := range entries {
		for _, support := range entry.UseCaseSupport {
			if support.UseCaseName == "" {
				continue
			}
			detail := UseCaseDetail{
				Name:            support.UseCaseName,
				Title:           support.UseCaseName,
				Actor:           entry.Actor,
				Version:         support.UseCaseVersion,
				Scenarios:       append([]uint{}, support.ScenarioSupport...),
				Address:         append([]uint{}, entry.Address...),
				Available:       support.UseCaseAvailable,
				TypedOperations: []string{},
			}
			if info, known := useCaseCatalog[support.UseCaseName]; known {
				detail.Acronym = info.Acronym
				detail.Title = info.Title
				detail.Description = info.Description
				detail.Domain = info.Domain
				detail.TypedOperations = append([]string{}, info.TypedOperations...)
			}
			details = append(details, detail)
		}
	}
	return details
}
