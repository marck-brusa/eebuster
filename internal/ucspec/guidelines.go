package ucspec

import "strings"

// Guideline is a document that places requirements on an EEBUS device's counterpart beyond
// the use-case specifications -- what a device meets in the field, such as a grid operator's
// control box -- and that test cases cite next to the use case. Only requirement identifiers
// and the section of each are recorded; the document itself is not reproduced.
type Guideline struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Title string `json:"title"`
	// Sections maps each requirement id to the section that states it.
	Sections map[string]string `json:"-"`
}

var guidelines = []Guideline{
	{
		ID: "vde-fnn-steuerbox", Label: "VDE FNN",
		Title: "VDE FNN Lastenheft Steuerbox V1.5 (Control Box Guideline)",
		// Chapter 10 (digital interface) and Annex A (EEBUS). EEBUS_005 is listed in the
		// requirement table but has no text in V1.5.
		Sections: map[string]string{
			"STB_0413": "10", "STB_0414": "10.1", "STB_0415": "10.1", "STB_0416": "10.1", "STB_0445": "10.1",
			"STB_0417": "10.1.1", "STB_0418": "10.1.1", "STB_0419": "10.1.1",
			"STB_0420": "10.1.2", "STB_0421": "10.1.2", "STB_0422": "10.1.3", "STB_0423": "10.1.3",
			"STB_0424": "10.2", "STB_0425": "10.2",
			"STB_0426": "10.3", "STB_0427": "10.3", "STB_0428": "10.3", "STB_0455": "10.3", "STB_0456": "10.3",
			"STB_0497": "10.3", "STB_0498": "10.3",

			"EEBUS_001": "A.4", "EEBUS_002": "A.4", "EEBUS_003": "A.4", "EEBUS_004": "A.4", "EEBUS_006": "A.4",
			"EEBUS_007": "A.4", "EEBUS_008": "A.4", "EEBUS_092": "A.4", "EEBUS_100": "A.4",
			"EEBUS_009": "A.5", "EEBUS_010": "A.5", "EEBUS_011": "A.5", "EEBUS_012": "A.5", "EEBUS_071": "A.5",
			"EEBUS_072": "A.5", "EEBUS_073": "A.5",
			"EEBUS_013": "A.5.1.2", "EEBUS_014": "A.5.1.2", "EEBUS_015": "A.5.1.2", "EEBUS_016": "A.5.1.2",
			"EEBUS_017": "A.5.1.2", "EEBUS_018": "A.5.1.2",
			"EEBUS_019": "A.5.1.3", "EEBUS_020": "A.5.1.3", "EEBUS_021": "A.5.1.3", "EEBUS_022": "A.5.1.3",
			"EEBUS_023": "A.5.1.3", "EEBUS_024": "A.5.1.3", "EEBUS_025": "A.5.1.3", "EEBUS_026": "A.5.1.3",
			"EEBUS_027": "A.5.1.3", "EEBUS_028": "A.5.1.3", "EEBUS_029": "A.5.1.3", "EEBUS_030": "A.5.1.3",
			"EEBUS_070": "A.5.1.3", "EEBUS_090": "A.5.1.3", "EEBUS_096": "A.5.1.3",
			"EEBUS_074": "A.5.1.4", "EEBUS_075": "A.5.1.4", "EEBUS_076": "A.5.1.4", "EEBUS_077": "A.5.1.4",
			"EEBUS_078": "A.5.1.4", "EEBUS_079": "A.5.1.4", "EEBUS_080": "A.5.1.4", "EEBUS_081": "A.5.1.4",
			"EEBUS_082": "A.5.1.4", "EEBUS_083": "A.5.1.4", "EEBUS_084": "A.5.1.4", "EEBUS_085": "A.5.1.4",
			"EEBUS_086": "A.5.1.4", "EEBUS_091": "A.5.1.4", "EEBUS_097": "A.5.1.4",
			"EEBUS_031": "A.5.1.5", "EEBUS_032": "A.5.1.5", "EEBUS_033": "A.5.1.5", "EEBUS_034": "A.5.1.5",
			"EEBUS_035": "A.5.1.5", "EEBUS_036": "A.5.1.5", "EEBUS_037": "A.5.1.5", "EEBUS_038": "A.5.1.5",
			"EEBUS_039": "A.5.2.2", "EEBUS_040": "A.5.2.2", "EEBUS_041": "A.5.2.2", "EEBUS_042": "A.5.2.2",
			"EEBUS_043": "A.5.2.2", "EEBUS_044": "A.5.2.2", "EEBUS_087": "A.5.2.2",
			"EEBUS_045": "A.5.3.2", "EEBUS_046": "A.5.3.2", "EEBUS_047": "A.5.3.2", "EEBUS_048": "A.5.3.2",
			"EEBUS_049": "A.5.3.2", "EEBUS_050": "A.5.3.2", "EEBUS_088": "A.5.3.2",
			"EEBUS_051": "A.5.4", "EEBUS_052": "A.5.4", "EEBUS_098": "A.5.4",
			"EEBUS_089": "A.6",
			"EEBUS_099": "A.7.1",
			"EEBUS_057": "A.7.2", "EEBUS_060": "A.7.2", "EEBUS_061": "A.7.2",
			"EEBUS_063": "A.7.3", "EEBUS_066": "A.7.3", "EEBUS_067": "A.7.3", "EEBUS_093": "A.7.3",
			"EEBUS_094": "A.7.3", "EEBUS_095": "A.7.3",
			"EEBUS_068": "A.7.4", "EEBUS_069": "A.7.4",
		},
	},
}

// Guidelines returns every guideline in catalog order.
func Guidelines() []Guideline {
	out := make([]Guideline, len(guidelines))
	copy(out, guidelines)
	return out
}

// LookupGuideline finds a guideline by id ("vde-fnn-steuerbox") or label ("VDE FNN"),
// ignoring case.
func LookupGuideline(key string) (Guideline, bool) {
	var found Guideline
	ok := false
	for _, g := range guidelines {
		if !ok && (strings.EqualFold(g.ID, key) || strings.EqualFold(g.Label, key)) {
			found, ok = g, true
		}
	}
	return found, ok
}

// Section returns the section that states a requirement of the guideline.
func (g Guideline) Section(requirement string) (string, bool) {
	section, ok := g.Sections[requirement]
	return section, ok
}
