package plan

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	plansv1 "github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/plans/v1/plansv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/html"
	"github.com/johanbrandhorst/trail-tools/internal/plans"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

var _ plansv1connect.WorkoutShareServiceHandler = (*Service)(nil)

const (
	// SharePath is the page of shared workouts, with the share token in the
	// link's fragment, so it isn't sent to the server or logged.
	SharePath = "/share/workouts"
	// ShareDownloadPath is where a shared workout's files are downloaded,
	// with the share token in the ShareTokenHeader header rather than the
	// path, which is logged.
	ShareDownloadPath = "/share/workout-files/"
	// ShareTokenHeader holds the share token of downloads.
	ShareTokenHeader = "X-Share-Token"
	// maxSharedRoutePoints is the most points of a shared route's path.
	maxSharedRoutePoints = 2000
)

// ShareRepository shares workouts.
type ShareRepository interface {
	ShareWorkout(ctx context.Context, userID, workoutID pgtype.UUID) (string, error)
	UnshareWorkout(ctx context.Context, userID, workoutID pgtype.UUID) error
	GetSharedWorkout(ctx context.Context, token string) (*plans.Workout, pgtype.UUID, error)
}

// SetSharing lets the service share workouts, with links under baseURL.
func (s *Service) SetSharing(shares ShareRepository, baseURL string) {
	s.shares, s.baseURL = shares, baseURL
}

func (s *Service) shareURL(token string) string {
	if token == "" || s.baseURL == "" {
		return ""
	}
	return s.baseURL + html.TokenURL(SharePath, token)
}

func (s *Service) ShareWorkout(ctx context.Context, req *plansv1.ShareWorkoutRequest) (*plansv1.ShareWorkoutResponse, error) {
	userID, workoutID, err := s.shareTarget(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	token, err := s.shares.ShareWorkout(ctx, userID, workoutID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to share workout")
	}
	return plansv1.ShareWorkoutResponse_builder{ShareUrl: s.shareURL(token)}.Build(), nil
}

func (s *Service) UnshareWorkout(ctx context.Context, req *plansv1.UnshareWorkoutRequest) (*plansv1.UnshareWorkoutResponse, error) {
	userID, workoutID, err := s.shareTarget(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.shares.UnshareWorkout(ctx, userID, workoutID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to unshare workout")
	}
	return &plansv1.UnshareWorkoutResponse{}, nil
}

// shareTarget returns the user and the workout of a share request.
func (s *Service) shareTarget(ctx context.Context, id string) (pgtype.UUID, pgtype.UUID, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if s.shares == nil {
		return pgtype.UUID{}, pgtype.UUID{}, connect.NewError(connect.CodeUnimplemented, "sharing workouts isn't set up on this server")
	}
	workoutID, err := store.StringToUUID(id)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, invalidArgument("invalid workout ID: %v", err)
	}
	return userID, workoutID, nil
}

// sharedWorkout returns the workout shared with a token, with its owner and
// route, if it has one.
func (s *Service) sharedWorkout(ctx context.Context, token string) (*plans.Workout, pgtype.UUID, *routes.Route, error) {
	if s.shares == nil {
		return nil, pgtype.UUID{}, nil, store.ErrNotFound
	}
	w, owner, err := s.shares.GetSharedWorkout(ctx, token)
	if err != nil {
		return nil, pgtype.UUID{}, nil, err
	}
	if !w.RouteID.Valid {
		return w, owner, nil, nil
	}
	r, err := s.routes.GetRoute(ctx, owner, w.RouteID)
	if errors.Is(err, store.ErrNotFound) {
		return w, owner, nil, nil
	}
	if err != nil {
		return nil, pgtype.UUID{}, nil, err
	}
	return w, owner, r, nil
}

func (s *Service) GetSharedWorkout(ctx context.Context, req *plansv1.GetSharedWorkoutRequest) (*plansv1.GetSharedWorkoutResponse, error) {
	// Shared workouts don't require signing in.
	w, owner, r, err := s.sharedWorkout(ctx, req.GetToken())
	if errors.Is(err, store.ErrNotFound) {
		return nil, connect.NewError(connect.CodeNotFound, "this workout doesn't exist or is no longer shared")
	}
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get shared workout")
	}
	// The owner's own: the plan, the activity that completed the workout,
	// how they logged it, and their equipment.
	w.PlanID, w.CompletedActivityID, w.Log, w.EquipmentIssues = pgtype.UUID{}, pgtype.UUID{}, nil, nil
	pw := workoutToProto(w)
	resp := plansv1.GetSharedWorkoutResponse_builder{Workout: pw}
	if r != nil {
		points, _ := r.Path()
		resp.Route = plansv1.SharedRoute_builder{
			Name:   r.Name,
			Stats:  route.StatsToProto(r.Stats),
			Points: route.PathToProto(points, maxSharedRoutePoints),
		}.Build()
	}
	if len(w.Exercises) > 0 {
		ids := make([]pgtype.UUID, len(w.Exercises))
		for i, e := range w.Exercises {
			ids[i] = e.ExerciseID
		}
		library, err := s.plans.FindExercises(ctx, owner, ids)
		if err != nil {
			return nil, s.toConnectError(ctx, err, "failed to get the workout's exercises")
		}
		seen := map[pgtype.UUID]bool{}
		for _, id := range ids {
			e, ok := library[id]
			if !ok || seen[id] {
				continue
			}
			seen[id] = true
			animations, err := animationsToProto(e.Animations)
			if err != nil {
				return nil, s.toConnectError(ctx, err, "failed to read exercise")
			}
			resp.Exercises = append(resp.Exercises, exerciseToProto(e, animations))
		}
	}
	return resp.Build(), nil
}

// ShareDownloadHandler serves the files of shared workouts without signing
// in, at ShareDownloadPath: route.gpx and route.fit, the route as GPX and
// as a FIT course, and workout.fit, a strength workout for a watch. The
// share token is in the ShareTokenHeader header.
func (s *Service) ShareDownloadHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		file := strings.TrimPrefix(r.URL.Path, ShareDownloadPath)
		workout, _, rt, err := s.sharedWorkout(r.Context(), r.Header.Get(ShareTokenHeader))
		if errors.Is(err, store.ErrNotFound) {
			http.Error(w, "this workout doesn't exist or is no longer shared", http.StatusNotFound)
			return
		}
		if err != nil {
			s.report.Report(r.Context(), "Failed to get shared workout", err)
			http.Error(w, "failed to get the workout", http.StatusInternalServerError)
			return
		}
		switch file {
		case "route.gpx", "route.fit":
			if rt == nil {
				http.Error(w, "the workout has no route", http.StatusNotFound)
				return
			}
			route.WriteRouteFile(w, r, s.report, rt, file == "route.fit")
		case "workout.fit":
			writeStrengthFIT(w, workout)
		default:
			http.NotFound(w, r)
		}
	})
}

// workoutProto converts one of the user's workouts, with its share link.
func (s *Service) workoutProto(w *plans.Workout) *plansv1.Workout {
	pw := workoutToProto(w)
	pw.SetShareUrl(s.shareURL(w.ShareToken))
	return pw
}
