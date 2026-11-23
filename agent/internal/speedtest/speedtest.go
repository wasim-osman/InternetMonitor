// Package speedtest runs HTTP download tests and keeps the last 50 results.
package speedtest

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"sync"
	"time"

	"imonitord/internal/config"
	"imonitord/internal/stats"
)

const (
	HistoryLen  = 50
	MaxDuration = 30 * time.Second
)

type Result struct {
	At         int64    `json:"at"`
	URL        string   `json:"url"`
	Bytes      int64    `json:"bytes"`
	DurationMs int64    `json:"durationMs"`
	DownMbps   *float64 `json:"downMbps"`
	LatencyMs  *float64 `json:"latencyMs"`
	Error      *string  `json:"error"`
}

var ErrBusy = errors.New("a speed test is already running")

type Runner struct {
	mu      sync.Mutex
	running bool
	path    string
	history []Result // newest first
	client  *http.Client
}

func New(dir string) *Runner {
	r := &Runner{
		path:   filepath.Join(dir, "speedtest.json"),
		client: &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}},
	}
	if data, err := os.ReadFile(r.path); err == nil {
		json.Unmarshal(data, &r.history)
	}
	return r
}

func (r *Runner) History() []Result {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Result, len(r.history))
	copy(out, r.history)
	return out
}

// Reset clears the history (used by reset-history).
func (r *Runner) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = nil
	os.Remove(r.path)
}

func (r *Runner) LastAt() int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.history) == 0 {
		return 0
	}
	return r.history[0].At
}

// Run performs one test; returns ErrBusy if one is already in progress.
func (r *Runner) Run(ctx context.Context, url string) (Result, error) {
	r.mu.Lock()
	if r.running {
		r.mu.Unlock()
		return Result{}, ErrBusy
	}
	r.running = true
	r.mu.Unlock()
	res := r.measure(ctx, url)
	r.mu.Lock()
	r.running = false
	r.history = append([]Result{res}, r.history...)
	if len(r.history) > HistoryLen {
		r.history = r.history[:HistoryLen]
	}
	data, _ := json.Marshal(r.history)
	r.mu.Unlock()
	config.WriteFileAtomic(r.path, data, 0o600)
	return res, nil
}

func (r *Runner) measure(ctx context.Context, url string) Result {
	res := Result{At: time.Now().Unix(), URL: url}
	fail := func(err error) Result {
		s := err.Error()
		res.Error = &s
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, MaxDuration)
	defer cancel()
	var wrote, first time.Time
	trace := &httptrace.ClientTrace{
		WroteRequest:         func(httptrace.WroteRequestInfo) { wrote = time.Now() },
		GotFirstResponseByte: func() { first = time.Now() },
	}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, url, nil)
	if err != nil {
		return fail(err)
	}
	req.Header.Set("User-Agent", "imonitord")
	start := time.Now()
	resp, err := r.client.Do(req)
	if err != nil {
		return fail(err)
	}
	defer resp.Body.Close()
	if !wrote.IsZero() && !first.IsZero() {
		res.LatencyMs = stats.F(float64(first.Sub(wrote).Microseconds()) / 1000)
	}
	if resp.StatusCode >= 400 {
		return fail(errors.New(resp.Status))
	}
	buf := make([]byte, 32*1024)
	n, cerr := io.CopyBuffer(io.Discard, resp.Body, buf)
	end := time.Now()
	res.Bytes = n
	res.DurationMs = end.Sub(start).Milliseconds()
	xfer := end.Sub(first)
	if first.IsZero() {
		xfer = end.Sub(start)
	}
	if xfer > 0 && n > 0 {
		res.DownMbps = stats.F(float64(n) * 8 / xfer.Seconds() / 1e6)
	}
	if cerr != nil && !errors.Is(cerr, context.DeadlineExceeded) {
		s := cerr.Error()
		res.Error = &s
	}
	return res
}
