package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/auth"
)

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	config, err := s.auth.Config(r.Context())
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"oidc_enabled": config.Enabled, "issuer_url": config.IssuerURL, "client_id": config.ClientID})
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	loginURL, err := s.auth.LoginURL(r.Context(), auth.RequestOrigin(r), r.URL.Query().Get("return_to"))
	if err != nil {
		s.platformError(w, err)
		return
	}
	http.Redirect(w, r, loginURL, http.StatusFound)
}

func (s *Server) authCallback(w http.ResponseWriter, r *http.Request) {
	session, returnTo, _, err := s.auth.Callback(r.Context(), auth.RequestOrigin(r), r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		s.platformError(w, err)
		return
	}
	secure := strings.HasPrefix(auth.RequestOrigin(r), "https://")
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: session, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode, MaxAge: 8 * 60 * 60})
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func (s *Server) session(w http.ResponseWriter, r *http.Request) {
	if user, ok := sessionUser(r); ok {
		writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": user, "admin": s.auth.IsAdmin(r.Context(), user)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(auth.SessionCookie); err == nil {
		_ = s.auth.Logout(r.Context(), cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}
