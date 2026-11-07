package fakestrava

import (
	"fmt"
	"html/template"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/geo"
)

// WebURL is the URL of the fake Strava's website, where activities are at
// /activities/<id> and the athlete at /athletes/<id>, as on Strava.
func (s *Server) WebURL() string { return s.baseURL }

var pageTemplate = template.Must(template.New("page").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} | Fake Strava</title>
<style>
body { font-family: system-ui, sans-serif; background: #f7f7fa; color: #242428; margin: 0; }
header { background: white; border-bottom: 1px solid #dfdfe8; padding: 12px 16px; display: flex; gap: 12px; align-items: baseline; }
header b { color: #fc5200; font-size: 20px; }
header span { color: #6d6d78; font-size: 14px; }
main { max-width: 860px; margin: 0 auto; padding: 24px 16px; display: flex; flex-direction: column; gap: 16px; }
.card { background: white; border-radius: 8px; box-shadow: 0 1px 3px rgba(0,0,0,.12); padding: 20px; }
h1 { margin: 0 0 4px; font-size: 26px; }
.meta { color: #6d6d78; font-size: 14px; }
.stats { display: flex; flex-wrap: wrap; gap: 24px; margin-top: 16px; }
.stats div { display: flex; flex-direction: column; }
.stats b { font-size: 22px; }
.stats span { color: #6d6d78; font-size: 12px; text-transform: uppercase; }
.description { white-space: pre-line; line-height: 1.5; margin-top: 16px; }
svg { width: 100%; height: auto; background: #eef3ea; border-radius: 6px; }
.photos { display: grid; grid-template-columns: repeat(auto-fill, minmax(180px, 1fr)); gap: 8px; }
.photos img { width: 100%; aspect-ratio: 4 / 3; object-fit: cover; border-radius: 6px; }
.photos figcaption { font-size: 13px; color: #6d6d78; }
ul { list-style: none; padding: 0; margin: 0; }
li { padding: 10px 0; border-top: 1px solid #eee; display: flex; justify-content: space-between; gap: 12px; }
a { color: #fc5200; text-decoration: none; }
a:hover { text-decoration: underline; }
.race { background: #fc5200; color: white; border-radius: 4px; font-size: 12px; padding: 1px 6px; margin-left: 6px; }
</style>
</head>
<body>
<header><b>Fake Strava</b><span>A stand-in for Strava, for running trailrunn.ing locally. Nothing here is real.</span></header>
<main>
{{if .Activity}}{{with .Activity}}
<section class="card">
<h1>{{.Name}}{{if .Race}}<span class="race">Race</span>{{end}}</h1>
<div class="meta"><a href="{{$.AthleteURL}}">Demo Runner</a> · {{.Sport}} · {{.Start}}</div>
<div class="stats">
<div><b>{{.Distance}}</b><span>Distance</span></div>
<div><b>{{.MovingTime}}</b><span>Moving time</span></div>
{{if .Gain}}<div><b>{{.Gain}}</b><span>Elevation</span></div>{{end}}
{{if .HeartRate}}<div><b>{{.HeartRate}}</b><span>Avg heart rate</span></div>{{end}}
</div>
{{if .Description}}<p class="description">{{.Description}}</p>{{end}}
</section>
{{if .Track}}<section class="card"><svg viewBox="0 0 600 300" role="img" aria-label="The track"><path d="{{.Track}}" fill="none" stroke="#fc5200" stroke-width="3" stroke-linejoin="round"/></svg></section>{{end}}
{{if .Photos}}<section class="card photos">{{range .Photos}}<figure><img src="{{.URL}}" alt="{{.Caption}}" loading="lazy"><figcaption>{{.Caption}}</figcaption></figure>{{end}}</section>{{end}}
{{end}}{{else}}
<section class="card">
<h1>Demo Runner</h1>
<div class="meta">Squamish, British Columbia · {{len .Activities}} activities</div>
</section>
<section class="card">
<ul>{{range .Activities}}<li><a href="{{.URL}}">{{.Name}}</a><span class="meta">{{.Sport}} · {{.Distance}} · {{.Start}}</span></li>{{end}}</ul>
</section>
{{end}}
</main>
</body>
</html>
`))

// activityPage is an activity as shown on its page.
type activityPage struct {
	URL, Name, Sport, Start    string
	Distance, MovingTime, Gain string
	HeartRate, Description     string
	Race                       bool
	Track                      string
	Photos                     []struct{ URL, Caption string }
}

func (s *Server) activityPageOf(a *activity) activityPage {
	p := activityPage{
		URL:         fmt.Sprintf("%s/activities/%d", s.baseURL, a.id),
		Name:        a.name,
		Sport:       a.sportType,
		Start:       a.start.In(timezone).Format("Monday, January 2, 2006 at 15:04"),
		Distance:    fmt.Sprintf("%.1f km", a.distance/1000),
		MovingTime:  strings.TrimSuffix((time.Duration(a.movingTime) * time.Second).Round(time.Minute).String(), "0s"),
		Description: a.description,
		Race:        a.race,
		Track:       svgPath(a.polyline),
	}
	if a.gain > 0 {
		p.Gain = fmt.Sprintf("%.0f m", a.gain)
	}
	if a.avgHR > 0 {
		p.HeartRate = fmt.Sprintf("%.0f bpm", a.avgHR)
	}
	for i, ph := range a.photos {
		p.Photos = append(p.Photos, struct{ URL, Caption string }{fmt.Sprintf("%s/photos/%d/%d.jpg", s.baseURL, a.id, i+1), ph.caption})
	}
	return p
}

// activityPageHandler shows an activity.
func (s *Server) activityPageHandler(w http.ResponseWriter, r *http.Request) {
	_, byID, ok := s.loadOrFail(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a := byID[id]
	if err != nil || a == nil {
		http.NotFound(w, r)
		return
	}
	page := s.activityPageOf(a)
	s.render(w, map[string]any{
		"Title":      page.Name,
		"Activity":   page,
		"AthleteURL": fmt.Sprintf("%s/athletes/%d", s.baseURL, athleteID),
	})
}

// athletePageHandler shows the athlete and their activities.
func (s *Server) athletePageHandler(w http.ResponseWriter, r *http.Request) {
	if r.PathValue("id") != strconv.Itoa(athleteID) {
		http.NotFound(w, r)
		return
	}
	activities, _, ok := s.loadOrFail(w, r)
	if !ok {
		return
	}
	pages := make([]activityPage, len(activities))
	for i, a := range activities {
		pages[i] = s.activityPageOf(a)
	}
	s.render(w, map[string]any{"Title": "Demo Runner", "Activities": pages})
}

func (s *Server) render(w http.ResponseWriter, data map[string]any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// svgPath returns an SVG path of a polyline fitted into 600 by 300, or an
// empty string without one.
func svgPath(polyline string) string {
	points, err := geo.DecodePolyline(polyline)
	if err != nil || len(points) < 2 {
		return ""
	}
	b, _ := geo.BoundsOf(points)
	scale := math.Cos((b.MinLat + b.MaxLat) / 2 * math.Pi / 180)
	width := math.Max((b.MaxLng-b.MinLng)*scale, 1e-9)
	height := math.Max(b.MaxLat-b.MinLat, 1e-9)
	fit := math.Min(560/width, 260/height)
	offsetX := (600 - width*fit) / 2
	offsetY := (300 - height*fit) / 2
	var path strings.Builder
	for i, p := range points {
		x := offsetX + (p.Lng-b.MinLng)*scale*fit
		y := offsetY + (b.MaxLat-p.Lat)*fit
		if i == 0 {
			fmt.Fprintf(&path, "M%.1f %.1f", x, y)
		} else {
			fmt.Fprintf(&path, "L%.1f %.1f", x, y)
		}
	}
	return path.String()
}
