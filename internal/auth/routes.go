package auth

import "net/http"

func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", m.Login)
	mux.HandleFunc("GET /oidc/callback", func(w http.ResponseWriter, r *http.Request) {
		if err := m.Callback(w, r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, "/", http.StatusFound)
	})
	mux.HandleFunc("POST /logout", m.Logout)
}
