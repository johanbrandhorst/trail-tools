package plan

import (
	"math"
	"testing"

	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
)

// The segment lengths of the web app's stick figure, see
// web/helpers/animation.ts.
const (
	torsoLength    = 52
	thighLength    = 44
	shinLength     = 42
	upperArmLength = 30
	forearmLength  = 28
)

type point struct{ x, y float64 }

// along returns the point length from p in a direction, with y up.
func along(p point, degrees, length float64) point {
	r := degrees * math.Pi / 180
	return point{p.x + math.Sin(r)*length, p.y - math.Cos(r)*length}
}

// joints returns the points of a pose, with the hips at the origin.
func joints(a [10]float64) map[plansv1.BodyPoint]point {
	hips := point{}
	shoulders := along(hips, a[0], torsoLength)
	nearKnee := along(hips, a[2], thighLength)
	farKnee := along(hips, a[4], thighLength)
	nearElbow := along(shoulders, a[6], upperArmLength)
	farElbow := along(shoulders, a[8], upperArmLength)
	return map[plansv1.BodyPoint]point{
		plansv1.BodyPoint_BODY_POINT_HIPS:      hips,
		plansv1.BodyPoint_BODY_POINT_SHOULDERS: shoulders,
		plansv1.BodyPoint_BODY_POINT_NEAR_KNEE: nearKnee,
		plansv1.BodyPoint_BODY_POINT_FAR_KNEE:  farKnee,
		plansv1.BodyPoint_BODY_POINT_NEAR_FOOT: along(nearKnee, a[3], shinLength),
		plansv1.BodyPoint_BODY_POINT_FAR_FOOT:  along(farKnee, a[5], shinLength),
		plansv1.BodyPoint_BODY_POINT_NEAR_HAND: along(nearElbow, a[7], forearmLength),
		plansv1.BodyPoint_BODY_POINT_FAR_HAND:  along(farElbow, a[9], forearmLength),
	}
}

// TestStockPosesReachTheFloor checks that every planted point, and the
// anchor of poses on the floor, is within a few units of the floor: the
// lowest point of the figure. Points on a prop, and hanging poses, are left
// out.
func TestStockPosesReachTheFloor(t *testing.T) {
	t.Parallel()
	const toleranceUnits = 6
	names := map[string]bool{}
	for _, p := range stockPoses {
		if names[p.name] {
			t.Errorf("%s: duplicate name", p.name)
		}
		names[p.name] = true
		onProp := map[plansv1.BodyPoint]bool{}
		for _, pr := range p.props {
			onProp[pr.point] = true
		}
		if onProp[plansv1.BodyPoint_BODY_POINT_NEAR_HAND] {
			// Hanging from a bar.
			continue
		}
		points := joints(p.angles)
		floor := math.Inf(1)
		for _, pt := range points {
			floor = math.Min(floor, pt.y)
		}
		for _, bp := range append([]plansv1.BodyPoint{p.anchor}, p.planted...) {
			if onProp[bp] || bp == plansv1.BodyPoint_BODY_POINT_HIPS {
				continue
			}
			if height := points[bp].y - floor; height > toleranceUnits {
				t.Errorf("%s: %v is %.0f units above the floor", p.name, bp, height)
			}
		}
	}
}
