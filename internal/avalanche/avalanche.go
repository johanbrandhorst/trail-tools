// Package avalanche is a client for the public avalanche forecasts of
// Avalanche Canada, which also publishes those of Parks Canada, Kananaskis
// Country and Avalanche Québec. Each forecast covers an area for the next
// three days, with a danger rating for each elevation band and the
// avalanche problems to expect.
package avalanche

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/geo"
)

// DefaultURL is the base URL of Avalanche Canada's API.
const DefaultURL = "https://api.avalanche.ca"

// userAgent identifies the application to Avalanche Canada.
const userAgent = "trail-tools/0.1 (+https://github.com/bs-tools/trailrunn.ing)"

// maxResponseBytes bounds what is read of a response. A forecast is about
// 15 kB, mostly its HTML summaries, so this leaves plenty of room while
// keeping a broken response from filling the server's 512 MiB.
const maxResponseBytes = 1 << 20

const (
	// cacheTTL is how long a forecast is kept. Forecasts are issued once a
	// day, in the afternoon, and route forecasts are often asked for again
	// while a route is changed, so this saves most requests while keeping
	// a new forecast at most this late.
	cacheTTL = 30 * time.Minute
	// maxCacheEntries bounds the cache, at a few kilobytes per entry.
	maxCacheEntries = 500
	// cachePrecision rounds points to a hundredth of a degree, about a
	// kilometer, for the cache key: forecast areas are tens of kilometers
	// wide, so nearby points of a route share their forecast.
	cachePrecision = 100
)

// Danger is a danger rating of the North American Public Avalanche Danger
// Scale, from 1 (low) to 5 (extreme), or 0 if there is no rating, like
// early in the season or in spring.
type Danger int

const (
	DangerNone Danger = iota
	DangerLow
	DangerModerate
	DangerConsiderable
	DangerHigh
	DangerExtreme
)

// String returns the name of the danger, like "Considerable".
func (d Danger) String() string {
	switch d {
	case DangerLow:
		return "Low"
	case DangerModerate:
		return "Moderate"
	case DangerConsiderable:
		return "Considerable"
	case DangerHigh:
		return "High"
	case DangerExtreme:
		return "Extreme"
	}
	return "No rating"
}

// dangers maps the API's rating values to the danger scale. Other values
// (earlyseason, spring, offseason, norating, noforecast) aren't on it.
var dangers = map[string]Danger{
	"low":          DangerLow,
	"moderate":     DangerModerate,
	"considerable": DangerConsiderable,
	"high":         DangerHigh,
	"extreme":      DangerExtreme,
}

// unforecast are the rating values that mean there is no forecast at all,
// rather than an unrated one like early season.
var unforecast = map[string]bool{"offseason": true, "noforecast": true}

// Rating is the danger rating of an elevation band on a day.
type Rating struct {
	// Danger is the rating on the danger scale, DangerNone if unrated.
	Danger Danger
	// Description is how the forecast names it, like "3 - Considerable"
	// or "Early Season".
	Description string
}

// Day is the danger ratings of a day.
type Day struct {
	// Date is the local date in the forecast's time zone, YYYY-MM-DD.
	Date string
	// The ratings above, near and below treeline.
	Alpine, Treeline, BelowTreeline Rating
}

// Max returns the highest danger of the day's elevation bands.
func (d Day) Max() Danger {
	return max(d.Alpine.Danger, d.Treeline.Danger, d.BelowTreeline.Danger)
}

// Problem is an avalanche problem: a kind of avalanche to expect.
type Problem struct {
	// Type is the problem's identifier, like "windslab" or
	// "persistentslab", and Name its name, like "Wind Slab".
	Type, Name string
	// Elevations are the bands it's found in, like "Alpine" and
	// "Treeline".
	Elevations []string
	// Aspects are the directions of the slopes it's found on, like "N" or
	// "SE".
	Aspects []string
	// Likelihood is how likely avalanches are, like "Possible to likely".
	Likelihood string
	// MinSize and MaxSize are the expected sizes on the Canadian
	// destructive size scale, from 1 (relatively harmless to people) to 5
	// (could destroy a village or forest), in half steps. 0 if not given.
	MinSize, MaxSize float64
	// Comment is the forecaster's description, in plain text.
	Comment string
}

// Forecast is an avalanche forecast for an area.
type Forecast struct {
	// Title names the forecast's area, like "Glacier".
	Title string
	// Forecaster is who wrote it, like "Avalanche Canada" or "Parks
	// Canada".
	Forecaster string
	// URL is the forecast on avalanche.ca.
	URL string
	// IssueTime is when it was issued, and ValidUntil when it expires,
	// zero if not given.
	IssueTime, ValidUntil time.Time
	// Highlights is the forecast's summary, in plain text.
	Highlights string
	// Confidence is the forecaster's confidence, like "Moderate", empty
	// if not rated.
	Confidence string
	// Days are the ratings of the next days, in order.
	Days []Day
	// Problems are the avalanche problems to expect.
	Problems []Problem
	// TravelAdvice is advice on choosing terrain and traveling in it.
	TravelAdvice []string
}

// Day returns the ratings of a local date, YYYY-MM-DD, if the forecast
// covers it.
func (f *Forecast) Day(date string) (Day, bool) {
	for _, d := range f.Days {
		if d.Date == date {
			return d, true
		}
	}
	return Day{}, false
}

// Client gets avalanche forecasts.
type Client struct {
	baseURL string
	client  *http.Client
	nowFn   func() time.Time

	mu    sync.Mutex
	cache map[cacheKey]cacheEntry
}

type cacheKey struct{ lat, lng int }

// cacheEntry is a cached forecast, nil for a point without one.
type cacheEntry struct {
	forecast *Forecast
	expires  time.Time
}

// New creates a client of the API at baseURL, usually DefaultURL.
func New(baseURL string, client *http.Client) *Client {
	return &Client{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		client:  client,
		nowFn:   time.Now,
		cache:   make(map[cacheKey]cacheEntry),
	}
}

// Forecast returns the current avalanche forecast for a point, or nil if
// there is none: outside the forecast areas, which only cover Canada's
// mountains, or out of season, when they're rated "Summer Conditions" or
// "No Forecast". Forecasts are cached for a while.
func (c *Client) Forecast(ctx context.Context, point geo.Point) (*Forecast, error) {
	key := cacheKey{int(math.Round(point.Lat * cachePrecision)), int(math.Round(point.Lng * cachePrecision))}
	now := c.nowFn()
	c.mu.Lock()
	entry, ok := c.cache[key]
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.forecast, nil
	}
	forecast, err := c.fetch(ctx, point)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.cache) >= maxCacheEntries {
		for k, e := range c.cache {
			if !now.Before(e.expires) {
				delete(c.cache, k)
			}
		}
		// If none had expired, drop any to make room; map iteration order
		// is random, so this is a random eviction.
		for k := range c.cache {
			if len(c.cache) < maxCacheEntries {
				break
			}
			delete(c.cache, k)
		}
	}
	c.cache[key] = cacheEntry{forecast: forecast, expires: now.Add(cacheTTL)}
	return forecast, nil
}

// fetch gets the forecast of a point from the API.
func (c *Client) fetch(ctx context.Context, point geo.Point) (*Forecast, error) {
	query := url.Values{}
	query.Set("lat", strconv.FormatFloat(point.Lat, 'f', 5, 64))
	query.Set("long", strconv.FormatFloat(point.Lng, 'f', 5, 64))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/forecasts/en/products/point?"+query.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create avalanche forecast request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to get avalanche forecast: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get avalanche forecast: %s", resp.Status)
	}
	var product product
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBytes)).Decode(&product); err != nil {
		return nil, fmt.Errorf("failed to decode avalanche forecast: %w", err)
	}
	return product.toForecast()
}

// product is the API's forecast product. A point outside the forecast
// areas gets one with empty fields.
type product struct {
	URL    string `json:"url"`
	Report struct {
		Forecaster string  `json:"forecaster"`
		DateIssued string  `json:"dateIssued"`
		ValidUntil *string `json:"validUntil"`
		Timezone   string  `json:"timezone"`
		Title      string  `json:"title"`
		Highlights string  `json:"highlights"`
		Confidence *struct {
			Rating valueDisplay `json:"rating"`
		} `json:"confidence"`
		DangerRatings []struct {
			// Date is an instant on the day rated: its date in the
			// report's time zone is the day.
			Date    valueDisplay `json:"date"`
			Ratings struct {
				Alpine        bandRating `json:"alp"`
				Treeline      bandRating `json:"tln"`
				BelowTreeline bandRating `json:"btl"`
			} `json:"ratings"`
		} `json:"dangerRatings"`
		Problems []struct {
			Type    valueDisplay `json:"type"`
			Comment string       `json:"comment"`
			Data    struct {
				Elevations   []valueDisplay `json:"elevations"`
				Aspects      []valueDisplay `json:"aspects"`
				Likelihood   valueDisplay   `json:"likelihood"`
				ExpectedSize struct {
					Min string `json:"min"`
					Max string `json:"max"`
				} `json:"expectedSize"`
			} `json:"data"`
		} `json:"problems"`
		TerrainAndTravelAdvice []string `json:"terrainAndTravelAdvice"`
	} `json:"report"`
	Owner valueDisplay `json:"owner"`
}

type valueDisplay struct {
	Value   string `json:"value"`
	Display string `json:"display"`
}

type bandRating struct {
	Rating valueDisplay `json:"rating"`
}

func (r bandRating) toRating() Rating {
	return Rating{Danger: dangers[r.Rating.Value], Description: r.Rating.Display}
}

// toForecast converts the product, returning nil if it has no forecast.
func (p *product) toForecast() (*Forecast, error) {
	r := &p.Report
	if p.URL == "" || len(r.DangerRatings) == 0 {
		return nil, nil
	}
	loc, err := time.LoadLocation(r.Timezone)
	if err != nil {
		return nil, fmt.Errorf("failed to load the avalanche forecast's time zone %q: %w", r.Timezone, err)
	}
	f := &Forecast{
		Title:        r.Title,
		Forecaster:   cmp.Or(r.Forecaster, p.Owner.Display),
		URL:          p.URL,
		Highlights:   plainText(r.Highlights),
		TravelAdvice: r.TerrainAndTravelAdvice,
	}
	f.IssueTime, _ = time.Parse(time.RFC3339, r.DateIssued)
	if r.ValidUntil != nil {
		f.ValidUntil, _ = time.Parse(time.RFC3339, *r.ValidUntil)
	}
	if r.Confidence != nil && r.Confidence.Rating.Value != "noRating" {
		f.Confidence = r.Confidence.Rating.Display
	}
	forecast := false
	for _, d := range r.DangerRatings {
		t, err := time.Parse(time.RFC3339, d.Date.Value)
		if err != nil {
			return nil, fmt.Errorf("failed to parse the avalanche forecast's date %q: %w", d.Date.Value, err)
		}
		f.Days = append(f.Days, Day{
			Date:          t.In(loc).Format(time.DateOnly),
			Alpine:        d.Ratings.Alpine.toRating(),
			Treeline:      d.Ratings.Treeline.toRating(),
			BelowTreeline: d.Ratings.BelowTreeline.toRating(),
		})
		for _, band := range []bandRating{d.Ratings.Alpine, d.Ratings.Treeline, d.Ratings.BelowTreeline} {
			forecast = forecast || !unforecast[band.Rating.Value]
		}
	}
	if !forecast {
		return nil, nil
	}
	for _, pr := range r.Problems {
		problem := Problem{
			Type:       pr.Type.Value,
			Name:       pr.Type.Display,
			Likelihood: likelihood(pr.Data.Likelihood.Value),
			Comment:    plainText(pr.Comment),
		}
		problem.MinSize, _ = strconv.ParseFloat(pr.Data.ExpectedSize.Min, 64)
		problem.MaxSize, _ = strconv.ParseFloat(pr.Data.ExpectedSize.Max, 64)
		for _, e := range pr.Data.Elevations {
			problem.Elevations = append(problem.Elevations, e.Display)
		}
		// The aspects aren't in compass order.
		for _, a := range compass {
			for _, got := range pr.Data.Aspects {
				if got.Value == a {
					problem.Aspects = append(problem.Aspects, strings.ToUpper(a))
				}
			}
		}
		f.Problems = append(f.Problems, problem)
	}
	return f, nil
}

// compass is the order of the aspects.
var compass = []string{"n", "ne", "e", "se", "s", "sw", "w", "nw"}

// likelihoods names the API's likelihoods of avalanches, which it only
// gives as identifiers.
var likelihoods = map[string]string{
	"unlikely":          "Unlikely",
	"possible_unlikely": "Unlikely to possible",
	"possible":          "Possible",
	"likely_possible":   "Possible to likely",
	"likely":            "Likely",
	"veryLikely_likely": "Likely to very likely",
	"veryLikely":        "Very likely",
	"certain":           "Almost certain",
}

func likelihood(value string) string {
	if name, ok := likelihoods[value]; ok {
		return name
	}
	return strings.ReplaceAll(value, "_", " to ")
}

var (
	htmlParagraph = regexp.MustCompile(`(?i)</p>|<br\s*/?>|</li>`)
	htmlTag       = regexp.MustCompile(`<[^>]*>`)
)

// plainText converts the forecast's HTML to plain text, with paragraphs
// separated by blank lines.
func plainText(s string) string {
	s = htmlParagraph.ReplaceAllString(s, "\n")
	s = html.UnescapeString(htmlTag.ReplaceAllString(s, " "))
	var paragraphs []string
	for line := range strings.SplitSeq(s, "\n") {
		if line = strings.Join(strings.Fields(line), " "); line != "" {
			paragraphs = append(paragraphs, line)
		}
	}
	return strings.Join(paragraphs, "\n\n")
}

// bandPhrases name the elevation bands in a sentence.
var bandPhrases = [3]string{"in the alpine", "at treeline", "below treeline"}

// Warning returns a warning about the day's danger if it's High or
// Extreme in a band, when travel in avalanche terrain isn't recommended,
// and an empty string otherwise.
func (f *Forecast) Warning(day Day) string {
	worst := day.Max()
	if worst < DangerHigh {
		return ""
	}
	var bands []string
	for i, r := range [3]Rating{day.Alpine, day.Treeline, day.BelowTreeline} {
		if r.Danger == worst {
			bands = append(bands, bandPhrases[i])
		}
	}
	where := strings.Join(bands, " and ")
	if len(bands) == 3 {
		where = "at all elevations"
	}
	advice := "travel in avalanche terrain is not recommended. Keep to simple terrain away from avalanche paths, or choose another day"
	if worst == DangerExtreme {
		advice = "avoid all avalanche terrain, and choose another day"
	}
	date, _ := time.Parse(time.DateOnly, day.Date)
	return fmt.Sprintf("Avalanche danger is %s %s on %s in the %s forecast by %s: %s. Read the full forecast at %s.",
		worst, where, date.Format("Monday, January 2"), f.Title, f.Forecaster, advice, f.URL)
}
