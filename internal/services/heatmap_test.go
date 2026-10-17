package services_test

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	activitiesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/activities/v1"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// addTrackedActivity adds an activity with a summary polyline and streams
// with the positions of track, if any.
func (e *env) addTrackedActivity(stravaID int64, summary, track []geo.Point) pgtype.UUID {
	e.t.Helper()
	ctx := context.Background()
	id, err := e.activities.UpsertActivity(ctx, e.userID, &activities.ActivitySummary{
		StravaID:        stravaID,
		Name:            "Run",
		SportType:       "TrailRun",
		StartTime:       time.Date(2026, 9, 7, 8, 0, 0, 0, time.UTC),
		StartDateLocal:  time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC),
		Timezone:        "Europe/Zurich",
		SummaryPolyline: geo.EncodePolyline(summary),
		RawJSON:         []byte("{}"),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	streams := &activitiesv1.ActivityStreams{}
	for i, p := range track {
		streams.SetTimeSeconds(append(streams.GetTimeSeconds(), int32(i)))
		streams.SetLat(append(streams.GetLat(), p.Lat))
		streams.SetLng(append(streams.GetLng(), p.Lng))
	}
	if len(track) == 0 {
		streams.SetTimeSeconds([]int32{0, 1, 2})
	}
	var bounds *geo.Bounds
	if b, ok := geo.BoundsOf(track); ok {
		bounds = &b
	}
	if err := e.activities.StoreActivityDetail(ctx, e.userID, id, stravaID, nil, streams, bounds); err != nil {
		e.t.Fatal(err)
	}
	return id
}

// storedTrack returns the stored track of an activity, and whether it has
// been computed.
func (e *env) storedTrack(id pgtype.UUID) (string, bool) {
	e.t.Helper()
	var track pgtype.Text
	if err := e.db.QueryRow(context.Background(), `select track_polyline from strava_activity_streams where activity_id = $1`, id).Scan(&track); err != nil {
		e.t.Fatal(err)
	}
	return track.String, track.Valid
}

// heatmapTracks lists the heatmap tracks in a box around (46, 7).
func (e *env) heatmapTracks(tolerance float64) [][]geo.Point {
	e.t.Helper()
	resp, err := e.geo.ListHeatmapTracks(context.Background(), geov1.ListHeatmapTracksRequest_builder{
		Bounds:          geov1.BoundingBox_builder{South: 45.99, West: 6.99, North: 46.03, East: 7.01}.Build(),
		ToleranceMeters: tolerance,
	}.Build())
	if err != nil {
		e.t.Fatalf("ListHeatmapTracks: %v", err)
	}
	var tracks [][]geo.Point
	for _, polyline := range resp.GetPolylines() {
		points, err := geo.DecodePolyline(polyline)
		if err != nil {
			e.t.Fatal(err)
		}
		tracks = append(tracks, points)
	}
	return tracks
}

func TestHeatmapTracks(t *testing.T) {
	t.Parallel()
	e := newEnv(t)
	ctx := context.Background()

	// A run 2 km north from (46, 7) every 10 meters, with half a meter of
	// GPS jitter and a 30 meter detour east in the middle. Its summary
	// polyline is a straight line 0.001° (77 meters) east of it, to tell
	// them apart.
	const metersPerDegreeLng = 111320 * 0.6947 // At 46°N.
	var track []geo.Point
	for i := range 201 {
		east := 0.5 * float64(i%2)
		if d := math.Abs(float64(i - 100)); d < 10 {
			east += 3 * (10 - d)
		}
		track = append(track, geo.Point{Lat: 46 + float64(i)*10/111320, Lng: 7 + east/metersPerDegreeLng})
	}
	summary := []geo.Point{{Lat: 46, Lng: 7.001}, {Lat: track[200].Lat, Lng: 7.001}}
	run := e.addTrackedActivity(1, summary, track)
	// A treadmill run's streams have no positions, so its summary polyline
	// is its track.
	treadmill := []geo.Point{{Lat: 46.001, Lng: 7.005}, {Lat: 46.002, Lng: 7.005}}
	treadmillRun := e.addTrackedActivity(2, treadmill, nil)

	// The track is stored with the streams, without the jitter but with the
	// detour.
	stored, ok := e.storedTrack(run)
	if !ok {
		t.Fatal("the track wasn't stored with the streams")
	}
	points, err := geo.DecodePolyline(stored)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) < 3 || len(points) > 6 {
		t.Errorf("got a stored track of %d points, want the ends and the detour", len(points))
	}
	if stored, ok := e.storedTrack(treadmillRun); !ok || stored != "" {
		t.Errorf("got stored track %q for the treadmill run, want an empty one", stored)
	}

	// The tracks are drawn from the stored track, and the treadmill run's
	// summary polyline.
	checkDetailed := func() {
		t.Helper()
		tracks := e.heatmapTracks(5)
		if len(tracks) != 2 {
			t.Fatalf("got %d tracks, want 2", len(tracks))
		}
		// The longest first.
		run, treadmill := tracks[0], tracks[1]
		maxEast := 0.0
		for _, p := range run {
			maxEast = max(maxEast, (p.Lng-7)*metersPerDegreeLng)
		}
		if len(run) < 3 || maxEast < 25 || maxEast > 35 {
			t.Errorf("got track %v, want the detour 30 meters east", run)
		}
		if len(treadmill) != 2 || treadmill[0].Lng != 7.005 {
			t.Errorf("got track %v, want the treadmill run's summary polyline", treadmill)
		}
	}
	checkDetailed()

	// At low zoom, the summary polylines are drawn instead.
	summaryDrawn := func(tracks [][]geo.Point) bool {
		return len(tracks) == 2 && slices.ContainsFunc(tracks, func(track []geo.Point) bool {
			return len(track) == 2 && track[0].Lng == 7.001
		})
	}
	if tracks := e.heatmapTracks(activities.SummaryPrecisionMeters); !summaryDrawn(tracks) {
		t.Errorf("got tracks %v, want the summary polylines", tracks)
	}

	// Streams whose track isn't computed yet fall back to the summary
	// polyline. MoveToBlobs computes them.
	if _, err := e.db.Exec(ctx, `update strava_activity_streams set track_polyline = null where activity_id = $1`, run); err != nil {
		t.Fatal(err)
	}
	if tracks := e.heatmapTracks(5); !summaryDrawn(tracks) {
		t.Errorf("got tracks %v, want the summary polylines", tracks)
	}

	// The personal heatmap counts the tracks too.
	heatmap, err := e.geo.GetPersonalHeatmap(ctx, geov1.GetPersonalHeatmapRequest_builder{
		Bounds:         geov1.BoundingBox_builder{South: 45.99, West: 6.99, North: 46.03, East: 7.01}.Build(),
		CellSizeMeters: 100,
	}.Build())
	if err != nil {
		t.Fatalf("GetPersonalHeatmap: %v", err)
	}
	if heatmap.GetActivityCount() != 2 || len(heatmap.GetCells()) < 20 {
		t.Errorf("got a heatmap of %d activities and %d cells", heatmap.GetActivityCount(), len(heatmap.GetCells()))
	}
}
