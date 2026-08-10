package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"kanpic/internal/database"
	"kanpic/internal/workbook"
	"kanpic/pkg/identity"
)

type staticSettings map[string]any

func (s staticSettings) Values(context.Context) (map[string]any, error) { return s, nil }

// The history queries aggregate JSON payloads and join three tables, which the
// in-memory repository cannot exercise, so they run against a real PostgreSQL
// when KANPIC_TEST_DSN is set.
func TestHistoryListsFiltersAndPurges(t *testing.T) {
	dsn := os.Getenv("KANPIC_TEST_DSN")
	if dsn == "" {
		t.Skip("KANPIC_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	defer pool.Close()
	repository := workbook.NewPostgresRepository(pool)
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "AI 이력 테스트", WorkspaceID: "default", OwnerID: "owner@corp.example"})
	if err != nil {
		t.Fatalf("create workbook: %v", err)
	}
	t.Cleanup(func() { _ = repository.PurgeWorkbook(ctx, book.ID) })

	service := &Service{pool: pool, settings: staticSettings{"ai.history_retention_days": float64(30)}, workbooks: repository,
		logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		now:    func() time.Time { return time.Now().UTC() }, limits: map[string]cachedLimits{}}

	old := time.Now().UTC().AddDate(0, 0, -40)
	seed := []struct {
		actor, mode, status string
		changes             string
		created             time.Time
		prompt, completion  int
	}{
		{"park@corp.example", ModeSummarize, StatusCompleted, `[]`, time.Now().UTC(), 1500, 210},
		{"park@corp.example", ModeFormula, StatusApplied, `[{"row":1,"column":1}]`, time.Now().UTC(), 900, 120},
		{"lee@corp.example", ModeClean, StatusFailed, `[]`, old, 400, 0},
		{"lee@corp.example", ModeFormula, StatusPlanned, `[{"row":2,"column":2}]`, old, 300, 60},
	}
	for index, row := range seed {
		id := identity.New()
		if _, err := pool.Exec(ctx, `INSERT INTO ai_actions(id,workbook_id,sheet_id,actor_id,idempotency_key,mode,selected_range,request,status,base_version,model,summary,changes,findings,input_cell_count,created_at,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,'A1:B2',$7,$8,1,'corp-llm','요약',$9,'[]'::jsonb,4,$10,$10)`,
			id, book.ID, book.Sheets[0].ID, row.actor, fmt.Sprintf("seed-%s-%d", book.ID, index), row.mode, "요청 "+row.mode, row.status, row.changes, row.created); err != nil {
			t.Fatalf("seed action: %v", err)
		}
		payload, _ := json.Marshal(map[string]any{"usage": Usage{PromptTokens: row.prompt, CompletionTokens: row.completion, Attempts: 1}})
		if _, err := pool.Exec(ctx, `INSERT INTO ai_action_events(action_id,actor_id,event_type,model,tool_name,payload,created_at) VALUES($1,$2,'planned','corp-llm','range.read',$3,$4)`,
			id, row.actor, payload, row.created); err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}

	page, err := service.History(ctx, HistoryFilter{WorkbookID: book.ID})
	if err != nil {
		t.Fatalf("history: %v", err)
	}
	if page.Summary.Total != 4 || len(page.Items) != 4 {
		t.Fatalf("history total=%d items=%d", page.Summary.Total, len(page.Items))
	}
	if page.Summary.PromptTokens != 3100 || page.Summary.CompletionTokens != 390 {
		t.Fatalf("token totals=%#v", page.Summary)
	}
	if page.Summary.ByStatus[StatusFailed] != 1 || page.Summary.ByMode[ModeFormula] != 2 {
		t.Fatalf("groupings=%#v %#v", page.Summary.ByStatus, page.Summary.ByMode)
	}
	// Applied cells only count the plans that were actually written.
	if page.Summary.AppliedCells != 1 {
		t.Fatalf("applied cells=%d", page.Summary.AppliedCells)
	}
	if len(page.Summary.TopActors) != 2 || page.Summary.TopActors[0].ActorID != "park@corp.example" || page.Summary.TopActors[0].Tokens != 2730 {
		t.Fatalf("top actors=%#v", page.Summary.TopActors)
	}
	if page.Items[0].WorkbookTitle != "AI 이력 테스트" || page.Items[0].PromptTokens == 0 {
		t.Fatalf("first row=%#v", page.Items[0])
	}

	for name, filter := range map[string]HistoryFilter{
		"actor":  {WorkbookID: book.ID, Actor: "lee"},
		"mode":   {WorkbookID: book.ID, Mode: ModeFormula},
		"status": {WorkbookID: book.ID, Status: StatusApplied},
		"query":  {WorkbookID: book.ID, Query: "요청 clean"},
		"since":  {WorkbookID: book.ID, Since: time.Now().UTC().AddDate(0, 0, -1)},
	} {
		filtered, err := service.History(ctx, filter)
		if err != nil {
			t.Fatalf("%s filter: %v", name, err)
		}
		switch name {
		case "actor", "mode", "since":
			if filtered.Summary.Total != 2 {
				t.Errorf("%s filter total=%d, want 2", name, filtered.Summary.Total)
			}
		default:
			if filtered.Summary.Total != 1 {
				t.Errorf("%s filter total=%d, want 1", name, filtered.Summary.Total)
			}
		}
	}

	// Paging reports where to continue.
	firstPage, err := service.History(ctx, HistoryFilter{WorkbookID: book.ID, Limit: 2})
	if err != nil || firstPage.NextOffset == nil || *firstPage.NextOffset != 2 || len(firstPage.Items) != 2 {
		t.Fatalf("paging=%#v, %v", firstPage.NextOffset, err)
	}

	// Purging keeps the request that is still waiting for a decision.
	// Purging is global, so the count is only asserted to include this row; the
	// workbook scoped view below is what proves the rule.
	removed, err := service.PurgeHistory(ctx, time.Now().UTC().AddDate(0, 0, -30), "admin@corp.example")
	if err != nil || removed < 1 {
		t.Fatalf("purge removed=%d, %v", removed, err)
	}
	after, err := service.History(ctx, HistoryFilter{WorkbookID: book.ID})
	if err != nil || after.Summary.Total != 3 || after.Summary.ByStatus[StatusPlanned] != 1 {
		t.Fatalf("after purge=%#v, %v", after.Summary, err)
	}

	if days := service.RetentionDays(ctx); days != 30 {
		t.Fatalf("retention days=%d", days)
	}
	// The scheduled pass uses the same rule as the manual purge.
	service.applyRetention(ctx)
	swept, err := service.History(ctx, HistoryFilter{WorkbookID: book.ID})
	if err != nil || swept.Summary.Total != 3 {
		t.Fatalf("retention sweep=%#v, %v", swept.Summary, err)
	}
}
