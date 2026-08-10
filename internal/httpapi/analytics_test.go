package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"kanpic/internal/analytics"
)

func writeFile(path, content string) error { return os.WriteFile(path, []byte(content), 0o600) }

func requestWithNonce(request *http.Request, nonce string) *http.Request {
	return request.WithContext(context.WithValue(request.Context(), nonceKey{}, nonce))
}

// The page shell, the injected snippet and the policy header have to agree, so
// they are checked together rather than one at a time.
func TestServeIndexInjectsTrackingWithAMatchingNonce(t *testing.T) {
	t.Parallel()
	directory := t.TempDir()
	page := "<!doctype html><html><head><title>kanpic</title></head><body><div id=\"root\"></div></body></html>"
	if err := writeFile(directory+"/index.html", page); err != nil {
		t.Fatal(err)
	}
	server := &Server{}
	config := analytics.ReadConfig(map[string]any{
		"analytics.enabled": true, "analytics.provider": "ga4", "analytics.measurement_id": "G-TEST1",
	})

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/workbooks/abc", nil)
	nonce := "test-nonce"
	request = requestWithNonce(request, nonce)
	server.serveIndexWith(recorder, request, directory, config)

	body := recorder.Body.String()
	if !strings.Contains(body, "googletagmanager.com/gtag/js?id=G-TEST1") {
		t.Fatalf("snippet was not injected: %s", body)
	}
	if !strings.Contains(body, `nonce="test-nonce"`) {
		t.Fatalf("nonce was not applied: %s", body)
	}
	// The snippet goes inside head, before the closing tag.
	if strings.Index(body, "gtag/js") > strings.Index(body, "</head>") {
		t.Fatalf("snippet is not in head: %s", body)
	}

	// The policy allows exactly that nonce and the vendor origin, and keeps the
	// rest of the strict policy intact.
	policy := (&Server{}).policyFor(config, "/workbooks/abc", nonce)
	for _, expected := range []string{"'nonce-test-nonce'", "https://www.googletagmanager.com", "default-src 'self'", "frame-ancestors 'none'"} {
		if !strings.Contains(policy, expected) {
			t.Errorf("policy is missing %q: %s", expected, policy)
		}
	}
	if strings.Contains(policy, "unsafe-inline") {
		t.Fatalf("the policy must not be relaxed: %s", policy)
	}

	// A console page is left untouched, and so is its policy.
	adminRecorder := httptest.NewRecorder()
	adminRequest := requestWithNonce(httptest.NewRequest(http.MethodGet, "/admin", nil), nonce)
	server.serveIndexWith(adminRecorder, adminRequest, directory, config)
	if strings.Contains(adminRecorder.Body.String(), "gtag/js") {
		t.Fatal("the console should not be tracked by default")
	}
	if policy := (&Server{}).policyFor(config, "/admin", nonce); strings.Contains(policy, "googletagmanager") {
		t.Fatalf("console policy=%s", policy)
	}
}
