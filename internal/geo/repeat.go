package geo

import (
	"math"
)

const (
	// repeatSpacing is the distance between the samples of a track that are
	// checked for repeats.
	repeatSpacing = 10.0
	// repeatTolerance is how close a sample must be to an earlier part of the
	// track to repeat it, allowing for GPS noise and both sides of a trail.
	repeatTolerance = 15.0
	// repeatMinGap is how far along the track an earlier part must be, so
	// that the track doesn't repeat itself where it's simply continuing, or
	// on the legs of tight switchbacks.
	repeatMinGap = 300.0
	// repeatMinAlignment is the minimum |cos| of the angle between the
	// directions of the track and the earlier part it repeats: they run along
	// the same line, either way, rather than crossing it.
	repeatMinAlignment = 0.85
	// repeatMaxGap is the longest line between two points of a track that is
	// sampled. Longer ones, such as where a recording paused or a bogus
	// point, aren't where the track went.
	repeatMaxGap = 5000.0
	// maxRepeatSamples limits the samples of a track, which take about 50
	// bytes each with their index. Tracks longer than about 2,000 km are
	// sampled further apart, which finds fewer repeats.
	maxRepeatSamples = 200_000
)

// RepeatedDistance returns the distance in meters of a track that retraces
// ground it covered earlier, in either direction. An out-and-back repeats
// about half its distance, a loop with a stem the stem's return, and a loop
// almost nothing. It's approximate: separate trails within about 15 m of each
// other count as the same.
func RepeatedDistance(points []Point) float64 {
	r := findRepeats(points)
	if r == nil {
		return 0
	}
	repeated := 0
	for _, m := range r.matches {
		if m >= 0 {
			repeated++
		}
	}
	total := CumulativeDistances(points)[len(points)-1]
	return math.Min(total, float64(repeated)*r.spacing)
}

// RepeatedSection is a section of a track that retraces ground it covered
// earlier, by distance along the track in meters.
type RepeatedSection struct {
	StartMeters, EndMeters float64
	// Where along the track the ground it retraces was first covered. It's
	// the start of the earlier part for a section in the same direction,
	// and its end for one in the opposite direction, like the way back of
	// an out-and-back.
	EarlierStartMeters, EarlierEndMeters float64
}

const (
	// repeatSectionGapMeters is the longest gap in a repeated section,
	// where the track wanders more than repeatTolerance from the earlier
	// part, which still counts as one section.
	repeatSectionGapMeters = 50.0
	// minRepeatSectionMeters is the shortest repeated section reported.
	minRepeatSectionMeters = 100.0
)

// RepeatedSections returns the sections of a track of at least 100 m that
// retrace ground it covered earlier, in order, as RepeatedDistance counts
// them.
func RepeatedSections(points []Point) []RepeatedSection {
	r := findRepeats(points)
	if r == nil {
		return nil
	}
	var sections []RepeatedSection
	var current *RepeatedSection
	lastRepeat := -1
	flush := func() {
		if current != nil && current.EndMeters-current.StartMeters >= minRepeatSectionMeters {
			sections = append(sections, *current)
		}
		current = nil
	}
	gap := int(repeatSectionGapMeters / r.spacing)
	for i, m := range r.matches {
		if m < 0 {
			continue
		}
		if current == nil || i-lastRepeat > gap+1 {
			flush()
			current = &RepeatedSection{StartMeters: r.along[i], EarlierStartMeters: r.along[m]}
		}
		current.EndMeters = r.along[i]
		current.EarlierEndMeters = r.along[m]
		lastRepeat = i
	}
	flush()
	return sections
}

// repeats are the samples of a track and the earlier ones they repeat.
type repeats struct {
	spacing float64
	// The distance along the track of each sample, in meters.
	along []float64
	// The index of the earlier sample each sample repeats, or -1.
	matches []int
}

// findRepeats samples a track and finds the samples that retrace an
// earlier part of it, or returns nil for a track too short to sample.
func findRepeats(points []Point) *repeats {
	spacing := math.Max(repeatSpacing, sampledDistance(points)/maxRepeatSamples)
	samples, along := resample(points, spacing)
	if len(samples) < 2 {
		return nil
	}
	// Index the samples by cell, with cells as wide as the tolerance, so
	// nearby samples are in the same or a neighboring cell.
	cells := map[[2]int][]int{}
	cellOf := func(s xy) [2]int {
		return [2]int{int(math.Floor(s.x / repeatTolerance)), int(math.Floor(s.y / repeatTolerance))}
	}
	direction := func(i int) (float64, float64) {
		a, b := samples[max(0, i-1)], samples[min(len(samples)-1, i+1)]
		dx, dy := b.x-a.x, b.y-a.y
		n := math.Hypot(dx, dy)
		if n == 0 {
			return 0, 0
		}
		return dx / n, dy / n
	}
	gap := max(1, int(repeatMinGap/spacing))
	matches := make([]int, len(samples))
	for i, s := range samples {
		matches[i] = -1
		if i >= gap {
			// Earlier samples become candidates once they're far enough
			// behind.
			earlier := samples[i-gap]
			c := cellOf(earlier)
			cells[c] = append(cells[c], i-gap)
		}
		dx, dy := direction(i)
		c := cellOf(s)
	search:
		for x := c[0] - 1; x <= c[0]+1; x++ {
			for y := c[1] - 1; y <= c[1]+1; y++ {
				for _, j := range cells[[2]int{x, y}] {
					o := samples[j]
					if math.Hypot(s.x-o.x, s.y-o.y) > repeatTolerance {
						continue
					}
					ox, oy := direction(j)
					if math.Abs(dx*ox+dy*oy) >= repeatMinAlignment {
						matches[i] = j
						break search
					}
				}
			}
		}
	}
	return &repeats{spacing: spacing, along: along, matches: matches}
}

// xy is a position in meters on a local flat projection.
type xy struct{ x, y float64 }

// sampledDistance returns the length in meters of the lines of a track that
// resample samples.
func sampledDistance(points []Point) float64 {
	total := 0.0
	for i := 1; i < len(points); i++ {
		if d := Distance(points[i-1], points[i]); d <= repeatMaxGap {
			total += d
		}
	}
	return total
}

// resample returns points spaced along a track, projected to meters around
// its first point, with their distances along the track. Lines longer than
// repeatMaxGap aren't sampled, but sampling starts again from their end.
func resample(points []Point, spacing float64) ([]xy, []float64) {
	if len(points) == 0 {
		return nil, nil
	}
	origin := points[0]
	cosLat := math.Cos(origin.Lat * math.Pi / 180)
	project := func(p Point) xy {
		return xy{
			x: (p.Lng - origin.Lng) * metersPerDegreeLat * cosLat,
			y: (p.Lat - origin.Lat) * metersPerDegreeLat,
		}
	}
	samples := []xy{project(points[0])}
	along := []float64{0}
	// The distance along the track to the next sample, since sampling last
	// started, and the distance along the whole track to points[i-1].
	next := spacing
	walked := 0.0
	total := 0.0
	for i := 1; i < len(points); i++ {
		d := Distance(points[i-1], points[i])
		if d > repeatMaxGap {
			total += d
			samples = append(samples, project(points[i]))
			along = append(along, total)
			walked, next = 0, spacing
			continue
		}
		for d > 0 && walked+d >= next {
			samples = append(samples, project(Interpolate(points[i-1], points[i], (next-walked)/d)))
			along = append(along, total+next-walked)
			next += spacing
		}
		walked += d
		total += d
	}
	return samples, along
}
