package plan

import (
	"context"

	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
)

// stockPose is a pose of the stick figure that agents start animations
// from. The angles were drawn and checked by eye, and worked out so hands,
// feet and shoulders reach the floor where they should: the figure's
// segments are 52 units for the torso, 44 for a thigh, 42 for a shin and 58
// for an arm, so a plank's straight body rises to its shoulders at the
// angle where the arms reach the floor.
type stockPose struct {
	name, description string
	// torso, head, near thigh, near shin, far thigh, far shin, near upper
	// arm, near forearm, far upper arm and far forearm, in degrees.
	angles  [10]float64
	anchor  plansv1.BodyPoint
	planted []plansv1.BodyPoint
	props   []prop
}

type prop struct {
	kind  plansv1.AnimationPropKind
	point plansv1.BodyPoint
}

const (
	shoulders = plansv1.BodyPoint_BODY_POINT_SHOULDERS
	hips      = plansv1.BodyPoint_BODY_POINT_HIPS
	nearHand  = plansv1.BodyPoint_BODY_POINT_NEAR_HAND
	farHand   = plansv1.BodyPoint_BODY_POINT_FAR_HAND
	nearKnee  = plansv1.BodyPoint_BODY_POINT_NEAR_KNEE
	nearFoot  = plansv1.BodyPoint_BODY_POINT_NEAR_FOOT
	farFoot   = plansv1.BodyPoint_BODY_POINT_FAR_FOOT
)

var stockPoses = []stockPose{
	{
		"standing", "Standing tall, arms by the sides. The start of most standing exercises.",
		[10]float64{180, 180, 0, 0, 0, 0, 0, 0, 0, 0},
		nearFoot,
		[]plansv1.BodyPoint{farFoot},
		nil,
	},
	{
		"squat_bottom", "The bottom of a squat: thighs parallel to the floor, knees forward, chest up and arms forward for balance.",
		[10]float64{140, 155, 85, -25, 85, -25, 90, 90, 90, 90},
		nearFoot,
		[]plansv1.BodyPoint{farFoot},
		nil,
	},
	{
		"split_squat_bottom", "The bottom of a split squat or lunge: the near leg in front with its thigh parallel to the floor, the far knee just above the floor, torso upright. For a Bulgarian split squat, raise the far foot onto a bench.",
		[10]float64{180, 180, 90, 0, -15, -90, 0, 0, 0, 0},
		nearFoot,
		[]plansv1.BodyPoint{farFoot},
		nil,
	},
	{
		"hinge", "The bottom of a Romanian deadlift or good morning: hips pushed back, back flat and nearly level, knees soft, arms hanging.",
		[10]float64{100, 100, 15, -10, 15, -10, 0, 0, 0, 0},
		nearFoot,
		[]plansv1.BodyPoint{farFoot},
		nil,
	},
	{
		"single_leg_hinge", "The bottom of a single-leg Romanian deadlift: standing on the near leg, the far leg straight back in line with the torso.",
		[10]float64{95, 95, 10, -5, -88, -88, 0, 0, 0, 0},
		nearFoot, nil, nil,
	},
	{
		"step_up", "Stepping up: the near foot on a step at about knee height, the far leg straight below.",
		[10]float64{172, 175, 90, 0, 0, 0, 0, 0, 0, 0},
		nearFoot, nil,
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_STEP, nearFoot}},
	},
	{
		"seated", "Sitting on a bench, thighs level and feet on the floor, hands forward.",
		[10]float64{180, 180, 90, 0, 90, 0, 0, 90, 0, 90},
		hips,
		[]plansv1.BodyPoint{nearFoot, farFoot},
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_BENCH, hips}},
	},
	{
		"tall_kneeling", "Kneeling upright, shins on the floor behind, hips over the knees.",
		[10]float64{180, 180, 0, -90, 0, -90, 0, 0, 0, 0},
		nearKnee,
		nil,
		nil,
	},
	{
		"hands_and_knees", "On hands and knees: hands under the shoulders, knees under the hips, back flat. The start of bird dogs and cat-cows.",
		[10]float64{106, 100, 0, -90, 0, -90, 0, 0, 0, 0},
		nearKnee,
		[]plansv1.BodyPoint{nearHand, farHand},
		nil,
	},
	{
		"forearm_plank", "A forearm plank: elbows under the shoulders, body straight from the feet to the shoulders.",
		[10]float64{103, 100, -77, -77, -77, -77, 0, 90, 0, 90},
		nearFoot,
		[]plansv1.BodyPoint{farFoot, nearHand, farHand},
		nil,
	},
	{
		"push_up_top", "The top of a push-up, or a high plank: arms straight under the shoulders, body straight from the feet to the shoulders.",
		[10]float64{115, 110, -65, -65, -65, -65, 0, 0, 0, 0},
		nearFoot,
		[]plansv1.BodyPoint{farFoot, nearHand, farHand},
		nil,
	},
	{
		"push_up_bottom", "The bottom of a push-up: chest just above the floor, forearms upright, elbows back, body straight. Move between it and push_up_top.",
		[10]float64{94, 92, -86, -86, -86, -86, -127, 0, -127, 0},
		nearFoot,
		[]plansv1.BodyPoint{farFoot, nearHand, farHand},
		nil,
	},
	{
		"lying_on_back", "Lying on the back, legs straight, arms along the sides, head to the left.",
		[10]float64{-90, -90, 90, 90, 90, 90, 90, 90, 90, 90},
		hips, nil,
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_MAT, plansv1.BodyPoint_BODY_POINT_UNSPECIFIED}},
	},
	{
		"lying_knees_bent", "Lying on the back with the knees bent and feet flat: the start of a glute bridge or hip raise.",
		[10]float64{-90, -90, 135, 42, 135, 42, 90, 90, 90, 90},
		shoulders,
		[]plansv1.BodyPoint{nearFoot, farFoot},
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_MAT, plansv1.BodyPoint_BODY_POINT_UNSPECIFIED}},
	},
	{
		"bridge_top", "The top of a glute bridge or hip raise: shoulders on the floor, shins upright, body straight from the shoulders to the knees. Move between it and lying_knees_bent; for a single-leg bridge, straighten the far leg.",
		[10]float64{-64, -90, 116, 0, 116, 0, 90, 90, 90, 90},
		shoulders,
		[]plansv1.BodyPoint{nearFoot, farFoot},
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_MAT, plansv1.BodyPoint_BODY_POINT_UNSPECIFIED}},
	},
	{
		"dead_bug", "The start of a dead bug: on the back, hips and knees bent to 90 degrees, arms straight up. Extend the near arm back and the far leg forward from it.",
		[10]float64{-90, -90, 180, 90, 180, 90, 180, 180, 180, 180},
		hips, nil,
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_MAT, plansv1.BodyPoint_BODY_POINT_UNSPECIFIED}},
	},
	{
		"lying_on_front", "Lying face down, arms stretched forward past the head. The start of superman holds.",
		[10]float64{90, 90, -90, -90, -90, -90, 90, 90, 90, 90},
		hips, nil,
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_MAT, plansv1.BodyPoint_BODY_POINT_UNSPECIFIED}},
	},
	{
		"hanging", "Hanging from a bar with straight arms: the bottom of a pull-up or a dead hang.",
		[10]float64{180, 180, 0, 0, 0, 0, 180, 180, 180, 180},
		nearHand,
		[]plansv1.BodyPoint{farHand},
		[]prop{{plansv1.AnimationPropKind_ANIMATION_PROP_KIND_BAR, nearHand}},
	},
}

func (s *ExerciseService) ListStockPoses(context.Context, *plansv1.ListStockPosesRequest) (*plansv1.ListStockPosesResponse, error) {
	poses := make([]*plansv1.StockPose, len(stockPoses))
	for i, p := range stockPoses {
		a := p.angles
		var props []*plansv1.AnimationProp
		for _, pr := range p.props {
			props = append(props, plansv1.AnimationProp_builder{Kind: pr.kind, Point: pr.point}.Build())
		}
		poses[i] = plansv1.StockPose_builder{
			Name:        p.name,
			Description: p.description,
			Pose: plansv1.Pose_builder{
				TorsoDegrees:        a[0],
				HeadDegrees:         a[1],
				NearThighDegrees:    a[2],
				NearShinDegrees:     a[3],
				FarThighDegrees:     a[4],
				FarShinDegrees:      a[5],
				NearUpperArmDegrees: a[6],
				NearForearmDegrees:  a[7],
				FarUpperArmDegrees:  a[8],
				FarForearmDegrees:   a[9],
			}.Build(),
			Anchor:  p.anchor,
			Planted: p.planted,
			Props:   props,
		}.Build()
	}
	return plansv1.ListStockPosesResponse_builder{Poses: poses}.Build(), nil
}
