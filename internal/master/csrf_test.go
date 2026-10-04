package master

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSRFGuard(t *testing.T) {
	h := csrfGuard(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
	do := func(method, origin, ctype string, cookie bool) int {
		r := httptest.NewRequest(method, "https://sg.example.org/api/v1/resources", strings.NewReader(`{"name":"x"}`))
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		if ctype != "" {
			r.Header.Set("Content-Type", ctype)
		}
		if cookie {
			r.AddCookie(&http.Cookie{Name: "sg_session", Value: "v"})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}
	if got := do("POST", "https://sg.example.org", "application/json", true); got != 204 {
		t.Fatalf("same-origin JSON POST: %d", got)
	}
	if got := do("POST", "https://other.example.org", "application/json", true); got != 403 {
		t.Fatalf("cross-origin POST must be rejected: %d", got)
	}
	if got := do("POST", "https://sg.example.org", "text/plain", true); got != 415 {
		t.Fatalf("text/plain POST must be rejected: %d", got)
	}
	if got := do("POST", "https://other.example.org", "text/plain", false); got != 204 {
		t.Fatalf("cookie-less (agent/broker) requests must pass: %d", got)
	}
	if got := do("GET", "https://other.example.org", "", true); got != 204 {
		t.Fatalf("GET must pass: %d", got)
	}
}
