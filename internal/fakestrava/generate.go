package fakestrava

import (
	"context"
	"fmt"
	"math"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// historyDays is how many days of activities the athlete has, up to today.
const historyDays = 120

// firstID is far above the IDs of real Strava activities, so that links to
// the activities on Strava don't show someone else's.
const firstID = 9_000_000_000_000

// home is where the athlete lives: Squamish, between Howe Sound and the
// Coast Mountains, below the Stawamus Chief.
var home = geo.Point{Lat: 49.7016, Lng: -123.1558}

// timezone is the timezone of the athlete.
var timezone = mustLoadLocation("America/Vancouver")

func mustLoadLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		return time.FixedZone("PST", -8*3600)
	}
	return loc
}

// place is where an activity starts, and what it's called.
type place struct {
	name      string
	trailhead geo.Point
}

// Trailheads around Squamish.
var (
	chief        = geo.Point{Lat: 49.6806, Lng: -123.1483}
	gondola      = geo.Point{Lat: 49.6764, Lng: -123.1565}
	smokeBluffs  = geo.Point{Lat: 49.7070, Lng: -123.1430}
	crumpitWoods = geo.Point{Lat: 49.7140, Lng: -123.1370}
	aliceLake    = geo.Point{Lat: 49.7770, Lng: -123.1210}
	diamondHead  = geo.Point{Lat: 49.7654, Lng: -123.0423}
	rubbleCreek  = geo.Point{Lat: 49.9574, Lng: -123.1200}
	quest        = geo.Point{Lat: 49.7340, Lng: -123.1004}
	murrinPark   = geo.Point{Lat: 49.6437, Lng: -123.2060}
	brohmLake    = geo.Point{Lat: 49.8220, Lng: -123.1330}
	estuary      = geo.Point{Lat: 49.6985, Lng: -123.1630}
	dike         = geo.Point{Lat: 49.7550, Lng: -123.1520}
	highlands    = geo.Point{Lat: 49.7480, Lng: -123.1180}
	brennanPark  = geo.Point{Lat: 49.7012, Lng: -123.1410}
)

// sport is a kind of activity the athlete does.
type sport struct {
	sportType string
	places    []place
	// The distance in km, and the climbing in meters per km of tracks that
	// aren't routed along mapped paths.
	minKm, maxKm                 float64
	minClimbPerKm, maxClimbPerKm float64
	// Whether tracks follow trails, or else take the shortest way.
	trails bool
	// The photos taken during the activity: one every so many km, up to a
	// maximum.
	kmPerPhoto float64
	maxPhotos  int
	// The hour of the day it starts.
	hour int
	// Whether it's a race.
	race bool
}

// within returns the sport with a distance from minKm to maxKm.
func (s sport) within(minKm, maxKm float64) sport {
	s.minKm, s.maxKm = minKm, maxKm
	return s
}

var (
	roadRun = sport{
		sportType: "Run", minKm: 7, maxKm: 12, minClimbPerKm: 1, maxClimbPerKm: 6, hour: 7,
		places: []place{
			{"Morning Run", home},
			{"Easy run along the estuary", estuary},
			{"Tempo on the dike", dike},
			{"Mamquam Blind Channel loop", home},
			{"Recovery run", home},
		},
	}
	trailRun = sport{
		sportType: "TrailRun", minKm: 9, maxKm: 34, minClimbPerKm: 45, maxClimbPerKm: 75, trails: true, hour: 7,
		kmPerPhoto: 5, maxPhotos: 7,
		places: []place{
			{"Stawamus Chief", chief},
			{"Sea to Summit", gondola},
			{"Smoke Bluffs and Crumpit Woods", smokeBluffs},
			{"Alice Lake loop", aliceLake},
			{"Four Lakes and Alice Ridge", aliceLake},
			{"Elfin Lakes", diamondHead},
			{"Quest loop", quest},
			{"Petgill Lake", murrinPark},
			{"Garibaldi Lake", rubbleCreek},
		},
	}
	hike = sport{
		sportType: "Hike", minKm: 9, maxKm: 17, minClimbPerKm: 60, maxClimbPerKm: 95, trails: true, hour: 9,
		kmPerPhoto: 3, maxPhotos: 5,
		places: []place{
			{"Stawamus Chief hike", chief},
			{"Elfin Lakes with friends", diamondHead},
			{"Brohm Lake", brohmLake},
			{"Hike to Garibaldi Lake", rubbleCreek},
		},
	}
	ride = sport{
		sportType: "Ride", minKm: 30, maxKm: 60, minClimbPerKm: 4, maxClimbPerKm: 12, hour: 17,
		places: []place{{"Sea to Sky ride", home}, {"Ride to Brackendale and back", home}, {"Evening spin", home}},
	}
	mountainBikeRide = sport{
		sportType: "MountainBikeRide", minKm: 15, maxKm: 35, minClimbPerKm: 25, maxClimbPerKm: 40, trails: true, hour: 17,
		kmPerPhoto: 15, maxPhotos: 2,
		places: []place{
			{"Half Nelson and Pseudo-Tsuga", highlands},
			{"Alice Lake trails", aliceLake},
			{"Diamond Head loop", quest},
			{"Crumpit Woods laps", crumpitWoods},
		},
	}
	skiTour = sport{
		sportType: "BackcountrySki", minKm: 10, maxKm: 20, minClimbPerKm: 70, maxClimbPerKm: 110, trails: true, hour: 7,
		kmPerPhoto: 4, maxPhotos: 5,
		places: []place{
			{"Elfin Lakes ski tour", diamondHead},
			{"Brohm Ridge", brohmLake},
			{"Diamond Head tour", diamondHead},
			{"Round Mountain laps", diamondHead},
		},
	}
)

// races are the trail races the athlete runs now and then, with their
// distance in km and climbing in meters per km.
var races = []struct {
	place
	km, climbPerKm float64
}{
	{place{"Squamish 50 – 50 km", brennanPark}, 50, 55},
	{place{"Loop the Lakes Trail Race", aliceLake}, 11, 30},
	{place{"Quest Trail Run", quest}, 21, 40},
	{place{"Sea to Summit Race", gondola}, 8, 115},
}

// raceEveryWeeks is how often the athlete races, on a Saturday.
const raceEveryWeeks = 5

// race returns the race on a day as a sport.
func race(day time.Time) sport {
	_, week := day.ISOWeek()
	r := races[week/raceEveryWeeks%len(races)]
	return sport{
		sportType: "TrailRun", places: []place{r.place}, minKm: r.km, maxKm: r.km,
		minClimbPerKm: r.climbPerKm, maxClimbPerKm: r.climbPerKm, trails: true, hour: 7,
		kmPerPhoto: 8, maxPhotos: 5, race: true,
	}
}

var captions = []string{
	"Above the clouds", "The Chief from the Sea to Sky", "Howe Sound below", "Lunch with a view", "Along the ridge",
	"Almost at the top", "Mossy forest", "Garibaldi on the horizon", "Summit selfie", "", "",
}

var raceDescriptions = []string{
	"Race day! Went out too fast, hung on at the end.",
	"Race day. Great crowd at the finish.\n\nThe last climb hurt.",
}

var descriptions = []string{
	"Legs felt good today.",
	"Perfect weather, tired legs.\n\nThe last climb was hard but the views made up for it.",
	"Easy pace, chatting the whole way.",
	"Started in the fog, came out above the clouds.",
	"",
	"",
}

// activity is a generated activity.
type activity struct {
	id          int64
	name        string
	sportType   string
	description string
	start       time.Time
	// Manual activities have no streams or map.
	manual bool
	race   bool

	distance           float64
	movingTime         int32
	elapsedTime        int32
	gain               float64
	avgHR, maxHR       float64
	avgCadence         float64
	avgSpeed, maxSpeed float64
	sufferScore        float64
	polyline           string
	streams            *streams
	photos             []*photo
	// Whether the track follows mapped paths.
	routed bool
}

// streams are the recorded data of an activity.
type streams struct {
	time     []int32
	distance []float64
	latlng   [][2]float64
	altitude []float64
	hr       []float64
	cadence  []float64
	velocity []float64
	grade    []float64
}

// photo is a photo taken during an activity.
type photo struct {
	id      string
	caption string
	taken   time.Time
	// Nil if the photo has no location.
	location *geo.Point
	// Seeds the generated image.
	seed uint64
}

// outing is a planned activity.
type outing struct {
	id     int64
	sport  sport
	place  place
	start  time.Time
	manual bool
}

// generate returns the athlete's activities from historyDays ago until now,
// newest first, with tracks made by tracer. Activities are the same for the
// same day, so their IDs stay stable between syncs.
func generate(ctx context.Context, now time.Time, tracer *tracer) []*activity {
	outings := plan(now)
	result := make([]*activity, len(outings))
	// Tracks are routed a few at a time.
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for i, o := range outings {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			if o.manual {
				result[i] = manualRun(o)
			} else {
				result[i] = newActivity(ctx, o, tracer)
			}
		})
	}
	wg.Wait()
	return result
}

// plan returns the outings from historyDays ago until now, newest first:
// easy runs, and a short trail run or a mountain bike ride during the week,
// a long trail run or a race on Saturday, and a hike, ski tour or easy run on
// Sunday.
func plan(now time.Time) []outing {
	today := now.In(timezone)
	var result []outing
	for back := range historyDays {
		day := time.Date(today.Year(), today.Month(), today.Day()-back, 0, 0, 0, 0, timezone)
		dayNumber := day.Unix() / 86400
		r := rand.New(rand.NewPCG(uint64(dayNumber), 42))
		for i, s := range sports(day, r) {
			o := outing{
				id:    firstID + dayNumber*10 + int64(i) + 1,
				sport: s,
				place: s.places[r.IntN(len(s.places))],
				start: day.Add(time.Duration(s.hour)*time.Hour + time.Duration(r.IntN(50))*time.Minute),
			}
			if o.start.After(now) {
				continue
			}
			o.manual = s.sportType == "Run" && day.Weekday() == time.Thursday && day.YearDay()%4 == 0
			result = append(result, o)
		}
	}
	return result
}

// sports returns the sports of the activities of a day.
func sports(day time.Time, r *rand.Rand) []sport {
	skip := r.Float64() < 0.15
	switch day.Weekday() {
	case time.Monday:
		return nil
	case time.Tuesday, time.Thursday:
		if skip {
			return nil
		}
		return []sport{roadRun}
	case time.Wednesday:
		switch {
		case skip:
			return nil
		case r.Float64() < 0.3:
			return []sport{mountainBikeRide}
		}
		hillRun := trailRun.within(9, 15)
		hillRun.hour = 18
		return []sport{hillRun}
	case time.Friday:
		switch x := r.Float64(); {
		case x < 0.3:
			return []sport{mountainBikeRide}
		case x < 0.5:
			return []sport{ride}
		}
		return nil
	case time.Saturday:
		if _, week := day.ISOWeek(); week%raceEveryWeeks == 0 {
			return []sport{race(day)}
		}
		return []sport{trailRun.within(18, 34)}
	case time.Sunday:
		switch x := r.Float64(); {
		case x < 0.3:
			return []sport{hike}
		case x < 0.6:
			return []sport{skiTour}
		case skip:
			return nil
		}
		return []sport{roadRun}
	}
	return nil
}

// manualRun returns an activity entered by hand, without a track.
func manualRun(o outing) *activity {
	r := rand.New(rand.NewPCG(uint64(o.id), 42))
	distance := 8000 + float64(r.IntN(4000))
	moving := int32(distance / 3.1)
	return &activity{
		id:          o.id,
		name:        "Treadmill run",
		sportType:   "Run",
		description: "Raining all day, so the treadmill it was.",
		start:       o.start,
		manual:      true,
		distance:    distance,
		movingTime:  moving,
		elapsedTime: moving,
		avgSpeed:    distance / float64(moving),
		maxSpeed:    3.6,
	}
}

// newActivity generates an activity with its track.
func newActivity(ctx context.Context, o outing, tracer *tracer) *activity {
	s := o.sport
	r := rand.New(rand.NewPCG(uint64(o.id), 42))
	a := &activity{
		id:          o.id,
		name:        o.place.name,
		sportType:   s.sportType,
		description: descriptions[r.IntN(len(descriptions))],
		start:       o.start,
		race:        s.race,
	}
	if s.race {
		a.description = raceDescriptions[r.IntN(len(raceDescriptions))]
	}
	meters := (s.minKm + r.Float64()*(s.maxKm-s.minKm)) * 1000
	points, routed := tracer.track(ctx, o, meters, r)
	a.routed = routed
	dists := geo.CumulativeDistances(points)
	total := dists[len(dists)-1]

	st := &streams{}
	var t, pausedSeconds float64
	photoAt := photoFractions(s, total, r)
	var maxSpeed, hrSum, maxHR float64
	// The heart rate follows the effort with a lag.
	hr := 0.0
	for i, p := range points {
		f := dists[i] / total
		grade := 0.0
		if i > 0 {
			step := dists[i] - dists[i-1]
			if step > 0 {
				grade = (p.Ele - points[i-1].Ele) / step
			}
			v := speed(s.sportType, grade)
			if s.race {
				v *= 1.12
			}
			t += step / v
			maxSpeed = math.Max(maxSpeed, v)
			// Stop for photos.
			for len(photoAt) > 0 && f >= photoAt[0] {
				photoAt = photoAt[1:]
				pause := float64(30 + r.IntN(120))
				a.photos = append(a.photos, &photo{
					taken: o.start.Add(time.Duration(t) * time.Second),
					seed:  r.Uint64(),
				})
				if r.Float64() < 0.8 {
					loc := geo.Point{Lat: p.Lat, Lng: p.Lng}
					a.photos[len(a.photos)-1].location = &loc
				}
				t += pause
				pausedSeconds += pause
			}
		}
		target := heartRate(s.sportType, grade, f, r)
		if s.race {
			target += 10
		}
		if i == 0 {
			hr = target - 25
		}
		hr += (target - hr) * 0.04
		hrSum += hr
		maxHR = max(maxHR, hr)
		st.time = append(st.time, int32(t))
		st.distance = append(st.distance, math.Round(dists[i]*10)/10)
		st.latlng = append(st.latlng, [2]float64{round(p.Lat, 6), round(p.Lng, 6)})
		st.altitude = append(st.altitude, math.Round(p.Ele*10)/10)
		st.hr = append(st.hr, math.Round(hr))
		st.cadence = append(st.cadence, cadence(s.sportType, r))
		st.velocity = append(st.velocity, math.Round(speed(s.sportType, grade)*100)/100)
		st.grade = append(st.grade, math.Round(grade*1000)/10)
	}
	for i, p := range a.photos {
		p.id = fmt.Sprintf("%d-%d", o.id, i+1)
		p.caption = captions[r.IntN(len(captions))]
	}
	a.streams = st
	a.distance = math.Round(total*10) / 10
	a.elapsedTime = int32(t)
	a.movingTime = int32(t - pausedSeconds)
	a.avgSpeed = total / float64(a.movingTime)
	a.maxSpeed = maxSpeed
	a.gain, _ = geo.ElevationGainLoss(st.altitude)
	a.gain = math.Round(a.gain)
	a.avgHR = math.Round(hrSum / float64(len(points)))
	a.maxHR = math.Round(maxHR)
	a.avgCadence = cadence(s.sportType, r)
	a.sufferScore = math.Round(float64(a.movingTime) / 60 * (a.avgHR - 100) / 40)
	idxs := geo.Downsample(len(points), 300)
	summary := make([]geo.Point, len(idxs))
	for i, idx := range idxs {
		summary[i] = points[idx]
	}
	a.polyline = geo.EncodePolyline(summary)
	return a
}

// speed returns the speed in m/s at a grade.
func speed(sportType string, grade float64) float64 {
	switch sportType {
	case "Run":
		return math.Max(3.2*(1-4*grade), 2.2)
	case "Ride":
		return math.Min(math.Max(8*(1-8*grade), 2.5), 14)
	case "MountainBikeRide":
		switch {
		case grade > 0.04:
			// About 900 vertical meters an hour.
			return math.Min(5, 0.25/grade)
		case grade < -0.05:
			return 6
		}
		return 5
	case "Hike":
		if grade > 0.03 {
			// About 450 vertical meters an hour.
			return math.Min(1.3, 0.125/grade)
		}
		return 1.25
	case "BackcountrySki":
		switch {
		case grade > 0.03:
			// Skinning up at about 550 vertical meters an hour.
			return math.Min(1.4, 0.15/grade)
		case grade < -0.05:
			// Skiing down.
			return 7
		}
		return 1.4
	default:
		if grade > 0.05 {
			// About 800 vertical meters an hour.
			return math.Min(2.8, 0.22/grade)
		}
		if grade < -0.15 {
			return 2.4
		}
		return 2.9
	}
}

// heartRate returns the heart rate the effort at a grade leads to, a
// fraction of the way in.
func heartRate(sportType string, grade, f float64, r *rand.Rand) float64 {
	base := 140.0
	switch sportType {
	case "Hike":
		base = 115
	case "Ride":
		base = 128
	case "MountainBikeRide":
		base = 135
	case "BackcountrySki":
		base = 125
	}
	// Steep climbs are walked, so the effort levels off.
	hr := base + 140*math.Min(math.Max(grade, -0.06), 0.15) + 10*f + 1.5*(r.Float64()-0.5)
	return math.Min(math.Max(hr, 95), 186)
}

// cadence returns a cadence, in steps a minute per leg for foot sports, or
// 0 if it isn't recorded.
func cadence(sportType string, r *rand.Rand) float64 {
	switch sportType {
	case "Hike":
		return math.Round(52 + 4*r.Float64())
	case "Ride":
		return math.Round(82 + 8*r.Float64())
	case "MountainBikeRide":
		return math.Round(72 + 8*r.Float64())
	case "BackcountrySki":
		// Not recorded.
		return 0
	}
	return math.Round(83 + 5*r.Float64())
}

// photoFractions returns where along the way photos are taken, in order.
func photoFractions(s sport, meters float64, r *rand.Rand) []float64 {
	n := 0
	if s.kmPerPhoto > 0 {
		// About one every kmPerPhoto, give or take one.
		n = min(max(int(math.Round(meters/1000/s.kmPerPhoto))+r.IntN(3)-1, 0), s.maxPhotos)
	}
	fractions := make([]float64, n)
	for i := range fractions {
		fractions[i] = 0.05 + 0.9*(float64(i)+r.Float64())/float64(max(n, 1))
	}
	// Two photos close together, as people do at the top.
	if n >= 4 {
		fractions[n/2] = fractions[n/2-1] + 0.004
	}
	return fractions
}

// offset returns the point east and north meters from p.
func offset(p geo.Point, east, north float64) geo.Point {
	return geo.Point{
		Lat: p.Lat + north/111_320,
		Lng: p.Lng + east/(111_320*math.Cos(p.Lat*math.Pi/180)),
		Ele: p.Ele,
	}
}

func round(v float64, decimals int) float64 {
	scale := math.Pow(10, float64(decimals))
	return math.Round(v*scale) / scale
}
