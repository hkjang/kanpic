package workbook

import (
	"context"
	"os"
	"testing"

	"kanpic/internal/database"
)

// Listing runs different SQL for administrators, and that branch cannot be
// exercised by the in-memory repository. The test therefore runs against a real
// PostgreSQL when KANPIC_TEST_DSN is set and skips otherwise.
func postgresTestRepository(t *testing.T) (*PostgresRepository, context.Context) {
	t.Helper()
	dsn := os.Getenv("KANPIC_TEST_DSN")
	if dsn == "" {
		t.Skip("KANPIC_TEST_DSN is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(pool.Close)
	return NewPostgresRepository(pool), ctx
}

func TestPostgresListingWorksForAdministrators(t *testing.T) {
	repository, ctx := postgresTestRepository(t)
	owner := "listing.owner@corp.example"
	created, err := repository.CreateWorkbook(ctx, CreateWorkbookInput{Title: "관리자 목록", WorkspaceID: "default", OwnerID: owner})
	if err != nil {
		t.Fatalf("create workbook: %v", err)
	}
	t.Cleanup(func() { _ = repository.PurgeWorkbook(ctx, created.ID) })

	admin := AccessPrincipal{UserID: "admin@corp.example", Admin: true}
	for _, workspace := range []string{"", "default"} {
		items, err := repository.ListWorkbooks(ctx, workspace, admin)
		if err != nil {
			t.Fatalf("admin list workspace=%q: %v", workspace, err)
		}
		if !containsWorkbook(items, created.ID) {
			t.Fatalf("admin list workspace=%q did not include the new workbook", workspace)
		}
	}

	// A non-administrator still only sees what they may open.
	stranger := AccessPrincipal{UserID: "stranger@corp.example"}
	items, err := repository.ListWorkbooks(ctx, "default", stranger)
	if err != nil {
		t.Fatalf("stranger list: %v", err)
	}
	if containsWorkbook(items, created.ID) {
		t.Fatal("a stranger should not see a restricted workbook")
	}

	if err := repository.DeleteWorkbook(ctx, created.ID, owner); err != nil {
		t.Fatalf("delete workbook: %v", err)
	}
	for _, workspace := range []string{"", "default"} {
		trashed, err := repository.ListDeletedWorkbooks(ctx, workspace, admin)
		if err != nil {
			t.Fatalf("admin trash workspace=%q: %v", workspace, err)
		}
		if !containsWorkbook(trashed, created.ID) {
			t.Fatalf("admin trash workspace=%q did not include the deleted workbook", workspace)
		}
	}
	if trashed, err := repository.ListDeletedWorkbooks(ctx, "default", stranger); err != nil || containsWorkbook(trashed, created.ID) {
		t.Fatalf("stranger trash = %d items, %v", len(trashed), err)
	}
}

func containsWorkbook(items []Workbook, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}
