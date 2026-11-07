// Package lactate implements the LactateTestService.
package lactate

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	lactatev1 "github.com/johanbrandhorst/trail-tools/internal/gen/lactate/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/lactate/v1/lactatev1connect"
	"github.com/johanbrandhorst/trail-tools/internal/lactate"
	"github.com/johanbrandhorst/trail-tools/internal/protomask"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ lactatev1connect.LactateTestServiceHandler = (*Service)(nil)

const (
	// maxStages is the most stages a test can have. A test usually has fewer
	// than 15: stages 5 to 10 bpm apart from about 100 bpm.
	maxStages = 30
	// minHeartRateBPM and maxHeartRateBPM bound the heart rate of a stage.
	minHeartRateBPM, maxHeartRateBPM = 40, 230
	// minLactateMmolPerLiter and maxLactateMmolPerLiter bound a reading, as
	// handheld meters measure from about 0.3 to 25 mmol/L.
	minLactateMmolPerLiter, maxLactateMmolPerLiter = 0.1, 30
	// lowestPlusMmolPerLiter is how far above the lowest reading the
	// baseline-relative definition of the aerobic threshold is.
	lowestPlusMmolPerLiter = 1.0
	// fixed2_5MmolPerLiter is the lactate of the 2.5 mmol/L definition.
	fixed2_5MmolPerLiter = 2.5
)

// TestRepository stores lactate tests, and the links to record them.
type TestRepository interface {
	CreateTest(ctx context.Context, test *lactate.Test) (*lactate.Test, error)
	GetTest(ctx context.Context, userID, testID pgtype.UUID) (*lactate.Test, error)
	ListTests(ctx context.Context, userID pgtype.UUID) ([]*lactate.Test, error)
	UpdateTest(ctx context.Context, test *lactate.Test) (*lactate.Test, error)
	DeleteTest(ctx context.Context, userID, testID pgtype.UUID) error
	CreateShare(ctx context.Context, userID pgtype.UUID, tokenSHA256 []byte, expireTime time.Time, workoutID pgtype.UUID) (*lactate.Share, error)
	ListShares(ctx context.Context, userID pgtype.UUID) ([]*lactate.Share, error)
	DeleteShare(ctx context.Context, userID, shareID pgtype.UUID) error
	GetShareByToken(ctx context.Context, tokenSHA256 []byte) (*lactate.Share, error)
	OpenShare(ctx context.Context, shareID pgtype.UUID, recorderSHA256 []byte) (bool, error)
	GetRecording(ctx context.Context, recorderSHA256 []byte) (*lactate.Recording, error)
	SetShareTest(ctx context.Context, shareID, testID pgtype.UUID) error
}

// Service implements API handlers for the lactate test service.
type Service struct {
	log    *slog.Logger
	report *errreport.Reporter
	tests  TestRepository
	// The public URL of the app, which recording links start with.
	baseURL string
	nowFn   func() time.Time
}

// NewService creates a new Service.
func NewService(log *slog.Logger, report *errreport.Reporter, tests TestRepository, baseURL string) *Service {
	return &Service{log: log, report: report, tests: tests, baseURL: baseURL, nowFn: time.Now}
}

func (s *Service) CreateLactateTest(ctx context.Context, req *lactatev1.CreateLactateTestRequest) (*lactatev1.CreateLactateTestResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	test, err := testFromProto(userID, req.GetTest())
	if err != nil {
		return nil, err
	}
	created, err := s.tests.CreateTest(ctx, test)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to create lactate test")
	}
	return lactatev1.CreateLactateTestResponse_builder{Test: TestToProto(created)}.Build(), nil
}

func (s *Service) GetLactateTest(ctx context.Context, req *lactatev1.GetLactateTestRequest) (*lactatev1.GetLactateTestResponse, error) {
	test, err := s.getTest(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	return lactatev1.GetLactateTestResponse_builder{Test: TestToProto(test)}.Build(), nil
}

func (s *Service) ListLactateTests(ctx context.Context, _ *lactatev1.ListLactateTestsRequest) (*lactatev1.ListLactateTestsResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	tests, err := s.tests.ListTests(ctx, userID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to list lactate tests")
	}
	protoTests := make([]*lactatev1.LactateTest, len(tests))
	for i, test := range tests {
		protoTests[i] = TestToProto(test)
	}
	return lactatev1.ListLactateTestsResponse_builder{Tests: protoTests}.Build(), nil
}

func (s *Service) UpdateLactateTest(ctx context.Context, req *lactatev1.UpdateLactateTestRequest) (*lactatev1.UpdateLactateTestResponse, error) {
	existing, err := s.getTest(ctx, req.GetTest().GetId())
	if err != nil {
		return nil, err
	}
	merged := TestToProto(existing)
	if err := protomask.Apply(merged, req.GetTest(), req.GetUpdateMask(), "id", "lowest_lactate_mmol_per_liter", "aerobic_threshold_heart_rate_bpm", "threshold_estimates", "create_time", "update_time"); err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	test, err := testFromProto(existing.UserID, merged)
	if err != nil {
		return nil, err
	}
	test.ID = existing.ID
	updated, err := s.tests.UpdateTest(ctx, test)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to update lactate test")
	}
	return lactatev1.UpdateLactateTestResponse_builder{Test: TestToProto(updated)}.Build(), nil
}

func (s *Service) DeleteLactateTest(ctx context.Context, req *lactatev1.DeleteLactateTestRequest) (*lactatev1.DeleteLactateTestResponse, error) {
	test, err := s.getTest(ctx, req.GetId())
	if err != nil {
		return nil, err
	}
	if err := s.tests.DeleteTest(ctx, test.UserID, test.ID); err != nil {
		return nil, s.toConnectError(ctx, err, "failed to delete lactate test")
	}
	return lactatev1.DeleteLactateTestResponse_builder{}.Build(), nil
}

func (s *Service) getTest(ctx context.Context, id string) (*lactate.Test, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	testID, err := store.StringToUUID(id)
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "invalid lactate test ID: %v", err).WithCause(err)
	}
	test, err := s.tests.GetTest(ctx, userID, testID)
	if err != nil {
		return nil, s.toConnectError(ctx, err, "failed to get lactate test")
	}
	return test, nil
}

func testFromProto(userID pgtype.UUID, t *lactatev1.LactateTest) (*lactate.Test, error) {
	date, err := store.ParseDate(t.GetDate())
	if err != nil {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "date must be YYYY-MM-DD: %v", err).WithCause(err)
	}
	resting := t.GetRestingLactateMmolPerLiter()
	if resting != 0 && !validLactate(resting) {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "resting_lactate_mmol_per_liter must be from %g to %g mmol/L, or 0 if not measured", float64(minLactateMmolPerLiter), float64(maxLactateMmolPerLiter))
	}
	if len(t.GetStages()) > maxStages {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "a test can have at most %d stages", maxStages)
	}
	stages := make([]lactate.Stage, len(t.GetStages()))
	for i, stage := range t.GetStages() {
		heartRate, mmol := stage.GetHeartRateBpm(), stage.GetLactateMmolPerLiter()
		if heartRate < minHeartRateBPM || heartRate > maxHeartRateBPM {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "stage %d: heart_rate_bpm must be from %d to %d", i+1, minHeartRateBPM, maxHeartRateBPM)
		}
		if !validLactate(mmol) {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "stage %d: lactate_mmol_per_liter must be from %g to %g mmol/L", i+1, float64(minLactateMmolPerLiter), float64(maxLactateMmolPerLiter))
		}
		if i > 0 && heartRate <= stages[i-1].HeartRateBPM {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "stage %d: each stage must be at a higher heart_rate_bpm than the last; to measure a stage again, replace its reading", i+1)
		}
		stages[i] = lactate.Stage{HeartRateBPM: heartRate, LactateMmolPerLiter: mmol}
	}
	workoutID, err := workoutIDFromProto(t.GetWorkoutId())
	if err != nil {
		return nil, err
	}
	return &lactate.Test{
		UserID:                     userID,
		Date:                       date,
		Notes:                      strings.TrimSpace(t.GetNotes()),
		RestingLactateMmolPerLiter: resting,
		Stages:                     stages,
		WorkoutID:                  workoutID,
	}, nil
}

// workoutIDFromProto parses the ID of the workout a test is for, invalid if
// it's empty.
func workoutIDFromProto(id string) (pgtype.UUID, error) {
	if id == "" {
		return pgtype.UUID{}, nil
	}
	workoutID, err := store.StringToUUID(id)
	if err != nil {
		return pgtype.UUID{}, connect.Errorf(connect.CodeInvalidArgument, "invalid workout_id: %v", err).WithCause(err)
	}
	return workoutID, nil
}

func validLactate(mmolPerLiter float64) bool {
	return mmolPerLiter >= minLactateMmolPerLiter && mmolPerLiter <= maxLactateMmolPerLiter
}

// TestToProto converts a test to its API representation, with its
// thresholds.
func TestToProto(t *lactate.Test) *lactatev1.LactateTest {
	stages := make([]*lactatev1.LactateStage, len(t.Stages))
	for i, stage := range t.Stages {
		stages[i] = lactatev1.LactateStage_builder{
			HeartRateBpm:        stage.HeartRateBPM,
			LactateMmolPerLiter: stage.LactateMmolPerLiter,
		}.Build()
	}
	lowest := t.LowestLactate()
	estimate := func(definition lactatev1.ThresholdDefinition, mmolPerLiter float64) *lactatev1.ThresholdEstimate {
		return lactatev1.ThresholdEstimate_builder{
			Definition:          definition,
			LactateMmolPerLiter: mmolPerLiter,
			HeartRateBpm:        t.HeartRateAt(mmolPerLiter),
		}.Build()
	}
	estimates := []*lactatev1.ThresholdEstimate{
		estimate(lactatev1.ThresholdDefinition_THRESHOLD_DEFINITION_FIXED_2_MMOL, lactate.AerobicThresholdMmolPerLiter),
		estimate(lactatev1.ThresholdDefinition_THRESHOLD_DEFINITION_FIXED_2_5_MMOL, fixed2_5MmolPerLiter),
	}
	if lowest > 0 {
		estimates = append(estimates, estimate(lactatev1.ThresholdDefinition_THRESHOLD_DEFINITION_LOWEST_PLUS_1_MMOL, lowest+lowestPlusMmolPerLiter))
	}
	anaerobic := []*lactatev1.ThresholdEstimate{
		estimate(lactatev1.ThresholdDefinition_THRESHOLD_DEFINITION_FIXED_4_MMOL, lactate.AnaerobicThresholdMmolPerLiter),
	}
	if heartRate, mmol, ok := t.Dmax(); ok {
		anaerobic = append(anaerobic, lactatev1.ThresholdEstimate_builder{
			Definition:          lactatev1.ThresholdDefinition_THRESHOLD_DEFINITION_DMAX,
			LactateMmolPerLiter: math.Round(mmol*10) / 10,
			HeartRateBpm:        heartRate,
		}.Build())
	}
	return lactatev1.LactateTest_builder{
		Id:                             store.UUIDToString(t.ID),
		Date:                           store.FormatDate(t.Date),
		Notes:                          t.Notes,
		RestingLactateMmolPerLiter:     t.RestingLactateMmolPerLiter,
		Stages:                         stages,
		LowestLactateMmolPerLiter:      lowest,
		AerobicThresholdHeartRateBpm:   t.HeartRateAt(lactate.AerobicThresholdMmolPerLiter),
		ThresholdEstimates:             estimates,
		AnaerobicThresholdHeartRateBpm: t.HeartRateAt(lactate.AnaerobicThresholdMmolPerLiter),
		AnaerobicThresholdEstimates:    anaerobic,
		WorkoutId:                      store.UUIDToString(t.WorkoutID),
		CreateTime:                     timestamppb.New(t.CreateTime),
		UpdateTime:                     timestamppb.New(t.UpdateTime),
	}.Build()
}

func (s *Service) toConnectError(ctx context.Context, err error, msg string) error {
	if connectErr := (*connect.Error)(nil); errors.As(err, &connectErr) {
		return connectErr
	}
	if errors.Is(err, lactate.ErrWorkoutNotFound) {
		return connect.NewError(connect.CodeInvalidArgument, "workout_id: no such workout in your training plans; get the ID from TrainingPlanService.ListWorkouts")
	}
	if errors.Is(err, store.ErrNotFound) {
		return connect.NewError(connect.CodeNotFound, "lactate test not found")
	}
	s.report.Report(ctx, msg, err)
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}
