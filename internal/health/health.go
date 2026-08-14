// Package health implements liveness and readiness endpoints.
//
// Spec: BUILD.md §48.
//
// The critical design decision: Redis must NOT gate readiness.
//
// If it did, a Redis outage would mark every API instance un-ready, the load
// balancer would drain all of them, and §15's per-endpoint degradation policy
// (auth fails closed, read-only stays available) would become unreachable —
// the customer checking their balance gets black-holed by the very design that
// promised not to black-hole them.
//
// Losing a cache degrades the service. It does not remove it from rotation.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Status is a single dependency's state.
type Status string

const (
	StatusUp       Status = "up"
	StatusDegraded Status = "degraded"
	StatusDown     Status = "down"
)

// Checker probes one dependency.
type Checker interface {
	Name() string
	// Critical reports whether readiness depends on this checker.
	// Only PostgreSQL should return true (§48).
	Critical() bool
	Check(ctx context.Context) error
}

// CheckerFunc adapts a function into a Checker.
type CheckerFunc struct {
	NameVal     string
	CriticalVal bool
	Fn          func(ctx context.Context) error
}

func (c CheckerFunc) Name() string   { return c.NameVal }
func (c CheckerFunc) Critical() bool { return c.CriticalVal }
func (c CheckerFunc) Check(ctx context.Context) error {
	return c.Fn(ctx)
}

// Result is one dependency's probe outcome.
type Result struct {
	Name     string `json:"name"`
	Status   Status `json:"status"`
	Critical bool   `json:"critical"`
	Error    string `json:"error,omitempty"`
	TookMS   int64  `json:"took_ms"`
}

// Response is the payload returned by the readiness and dependency endpoints.
type Response struct {
	Status  Status   `json:"status"`
	Service string   `json:"service"`
	Checks  []Result `json:"checks,omitempty"`
}

// Handler serves the three health endpoints.
type Handler struct {
	service  string
	checkers []Checker
	timeout  time.Duration
}

// New builds a Handler. Timeout bounds each individual probe (§49).
func New(service string, timeout time.Duration, checkers ...Checker) *Handler {
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	return &Handler{service: service, checkers: checkers, timeout: timeout}
}

// Live reports process liveness. It has NO dependencies by design: if this
// returns non-200 because Redis blipped, the orchestrator restarts a perfectly
// healthy process and turns a cache incident into a rolling outage.
func (h *Handler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Response{Status: StatusUp, Service: h.service})
}

// Ready reports whether the instance should receive traffic.
//
// Only critical checkers (PostgreSQL) can make this fail. A failing
// non-critical dependency yields 200 with status "degraded" — still in
// rotation, but visibly impaired.
func (h *Handler) Ready(w http.ResponseWriter, r *http.Request) {
	results := h.run(r.Context())

	code := http.StatusOK
	status := StatusUp
	for _, res := range results {
		if res.Status == StatusDown {
			if res.Critical {
				code = http.StatusServiceUnavailable
				status = StatusDown
				break
			}
			status = StatusDegraded
		}
	}
	writeJSON(w, code, Response{Status: status, Service: h.service, Checks: results})
}

// Deps is a diagnostic endpoint reporting every dependency individually.
// It always returns 200 and must never be consumed by a load balancer.
func (h *Handler) Deps(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, Response{
		Status:  StatusUp,
		Service: h.service,
		Checks:  h.run(r.Context()),
	})
}

// Routes registers the health endpoints on mux.
func (h *Handler) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /health/live", h.Live)
	mux.HandleFunc("GET /health/ready", h.Ready)
	mux.HandleFunc("GET /health/deps", h.Deps)
}

// run probes all checkers concurrently, each bounded by its own timeout.
func (h *Handler) run(ctx context.Context) []Result {
	results := make([]Result, len(h.checkers))
	var wg sync.WaitGroup

	for i, c := range h.checkers {
		wg.Add(1)
		go func(i int, c Checker) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, h.timeout)
			defer cancel()

			start := time.Now()
			err := c.Check(cctx)
			res := Result{
				Name:     c.Name(),
				Critical: c.Critical(),
				Status:   StatusUp,
				TookMS:   time.Since(start).Milliseconds(),
			}
			if err != nil {
				res.Status = StatusDown
				res.Error = err.Error()
			}
			results[i] = res
		}(i, c)
	}
	wg.Wait()
	return results
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}
