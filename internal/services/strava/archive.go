package strava

import (
	"context"
	"errors"
	"math"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/bs-tools/trailrunn.ing/internal/activities"
	"github.com/bs-tools/trailrunn.ing/internal/authn"
	"github.com/bs-tools/trailrunn.ing/internal/fit"
	activitiesv1 "github.com/bs-tools/trailrunn.ing/internal/gen/activities/v1"
	stravav1 "github.com/bs-tools/trailrunn.ing/internal/gen/strava/v1"
	"github.com/bs-tools/trailrunn.ing/internal/geo"
	"github.com/bs-tools/trailrunn.ing/internal/store"
	"github.com/bs-tools/trailrunn.ing/internal/strava"
	"github.com/bs-tools/trailrunn.ing/internal/stravaarchive"
	"github.com/jackc/pgx/v5/pgtype"
)

// ArchiveRepository stores the activities imported from Strava archives.
type ArchiveRepository interface {
	StageStravaArchive(ctx context.Context, userID pgtype.UUID, activities []stravaarchive.Activity) ([]activities.ArchiveEntry, error)
	GetStravaArchiveActivity(ctx context.Context, userID pgtype.UUID, stravaID int64) (*stravaarchive.Activity, error)
	ImportArchiveActivity(ctx context.Context, userID pgtype.UUID, a *activities.ImportedArchiveActivity) (pgtype.UUID, error)
	DeleteStaleStravaArchives(ctx context.Context, before time.Time) error
}

// staleArchiveAge is how long the activities of an unfinished archive
// import are kept: the browser sends them within minutes, so a day means
// the user left. They're Strava data the user gave for the import only.
const staleArchiveAge = 24 * time.Hour

func (s *Service) StartStravaArchiveImport(ctx context.Context, req *stravav1.StartStravaArchiveImportRequest) (*stravav1.StartStravaArchiveImportResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	parsed, unreadable, err := stravaarchive.ParseActivities(strings.NewReader(req.GetActivitiesCsv()))
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			err.Error()+"; choose the zip of your data that Strava emailed you").WithCause(err)
	}
	// Any import cleans up the unfinished imports of all users, which needs
	// no scheduled job.
	if err := s.archive.DeleteStaleStravaArchives(ctx, time.Now().Add(-staleArchiveAge)); err != nil {
		s.report.Report(ctx, "Failed to delete stale Strava archive imports, deleting them with the next import", err)
	}
	entries, err := s.archive.StageStravaArchive(ctx, userID, parsed)
	if err != nil {
		return nil, s.internalError(ctx, "failed to start the Strava archive import", err)
	}
	resp := stravav1.StartStravaArchiveImportResponse_builder{
		AlreadyImportedCount: int32(max(0, len(parsed)-len(entries))),
		UnreadableCount:      int32(unreadable),
	}
	for _, e := range entries {
		resp.Activities = append(resp.Activities, stravav1.StravaArchiveActivity_builder{
			StravaId: strconv.FormatInt(e.StravaID, 10),
			Name:     e.Name,
			Filename: e.Filename,
		}.Build())
	}
	return resp.Build(), nil
}

func (s *Service) ImportStravaArchiveActivity(ctx context.Context, req *stravav1.ImportStravaArchiveActivityRequest) (*stravav1.ImportStravaArchiveActivityResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	stravaID, err := strconv.ParseInt(req.GetStravaId(), 10, 64)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid strava_id: %v", err).WithCause(err)
	}
	a, err := s.archive.GetStravaArchiveActivity(ctx, userID, stravaID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, connect.Errorf(connect.CodeFailedPrecondition,
			"activity %d isn't being imported: it was imported already, or the import is over a day old; start the import again", stravaID)
	}
	if err != nil {
		return nil, s.internalError(ctx, "failed to get the archive's activity", err)
	}
	imported, unreadable := s.importedActivity(ctx, a, req.GetFile())
	id, err := s.archive.ImportArchiveActivity(ctx, userID, imported)
	if err != nil {
		return nil, s.internalError(ctx, "failed to import the archive's activity", err)
	}
	return stravav1.ImportStravaArchiveActivityResponse_builder{
		ActivityId:     store.UUIDToString(id),
		FileUnreadable: unreadable,
	}.Build(), nil
}

// importedActivity returns an activity of an archive to store, with the
// samples of its file, if it has one, and whether the file couldn't be
// read. Its sport type is the archive's, but for those its file tells
// apart, like trail runs from runs, and its local date is from the time
// zone its file says, or else its position: the archive only gives times
// in UTC.
func (s *Service) importedActivity(ctx context.Context, a *stravaarchive.Activity, file []byte) (*activities.ImportedArchiveActivity, bool) {
	imported := &activities.ImportedArchiveActivity{Activity: a, SportType: a.SportType}
	var decoded *fit.Activity
	unreadable := false
	if len(file) > 0 && a.Filename != "" {
		var err error
		decoded, err = stravaarchive.DecodeFile(a.Filename, file)
		if err != nil {
			// The activity is still worth having, with Strava's stats.
			s.log.DebugContext(ctx, "Importing a Strava archive's activity without its unreadable file",
				"strava_id", a.StravaID, "error", err)
			decoded, unreadable = nil, true
		}
	}
	if decoded != nil {
		imported.SportType = refineSportType(a.SportType, decoded)
	}
	foot := strava.IsFootSport(imported.SportType)
	imported.AverageCadenceSpm = a.AverageCadence
	if foot {
		imported.AverageCadenceSpm *= 2
	}
	var offset time.Duration
	if decoded != nil {
		var streams *activitiesv1.ActivityStreams
		var bounds *geo.Bounds
		streams, bounds = activities.StreamsFromFIT(decoded, foot)
		imported.Streams, imported.Bounds = streams, bounds
		offset = utcOffset(decoded)
	}
	local := a.StartTime.Add(offset)
	imported.StartDateLocal = time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, time.UTC)
	return imported, unreadable
}

// FIT sports and sub sports that refine the archive's activity types.
const (
	fitSportRunning        = 1
	fitSportCycling        = 2
	fitSportAlpineSki      = 13
	fitSubSportTrail       = 7
	fitSubSportMountain    = 8
	fitSubSportBackcountry = 37
)

// refineSportType returns the sport type of an activity of the archive,
// whose types are coarser than Strava's sport types, telling apart the
// ones its file does.
func refineSportType(sportType string, a *fit.Activity) string {
	switch {
	case sportType == "Run" && a.Sport == fitSportRunning && a.SubSport == fitSubSportTrail:
		return "TrailRun"
	case sportType == "Ride" && a.Sport == fitSportCycling && a.SubSport == fitSubSportMountain:
		return "MountainBikeRide"
	case sportType == "AlpineSki" && a.Sport == fitSportAlpineSki && a.SubSport == fitSubSportBackcountry:
		return "BackcountrySki"
	}
	return sportType
}

// utcOffset returns an activity's offset from UTC: the device's, from a FIT
// file, or else the time zone its first position is roughly in, an hour for
// every 15 degrees of longitude, or zero without either.
func utcOffset(a *fit.Activity) time.Duration {
	if a.HasUTCOffset {
		return a.UTCOffset
	}
	for _, r := range a.Records {
		if r.HasPosition {
			return time.Duration(math.Round(r.Lng/15)) * time.Hour
		}
	}
	return 0
}
