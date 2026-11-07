package raceplan

import (
	"strings"
	"testing"

	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
)

func TestSegmentFueling(t *testing.T) {
	t.Parallel()
	// A gel has 25 g carbohydrate and 50 mg sodium, a flask 500 ml water.
	plan := func(intake ...*racesv1.Intake) *racesv1.RacePlan {
		return racesv1.RacePlan_builder{
			Nutrition: racesv1.NutritionStrategy_builder{
				CarbsGramsPerHour: 60,
				FluidMlPerHour:    500,
				SodiumMgPerHour:   400,
				Products: []*racesv1.NutritionProduct{
					racesv1.NutritionProduct_builder{Name: "Gel", CarbsGrams: 25, SodiumMg: 50}.Build(),
					racesv1.NutritionProduct_builder{Name: "Flask", FluidMl: 500}.Build(),
					racesv1.NutritionProduct_builder{Name: "Salt tab", SodiumMg: 200}.Build(),
				},
			}.Build(),
			Checkpoints: []*racesv1.Checkpoint{
				racesv1.Checkpoint_builder{
					Name: "CP1", DistanceMeters: 10000, PlannedSegmentSeconds: 2 * 3600, Intake: intake,
				}.Build(),
			},
		}.Build()
	}
	intake := func(product string, quantity float64) *racesv1.Intake {
		return racesv1.Intake_builder{Product: product, Quantity: quantity}.Build()
	}
	rule := func(p *racesv1.RacePlan) *racesv1.RuleResult {
		for _, r := range Analyze(p, RaceInfo{DistanceMeters: 10000}).GetRules() {
			if r.GetId() == "segment-fueling" {
				return r
			}
		}
		t.Fatal("no segment-fueling rule")
		return nil
	}

	// Two hours need 120 g carbohydrate, 1000 ml fluid and 800 mg sodium.
	if r := rule(plan(intake("Gel", 5), intake("Flask", 2), intake("Salt tab", 3))); r.GetStatus() != racesv1.RuleStatus_RULE_STATUS_PASS {
		t.Errorf("enough fuel: got %v %q", r.GetStatus(), r.GetMessage())
	}
	// Enough carbohydrate, but too little fluid and sodium.
	r := rule(plan(intake("Gel", 5), intake("Flask", 1)))
	if r.GetStatus() != racesv1.RuleStatus_RULE_STATUS_FAIL ||
		!strings.Contains(r.GetMessage(), "500 of 1000 ml fluid") || !strings.Contains(r.GetMessage(), "250 of 800 mg sodium") ||
		strings.Contains(r.GetMessage(), "carbohydrate") {
		t.Errorf("too little fluid and sodium: got %v %q", r.GetStatus(), r.GetMessage())
	}
	// Too much fluid is a warning.
	if r := rule(plan(intake("Gel", 5), intake("Flask", 4), intake("Salt tab", 3))); r.GetStatus() != racesv1.RuleStatus_RULE_STATUS_WARN ||
		!strings.Contains(r.GetMessage(), "2000 of 1000 ml fluid") {
		t.Errorf("too much fluid: got %v %q", r.GetStatus(), r.GetMessage())
	}
}
