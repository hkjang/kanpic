package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

// MCPResourcePath is the resource MCP clients ask an access token for.
const MCPResourcePath = "/mcp"

// ProtectedResourceMetadataPath is where RFC 9728 metadata for the MCP resource
// is served. A client that gets a 401 from /mcp reads it to learn which
// authorization server to send the user to.
const ProtectedResourceMetadataPath = "/.well-known/oauth-protected-resource"

// ErrTokenDisabled means the deployment does not accept OAuth access tokens, so a
// Bearer credential can only be an API key.
var ErrTokenDisabled = errors.New("OAuth access tokens are not accepted")

// ErrTokenRejected wraps every reason a presented token failed verification.
var ErrTokenRejected = errors.New("OAuth access token rejected")

// AccessToken is a verified OAuth 2.1 access token issued by the configured
// identity provider. The token stands for the user it was issued to, so the user
// keeps every right they would have after signing in with the browser. Scopes
// narrow that: a token that carries kanpic scopes is treated like an API key with
// those scopes.
type AccessToken struct {
	User     User
	Scopes   []string
	ClientID string
}

// ProtectedResource is the RFC 9728 document describing /mcp.
type ProtectedResource struct {
	Resource               string   `json:"resource"`
	AuthorizationServers   []string `json:"authorization_servers"`
	BearerMethodsSupported []string `json:"bearer_methods_supported"`
	ScopesSupported        []string `json:"scopes_supported,omitempty"`
	ResourceName           string   `json:"resource_name,omitempty"`
	// MetadataURL is where this document is served; it goes into the
	// WWW-Authenticate challenge rather than into the document itself.
	MetadataURL string `json:"-"`
}

// mcpScopesSupported lists the scope names a Keycloak administrator may define as
// client scopes to narrow a token. It is advisory: a token without any of them
// acts with the user's own rights.
var mcpScopesSupported = []string{
	"workbook.read", "workbook.write", "range.read", "range.write", "formula.write",
	"chart.read", "chart.write", "pivot.read", "pivot.write", "format.read", "format.write",
	"automation.read", "automation.write", "automation.run", "presentation.read", "presentation.write",
	"ai.use", "admin.*",
}

// MCPTokensEnabled reports whether /mcp accepts access tokens from the identity
// provider. It needs OIDC to be on: the issuer is where tokens are verified.
func (c Config) MCPTokensEnabled() bool {
	return c.Enabled && c.MCPEnabled && strings.TrimSpace(c.IssuerURL) != ""
}

// Origin is the address clients see: the configured public URL when the server
// sits behind a proxy, otherwise the origin the request arrived at.
func (c Config) Origin(requestOrigin string) string {
	if public := strings.TrimRight(strings.TrimSpace(c.PublicURL), "/"); public != "" {
		return public
	}
	return strings.TrimRight(requestOrigin, "/")
}

// MCPResource is the resource identifier (RFC 8707) a token must be issued for.
func (c Config) MCPResource(requestOrigin string) string {
	return c.Origin(requestOrigin) + MCPResourcePath
}

// MCPResourceMetadataURL is where a client finds the protected resource document.
func (c Config) MCPResourceMetadataURL(requestOrigin string) string {
	return c.Origin(requestOrigin) + ProtectedResourceMetadataPath + MCPResourcePath
}

// acceptedAudiences lists the aud values a token may carry. Administrators can
// pin them; otherwise a token for kanpic's own client or for the resource URL
// is accepted.
func (c Config) acceptedAudiences(resource string) []string {
	accepted := make([]string, 0, len(c.MCPAudiences)+2)
	for _, audience := range c.MCPAudiences {
		if trimmed := strings.TrimSpace(audience); trimmed != "" {
			accepted = append(accepted, trimmed)
		}
	}
	if len(accepted) > 0 {
		return accepted
	}
	return []string{c.ClientID, resource}
}

// ProtectedResource returns the RFC 9728 document for /mcp and whether it is
// served at all.
func (s *Service) ProtectedResource(ctx context.Context, requestOrigin string) (ProtectedResource, bool, error) {
	config, err := s.Config(ctx)
	if err != nil {
		return ProtectedResource{}, false, err
	}
	if !config.MCPTokensEnabled() {
		return ProtectedResource{}, false, nil
	}
	return ProtectedResource{
		Resource:               config.MCPResource(requestOrigin),
		AuthorizationServers:   []string{strings.TrimRight(config.IssuerURL, "/")},
		BearerMethodsSupported: []string{"header"},
		ScopesSupported:        mcpScopesSupported,
		ResourceName:           "kanpic MCP",
		MetadataURL:            config.MCPResourceMetadataURL(requestOrigin),
	}, true, nil
}

// VerifyAccessToken checks a Bearer token against the identity provider's keys
// and returns the user it was issued to. The provider discovery document and key
// set are cached, so a call normally costs no round trip.
func (s *Service) VerifyAccessToken(ctx context.Context, requestOrigin, raw string) (AccessToken, error) {
	config, err := s.Config(ctx)
	if err != nil {
		return AccessToken{}, err
	}
	if !config.MCPTokensEnabled() {
		return AccessToken{}, ErrTokenDisabled
	}
	provider, client, err := s.providers.get(config)
	if err != nil {
		return AccessToken{}, fmt.Errorf("%w: discover issuer: %v", ErrTokenRejected, err)
	}
	// Key fetches during verification use the request's context and the
	// provider's client, so they end with the request and trust the same CA.
	verifyContext, cancel := context.WithTimeout(oidc.ClientContext(ctx, client), 10*time.Second)
	defer cancel()
	return verifyAccessToken(verifyContext, provider, config, config.MCPResource(requestOrigin), raw)
}

// verifyAccessToken is the pure verification step, separated so it can be
// exercised against a fake issuer without a database.
func verifyAccessToken(ctx context.Context, provider *oidc.Provider, config Config, resource, raw string) (AccessToken, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return AccessToken{}, fmt.Errorf("%w: empty token", ErrTokenRejected)
	}
	verifier := provider.VerifierContext(ctx, &oidc.Config{
		SkipClientIDCheck:    true,
		SupportedSigningAlgs: []string{oidc.RS256, oidc.RS384, oidc.RS512, oidc.ES256, oidc.ES384, oidc.ES512, oidc.PS256, oidc.PS384, oidc.PS512},
	})
	token, err := verifier.Verify(ctx, raw)
	if err != nil {
		return AccessToken{}, fmt.Errorf("%w: %v", ErrTokenRejected, err)
	}
	var claims struct {
		Type              string `json:"typ"`
		Email             string `json:"email"`
		PreferredUsername string `json:"preferred_username"`
		Name              string `json:"name"`
		AuthorizedParty   string `json:"azp"`
		Scope             string `json:"scope"`
		RealmAccess       struct {
			Roles []string `json:"roles"`
		} `json:"realm_access"`
	}
	if err := token.Claims(&claims); err != nil {
		return AccessToken{}, fmt.Errorf("%w: read claims: %v", ErrTokenRejected, err)
	}
	// An ID token proves who signed in to some client; it is not a grant to call
	// this server. Keycloak marks them, so they can be told apart.
	if strings.EqualFold(claims.Type, "ID") {
		return AccessToken{}, fmt.Errorf("%w: ID token presented as access token", ErrTokenRejected)
	}
	if strings.TrimSpace(token.Subject) == "" {
		return AccessToken{}, fmt.Errorf("%w: subject is empty", ErrTokenRejected)
	}
	if !audienceAccepted(token.Audience, claims.AuthorizedParty, config.ClientID, config.acceptedAudiences(resource)) {
		return AccessToken{}, fmt.Errorf("%w: audience %v is not for this resource", ErrTokenRejected, token.Audience)
	}
	displayName := claims.Name
	if displayName == "" {
		displayName = claims.PreferredUsername
	}
	if displayName == "" {
		displayName = claims.Email
	}
	if displayName == "" {
		displayName = token.Subject
	}
	user := User{ID: token.Subject, Email: claims.Email, DisplayName: displayName, Roles: claims.RealmAccess.Roles, ExpiresAt: token.Expiry.UTC()}
	return AccessToken{User: user, Scopes: kanpicScopes(claims.Scope), ClientID: claims.AuthorizedParty}, nil
}

// audienceAccepted implements the resource check: the token must name this
// server (or a configured alias) in aud, or have been issued to kanpic's own
// client, which Keycloak records in azp rather than aud.
func audienceAccepted(audiences []string, authorizedParty, clientID string, accepted []string) bool {
	for _, audience := range audiences {
		for _, candidate := range accepted {
			if audience == candidate {
				return true
			}
		}
	}
	return strings.TrimSpace(authorizedParty) != "" && authorizedParty == clientID
}

// kanpicScopes keeps the scopes in a token's scope claim that mean something to
// kanpic. OIDC scopes such as openid, profile and email have no dot; kanpic
// scopes always do, apart from the wildcard.
func kanpicScopes(claim string) []string {
	seen := make(map[string]struct{})
	for _, scope := range strings.Fields(claim) {
		if scope != "*" && !strings.Contains(scope, ".") {
			continue
		}
		seen[scope] = struct{}{}
	}
	if len(seen) == 0 {
		return nil
	}
	scopes := make([]string, 0, len(seen))
	for scope := range seen {
		scopes = append(scopes, scope)
	}
	sort.Strings(scopes)
	return scopes
}

// providerCache keeps one discovered provider per issuer. Discovery costs a round
// trip and the remote key set caches keys itself, so verifying a token normally
// touches the network only when Keycloak rotates its keys.
type providerCache struct {
	mu       sync.Mutex
	key      string
	provider *oidc.Provider
	client   *http.Client
	fetched  time.Time
}

const providerCacheTTL = time.Hour

func (c *providerCache) get(config Config) (*oidc.Provider, *http.Client, error) {
	key := strings.TrimRight(config.IssuerURL, "/") + "\n" + config.CAPEM
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.provider != nil && c.key == key && time.Since(c.fetched) < providerCacheTTL {
		return c.provider, c.client, nil
	}
	client, err := httpClientFor(config)
	if err != nil {
		return nil, nil, err
	}
	// The key set may refresh keys long after the request that triggered
	// discovery has finished, so discovery must not run on a request context.
	provider, err := oidc.NewProvider(oidc.ClientContext(context.Background(), client), strings.TrimRight(config.IssuerURL, "/"))
	if err != nil {
		return nil, nil, err
	}
	c.key, c.provider, c.client, c.fetched = key, provider, client, time.Now()
	return provider, client, nil
}
