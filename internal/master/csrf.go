package master

import (
	"net/http"
	"strings"

	"github.com/example/sessionguard/internal/httpx"
)

// csrfGuard protects every state-changing request that is authenticated by
// the admin session cookie. SameSite=Lax alone still lets same-site origins
// (other subdomains) send simple text/plain POSTs. Agent, broker and OIDC
// back-channel requests carry no admin cookie and are unaffected.
func csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			next.ServeHTTP(w, r)
			return
		}
		if _, err := r.Cookie("sg_session"); err == nil {
			if !httpx.SameOrigin(r) {
				httpx.Error(w, http.StatusForbidden, "cross-origin request rejected")
				return
			}
			if strings.HasPrefix(r.URL.Path, "/api/") && r.ContentLength != 0 && !httpx.IsJSON(r) {
				httpx.Error(w, http.StatusUnsupportedMediaType, "Content-Type must be application/json")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
