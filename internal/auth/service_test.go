package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/oauth2"
)

func TestOAuthConfigForConfidentialClient(t *testing.T) {
	config := Config{ClientID: "kanpic", ClientSecret: "client-secret", Scopes: []string{"openid", "profile"}}
	endpoint := oauth2.Endpoint{AuthURL: "https://id.example/authorize", TokenURL: "https://id.example/token"}

	result := oauthConfigFor(config, endpoint, "https://kanpic.example/auth/callback")

	if result.ClientID != config.ClientID || result.ClientSecret != config.ClientSecret {
		t.Fatalf("client credentials not propagated: %#v", result)
	}
	if result.Endpoint != endpoint || result.RedirectURL != "https://kanpic.example/auth/callback" {
		t.Fatalf("OAuth endpoint configuration mismatch: %#v", result)
	}
}

func TestConfidentialClientExchangeSendsSecretAndPKCE(t *testing.T) {
	type credentials struct{ secret, verifier string }
	received := make(chan credentials, 1)
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse token request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, clientSecret, _ := r.BasicAuth()
		if clientSecret == "" {
			clientSecret = r.Form.Get("client_secret")
		}
		received <- credentials{secret: clientSecret, verifier: r.Form.Get("code_verifier")}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-token","token_type":"Bearer"}`))
	}))
	defer provider.Close()

	config := oauthConfigFor(
		Config{ClientID: "kanpic", ClientSecret: "client-secret"},
		oauth2.Endpoint{TokenURL: provider.URL},
		"https://kanpic.example/auth/callback",
	)
	if _, err := config.Exchange(context.Background(), "authorization-code", oauth2.VerifierOption("pkce-verifier")); err != nil {
		t.Fatal(err)
	}
	requestCredentials := <-received
	if requestCredentials.secret != "client-secret" || requestCredentials.verifier != "pkce-verifier" {
		t.Fatalf("token exchange credentials: secret=%q verifier=%q", requestCredentials.secret, requestCredentials.verifier)
	}
}

func TestBootstrapAdministratorRecognition(t *testing.T) {
	service := &Service{bootstrap: BootstrapCredentials{ID: "bootstrap-admin", Password: "correct-password"}}
	user := User{ID: "bootstrap-admin", Roles: []string{bootstrapAdminRole}}

	if !service.BootstrapEnabled() {
		t.Fatal("bootstrap login should be enabled")
	}
	if !service.IsAdmin(context.Background(), user) {
		t.Fatal("bootstrap session should be an administrator")
	}
	if secureEqual("correct-password", "wrong-password") {
		t.Fatal("different credentials compared equal")
	}
}
