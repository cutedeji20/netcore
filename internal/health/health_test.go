package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func up(name string, critical bool) Checker {
	return CheckerFunc{NameVal: name, CriticalVal: critical,
		Fn: func(context.Context) error { return nil }}
}

func down(name string, critical bool) Checker {
	return CheckerFunc{NameVal: name, CriticalVal: critical,
		Fn: func(context.Context) error { return errors.New(name + " unreachable") }}
}

func get(t *testing.T, h http.HandlerFunc) (int, Response) {
	t.Helper()
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	var resp Response
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("bad JSON: %v\n%s", err, rec.Body.String())
	}
	return rec.Code, resp
}

// §48 — liveness must never depend on anything external.
func TestLive_IgnoresFailingDependencies(t *testing.T) {
	h := New("api", time.Second, down("postgres", true), down("redis", false))
	code, resp := get(t, h.Live)
	if code != http.StatusOK {
		t.Fatalf("Live returned %d with everything down; want 200", code)
	}
	if resp.Status != StatusUp {
		t.Errorf("status = %s, want up", resp.Status)
	}
}

// THE decision this package exists to encode. A Redis outage must not drain
// the instance from the load balancer.
func TestReady_RedisDownStaysInRotation(t *testing.T) {
	h := New("api", time.Second, up("postgres", true), down("redis", false))
	code, resp := get(t, h.Ready)

	if code != http.StatusOK {
		t.Fatalf("Ready returned %d with Redis down; want 200 (§48: a cache "+
			"outage must not become a total outage)", code)
	}
	if resp.Status != StatusDegraded {
		t.Errorf("status = %s, want degraded", resp.Status)
	}
}

func TestReady_PostgresDownDrainsInstance(t *testing.T) {
	h := New("api", time.Second, down("postgres", true), up("redis", false))
	code, resp := get(t, h.Ready)

	if code != http.StatusServiceUnavailable {
		t.Fatalf("Ready returned %d with Postgres down; want 503", code)
	}
	if resp.Status != StatusDown {
		t.Errorf("status = %s, want down", resp.Status)
	}
}

func TestReady_AllHealthy(t *testing.T) {
	h := New("api", time.Second, up("postgres", true), up("redis", false))
	code, resp := get(t, h.Ready)
	if code != http.StatusOK || resp.Status != StatusUp {
		t.Fatalf("code=%d status=%s, want 200/up", code, resp.Status)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(resp.Checks))
	}
}

// Deps is diagnostic: always 200, even when everything is broken, so an
// operator can see the detail without a load balancer reacting to it.
func TestDeps_AlwaysReturns200(t *testing.T) {
	h := New("api", time.Second, down("postgres", true), down("redis", false))
	code, resp := get(t, h.Deps)
	if code != http.StatusOK {
		t.Fatalf("Deps returned %d; want 200 always", code)
	}
	if len(resp.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(resp.Checks))
	}
	for _, c := range resp.Checks {
		if c.Status != StatusDown {
			t.Errorf("%s status = %s, want down", c.Name, c.Status)
		}
		if c.Error == "" {
			t.Errorf("%s should report an error string", c.Name)
		}
	}
}

// §49 — a hanging dependency must not hang the health endpoint.
func TestCheck_HangingDependencyTimesOut(t *testing.T) {
	hang := CheckerFunc{NameVal: "slow", CriticalVal: false, Fn: func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	}}
	h := New("api", 50*time.Millisecond, up("postgres", true), hang)

	start := time.Now()
	code, resp := get(t, h.Ready)
	elapsed := time.Since(start)

	if elapsed > 2*time.Second {
		t.Fatalf("probe took %v; timeout not enforced", elapsed)
	}
	if code != http.StatusOK {
		t.Errorf("code = %d, want 200 (non-critical timeout is degradation)", code)
	}
	if resp.Status != StatusDegraded {
		t.Errorf("status = %s, want degraded", resp.Status)
	}
}

// Probes run concurrently; the handler must be race-free under -race.
func TestRun_ConcurrentProbesAreRaceFree(t *testing.T) {
	checkers := make([]Checker, 0, 20)
	for i := 0; i < 20; i++ {
		checkers = append(checkers, CheckerFunc{
			NameVal:     "dep",
			CriticalVal: false,
			Fn: func(context.Context) error {
				time.Sleep(time.Millisecond)
				return nil
			},
		})
	}
	h := New("api", time.Second, checkers...)
	results := h.run(context.Background())
	if len(results) != 20 {
		t.Fatalf("got %d results, want 20", len(results))
	}
	for i, r := range results {
		if r.Name == "" {
			t.Fatalf("result %d not populated (race or index bug)", i)
		}
	}
}

func TestRoutes_Registered(t *testing.T) {
	h := New("api", time.Second, up("postgres", true))
	mux := http.NewServeMux()
	h.Routes(mux)

	for _, path := range []string{"/health/live", "/health/ready", "/health/deps"} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("%s not registered", path)
		}
	}
}

// Health responses must never be cached by an intermediary.
func TestResponses_AreNotCacheable(t *testing.T) {
	h := New("api", time.Second, up("postgres", true))
	rec := httptest.NewRecorder()
	h.Ready(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}
