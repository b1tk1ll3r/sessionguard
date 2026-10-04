package auth

import (
	"log"
	"net/http"
)

func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", m.Login)
	mux.HandleFunc("GET /oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Callback(w, r); err != nil {
			// Token-exchange errors may echo IdP responses; log them only.
			log.Printf("OIDC login failed: %v", err)
			http.Error(w, "login failed", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc("POST /logout", m.Logout)
	mux.HandleFunc("POST /oidc/backchannel-logout", m.BackchannelLogout)
}
