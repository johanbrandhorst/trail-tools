package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// maxWeightKilograms is the heaviest weight, far above any dumbbell, to
	// catch weights in the wrong unit.
	maxWeightKilograms = 300
	// maxOwnedWeights is how many weights of a kind of equipment the user
	// can list, enough for a rack of dumbbells in both units.
	maxOwnedWeights = 30
	maxAnimations   = 3
	maxProps        = 6
	minKeyframes    = 2
	maxKeyframes    = 8
	// maxKeyframeMilliseconds is the longest move or hold of a keyframe.
	maxKeyframeMilliseconds = 10000
	maxCueRunes             = 60
)

// equipmentKinds are the stored kinds of equipment by their API values,
// whose names are the stored names in upper case, prefixed with
// EQUIPMENT_.
var equipmentKinds = func() map[plansv1.Equipment]plans.Equipment {
	m := map[plansv1.Equipment]plans.Equipment{}
	for _, e := range plans.Equipments {
		value, ok := plansv1.Equipment_value["EQUIPMENT_"+strings.ToUpper(string(e))]
		if !ok {
			panic(fmt.Sprintf("equipment %q has no API value", e))
		}
		m[plansv1.Equipment(value)] = e
	}
	return m
}()

var equipmentKindsToProto = reverse(equipmentKinds)

func equipmentListFromProto(field string, list []plansv1.Equipment) ([]plans.Equipment, error) {
	var result []plans.Equipment
	for _, e := range list {
		kind, ok := equipmentKinds[e]
		if !ok {
			return nil, invalidArgument("%s: invalid equipment %v", field, e)
		}
		if !slices.Contains(result, kind) {
			result = append(result, kind)
		}
	}
	return result, nil
}

func equipmentListToProto(list []plans.Equipment) []plansv1.Equipment {
	result := make([]plansv1.Equipment, len(list))
	for i, e := range list {
		result[i] = equipmentKindsToProto[e]
	}
	return result
}

// weightFromProto converts and validates a weight. An unset weight is none.
func weightFromProto(field string, w *plansv1.Weight) (plans.Weight, error) {
	if w.GetValue() == 0 {
		return plans.Weight{}, nil
	}
	var weight plans.Weight
	switch w.GetUnit() {
	case plansv1.WeightUnit_WEIGHT_UNIT_KILOGRAMS:
		weight = plans.Weight{Value: w.GetValue(), Unit: plans.Kilograms}
	case plansv1.WeightUnit_WEIGHT_UNIT_POUNDS:
		weight = plans.Weight{Value: w.GetValue(), Unit: plans.Pounds}
	default:
		return plans.Weight{}, fmt.Errorf("%s.unit must be KILOGRAMS or POUNDS", field)
	}
	if math.IsNaN(w.GetValue()) || w.GetValue() < 0 || weight.Kilograms() > maxWeightKilograms {
		return plans.Weight{}, fmt.Errorf("%s.value must be between 0 and %d kg", field, maxWeightKilograms)
	}
	return weight, nil
}

// weightToProto converts a weight, returning nil for none.
func weightToProto(w plans.Weight) *plansv1.Weight {
	if w.Value == 0 {
		return nil
	}
	unit := plansv1.WeightUnit_WEIGHT_UNIT_KILOGRAMS
	if w.Unit == plans.Pounds {
		unit = plansv1.WeightUnit_WEIGHT_UNIT_POUNDS
	}
	return plansv1.Weight_builder{Value: w.Value, Unit: unit, Kilograms: math.Round(w.Kilograms()*100) / 100}.Build()
}

func equipmentIssuesToProto(issues []plans.EquipmentIssue) []*plansv1.EquipmentIssue {
	result := make([]*plansv1.EquipmentIssue, len(issues))
	for i, issue := range issues {
		weights := make([]*plansv1.Weight, len(issue.ClosestWeights))
		for j, w := range issue.ClosestWeights {
			weights[j] = weightToProto(w)
		}
		result[i] = plansv1.EquipmentIssue_builder{
			ExerciseIndex:  int32(issue.ExerciseIndex),
			Equipment:      equipmentKindsToProto[issue.Equipment],
			ClosestWeights: weights,
			Description:    issue.Description,
		}.Build()
	}
	return result
}

// animationsFromProto validates an exercise's animations, and returns them
// as stored: a JSON array of their JSON forms.
func animationsFromProto(animations []*plansv1.ExerciseAnimation) (json.RawMessage, error) {
	if len(animations) > maxAnimations {
		return nil, invalidArgument("animations: at most %d are allowed", maxAnimations)
	}
	encoded := make([]json.RawMessage, len(animations))
	for i, a := range animations {
		if err := validateAnimation(a); err != nil {
			return nil, invalidArgument("animations[%d]: %v", i, err)
		}
		data, err := protojson.Marshal(a)
		if err != nil {
			return nil, fmt.Errorf("failed to encode animation: %w", err)
		}
		encoded[i] = data
	}
	data, err := json.Marshal(encoded)
	if err != nil {
		return nil, fmt.Errorf("failed to encode animations: %w", err)
	}
	return data, nil
}

func validateAnimation(a *plansv1.ExerciseAnimation) error {
	if strings.TrimSpace(a.GetTitle()) == "" {
		return errors.New("title is required")
	}
	if a.GetAnchor() == plansv1.BodyPoint_BODY_POINT_UNSPECIFIED {
		return errors.New("anchor is required, e.g. NEAR_FOOT for a standing exercise")
	}
	for _, p := range a.GetPlanted() {
		switch p {
		case plansv1.BodyPoint_BODY_POINT_NEAR_HAND, plansv1.BodyPoint_BODY_POINT_FAR_HAND,
			plansv1.BodyPoint_BODY_POINT_NEAR_FOOT, plansv1.BodyPoint_BODY_POINT_FAR_FOOT:
		default:
			return fmt.Errorf("planted: only hands and feet can be planted, not %v", p)
		}
	}
	if len(a.GetProps()) > maxProps {
		return fmt.Errorf("props: at most %d are allowed", maxProps)
	}
	for i, p := range a.GetProps() {
		if p.GetKind() == plansv1.AnimationPropKind_ANIMATION_PROP_KIND_UNSPECIFIED {
			return fmt.Errorf("props[%d]: kind is required", i)
		}
		if p.GetKind() != plansv1.AnimationPropKind_ANIMATION_PROP_KIND_MAT && p.GetPoint() == plansv1.BodyPoint_BODY_POINT_UNSPECIFIED {
			return fmt.Errorf("props[%d]: point is required", i)
		}
	}
	if n := len(a.GetKeyframes()); n < minKeyframes || n > maxKeyframes {
		return fmt.Errorf("between %d and %d keyframes are required", minKeyframes, maxKeyframes)
	}
	for i, k := range a.GetKeyframes() {
		if !k.HasPose() {
			return fmt.Errorf("keyframes[%d]: pose is required", i)
		}
		p := k.GetPose()
		for _, angle := range []float64{
			p.GetTorsoDegrees(), p.GetHeadDegrees(), p.GetNearThighDegrees(), p.GetNearShinDegrees(),
			p.GetFarThighDegrees(), p.GetFarShinDegrees(), p.GetNearUpperArmDegrees(), p.GetNearForearmDegrees(),
			p.GetFarUpperArmDegrees(), p.GetFarForearmDegrees(),
		} {
			if math.IsNaN(angle) || math.Abs(angle) > 360 {
				return fmt.Errorf("keyframes[%d]: angles must be between -360 and 360 degrees", i)
			}
		}
		if k.GetMoveMilliseconds() < 0 || k.GetMoveMilliseconds() > maxKeyframeMilliseconds ||
			k.GetHoldMilliseconds() < 0 || k.GetHoldMilliseconds() > maxKeyframeMilliseconds {
			return fmt.Errorf("keyframes[%d]: move_milliseconds and hold_milliseconds must be between 0 and %d", i, maxKeyframeMilliseconds)
		}
		if len([]rune(k.GetCue())) > maxCueRunes {
			return fmt.Errorf("keyframes[%d]: cue must be at most %d characters", i, maxCueRunes)
		}
	}
	return nil
}

// animationsToProto decodes stored animations.
func animationsToProto(data json.RawMessage) ([]*plansv1.ExerciseAnimation, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var encoded []json.RawMessage
	if err := json.Unmarshal(data, &encoded); err != nil {
		return nil, fmt.Errorf("failed to decode animations: %w", err)
	}
	result := make([]*plansv1.ExerciseAnimation, len(encoded))
	for i, e := range encoded {
		a := &plansv1.ExerciseAnimation{}
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(e, a); err != nil {
			return nil, fmt.Errorf("failed to decode animation: %w", err)
		}
		result[i] = a
	}
	return result, nil
}

func ownedEquipmentFromProto(list []*plansv1.OwnedEquipment) ([]plans.OwnedEquipment, error) {
	var result []plans.OwnedEquipment
	seen := map[plans.Equipment]bool{}
	for i, o := range list {
		kind, ok := equipmentKinds[o.GetEquipment()]
		if !ok {
			return nil, invalidArgument("equipment[%d]: equipment is required", i)
		}
		if seen[kind] {
			return nil, invalidArgument("equipment[%d]: %v is listed twice", i, o.GetEquipment())
		}
		seen[kind] = true
		owned := plans.OwnedEquipment{Equipment: kind, Notes: strings.TrimSpace(o.GetNotes())}
		if len(o.GetWeights()) > 0 && !kind.Weighted() {
			return nil, invalidArgument("equipment[%d]: only dumbbells, kettlebells and medicine balls have weights", i)
		}
		if len(o.GetWeights()) > maxOwnedWeights {
			return nil, invalidArgument("equipment[%d]: at most %d weights are allowed", i, maxOwnedWeights)
		}
		for j, w := range o.GetWeights() {
			weight, err := weightFromProto(fmt.Sprintf("equipment[%d].weights[%d]", i, j), w)
			if err != nil {
				return nil, invalidArgument("%v", err)
			}
			if weight.Value == 0 {
				return nil, invalidArgument("equipment[%d].weights[%d]: value is required", i, j)
			}
			if !slices.Contains(owned.Weights, weight) {
				owned.Weights = append(owned.Weights, weight)
			}
		}
		plans.SortWeights(owned.Weights)
		result = append(result, owned)
	}
	// Listed in the order of the enum, like the settings page.
	slices.SortFunc(result, func(a, b plans.OwnedEquipment) int {
		return int(equipmentKindsToProto[a.Equipment] - equipmentKindsToProto[b.Equipment])
	})
	return result, nil
}

func homeEquipmentToProto(home *plans.HomeEquipment) ([]*plansv1.OwnedEquipment, *timestamppb.Timestamp) {
	result := make([]*plansv1.OwnedEquipment, len(home.Equipment))
	for i, o := range home.Equipment {
		weights := make([]*plansv1.Weight, len(o.Weights))
		for j, w := range o.Weights {
			weights[j] = weightToProto(w)
		}
		result[i] = plansv1.OwnedEquipment_builder{
			Equipment: equipmentKindsToProto[o.Equipment],
			Weights:   weights,
			Notes:     o.Notes,
		}.Build()
	}
	if home.UpdateTime.IsZero() {
		return result, nil
	}
	return result, timestamppb.New(home.UpdateTime)
}

func (s *ExerciseService) GetHomeEquipment(ctx context.Context, _ *plansv1.GetHomeEquipmentRequest) (*plansv1.GetHomeEquipmentResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	home, err := s.exercises.GetHomeEquipment(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get home equipment")
	}
	equipment, updateTime := homeEquipmentToProto(home)
	return plansv1.GetHomeEquipmentResponse_builder{Equipment: equipment, UpdateTime: updateTime}.Build(), nil
}

func (s *ExerciseService) SetHomeEquipment(ctx context.Context, req *plansv1.SetHomeEquipmentRequest) (*plansv1.SetHomeEquipmentResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	owned, err := ownedEquipmentFromProto(req.GetEquipment())
	if err != nil {
		return nil, err
	}
	home, err := s.exercises.SetHomeEquipment(ctx, userID, owned)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to set home equipment")
	}
	equipment, updateTime := homeEquipmentToProto(home)
	return plansv1.SetHomeEquipmentResponse_builder{Equipment: equipment, UpdateTime: updateTime}.Build(), nil
}
