package timeouts_test

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/johanbrandhorst/trail-tools/internal/timeouts"
)

// result is what a test handler saw.
type result struct {
	body       string
	readErr    error
	ctxErr     error
	ranFor     time.Duration
	hadTimeout bool
}

// newServer serves a handler that reads the body, then waits for work or
// for its context to be done, and sends what it saw to results.
func newServer(t *testing.T, conf timeouts.Config, work time.Duration, results chan<- result) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(timeouts.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		body, err := io.ReadAll(r.Body)
		res := result{body: string(body), readErr: err}
		_, res.hadTimeout = r.Context().Deadline()
		select {
		case <-time.After(work):
		case <-r.Context().Done():
		}
		res.ctxErr, res.ranFor = r.Context().Err(), time.Since(start)
		results <- res
	}), conf))
	t.Cleanup(srv.Close)
	return srv
}

func TestStalledBody(t *testing.T) {
	t.Parallel()
	results := make(chan result, 1)
	srv := newServer(t, timeouts.Config{BodyRead: 100 * time.Millisecond}, 0, results)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	// Send half the body and stall.
	fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 10\r\n\r\nhello")
	res := <-results
	if res.readErr == nil {
		t.Errorf("reading a stalled body succeeded with %q, want a timeout", res.body)
	}
}

func TestSlowHandlerAfterBody(t *testing.T) {
	t.Parallel()
	results := make(chan result, 1)
	// The handler runs for longer than reading the body may take, which
	// mustn't cancel it.
	srv := newServer(t, timeouts.Config{BodyRead: 50 * time.Millisecond}, 300*time.Millisecond, results)
	for _, method := range []string{http.MethodPost, http.MethodGet} {
		req, err := http.NewRequest(method, srv.URL, nil)
		if err != nil {
			t.Fatal(err)
		}
		if method == http.MethodPost {
			req.Body = io.NopCloser(strings.NewReader("hello"))
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		resp.Body.Close()
		res := <-results
		if res.readErr != nil || res.ctxErr != nil {
			t.Errorf("%s: got read error %v and context error %v, want none", method, res.readErr, res.ctxErr)
		}
	}
}

func TestHandlerTimeout(t *testing.T) {
	t.Parallel()
	results := make(chan result, 1)
	srv := newServer(t, timeouts.Config{Handler: 100 * time.Millisecond, LongPaths: []string{"/long"}}, 500*time.Millisecond, results)

	// The handler's context is canceled after the timeout.
	resp, err := srv.Client().Get(srv.URL + "/short")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if res := <-results; res.ctxErr == nil || res.ranFor > 400*time.Millisecond {
		t.Errorf("got context error %v after %v, want it canceled after about 100ms", res.ctxErr, res.ranFor)
	}

	// Long paths run on.
	resp, err = srv.Client().Get(srv.URL + "/long")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	resp.Body.Close()
	if res := <-results; res.ctxErr != nil || res.hadTimeout {
		t.Errorf("got context error %v and timeout %v for a long path, want none", res.ctxErr, res.hadTimeout)
	}
}

func TestKeepAliveAfterTimeouts(t *testing.T) {
	t.Parallel()
	results := make(chan result, 2)
	srv := newServer(t, timeouts.Config{BodyRead: 50 * time.Millisecond, Handler: 50 * time.Millisecond}, 0, results)
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer conn.Close()
	r := bufio.NewReader(conn)
	// The deadlines of a request don't carry over to the next one on the same
	// connection, sent after they've passed.
	for i := range 2 {
		fmt.Fprintf(conn, "POST / HTTP/1.1\r\nHost: test\r\nContent-Length: 5\r\n\r\nhello")
		resp, err := http.ReadResponse(r, nil)
		if err != nil {
			t.Fatalf("request %d: ReadResponse: %v", i+1, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if res := <-results; res.readErr != nil || res.body != "hello" {
			t.Errorf("request %d: got body %q and error %v, want hello", i+1, res.body, res.readErr)
		}
		time.Sleep(150 * time.Millisecond)
	}
}
