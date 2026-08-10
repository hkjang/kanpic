package analytics

import (
	"strings"
	"testing"
)

func config(values map[string]any) Config {
	base := map[string]any{"analytics.enabled": true}
	for key, value := range values {
		base[key] = value
	}
	return ReadConfig(base)
}

func TestPresetSnippetsCarryTheNonceAndIdentifiers(t *testing.T) {
	t.Parallel()
	ga4 := config(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-ABC123"})
	snippet := ga4.Snippet("n0nce")
	if !strings.Contains(snippet, "googletagmanager.com/gtag/js?id=G-ABC123") || !strings.Contains(snippet, "gtag('config','G-ABC123')") {
		t.Fatalf("ga4 snippet=%s", snippet)
	}
	// Both the loader and the inline configuration must carry the nonce.
	if strings.Count(snippet, `nonce="n0nce"`) != 2 {
		t.Fatalf("ga4 nonce count=%d in %s", strings.Count(snippet, `nonce="n0nce"`), snippet)
	}

	matomo := config(map[string]any{"analytics.provider": "matomo", "analytics.matomo_url": "https://matomo.corp.example/", "analytics.matomo_site_id": "7"})
	snippet = matomo.Snippet("n1")
	// The tracker builds its own URLs from the base, so the base and the site
	// id are what the snippet has to carry.
	if !strings.Contains(snippet, `u="https://matomo.corp.example/"`) || !strings.Contains(snippet, "u+'matomo.js'") || !strings.Contains(snippet, "setSiteId','7'") {
		t.Fatalf("matomo snippet=%s", snippet)
	}

	gtm := config(map[string]any{"analytics.provider": "gtm", "analytics.measurement_id": "GTM-XYZ"})
	if !strings.Contains(gtm.Snippet(""), "'GTM-XYZ'") {
		t.Fatalf("gtm snippet=%s", gtm.Snippet(""))
	}
}

// A pasted snippet runs unchanged except for the nonce, and one that already
// carries a nonce is left alone.
func TestCustomSnippetIsNoncedWithoutRewriting(t *testing.T) {
	t.Parallel()
	custom := config(map[string]any{"analytics.provider": "custom",
		"analytics.custom_snippet": `<script src="https://stats.corp.example/t.js"></script><script>track({site:1})</script>`})
	snippet := custom.Snippet("abc")
	if strings.Count(snippet, `nonce="abc"`) != 2 || !strings.Contains(snippet, "track({site:1})") {
		t.Fatalf("custom snippet=%s", snippet)
	}
	already := config(map[string]any{"analytics.provider": "custom", "analytics.custom_snippet": `<script nonce="mine">x()</script>`})
	if strings.Count(already.Snippet("abc"), "nonce=") != 1 {
		t.Fatalf("existing nonce was duplicated: %s", already.Snippet("abc"))
	}
}

func TestActiveSkipsAdminPagesUnlessRequested(t *testing.T) {
	t.Parallel()
	tracked := config(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-1"})
	if !tracked.Active("/workbooks/abc") {
		t.Fatal("a normal page should be tracked")
	}
	if tracked.Active("/admin?tab=mail") || tracked.Active("/preferences") {
		t.Fatal("console pages are excluded by default")
	}
	including := config(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-1", "analytics.include_admin": true})
	if !including.Active("/admin") {
		t.Fatal("console pages should be tracked when asked for")
	}
	// Switching the feature off stops everything, even with a provider set.
	off := ReadConfig(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-1"})
	if off.Active("/") {
		t.Fatal("tracking must stay off until it is enabled")
	}
}

func TestPolicySourcesFollowTheProvider(t *testing.T) {
	t.Parallel()
	scripts, connects, images := config(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-1"}).PolicySources()
	if !contains(scripts, "https://www.googletagmanager.com") || !contains(connects, "https://www.google-analytics.com") || len(images) == 0 {
		t.Fatalf("ga4 sources=%v %v %v", scripts, connects, images)
	}
	// A Matomo host is derived from the configured URL rather than typed twice.
	scripts, connects, _ = config(map[string]any{"analytics.provider": "matomo", "analytics.matomo_url": "https://matomo.corp.example/path", "analytics.matomo_site_id": "3"}).PolicySources()
	if !contains(scripts, "https://matomo.corp.example") || !contains(connects, "https://matomo.corp.example") {
		t.Fatalf("matomo sources=%v %v", scripts, connects)
	}
	// A custom snippet declares its own hosts.
	scripts, _, _ = config(map[string]any{"analytics.provider": "custom", "analytics.custom_snippet": "<script>x()</script>",
		"analytics.allowed_hosts": "https://stats.corp.example, https://cdn.corp.example"}).PolicySources()
	if !contains(scripts, "https://stats.corp.example") || !contains(scripts, "https://cdn.corp.example") {
		t.Fatalf("custom sources=%v", scripts)
	}
}

func TestValidateReportsMissingConfiguration(t *testing.T) {
	t.Parallel()
	for name, values := range map[string]map[string]any{
		"ga4 without id":      {"analytics.provider": "ga4"},
		"matomo without site": {"analytics.provider": "matomo", "analytics.matomo_url": "https://matomo.corp.example"},
		"custom without code": {"analytics.provider": "custom"},
		"unknown provider":    {"analytics.provider": "spyware"},
		"custom that is huge": {"analytics.provider": "custom", "analytics.custom_snippet": strings.Repeat("x", MaxSnippetBytes+1)},
	} {
		if err := config(values).Validate(); err == nil {
			t.Errorf("%s should be reported", name)
		}
	}
	if err := config(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-1"}).Validate(); err != nil {
		t.Fatalf("a complete configuration should pass: %v", err)
	}
	// Nothing is required while the feature is off.
	if err := ReadConfig(map[string]any{"analytics.provider": "ga4"}).Validate(); err != nil {
		t.Fatalf("disabled configuration=%v", err)
	}
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}
