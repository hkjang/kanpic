// Package analytics injects a visitor tracking snippet into the served pages.
//
// The content security policy shipped with kanpic allows scripts from the
// application origin only, so a tracking snippet cannot simply be pasted in.
// This package produces both halves of the answer: the markup to inject and the
// policy sources it needs, with a per-request nonce so inline code runs without
// weakening the policy for everything else.
package analytics

import (
	"fmt"
	"html"
	"net/url"
	"strings"
)

const (
	ProviderNone   = "none"
	ProviderGA4    = "ga4"
	ProviderGTM    = "gtm"
	ProviderMatomo = "matomo"
	ProviderCustom = "custom"

	MaxSnippetBytes = 8 * 1024
)

type Config struct {
	Enabled       bool   `json:"enabled"`
	Provider      string `json:"provider"`
	MeasurementID string `json:"measurement_id,omitempty"`
	MatomoURL     string `json:"matomo_url,omitempty"`
	MatomoSiteID  string `json:"matomo_site_id,omitempty"`
	CustomSnippet string `json:"custom_snippet,omitempty"`
	AllowedHosts  string `json:"allowed_hosts,omitempty"`
	IncludeAdmin  bool   `json:"include_admin"`
	Placement     string `json:"placement"`
}

// ReadConfig maps the stored settings onto the configuration.
func ReadConfig(values map[string]any) Config {
	config := Config{Provider: ProviderNone, Placement: "head"}
	config.Enabled, _ = values["analytics.enabled"].(bool)
	config.Provider = stringValue(values, "analytics.provider", ProviderNone)
	config.MeasurementID = stringValue(values, "analytics.measurement_id", "")
	config.MatomoURL = stringValue(values, "analytics.matomo_url", "")
	config.MatomoSiteID = stringValue(values, "analytics.matomo_site_id", "")
	config.CustomSnippet = stringValue(values, "analytics.custom_snippet", "")
	config.AllowedHosts = stringValue(values, "analytics.allowed_hosts", "")
	config.IncludeAdmin, _ = values["analytics.include_admin"].(bool)
	config.Placement = strings.ToLower(stringValue(values, "analytics.placement", "head"))
	if config.Placement != "body" {
		config.Placement = "head"
	}
	return config
}

// Active reports whether a page should carry the snippet. Administrative pages
// are excluded unless an administrator asks for them, because console traffic
// is rarely the visitor data anybody wants.
func (c Config) Active(path string) bool {
	if !c.Enabled || c.Provider == ProviderNone || c.Provider == "" {
		return false
	}
	if !c.IncludeAdmin && (strings.HasPrefix(path, "/admin") || strings.HasPrefix(path, "/preferences")) {
		return false
	}
	return strings.TrimSpace(c.Snippet("")) != ""
}

// Validate reports what is missing for the chosen provider.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	switch c.Provider {
	case ProviderNone, "":
		return nil
	case ProviderGA4, ProviderGTM:
		if strings.TrimSpace(c.MeasurementID) == "" {
			return fmt.Errorf("analytics.measurement_id가 필요합니다")
		}
	case ProviderMatomo:
		if strings.TrimSpace(c.MatomoURL) == "" || strings.TrimSpace(c.MatomoSiteID) == "" {
			return fmt.Errorf("analytics.matomo_url과 analytics.matomo_site_id가 필요합니다")
		}
		if _, err := url.Parse(c.MatomoURL); err != nil {
			return fmt.Errorf("analytics.matomo_url이 올바른 주소가 아닙니다")
		}
	case ProviderCustom:
		if strings.TrimSpace(c.CustomSnippet) == "" {
			return fmt.Errorf("analytics.custom_snippet이 비어 있습니다")
		}
		if len(c.CustomSnippet) > MaxSnippetBytes {
			return fmt.Errorf("추적 코드는 %d바이트를 넘을 수 없습니다", MaxSnippetBytes)
		}
	default:
		return fmt.Errorf("analytics.provider는 none, ga4, gtm, matomo, custom 중 하나여야 합니다")
	}
	return nil
}

// Snippet renders the markup to inject. The nonce is applied to every script
// tag in the snippet so the policy can stay strict.
func (c Config) Snippet(nonce string) string {
	switch c.Provider {
	case ProviderGA4:
		id := html.EscapeString(strings.TrimSpace(c.MeasurementID))
		if id == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script async src="https://www.googletagmanager.com/gtag/js?id=%s"></script>
<script>window.dataLayer=window.dataLayer||[];function gtag(){dataLayer.push(arguments);}gtag('js',new Date());gtag('config','%s');</script>`, id, id), nonce)
	case ProviderGTM:
		id := html.EscapeString(strings.TrimSpace(c.MeasurementID))
		if id == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>(function(w,d,s,l,i){w[l]=w[l]||[];w[l].push({'gtm.start':new Date().getTime(),event:'gtm.js'});var f=d.getElementsByTagName(s)[0],j=d.createElement(s),dl=l!='dataLayer'?'&l='+l:'';j.async=true;j.src='https://www.googletagmanager.com/gtm.js?id='+i+dl;f.parentNode.insertBefore(j,f);})(window,document,'script','dataLayer','%s');</script>`, id), nonce)
	case ProviderMatomo:
		base := strings.TrimRight(strings.TrimSpace(c.MatomoURL), "/")
		site := html.EscapeString(strings.TrimSpace(c.MatomoSiteID))
		if base == "" || site == "" {
			return ""
		}
		return withNonce(fmt.Sprintf(`<script>var _paq=window._paq=window._paq||[];_paq.push(['trackPageView']);_paq.push(['enableLinkTracking']);(function(){var u="%s/";_paq.push(['setTrackerUrl',u+'matomo.php']);_paq.push(['setSiteId','%s']);var d=document,g=d.createElement('script'),s=d.getElementsByTagName('script')[0];g.async=true;g.src=u+'matomo.js';s.parentNode.insertBefore(g,s);})();</script>`, html.EscapeString(base), site), nonce)
	case ProviderCustom:
		return withNonce(strings.TrimSpace(c.CustomSnippet), nonce)
	}
	return ""
}

// withNonce adds the nonce to every script tag that does not already carry one,
// which is what lets a pasted snippet run under a strict policy unchanged.
func withNonce(snippet, nonce string) string {
	if nonce == "" || snippet == "" {
		return snippet
	}
	var builder strings.Builder
	remaining := snippet
	for {
		index := strings.Index(strings.ToLower(remaining), "<script")
		if index < 0 {
			builder.WriteString(remaining)
			return builder.String()
		}
		end := index + len("<script")
		builder.WriteString(remaining[:end])
		closing := strings.Index(remaining[end:], ">")
		tag := remaining[end:]
		if closing >= 0 {
			tag = remaining[end : end+closing]
		}
		if !strings.Contains(strings.ToLower(tag), "nonce=") {
			builder.WriteString(fmt.Sprintf(` nonce="%s"`, html.EscapeString(nonce)))
		}
		remaining = remaining[end:]
	}
}

// PolicySources lists the extra origins the snippet needs, derived from the
// provider so a common setup needs no policy knowledge at all.
func (c Config) PolicySources() (scripts []string, connects []string, images []string) {
	switch c.Provider {
	case ProviderGA4, ProviderGTM:
		scripts = append(scripts, "https://www.googletagmanager.com")
		connects = append(connects, "https://www.google-analytics.com", "https://analytics.google.com", "https://*.google-analytics.com")
		images = append(images, "https://www.google-analytics.com", "https://www.googletagmanager.com")
	case ProviderMatomo:
		if origin := originOf(c.MatomoURL); origin != "" {
			scripts = append(scripts, origin)
			connects = append(connects, origin)
			images = append(images, origin)
		}
	}
	for _, host := range strings.FieldsFunc(c.AllowedHosts, func(letter rune) bool { return letter == ',' || letter == ' ' || letter == '\n' }) {
		trimmed := strings.TrimSpace(host)
		if trimmed == "" {
			continue
		}
		scripts = append(scripts, trimmed)
		connects = append(connects, trimmed)
		images = append(images, trimmed)
	}
	return scripts, connects, images
}

func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return ""
	}
	scheme := parsed.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + parsed.Host
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}
