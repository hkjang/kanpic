//go:build integration

package integration

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"kanpic/internal/database"
	"kanpic/internal/handoff"
	"kanpic/internal/workbook"
)

// 표는 PostgreSQL 에서도 한 번만 꺼내지고, 출처는 워크북에 남는다. 메모리
// 저장소로는 SQL 의 오타를 잡지 못하므로 실제 데이터베이스에 대고 본다.
func TestPostgresHandoffStore(t *testing.T) {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		t.Skip("POSTGRES_DSN is not configured")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := handoff.NewPostgresStore(pool)
	service := handoff.NewService(nil, store)

	claim, _, err := service.Issue(ctx, handoff.Ticket{Resource: "wb", Format: "csv", Filename: "실적.csv", ContentType: "text/csv; charset=utf-8", Data: []byte("a,b\n"), IssuedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	taken, err := service.Redeem(ctx, claim)
	if err != nil || string(taken.Data) != "a,b\n" || taken.Filename != "실적.csv" || taken.IssuedBy != "alice" {
		t.Fatalf("taken=%+v err=%v", taken, err)
	}
	if _, err := service.Redeem(ctx, claim); !errors.Is(err, handoff.ErrNotFound) {
		t.Fatalf("second use err=%v", err)
	}
	// 시간이 지난 표는 꺼낼 수 없고, 다음 표를 둘 때 치워진다.
	expired := handoff.NewService(nil, store).WithClock(func() time.Time { return time.Now().Add(-2 * handoff.ClaimTTL) })
	stale, _, err := expired.Issue(ctx, handoff.Ticket{Resource: "wb", Format: "csv"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Redeem(ctx, stale); !errors.Is(err, handoff.ErrNotFound) {
		t.Fatalf("expired err=%v", err)
	}

	repository := workbook.NewPostgresRepository(pool)
	wb, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "handoff receipt", WorkspaceID: "integration", OwnerID: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	defer repository.DeleteWorkbook(context.Background(), wb.ID, "integration-cleanup")
	if _, err := service.Origin(ctx, wb.ID); !errors.Is(err, handoff.ErrNotFound) {
		t.Fatalf("origin before receipt err=%v", err)
	}
	if err := service.Record(ctx, handoff.Receipt{WorkbookID: wb.ID, Source: "https://umm.intra", Filename: "개편안.csv", ReceivedBy: "bob"}); err != nil {
		t.Fatal(err)
	}
	receipt, err := service.Origin(ctx, wb.ID)
	if err != nil || receipt.Source != "https://umm.intra" || receipt.ReceivedBy != "bob" || receipt.ReceivedAt.IsZero() {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
}
