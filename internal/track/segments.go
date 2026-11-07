package track

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// ErrNoProfile is returned when suggesting sections for a track without an
// elevation profile.
var ErrNoProfile = errors.New("the track has no elevation profile")

// segmentKind is the shape of a stretch of a track.
type segmentKind int

const (
	segmentFlat segmentKind = iota
	segmentRolling
	segmentClimb
	segmentDescent
)

func (k segmentKind) String() string {
	switch k {
	case segmentFlat:
		return "flat"
	case segmentRolling:
		return "rolling"
	case segmentClimb:
		return "climb"
	case segmentDescent:
		return "descent"
	}
	return "unknown"
}

// segment is a natural stretch of a track: a climb, a descent, a rolling
// stretch or a flat one.
type Segment struct {
	kind segmentKind
	// The distances along the track where the segment starts and ends.
	StartMeters, EndMeters float64
	// The elevation gain and loss within the segment, and the change from
	// its start to its end.
	gain, loss, net float64
	// The lowest, highest and mean elevation, from the unsmoothed profile.
	minEle, maxEle, meanEle float64
	// Whether the segment holds the highest point of the track.
	highPoint bool
	// A descriptive name, e.g. "Climb to 1,450 m".
	Name string
}

func (s *Segment) length() float64 { return s.EndMeters - s.StartMeters }

const (
	// maxSuggestedSections is the most sections suggested for a track.
	maxSuggestedSections = 8
	// minProfileMeters is the shortest track sections are suggested for.
	minProfileMeters = 1000
	// minSegmentMeters is the shortest segment, unless 5% of the track is
	// longer.
	minSegmentMeters = 1000
	// minChangeMeters is the smallest elevation change of a climb or
	// descent, and the smallest gain and loss of a rolling stretch.
	minChangeMeters = 30
	// minClimbGrade is the smallest average grade of a climb or descent.
	minClimbGrade = 0.02
)

// profile is an elevation profile resampled at a fixed step.
type profile struct {
	// The distance of the first sample and the step between samples.
	start, step float64
	// The total distance.
	end float64
	// The elevations, as recorded and smoothed.
	raw, smooth []float64
}

// newProfile resamples the elevations of a track at a fixed step and
// smooths them. Points with unknown elevations are skipped. It returns
// ErrNoProfile if fewer than two points have elevations or the track is
// shorter than a kilometer.
func newProfile(dists, eles []float64) (*profile, error) {
	var ds, es []float64
	for i, e := range eles {
		if math.IsNaN(e) || (len(ds) > 0 && dists[i] < ds[len(ds)-1]) {
			continue
		}
		ds = append(ds, dists[i])
		es = append(es, e)
	}
	if len(ds) < 2 || ds[len(ds)-1]-ds[0] < minProfileMeters {
		return nil, ErrNoProfile
	}
	p := &profile{start: ds[0], end: ds[len(ds)-1]}
	total := p.end - p.start
	p.step = math.Min(math.Max(total/1500, 10), 100)
	n := int(total/p.step) + 1
	p.raw = make([]float64, n)
	j := 0
	for i := range n {
		d := p.start + float64(i)*p.step
		for j+1 < len(ds)-1 && ds[j+1] < d {
			j++
		}
		span := ds[j+1] - ds[j]
		f := 0.0
		if span > 0 {
			f = math.Min(math.Max((d-ds[j])/span, 0), 1)
		}
		p.raw[i] = es[j] + f*(es[j+1]-es[j])
	}
	// A median removes spikes, and a mean over a longer window the jitter.
	p.smooth = movingMean(movingMedian(p.raw, int(math.Round(100/p.step))), int(math.Round(200/p.step)))
	return p, nil
}

// dist returns the distance of a sample.
func (p *profile) dist(i int) float64 {
	if i == len(p.raw)-1 {
		return p.end
	}
	return p.start + float64(i)*p.step
}

// movingMedian returns the median of each value and radius values on either
// side.
func movingMedian(values []float64, radius int) []float64 {
	result := make([]float64, len(values))
	window := make([]float64, 0, 2*radius+1)
	for i := range values {
		window = append(window[:0], values[max(0, i-radius):min(len(values), i+radius+1)]...)
		slices.Sort(window)
		result[i] = window[len(window)/2]
	}
	return result
}

// movingMean returns the mean of each value and radius values on either
// side.
func movingMean(values []float64, radius int) []float64 {
	result := make([]float64, len(values))
	sum := 0.0
	lo, hi := 0, 0
	for i := range values {
		for hi < min(len(values), i+radius+1) {
			sum += values[hi]
			hi++
		}
		for lo < i-radius {
			sum -= values[lo]
			lo++
		}
		result[i] = sum / float64(hi-lo)
	}
	return result
}

// turningPoints returns the indices of the start, the end and the major
// highs and lows of the smoothed profile between them. A high or low is
// major if the profile turns back from it by at least threshold meters.
func turningPoints(e []float64, threshold float64) []int {
	points := []int{0}
	const (
		unknown = iota
		up
		down
	)
	dir := unknown
	hi, lo := 0, 0
	for i := range e {
		if e[i] > e[hi] {
			hi = i
		}
		if e[i] < e[lo] {
			lo = i
		}
		switch dir {
		case unknown:
			switch {
			case e[i]-e[lo] >= threshold:
				if lo != 0 {
					points = append(points, lo)
				}
				dir, hi = up, i
			case e[hi]-e[i] >= threshold:
				if hi != 0 {
					points = append(points, hi)
				}
				dir, lo = down, i
			}
		case up:
			if e[hi]-e[i] >= threshold {
				points = append(points, hi)
				dir, lo = down, i
			}
		case down:
			if e[i]-e[lo] >= threshold {
				points = append(points, lo)
				dir, hi = up, i
			}
		}
	}
	// The last leg ends at its extreme, followed by what's left.
	last := len(e) - 1
	switch dir {
	case up:
		if hi != points[len(points)-1] && hi != last {
			points = append(points, hi)
		}
	case down:
		if lo != points[len(points)-1] && lo != last {
			points = append(points, lo)
		}
	}
	if points[len(points)-1] != last {
		points = append(points, last)
	}
	return points
}

// SuggestSegments splits a track into its natural segments from its
// elevation profile: the climbs, descents, and rolling or flat stretches in
// between, at most maxSuggestedSections of them. dists are the cumulative
// distances of the track's points and eles their elevations, NaN if
// unknown.
func SuggestSegments(dists, eles []float64) ([]*Segment, error) {
	p, err := newProfile(dists, eles)
	if err != nil {
		return nil, err
	}
	lowest, highest := slices.Min(p.smooth), slices.Max(p.smooth)
	elevationRange := highest - lowest
	// A climb or descent stands on its own when the profile turns back by
	// 8% of its range.
	threshold := math.Max(50, 0.08*elevationRange)
	// The band below the top of a climb, or above the bottom of a descent,
	// where it levels off.
	band := math.Max(minChangeMeters, 0.05*elevationRange)
	minLength := math.Max(minSegmentMeters, 0.05*(p.end-p.start))

	// Split each leg between turning points into the climb or descent, and
	// the long, level stretches at its ends, e.g. a ridge after a climb.
	pivots := turningPoints(p.smooth, threshold)
	cuts := []int{0}
	for k := 1; k < len(pivots); k++ {
		a, b := pivots[k-1], pivots[k]
		delta := p.smooth[b] - p.smooth[a]
		if math.Abs(delta) > 3*band {
			// The leg leaves its start where it last was near the start's
			// elevation, and arrives where it first gets near the end's.
			arrive := b
			for i := a; i <= b; i++ {
				if math.Abs(p.smooth[i]-p.smooth[b]) <= band {
					arrive = i
					break
				}
			}
			leave := a
			for i := arrive; i >= a; i-- {
				if math.Abs(p.smooth[i]-p.smooth[a]) <= band {
					leave = i
					break
				}
			}
			// Climbs end at a top and start at a bottom, and descents the
			// other way around: move the ends while the grade goes on.
			rise := math.Copysign(minClimbGrade/2*p.step, delta)
			for arrive < b && (p.smooth[arrive+1]-p.smooth[arrive])/rise > 1 {
				arrive++
			}
			for leave > a && (p.smooth[leave]-p.smooth[leave-1])/rise > 1 {
				leave--
			}
			if p.dist(leave)-p.dist(a) >= minLength {
				cuts = append(cuts, leave)
			}
			if p.dist(b)-p.dist(arrive) >= minLength {
				cuts = append(cuts, arrive)
			}
		}
		cuts = append(cuts, b)
	}

	type piece struct{ from, to int }
	pieces := make([]piece, 0, len(cuts))
	for k := 1; k < len(cuts); k++ {
		if cuts[k] > cuts[k-1] {
			pieces = append(pieces, piece{cuts[k-1], cuts[k]})
		}
	}
	measure := func(pc piece) *Segment {
		s := &Segment{
			StartMeters: p.dist(pc.from),
			EndMeters:   p.dist(pc.to),
			net:         p.smooth[pc.to] - p.smooth[pc.from],
		}
		s.gain, s.loss = geo.ElevationGainLoss(p.smooth[pc.from : pc.to+1])
		raw := p.raw[pc.from : pc.to+1]
		s.minEle, s.maxEle = slices.Min(raw), slices.Max(raw)
		sum := 0.0
		for _, e := range raw {
			sum += e
		}
		s.meanEle = sum / float64(len(raw))
		s.kind = classify(s)
		return s
	}
	segs := make([]*Segment, len(pieces))
	for i, pc := range pieces {
		segs[i] = measure(pc)
	}
	// merge joins segment i with the next one. segs and pieces are kept in
	// step.
	merge := func(i int) {
		pieces[i].to = pieces[i+1].to
		pieces = slices.Delete(pieces, i+1, i+2)
		segs[i] = measure(pieces[i])
		segs = slices.Delete(segs, i+1, i+2)
	}
	// mergeSameKinds joins neighbours of the same kind.
	mergeSameKinds := func() {
		for i := 0; i+1 < len(segs); {
			if segs[i].kind == segs[i+1].kind {
				merge(i)
				continue
			}
			i++
		}
	}
	// mergeIntoNeighbour joins a segment with the neighbour whose grade is
	// closest to its own.
	mergeIntoNeighbour := func(i int) {
		switch {
		case i == 0:
			merge(0)
		case i == len(segs)-1:
			merge(i - 1)
		default:
			grade := segs[i].net / segs[i].length()
			before := math.Abs(segs[i-1].net/segs[i-1].length() - grade)
			after := math.Abs(segs[i+1].net/segs[i+1].length() - grade)
			if before <= after {
				merge(i - 1)
			} else {
				merge(i)
			}
		}
	}
	mergeSameKinds()
	// Merge short segments without much climbing, shortest first.
	for len(segs) > 1 {
		shortest := -1
		for i, s := range segs {
			small := s.length() < minLength && math.Max(s.gain, s.loss) < 2*threshold
			if (small || s.length() < minLength/3) && (shortest < 0 || s.length() < segs[shortest].length()) {
				shortest = i
			}
		}
		if shortest < 0 {
			break
		}
		mergeIntoNeighbour(shortest)
		mergeSameKinds()
	}
	// Merge the least significant segments until few enough are left.
	for len(segs) > maxSuggestedSections {
		least := 0
		for i, s := range segs {
			if significance(s) < significance(segs[least]) {
				least = i
			}
		}
		mergeIntoNeighbour(least)
		mergeSameKinds()
	}
	nameSegments(segs, lowest, highest)
	return segs, nil
}

// classify returns the kind of a measured segment.
func classify(s *Segment) segmentKind {
	grade := s.net / s.length()
	switch {
	case s.net >= minChangeMeters && grade >= minClimbGrade && s.net >= s.gain/2:
		return segmentClimb
	case -s.net >= minChangeMeters && -grade >= minClimbGrade && -s.net >= s.loss/2:
		return segmentDescent
	case s.gain >= minChangeMeters && s.loss >= minChangeMeters:
		return segmentRolling
	}
	return segmentFlat
}

// significance is how much a segment stands out: its climbing or
// descending, and its length.
func significance(s *Segment) float64 {
	return math.Max(s.gain, s.loss) + 0.01*s.length()
}

// nameSegments names segments, e.g. "Climb to 1,450 m". lowest and highest
// are the lowest and highest smoothed elevations of the track.
func nameSegments(segs []*Segment, lowest, highest float64) {
	top := 0
	climbs := 0
	for i, s := range segs {
		if s.maxEle > segs[top].maxEle {
			top = i
		}
		if s.kind == segmentClimb {
			climbs++
		}
	}
	segs[top].highPoint = true
	firstClimb := true
	for i, s := range segs {
		switch s.kind {
		case segmentClimb:
			switch {
			case s.highPoint && climbs > 1:
				s.Name = "Climb to the high point, " + formatElevation(s.maxEle)
			case firstClimb && climbs > 1:
				s.Name = "First climb to " + formatElevation(s.maxEle)
			default:
				s.Name = "Climb to " + formatElevation(s.maxEle)
			}
			firstClimb = false
		case segmentDescent:
			if i == len(segs)-1 {
				s.Name = "Final descent to " + formatElevation(s.minEle)
			} else {
				s.Name = "Descent to " + formatElevation(s.minEle)
			}
		case segmentRolling:
			// A rolling stretch in the upper half of the track is a ridge.
			if highest > lowest && (s.meanEle-lowest)/(highest-lowest) >= 0.5 {
				s.Name = "Rolling ridge, " + formatKilometers(s.length())
			} else {
				s.Name = "Rolling terrain, " + formatKilometers(s.length())
			}
		case segmentFlat:
			s.Name = "Flat stretch, " + formatKilometers(s.length())
		}
	}
}

// formatElevation formats an elevation rounded to 10 meters, e.g.
// "1,450 m".
func formatElevation(meters float64) string {
	rounded := int(math.Round(meters/10) * 10)
	s := strconv.Itoa(max(rounded, -rounded))
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if rounded < 0 {
		s = "-" + s
	}
	return s + " m"
}

// formatKilometers formats a distance, with a decimal below 3 km, e.g.
// "2.5 km" or "6 km".
func formatKilometers(meters float64) string {
	km := meters / 1000
	if km < 3 {
		if s := strconv.FormatFloat(math.Round(km*10)/10, 'f', -1, 64); s != "0" {
			return s + " km"
		}
		return "0.1 km"
	}
	return fmt.Sprintf("%.0f km", km)
}
