package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/tracelog"
)

// logLevelMapper filters pgx's query logs before passing them on. It lowers
// tracelog.LogLevelInfo to tracelog.LogLevelDebug, and failures of canceled
// queries, which the client caused by going away, to tracelog.LogLevelDebug
// too.
//
// It also drops the queries' arguments. They include secrets, like session
// tokens and Strava's OAuth tokens, and the logs are readable by everyone
// with viewer access to the project. The SQL and the error are enough to
// tell which query failed and why.
type logLevelMapper struct {
	wrapped tracelog.Logger
}

func (l *logLevelMapper) Log(ctx context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	// tracelog makes a new map for each log, so it's ours to change.
	delete(data, "args")
	if level == tracelog.LogLevelInfo {
		level = tracelog.LogLevelDebug
	}
	if err, ok := data["err"].(error); ok && level == tracelog.LogLevelError && errors.Is(err, context.Canceled) {
		level = tracelog.LogLevelDebug
	}
	l.wrapped.Log(ctx, level, msg, data)
}
