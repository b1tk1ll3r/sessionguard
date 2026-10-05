package webui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestServesAssets(t *testing.T) {
	mux := http.NewServeMux()
	Register(mux)
	for path, ctype := range map[string]string{"/ui/sg.css": "text/css", "/ui/sg.js": "application/javascript", "/ui/policy-editor.js": "application/javascript"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), ctype) || w.Body.Len() == 0 {
			t.Fatalf("%s: code=%d type=%q len=%d", path, w.Code, w.Header().Get("Content-Type"), w.Body.Len())
		}
		if w.Header().Get("Cache-Control") == "" {
			t.Fatalf("%s: missing Cache-Control", path)
		}
	}
	for _, path := range []string{"/ui/missing.js", "/ui/..%2Fwebui.go", "/ui/.hidden"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code == 200 {
			t.Fatalf("%s must not be served", path)
		}
	}
}
