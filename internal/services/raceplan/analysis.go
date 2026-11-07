package raceplan

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// Thresholds used by the rules.
const (
	minCutoffBuffer          = 30 * time.Minute
	minFueledSegment         = 30 * time.Minute
	minSegmentFuelFraction   = 0.8
	maxSegmentFuelFraction   = 1.3
	minCarbsPerHour          = 60
	maxCarbsPerHour          = 120
	minFluidPerHour          = 300
	maxFluidPerHour          = 1000
	minSodiumPerHour         = 300
	maxSodiumPerHour         = 1500
	goalTolerance            = 0.05
	finishDistanceTolerance  = 0.03
	minRaceMorningItems      = 3
	nightStartHour           = 6
	nightEndHour             = 20
	finishDistanceMinMeters  = 500
	defaultCheckpointPrefix  = "checkpoint"
	maxListedRuleViolations  = 5
	metersPerKilometer       = 1000.0
	headlampKeywordsSentence = "headlamp, head torch or headtorch"
)

// coreContingencies are the scenarios every race plan must cover.
var coreContingencies = []racesv1.ContingencyScenario{
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_GI_DISTRESS,
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_BEHIND_SCHEDULE,
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_LOW_ENERGY,
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_INJURY,
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_WEATHER,
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_GEAR_FAILURE,
	racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_LOW_MOTIVATION,
}

// RaceInfo is the information about a race needed to analyse a plan.
type RaceInfo struct {
	DistanceMeters float64
	// The local start time as HH:MM, empty if unknown.
	StartTime     string
	CutoffSeconds int32
	// The race course with elevations, nil if unknown.
	Course []geo.Point
}

// Analyze computes the timing, fueling and rule results of a race plan.
func Analyze(plan *racesv1.RacePlan, race RaceInfo) *racesv1.RacePlanAnalysis {
	a := &analyzer{plan: plan, race: race, products: map[string]*racesv1.NutritionProduct{}}
	for _, p := range plan.GetNutrition().GetProducts() {
		a.products[normalize(p.GetName())] = p
	}
	a.startMinutes, a.hasStart = parseClock(race.StartTime)
	a.analyzeCheckpoints()
	rules := a.rules()
	passed := 0
	for _, r := range rules {
		if r.GetStatus() != racesv1.RuleStatus_RULE_STATUS_FAIL {
			passed++
		}
	}
	return racesv1.RacePlanAnalysis_builder{
		Checkpoints:            a.checkpoints,
		PlannedFinishSeconds:   int32(a.finish.Seconds()),
		PlannedFinishClockTime: a.clockTime(a.finish),
		TotalCarbsGrams:        a.totalCarbs,
		TotalFluidMl:           a.totalFluid,
		TotalSodiumMg:          a.totalSodium,
		TotalCaffeineMg:        a.totalCaffeine,
		Rules:                  rules,
		RulesPassed:            int32(passed),
		RulesTotal:             int32(len(rules)),
	}.Build()
}

type analyzer struct {
	plan         *racesv1.RacePlan
	race         RaceInfo
	products     map[string]*racesv1.NutritionProduct
	startMinutes int
	hasStart     bool

	checkpoints                                        []*racesv1.CheckpointAnalysis
	finish                                             time.Duration
	totalCarbs, totalFluid, totalSodium, totalCaffeine float64
	unknownProducts                                    []string
}

func (a *analyzer) analyzeCheckpoints() {
	courseDists := geo.CumulativeDistances(a.race.Course)
	courseScale := 1.0
	if n := len(courseDists); n > 0 && a.race.DistanceMeters > 0 {
		courseScale = courseDists[n-1] / a.race.DistanceMeters
	}
	nutrition := a.plan.GetNutrition()
	var elapsed time.Duration
	var prevDistance float64
	for _, cp := range a.plan.GetCheckpoints() {
		segment := time.Duration(cp.GetPlannedSegmentSeconds()) * time.Second
		arrival := elapsed + segment
		departure := arrival + time.Duration(cp.GetPlannedStopSeconds())*time.Second
		segmentDistance := cp.GetDistanceMeters() - prevDistance
		gain, loss := cp.GetElevationGainMeters(), cp.GetElevationLossMeters()
		if gain == 0 && loss == 0 && len(a.race.Course) > 1 {
			gain, loss = segmentElevation(a.race.Course, courseDists, prevDistance*courseScale, cp.GetDistanceMeters()*courseScale)
		}
		hours := segment.Hours()
		var carbs, fluid, sodium float64
		for _, intake := range cp.GetIntake() {
			product, ok := a.products[normalize(intake.GetProduct())]
			if !ok {
				a.unknownProducts = append(a.unknownProducts, intake.GetProduct())
				continue
			}
			carbs += product.GetCarbsGrams() * intake.GetQuantity()
			fluid += product.GetFluidMl() * intake.GetQuantity()
			sodium += product.GetSodiumMg() * intake.GetQuantity()
			a.totalCaffeine += product.GetCaffeineMg() * intake.GetQuantity()
		}
		a.totalCarbs += carbs
		a.totalFluid += fluid
		a.totalSodium += sodium
		b := racesv1.CheckpointAnalysis_builder{
			Name:                       cp.GetName(),
			SegmentDistanceMeters:      segmentDistance,
			SegmentElevationGainMeters: math.Round(gain),
			SegmentElevationLossMeters: math.Round(loss),
			ArrivalSeconds:             int32(arrival.Seconds()),
			ArrivalClockTime:           a.clockTime(arrival),
			DepartureSeconds:           int32(departure.Seconds()),
			HasCutoff:                  cp.GetCutoffSeconds() > 0,
			CarbsGrams:                 carbs,
			CarbsTargetGrams:           math.Round(nutrition.GetCarbsGramsPerHour() * hours),
			FluidMl:                    fluid,
			FluidTargetMl:              math.Round(nutrition.GetFluidMlPerHour() * hours),
			SodiumMg:                   sodium,
			SodiumTargetMg:             math.Round(nutrition.GetSodiumMgPerHour() * hours),
		}
		if cp.GetCutoffSeconds() > 0 {
			b.CutoffBufferSeconds = cp.GetCutoffSeconds() - int32(arrival.Seconds())
		}
		if segmentDistance > 0 && segment > 0 {
			b.SegmentPaceSecondsPerKm = math.Round(segment.Seconds() / (segmentDistance / metersPerKilometer))
		}
		a.checkpoints = append(a.checkpoints, b.Build())
		elapsed = departure
		prevDistance = cp.GetDistanceMeters()
		a.finish = arrival
	}
}

// segmentElevation returns the elevation gain and loss of the course between
// two distances.
func segmentElevation(course []geo.Point, dists []float64, from, to float64) (gain, loss float64) {
	elevations := []float64{elevationAt(course, dists, from)}
	for i, p := range course {
		if dists[i] > from && dists[i] < to {
			elevations = append(elevations, p.Ele)
		}
	}
	elevations = append(elevations, elevationAt(course, dists, to))
	return geo.ElevationGainLoss(elevations)
}

// elevationAt interpolates the elevation of the course at a distance.
func elevationAt(course []geo.Point, dists []float64, d float64) float64 {
	i := sort.SearchFloat64s(dists, d)
	switch {
	case i == 0:
		return course[0].Ele
	case i >= len(course):
		return course[len(course)-1].Ele
	}
	f := (d - dists[i-1]) / (dists[i] - dists[i-1])
	return course[i-1].Ele + (course[i].Ele-course[i-1].Ele)*f
}

// clockTime formats an elapsed time as the local time of day, prefixed with
// the day if after the first day. It returns an empty string if the start
// time is unknown.
func (a *analyzer) clockTime(elapsed time.Duration) string {
	if !a.hasStart {
		return ""
	}
	minutes := a.startMinutes + int(elapsed.Minutes())
	day := minutes / (24 * 60)
	clock := fmt.Sprintf("%02d:%02d", (minutes/60)%24, minutes%60)
	if day > 0 {
		return fmt.Sprintf("Day %d %s", day+1, clock)
	}
	return clock
}

// parseClock parses an HH:MM time into minutes after midnight.
func parseClock(s string) (int, bool) {
	t, err := time.Parse("15:04", strings.TrimSpace(s))
	if err != nil {
		return 0, false
	}
	return t.Hour()*60 + t.Minute(), true
}

func normalize(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// rule is a check of a race plan.
type rule struct {
	id, section, title, description string
	check                           func(a *analyzer) (racesv1.RuleStatus, string)
}

const (
	pass = racesv1.RuleStatus_RULE_STATUS_PASS
	warn = racesv1.RuleStatus_RULE_STATUS_WARN
	fail = racesv1.RuleStatus_RULE_STATUS_FAIL
)

func (a *analyzer) rules() []*racesv1.RuleResult {
	results := make([]*racesv1.RuleResult, len(planRules))
	for i, r := range planRules {
		status, message := r.check(a)
		results[i] = racesv1.RuleResult_builder{
			Id:          r.id,
			Section:     r.section,
			Title:       r.title,
			Description: r.description,
			Status:      status,
			Message:     message,
		}.Build()
	}
	return results
}

// listViolations formats a list of problems, truncating long lists.
func listViolations(items []string) string {
	if len(items) <= maxListedRuleViolations {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:maxListedRuleViolations], ", ") + fmt.Sprintf(" and %d more", len(items)-maxListedRuleViolations)
}

func checkpointName(cp *racesv1.Checkpoint, i int) string {
	if cp.GetName() != "" {
		return cp.GetName()
	}
	return fmt.Sprintf("%s %d", defaultCheckpointPrefix, i+1)
}

func formatDuration(d time.Duration) string {
	sign := ""
	if d < 0 {
		sign, d = "-", -d
	}
	return fmt.Sprintf("%s%d:%02d", sign, int(d.Hours()), int(d.Minutes())%60)
}

var planRules = []rule{
	{
		id:          "goals",
		section:     "goals",
		title:       "Set A, B and C goals",
		description: "An A goal for when everything goes right, a B goal for a solid day and a C goal for a tough day. Having fallback goals makes it easier to adapt when the race doesn't go to plan.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			g := a.plan.GetGoals()
			switch {
			case g.GetAFinishSeconds() <= 0:
				return fail, "Set an A goal finish time."
			case g.GetBFinishSeconds() <= 0 || g.GetCFinishSeconds() <= 0:
				return warn, "Set B and C goals for when the day doesn't go to plan."
			case g.GetAFinishSeconds() > g.GetBFinishSeconds() || g.GetBFinishSeconds() > g.GetCFinishSeconds():
				return warn, "Goals should get slower from A to C."
			case a.race.CutoffSeconds > 0 && g.GetCFinishSeconds() > a.race.CutoffSeconds:
				return warn, "The C goal is slower than the race time limit."
			}
			return pass, ""
		},
	},
	{
		id:          "checkpoints",
		section:     "checkpoints",
		title:       "List every checkpoint to the finish",
		description: "Every checkpoint and aid station in course order, ending with the finish at the race distance.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			cps := a.plan.GetCheckpoints()
			if len(cps) == 0 {
				return fail, "Add the checkpoints and the finish."
			}
			var prev float64
			for i, cp := range cps {
				if cp.GetDistanceMeters() <= prev {
					return fail, fmt.Sprintf("%s must be further along the course than the previous checkpoint.", checkpointName(cp, i))
				}
				prev = cp.GetDistanceMeters()
			}
			if d := a.race.DistanceMeters; d > 0 {
				diff := math.Abs(prev - d)
				if diff > math.Max(finishDistanceMinMeters, d*finishDistanceTolerance) {
					return warn, fmt.Sprintf("The last checkpoint is at %.1f km but the race is %.1f km; make sure the finish is included.", prev/metersPerKilometer, d/metersPerKilometer)
				}
			}
			return pass, ""
		},
	},
	{
		id:          "segment-times",
		section:     "checkpoints",
		title:       "Plan a time for every segment",
		description: "A planned time to each checkpoint, so arrival times, cutoff buffers and fueling can be computed.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			cps := a.plan.GetCheckpoints()
			if len(cps) == 0 {
				return fail, "Add checkpoints first."
			}
			var missing []string
			for i, cp := range cps {
				if cp.GetPlannedSegmentSeconds() <= 0 {
					missing = append(missing, checkpointName(cp, i))
				}
			}
			if len(missing) > 0 {
				return fail, "Missing segment times: " + listViolations(missing) + "."
			}
			return pass, ""
		},
	},
	{
		id:          "cutoff-buffers",
		section:     "checkpoints",
		title:       "Stay ahead of the cutoffs",
		description: "The plan should reach every checkpoint at least 30 minutes before its cutoff, to leave room for a bad patch.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			var missed, tight []string
			for i, cp := range a.checkpoints {
				if !cp.GetHasCutoff() {
					continue
				}
				buffer := time.Duration(cp.GetCutoffBufferSeconds()) * time.Second
				name := checkpointName(a.plan.GetCheckpoints()[i], i)
				switch {
				case buffer < 0:
					missed = append(missed, fmt.Sprintf("%s (%s)", name, formatDuration(buffer)))
				case buffer < minCutoffBuffer:
					tight = append(tight, fmt.Sprintf("%s (%s)", name, formatDuration(buffer)))
				}
			}
			if a.race.CutoffSeconds > 0 && a.finish > time.Duration(a.race.CutoffSeconds)*time.Second {
				missed = append(missed, "the race time limit")
			}
			switch {
			case len(missed) > 0:
				return fail, "The plan misses cutoffs at " + listViolations(missed) + "."
			case len(tight) > 0:
				return warn, "Less than 30 minutes buffer at " + listViolations(tight) + "."
			}
			return pass, ""
		},
	},
	{
		id:          "goal-consistency",
		section:     "pacing",
		title:       "Match the plan to the goals",
		description: "The planned finish time from the segment times should be close to the A or B goal, otherwise either the goals or the pacing need adjusting.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			g := a.plan.GetGoals()
			if g.GetAFinishSeconds() <= 0 || a.finish <= 0 {
				return fail, "Set an A goal and segment times first."
			}
			aGoal := float64(g.GetAFinishSeconds())
			bGoal := math.Max(aGoal, float64(g.GetBFinishSeconds()))
			finish := a.finish.Seconds()
			switch {
			case finish < aGoal*(1-goalTolerance):
				return warn, fmt.Sprintf("The planned finish (%s) is faster than the A goal; the pacing may be too aggressive.", formatDuration(a.finish))
			case finish > bGoal*(1+goalTolerance):
				return warn, fmt.Sprintf("The planned finish (%s) is slower than the B goal.", formatDuration(a.finish))
			}
			return pass, ""
		},
	},
	{
		id:          "pacing-strategy",
		section:     "pacing",
		title:       "Decide how to pace",
		description: "An overall pacing strategy and the grade above which to hike, decided before the start rather than in the moment.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			p := a.plan.GetPacing()
			switch {
			case strings.TrimSpace(p.GetStrategy()) == "":
				return fail, "Describe the pacing strategy."
			case p.GetHikeGradePercent() <= 0:
				return warn, "Set the grade above which to hike."
			}
			return pass, ""
		},
	},
	{
		id:          "fueling-targets",
		section:     "nutrition",
		title:       "Set hourly fueling targets",
		description: "Targets for carbohydrate (60-120 g/h), fluid (300-1000 ml/h) and sodium (300-1500 mg/h), practiced in training.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			n := a.plan.GetNutrition()
			carbs, fluid, sodium := n.GetCarbsGramsPerHour(), n.GetFluidMlPerHour(), n.GetSodiumMgPerHour()
			if carbs <= 0 || fluid <= 0 || sodium <= 0 {
				return fail, "Set carbohydrate, fluid and sodium targets per hour."
			}
			var unusual []string
			if carbs < minCarbsPerHour || carbs > maxCarbsPerHour {
				unusual = append(unusual, fmt.Sprintf("%.0f g/h carbohydrate", carbs))
			}
			if fluid < minFluidPerHour || fluid > maxFluidPerHour {
				unusual = append(unusual, fmt.Sprintf("%.0f ml/h fluid", fluid))
			}
			if sodium < minSodiumPerHour || sodium > maxSodiumPerHour {
				unusual = append(unusual, fmt.Sprintf("%.0f mg/h sodium", sodium))
			}
			if len(unusual) > 0 {
				return warn, "Unusual targets: " + strings.Join(unusual, ", ") + "."
			}
			return pass, ""
		},
	},
	{
		id:          "segment-fueling",
		section:     "nutrition",
		title:       "Plan fuel for every segment",
		description: "Every segment longer than 30 minutes should have planned intake meeting at least 80% of the carbohydrate, fluid and sodium targets, so fueling and hydration are decided before the race.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			n := a.plan.GetNutrition()
			if n.GetCarbsGramsPerHour() <= 0 && n.GetFluidMlPerHour() <= 0 && n.GetSodiumMgPerHour() <= 0 {
				return fail, "Set nutrition targets first."
			}
			var under, over []string
			for i, cp := range a.checkpoints {
				if time.Duration(a.plan.GetCheckpoints()[i].GetPlannedSegmentSeconds())*time.Second < minFueledSegment {
					continue
				}
				nutrients := []struct {
					name, unit      string
					planned, target float64
				}{
					{"carbohydrate", "g", cp.GetCarbsGrams(), cp.GetCarbsTargetGrams()},
					{"fluid", "ml", cp.GetFluidMl(), cp.GetFluidTargetMl()},
					{"sodium", "mg", cp.GetSodiumMg(), cp.GetSodiumTargetMg()},
				}
				var low, high []string
				for _, nutrient := range nutrients {
					if nutrient.target <= 0 {
						continue
					}
					amount := fmt.Sprintf("%.0f of %.0f %s %s", nutrient.planned, nutrient.target, nutrient.unit, nutrient.name)
					switch ratio := nutrient.planned / nutrient.target; {
					case ratio < minSegmentFuelFraction:
						low = append(low, amount)
					case ratio > maxSegmentFuelFraction:
						high = append(high, amount)
					}
				}
				name := checkpointName(a.plan.GetCheckpoints()[i], i)
				if len(low) > 0 {
					under = append(under, fmt.Sprintf("%s (%s)", name, strings.Join(low, ", ")))
				}
				if len(high) > 0 {
					over = append(over, fmt.Sprintf("%s (%s)", name, strings.Join(high, ", ")))
				}
			}
			switch {
			case len(under) > 0:
				return fail, "Not enough planned to " + listViolations(under) + "."
			case len(over) > 0:
				return warn, "More than the target planned to " + listViolations(over) + "; check the stomach can handle it."
			}
			return pass, ""
		},
	},
	{
		id:          "known-products",
		section:     "nutrition",
		title:       "Define every product",
		description: "Every product in the checkpoint intake must be listed in the nutrition products, so its contents are known.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			if len(a.unknownProducts) > 0 {
				return fail, "Unknown products: " + listViolations(dedupe(a.unknownProducts)) + "."
			}
			return pass, ""
		},
	},
	{
		id:          "mandatory-gear",
		section:     "gear",
		title:       "List the mandatory kit",
		description: "The race's mandatory kit list, so nothing is forgotten at kit check. Mark items as packed when packing.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			var mandatory, packed int
			for _, item := range a.plan.GetGear() {
				if item.GetMandatory() {
					mandatory++
					if item.GetPacked() {
						packed++
					}
				}
			}
			if mandatory == 0 {
				return warn, "No mandatory gear listed. Check the race rules for the mandatory kit."
			}
			return pass, fmt.Sprintf("%d of %d mandatory items packed.", packed, mandatory)
		},
	},
	{
		id:          "night-gear",
		section:     "gear",
		title:       "Carry a headlamp for the dark",
		description: "If any part of the race is planned before 06:00 or after 20:00, the gear should include a headlamp.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			if !a.hasStart || a.finish <= 0 {
				return pass, "Set the race start time and segment times to check."
			}
			endMinutes := a.startMinutes + int(a.finish.Minutes())
			dark := a.startMinutes < nightStartHour*60 || endMinutes > nightEndHour*60
			if !dark {
				return pass, "The plan finishes in daylight."
			}
			for _, item := range a.plan.GetGear() {
				name := normalize(item.GetName())
				if strings.Contains(name, "headlamp") || strings.Contains(name, "head torch") || strings.Contains(name, "headtorch") {
					return pass, ""
				}
			}
			return fail, "Part of the race is in the dark; add a " + headlampKeywordsSentence + " to the gear."
		},
	},
	{
		id:          "drop-bags",
		section:     "gear",
		title:       "Pack every drop bag",
		description: "Every checkpoint with a drop bag should list what's in it.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			var missing []string
			for i, cp := range a.plan.GetCheckpoints() {
				if cp.GetDropBag() && strings.TrimSpace(cp.GetDropBagContents()) == "" {
					missing = append(missing, checkpointName(cp, i))
				}
			}
			if len(missing) > 0 {
				return fail, "Missing drop bag contents at " + listViolations(missing) + "."
			}
			return pass, ""
		},
	},
	{
		id:          "crew",
		section:     "crew",
		title:       "Brief the crew",
		description: "If there is crew, who they are and what to do at every checkpoint they can access.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			crew := a.plan.GetCrew()
			if !crew.GetHasCrew() {
				return pass, "No crew."
			}
			if strings.TrimSpace(crew.GetMembers()) == "" {
				return fail, "List the crew members and how to reach them."
			}
			var missing []string
			for i, cp := range a.plan.GetCheckpoints() {
				if cp.GetCrewAccess() && strings.TrimSpace(cp.GetCrewInstructions()) == "" {
					missing = append(missing, checkpointName(cp, i))
				}
			}
			if len(missing) > 0 {
				return fail, "Missing crew instructions at " + listViolations(missing) + "."
			}
			return pass, ""
		},
	},
	{
		id:          "contingencies",
		section:     "contingencies",
		title:       "Plan for things going wrong",
		description: "A response for each of the common problems in trail races: stomach issues, falling behind, low energy, injury, weather, gear failure and low motivation.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			covered := map[racesv1.ContingencyScenario]bool{}
			for _, c := range a.plan.GetContingencies() {
				if strings.TrimSpace(c.GetResponse()) != "" {
					covered[c.GetScenario()] = true
				}
			}
			var missing []string
			for _, scenario := range coreContingencies {
				if !covered[scenario] {
					missing = append(missing, scenarioName(scenario))
				}
			}
			if len(missing) > 0 {
				return fail, "Missing responses for " + strings.Join(missing, ", ") + "."
			}
			return pass, ""
		},
	},
	{
		id:          "race-morning",
		section:     "race_morning",
		title:       "Plan the race morning",
		description: "A timeline from waking up to the start, including breakfast and getting to the start line.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			items := a.plan.GetRaceMorning()
			if len(items) < minRaceMorningItems {
				return fail, fmt.Sprintf("Add at least %d items, e.g. wake up, breakfast and getting to the start.", minRaceMorningItems)
			}
			for _, item := range items {
				minutes, ok := parseClock(item.GetTime())
				if !ok {
					return warn, "Every item should have a time (HH:MM)."
				}
				if a.hasStart && minutes > a.startMinutes {
					return warn, fmt.Sprintf("%q is after the race start.", item.GetDescription())
				}
			}
			return pass, ""
		},
	},
	{
		id:          "mental",
		section:     "mental",
		title:       "Prepare mentally",
		description: "At least one mantra to fall back on during the hardest moments.",
		check: func(a *analyzer) (racesv1.RuleStatus, string) {
			for _, m := range a.plan.GetMental().GetMantras() {
				if strings.TrimSpace(m) != "" {
					return pass, ""
				}
			}
			return fail, "Add a mantra."
		},
	},
}

func scenarioName(s racesv1.ContingencyScenario) string {
	name := strings.TrimPrefix(s.String(), "CONTINGENCY_SCENARIO_")
	return strings.ToLower(strings.ReplaceAll(name, "_", " "))
}

func dedupe(items []string) []string {
	seen := map[string]bool{}
	var result []string
	for _, item := range items {
		if !seen[item] {
			seen[item] = true
			result = append(result, item)
		}
	}
	return result
}
