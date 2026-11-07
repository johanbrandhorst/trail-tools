package races

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/johanbrandhorst/trail-tools/internal/races/internal/gen"
	"github.com/johanbrandhorst/trail-tools/internal/store"
)

// Event groups races that belong together, e.g. a 50 mile race on the
// Saturday and a 50K on the Sunday.
type Event struct {
	ID         pgtype.UUID
	UserID     pgtype.UUID
	Name       string
	Location   string
	WebsiteURL string
	Notes      string
	CreateTime time.Time
	UpdateTime time.Time
}

// CreateEvent stores a new event.
func (r *Repository) CreateEvent(ctx context.Context, event *Event) (*Event, error) {
	dbEvent, err := r.q.CreateRaceEvent(ctx, &gen.CreateRaceEventParams{
		UserID:     event.UserID,
		Name:       event.Name,
		Location:   event.Location,
		WebsiteURL: event.WebsiteURL,
		Notes:      event.Notes,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create event: %w", err)
	}
	return eventFromDB(dbEvent), nil
}

// GetEvent gets one of the user's events.
func (r *Repository) GetEvent(ctx context.Context, userID, eventID pgtype.UUID) (*Event, error) {
	dbEvent, err := r.q.GetRaceEvent(ctx, &gen.GetRaceEventParams{ID: eventID, UserID: userID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get event: %w", err)
	}
	return eventFromDB(dbEvent), nil
}

// ListEvents lists the user's events by name.
func (r *Repository) ListEvents(ctx context.Context, userID pgtype.UUID) ([]*Event, error) {
	dbEvents, err := r.q.ListRaceEvents(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to list events: %w", err)
	}
	events := make([]*Event, len(dbEvents))
	for i, dbEvent := range dbEvents {
		events[i] = eventFromDB(dbEvent)
	}
	return events, nil
}

// UpdateEvent replaces an event.
func (r *Repository) UpdateEvent(ctx context.Context, event *Event) (*Event, error) {
	dbEvent, err := r.q.UpdateRaceEvent(ctx, &gen.UpdateRaceEventParams{
		ID:         event.ID,
		UserID:     event.UserID,
		Name:       event.Name,
		Location:   event.Location,
		WebsiteURL: event.WebsiteURL,
		Notes:      event.Notes,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrNotFound
		}
		return nil, fmt.Errorf("failed to update event: %w", err)
	}
	return eventFromDB(dbEvent), nil
}

// DeleteEvent deletes an event. Its races are kept, outside any event.
func (r *Repository) DeleteEvent(ctx context.Context, userID, eventID pgtype.UUID) error {
	n, err := r.q.DeleteRaceEvent(ctx, &gen.DeleteRaceEventParams{ID: eventID, UserID: userID})
	if err != nil {
		return fmt.Errorf("failed to delete event: %w", err)
	}
	if n == 0 {
		return store.ErrNotFound
	}
	return nil
}

func eventFromDB(e *gen.RaceEvent) *Event {
	return &Event{
		ID:         e.ID,
		UserID:     e.UserID,
		Name:       e.Name,
		Location:   e.Location,
		WebsiteURL: e.WebsiteURL,
		Notes:      e.Notes,
		CreateTime: e.CreateTime.Time,
		UpdateTime: e.UpdateTime.Time,
	}
}
