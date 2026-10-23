// Package raceplan implements the RacePlanService.
package raceplan

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	racesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/races/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/races/v1/racesv1connect"
	routesv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routes/v1"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/html"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"github.com/johanbrandhorst/trail-tools/internal/races"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
	"github.com/johanbrandhorst/trail-tools/internal/services/race"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ racesv1connect.RacePlanServiceHandler = (*Service)(nil)

type RacePlanRepository interface {
	GetRace(ctx context.Context, userID, raceID pgtype.UUID) (*races.Race, error)
	ListRaces(ctx context.Context, userID pgtype.UUID, onCalendarOnly bool) ([]*races.Race, error)
	CreateRacePlan(ctx context.Context, plan *races.RacePlan) (*races.RacePlan, error)
	GetRacePlan(ctx context.Context, userID, planID pgtype.UUID) (*races.RacePlan, error)
	GetRacePlanForRace(ctx context.Context, userID, raceID pgtype.UUID) (*races.RacePlan, error)
	GetSharedRacePlan(ctx context.Context, token string) (*races.RacePlan, *races.Race, error)
	ListRacePlans(ctx context.Context, userID pgtype.UUID) ([]*races.RacePlan, error)
	UpdateRacePlan(ctx context.Context, plan *races.RacePlan, sections []races.RacePlanSection) (*races.RacePlan, error)
	ShareRacePlan(ctx context.Context, userID, planID pgtype.UUID) (string, error)
	UnshareRacePlan(ctx context.Context, userID, planID pgtype.UUID) error
	UpdateRaceDay(ctx context.Context, planID pgtype.UUID, raceDay *races.RaceDay, fields races.RaceDayFields) (*races.RacePlan, error)
	SetCheckpointSplit(ctx context.Context, planID pgtype.UUID, split *races.CheckpointSplit) (*races.RacePlan, error)
	DeleteRacePlan(ctx context.Context, userID, planID pgtype.UUID) error
}

type RouteRepository interface {
	GetRoute(ctx context.Context, userID, routeID pgtype.UUID) (*routes.Route, error)
}

// Service implements API handlers for the race plan service.
type Service struct {
	log         *slog.Logger
	report      *errreport.Reporter
	repo        RacePlanRepository
	routes      RouteRepository
	liveTracker LiveTracker
	baseURL     string
	nowFn       func() time.Time
}

// NewService creates a new Service. baseURL is the public URL of the server,
// used to create share links.
func NewService(log *slog.Logger, report *errreport.Reporter, repo RacePlanRepository, routes RouteRepository, liveTracker LiveTracker, baseURL string) *Service {
	return &Service{
		log:         log,
		report:      report,
		repo:        repo,
		routes:      routes,
		liveTracker: liveTracker,
		baseURL:     strings.TrimSuffix(baseURL, "/"),
		nowFn:       time.Now,
	}
}

// SharePath is the shared race plan page. Its links carry the share token in
// the fragment, which browsers don't send, so it doesn't end up in request
// logs.
const SharePath = "/share/race-plans"

// maxSharedCoursePoints is the number of course points in shared plans.
const maxSharedCoursePoints = 1000

func (s *Service) CreateRacePlan(ctx context.Context, req *racesv1.CreateRacePlanRequest) (*racesv1.CreateRacePlanResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	raceID, err := store.StringToUUID(req.GetRaceId())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid race ID: %v", err).WithCause(err)
	}
	r, err := s.repo.GetRace(ctx, userID, raceID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race")
	}
	if !r.OnCalendar {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			"the race isn't on the user's calendar; add it with UpdateRace, setting on_calendar, if the user is running it")
	}
	seed := planFromProto(seedPlan(r))
	seed.UserID, seed.RaceID = userID, raceID
	stored, err := s.repo.CreateRacePlan(ctx, seed)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create race plan")
	}
	plan, analysis, err := s.planWithAnalysis(ctx, stored, r)
	if err != nil {
		return nil, err
	}
	return racesv1.CreateRacePlanResponse_builder{
		Plan:     plan,
		Race:     race.RaceToProto(r),
		Analysis: analysis,
	}.Build(), nil
}

func (s *Service) GetRacePlan(ctx context.Context, req *racesv1.GetRacePlanRequest) (*racesv1.GetRacePlanResponse, error) {
	stored, r, err := s.getPlan(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	plan, analysis, err := s.planWithAnalysis(ctx, stored, r)
	if err != nil {
		return nil, err
	}
	return racesv1.GetRacePlanResponse_builder{
		Plan:     plan,
		Race:     race.RaceToProto(r),
		Analysis: analysis,
	}.Build(), nil
}

func (s *Service) ListRacePlans(ctx context.Context, req *racesv1.ListRacePlansRequest) (*racesv1.ListRacePlansResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	plans, err := s.repo.ListRacePlans(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list race plans")
	}
	raceList, err := s.repo.ListRaces(ctx, userID, false)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list races")
	}
	raceByID := map[pgtype.UUID]*races.Race{}
	for _, r := range raceList {
		raceByID[r.ID] = r
	}
	var summaries []*racesv1.RacePlanSummary
	for _, stored := range plans {
		r, ok := raceByID[stored.RaceID]
		if !ok {
			continue
		}
		// Listing skips the course, which only affects segment elevations.
		analysis := Analyze(planToProto(stored, ""), raceInfo(r, nil))
		summaries = append(summaries, racesv1.RacePlanSummary_builder{
			Id:          store.UUIDToString(stored.ID),
			Race:        race.RaceToProto(r),
			RulesPassed: analysis.GetRulesPassed(),
			RulesTotal:  analysis.GetRulesTotal(),
			Shared:      stored.ShareToken != "",
			UpdateTime:  timestamppb.New(stored.UpdateTime),
		}.Build())
	}
	sort.SliceStable(summaries, func(i, j int) bool {
		return summaries[i].GetRace().GetDate() < summaries[j].GetRace().GetDate()
	})
	return racesv1.ListRacePlansResponse_builder{Plans: summaries}.Build(), nil
}

// outputFields are the fields of a race plan that are not sections. The race
// day is updated with UpdateRaceDay.
var outputFields = []protoreflect.Name{"id", "race_id", "share_url", "create_time", "update_time", "race_day"}

func (s *Service) UpdateRacePlan(ctx context.Context, req *racesv1.UpdateRacePlanRequest) (*racesv1.UpdateRacePlanResponse, error) {
	userID, planID, err := s.parsePlanID(ctx, req.GetPlan().GetId())
	if err != nil {
		return nil, err
	}
	fields, err := protomask.Fields(req.GetPlan().ProtoReflect().Descriptor(), req.GetUpdateMask(), outputFields...)
	if err != nil {
		var names []string
		for _, section := range races.AllRacePlanSections {
			names = append(names, string(section))
		}
		return nil, connect.Errorf(connect.CodeInvalidArgument, "%v; valid sections are %s", err, strings.Join(names, ", "))
	}
	sections := make([]races.RacePlanSection, len(fields))
	for i, fd := range fields {
		sections[i] = races.RacePlanSection(fd.Name())
	}
	// Only the updated sections are validated and written.
	partial := &racesv1.RacePlan{}
	if err := protomask.Apply(partial, req.GetPlan(), req.GetUpdateMask(), outputFields...); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err := validatePlan(partial); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	plan := planFromProto(partial)
	plan.ID, plan.UserID = planID, userID
	updated, err := s.repo.UpdateRacePlan(ctx, plan, sections)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update race plan")
	}
	r, err := s.repo.GetRace(ctx, userID, updated.RaceID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get race")
	}
	result, analysis, err := s.planWithAnalysis(ctx, updated, r)
	if err != nil {
		return nil, err
	}
	return racesv1.UpdateRacePlanResponse_builder{
		Plan:     result,
		Race:     race.RaceToProto(r),
		Analysis: analysis,
	}.Build(), nil
}

func (s *Service) DeleteRacePlan(ctx context.Context, req *racesv1.DeleteRacePlanRequest) (*racesv1.DeleteRacePlanResponse, error) {
	userID, planID, err := s.parsePlanID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.repo.DeleteRacePlan(ctx, userID, planID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete race plan")
	}
	return racesv1.DeleteRacePlanResponse_builder{}.Build(), nil
}

func (s *Service) ShareRacePlan(ctx context.Context, req *racesv1.ShareRacePlanRequest) (*racesv1.ShareRacePlanResponse, error) {
	userID, planID, err := s.parsePlanID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	token, err := s.repo.ShareRacePlan(ctx, userID, planID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to share race plan")
	}
	return racesv1.ShareRacePlanResponse_builder{
		ShareUrl: s.shareURL(token),
	}.Build(), nil
}

func (s *Service) UnshareRacePlan(ctx context.Context, req *racesv1.UnshareRacePlanRequest) (*racesv1.UnshareRacePlanResponse, error) {
	userID, planID, err := s.parsePlanID(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.repo.UnshareRacePlan(ctx, userID, planID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to unshare race plan")
	}
	return racesv1.UnshareRacePlanResponse_builder{}.Build(), nil
}

func (s *Service) GetSharedRacePlan(ctx context.Context, req *racesv1.GetSharedRacePlanRequest) (*racesv1.GetSharedRacePlanResponse, error) {
	// Shared plans don't require authentication.
	stored, r, err := s.repo.GetSharedRacePlan(ctx, req.GetToken())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "this race plan doesn't exist or is no longer shared")
		}
		return nil, s.toConnectError(ctx, err, "failed to get shared race plan")
	}
	course, err := s.course(ctx, r)
	if err != nil {
		return nil, err
	}
	plan := planToProto(stored, s.shareURL(stored.ShareToken))
	var coursePoints []*routesv1.RoutePoint
	if len(course) > 0 {
		coursePoints = route.PathToProto(course, maxSharedCoursePoints)
	}
	return racesv1.GetSharedRacePlanResponse_builder{
		Plan:     plan,
		Race:     sharedRace(r),
		Analysis: Analyze(plan, raceInfo(r, course)),
		Course:   coursePoints,
	}.Build(), nil
}

// sharedRace returns the race of a shared plan, with what crew need to know
// about it. The plan itself is shared in full, but the race's notes are the
// owner's own, and its IDs and calendar state only mean something to the
// owner: the course route, for one, can't be viewed by others.
func sharedRace(r *races.Race) *racesv1.Race {
	full := race.RaceToProto(r)
	return racesv1.Race_builder{
		Name:                full.GetName(),
		Date:                full.GetDate(),
		StartTime:           full.GetStartTime(),
		Location:            full.GetLocation(),
		WebsiteUrl:          full.GetWebsiteUrl(),
		DistanceMeters:      full.GetDistanceMeters(),
		ElevationGainMeters: full.GetElevationGainMeters(),
		CutoffSeconds:       full.GetCutoffSeconds(),
		Priority:            full.GetPriority(),
	}.Build()
}

// parsePlanID returns the authenticated user's ID and the parsed plan ID.
func (s *Service) parsePlanID(ctx context.Context, id string) (userID, planID pgtype.UUID, err error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return userID, planID, connect.NewError(connect.CodeUnauthenticated, "")
	}
	planID, err = store.StringToUUID(id)
	if err != nil {
		return userID, planID, connect.Errorf(connect.CodeInvalidArgument, "invalid race plan ID: %v", err).WithCause(err)
	}
	return userID, planID, nil
}

// getPlan gets one of the authenticated user's race plans and its race.
func (s *Service) getPlan(ctx context.Context, id string) (*races.RacePlan, *races.Race, error) {
	userID, planID, err := s.parsePlanID(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	stored, err := s.repo.GetRacePlan(ctx, userID, planID)
	if err != nil {
		return nil, nil, s.toConnectError(ctx, err, "failed to get race plan")
	}
	r, err := s.repo.GetRace(ctx, userID, stored.RaceID)
	if err != nil {
		return nil, nil, s.toConnectError(ctx, err, "failed to get race")
	}
	return stored, r, nil
}

// planWithAnalysis converts a stored plan and analyses it.
func (s *Service) planWithAnalysis(ctx context.Context, stored *races.RacePlan, r *races.Race) (*racesv1.RacePlan, *racesv1.RacePlanAnalysis, error) {
	plan := planToProto(stored, s.shareURL(stored.ShareToken))
	course, err := s.course(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	return plan, Analyze(plan, raceInfo(r, course)), nil
}

// course returns the race's course, or nil if it has none.
func (s *Service) course(ctx context.Context, r *races.Race) ([]geo.Point, error) {
	if !r.CourseRouteID.Valid {
		return nil, nil
	}
	courseRoute, err := s.routes.GetRoute(ctx, r.UserID, r.CourseRouteID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, nil
		}
		return nil, s.toConnectError(ctx, err, "failed to get course route")
	}
	points, _ := courseRoute.Path()
	return points, nil
}

func (s *Service) shareURL(token string) string {
	if token == "" {
		return ""
	}
	return s.baseURL + html.TokenURL(SharePath, token)
}

func raceInfo(r *races.Race, course []geo.Point) RaceInfo {
	return RaceInfo{
		DistanceMeters: r.DistanceMeters,
		StartTime:      r.StartTime,
		CutoffSeconds:  r.CutoffSeconds,
		Course:         course,
	}
}

// Limits on plan sizes, to keep plans reasonable.
const (
	maxCheckpoints = 100
	maxListItems   = 200
)

func validatePlan(plan *racesv1.RacePlan) error {
	if len(plan.GetCheckpoints()) > maxCheckpoints {
		return fmt.Errorf("at most %d checkpoints are allowed", maxCheckpoints)
	}
	for _, n := range []int{len(plan.GetGear()), len(plan.GetContingencies()), len(plan.GetRaceMorning()), len(plan.GetNutrition().GetProducts()), len(plan.GetMental().GetMantras())} {
		if n > maxListItems {
			return fmt.Errorf("at most %d items are allowed per list", maxListItems)
		}
	}
	for i, cp := range plan.GetCheckpoints() {
		if cp.GetDistanceMeters() < 0 || cp.GetPlannedSegmentSeconds() < 0 || cp.GetPlannedStopSeconds() < 0 || cp.GetCutoffSeconds() < 0 {
			return fmt.Errorf("checkpoint %d: distances and times must not be negative", i+1)
		}
		if len(cp.GetIntake()) > maxListItems {
			return fmt.Errorf("checkpoint %d: at most %d intakes are allowed", i+1, maxListItems)
		}
		for _, intake := range cp.GetIntake() {
			if intake.GetQuantity() < 0 {
				return fmt.Errorf("checkpoint %d: intake quantities must not be negative", i+1)
			}
		}
	}
	for i, c := range plan.GetContingencies() {
		if _, ok := scenarioFromProto[c.GetScenario()]; !ok {
			return fmt.Errorf("contingency %d: a scenario is required", i+1)
		}
	}
	return nil
}

// seedPlan returns a new plan for a race, with the finish and an empty entry
// for each core contingency scenario.
func seedPlan(r *races.Race) *racesv1.RacePlan {
	var contingencies []*racesv1.Contingency
	for _, scenario := range coreContingencies {
		contingencies = append(contingencies, racesv1.Contingency_builder{Scenario: scenario}.Build())
	}
	return racesv1.RacePlan_builder{
		Checkpoints: []*racesv1.Checkpoint{
			racesv1.Checkpoint_builder{
				Name:           "Finish",
				DistanceMeters: r.DistanceMeters,
				CutoffSeconds:  r.CutoffSeconds,
			}.Build(),
		},
		Contingencies: contingencies,
	}.Build()
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	var connectErr *connect.Error
	switch {
	case errors.As(err, &connectErr):
		return err
	case errors.Is(err, store.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, "not found")
	case errors.Is(err, races.ErrCheckpointNotFound):
		return connect.NewError(connect.CodeInvalidArgument, "the checkpoint doesn't exist in the plan")
	default:
		s.report.Report(ctx, msg, err)
		return connect.NewError(connect.CodeInternal, msg).WithCause(err)
	}
}
