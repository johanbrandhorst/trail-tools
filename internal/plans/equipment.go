package plans

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/bs-tools/trailrunn.ing/internal/plans/internal/gen"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/jackc/pgx/v5/pgtype"
)

// EquipmentIssue is equipment an exercise of a strength workout needs that
// the user doesn't have at home.
type EquipmentIssue struct {
	// The index of the exercise in the workout's exercises.
	ExerciseIndex int
	Equipment     Equipment
	// For a weight the user doesn't have, the closest weights of the
	// equipment they have, lighter and heavier. Nil if they don't have the
	// equipment at all.
	ClosestWeights []Weight
	Description    string
}

// equipmentNames are how descriptions name equipment, as in "needs a
// bench".
var equipmentNames = map[Equipment]string{
	"dumbbells":          "dumbbells",
	"kettlebell":         "a kettlebell",
	"barbell":            "a barbell",
	"squat_rack":         "a squat rack",
	"medicine_ball":      "a medicine ball",
	"bench":              "a bench",
	"step":               "a step or box",
	"mat":                "a mat",
	"resistance_band":    "a resistance band",
	"mini_band":          "a mini band",
	"pull_up_bar":        "a pull-up bar",
	"suspension_trainer": "a suspension trainer",
	"foam_roller":        "a foam roller",
	"sliders":            "sliders",
	"balance_board":      "a balance board",
	"jump_rope":          "a jump rope",
}

// weightedName names weighted equipment of a weight, as in "planned with
// 10 kg dumbbells".
func weightedName(e Equipment, w Weight) string {
	switch e {
	case EquipmentDumbbells:
		return w.String() + " dumbbells"
	case EquipmentMedicineBall:
		return "a " + w.String() + " medicine ball"
	default:
		return "a " + w.String() + " kettlebell"
	}
}

// GetHomeEquipment gets the equipment the user has at home.
func (r *Repository) GetHomeEquipment(ctx context.Context, userID pgtype.UUID) (*HomeEquipment, error) {
	rows, err := r.q.GetHomeEquipment(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get home equipment: %w", err)
	}
	return homeEquipmentFromDB(rows)
}

// SetHomeEquipment replaces the equipment the user has at home.
func (r *Repository) SetHomeEquipment(ctx context.Context, userID pgtype.UUID, equipment []OwnedEquipment) (*HomeEquipment, error) {
	data, err := json.Marshal(nonNilEquipment(equipment))
	if err != nil {
		return nil, fmt.Errorf("failed to encode home equipment: %w", err)
	}
	row, err := r.q.SetHomeEquipment(ctx, &gen.SetHomeEquipmentParams{UserID: userID, Equipment: data})
	if err != nil {
		return nil, fmt.Errorf("failed to set home equipment: %w", err)
	}
	return homeEquipmentFromDB([]*gen.HomeEquipment{row})
}

func nonNilEquipment(equipment []OwnedEquipment) []OwnedEquipment {
	if equipment == nil {
		return []OwnedEquipment{}
	}
	return equipment
}

func homeEquipmentFromDB(rows []*gen.HomeEquipment) (*HomeEquipment, error) {
	if len(rows) == 0 {
		return &HomeEquipment{}, nil
	}
	home := &HomeEquipment{UpdateTime: rows[0].UpdateTime.Time}
	if err := json.Unmarshal(rows[0].Equipment, &home.Equipment); err != nil {
		return nil, fmt.Errorf("failed to decode home equipment: %w", err)
	}
	return home, nil
}

// queueHomeEquipment queues reading the user's home equipment into home.
func queueHomeEquipment(ctx context.Context, b *store.Batch, userID pgtype.UUID, home **HomeEquipment) {
	b.Read(func(db store.Querier) error {
		rows, err := gen.New(db).GetHomeEquipment(ctx, userID)
		if err != nil {
			return err
		}
		*home, err = homeEquipmentFromDB(rows)
		return err
	})
}

// EquipmentIssues returns the exercises the user lacks the equipment or the
// weights for. It returns none if the user hasn't said what they have, as
// they may have everything.
func EquipmentIssues(exercises []*PlannedExercise, home *HomeEquipment) []EquipmentIssue {
	if home == nil || home.UpdateTime.IsZero() {
		return nil
	}
	owned := map[Equipment]OwnedEquipment{}
	for _, e := range home.Equipment {
		owned[e.Equipment] = e
	}
	var issues []EquipmentIssue
	for i, e := range exercises {
		for _, needed := range e.ExerciseEquipment {
			have, ok := owned[needed]
			if !ok {
				issues = append(issues, EquipmentIssue{
					ExerciseIndex: i,
					Equipment:     needed,
					Description:   fmt.Sprintf("%s needs %s, which you don't have at home", e.ExerciseName, equipmentNames[needed]),
				})
				continue
			}
			// Equipment without weights listed is taken to come in any
			// weight, like adjustable dumbbells.
			if !needed.Weighted() || e.Weight.Value == 0 || len(have.Weights) == 0 {
				continue
			}
			if slices.ContainsFunc(have.Weights, e.Weight.Same) {
				continue
			}
			closest := closestWeights(e.Weight, have.Weights)
			names := make([]string, len(closest))
			for j, w := range closest {
				names[j] = w.String()
			}
			issues = append(issues, EquipmentIssue{
				ExerciseIndex:  i,
				Equipment:      needed,
				ClosestWeights: closest,
				Description: fmt.Sprintf("%s is planned with %s, and you have %s ones", e.ExerciseName,
					weightedName(needed, e.Weight), strings.Join(names, " and ")),
			})
		}
	}
	return issues
}

// closestWeights returns the heaviest of weights lighter than w and the
// lightest heavier, if any.
func closestWeights(w Weight, weights []Weight) []Weight {
	var lighter, heavier *Weight
	for _, other := range weights {
		switch {
		case other.Kilograms() < w.Kilograms():
			if lighter == nil || other.Kilograms() > lighter.Kilograms() {
				lighter = &other
			}
		case heavier == nil || other.Kilograms() < heavier.Kilograms():
			heavier = &other
		}
	}
	var result []Weight
	for _, c := range []*Weight{lighter, heavier} {
		if c != nil {
			result = append(result, *c)
		}
	}
	return result
}

// SortWeights sorts weights from the lightest.
func SortWeights(weights []Weight) {
	slices.SortFunc(weights, func(a, b Weight) int { return cmp.Compare(a.Kilograms(), b.Kilograms()) })
}
