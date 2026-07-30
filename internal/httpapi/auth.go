package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"kanpic/internal/auth"
)

func (s *Server) authConfig(w http.ResponseWriter, r *http.Request) {
	config, err := s.auth.Config(r.Context())
	if err != nil {
		s.platformError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"oidc_enabled":             config.Enabled,
		"bootstrap_login_enabled":  s.auth.BootstrapEnabled(),
		"issuer_url":               config.IssuerURL,
		"client_id":                config.ClientID,
		"client_secret_configured": strings.TrimSpace(config.ClientSecret) != "",
	})
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
	session, returnTo, user, err := s.auth.Callback(r.Context(), auth.RequestOrigin(r), r.URL.Query().Get("state"), r.URL.Query().Get("code"))
	if err != nil {
		s.platformError(w, err)
		return
	}
	s.setSessionCookie(w, r, session, user.ExpiresAt)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

func (s *Server) bootstrapLogin(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ID       string `json:"id"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	session, user, err := s.auth.BootstrapLogin(r.Context(), input.ID, input.Password)
	if errors.Is(err, auth.ErrInvalidBootstrapCredentials) {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "invalid_credentials", "message": "아이디 또는 비밀번호가 올바르지 않습니다."}})
		return
	}
	if err != nil {
		s.platformError(w, err)
		return
	}
	s.setSessionCookie(w, r, session, user.ExpiresAt)
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true, "user": user, "admin": true})
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, session string, expiresAt time.Time) {
	maxAge := int(time.Until(expiresAt).Seconds())
	if maxAge < 1 {
		maxAge = 1
	}
	http.SetCookie(w, &http.Cookie{
		Name: auth.SessionCookie, Value: session, Path: "/", HttpOnly: true,
		Secure: strings.HasPrefix(auth.RequestOrigin(r), "https://"), SameSite: http.SameSiteLaxMode,
		Expires: expiresAt, MaxAge: maxAge,
	})
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
	http.SetCookie(w, &http.Cookie{Name: auth.SessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: strings.HasPrefix(auth.RequestOrigin(r), "https://"), MaxAge: -1, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}
