package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeIssuer is a Keycloak stand-in: a discovery document and a JWKS with one
// RSA key, plus a signer that mints tokens the way Keycloak does.
type fakeIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &fakeIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/realms/company/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                 issuer.url(),
			"authorization_endpoint": issuer.url() + "/protocol/openid-connect/auth",
			"token_endpoint":         issuer.url() + "/protocol/openid-connect/token",
			"jwks_uri":               issuer.url() + "/protocol/openid-connect/certs",
		})
	})
	mux.HandleFunc("/realms/company/protocol/openid-connect/certs", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "kid": "k1", "use": "sig", "alg": "RS256",
			"n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (f *fakeIssuer) url() string { return f.server.URL + "/realms/company" }

func (f *fakeIssuer) sign(t *testing.T, claims map[string]any) string {
	t.Helper()
	if _, ok := claims["iss"]; !ok {
		claims["iss"] = f.url()
	}
	if _, ok := claims["exp"]; !ok {
		claims["exp"] = time.Now().Add(5 * time.Minute).Unix()
	}
	if _, ok := claims["iat"]; !ok {
		claims["iat"] = time.Now().Unix()
	}
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": "k1"})
	payload, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func (f *fakeIssuer) config() Config {
	return Config{Enabled: true, MCPEnabled: true, IssuerURL: f.url(), ClientID: "kanpic", SessionTTL: time.Hour}
}

func verifyWith(t *testing.T, config Config, raw string) (AccessToken, error) {
	t.Helper()
	provider, providerContext, err := providerFor(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return verifyAccessToken(providerContext, provider, config, "https://kanpic.example/mcp", raw)
}

func TestVerifyAccessTokenAcceptsKeycloakTokenForResource(t *testing.T) {
	t.Parallel()
	issuer := newFakeIssuer(t)
	raw := issuer.sign(t, map[string]any{
		"sub": "user-1", "typ": "Bearer", "azp": "claude-desktop", "aud": []string{"account", "https://kanpic.example/mcp"},
		"email": "user@example.com", "preferred_username": "user", "name": "홍길동",
		"scope":        "openid profile email workbook.read range.read",
		"realm_access": map[string]any{"roles": []string{"kanpic-admin", "offline_access"}},
	})

	token, err := verifyWith(t, issuer.config(), raw)
	if err != nil {
		t.Fatalf("token for the resource must be accepted: %v", err)
	}
	if token.User.ID != "user-1" || token.User.Email != "user@example.com" || token.User.DisplayName != "홍길동" {
		t.Fatalf("user claims were not carried over: %#v", token.User)
	}
	if len(token.User.Roles) != 2 || token.User.Roles[0] != "kanpic-admin" {
		t.Fatalf("realm roles were not carried over: %#v", token.User.Roles)
	}
	if strings.Join(token.Scopes, " ") != "range.read workbook.read" {
		t.Fatalf("only kanpic scopes may narrow the token, got %v", token.Scopes)
	}
	if token.ClientID != "claude-desktop" {
		t.Fatalf("azp should be reported as client id: %q", token.ClientID)
	}
	if token.User.ExpiresAt.Before(time.Now()) {
		t.Fatalf("expiry must come from the token: %v", token.User.ExpiresAt)
	}
}

func TestVerifyAccessTokenAcceptsTokenIssuedToOwnClient(t *testing.T) {
	t.Parallel()
	issuer := newFakeIssuer(t)
	// Keycloak puts the requesting client in azp and only other clients in aud,
	// so a token obtained through kanpic's own client has no kanpic audience.
	raw := issuer.sign(t, map[string]any{"sub": "user-2", "typ": "Bearer", "azp": "kanpic", "aud": "account", "scope": "openid"})

	token, err := verifyWith(t, issuer.config(), raw)
	if err != nil {
		t.Fatalf("token issued to kanpic's client must be accepted: %v", err)
	}
	if token.Scopes != nil {
		t.Fatalf("a token without kanpic scopes keeps the user's own rights, got %v", token.Scopes)
	}
	if token.User.DisplayName != "user-2" {
		t.Fatalf("display name falls back to the subject: %q", token.User.DisplayName)
	}
}

func TestVerifyAccessTokenRejectsTokensNotMeantForKanpic(t *testing.T) {
	t.Parallel()
	issuer := newFakeIssuer(t)
	other := newFakeIssuer(t)
	cases := map[string]struct {
		config Config
		raw    string
	}{
		"other audience": {issuer.config(), issuer.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "azp": "hr-portal", "aud": "hr-portal"})},
		"id token":       {issuer.config(), issuer.sign(t, map[string]any{"sub": "u", "typ": "ID", "azp": "kanpic", "aud": "kanpic"})},
		"expired":        {issuer.config(), issuer.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "aud": "kanpic", "exp": time.Now().Add(-time.Minute).Unix()})},
		"no expiry":      {issuer.config(), issuer.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "aud": "kanpic", "exp": nil})},
		"empty subject":  {issuer.config(), issuer.sign(t, map[string]any{"sub": "", "typ": "Bearer", "aud": "kanpic"})},
		"other issuer":   {issuer.config(), other.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "aud": "kanpic"})},
		"tampered":       {issuer.config(), issuer.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "aud": "kanpic"})[:len(issuer.sign(t, map[string]any{"sub": "u"}))-4] + "AAAA"},
		"garbage":        {issuer.config(), "not.a.jwt"},
		"pinned audience": {func() Config {
			config := issuer.config()
			config.MCPAudiences = []string{"kanpic-mcp"}
			return config
		}(), issuer.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "azp": "claude", "aud": "kanpic"})},
	}
	for name, testCase := range cases {
		if _, err := verifyWith(t, testCase.config, testCase.raw); !errors.Is(err, ErrTokenRejected) {
			t.Errorf("%s: expected rejection, got %v", name, err)
		}
	}
}

func TestVerifyAccessTokenHonoursPinnedAudiences(t *testing.T) {
	t.Parallel()
	issuer := newFakeIssuer(t)
	config := issuer.config()
	config.MCPAudiences = []string{"kanpic-mcp"}
	raw := issuer.sign(t, map[string]any{"sub": "u", "typ": "Bearer", "azp": "claude", "aud": []string{"kanpic-mcp"}})
	if _, err := verifyWith(t, config, raw); err != nil {
		t.Fatalf("pinned audience must be accepted: %v", err)
	}
}

func TestProtectedResourceFollowsPublicURL(t *testing.T) {
	t.Parallel()
	config := Config{Enabled: true, MCPEnabled: true, IssuerURL: "https://id.example/realms/company/", PublicURL: "https://kanpic.example/"}
	if got := config.MCPResource("http://10.0.0.5:8080"); got != "https://kanpic.example/mcp" {
		t.Fatalf("public URL must win over the request origin: %q", got)
	}
	if got := config.MCPResourceMetadataURL("http://10.0.0.5:8080"); got != "https://kanpic.example/.well-known/oauth-protected-resource/mcp" {
		t.Fatalf("metadata URL: %q", got)
	}
	config.PublicURL = ""
	if got := config.MCPResource("http://10.0.0.5:8080/"); got != "http://10.0.0.5:8080/mcp" {
		t.Fatalf("without a public URL the request origin is used: %q", got)
	}
	if !config.MCPTokensEnabled() {
		t.Fatal("tokens are on when OIDC and the MCP switch are on")
	}
	config.Enabled = false
	if config.MCPTokensEnabled() {
		t.Fatal("tokens need OIDC: there is nobody to verify them against otherwise")
	}
}

func TestKanpicScopesIgnoreOIDCScopes(t *testing.T) {
	t.Parallel()
	if got := kanpicScopes("openid profile email roles web-origins offline_access"); got != nil {
		t.Fatalf("OIDC scopes are not kanpic scopes: %v", got)
	}
	if got := strings.Join(kanpicScopes("openid workbook.* workbook.* admin.* *"), " "); got != "* admin.* workbook.*" {
		t.Fatalf("kanpic scopes are kept once, sorted: %q", got)
	}
}
