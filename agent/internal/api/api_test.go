package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"imonitord/internal/config"
	"imonitord/internal/probe"
)

type nopProber struct{}

func (nopProber) Probe(ctx context.Context, _ *config.Target, _ time.Duration, _ *probe.State) probe.Result {
	<-ctx.Done()
	return probe.Result{}
}

func newTestApp(t *testing.T) *App {
	t.Helper()
	a, err := NewApp(Options{Version: "1.0.0", StateDir: t.TempDir(), Prober: nopProber{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(a.Shutdown)
	return a
}

func do(t *testing.T, h http.Handler, method, path, remote, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = remote
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPairingCodeSingleUseAndExpiry(t *testing.T) {
	p := NewPairing()
	now := time.Unix(1700000000, 0)
	code, exp := p.NewCode(now)
	if len(code) != 6 || exp.Sub(now) != 10*time.Minute {
		t.Fatalf("code %q exp %v", code, exp)
	}
	if p.Redeem("000000x", now) {
		t.Fatal("bad code accepted")
	}
	if !p.Redeem(code, now.Add(time.Minute)) {
		t.Fatal("valid code rejected")
	}
	if p.Redeem(code, now.Add(time.Minute)) {
		t.Fatal("code reused")
	}
	code2, _ := p.NewCode(now)
	if p.Redeem(code2, now.Add(10*time.Minute+time.Second)) {
		t.Fatal("expired code accepted")
	}
}

func TestPairRateLimit(t *testing.T) {
	p := NewPairing()
	now := time.Unix(1700000000, 0)
	for i := 0; i < 5; i++ {
		if !p.Allow("1.2.3.4", now.Add(time.Duration(i)*time.Second)) {
			t.Fatalf("attempt %d blocked", i)
		}
	}
	if p.Allow("1.2.3.4", now.Add(10*time.Second)) {
		t.Fatal("6th attempt allowed")
	}
	if !p.Allow("5.6.7.8", now.Add(10*time.Second)) {
		t.Fatal("other IP blocked")
	}
	if !p.Allow("1.2.3.4", now.Add(61*time.Second)) {
		t.Fatal("should be allowed after the window")
	}
}

func TestAuthRequired(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	authed := []struct{ m, p string }{
		{"GET", "/v1/config"}, {"PUT", "/v1/config"}, {"GET", "/v1/live"}, {"GET", "/v1/series?target=a"},
		{"GET", "/v1/summary"}, {"GET", "/v1/incidents"}, {"GET", "/v1/system"}, {"POST", "/v1/traceroute"},
		{"POST", "/v1/speedtest"}, {"GET", "/v1/speedtest/history"}, {"GET", "/metrics"}, {"POST", "/v1/restart"},
		{"POST", "/v1/reset-history"}, {"GET", "/v1/clients"}, {"DELETE", "/v1/clients/c-1"}, {"POST", "/v1/pair-code"},
	}
	for _, c := range authed {
		for _, tok := range []string{"", strings.Repeat("0", 64)} {
			w := do(t, h, c.m, c.p, "10.0.0.5:5000", tok, "")
			if w.Code != 401 {
				t.Errorf("%s %s token=%q: got %d", c.m, c.p, tok, w.Code)
			}
			var e map[string]string
			if json.Unmarshal(w.Body.Bytes(), &e) != nil || e["error"] == "" {
				t.Errorf("%s %s: error body %q", c.m, c.p, w.Body.String())
			}
		}
	}
	w := do(t, h, "GET", "/v1/identity", "10.0.0.5:5000", "", "")
	var id map[string]any
	json.Unmarshal(w.Body.Bytes(), &id)
	if w.Code != 200 || id["service"] != "internetmonitor-agent" || id["paired"] != false || id["apiVersion"].(float64) != 1 {
		t.Fatalf("identity %d %s", w.Code, w.Body.String())
	}
}

func TestLocalPairCodeLoopbackOnly(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	req := httptest.NewRequest("POST", "/v1/local/pair-code", nil)
	req.RemoteAddr = "192.168.1.50:40000"
	req.Header.Set("X-Forwarded-For", "127.0.0.1")
	req.Header.Set("X-Real-IP", "127.0.0.1")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatalf("non-loopback got %d", w.Code)
	}
	for _, remote := range []string{"127.0.0.1:5555", "[::1]:5555"} {
		if w := do(t, h, "POST", "/v1/local/pair-code", remote, "", ""); w.Code != 200 {
			t.Fatalf("%s got %d", remote, w.Code)
		}
	}
}

func TestPairFlowAndClients(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	w := do(t, h, "POST", "/v1/local/pair-code", "127.0.0.1:1", "", "")
	var pc struct {
		Code      string `json:"code"`
		ExpiresAt int64  `json:"expiresAt"`
	}
	json.Unmarshal(w.Body.Bytes(), &pc)
	if w := do(t, h, "POST", "/v1/pair", "10.0.0.9:1", "", `{"code":"999999x","clientName":"x"}`); w.Code != 401 {
		t.Fatalf("wrong code: %d", w.Code)
	}
	w = do(t, h, "POST", "/v1/pair", "10.0.0.9:1", "", `{"code":"`+pc.Code+`","clientName":"Mac"}`)
	if w.Code != 200 {
		t.Fatalf("pair: %d %s", w.Code, w.Body.String())
	}
	var pr struct{ Token, NodeID, ClientID string }
	json.Unmarshal(w.Body.Bytes(), &pr)
	if len(pr.Token) != 64 || pr.NodeID != a.Identity.NodeID || pr.ClientID == "" {
		t.Fatalf("pair resp %+v", pr)
	}
	// reuse fails
	if w := do(t, h, "POST", "/v1/pair", "10.0.0.9:1", "", `{"code":"`+pc.Code+`","clientName":"Mac"}`); w.Code != 401 {
		t.Fatalf("reuse: %d", w.Code)
	}
	// config.json must not contain the raw token
	a.mu.Lock()
	stored := a.file.Clients[0].TokenSHA256
	a.mu.Unlock()
	if stored == pr.Token || stored != HashToken(pr.Token) {
		t.Fatal("token must be stored hashed")
	}
	if w := do(t, h, "GET", "/v1/clients", "10.0.0.9:1", pr.Token, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Mac"`) {
		t.Fatalf("clients %d %s", w.Code, w.Body.String())
	}
	// rate limit: 2 attempts used by 10.0.0.9 (plus 1 above) -> 2 more allowed, then 429
	codes := []int{}
	for i := 0; i < 4; i++ {
		codes = append(codes, do(t, h, "POST", "/v1/pair", "10.0.0.9:1", "", `{"code":"000000"}`).Code)
	}
	if codes[0] != 401 || codes[1] != 401 || codes[2] != 429 || codes[3] != 429 {
		t.Fatalf("rate limit codes %v", codes)
	}
	// config validation -> 400
	if w := do(t, h, "PUT", "/v1/config", "10.0.0.9:1", pr.Token, `{"intervalMs":100}`); w.Code != 400 {
		t.Fatalf("validation: %d", w.Code)
	}
	if w := do(t, h, "PUT", "/v1/config", "10.0.0.9:1", pr.Token, `{"nodeName":"Toronto","targets":[{"id":"x","kind":"tcp","host":"1.1.1.1","port":443}]}`); w.Code != 200 || !strings.Contains(w.Body.String(), `"nodeName":"Toronto"`) {
		t.Fatalf("put config: %d %s", w.Code, w.Body.String())
	}
	// delete client -> token stops working
	if w := do(t, h, "DELETE", "/v1/clients/"+pr.ClientID, "10.0.0.9:1", pr.Token, ""); w.Code != 200 {
		t.Fatalf("delete %d", w.Code)
	}
	if w := do(t, h, "GET", "/v1/live", "10.0.0.9:1", pr.Token, ""); w.Code != 401 {
		t.Fatalf("deleted token still works: %d", w.Code)
	}
}

func TestNotFoundAndMethodJSON(t *testing.T) {
	a := newTestApp(t)
	h := a.Handler()
	if w := do(t, h, "GET", "/nope", "127.0.0.1:1", "", ""); w.Code != 404 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("404: %d %s", w.Code, w.Body.String())
	}
	if w := do(t, h, "GET", "/v1/pair", "127.0.0.1:1", "", ""); w.Code != 405 || !strings.Contains(w.Body.String(), `"error"`) {
		t.Fatalf("405: %d %s", w.Code, w.Body.String())
	}
}
