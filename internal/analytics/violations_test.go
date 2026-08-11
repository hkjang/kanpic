package analytics

import (
	"testing"
	"time"
)

func recorder() *Recorder {
	moment := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
	made := NewRecorder()
	made.now = func() time.Time { moment = moment.Add(time.Second); return moment }
	return made
}

// The same blocked address repeats on every page view, so the recorder counts
// it instead of filling up with copies.
func TestRecorderCountsRepeatedOrigins(t *testing.T) {
	t.Parallel()
	store := recorder()
	for index := 0; index < 5; index++ {
		store.Record("https://momento.corp.example/collect/v1/events", "connect-src", "https://sheet.corp.example/")
	}
	items := store.List(Config{})
	if len(items) != 1 || items[0].Origin != "https://momento.corp.example" || items[0].Count != 5 {
		t.Fatalf("items=%#v", items)
	}
	if items[0].Directive != "connect-src" || items[0].Allowed {
		t.Fatalf("item=%#v", items[0])
	}
}

func TestRecorderSkipsWhatCannotBeAllowed(t *testing.T) {
	t.Parallel()
	store := recorder()
	for _, blocked := range []string{"", "data", "inline", "chrome-extension://abc/x.js"} {
		store.Record(blocked, "script-src", "/")
	}
	if items := store.List(Config{}); len(items) != 0 {
		t.Fatalf("items=%#v", items)
	}
}

// Once the configuration covers an origin the report is marked resolved, so a
// fixed snippet stops looking like an outstanding problem.
func TestRecorderMarksOriginsTheConfigurationAlreadyAllows(t *testing.T) {
	t.Parallel()
	store := recorder()
	store.Record("https://momento.corp.example/collect", "connect-src", "/")
	store.Record("https://region1.google-analytics.com/g/collect", "connect-src", "/")
	settings := config(map[string]any{"analytics.provider": "ga4", "analytics.measurement_id": "G-1", "analytics.allowed_hosts": "https://momento.corp.example"})
	for _, item := range store.List(settings) {
		if !item.Allowed {
			t.Errorf("%s should be reported as allowed", item.Origin)
		}
	}
	store.Forget()
	if items := store.List(Config{}); len(items) != 0 {
		t.Fatalf("forget left %#v", items)
	}
}

func TestRecorderStaysBounded(t *testing.T) {
	t.Parallel()
	store := recorder()
	for index := 0; index < MaxViolations+20; index++ {
		store.Record("https://host"+string(rune('a'+index%26))+string(rune('a'+index/26))+".corp.example/x", "img-src", "/")
	}
	if items := store.List(Config{}); len(items) > MaxViolations {
		t.Fatalf("recorder grew to %d", len(items))
	}
}

func TestAddAllowedHostKeepsTheExistingList(t *testing.T) {
	t.Parallel()
	if got := AddAllowedHost("", "https://a.example/"); got != "https://a.example" {
		t.Fatalf("got %q", got)
	}
	if got := AddAllowedHost("https://a.example", "https://b.example"); got != "https://a.example, https://b.example" {
		t.Fatalf("got %q", got)
	}
	if got := AddAllowedHost("https://a.example", "https://A.example"); got != "https://a.example" {
		t.Fatalf("duplicate added: %q", got)
	}
}
