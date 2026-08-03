package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"

	"kanpic/internal/settings"
)

const SessionCookie = "kanpic_session"

const bootstrapAdminRole = "kanpic-bootstrap-admin"

var ErrInvalidBootstrapCredentials = errors.New("invalid bootstrap administrator credentials")

type User struct {
	ID          string    `json:"id"`
	Email       string    `json:"email,omitempty"`
	DisplayName string    `json:"display_name"`
	Roles       []string  `json:"roles"`
	ExpiresAt   time.Time `json:"expires_at"`
}

type Config struct {
	Enabled      bool
	IssuerURL    string
	ClientID     string
	ClientSecret string
	Scopes       []string
	AdminRoles   []string
	SessionTTL   time.Duration
	CAPEM        string
	PublicURL    string
}

type BootstrapCredentials struct {
	ID       string
	Password string
}

func (c BootstrapCredentials) Enabled() bool {
	return strings.TrimSpace(c.ID) != "" && c.Password != ""
}

type Service struct {
	pool      *pgxpool.Pool
	settings  *settings.Repository
	bootstrap BootstrapCredentials
}

func New(pool *pgxpool.Pool, settingRepository *settings.Repository, bootstrap BootstrapCredentials) *Service {
	bootstrap.ID = strings.TrimSpace(bootstrap.ID)
	return &Service{pool: pool, settings: settingRepository, bootstrap: bootstrap}
}

func (s *Service) BootstrapEnabled() bool { return s.bootstrap.Enabled() }

func (s *Service) Config(ctx context.Context) (Config, error) {
	values, err := s.settings.Values(ctx)
	if err != nil {
		return Config{}, err
	}
	config := Config{ClientID: "kanpic", Scopes: []string{"openid", "profile", "email"}, AdminRoles: []string{"kanpic-admin"}, SessionTTL: 8 * time.Hour}
	config.Enabled, _ = values["auth.oidc.enabled"].(bool)
	config.IssuerURL, _ = values["auth.oidc.issuer_url"].(string)
	if value, ok := values["auth.oidc.client_id"].(string); ok && value != "" {
		config.ClientID = value
	}
	config.ClientSecret, _ = values["auth.oidc.client_secret"].(string)
	if value := stringList(values["auth.oidc.scopes"]); len(value) > 0 {
		config.Scopes = value
	}
	if value := stringList(values["auth.oidc.admin_roles"]); len(value) > 0 {
		config.AdminRoles = value
	}
	if hours, ok := values["auth.session_hours"].(float64); ok && hours > 0 && hours <= 720 {
		config.SessionTTL = time.Duration(hours * float64(time.Hour))
	}
	config.CAPEM, _ = values["auth.oidc.ca_pem"].(string)
	config.PublicURL, _ = values["server.public_url"].(string)
	return config, nil
}

func (s *Service) LoginURL(ctx context.Context, requestOrigin, returnTo string) (string, error) {
	config, err := s.Config(ctx)
	if err != nil {
		return "", err
	}
	if !config.Enabled {
		return "", errors.New("OIDC is disabled")
	}
	provider, providerContext, err := providerFor(ctx, config)
	if err != nil {
		return "", err
	}
	origin := requestOrigin
	if config.PublicURL != "" {
		origin = strings.TrimRight(config.PublicURL, "/")
	}
	redirectURL := origin + "/auth/callback"
	oauthConfig := oauthConfigFor(config, provider.Endpoint(), redirectURL)
	state, err := randomToken(32)
	if err != nil {
		return "", err
	}
	verifier := oauth2.GenerateVerifier()
	if !validReturnTo(returnTo) {
		returnTo = "/"
	}
	hash := sha256.Sum256([]byte(state))
	_, err = s.pool.Exec(ctx, `INSERT INTO auth_transactions(state_hash,code_verifier,return_to,expires_at) VALUES($1,$2,$3,$4)`, hash[:], verifier, returnTo, time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		return "", err
	}
	_ = providerContext
	return oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.S256ChallengeOption(verifier)), nil
}

func (s *Service) Callback(ctx context.Context, requestOrigin, state, code string) (string, string, User, error) {
	if state == "" || code == "" {
		return "", "", User{}, errors.New("state and code are required")
	}
	hash := sha256.Sum256([]byte(state))
	var verifier, returnTo string
	err := s.pool.QueryRow(ctx, `DELETE FROM auth_transactions WHERE state_hash=$1 AND expires_at>now() RETURNING code_verifier,return_to`, hash[:]).Scan(&verifier, &returnTo)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", "", User{}, errors.New("login transaction expired or invalid")
	}
	if err != nil {
		return "", "", User{}, err
	}
	config, err := s.Config(ctx)
	if err != nil {
		return "", "", User{}, err
	}
	provider, providerContext, err := providerFor(ctx, config)
	if err != nil {
		return "", "", User{}, err
	}
	origin := requestOrigin
	if config.PublicURL != "" {
		origin = strings.TrimRight(config.PublicURL, "/")
	}
	oauthConfig := oauthConfigFor(config, provider.Endpoint(), origin+"/auth/callback")
	token, err := oauthConfig.Exchange(providerContext, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return "", "", User{}, fmt.Errorf("exchange OIDC code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return "", "", User{}, errors.New("OIDC response did not include id_token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: config.ClientID}).Verify(providerContext, rawIDToken)
	if err != nil {
		return "", "", User{}, fmt.Errorf("verify ID token: %w", err)
	}
	var claims struct {
		Subject           string `json:"sub"`
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
		RealmAccess       struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return "", "", User{}, err
	}
	if claims.Subject == "" {
		return "", "", User{}, errors.New("ID token subject is empty")
	}
	displayName := claims.Name
	if displayName == "" {
		displayName = claims.PreferredUsername
	}
	if displayName == "" {
		displayName = claims.Email
	}
	user := User{ID: claims.Subject, Email: claims.Email, DisplayName: displayName, Roles: claims.RealmAccess.Roles, ExpiresAt: time.Now().UTC().Add(config.SessionTTL)}
	session, err := s.createSession(ctx, user, rawIDToken)
	if err != nil {
		return "", "", User{}, err
	}
	return session, returnTo, user, nil
}

func (s *Service) BootstrapLogin(ctx context.Context, id, password string) (string, User, error) {
	idMatches := secureEqual(strings.TrimSpace(id), s.bootstrap.ID)
	passwordMatches := secureEqual(password, s.bootstrap.Password)
	if !s.bootstrap.Enabled() || !idMatches || !passwordMatches {
		return "", User{}, ErrInvalidBootstrapCredentials
	}
	config, err := s.Config(ctx)
	if err != nil {
		return "", User{}, err
	}
	user := User{
		ID:          s.bootstrap.ID,
		DisplayName: s.bootstrap.ID,
		Roles:       []string{bootstrapAdminRole},
		ExpiresAt:   time.Now().UTC().Add(config.SessionTTL),
	}
	session, err := s.createSession(ctx, user, "")
	return session, user, err
}

func (s *Service) createSession(ctx context.Context, user User, idTokenHint string) (string, error) {
	session, err := randomToken(32)
	if err != nil {
		return "", err
	}
	sessionHash := sha256.Sum256([]byte(session))
	_, err = s.pool.Exec(ctx, `INSERT INTO user_sessions(session_hash,user_id,email,display_name,roles,id_token_hint,expires_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, sessionHash[:], user.ID, user.Email, user.DisplayName, user.Roles, idTokenHint, user.ExpiresAt)
	return session, err
}

func (s *Service) Session(ctx context.Context, token string) (User, error) {
	hash := sha256.Sum256([]byte(token))
	var user User
	err := s.pool.QueryRow(ctx, `UPDATE user_sessions SET last_seen_at=now() WHERE session_hash=$1 AND expires_at>now() RETURNING user_id,email,display_name,roles,expires_at`, hash[:]).Scan(&user.ID, &user.Email, &user.DisplayName, &user.Roles, &user.ExpiresAt)
	return user, err
}

func (s *Service) Logout(ctx context.Context, token string) error {
	hash := sha256.Sum256([]byte(token))
	_, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE session_hash=$1`, hash[:])
	return err
}

// RevokeUserSessions signs a user out everywhere, used when an administrator
// suspends an account or forces a re-login.
func (s *Service) RevokeUserSessions(ctx context.Context, userID string) error {
	_, err := s.RevokeUserSessionsCount(ctx, userID)
	return err
}

func (s *Service) RevokeUserSessionsCount(ctx context.Context, userID string) (int, error) {
	command, err := s.pool.Exec(ctx, `DELETE FROM user_sessions WHERE lower(user_id)=lower($1)`, strings.TrimSpace(userID))
	if err != nil {
		return 0, err
	}
	return int(command.RowsAffected()), nil
}

func (s *Service) IsAdmin(ctx context.Context, user User) bool {
	if s.bootstrap.Enabled() && secureEqual(user.ID, s.bootstrap.ID) {
		for _, role := range user.Roles {
			if role == bootstrapAdminRole {
				return true
			}
		}
	}
	config, err := s.Config(ctx)
	if err != nil {
		return false
	}
	allowed := make(map[string]struct{}, len(config.AdminRoles))
	for _, role := range config.AdminRoles {
		allowed[role] = struct{}{}
	}
	for _, role := range user.Roles {
		if _, ok := allowed[role]; ok {
			return true
		}
	}
	return false
}

func oauthConfigFor(config Config, endpoint oauth2.Endpoint, redirectURL string) oauth2.Config {
	return oauth2.Config{
		ClientID:     config.ClientID,
		ClientSecret: config.ClientSecret,
		Endpoint:     endpoint,
		RedirectURL:  redirectURL,
		Scopes:       config.Scopes,
	}
}

func providerFor(ctx context.Context, config Config) (*oidc.Provider, context.Context, error) {
	providerContext := ctx
	if strings.TrimSpace(config.CAPEM) != "" {
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM([]byte(config.CAPEM)) {
			return nil, ctx, errors.New("configured OIDC CA PEM is invalid")
		}
		client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}}
		providerContext = oidc.ClientContext(ctx, client)
	}
	provider, err := oidc.NewProvider(providerContext, strings.TrimRight(config.IssuerURL, "/"))
	return provider, providerContext, err
}

func RequestOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Proto"), ",")[0]); forwarded == "http" || forwarded == "https" {
		scheme = forwarded
	}
	host := r.Host
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
		host = forwarded
	}
	return scheme + "://" + host
}

func validReturnTo(value string) bool {
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() == false && parsed.Host == ""
}

func randomToken(size int) (string, error) {
	data := make([]byte, size)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func secureEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

func stringList(value any) []string {
	list, ok := value.([]any)
	if !ok {
		if direct, ok := value.([]string); ok {
			return direct
		}
		return nil
	}
	result := make([]string, 0, len(list))
	for _, item := range list {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}
