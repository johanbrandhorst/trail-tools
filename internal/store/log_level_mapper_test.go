package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/tracelog"
)

// recordingLogger records the last log it was given.
type recordingLogger struct {
	level tracelog.LogLevel
	data  map[string]any
}

func (l *recordingLogger) Log(_ context.Context, level tracelog.LogLevel, _ string, data map[string]any) {
	l.level, l.data = level, data
}

func TestLogLevelMapper(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		level tracelog.LogLevel
		err   error
		want  tracelog.LogLevel
	}{
		{"info", tracelog.LogLevelInfo, nil, tracelog.LogLevelDebug},
		{"error", tracelog.LogLevelError, errors.New("syntax error"), tracelog.LogLevelError},
		{"canceled", tracelog.LogLevelError, fmt.Errorf("timeout: %w", context.Canceled), tracelog.LogLevelDebug},
		{"deadline", tracelog.LogLevelError, context.DeadlineExceeded, tracelog.LogLevelError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := &recordingLogger{}
			data := map[string]any{"sql": "select $1", "args": []any{"secret"}}
			if tc.err != nil {
				data["err"] = tc.err
			}
			(&logLevelMapper{wrapped: rec}).Log(context.Background(), tc.level, "Query", data)
			if rec.level != tc.want {
				t.Errorf("logged at %v, want %v", rec.level, tc.want)
			}
			if _, ok := rec.data["args"]; ok {
				t.Errorf("logged the query's arguments: %v", rec.data)
			}
			if rec.data["sql"] != "select $1" {
				t.Errorf("didn't log the SQL: %v", rec.data)
			}
		})
	}
}
