package mcp

import (
	"strings"
	"testing"

	plansv1 "github.com/bs-tools/trailrunn.ing/internal/gen/plans/v1"
)

func TestCheckRemovedFields(t *testing.T) {
	t.Parallel()
	md := (&plansv1.CreateWorkoutsRequest{}).ProtoReflect().Descriptor()
	for _, tc := range []struct {
		args, wantField string
	}{
		{`{"planId": "p", "workouts": [{"date": "2026-11-09", "run": {"targetDistanceMeters": 10000}}]}`, ""},
		// The fields moved into run, and agents use both spellings.
		{`{"workouts": [{"date": "2026-11-09"}, {"targetDistanceMeters": 10000}]}`, "targetDistanceMeters"},
		{`{"workouts": [{"route_id": "r"}]}`, "route_id"},
		// Fields that never existed are left for protojson to report.
		{`{"workouts": [{"nonsense": 1}]}`, ""},
		{`"not an object"`, ""},
	} {
		err := checkRemovedFields([]byte(tc.args), md)
		switch {
		case tc.wantField == "" && err != nil:
			t.Errorf("%s: got %v, want no error", tc.args, err)
		case tc.wantField != "" && (err == nil || !strings.Contains(err.Error(), "Workout no longer has the field "+tc.wantField)):
			t.Errorf("%s: got %v, want an error about %s", tc.args, err, tc.wantField)
		}
	}
}
