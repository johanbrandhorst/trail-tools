// Package geo implements the GeoService, which provides map data for
// planning routes.
package geo

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/activities"
	"github.com/johanbrandhorst/trail-tools/internal/authn"
	"github.com/johanbrandhorst/trail-tools/internal/elevation"
	"github.com/johanbrandhorst/trail-tools/internal/errreport"
	geov1 "github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1"
	"github.com/johanbrandhorst/trail-tools/internal/gen/geo/v1/geov1connect"
	"github.com/johanbrandhorst/trail-tools/internal/geo"
	"github.com/johanbrandhorst/trail-tools/internal/mapdata"
	"github.com/johanbrandhorst/trail-tools/internal/routing"
	"github.com/johanbrandhorst/trail-tools/internal/services/route"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

var _ geov1connect.GeoServiceHandler = (*Service)(nil)

type MapData interface {
	Search(ctx context.Context, query string, limit int) ([]mapdata.Place, error)
	Elevations(ctx context.Context, points []geo.Point) ([]float64, error)
}

// Finder finds trails and points of interest in the imported OpenStreetMap
// data.
type Finder interface {
	FindTrails(ctx context.Context, center geo.Point, radius float64, limit int) ([]routing.Trail, error)
	FindFeatures(ctx context.Context, center geo.Point, radius float64, kinds []geo.FeatureKind) ([]routing.Feature, error)
}

type TrackRepository interface {
	ListTracksInBounds(ctx context.Context, userID pgtype.UUID, bounds geo.Bounds, startDate, endDate time.Time, precision float64) (tracks []activities.Track, truncated bool, err error)
}

// Service implements API handlers for the geo service.
type Service struct {
	log     *slog.Logger
	report  *errreport.Reporter
	mapData MapData
	finder  Finder
	tracks  TrackRepository
}

// NewService creates a new Service.
func NewService(log *slog.Logger, report *errreport.Reporter, mapData MapData, finder Finder, tracks TrackRepository) *Service {
	return &Service{
		log:     log,
		report:  report,
		mapData: mapData,
		finder:  finder,
		tracks:  tracks,
	}
}

const (
	defaultRadiusMeters = 3000
	maxRadiusMeters     = 15000
)

func (s *Service) SearchPlaces(ctx context.Context, req *geov1.SearchPlacesRequest) (*geov1.SearchPlacesResponse, error) {
	if _, ok := authn.GetUserID(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	query := strings.TrimSpace(req.GetQuery())
	if query == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, "query is required")
	}
	limit := clamp(int(req.GetLimit()), 5, 20)
	places, err := s.mapData.Search(ctx, query, limit)
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	}
	protoPlaces := make([]*geov1.Place, len(places))
	for i, p := range places {
		b := geov1.Place_builder{
			Name:     p.Name,
			Location: latLng(p.Lat, p.Lng),
			Category: p.Category,
			Type:     p.Type,
		}
		if len(p.BoundingBox) == 4 {
			b.Bounds = geov1.BoundingBox_builder{
				South: p.BoundingBox[0],
				North: p.BoundingBox[1],
				West:  p.BoundingBox[2],
				East:  p.BoundingBox[3],
			}.Build()
		}
		protoPlaces[i] = b.Build()
	}
	return geov1.SearchPlacesResponse_builder{Places: protoPlaces}.Build(), nil
}

func (s *Service) FindTrails(ctx context.Context, req *geov1.FindTrailsRequest) (*geov1.FindTrailsResponse, error) {
	if _, ok := authn.GetUserID(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	center, radius, err := searchArea(req.GetCenter(), req.GetRadiusMeters())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	limit := clamp(int(req.GetLimit()), 50, 500)
	tolerance := req.GetGeometryToleranceMeters()
	if tolerance < 0 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "geometry_tolerance_meters must not be negative")
	}
	ways, err := s.finder.FindTrails(ctx, center, radius, limit)
	if err != nil {
		return nil, s.findError(ctx, err)
	}
	type trail struct {
		proto  *geov1.Trail
		length float64
	}
	var trails []trail
	for _, way := range ways {
		points := way.Points
		if len(points) < 2 {
			continue
		}
		// Only count the part of the way inside the search area.
		var length float64
		for i := 1; i < len(points); i++ {
			if geo.Distance(center, geo.Interpolate(points[i-1], points[i], 0.5)) <= radius {
				length += geo.Distance(points[i-1], points[i])
			}
		}
		if length == 0 {
			continue
		}
		nearest, nearestDist := geo.NearestIndex(points, center)
		b := geov1.Trail_builder{
			OsmId:                    strconv.FormatInt(way.WayID, 10),
			Name:                     way.Name,
			Highway:                  way.Highway,
			Surface:                  way.Surface,
			SacScale:                 way.SacScale,
			Terrain:                  route.TerrainToProto(way.Terrain),
			LengthMeters:             math.Round(length),
			Start:                    latLng(points[0].Lat, points[0].Lng),
			End:                      latLng(points[len(points)-1].Lat, points[len(points)-1].Lng),
			NearestPoint:             latLng(points[nearest].Lat, points[nearest].Lng),
			DistanceFromCenterMeters: math.Round(nearestDist),
		}
		if req.GetIncludeGeometry() {
			geometry := points
			if tolerance > 0 {
				geometry = geo.Simplify(points, tolerance)
			}
			for _, p := range geometry {
				b.Geometry = append(b.Geometry, latLng(p.Lat, p.Lng))
			}
		}
		trails = append(trails, trail{proto: b.Build(), length: length})
	}
	sort.SliceStable(trails, func(i, j int) bool { return trails[i].length > trails[j].length })
	result := make([]*geov1.Trail, 0, min(limit, len(trails)))
	for _, t := range trails[:min(limit, len(trails))] {
		result = append(result, t.proto)
	}
	return geov1.FindTrailsResponse_builder{Trails: result}.Build(), nil
}

// poiKinds are the kinds of points of interest.
var poiKinds = map[geov1.PointOfInterestKind]geo.FeatureKind{
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_PEAK:      geo.FeaturePeak,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_WATER:     geo.FeatureWater,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_PARKING:   geo.FeatureParking,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_VIEWPOINT: geo.FeatureViewpoint,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_SHELTER:   geo.FeatureShelter,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_TOILETS:   geo.FeatureToilets,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_SADDLE:    geo.FeatureSaddle,
	geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_TRAILHEAD: geo.FeatureTrailhead,
}

// poiKindToProto returns the proto kind of a point of interest.
func poiKindToProto(kind geo.FeatureKind) geov1.PointOfInterestKind {
	for k, v := range poiKinds {
		if v == kind {
			return k
		}
	}
	return geov1.PointOfInterestKind_POINT_OF_INTEREST_KIND_UNSPECIFIED
}

// findError returns the API error for a failure to find trails or points
// of interest.
func (s *Service) findError(ctx context.Context, err error) error {
	if errors.Is(err, routing.ErrNoMapData) {
		return connect.NewError(connect.CodeFailedPrecondition, "no map data has been imported for this area; import it on the map data page first").WithCause(err)
	}
	return s.internalError(ctx, err, "failed to query the map data")
}

// internalError reports err, unless the request was cancelled, as the map
// cancels its requests when it moves, and returns an Internal error.
func (s *Service) internalError(ctx context.Context, err error, msg string) error {
	if ctx.Err() == nil {
		s.report.Report(ctx, msg, err)
	}
	return connect.NewError(connect.CodeInternal, msg).WithCause(err)
}

func (s *Service) FindPointsOfInterest(ctx context.Context, req *geov1.FindPointsOfInterestRequest) (*geov1.FindPointsOfInterestResponse, error) {
	if _, ok := authn.GetUserID(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	center, radius, err := searchArea(req.GetCenter(), req.GetRadiusMeters())
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	limit := clamp(int(req.GetLimit()), 100, 500)
	kinds := req.GetKinds()
	if len(kinds) == 0 {
		for kind := range poiKinds {
			kinds = append(kinds, kind)
		}
	}
	var featureKinds []geo.FeatureKind
	for _, kind := range kinds {
		k, ok := poiKinds[kind]
		if !ok {
			return nil, connect.Errorf(connect.CodeInvalidArgument, "unknown kind %v", kind)
		}
		featureKinds = append(featureKinds, k)
	}
	features, err := s.finder.FindFeatures(ctx, center, radius, featureKinds)
	if err != nil {
		return nil, s.findError(ctx, err)
	}
	var points []*geov1.PointOfInterest
	for _, f := range features {
		loc := geo.Point{Lat: f.Point.Lat, Lng: f.Point.Lng}
		var ele float64
		if !math.IsNaN(f.Point.Ele) {
			ele = f.Point.Ele
		}
		points = append(points, geov1.PointOfInterest_builder{
			OsmId:                    strconv.FormatInt(f.ID, 10),
			Kind:                     poiKindToProto(f.Kind),
			Name:                     f.Name,
			Location:                 latLng(loc.Lat, loc.Lng),
			ElevationMeters:          ele,
			DistanceFromCenterMeters: math.Round(geo.Distance(center, loc)),
		}.Build())
	}
	sort.SliceStable(points, func(i, j int) bool {
		return points[i].GetDistanceFromCenterMeters() < points[j].GetDistanceFromCenterMeters()
	})
	return geov1.FindPointsOfInterestResponse_builder{
		Points: points[:min(limit, len(points))],
	}.Build(), nil
}

func (s *Service) GetElevation(ctx context.Context, req *geov1.GetElevationRequest) (*geov1.GetElevationResponse, error) {
	if _, ok := authn.GetUserID(ctx); !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	if n := len(req.GetPoints()); n == 0 || n > 100 {
		return nil, connect.NewError(connect.CodeInvalidArgument, "between 1 and 100 points are required")
	}
	points := make([]geo.Point, len(req.GetPoints()))
	for i, p := range req.GetPoints() {
		points[i] = geo.Point{Lat: p.GetLat(), Lng: p.GetLng()}
	}
	elevations, err := s.mapData.Elevations(ctx, points)
	if errors.Is(err, elevation.ErrInvalidCoordinates) || errors.Is(err, elevation.ErrTooManyTiles) {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	if err != nil {
		return nil, connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	}
	return geov1.GetElevationResponse_builder{ElevationsMeters: elevations}.Build(), nil
}

const (
	defaultCellSizeMeters = 100
	minCellSizeMeters     = 25
	maxHeatmapCells       = 4_000_000

	defaultTrackToleranceMeters = 10
	minTrackToleranceMeters     = 1
	// maxHeatmapTrackPoints bounds the size of a heatmap tracks response.
	maxHeatmapTrackPoints = 200_000
	// maxDetailedHeatmapMeters is the widest or tallest area that heatmaps
	// are made from the activities' tracks rather than their summary
	// polylines. Over larger areas, the summary polylines are accurate
	// enough, and the tracks are tens of megabytes for active users.
	maxDetailedHeatmapMeters = 100_000
	// metersPerDegreeLat is the approximate length of a degree of latitude.
	metersPerDegreeLat = 111_320.0
)

// trackPrecision returns the precision to list the tracks in bounds with,
// for heatmaps that need the precision: at least
// activities.SummaryPrecisionMeters for areas larger than
// maxDetailedHeatmapMeters.
func trackPrecision(bounds geo.Bounds, precision float64) float64 {
	midLat := (bounds.MinLat + bounds.MaxLat) / 2
	height := (bounds.MaxLat - bounds.MinLat) * metersPerDegreeLat
	width := (bounds.MaxLng - bounds.MinLng) * metersPerDegreeLat * math.Cos(midLat*math.Pi/180)
	if max(height, width) > maxDetailedHeatmapMeters {
		return max(precision, activities.SummaryPrecisionMeters)
	}
	return precision
}

func (s *Service) ListHeatmapTracks(ctx context.Context, req *geov1.ListHeatmapTracksRequest) (*geov1.ListHeatmapTracksResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	pb := req.GetBounds()
	bounds := geo.Bounds{MinLat: pb.GetSouth(), MinLng: pb.GetWest(), MaxLat: pb.GetNorth(), MaxLng: pb.GetEast()}
	if !req.HasBounds() || bounds.MinLat >= bounds.MaxLat || bounds.MinLng >= bounds.MaxLng {
		return nil, connect.NewError(connect.CodeInvalidArgument, "bounds with south < north and west < east are required")
	}
	tolerance := req.GetToleranceMeters()
	if tolerance == 0 {
		tolerance = defaultTrackToleranceMeters
	}
	if tolerance < minTrackToleranceMeters {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "tolerance_meters must be at least %d", minTrackToleranceMeters)
	}
	tracks, truncated, err := s.tracks.ListTracksInBounds(ctx, userID, bounds, time.Time{}, time.Time{}, trackPrecision(bounds, tolerance))
	if err != nil {
		return nil, s.internalError(ctx, err, "failed to list tracks")
	}
	var parts [][]geo.Point
	for _, track := range tracks {
		// Connect reports the context's error with its code.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for _, part := range geo.Clip(track.Points, bounds) {
			parts = append(parts, geo.Simplify(part, tolerance))
		}
	}
	// The longest parts first, so a truncated response keeps the most of
	// the tracks.
	sort.SliceStable(parts, func(i, j int) bool { return len(parts[i]) > len(parts[j]) })
	resp := geov1.ListHeatmapTracksResponse_builder{Truncated: truncated}
	points := 0
	for _, part := range parts {
		if points+len(part) > maxHeatmapTrackPoints {
			resp.Truncated = true
			break
		}
		points += len(part)
		resp.Polylines = append(resp.Polylines, geo.EncodePolyline(part))
	}
	return resp.Build(), nil
}

func (s *Service) GetPersonalHeatmap(ctx context.Context, req *geov1.GetPersonalHeatmapRequest) (*geov1.GetPersonalHeatmapResponse, error) {
	userID, ok := authn.GetUserID(ctx)
	if !ok {
		return nil, connect.NewError(connect.CodeUnauthenticated, "")
	}
	pb := req.GetBounds()
	bounds := geo.Bounds{MinLat: pb.GetSouth(), MinLng: pb.GetWest(), MaxLat: pb.GetNorth(), MaxLng: pb.GetEast()}
	if !req.HasBounds() || bounds.MinLat >= bounds.MaxLat || bounds.MinLng >= bounds.MaxLng {
		return nil, connect.NewError(connect.CodeInvalidArgument, "bounds with south < north and west < east are required")
	}
	cellSize := req.GetCellSizeMeters()
	if cellSize == 0 {
		cellSize = defaultCellSizeMeters
	}
	if cellSize < minCellSizeMeters {
		return nil, connect.Errorf(connect.CodeInvalidArgument, "cell_size_meters must be at least %d", minCellSizeMeters)
	}
	limit := clamp(int(req.GetLimit()), 2000, 20000)
	var startDate, endDate time.Time
	var err error
	if req.GetStartDate() != "" {
		if startDate, err = store.ParseDate(req.GetStartDate()); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
	}
	if req.GetEndDate() != "" {
		if endDate, err = store.ParseDate(req.GetEndDate()); err != nil {
			return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
		}
	}

	grid := geo.NewGrid(bounds, cellSize)
	if grid.Rows*grid.Cols > maxHeatmapCells {
		return nil, connect.NewError(connect.CodeInvalidArgument, "the area is too large for the cell size; use a smaller area or larger cells")
	}
	// Tracks accurate to a quarter of a cell count in the right cells.
	tracks, truncated, err := s.tracks.ListTracksInBounds(ctx, userID, bounds, startDate, endDate, trackPrecision(bounds, cellSize/4))
	if err != nil {
		return nil, s.internalError(ctx, err, "failed to list tracks")
	}
	counts := map[int]int32{}
	for _, track := range tracks {
		// Connect reports the context's error with its code.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for cell := range grid.Cells(track.Points) {
			counts[cell]++
		}
	}
	cells := make([]*geov1.HeatmapCell, 0, len(counts))
	for cell, count := range counts {
		lat, lng := grid.Center(cell)
		cells = append(cells, geov1.HeatmapCell_builder{
			Center:        latLng(lat, lng),
			ActivityCount: count,
		}.Build())
	}
	sort.Slice(cells, func(i, j int) bool {
		if cells[i].GetActivityCount() != cells[j].GetActivityCount() {
			return cells[i].GetActivityCount() > cells[j].GetActivityCount()
		}
		if cells[i].GetCenter().GetLat() != cells[j].GetCenter().GetLat() {
			return cells[i].GetCenter().GetLat() < cells[j].GetCenter().GetLat()
		}
		return cells[i].GetCenter().GetLng() < cells[j].GetCenter().GetLng()
	})
	return geov1.GetPersonalHeatmapResponse_builder{
		Cells:          cells[:min(limit, len(cells))],
		CellSizeMeters: cellSize,
		ActivityCount:  int32(len(tracks)),
		Truncated:      truncated,
	}.Build(), nil
}

func searchArea(center *geov1.LatLng, radius float64) (geo.Point, float64, error) {
	if center == nil {
		return geo.Point{}, 0, errors.New("center is required")
	}
	if radius == 0 {
		radius = defaultRadiusMeters
	}
	if radius < 0 || radius > maxRadiusMeters {
		return geo.Point{}, 0, fmt.Errorf("radius_meters must be between 0 and %d", maxRadiusMeters)
	}
	return geo.Point{Lat: center.GetLat(), Lng: center.GetLng()}, radius, nil
}

// clamp returns def if v is 0, otherwise v limited to at most maxV.
func clamp(v, def, maxV int) int {
	if v <= 0 {
		return def
	}
	return min(v, maxV)
}

func latLng(lat, lng float64) *geov1.LatLng {
	return geov1.LatLng_builder{Lat: lat, Lng: lng}.Build()
}
