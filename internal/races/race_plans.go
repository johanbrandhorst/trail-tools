package races

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/races/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// RacePlan is a race plan: a fixed set of sections that every trail race
// plan should cover.
type RacePlan struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
	RaceID pgtype.UUID
	// Empty if the plan is not shared.
	ShareToken    string
	Goals         RaceGoals
	Checkpoints   []*Checkpoint
	Pacing        PacingStrategy
	Nutrition     NutritionStrategy
	Gear          []*GearItem
	Crew          CrewPlan
	Contingencies []*Contingency
	RaceMorning   []*TimelineItem
	Mental        MentalStrategy
	Notes         string
	// What happened on race day. Not a section: updated with UpdateRaceDay
	// and SetCheckpointSplit.
	RaceDay    RaceDay
	CreateTime time.Time
	UpdateTime time.Time
}

// RaceGoals are finish time goals, from most to least ambitious.
type RaceGoals struct {
	AFinishSeconds int32
	BFinishSeconds int32
	CFinishSeconds int32
	Notes          string
}

// Checkpoint is a checkpoint, aid station or the finish, together with the
// segment leading to it.
type Checkpoint struct {
	Name                  string
	DistanceMeters        float64
	ElevationGainMeters   float64
	ElevationLossMeters   float64
	CutoffSeconds         int32
	CrewAccess            bool
	DropBag               bool
	Aid                   string
	PlannedSegmentSeconds int32
	PlannedStopSeconds    int32
	SegmentStrategy       string
	// The fuel to consume on the segment leading to the checkpoint.
	Intake           []*Intake
	Tasks            string
	CrewInstructions string
	DropBagContents  string
}

// Intake is an amount of a nutrition product.
type Intake struct {
	Product  string
	Quantity float64
}

// PacingStrategy is the overall pacing strategy.
type PacingStrategy struct {
	Strategy         string
	HikeGradePercent float64
	HeartRateCapBpm  int32
	Notes            string
}

// NutritionStrategy is the fueling and hydration targets and the products
// carried.
type NutritionStrategy struct {
	CarbsGramsPerHour float64
	FluidMlPerHour    float64
	SodiumMgPerHour   float64
	Products          []*NutritionProduct
	Notes             string
}

// NutritionProduct is a nutrition product and what one serving contains.
type NutritionProduct struct {
	Name       string
	CarbsGrams float64
	SodiumMg   float64
	FluidMl    float64
	CaffeineMg float64
}

// GearItem is an item of gear.
type GearItem struct {
	Name      string
	Mandatory bool
	Location  string
	Packed    bool
	Notes     string
}

// CrewPlan is the crew logistics.
type CrewPlan struct {
	HasCrew bool
	Members string
	Notes   string
}

// ContingencyScenario is a kind of problem that can occur during a race.
type ContingencyScenario string

// Contingency scenarios.
const (
	ContingencyScenarioGIDistress     ContingencyScenario = "gi_distress"
	ContingencyScenarioBehindSchedule ContingencyScenario = "behind_schedule"
	ContingencyScenarioLowEnergy      ContingencyScenario = "low_energy"
	ContingencyScenarioInjury         ContingencyScenario = "injury"
	ContingencyScenarioWeather        ContingencyScenario = "weather"
	ContingencyScenarioGearFailure    ContingencyScenario = "gear_failure"
	ContingencyScenarioLowMotivation  ContingencyScenario = "low_motivation"
	ContingencyScenarioNavigation     ContingencyScenario = "navigation"
	ContingencyScenarioOther          ContingencyScenario = "other"
)

// Contingency is a plan for a problem.
type Contingency struct {
	Scenario ContingencyScenario
	Trigger  string
	Response string
}

// TimelineItem is an item in the race morning timeline.
type TimelineItem struct {
	// The local time, e.g. "04:30".
	Time        string
	Description string
}

// MentalStrategy is the mental strategy.
type MentalStrategy struct {
	Mantras []string
	Notes   string
}

// RaceDay is what happened on race day, as recorded by the runner or crew.
type RaceDay struct {
	// Empty if no LiveTrack session is linked.
	LivetrackURL string
	// Zero if the start time hasn't been recorded.
	StartTime time.Time
	// The recorded checkpoint times in checkpoint order.
	Splits []*CheckpointSplit
}

// CheckpointSplit is the recorded times at a checkpoint.
type CheckpointSplit struct {
	// The index of the checkpoint in the plan.
	CheckpointIndex int32
	// Zero if not recorded. At least one of the times must be set.
	ArrivalTime   time.Time
	DepartureTime time.Time
}

// RaceDayFields selects the race day fields to update.
type RaceDayFields struct {
	LivetrackURL bool
	StartTime    bool
	Splits       bool
}

// ErrCheckpointNotFound is returned when a split refers to a checkpoint the
// plan doesn't have.
var ErrCheckpointNotFound = errors.New("checkpoint not found")

// RacePlanSection is a section of a race plan. A section is the unit of
// update: updating a section replaces it as a whole.
type RacePlanSection string

// Race plan sections.
const (
	SectionGoals         RacePlanSection = "goals"
	SectionCheckpoints   RacePlanSection = "checkpoints"
	SectionPacing        RacePlanSection = "pacing"
	SectionNutrition     RacePlanSection = "nutrition"
	SectionGear          RacePlanSection = "gear"
	SectionCrew          RacePlanSection = "crew"
	SectionContingencies RacePlanSection = "contingencies"
	SectionRaceMorning   RacePlanSection = "race_morning"
	SectionMental        RacePlanSection = "mental"
	SectionNotes         RacePlanSection = "notes"
)

// AllRacePlanSections are all the sections of a race plan.
var AllRacePlanSections = []RacePlanSection{
	SectionGoals,
	SectionCheckpoints,
	SectionPacing,
	SectionNutrition,
	SectionGear,
	SectionCrew,
	SectionContingencies,
	SectionRaceMorning,
	SectionMental,
	SectionNotes,
}

// CreateRacePlan stores a new race plan for the plan's user and race.
func (r *Repository) CreateRacePlan(ctx context.Context, plan *RacePlan) (*RacePlan, error) {
	var id pgtype.UUID
	var p planReader
	err := r.db.Transact(ctx, func(b *store.Batch) error {
		read(b, &id, "create race plan", func(q *gen.Queries) (pgtype.UUID, error) {
			return q.CreateRacePlan(ctx, &gen.CreateRacePlanParams{
				UserID:                     plan.UserID,
				RaceID:                     plan.RaceID,
				GoalsAFinishSeconds:        plan.Goals.AFinishSeconds,
				GoalsBFinishSeconds:        plan.Goals.BFinishSeconds,
				GoalsCFinishSeconds:        plan.Goals.CFinishSeconds,
				GoalsNotes:                 plan.Goals.Notes,
				PacingStrategy:             plan.Pacing.Strategy,
				PacingHikeGradePercent:     plan.Pacing.HikeGradePercent,
				PacingHeartRateCapBpm:      plan.Pacing.HeartRateCapBpm,
				PacingNotes:                plan.Pacing.Notes,
				NutritionCarbsGramsPerHour: plan.Nutrition.CarbsGramsPerHour,
				NutritionFluidMlPerHour:    plan.Nutrition.FluidMlPerHour,
				NutritionSodiumMgPerHour:   plan.Nutrition.SodiumMgPerHour,
				NutritionNotes:             plan.Nutrition.Notes,
				CrewHasCrew:                plan.Crew.HasCrew,
				CrewMembers:                plan.Crew.Members,
				CrewNotes:                  plan.Crew.Notes,
				MentalMantras:              nonNil(plan.Mental.Mantras),
				MentalNotes:                plan.Mental.Notes,
				Notes:                      plan.Notes,
			})
		})
		return nil
	}, func(b *store.Batch) error {
		// The columns of the sections were set above, and the new plan has
		// no child rows to replace.
		q := gen.New(b)
		if err := createCheckpoints(ctx, q, id, plan.Checkpoints); err != nil {
			return err
		}
		if err := createNutritionProducts(ctx, q, id, plan.Nutrition.Products); err != nil {
			return err
		}
		if err := createGear(ctx, q, id, plan.Gear); err != nil {
			return err
		}
		if err := createContingencies(ctx, q, id, plan.Contingencies); err != nil {
			return err
		}
		if err := createRaceMorning(ctx, q, id, plan.RaceMorning); err != nil {
			return err
		}
		p.queuePlan(ctx, b, id, func(q *gen.Queries) (*gen.RacePlan, error) {
			return q.GetRacePlan(ctx, &gen.GetRacePlanParams{ID: id, UserID: plan.UserID})
		})
		return nil
	})
	if err != nil {
		return nil, writeError("create race plan", err)
	}
	return p.plan()
}

// GetRacePlan gets one of the user's race plans.
func (r *Repository) GetRacePlan(ctx context.Context, userID, planID pgtype.UUID) (*RacePlan, error) {
	var p planReader
	err := r.db.ReadSnapshot(ctx, func(b *store.Batch) error {
		p.queuePlan(ctx, b, planID, func(q *gen.Queries) (*gen.RacePlan, error) {
			return q.GetRacePlan(ctx, &gen.GetRacePlanParams{ID: planID, UserID: userID})
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p.plan()
}

// GetRacePlanForRace gets the plan of one of the user's races, the first one
// created if it has several. It returns store.ErrNotFound if the race has no
// plan.
func (r *Repository) GetRacePlanForRace(ctx context.Context, userID, raceID pgtype.UUID) (*RacePlan, error) {
	var p planReader
	err := r.db.ReadSnapshot(ctx, func(b *store.Batch) error {
		p.queueRow(b, func(q *gen.Queries) (*gen.RacePlan, error) {
			return q.GetRacePlanForRace(ctx, &gen.GetRacePlanForRaceParams{RaceID: raceID, UserID: userID})
		})
		return nil
	}, func(b *store.Batch) error {
		p.queueChildren(ctx, b, []pgtype.UUID{p.rows[0].ID})
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p.plan()
}

// GetSharedRacePlan gets a shared race plan and its race by share token.
func (r *Repository) GetSharedRacePlan(ctx context.Context, token string) (*RacePlan, *Race, error) {
	if token == "" {
		return nil, nil, store.ErrNotFound
	}
	var p planReader
	var race *gen.Race
	err := r.db.ReadSnapshot(ctx, func(b *store.Batch) error {
		p.queueRow(b, func(q *gen.Queries) (*gen.RacePlan, error) {
			return q.GetRacePlanByShareToken(ctx, pgtype.Text{String: token, Valid: true})
		})
		return nil
	}, func(b *store.Batch) error {
		row := p.rows[0]
		p.queueChildren(ctx, b, []pgtype.UUID{row.ID})
		read(b, &race, "get race", func(q *gen.Queries) (*gen.Race, error) {
			return q.GetRaceByID(ctx, row.RaceID)
		})
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	plan, err := p.plan()
	if err != nil {
		return nil, nil, err
	}
	return plan, raceFromDB(race), nil
}

// ListRacePlans lists the user's race plans in creation order.
func (r *Repository) ListRacePlans(ctx context.Context, userID pgtype.UUID) ([]*RacePlan, error) {
	var p planReader
	err := r.db.ReadSnapshot(ctx, func(b *store.Batch) error {
		read(b, &p.rows, "list race plans", func(q *gen.Queries) ([]*gen.RacePlan, error) {
			return q.ListRacePlans(ctx, userID)
		})
		return nil
	}, func(b *store.Batch) error {
		ids := make([]pgtype.UUID, len(p.rows))
		for i, row := range p.rows {
			ids[i] = row.ID
		}
		p.queueChildren(ctx, b, ids)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return p.plans()
}

// UpdateRacePlan replaces the given sections of one of the user's race
// plans, identified by the plan's ID and user ID, leaving the other sections
// unchanged. It returns the updated plan.
func (r *Repository) UpdateRacePlan(ctx context.Context, plan *RacePlan, sections []RacePlanSection) (*RacePlan, error) {
	var touched int64
	var p planReader
	err := r.db.Transact(ctx, func(b *store.Batch) error {
		// Touching the plan checks it belongs to the user, and locks it so
		// that concurrent updates of the same section don't interleave.
		read(b, &touched, "update race plan", func(q *gen.Queries) (int64, error) {
			return q.TouchRacePlan(ctx, &gen.TouchRacePlanParams{ID: plan.ID, UserID: plan.UserID})
		})
		return nil
	}, func(b *store.Batch) error {
		if touched == 0 {
			return store.ErrNotFound
		}
		q := gen.New(b)
		for _, section := range sections {
			if err := writeSection(ctx, q, plan.ID, plan, section); err != nil {
				return err
			}
		}
		p.queuePlan(ctx, b, plan.ID, func(q *gen.Queries) (*gen.RacePlan, error) {
			return q.GetRacePlan(ctx, &gen.GetRacePlanParams{ID: plan.ID, UserID: plan.UserID})
		})
		return nil
	})
	if err != nil {
		return nil, writeError("update race plan", err)
	}
	return p.plan()
}

// ShareRacePlan creates a share token for one of the user's race plans, or
// returns the existing one.
func (r *Repository) ShareRacePlan(ctx context.Context, userID, planID pgtype.UUID) (string, error) {
	token, err := r.q.ShareRacePlan(ctx, &gen.ShareRacePlanParams{ID: planID, UserID: userID, NewToken: rand.Text()})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", store.ErrNotFound
		}
		return "", fmt.Errorf("failed to share race plan: %w", err)
	}
	return token.String, nil
}

// UnshareRacePlan removes the share token of one of the user's race plans.
func (r *Repository) UnshareRacePlan(ctx context.Context, userID, planID pgtype.UUID) error {
	n, err := r.q.UnshareRacePlan(ctx, &gen.UnshareRacePlanParams{ID: planID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to unshare race plan: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// DeleteRacePlan deletes one of the user's race plans.
func (r *Repository) DeleteRacePlan(ctx context.Context, userID, planID pgtype.UUID) error {
	n, err := r.q.DeleteRacePlan(ctx, &gen.DeleteRacePlanParams{ID: planID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete race plan: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// UpdateRaceDay replaces the selected fields of the race day of a race plan,
// leaving the others unchanged, and returns the updated plan. The caller must
// check the plan may be updated, e.g. that it belongs to the user or the
// share token matches.
func (r *Repository) UpdateRaceDay(ctx context.Context, planID pgtype.UUID, raceDay *RaceDay, fields RaceDayFields) (*RacePlan, error) {
	var p planReader
	err := r.db.Transact(ctx, func(b *store.Batch) error {
		// Locking the plan serializes concurrent updates of the splits.
		var locked pgtype.UUID
		read(b, &locked, "lock race plan", func(q *gen.Queries) (pgtype.UUID, error) {
			return q.LockRacePlan(ctx, planID)
		})
		return nil
	}, func(b *store.Batch) error {
		q := gen.New(b)
		if fields.LivetrackURL {
			if err := q.SetRaceDayLivetrackURL(ctx, &gen.SetRaceDayLivetrackURLParams{ID: planID, RaceDayLivetrackURL: raceDay.LivetrackURL}); err != nil {
				return fmt.Errorf("failed to set LiveTrack URL: %w", err)
			}
		}
		if fields.StartTime {
			if err := q.SetRaceDayStartTime(ctx, &gen.SetRaceDayStartTimeParams{ID: planID, RaceDayStartTime: timeToPG(raceDay.StartTime)}); err != nil {
				return fmt.Errorf("failed to set start time: %w", err)
			}
		}
		if fields.Splits {
			if err := q.DeleteRacePlanSplits(ctx, planID); err != nil {
				return fmt.Errorf("failed to delete splits: %w", err)
			}
			if err := upsertSplits(ctx, q, planID, raceDay.Splits); err != nil {
				return err
			}
		}
		p.queuePlan(ctx, b, planID, func(q *gen.Queries) (*gen.RacePlan, error) {
			return q.GetRacePlanByID(ctx, planID)
		})
		return nil
	})
	if err != nil {
		return nil, splitsError("update race day", err)
	}
	return p.plan()
}

// SetCheckpointSplit sets the recorded times at a single checkpoint of a race
// plan, leaving the other checkpoints unchanged, and returns the updated plan.
// If both times are zero, the split is deleted. The caller must check the
// plan may be updated, e.g. that it belongs to the user or the share token
// matches.
func (r *Repository) SetCheckpointSplit(ctx context.Context, planID pgtype.UUID, split *CheckpointSplit) (*RacePlan, error) {
	// The split is set and the plan read in a single batch, which runs in an
	// implicit transaction.
	var b store.Batch
	q := gen.New(&b)
	if split.ArrivalTime.IsZero() && split.DepartureTime.IsZero() {
		if err := q.DeleteRacePlanSplit(ctx, &gen.DeleteRacePlanSplitParams{PlanID: planID, CheckpointPosition: split.CheckpointIndex}); err != nil {
			return nil, fmt.Errorf("failed to delete split: %w", err)
		}
	} else if err := upsertSplits(ctx, q, planID, []*CheckpointSplit{split}); err != nil {
		return nil, err
	}
	var p planReader
	p.queuePlan(ctx, &b, planID, func(q *gen.Queries) (*gen.RacePlan, error) {
		return q.GetRacePlanByID(ctx, planID)
	})
	if err := b.Send(ctx, r.db); err != nil {
		return nil, splitsError("set split", err)
	}
	return p.plan()
}

func upsertSplits(ctx context.Context, q *gen.Queries, planID pgtype.UUID, splits []*CheckpointSplit) error {
	if len(splits) == 0 {
		return nil
	}
	params := &gen.UpsertRacePlanSplitsParams{PlanID: planID}
	for _, split := range splits {
		params.CheckpointPositions = append(params.CheckpointPositions, split.CheckpointIndex)
		params.ArrivalTimes = append(params.ArrivalTimes, timeToPG(split.ArrivalTime))
		params.DepartureTimes = append(params.DepartureTimes, timeToPG(split.DepartureTime))
	}
	if err := q.UpsertRacePlanSplits(ctx, params); err != nil {
		return fmt.Errorf("failed to set splits: %w", err)
	}
	return nil
}

// writeError returns the error of writing to a plan.
func writeError(op string, err error) error {
	if errors.Is(err, store.ErrNotFound) {
		return err
	}
	return fmt.Errorf("failed to %s: %w", op, err)
}

// splitsError returns the error of setting a plan's splits.
func splitsError(op string, err error) error {
	// A foreign key violation means the checkpoint of a split doesn't exist.
	if pgErr := (*pgconn.PgError)(nil); errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return ErrCheckpointNotFound
	}
	return writeError(op, err)
}

// read queues a query whose results are read into dst. The query's error is
// wrapped with what it does, and pgx.ErrNoRows replaced with
// store.ErrNotFound.
func read[T any](b *store.Batch, dst *T, does string, query func(q *gen.Queries) (T, error)) {
	b.Read(func(db store.Querier) error {
		v, err := query(gen.New(db))
		if errors.Is(err, pgx.ErrNoRows) {
			return store.ErrNotFound
		}
		if err != nil {
			return fmt.Errorf("failed to %s: %w", does, err)
		}
		*dst = v
		return nil
	})
}

// planReader reads race plans and their child rows with batched queries.
type planReader struct {
	rows          []*gen.RacePlan
	checkpoints   []*gen.RacePlanCheckpoint
	intakes       []*gen.RacePlanIntake
	products      []*gen.RacePlanNutritionProduct
	gear          []*gen.RacePlanGear
	contingencies []*gen.RacePlanContingency
	raceMorning   []*gen.RacePlanRaceMorning
	splits        []*gen.RacePlanSplit
}

// queuePlan queues reading a plan, with query, and its child rows, by the
// plan's ID. If query finds no plan, sending the batch returns
// store.ErrNotFound.
func (p *planReader) queuePlan(ctx context.Context, b *store.Batch, planID pgtype.UUID, query func(q *gen.Queries) (*gen.RacePlan, error)) {
	p.queueRow(b, query)
	p.queueChildren(ctx, b, []pgtype.UUID{planID})
}

// queueRow queues reading a plan row with query. If query finds no plan,
// sending the batch returns store.ErrNotFound.
func (p *planReader) queueRow(b *store.Batch, query func(q *gen.Queries) (*gen.RacePlan, error)) {
	p.rows = make([]*gen.RacePlan, 1)
	read(b, &p.rows[0], "get race plan", query)
}

// queueChildren queues reading the child rows of plans.
func (p *planReader) queueChildren(ctx context.Context, b *store.Batch, ids []pgtype.UUID) {
	read(b, &p.checkpoints, "list checkpoints", func(q *gen.Queries) ([]*gen.RacePlanCheckpoint, error) {
		return q.ListRacePlanCheckpoints(ctx, ids)
	})
	read(b, &p.intakes, "list intakes", func(q *gen.Queries) ([]*gen.RacePlanIntake, error) {
		return q.ListRacePlanIntakes(ctx, ids)
	})
	read(b, &p.products, "list nutrition products", func(q *gen.Queries) ([]*gen.RacePlanNutritionProduct, error) {
		return q.ListRacePlanNutritionProducts(ctx, ids)
	})
	read(b, &p.gear, "list gear", func(q *gen.Queries) ([]*gen.RacePlanGear, error) {
		return q.ListRacePlanGear(ctx, ids)
	})
	read(b, &p.contingencies, "list contingencies", func(q *gen.Queries) ([]*gen.RacePlanContingency, error) {
		return q.ListRacePlanContingencies(ctx, ids)
	})
	read(b, &p.raceMorning, "list race morning", func(q *gen.Queries) ([]*gen.RacePlanRaceMorning, error) {
		return q.ListRacePlanRaceMorning(ctx, ids)
	})
	read(b, &p.splits, "list splits", func(q *gen.Queries) ([]*gen.RacePlanSplit, error) {
		return q.ListRacePlanSplits(ctx, ids)
	})
}

// plan returns the plan read by queuePlan.
func (p *planReader) plan() (*RacePlan, error) {
	plans, err := p.plans()
	if err != nil {
		return nil, err
	}
	return plans[0], nil
}

// checkpointKey identifies a checkpoint of a plan.
type checkpointKey struct {
	planID   pgtype.UUID
	position int32
}

// plans converts the plan rows read and adds their child rows.
func (p *planReader) plans() ([]*RacePlan, error) {
	plans := make([]*RacePlan, len(p.rows))
	byID := make(map[pgtype.UUID]*RacePlan, len(p.rows))
	for i, row := range p.rows {
		plans[i] = planFromDB(row)
		byID[row.ID] = plans[i]
	}

	checkpoints := make(map[checkpointKey]*Checkpoint, len(p.checkpoints))
	for _, c := range p.checkpoints {
		cp := &Checkpoint{
			Name:                  c.Name,
			DistanceMeters:        c.DistanceMeters,
			ElevationGainMeters:   c.ElevationGainMeters,
			ElevationLossMeters:   c.ElevationLossMeters,
			CutoffSeconds:         c.CutoffSeconds,
			CrewAccess:            c.CrewAccess,
			DropBag:               c.DropBag,
			Aid:                   c.Aid,
			PlannedSegmentSeconds: c.PlannedSegmentSeconds,
			PlannedStopSeconds:    c.PlannedStopSeconds,
			SegmentStrategy:       c.SegmentStrategy,
			Tasks:                 c.Tasks,
			CrewInstructions:      c.CrewInstructions,
			DropBagContents:       c.DropBagContents,
		}
		plan := byID[c.PlanID]
		plan.Checkpoints = append(plan.Checkpoints, cp)
		checkpoints[checkpointKey{c.PlanID, c.Position}] = cp
	}

	for _, i := range p.intakes {
		cp, ok := checkpoints[checkpointKey{i.PlanID, i.CheckpointPosition}]
		if !ok {
			return nil, fmt.Errorf("intake for unknown checkpoint %d", i.CheckpointPosition)
		}
		cp.Intake = append(cp.Intake, &Intake{Product: i.Product, Quantity: i.Quantity})
	}

	for _, product := range p.products {
		plan := byID[product.PlanID]
		plan.Nutrition.Products = append(plan.Nutrition.Products, &NutritionProduct{
			Name:       product.Name,
			CarbsGrams: product.CarbsGrams,
			SodiumMg:   product.SodiumMg,
			FluidMl:    product.FluidMl,
			CaffeineMg: product.CaffeineMg,
		})
	}

	for _, g := range p.gear {
		plan := byID[g.PlanID]
		plan.Gear = append(plan.Gear, &GearItem{
			Name:      g.Name,
			Mandatory: g.Mandatory,
			Location:  g.Location,
			Packed:    g.Packed,
			Notes:     g.Notes,
		})
	}

	for _, c := range p.contingencies {
		plan := byID[c.PlanID]
		plan.Contingencies = append(plan.Contingencies, &Contingency{
			Scenario: ContingencyScenario(c.Scenario),
			Trigger:  c.Trigger,
			Response: c.Response,
		})
	}

	for _, t := range p.raceMorning {
		plan := byID[t.PlanID]
		plan.RaceMorning = append(plan.RaceMorning, &TimelineItem{Time: t.Time, Description: t.Description})
	}

	for _, split := range p.splits {
		plan := byID[split.PlanID]
		plan.RaceDay.Splits = append(plan.RaceDay.Splits, &CheckpointSplit{
			CheckpointIndex: split.CheckpointPosition,
			ArrivalTime:     timeFromPG(split.ArrivalTime),
			DepartureTime:   timeFromPG(split.DepartureTime),
		})
	}
	return plans, nil
}

// writeSection replaces a section of a plan. The caller must check the plan
// belongs to the user.
func writeSection(ctx context.Context, q *gen.Queries, planID pgtype.UUID, plan *RacePlan, section RacePlanSection) error {
	var err error
	switch section {
	case SectionGoals:
		err = q.UpdateRacePlanGoals(ctx, &gen.UpdateRacePlanGoalsParams{
			ID:                  planID,
			GoalsAFinishSeconds: plan.Goals.AFinishSeconds,
			GoalsBFinishSeconds: plan.Goals.BFinishSeconds,
			GoalsCFinishSeconds: plan.Goals.CFinishSeconds,
			GoalsNotes:          plan.Goals.Notes,
		})
	case SectionCheckpoints:
		err = writeCheckpoints(ctx, q, planID, plan.Checkpoints)
	case SectionPacing:
		err = q.UpdateRacePlanPacing(ctx, &gen.UpdateRacePlanPacingParams{
			ID:                     planID,
			PacingStrategy:         plan.Pacing.Strategy,
			PacingHikeGradePercent: plan.Pacing.HikeGradePercent,
			PacingHeartRateCapBpm:  plan.Pacing.HeartRateCapBpm,
			PacingNotes:            plan.Pacing.Notes,
		})
	case SectionNutrition:
		err = q.UpdateRacePlanNutrition(ctx, &gen.UpdateRacePlanNutritionParams{
			ID:                         planID,
			NutritionCarbsGramsPerHour: plan.Nutrition.CarbsGramsPerHour,
			NutritionFluidMlPerHour:    plan.Nutrition.FluidMlPerHour,
			NutritionSodiumMgPerHour:   plan.Nutrition.SodiumMgPerHour,
			NutritionNotes:             plan.Nutrition.Notes,
		})
		if err == nil {
			err = writeNutritionProducts(ctx, q, planID, plan.Nutrition.Products)
		}
	case SectionGear:
		err = writeGear(ctx, q, planID, plan.Gear)
	case SectionCrew:
		err = q.UpdateRacePlanCrew(ctx, &gen.UpdateRacePlanCrewParams{
			ID:          planID,
			CrewHasCrew: plan.Crew.HasCrew,
			CrewMembers: plan.Crew.Members,
			CrewNotes:   plan.Crew.Notes,
		})
	case SectionContingencies:
		err = writeContingencies(ctx, q, planID, plan.Contingencies)
	case SectionRaceMorning:
		err = writeRaceMorning(ctx, q, planID, plan.RaceMorning)
	case SectionMental:
		err = q.UpdateRacePlanMental(ctx, &gen.UpdateRacePlanMentalParams{
			ID:            planID,
			MentalMantras: nonNil(plan.Mental.Mantras),
			MentalNotes:   plan.Mental.Notes,
		})
	case SectionNotes:
		err = q.UpdateRacePlanNotes(ctx, &gen.UpdateRacePlanNotesParams{ID: planID, Notes: plan.Notes})
	default:
		return fmt.Errorf("unknown race plan section %q", section)
	}
	if err != nil {
		return fmt.Errorf("failed to write race plan %s: %w", section, err)
	}
	return nil
}

// writeCheckpoints replaces the checkpoints of a plan. Checkpoints are updated
// in place by position and the ones past the end deleted, rather than all
// replaced, so that rows referencing the remaining checkpoints are kept.
func writeCheckpoints(ctx context.Context, q *gen.Queries, planID pgtype.UUID, checkpoints []*Checkpoint) error {
	if err := q.DeleteRacePlanIntakes(ctx, planID); err != nil {
		return err
	}
	cps, intakes := checkpointParams(planID, checkpoints)
	if len(checkpoints) > 0 {
		if err := q.UpsertRacePlanCheckpoints(ctx, cps); err != nil {
			return err
		}
	}
	if err := q.DeleteRacePlanCheckpointsFrom(ctx, &gen.DeleteRacePlanCheckpointsFromParams{
		PlanID:       planID,
		FromPosition: int32(len(checkpoints)),
	}); err != nil {
		return err
	}
	if len(intakes.Positions) > 0 {
		return q.CreateRacePlanIntakes(ctx, intakes)
	}
	return nil
}

// createCheckpoints creates the checkpoints of a plan that has none.
func createCheckpoints(ctx context.Context, q *gen.Queries, planID pgtype.UUID, checkpoints []*Checkpoint) error {
	if len(checkpoints) == 0 {
		return nil
	}
	cps, intakes := checkpointParams(planID, checkpoints)
	if err := q.UpsertRacePlanCheckpoints(ctx, cps); err != nil {
		return err
	}
	if len(intakes.Positions) > 0 {
		return q.CreateRacePlanIntakes(ctx, intakes)
	}
	return nil
}

// checkpointParams returns the parameters to store checkpoints and their
// intakes.
func checkpointParams(planID pgtype.UUID, checkpoints []*Checkpoint) (*gen.UpsertRacePlanCheckpointsParams, *gen.CreateRacePlanIntakesParams) {
	cps := &gen.UpsertRacePlanCheckpointsParams{PlanID: planID}
	intakes := &gen.CreateRacePlanIntakesParams{PlanID: planID}
	for i, cp := range checkpoints {
		cps.Positions = append(cps.Positions, int32(i))
		cps.Names = append(cps.Names, cp.Name)
		cps.DistancesMeters = append(cps.DistancesMeters, cp.DistanceMeters)
		cps.ElevationGainsMeters = append(cps.ElevationGainsMeters, cp.ElevationGainMeters)
		cps.ElevationLossesMeters = append(cps.ElevationLossesMeters, cp.ElevationLossMeters)
		cps.CutoffsSeconds = append(cps.CutoffsSeconds, cp.CutoffSeconds)
		cps.CrewAccess = append(cps.CrewAccess, cp.CrewAccess)
		cps.DropBags = append(cps.DropBags, cp.DropBag)
		cps.Aid = append(cps.Aid, cp.Aid)
		cps.PlannedSegmentsSeconds = append(cps.PlannedSegmentsSeconds, cp.PlannedSegmentSeconds)
		cps.PlannedStopsSeconds = append(cps.PlannedStopsSeconds, cp.PlannedStopSeconds)
		cps.SegmentStrategies = append(cps.SegmentStrategies, cp.SegmentStrategy)
		cps.Tasks = append(cps.Tasks, cp.Tasks)
		cps.CrewInstructions = append(cps.CrewInstructions, cp.CrewInstructions)
		cps.DropBagContents = append(cps.DropBagContents, cp.DropBagContents)
		for j, intake := range cp.Intake {
			intakes.CheckpointPositions = append(intakes.CheckpointPositions, int32(i))
			intakes.Positions = append(intakes.Positions, int32(j))
			intakes.Products = append(intakes.Products, intake.Product)
			intakes.Quantities = append(intakes.Quantities, intake.Quantity)
		}
	}
	return cps, intakes
}

func writeNutritionProducts(ctx context.Context, q *gen.Queries, planID pgtype.UUID, products []*NutritionProduct) error {
	if err := q.DeleteRacePlanNutritionProducts(ctx, planID); err != nil {
		return err
	}
	return createNutritionProducts(ctx, q, planID, products)
}

func createNutritionProducts(ctx context.Context, q *gen.Queries, planID pgtype.UUID, products []*NutritionProduct) error {
	if len(products) == 0 {
		return nil
	}
	params := &gen.CreateRacePlanNutritionProductsParams{PlanID: planID}
	for i, p := range products {
		params.Positions = append(params.Positions, int32(i))
		params.Names = append(params.Names, p.Name)
		params.CarbsGrams = append(params.CarbsGrams, p.CarbsGrams)
		params.SodiumMg = append(params.SodiumMg, p.SodiumMg)
		params.FluidMl = append(params.FluidMl, p.FluidMl)
		params.CaffeineMg = append(params.CaffeineMg, p.CaffeineMg)
	}
	return q.CreateRacePlanNutritionProducts(ctx, params)
}

func writeGear(ctx context.Context, q *gen.Queries, planID pgtype.UUID, gear []*GearItem) error {
	if err := q.DeleteRacePlanGear(ctx, planID); err != nil {
		return err
	}
	return createGear(ctx, q, planID, gear)
}

func createGear(ctx context.Context, q *gen.Queries, planID pgtype.UUID, gear []*GearItem) error {
	if len(gear) == 0 {
		return nil
	}
	params := &gen.CreateRacePlanGearParams{PlanID: planID}
	for i, g := range gear {
		params.Positions = append(params.Positions, int32(i))
		params.Names = append(params.Names, g.Name)
		params.Mandatory = append(params.Mandatory, g.Mandatory)
		params.Locations = append(params.Locations, g.Location)
		params.Packed = append(params.Packed, g.Packed)
		params.Notes = append(params.Notes, g.Notes)
	}
	return q.CreateRacePlanGear(ctx, params)
}

func writeContingencies(ctx context.Context, q *gen.Queries, planID pgtype.UUID, contingencies []*Contingency) error {
	if err := q.DeleteRacePlanContingencies(ctx, planID); err != nil {
		return err
	}
	return createContingencies(ctx, q, planID, contingencies)
}

func createContingencies(ctx context.Context, q *gen.Queries, planID pgtype.UUID, contingencies []*Contingency) error {
	if len(contingencies) == 0 {
		return nil
	}
	params := &gen.CreateRacePlanContingenciesParams{PlanID: planID}
	for i, c := range contingencies {
		params.Positions = append(params.Positions, int32(i))
		params.Scenarios = append(params.Scenarios, string(c.Scenario))
		params.Triggers = append(params.Triggers, c.Trigger)
		params.Responses = append(params.Responses, c.Response)
	}
	return q.CreateRacePlanContingencies(ctx, params)
}

func writeRaceMorning(ctx context.Context, q *gen.Queries, planID pgtype.UUID, items []*TimelineItem) error {
	if err := q.DeleteRacePlanRaceMorning(ctx, planID); err != nil {
		return err
	}
	return createRaceMorning(ctx, q, planID, items)
}

func createRaceMorning(ctx context.Context, q *gen.Queries, planID pgtype.UUID, items []*TimelineItem) error {
	if len(items) == 0 {
		return nil
	}
	params := &gen.CreateRacePlanRaceMorningParams{PlanID: planID}
	for i, t := range items {
		params.Positions = append(params.Positions, int32(i))
		params.Times = append(params.Times, t.Time)
		params.Descriptions = append(params.Descriptions, t.Description)
	}
	return q.CreateRacePlanRaceMorning(ctx, params)
}

// timeToPG converts a time to a postgres timestamp. The zero time is NULL.
func timeToPG(t time.Time) pgtype.Timestamptz {
	if t.IsZero() {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// timeFromPG converts a postgres timestamp to a time. NULL is the zero time.
func timeFromPG(t pgtype.Timestamptz) time.Time {
	if !t.Valid {
		return time.Time{}
	}
	return t.Time
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// planFromDB converts a plan row, without its child rows.
func planFromDB(p *gen.RacePlan) *RacePlan {
	return &RacePlan{
		ID:         p.ID,
		UserID:     p.UserID,
		RaceID:     p.RaceID,
		ShareToken: p.ShareToken.String,
		Goals: RaceGoals{
			AFinishSeconds: p.GoalsAFinishSeconds,
			BFinishSeconds: p.GoalsBFinishSeconds,
			CFinishSeconds: p.GoalsCFinishSeconds,
			Notes:          p.GoalsNotes,
		},
		Pacing: PacingStrategy{
			Strategy:         p.PacingStrategy,
			HikeGradePercent: p.PacingHikeGradePercent,
			HeartRateCapBpm:  p.PacingHeartRateCapBpm,
			Notes:            p.PacingNotes,
		},
		Nutrition: NutritionStrategy{
			CarbsGramsPerHour: p.NutritionCarbsGramsPerHour,
			FluidMlPerHour:    p.NutritionFluidMlPerHour,
			SodiumMgPerHour:   p.NutritionSodiumMgPerHour,
			Notes:             p.NutritionNotes,
		},
		Crew: CrewPlan{
			HasCrew: p.CrewHasCrew,
			Members: p.CrewMembers,
			Notes:   p.CrewNotes,
		},
		Mental: MentalStrategy{
			Mantras: p.MentalMantras,
			Notes:   p.MentalNotes,
		},
		Notes: p.Notes,
		RaceDay: RaceDay{
			LivetrackURL: p.RaceDayLivetrackURL,
			StartTime:    timeFromPG(p.RaceDayStartTime),
		},
		CreateTime: p.CreateTime.Time,
		UpdateTime: p.UpdateTime.Time,
	}
}
