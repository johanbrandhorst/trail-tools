package mapdata

import (
	"context"
	"fmt"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// Elevation looks up ground elevations, e.g. an elevation.DEM.
type Elevation interface {
	// Points returns the ground elevation of each point in meters.
	Points(ctx context.Context, points []geo.Point) ([]float64, error)
}

// Elevations looks up the ground elevation of each point in meters.
func (c *Client) Elevations(ctx context.Context, points []geo.Point) ([]float64, error) {
	elevations, err := c.conf.Elevation.Points(ctx, points)
	if err != nil {
		return nil, fmt.Errorf("elevation lookup failed: %w", err)
	}
	return elevations, nil
}

// FillElevations sets the elevation of every point. To limit the work, at most
// maxLookups points are looked up and elevations in between are interpolated
// by distance.
func (c *Client) FillElevations(ctx context.Context, points []geo.Point, maxLookups int) error {
	if len(points) == 0 {
		return nil
	}
	idxs := geo.Downsample(len(points), maxLookups)
	lookup := make([]geo.Point, len(idxs))
	for i, idx := range idxs {
		lookup[i] = points[idx]
	}
	elevations, err := c.Elevations(ctx, lookup)
	if err != nil {
		return err
	}
	dists := geo.CumulativeDistances(points)
	for k := 0; k < len(idxs); k++ {
		points[idxs[k]].Ele = elevations[k]
		if k == 0 {
			continue
		}
		from, to := idxs[k-1], idxs[k]
		span := dists[to] - dists[from]
		for i := from + 1; i < to; i++ {
			f := 0.0
			if span > 0 {
				f = (dists[i] - dists[from]) / span
			}
			points[i].Ele = elevations[k-1] + (elevations[k]-elevations[k-1])*f
		}
	}
	return nil
}
