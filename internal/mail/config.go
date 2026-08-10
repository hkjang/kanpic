package mail

import (
	"context"
	"strings"
	"time"
)

// Defaults aim at the common case: an internal relay on port 25 that accepts
// mail from the network without credentials.
const (
	defaultPort     = 25
	defaultSecurity = "auto"
	defaultTimeout  = 10 * time.Second
)

var eventSettings = map[string]string{
	EventShareGranted:  "mail.notify_share",
	EventComment:       "mail.notify_comment",
	EventMention:       "mail.notify_mention",
	EventAccessRequest: "mail.notify_access_request",
	EventAccessDecided: "mail.notify_access_request",
}

// ConfigFromValues builds the configuration from an already loaded settings
// map, which is how the settings screen tests the relay.
func ConfigFromValues(values map[string]any) (Config, error) {
	return readValues(values), nil
}

func readConfig(ctx context.Context, provider settingsProvider) (Config, error) {
	config := Config{Port: defaultPort, Security: defaultSecurity, Timeout: defaultTimeout, Events: map[string]bool{}}
	if provider == nil {
		return config, nil
	}
	values, err := provider.Values(ctx)
	if err != nil {
		return Config{}, err
	}
	return readValues(values), nil
}

func readValues(values map[string]any) Config {
	config := Config{Port: defaultPort, Security: defaultSecurity, Timeout: defaultTimeout, Events: map[string]bool{}}
	config.Enabled, _ = values["mail.enabled"].(bool)
	config.Host = stringValue(values, "mail.smtp_host", "")
	config.Username = stringValue(values, "mail.username", "")
	config.Password = stringValue(values, "mail.password", "")
	config.FromAddress = stringValue(values, "mail.from_address", "")
	config.FromName = stringValue(values, "mail.from_name", "kanpic")
	config.Security = strings.ToLower(stringValue(values, "mail.security", defaultSecurity))
	config.BaseURL = stringValue(values, "mail.base_url", "")
	config.SkipVerify, _ = values["mail.skip_tls_verify"].(bool)
	if port, ok := numberValue(values, "mail.smtp_port"); ok && port > 0 {
		config.Port = port
	}
	if seconds, ok := numberValue(values, "mail.timeout_seconds"); ok && seconds > 0 {
		config.Timeout = time.Duration(seconds) * time.Second
	}
	// A relay on the implicit TLS port needs no extra configuration.
	if config.Security == defaultSecurity && config.Port == 465 {
		config.Security = "tls"
	}
	for event, key := range eventSettings {
		if enabled, ok := values[key].(bool); ok {
			config.Events[event] = enabled
		}
	}
	if strings.TrimSpace(config.FromAddress) == "" && strings.TrimSpace(config.Host) != "" {
		config.FromAddress = "kanpic@" + config.Host
	}
	return config
}

func stringValue(values map[string]any, key, fallback string) string {
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func numberValue(values map[string]any, key string) (int, bool) {
	switch typed := values[key].(type) {
	case float64:
		return int(typed), true
	case int:
		return typed, true
	case int64:
		return int(typed), true
	}
	return 0, false
}
