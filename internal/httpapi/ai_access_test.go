package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"kanpic/internal/ai"
	"kanpic/internal/workbook"
)

// The AI routes name their workbook in the request body, which the URL based
// middleware cannot see, so the handlers have to check access themselves.
func TestAIRoutesRefuseWorkbooksTheCallerCannotRead(t *testing.T) {
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(NewPlatformWithAI(repository, nil, nil, nil, nil, &fakeAIOrchestrator{}, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	book := requestAs[workbook.Workbook](t, server, "owner@corp.example", http.MethodPost, "/api/v1/workbooks", map[string]any{"title": "비공개 워크북"}, http.StatusCreated)
	body := map[string]any{"workbook_id": book.ID, "sheet_id": book.Sheets[0].ID, "range": "A1:B2", "request": "요약", "mode": "summarize", "base_version": book.Version}

	// The owner reads the prompt that would be sent, including the cell count.
	preview := requestAs[ai.PromptPreview](t, server, "owner@corp.example", http.MethodPost, "/api/v1/ai/prompt:preview", body, http.StatusOK)
	if preview.SystemPrompt == "" || preview.UserContent == "" {
		t.Fatalf("owner preview=%#v", preview)
	}

	// Somebody without a share sees neither the prompt nor a plan.
	requestAs[map[string]any](t, server, "stranger@corp.example", http.MethodPost, "/api/v1/ai/prompt:preview", body, http.StatusForbidden)
	planBody := map[string]any{"workbook_id": book.ID, "sheet_id": book.Sheets[0].ID, "range": "A1:B2", "request": "요약", "mode": "summarize", "base_version": book.Version, "idempotency_key": "stranger-plan"}
	requestAs[map[string]any](t, server, "stranger@corp.example", http.MethodPost, "/api/v1/ai/actions:plan", planBody, http.StatusForbidden)

	// A viewer share is enough to read the prompt.
	requestAs[map[string]any](t, server, "owner@corp.example", http.MethodPut, "/api/v1/workbooks/"+book.ID+"/shares", map[string]any{"principal_type": "user", "principal_id": "reader@corp.example", "role": "viewer"}, http.StatusOK)
	requestAs[ai.PromptPreview](t, server, "reader@corp.example", http.MethodPost, "/api/v1/ai/prompt:preview", body, http.StatusOK)
}
