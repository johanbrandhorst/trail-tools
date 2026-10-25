// Package mapdata implements the MapDataService, which manages the
// OpenStreetMap data in the routing graph.
package mapdata

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"regexp"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	routingv1 "github.com/johanbrandhorst/trail-tools/internal/gen/routing/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/routing/v1/routingv1connect"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/store"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var _ routingv1connect.MapDataServiceHandler = (*Service)(nil)

const (
	// maxAreaDegrees limits the size of an imported area, as the graph of a
	// large area doesn't fit in a small database and takes long to import.
	maxAreaDegrees = 3
	// recentImports is the number of imports listed.
	recentImports = 20
	// RefreshPath is where a scheduler asks to refresh the map data.
	RefreshPath = "/internal/map-data/refresh"
	// refreshAge is how old a region's import gets before it's imported
	// again. Geofabrik updates its extracts daily, but trails change
	// slowly, so a month keeps routing current without importing every day,
	// which takes the import job most of an hour and rewrites the region.
	refreshAge = 30 * 24 * time.Hour
	// storageLimitBytes is the storage of Neon's free plan, which the
	// database should stay within, as on the map data page.
	storageLimitBytes = 500_000_000
)

// regionName is the format of region names.
var regionName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,59}$`)

// ExtractFinder finds the OpenStreetMap extracts covering an area.
type ExtractFinder interface {
	Find(ctx context.Context, bounds geo.Bounds) ([]routing.Extract, error)
}

// ImportRunner runs a queued import, in the background or as a job.
type ImportRunner interface {
	Run(ctx context.Context, id pgtype.UUID) error
}

// Service implements API handlers for the map data service.
type Service struct {
	log      *slog.Logger
	report   *errreport.Reporter
	db       *store.DB
	extracts ExtractFinder
	runner   ImportRunner
	nowFn    func() time.Time
}

// NewService creates a new Service.
func NewService(log *slog.Logger, report *errreport.Reporter, db *store.DB, extracts ExtractFinder, runner ImportRunner) *Service {
	return &Service{log: log, report: report, db: db, extracts: extracts, runner: runner, nowFn: time.Now}
}

func (s *Service) GetMapData(ctx context.Context, req *routingv1.GetMapDataRequest) (*routingv1.GetMapDataResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	regions, err := routing.ListRegions(ctx, s.db)
	if err != nil {
		return nil, s.internalError(ctx, err)
	}
	imports, err := routing.ListImports(ctx, s.db, recentImports)
	if err != nil {
		return nil, s.internalError(ctx, err)
	}
	size, err := routing.DatabaseSize(ctx, s.db)
	if err != nil {
		return nil, s.internalError(ctx, err)
	}
	resp := routingv1.GetMapDataResponse_builder{DatabaseSizeBytes: size}.Build()
	protoRegions := make([]*routingv1.Region, len(regions))
	for i, r := range regions {
		protoRegions[i] = routingv1.Region_builder{
			Name:       r.Name,
			Area:       areaToProto(r.Bounds),
			EdgeCount:  int32(r.EdgeCount),
			SizeBytes:  r.SizeBytes,
			ImportTime: timestamppb.New(r.ImportTime),
		}.Build()
	}
	resp.SetRegions(protoRegions)
	protoImports := make([]*routingv1.Import, len(imports))
	for i, r := range imports {
		protoImports[i] = importToProto(r)
	}
	resp.SetImports(protoImports)
	return resp, nil
}

func (s *Service) FindExtracts(ctx context.Context, req *routingv1.FindExtractsRequest) (*routingv1.FindExtractsResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	bounds, err := areaFromProto(req.GetArea())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	extracts, err := s.findExtracts(ctx, bounds)
	if err != nil {
		return nil, err
	}
	return routingv1.FindExtractsResponse_builder{ExtractNames: extractNames(extracts)}.Build(), nil
}

func (s *Service) StartImport(ctx context.Context, req *routingv1.StartImportRequest) (*routingv1.StartImportResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	region := req.GetRegion()
	if !regionName.MatchString(region) {
		return nil, connect.NewError(connect.CodeInvalidArgument, "region must be lowercase letters, digits and dashes, e.g. central-switzerland")
	}
	bounds, err := areaFromProto(req.GetArea())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	// Check before looking up the extracts, though only creating the import
	// ensures no other import started meanwhile.
	active, err := routing.HasActiveImport(ctx, s.db)
	if err != nil {
		return nil, s.internalError(ctx, err)
	}
	if active {
		return nil, activeImportError()
	}
	extracts, err := s.findExtracts(ctx, bounds)
	if err != nil {
		return nil, err
	}
	userID, _ := authn.GetUserID(ctx)
	record, err := routing.CreateImport(ctx, s.db, region, bounds, extracts, userID)
	if err != nil {
		if errors.Is(err, routing.ErrActiveImport) {
			return nil, activeImportError()
		}
		return nil, s.internalError(ctx, err)
	}
	if err := s.runner.Run(ctx, record.ID); err != nil {
		s.report.Report(ctx, "Failed to start an import", err)
		if err := routing.FailImport(context.WithoutCancel(ctx), s.db, record.ID, "failed to start the import"); err != nil {
			s.report.Report(ctx, "Failed to record the failed import", err)
		}
		return nil, connect.NewError(connect.CodeUnavailable, "failed to start the import, try again later")
	}
	return routingv1.StartImportResponse_builder{Import: importToProto(record)}.Build(), nil
}

func (s *Service) DeleteRegion(ctx context.Context, req *routingv1.DeleteRegionRequest) (*routingv1.DeleteRegionResponse, error) {
	if err := requireAdmin(ctx); err != nil {
		return nil, err
	}
	if err := routing.DeleteRegion(ctx, s.db, req.GetName()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, "region not found")
		}
		return nil, s.internalError(ctx, err)
	}
	return routingv1.DeleteRegionResponse_builder{}.Build(), nil
}

// RefreshHandler returns a handler that imports again the region imported
// longest ago, if that was more than refreshAge ago, so routing follows
// changes to OpenStreetMap. It starts at most one import per call, as
// imports run one at a time, so a scheduler calling it daily refreshes each
// region about monthly. It responds 202 when it starts an import and 204
// otherwise. It must be protected by authorization.
func (s *Service) RefreshHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		started, err := s.refreshOldest(r.Context())
		switch {
		case err != nil:
			s.report.Report(r.Context(), "Failed to refresh the map data", err)
			http.Error(w, "failed to refresh the map data", http.StatusInternalServerError)
		case started:
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	})
}

// refreshOldest starts importing again the region imported longest ago, if
// it's due. It returns whether it started an import: not when no region is
// due or another import is queued or running, which the next call retries.
func (s *Service) refreshOldest(ctx context.Context) (bool, error) {
	regions, err := routing.ListRegions(ctx, s.db)
	if err != nil {
		return false, err
	}
	imported, err := routing.ImportedRegions(ctx, s.db)
	if err != nil {
		return false, err
	}
	var due *routing.Region
	for i, r := range regions {
		if imported[r.Name] && s.nowFn().Sub(r.ImportTime) >= refreshAge &&
			(due == nil || r.ImportTime.Before(due.ImportTime)) {
			due = &regions[i]
		}
	}
	if due == nil {
		return false, nil
	}
	active, err := routing.HasActiveImport(ctx, s.db)
	if err != nil || active {
		return false, err
	}
	size, err := routing.DatabaseSize(ctx, s.db)
	if err != nil {
		return false, err
	}
	// Importing a region again replaces it in one transaction, so its old
	// and new data are stored at once until it commits.
	if size+due.SizeBytes > storageLimitBytes {
		return false, fmt.Errorf("refreshing region %s needs about %d MB more than the database's %d MB,"+
			" past the %d MB of Neon's free plan; delete a region or import a smaller area again",
			due.Name, due.SizeBytes/1_000_000, size/1_000_000, storageLimitBytes/1_000_000)
	}
	extracts, err := s.extracts.Find(ctx, due.Bounds)
	if err != nil {
		return false, fmt.Errorf("failed to find the extracts of region %s: %w", due.Name, err)
	}
	// The import has no user who started it.
	record, err := routing.CreateImport(ctx, s.db, due.Name, due.Bounds, extracts, pgtype.UUID{})
	if err != nil {
		if errors.Is(err, routing.ErrActiveImport) {
			return false, nil
		}
		return false, err
	}
	if err := s.runner.Run(ctx, record.ID); err != nil {
		if err := routing.FailImport(context.WithoutCancel(ctx), s.db, record.ID, "failed to start the import"); err != nil {
			s.report.Report(ctx, "Failed to record the failed import", err)
		}
		return false, fmt.Errorf("failed to start refreshing region %s: %w", due.Name, err)
	}
	s.log.InfoContext(ctx, "Refreshing map data",
		slog.String("region", due.Name), slog.Time("import_time", due.ImportTime))
	return true, nil
}

func (s *Service) findExtracts(ctx context.Context, bounds geo.Bounds) ([]routing.Extract, error) {
	extracts, err := s.extracts.Find(ctx, bounds)
	if err != nil {
		if errors.Is(err, routing.ErrNoExtract) || errors.Is(err, routing.ErrTooManyExtracts) {
			return nil, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
		}
		s.report.Report(ctx, "Failed to find OpenStreetMap extracts", err)
		return nil, connect.NewError(connect.CodeUnavailable, "failed to look up OpenStreetMap extracts, try again later")
	}
	return extracts, nil
}

// activeImportError is returned when starting an import while another is
// queued or running.
func activeImportError() error {
	return connect.NewError(connect.CodeFailedPrecondition, "an import is already running, wait for it to finish")
}

func extractNames(extracts []routing.Extract) []string {
	names := make([]string, len(extracts))
	for i, x := range extracts {
		names[i] = x.Name
	}
	return names
}

func (s *Service) internalError(ctx context.Context, err error) error {
	s.report.Report(ctx, "Map data request failed", err)
	return connect.NewError(connect.CodeInternal, "internal error").WithCause(err)
}

// requireAdmin returns an error unless the user is an administrator.
func requireAdmin(ctx context.Context) error {
	user, ok := authn.GetUser(ctx)
	if !ok {
		return connect.NewError(connect.CodeUnauthenticated, "")
	}
	if !user.IsAdmin() {
		return connect.NewError(connect.CodePermissionDenied, "only administrators can manage map data")
	}
	return nil
}

func areaFromProto(area *routingv1.Area) (geo.Bounds, error) {
	b := geo.Bounds{
		MinLat: area.GetSouthWest().GetLat(),
		MinLng: area.GetSouthWest().GetLng(),
		MaxLat: area.GetNorthEast().GetLat(),
		MaxLng: area.GetNorthEast().GetLng(),
	}
	switch {
	case b.MinLat >= b.MaxLat || b.MinLng >= b.MaxLng:
		return geo.Bounds{}, errors.New("the area's south west corner must be south and west of its north east corner")
	case b.MinLat < -90 || b.MaxLat > 90 || b.MinLng < -180 || b.MaxLng > 180:
		return geo.Bounds{}, errors.New("the area is outside the map")
	case b.MaxLat-b.MinLat > maxAreaDegrees || b.MaxLng-b.MinLng > maxAreaDegrees:
		return geo.Bounds{}, fmt.Errorf("the area must be at most %d degrees across", maxAreaDegrees)
	}
	return b, nil
}

func areaToProto(b geo.Bounds) *routingv1.Area {
	return routingv1.Area_builder{
		SouthWest: geov1.LatLng_builder{Lat: b.MinLat, Lng: b.MinLng}.Build(),
		NorthEast: geov1.LatLng_builder{Lat: b.MaxLat, Lng: b.MaxLng}.Build(),
	}.Build()
}

func importToProto(r *routing.ImportRecord) *routingv1.Import {
	b := routingv1.Import_builder{
		Id:           store.UUIDToString(r.ID),
		Region:       r.Region,
		Area:         areaToProto(r.Bounds),
		ExtractNames: extractNames(r.Extracts),
		State:        stateToProto(r.State),
		Message:      r.Message,
		EdgeCount:    int32(r.EdgeCount),
		CreateTime:   timestamppb.New(r.CreateTime),
	}
	if r.FinishTime.Valid {
		b.FinishTime = timestamppb.New(r.FinishTime.Time)
	}
	return b.Build()
}

func stateToProto(state routing.ImportState) routingv1.ImportState {
	switch state {
	case routing.ImportQueued:
		return routingv1.ImportState_IMPORT_STATE_QUEUED
	case routing.ImportRunning:
		return routingv1.ImportState_IMPORT_STATE_RUNNING
	case routing.ImportDone:
		return routingv1.ImportState_IMPORT_STATE_DONE
	case routing.ImportFailed:
		return routingv1.ImportState_IMPORT_STATE_FAILED
	default:
		return routingv1.ImportState_IMPORT_STATE_UNSPECIFIED
	}
}
