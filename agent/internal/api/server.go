package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"imonitord/internal/config"
	"imonitord/internal/engine"
	"imonitord/internal/speedtest"
	"imonitord/internal/sysinfo"
	"imonitord/internal/traceroute"
)

const maxBody = 1 << 20

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// remoteIP returns the real TCP peer address (never proxy headers).
func remoteIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if i := strings.IndexByte(host, '%'); i >= 0 {
		host = host[:i]
	}
	return net.ParseIP(host)
}

func isLoopback(r *http.Request) bool {
	ip := remoteIP(r)
	return ip != nil && ip.IsLoopback()
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}

func decodeBody(r *http.Request, v any) error {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil {
		return err
	}
	if len(data) > maxBody {
		return errors.New("body too large")
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return nil
	}
	return json.Unmarshal(data, v)
}

type methods map[string]http.HandlerFunc

func (m methods) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h, ok := m[r.Method]
	if !ok && r.Method == http.MethodHead {
		h, ok = m[http.MethodGet]
	}
	if !ok {
		allow := make([]string, 0, len(m))
		for k := range m {
			allow = append(allow, k)
		}
		sort.Strings(allow)
		w.Header().Set("Allow", strings.Join(allow, ", "))
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	h(w, r)
}

func (a *App) auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.Authenticate(bearer(r)) {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		h(w, r)
	}
}

// Handler builds the HTTP routes.
func (a *App) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/v1/identity", methods{"GET": a.handleIdentity})
	mux.Handle("/v1/pair", methods{"POST": a.handlePair})
	mux.Handle("/v1/local/pair-code", methods{"POST": a.handleLocalPairCode})
	mux.Handle("/v1/pair-code", methods{"POST": a.auth(a.handlePairCode)})
	mux.Handle("/v1/clients", methods{"GET": a.auth(a.handleClients)})
	mux.Handle("/v1/clients/{id}", methods{"DELETE": a.auth(a.handleDeleteClient)})
	mux.Handle("/v1/config", methods{"GET": a.auth(a.handleGetConfig), "PUT": a.auth(a.handlePutConfig)})
	mux.Handle("/v1/live", methods{"GET": a.auth(a.handleLive)})
	mux.Handle("/v1/series", methods{"GET": a.auth(a.handleSeries)})
	mux.Handle("/v1/summary", methods{"GET": a.auth(a.handleSummary)})
	mux.Handle("/v1/incidents", methods{"GET": a.auth(a.handleIncidents)})
	mux.Handle("/v1/system", methods{"GET": a.auth(a.handleSystem)})
	mux.Handle("/v1/traceroute", methods{"POST": a.auth(a.handleTraceroute)})
	mux.Handle("/v1/speedtest", methods{"POST": a.auth(a.handleSpeedtest)})
	mux.Handle("/v1/speedtest/history", methods{"GET": a.auth(a.handleSpeedHistory)})
	mux.Handle("/metrics", methods{"GET": a.handleMetrics})
	mux.Handle("/v1/restart", methods{"POST": a.auth(a.handleRestart)})
	mux.Handle("/v1/reset-history", methods{"POST": a.auth(a.handleResetHistory)})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, http.StatusNotFound, "not found") })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				if v == http.ErrAbortHandler {
					panic(v)
				}
				buf := make([]byte, 4096)
				buf = buf[:runtime.Stack(buf, false)]
				log.Printf("panic serving %s: %v\n%s", r.URL.Path, v, buf)
				writeErr(w, http.StatusInternalServerError, "internal error")
			}
		}()
		mux.ServeHTTP(w, r)
	})
}

// ---- recognition & pairing ----

func (a *App) handleIdentity(w http.ResponseWriter, r *http.Request) {
	cfg := a.Engine.Config()
	writeJSON(w, 200, map[string]any{
		"service":    "internetmonitor-agent",
		"apiVersion": 1,
		"version":    a.Version,
		"nodeId":     a.Identity.NodeID,
		"hostname":   a.Hostname,
		"nodeName":   cfg.NodeName,
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"uptimeSec":  int64(time.Since(a.StartedAt).Seconds()),
		"startedAt":  a.StartedAt.Unix(),
		"paired":     a.Paired(),
	})
}

func (a *App) handlePair(w http.ResponseWriter, r *http.Request) {
	ip := remoteIP(r)
	key := r.RemoteAddr
	if ip != nil {
		key = ip.String()
	}
	if !a.Pair.Allow(key, a.now()) {
		writeErr(w, http.StatusTooManyRequests, "too many pairing attempts; try again in a minute")
		return
	}
	var body struct {
		Code       string `json:"code"`
		ClientName string `json:"clientName"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !a.Pair.Redeem(strings.TrimSpace(body.Code), a.now()) {
		writeErr(w, http.StatusUnauthorized, "invalid or expired pairing code")
		return
	}
	id, tok, err := a.AddClient(body.ClientName)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not save client: "+err.Error())
		return
	}
	log.Printf("paired new client %s (%q) from %s", id, body.ClientName, key)
	writeJSON(w, 200, map[string]string{"token": tok, "nodeId": a.Identity.NodeID, "clientId": id})
}

func (a *App) newCode(w http.ResponseWriter) {
	code, exp := a.Pair.NewCode(a.now())
	writeJSON(w, 200, map[string]any{"code": code, "expiresAt": exp.Unix()})
}

func (a *App) handleLocalPairCode(w http.ResponseWriter, r *http.Request) {
	if !isLoopback(r) {
		writeErr(w, http.StatusForbidden, "pair-code is only available from localhost")
		return
	}
	a.newCode(w)
}

func (a *App) handlePairCode(w http.ResponseWriter, r *http.Request) { a.newCode(w) }

func (a *App) handleClients(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Clients())
}

func (a *App) handleDeleteClient(w http.ResponseWriter, r *http.Request) {
	ok, err := a.DeleteClient(r.PathValue("id"))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if !ok {
		writeErr(w, http.StatusNotFound, "client not found")
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---- config ----

func (a *App) handleGetConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Engine.Config())
}

func (a *App) handlePutConfig(w http.ResponseWriter, r *http.Request) {
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBody+1))
	if err != nil || len(data) > maxBody {
		writeErr(w, http.StatusBadRequest, "body too large or unreadable")
		return
	}
	c, err := config.Decode(data, a.Hostname)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := a.SetConfig(c)
	if err != nil {
		var ve *config.ValidationError
		if errors.As(err, &ve) {
			writeErr(w, http.StatusBadRequest, ve.Msg)
			return
		}
		writeErr(w, 500, "could not save config: "+err.Error())
		return
	}
	writeJSON(w, 200, saved)
}

// ---- live & history ----

func (a *App) handleLive(w http.ResponseWriter, r *http.Request) {
	now, targets := a.Engine.Live()
	writeJSON(w, 200, map[string]any{"now": now, "targets": targets})
}

func qInt(r *http.Request, name string, def int64) (int64, error) {
	s := r.URL.Query().Get(name)
	if s == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer", name)
	}
	return v, nil
}

func splitIDs(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" && !seen[p] {
			seen[p] = true
			out = append(out, p)
		}
	}
	return out
}

func (a *App) handleSeries(w http.ResponseWriter, r *http.Request) {
	ids := splitIDs(r.URL.Query().Get("target"))
	if len(ids) == 0 {
		writeErr(w, 400, "target is required")
		return
	}
	if len(ids) > 50 {
		writeErr(w, 400, "too many targets (max 50)")
		return
	}
	now := a.now().Unix()
	to, err := qInt(r, "to", now)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	from, err := qInt(r, "from", to-3600)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	points, err := qInt(r, "points", 300)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if from >= to {
		writeErr(w, 400, "from must be < to")
		return
	}
	if points < 1 {
		writeErr(w, 400, "points must be >= 1")
		return
	}
	if points > 1500 {
		points = 1500
	}
	series := a.Engine.Series(ids, from, to, int(points))
	writeJSON(w, 200, map[string]any{"from": from, "to": to, "series": series})
}

func (a *App) handleSummary(w http.ResponseWriter, r *http.Request) {
	ids := splitIDs(r.URL.Query().Get("target"))
	writeJSON(w, 200, map[string]any{"summaries": a.Engine.Summary(ids)})
}

func (a *App) handleIncidents(w http.ResponseWriter, r *http.Request) {
	now := a.now().Unix()
	since, err := qInt(r, "since", now-7*86400)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	limit, err := qInt(r, "limit", 200)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 10000 {
		limit = 10000
	}
	writeJSON(w, 200, a.Engine.Incidents(since, int(limit)))
}

// ---- diagnostics ----

func (a *App) handleSystem(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, sysinfo.Collect(a.StateDir, a.TS))
}

func (a *App) handleTraceroute(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Host    string `json:"host"`
		MaxHops int    `json:"maxHops"`
	}
	if err := decodeBody(r, &body); err != nil {
		writeErr(w, 400, "invalid JSON body")
		return
	}
	body.Host = strings.TrimSpace(body.Host)
	if body.Host == "" || len(body.Host) > 253 || strings.ContainsAny(body.Host, " /\\\"'`;|&$") || strings.HasPrefix(body.Host, "-") {
		writeErr(w, 400, "host is required (hostname or IPv4 address)")
		return
	}
	if body.MaxHops < 0 || body.MaxHops > traceroute.MaxHops {
		writeErr(w, 400, fmt.Sprintf("maxHops must be 1-%d", traceroute.MaxHops))
		return
	}
	select {
	case a.traceSem <- struct{}{}:
		defer func() { <-a.traceSem }()
	default:
		writeErr(w, http.StatusTooManyRequests, "too many concurrent traceroutes (max 2)")
		return
	}
	res, err := traceroute.Run(r.Context(), body.Host, body.MaxHops)
	if err != nil {
		code := 500
		if strings.Contains(err.Error(), "resolve") || strings.Contains(err.Error(), "IPv4") {
			code = 400
		}
		writeErr(w, code, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (a *App) handleSpeedtest(w http.ResponseWriter, r *http.Request) {
	cfg := a.Engine.Config()
	res, err := a.Speed.Run(r.Context(), cfg.Speedtest.URL)
	if errors.Is(err, speedtest.ErrBusy) {
		writeErr(w, http.StatusTooManyRequests, err.Error())
		return
	}
	writeJSON(w, 200, res)
}

func (a *App) handleSpeedHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Speed.History())
}

func promEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`)
	return r.Replace(s)
}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	cfg := a.Engine.Config()
	if !cfg.MetricsPublic && !a.Authenticate(bearer(r)) {
		writeErr(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	targets := a.Engine.Metrics()
	var b strings.Builder
	labels := func(t engine.MetricTarget) string {
		return fmt.Sprintf(`{target="%s",name="%s",kind="%s"}`, promEscape(t.ID), promEscape(t.Name), promEscape(t.Kind))
	}
	b.WriteString("# HELP imonitor_rtt_ms Last successful round-trip time in milliseconds.\n# TYPE imonitor_rtt_ms gauge\n")
	for _, t := range targets {
		if t.LastRttMs != nil {
			fmt.Fprintf(&b, "imonitor_rtt_ms%s %g\n", labels(t), *t.LastRttMs)
		}
	}
	b.WriteString("# HELP imonitor_loss_pct Packet loss percent over the live window (60 s).\n# TYPE imonitor_loss_pct gauge\n")
	for _, t := range targets {
		if t.LossPct != nil {
			fmt.Fprintf(&b, "imonitor_loss_pct%s %g\n", labels(t), *t.LossPct)
		}
	}
	b.WriteString("# HELP imonitor_up 1 if the target is up or degraded, 0 if down.\n# TYPE imonitor_up gauge\n")
	for _, t := range targets {
		if t.State == "unknown" {
			continue
		}
		v := 1
		if t.State == "down" {
			v = 0
		}
		fmt.Fprintf(&b, "imonitor_up%s %d\n", labels(t), v)
	}
	b.WriteString("# HELP imonitor_incidents_open Number of open incidents.\n# TYPE imonitor_incidents_open gauge\n")
	fmt.Fprintf(&b, "imonitor_incidents_open %d\n", a.Engine.OpenIncidents())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	io.WriteString(w, b.String())
}

// ---- lifecycle ----

func (a *App) handleRestart(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(300 * time.Millisecond)
		log.Printf("restart requested via API; exiting")
		a.Shutdown()
		a.Exit(0)
	}()
}

func (a *App) handleResetHistory(w http.ResponseWriter, r *http.Request) {
	if err := a.Engine.ResetHistory(); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// Serve runs the HTTP server until ctx is cancelled.
func (a *App) Serve(ctx context.Context, listen string) error {
	srv := &http.Server{
		Addr:              listen,
		Handler:           a.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      90 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return err
	}
	log.Printf("imonitord %s listening on %s (state %s, node %s)", a.Version, ln.Addr(), a.StateDir, a.Identity.NodeID)
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		return nil
	case err := <-errc:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
