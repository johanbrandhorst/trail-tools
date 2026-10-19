// Package timeouts bounds how long the server's requests take, so slow or
// stalled clients and runaway handlers can't hold the connections, memory and
// database connections of the server's single instance for the 15 minutes
// Cloud Run allows a request.
package timeouts

import (
	"context"
	"net/http"
	"slices"
	"time"
)

// Config sets the timeouts of requests.
type Config struct {
	// BodyRead bounds reading a request's body, from the start of the
	// request. Zero means no limit.
	BodyRead time.Duration
	// Handler bounds how long a handler runs: its request's context is
	// canceled after it, and writing the response fails a little later.
	// Zero means no limit.
	Handler time.Duration
	// LongPaths are the paths of handlers that run for longer than Handler,
	// such as streams and scheduled jobs, which bound themselves.
	LongPaths []string
}

// writeGrace is how long a handler has to write its response after its
// context is canceled, such as an error saying it took too long.
const writeGrace = 5 * time.Second

// Handler applies the timeouts of conf to the requests of next. It must wrap
// the handler given to the http.Server, so it sees the server's own
// ResponseWriter.
//
// The http.Server's own ReadTimeout and WriteTimeout can't be used instead:
// they apply to every request alike, and with HTTP/1, which Cloud Run speaks
// to the server, an expired ReadTimeout cancels the context of a handler
// still running.
func Handler(next http.Handler, conf Config) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rc := http.NewResponseController(w)
		if conf.BodyRead > 0 && r.Body != nil && r.Body != http.NoBody {
			// With HTTP/1, the server lifts the deadline once the body has been
			// read, to read the connection in the background. Requests without
			// a body are read in the background from the start, where a
			// deadline would cancel the request's context, so they get none.
			_ = rc.SetReadDeadline(time.Now().Add(conf.BodyRead))
		}
		if conf.Handler > 0 && !slices.Contains(conf.LongPaths, r.URL.Path) {
			ctx, cancel := context.WithTimeout(r.Context(), conf.Handler)
			defer cancel()
			r = r.WithContext(ctx)
			// The deadline stays on an HTTP/1 connection for its next requests,
			// so it's lifted again at the end.
			if rc.SetWriteDeadline(time.Now().Add(conf.Handler+writeGrace)) == nil {
				defer func() { _ = rc.SetWriteDeadline(time.Time{}) }()
			}
		}
		next.ServeHTTP(w, r)
	})
}
