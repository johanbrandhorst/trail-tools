package strava

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/bs-tools/trailrunn.ing/internal/errreport"
)

// SyncPath is the path of the scheduled sync endpoint.
const SyncPath = "/internal/strava/sync"

// scheduledSyncBudget is how long a scheduled sync runs for. It must be less
// than the request timeout of the server.
const scheduledSyncBudget = 10 * time.Minute

// ScheduledSync is another sync run by the scheduled sync, alongside
// Strava's, like Garmin's, so one scheduler job runs them all.
type ScheduledSync func(ctx context.Context) error

// SyncHandler returns a handler that syncs all connections that are due a
// sync, for running from a scheduler such as Cloud Scheduler. syncer is nil
// if Strava isn't configured or syncs in the background, and others run at
// the same time. It must be protected by authorization.
func SyncHandler(report *errreport.Reporter, syncer *Syncer, others ...ScheduledSync) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), scheduledSyncBudget)
		defer cancel()
		syncs := others
		if syncer != nil {
			syncs = append([]ScheduledSync{syncer.SyncDue}, others...)
		}
		var (
			wg   sync.WaitGroup
			mu   sync.Mutex
			errs []error
		)
		for _, sync := range syncs {
			wg.Go(func() {
				if err := sync(ctx); err != nil && ctx.Err() == nil {
					mu.Lock()
					errs = append(errs, err)
					mu.Unlock()
				}
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			report.Report(ctx, "Scheduled sync failed", err)
			http.Error(w, "sync failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}
