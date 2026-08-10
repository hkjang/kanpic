package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"kanpic/internal/ai"
	"kanpic/internal/workbook"
)

// The console reads the history, exports it and prunes it through these routes,
// so they have to filter, format and stay behind the administrator check.
func TestAdminAIHistoryRoutes(t *testing.T) {
	server := httptest.NewServer(NewPlatformWithAI(workbook.NewMemoryRepository(), nil, nil, nil, nil, &fakeAIOrchestrator{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	page := request[ai.HistoryPage](t, server, http.MethodGet, "/api/v1/admin/ai/actions?limit=10", nil, http.StatusOK)
	if page.Summary.Total != 1 || len(page.Items) != 1 || page.Items[0].PromptTokens != 640 {
		t.Fatalf("history page=%#v", page)
	}
	if page.Summary.RetentionDays != 30 {
		t.Fatalf("retention days=%d", page.Summary.RetentionDays)
	}

	// A filter that matches nothing comes back empty rather than unfiltered.
	empty := request[ai.HistoryPage](t, server, http.MethodGet, "/api/v1/admin/ai/actions?status=failed", nil, http.StatusOK)
	if len(empty.Items) != 0 {
		t.Fatalf("filtered page=%#v", empty)
	}

	response, err := http.Get(server.URL + "/api/v1/admin/ai/actions?format=csv")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/csv") {
		t.Fatalf("csv content type=%q", contentType)
	}
	if !strings.Contains(string(body), "사용자") || !strings.Contains(string(body), "alice") {
		t.Fatalf("csv body=%q", string(body))
	}

	action := request[ai.Action](t, server, http.MethodGet, "/api/v1/admin/ai/actions/ai-action", nil, http.StatusOK)
	_ = action

	purged := request[struct {
		Removed int64 `json:"removed"`
	}](t, server, http.MethodDelete, "/api/v1/admin/ai/actions?before=2026-01-01", nil, http.StatusOK)
	if purged.Removed != 3 {
		t.Fatalf("purged=%#v", purged)
	}
	// A purge without a usable cutoff is refused instead of deleting everything.
	request[map[string]any](t, server, http.MethodDelete, "/api/v1/admin/ai/actions", nil, http.StatusBadRequest)
}
