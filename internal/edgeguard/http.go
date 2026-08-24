package edgeguard

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

type HTTPServer struct {
	guard  *Guard
	logger *slog.Logger
}

func NewHTTPServer(g *Guard, logger *slog.Logger) *HTTPServer {
	if logger == nil {
		logger = slog.Default()
	}
	return &HTTPServer{guard: g, logger: logger}
}

func (s *HTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.healthz)
	mux.HandleFunc("GET /metrics", s.metrics)
	mux.HandleFunc("GET /check", s.check)
	return mux
}

func (s *HTTPServer) healthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func (s *HTTPServer) metrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(s.guard.Metrics()))
}

func (s *HTTPServer) check(w http.ResponseWriter, r *http.Request) {
	rawIP := strings.TrimSpace(r.Header.Get("X-Edge-Client-IP"))
	ip, err := netip.ParseAddr(rawIP)
	if err != nil {
		s.writeDeny(w, http.StatusBadRequest, "invalid client ip", 0)
		return
	}
	host := r.Header.Get("X-Edge-Original-Host")
	method := r.Header.Get("X-Edge-Original-Method")
	uri := r.Header.Get("X-Edge-Original-URI")
	decision := s.guard.Check(ip.Unmap(), host, method, uri, time.Now())
	if decision.Allowed {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
		return
	}

	// Do not log every denial here: during an attack that would turn logging
	// itself into a resource-exhaustion vector. Caddy access logs are sampled
	// and EdgeGuard exposes counters; only state changes such as auto-bans are
	// emitted as runtime log events.
	s.writeDeny(w, decision.StatusCode, decision.Reason, decision.RetryAfter)
}

func (s *HTTPServer) writeDeny(w http.ResponseWriter, code int, reason string, retryAfter int) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if retryAfter > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
	}
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error":  http.StatusText(code),
		"reason": reason,
	})
}
