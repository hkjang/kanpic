package collaboration

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	"kanpic/internal/workbook"
)

func TestWebSocketOperationNotifiesMutationListenerOnce(t *testing.T) {
	ctx := context.Background()
	repository := workbook.NewMemoryRepository()
	book, err := repository.CreateWorkbook(ctx, workbook.CreateWorkbookInput{Title: "listener", OwnerID: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	hub := New(repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	var calls int
	var observed workbook.MutationResult
	hub.SetMutationListener(func(_ context.Context, result workbook.MutationResult, cells []workbook.CellInput, actor, clientID string) {
		calls++
		observed = result
		if actor != "alice" || clientID != "browser" || len(cells) != 1 || cells[0].Row != 1 {
			t.Fatalf("listener payload actor=%q client=%q cells=%#v", actor, clientID, cells)
		}
	})
	peer := &client{actorID: "alice", clientID: "browser", workbookID: book.ID, send: make(chan Event, 4), canWrite: true}
	payload, _ := json.Marshal(operationSubmit{SheetID: book.Sheets[0].ID, BaseVersion: 1, IdempotencyKey: "ws-operation", Cells: []workbook.CellInput{{Row: 1, Column: 1, Value: json.RawMessage(`7`)}}})
	hub.handle(ctx, peer, inboundEvent{ID: "event-1", Type: "operation.submit", Data: payload})
	if calls != 1 || observed.OperationID == "" || observed.ServerVersion != 2 {
		t.Fatalf("listener calls=%d result=%#v", calls, observed)
	}
	hub.handle(ctx, peer, inboundEvent{ID: "event-2", Type: "operation.submit", Data: payload})
	if calls != 1 {
		t.Fatalf("duplicate operation notified listener %d times", calls)
	}
}
