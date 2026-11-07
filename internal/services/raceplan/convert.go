package raceplan

import (
	"time"

	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var scenarioToProto = map[races.ContingencyScenario]racesv1.ContingencyScenario{
	races.ContingencyScenarioGIDistress:     racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_GI_DISTRESS,
	races.ContingencyScenarioBehindSchedule: racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_BEHIND_SCHEDULE,
	races.ContingencyScenarioLowEnergy:      racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_LOW_ENERGY,
	races.ContingencyScenarioInjury:         racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_INJURY,
	races.ContingencyScenarioWeather:        racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_WEATHER,
	races.ContingencyScenarioGearFailure:    racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_GEAR_FAILURE,
	races.ContingencyScenarioLowMotivation:  racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_LOW_MOTIVATION,
	races.ContingencyScenarioNavigation:     racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_NAVIGATION,
	races.ContingencyScenarioOther:          racesv1.ContingencyScenario_CONTINGENCY_SCENARIO_OTHER,
}

var scenarioFromProto = func() map[racesv1.ContingencyScenario]races.ContingencyScenario {
	m := make(map[racesv1.ContingencyScenario]races.ContingencyScenario, len(scenarioToProto))
	for k, v := range scenarioToProto {
		m[v] = k
	}
	return m
}()

// planToProto converts a stored plan.
func planToProto(p *races.RacePlan, shareURL string) *racesv1.RacePlan {
	checkpoints := make([]*racesv1.Checkpoint, len(p.Checkpoints))
	for i, cp := range p.Checkpoints {
		intake := make([]*racesv1.Intake, len(cp.Intake))
		for j, in := range cp.Intake {
			intake[j] = racesv1.Intake_builder{Product: in.Product, Quantity: in.Quantity}.Build()
		}
		checkpoints[i] = racesv1.Checkpoint_builder{
			Id:                    store.UUIDToString(cp.ID),
			Name:                  cp.Name,
			DistanceMeters:        cp.DistanceMeters,
			ElevationGainMeters:   cp.ElevationGainMeters,
			ElevationLossMeters:   cp.ElevationLossMeters,
			CutoffSeconds:         cp.CutoffSeconds,
			CrewAccess:            cp.CrewAccess,
			DropBag:               cp.DropBag,
			Aid:                   cp.Aid,
			PlannedSegmentSeconds: cp.PlannedSegmentSeconds,
			PlannedStopSeconds:    cp.PlannedStopSeconds,
			SegmentStrategy:       cp.SegmentStrategy,
			Intake:                intake,
			Tasks:                 cp.Tasks,
			CrewInstructions:      cp.CrewInstructions,
			DropBagContents:       cp.DropBagContents,
		}.Build()
	}
	products := make([]*racesv1.NutritionProduct, len(p.Nutrition.Products))
	for i, np := range p.Nutrition.Products {
		products[i] = racesv1.NutritionProduct_builder{
			Name:       np.Name,
			CarbsGrams: np.CarbsGrams,
			SodiumMg:   np.SodiumMg,
			FluidMl:    np.FluidMl,
			CaffeineMg: np.CaffeineMg,
		}.Build()
	}
	gear := make([]*racesv1.GearItem, len(p.Gear))
	for i, g := range p.Gear {
		gear[i] = racesv1.GearItem_builder{
			Name:      g.Name,
			Mandatory: g.Mandatory,
			Location:  g.Location,
			Packed:    g.Packed,
			Notes:     g.Notes,
		}.Build()
	}
	contingencies := make([]*racesv1.Contingency, len(p.Contingencies))
	for i, c := range p.Contingencies {
		contingencies[i] = racesv1.Contingency_builder{
			Scenario: scenarioToProto[c.Scenario],
			Trigger:  c.Trigger,
			Response: c.Response,
		}.Build()
	}
	raceMorning := make([]*racesv1.TimelineItem, len(p.RaceMorning))
	for i, t := range p.RaceMorning {
		raceMorning[i] = racesv1.TimelineItem_builder{Time: t.Time, Description: t.Description}.Build()
	}
	return racesv1.RacePlan_builder{
		Id:         store.UUIDToString(p.ID),
		RaceId:     store.UUIDToString(p.RaceID),
		ShareUrl:   shareURL,
		CreateTime: timestamppb.New(p.CreateTime),
		UpdateTime: timestamppb.New(p.UpdateTime),
		Goals: racesv1.RaceGoals_builder{
			AFinishSeconds: p.Goals.AFinishSeconds,
			BFinishSeconds: p.Goals.BFinishSeconds,
			CFinishSeconds: p.Goals.CFinishSeconds,
			Notes:          p.Goals.Notes,
		}.Build(),
		Checkpoints: checkpoints,
		Pacing: racesv1.PacingStrategy_builder{
			Strategy:         p.Pacing.Strategy,
			HikeGradePercent: p.Pacing.HikeGradePercent,
			HeartRateCapBpm:  p.Pacing.HeartRateCapBpm,
			Notes:            p.Pacing.Notes,
		}.Build(),
		Nutrition: racesv1.NutritionStrategy_builder{
			CarbsGramsPerHour: p.Nutrition.CarbsGramsPerHour,
			FluidMlPerHour:    p.Nutrition.FluidMlPerHour,
			SodiumMgPerHour:   p.Nutrition.SodiumMgPerHour,
			Products:          products,
			Notes:             p.Nutrition.Notes,
		}.Build(),
		Gear: gear,
		Crew: racesv1.CrewPlan_builder{
			HasCrew: p.Crew.HasCrew,
			Members: p.Crew.Members,
			Notes:   p.Crew.Notes,
		}.Build(),
		Contingencies: contingencies,
		RaceMorning:   raceMorning,
		Mental: racesv1.MentalStrategy_builder{
			Mantras: p.Mental.Mantras,
			Notes:   p.Mental.Notes,
		}.Build(),
		Notes:   p.Notes,
		RaceDay: raceDayToProto(&p.RaceDay),
	}.Build()
}

func raceDayToProto(rd *races.RaceDay) *racesv1.RaceDay {
	splits := make([]*racesv1.CheckpointSplit, len(rd.Splits))
	for i, split := range rd.Splits {
		splits[i] = racesv1.CheckpointSplit_builder{
			CheckpointIndex: split.CheckpointIndex,
			ArrivalTime:     timeToProto(split.ArrivalTime),
			DepartureTime:   timeToProto(split.DepartureTime),
		}.Build()
	}
	return racesv1.RaceDay_builder{
		LivetrackUrl: rd.LivetrackURL,
		StartTime:    timeToProto(rd.StartTime),
		Splits:       splits,
	}.Build()
}

func raceDayFromProto(rd *racesv1.RaceDay) *races.RaceDay {
	var splits []*races.CheckpointSplit
	for _, split := range rd.GetSplits() {
		splits = append(splits, splitFromProto(split))
	}
	return &races.RaceDay{
		LivetrackURL: rd.GetLivetrackUrl(),
		StartTime:    timeFromProto(rd.GetStartTime()),
		Splits:       splits,
	}
}

func splitFromProto(split *racesv1.CheckpointSplit) *races.CheckpointSplit {
	return &races.CheckpointSplit{
		CheckpointIndex: split.GetCheckpointIndex(),
		ArrivalTime:     timeFromProto(split.GetArrivalTime()),
		DepartureTime:   timeFromProto(split.GetDepartureTime()),
	}
}

// timeToProto converts a time to a timestamp. The zero time is unset.
func timeToProto(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// timeFromProto converts a timestamp to a time. Unset is the zero time.
func timeFromProto(ts *timestamppb.Timestamp) time.Time {
	if ts == nil {
		return time.Time{}
	}
	return ts.AsTime()
}

// planFromProto converts the sections of a plan. The plan must have been
// validated with validatePlan.
func planFromProto(p *racesv1.RacePlan) *races.RacePlan {
	var checkpoints []*races.Checkpoint
	for _, cp := range p.GetCheckpoints() {
		var intake []*races.Intake
		for _, in := range cp.GetIntake() {
			intake = append(intake, &races.Intake{Product: in.GetProduct(), Quantity: in.GetQuantity()})
		}
		// validatePlan checked the ID, and an empty one is a new checkpoint.
		id, _ := store.StringToUUID(cp.GetId())
		checkpoints = append(checkpoints, &races.Checkpoint{
			ID:                    id,
			Name:                  cp.GetName(),
			DistanceMeters:        cp.GetDistanceMeters(),
			ElevationGainMeters:   cp.GetElevationGainMeters(),
			ElevationLossMeters:   cp.GetElevationLossMeters(),
			CutoffSeconds:         cp.GetCutoffSeconds(),
			CrewAccess:            cp.GetCrewAccess(),
			DropBag:               cp.GetDropBag(),
			Aid:                   cp.GetAid(),
			PlannedSegmentSeconds: cp.GetPlannedSegmentSeconds(),
			PlannedStopSeconds:    cp.GetPlannedStopSeconds(),
			SegmentStrategy:       cp.GetSegmentStrategy(),
			Intake:                intake,
			Tasks:                 cp.GetTasks(),
			CrewInstructions:      cp.GetCrewInstructions(),
			DropBagContents:       cp.GetDropBagContents(),
		})
	}
	var products []*races.NutritionProduct
	for _, np := range p.GetNutrition().GetProducts() {
		products = append(products, &races.NutritionProduct{
			Name:       np.GetName(),
			CarbsGrams: np.GetCarbsGrams(),
			SodiumMg:   np.GetSodiumMg(),
			FluidMl:    np.GetFluidMl(),
			CaffeineMg: np.GetCaffeineMg(),
		})
	}
	var gear []*races.GearItem
	for _, g := range p.GetGear() {
		gear = append(gear, &races.GearItem{
			Name:      g.GetName(),
			Mandatory: g.GetMandatory(),
			Location:  g.GetLocation(),
			Packed:    g.GetPacked(),
			Notes:     g.GetNotes(),
		})
	}
	var contingencies []*races.Contingency
	for _, c := range p.GetContingencies() {
		contingencies = append(contingencies, &races.Contingency{
			Scenario: scenarioFromProto[c.GetScenario()],
			Trigger:  c.GetTrigger(),
			Response: c.GetResponse(),
		})
	}
	var raceMorning []*races.TimelineItem
	for _, t := range p.GetRaceMorning() {
		raceMorning = append(raceMorning, &races.TimelineItem{Time: t.GetTime(), Description: t.GetDescription()})
	}
	return &races.RacePlan{
		Goals: races.RaceGoals{
			AFinishSeconds: p.GetGoals().GetAFinishSeconds(),
			BFinishSeconds: p.GetGoals().GetBFinishSeconds(),
			CFinishSeconds: p.GetGoals().GetCFinishSeconds(),
			Notes:          p.GetGoals().GetNotes(),
		},
		Checkpoints: checkpoints,
		Pacing: races.PacingStrategy{
			Strategy:         p.GetPacing().GetStrategy(),
			HikeGradePercent: p.GetPacing().GetHikeGradePercent(),
			HeartRateCapBpm:  p.GetPacing().GetHeartRateCapBpm(),
			Notes:            p.GetPacing().GetNotes(),
		},
		Nutrition: races.NutritionStrategy{
			CarbsGramsPerHour: p.GetNutrition().GetCarbsGramsPerHour(),
			FluidMlPerHour:    p.GetNutrition().GetFluidMlPerHour(),
			SodiumMgPerHour:   p.GetNutrition().GetSodiumMgPerHour(),
			Products:          products,
			Notes:             p.GetNutrition().GetNotes(),
		},
		Gear: gear,
		Crew: races.CrewPlan{
			HasCrew: p.GetCrew().GetHasCrew(),
			Members: p.GetCrew().GetMembers(),
			Notes:   p.GetCrew().GetNotes(),
		},
		Contingencies: contingencies,
		RaceMorning:   raceMorning,
		Mental: races.MentalStrategy{
			Mantras: p.GetMental().GetMantras(),
			Notes:   p.GetMental().GetNotes(),
		},
		Notes: p.GetNotes(),
	}
}
