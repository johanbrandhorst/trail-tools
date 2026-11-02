// Package lactate stores the user's blood lactate tests, and finds their
// aerobic threshold in them.
package lactate

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/lactate/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// AerobicThresholdMmolPerLiter is the blood lactate at the aerobic threshold,
// the definition Uphill Athlete uses.
const AerobicThresholdMmolPerLiter = 2.0

// AnaerobicThresholdMmolPerLiter is the blood lactate at the anaerobic
// threshold by its classic fixed definition, the onset of blood lactate
// accumulation.
const AnaerobicThresholdMmolPerLiter = 4.0

// minDmaxStages is the fewest stages Dmax fits a curve to: a cubic needs four
// points, and fewer stages don't describe the curve's bend.
const minDmaxStages = 4

// minDmaxBendMmolPerLiter is how far below the straight line between the
// first and last stage the fitted curve must bend for Dmax to mean anything,
// in mmol/L. Lactate that rises in a straight line, or falls, has no Dmax,
// and less is within a meter's error.
const minDmaxBendMmolPerLiter = 0.2

// Test is a blood lactate test.
type Test struct {
	ID     pgtype.UUID
	UserID pgtype.UUID
	Date   time.Time
	Notes  string
	// The resting blood lactate in mmol/L, 0 if not measured.
	RestingLactateMmolPerLiter float64
	// The stages, in the order they were run.
	Stages     []Stage
	CreateTime time.Time
	UpdateTime time.Time
}

// Stage is a stage of a test: a heart rate held for a few minutes, and the
// blood lactate measured at its end.
type Stage struct {
	HeartRateBPM        int32
	LactateMmolPerLiter float64
}

// LowestLactate returns the lowest reading of the test, at rest or of a
// stage, in mmol/L, or 0 if it has none.
func (t *Test) LowestLactate() float64 {
	lowest := t.RestingLactateMmolPerLiter
	for _, s := range t.Stages {
		if lowest == 0 || s.LactateMmolPerLiter < lowest {
			lowest = s.LactateMmolPerLiter
		}
	}
	return lowest
}

// HeartRateAt returns the heart rate at which the stages' lactate first rises
// to mmolPerLiter, interpolated between the stage below it and the stage
// reaching it, or 0 if no stage rises to it. Lactate often dips in the first
// stages after the warm-up, so a stage above it followed by one below doesn't
// count.
func (t *Test) HeartRateAt(mmolPerLiter float64) int32 {
	for i := 1; i < len(t.Stages); i++ {
		below, reached := t.Stages[i-1], t.Stages[i]
		if below.LactateMmolPerLiter >= mmolPerLiter || reached.LactateMmolPerLiter < mmolPerLiter {
			continue
		}
		f := (mmolPerLiter - below.LactateMmolPerLiter) / (reached.LactateMmolPerLiter - below.LactateMmolPerLiter)
		return int32(math.Round(float64(below.HeartRateBPM) + f*float64(reached.HeartRateBPM-below.HeartRateBPM)))
	}
	return 0
}

// Dmax returns the anaerobic threshold by the Dmax method: the heart rate
// where a cubic fitted to the stages is furthest below the straight line from
// the first stage's heart rate to the last's, and the fitted lactate there.
// It reports false for tests of fewer than minDmaxStages stages, or whose
// curve doesn't bend upward by at least minDmaxBendMmolPerLiter.
//
// Unlike a fixed lactate, Dmax adapts to the user's baseline, but it depends
// on where the test starts and stops: stopping a stage early or late moves
// it.
func (t *Test) Dmax() (heartRateBPM int32, mmolPerLiter float64, ok bool) {
	if len(t.Stages) < minDmaxStages {
		return 0, 0, false
	}
	first, last := float64(t.Stages[0].HeartRateBPM), float64(t.Stages[len(t.Stages)-1].HeartRateBPM)
	span := last - first
	if span <= 0 {
		return 0, 0, false
	}
	// The heart rates are scaled to 0 to 1, so the fit is well conditioned.
	xs, ys := make([]float64, len(t.Stages)), make([]float64, len(t.Stages))
	for i, s := range t.Stages {
		xs[i], ys[i] = (float64(s.HeartRateBPM)-first)/span, s.LactateMmolPerLiter
	}
	coefficients, ok := fitPolynomial(xs, ys, 3)
	if !ok {
		return 0, 0, false
	}
	fit := func(x float64) float64 {
		return coefficients[0] + x*(coefficients[1]+x*(coefficients[2]+x*coefficients[3]))
	}
	start, end := fit(0), fit(1)
	// The furthest point vertically is also the furthest perpendicularly,
	// as the line is straight. Searching every tenth of a bpm is precise
	// enough, and cheap.
	steps := int(math.Ceil(span * 10))
	bestX, bestDistance := 0.0, 0.0
	for i := 1; i < steps; i++ {
		x := float64(i) / float64(steps)
		if distance := start + x*(end-start) - fit(x); distance > bestDistance {
			bestX, bestDistance = x, distance
		}
	}
	if bestDistance < minDmaxBendMmolPerLiter {
		return 0, 0, false
	}
	return int32(math.Round(first + bestX*span)), fit(bestX), true
}

// fitPolynomial fits a polynomial of a degree to points by least squares,
// and returns its coefficients, lowest degree first. It reports false if the
// points don't determine it.
func fitPolynomial(xs, ys []float64, degree int) ([]float64, bool) {
	n := degree + 1
	// The normal equations, as an augmented matrix.
	m := make([][]float64, n)
	for row := range m {
		m[row] = make([]float64, n+1)
		for col := range n {
			for _, x := range xs {
				m[row][col] += math.Pow(x, float64(row+col))
			}
		}
		for i, x := range xs {
			m[row][n] += ys[i] * math.Pow(x, float64(row))
		}
	}
	// Gaussian elimination with partial pivoting.
	for col := range n {
		pivot := col
		for row := col + 1; row < n; row++ {
			if math.Abs(m[row][col]) > math.Abs(m[pivot][col]) {
				pivot = row
			}
		}
		if math.Abs(m[pivot][col]) < 1e-12 {
			return nil, false
		}
		m[col], m[pivot] = m[pivot], m[col]
		for row := range n {
			if row == col {
				continue
			}
			factor := m[row][col] / m[col][col]
			for k := col; k <= n; k++ {
				m[row][k] -= factor * m[col][k]
			}
		}
	}
	coefficients := make([]float64, n)
	for i := range n {
		coefficients[i] = m[i][n] / m[i][i]
	}
	return coefficients, true
}

// Repository stores lactate tests.
type Repository struct {
	log *slog.Logger
	db  *store.DB
	q   *gen.Queries
}

// NewRepository creates a new Repository from the provided logger and
// database.
func NewRepository(log *slog.Logger, db *store.DB) (*Repository, error) {
	return &Repository{
		log: log,
		db:  db,
		q:   gen.New(db),
	}, nil
}

// CreateTest stores a new test.
func (r *Repository) CreateTest(ctx context.Context, test *Test) (*Test, error) {
	heartRates, lactates := stageColumns(test.Stages)
	dbTest, err := r.q.CreateLactateTest(ctx, &gen.CreateLactateTestParams{
		UserID:                     test.UserID,
		Date:                       store.DateToPG(test.Date),
		Notes:                      test.Notes,
		RestingLactateMmolPerLiter: restingToDB(test.RestingLactateMmolPerLiter),
		StageHeartRatesBpm:         heartRates,
		StageLactatesMmolPerLiter:  lactates,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create lactate test: %w", err)
	}
	return testFromDB(dbTest), nil
}

// GetTest gets one of the user's tests.
func (r *Repository) GetTest(ctx context.Context, userID, testID pgtype.UUID) (*Test, error) {
	dbTest, err := r.q.GetLactateTest(ctx, &gen.GetLactateTestParams{ID: testID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get lactate test: %w", err)
	}
	return testFromDB(dbTest), nil
}

// ListTests lists the user's tests, the latest first.
func (r *Repository) ListTests(ctx context.Context, userID pgtype.UUID) ([]*Test, error) {
	dbTests, err := r.q.ListLactateTests(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list lactate tests: %w", err)
	}
	tests := make([]*Test, len(dbTests))
	for i, dbTest := range dbTests {
		tests[i] = testFromDB(dbTest)
	}
	return tests, nil
}

// UpdateTest replaces a test.
func (r *Repository) UpdateTest(ctx context.Context, test *Test) (*Test, error) {
	heartRates, lactates := stageColumns(test.Stages)
	dbTest, err := r.q.UpdateLactateTest(ctx, &gen.UpdateLactateTestParams{
		ID:                         test.ID,
		UserID:                     test.UserID,
		Date:                       store.DateToPG(test.Date),
		Notes:                      test.Notes,
		RestingLactateMmolPerLiter: restingToDB(test.RestingLactateMmolPerLiter),
		StageHeartRatesBpm:         heartRates,
		StageLactatesMmolPerLiter:  lactates,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update lactate test: %w", err)
	}
	return testFromDB(dbTest), nil
}

// DeleteTest deletes one of the user's tests.
func (r *Repository) DeleteTest(ctx context.Context, userID, testID pgtype.UUID) error {
	n, err := r.q.DeleteLactateTest(ctx, &gen.DeleteLactateTestParams{ID: testID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete lactate test: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

// stageColumns returns the stages' heart rates and lactates, the columns
// they're stored in.
func stageColumns(stages []Stage) (heartRates []int32, lactates []float64) {
	heartRates, lactates = make([]int32, len(stages)), make([]float64, len(stages))
	for i, s := range stages {
		heartRates[i], lactates[i] = s.HeartRateBPM, s.LactateMmolPerLiter
	}
	return heartRates, lactates
}

func restingToDB(mmolPerLiter float64) pgtype.Float8 {
	return pgtype.Float8{Float64: mmolPerLiter, Valid: mmolPerLiter > 0}
}

func testFromDB(t *gen.LactateTest) *Test {
	stages := make([]Stage, len(t.StageHeartRatesBpm))
	for i := range stages {
		stages[i] = Stage{HeartRateBPM: t.StageHeartRatesBpm[i], LactateMmolPerLiter: t.StageLactatesMmolPerLiter[i]}
	}
	return &Test{
		ID:                         t.ID,
		UserID:                     t.UserID,
		Date:                       store.DateFromPG(t.Date),
		Notes:                      t.Notes,
		RestingLactateMmolPerLiter: t.RestingLactateMmolPerLiter.Float64,
		Stages:                     stages,
		CreateTime:                 t.CreateTime.Time,
		UpdateTime:                 t.UpdateTime.Time,
	}
}
