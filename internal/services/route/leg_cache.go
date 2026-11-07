package route

import (
	"container/list"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/routes"
)

const (
	// maxCachedLegs is the number of recently routed legs kept in memory.
	maxCachedLegs = 2000
	// legCacheTTL is how long a routed leg is reused, after which it is
	// routed again to pick up map and heatmap changes.
	legCacheTTL = time.Hour
)

// legKey identifies a leg routed for a user. Legs are cached per user as
// heatmap legs depend on the user's activities.
type legKey struct {
	userID   pgtype.UUID
	from, to routes.LatLng
	profile  routes.Profile
}

type legCacheEntry struct {
	key    legKey
	leg    routes.Leg
	expiry time.Time
}

// legCache keeps recently routed legs, so that editing a route only routes
// the legs that changed, even before it is saved. The least recently used
// legs are evicted first.
type legCache struct {
	mu      sync.Mutex
	entries map[legKey]*list.Element
	// order has the most recently used entry at the front.
	order *list.List
}

func newLegCache() *legCache {
	return &legCache{entries: map[legKey]*list.Element{}, order: list.New()}
}

// get returns a cached leg, or the reversed leg if it was routed in the
// opposite direction.
func (c *legCache) get(userID pgtype.UUID, from, to routes.LatLng, profile routes.Profile) (routes.Leg, bool) {
	if leg, ok := c.getExact(userID, from, to, profile); ok {
		return leg, true
	}
	if leg, ok := c.getExact(userID, to, from, profile); ok {
		return leg.Reversed(), true
	}
	return routes.Leg{}, false
}

func (c *legCache) getExact(userID pgtype.UUID, from, to routes.LatLng, profile routes.Profile) (routes.Leg, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := legKey{userID: userID, from: from, to: to, profile: profile}
	elem, ok := c.entries[key]
	if !ok {
		return routes.Leg{}, false
	}
	entry := elem.Value.(*legCacheEntry)
	if time.Now().After(entry.expiry) {
		c.order.Remove(elem)
		delete(c.entries, key)
		return routes.Leg{}, false
	}
	c.order.MoveToFront(elem)
	return entry.leg, true
}

func (c *legCache) put(userID pgtype.UUID, leg routes.Leg) {
	c.mu.Lock()
	defer c.mu.Unlock()
	key := legKey{userID: userID, from: leg.From, to: leg.To, profile: leg.Profile}
	entry := &legCacheEntry{key: key, leg: leg, expiry: time.Now().Add(legCacheTTL)}
	if elem, ok := c.entries[key]; ok {
		elem.Value = entry
		c.order.MoveToFront(elem)
		return
	}
	c.entries[key] = c.order.PushFront(entry)
	for c.order.Len() > maxCachedLegs {
		oldest := c.order.Back()
		c.order.Remove(oldest)
		delete(c.entries, oldest.Value.(*legCacheEntry).key)
	}
}
