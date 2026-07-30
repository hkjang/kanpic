package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"kanpic/internal/collaboration"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func TestWebSocketPresenceOperationConflictAndResync(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	server := httptest.NewServer(New(repository, slog.New(slog.NewTextHandler(io.Discard, nil))))
	defer server.Close()

	book := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "live"}, http.StatusCreated)
	other := request[workbook.Workbook](t, server, http.MethodPost, "/api/v1/workbooks", map[string]string{"title": "other"}, http.StatusCreated)
	alice := dialCollaboration(t, server.URL, book.ID, "alice-client", "alice")
	defer alice.CloseNow()
	aliceSnapshot := readEvent(t, alice, "presence.snapshot")
	if aliceSnapshot.ServerVersion != 1 || presenceCount(t, aliceSnapshot) != 1 {
		t.Fatalf("alice snapshot: %#v", aliceSnapshot)
	}

	bob := dialCollaboration(t, server.URL, book.ID, "bob-client", "bob")
	bobSnapshot := readEvent(t, bob, "presence.snapshot")
	if presenceCount(t, bobSnapshot) != 2 {
		t.Fatalf("bob snapshot: %#v", bobSnapshot)
	}
	joined := readEvent(t, alice, "presence.join")
	if joined.ActorID != "bob" || joined.ClientID != "bob-client" {
		t.Fatalf("join event: %#v", joined)
	}

	writeEvent(t, alice, map[string]any{"id": "cursor-1", "type": "cursor.update", "data": map[string]any{"sheet_id": book.Sheets[0].ID, "row": 4, "column": 2}})
	cursor := readEvent(t, bob, "cursor.update")
	if cursor.ActorID != "alice" || cursor.ClientID != "alice-client" {
		t.Fatalf("cursor event: %#v", cursor)
	}

	writeEvent(t, alice, map[string]any{"id": "operation-1", "type": "operation.submit", "data": map[string]any{
		"sheet_id": book.Sheets[0].ID, "base_version": 1, "idempotency_key": "alice-write",
		"cells": []map[string]any{{"row": 1, "column": 1, "value": 7}},
	}})
	accepted := readEvent(t, alice, "operation.accepted")
	if accepted.ServerVersion != 2 {
		t.Fatalf("accepted event: %#v", accepted)
	}
	broadcast := readEvent(t, bob, "operation.broadcast")
	if broadcast.ServerVersion != 2 || broadcast.ActorID != "alice" {
		t.Fatalf("broadcast event: %#v", broadcast)
	}

	writeEvent(t, bob, map[string]any{"id": "operation-2", "type": "operation.submit", "data": map[string]any{
		"sheet_id": book.Sheets[0].ID, "base_version": 1, "idempotency_key": "bob-stale-write",
		"cells": []map[string]any{{"row": 1, "column": 1, "value": 9}},
	}})
	accepted = readEvent(t, bob, "operation.accepted")
	if accepted.ServerVersion != 3 {
		t.Fatalf("stale write result: %#v", accepted)
	}
	conflict := readEvent(t, bob, "operation.conflict")
	var conflicts []workbook.CellConflict
	decodeEventData(t, conflict, &conflicts)
	if len(conflicts) != 1 || conflicts[0].Row != 1 || conflicts[0].Column != 1 {
		t.Fatalf("conflict event: %#v", conflict)
	}

	writeEvent(t, bob, map[string]any{"id": "wrong-sheet", "type": "operation.submit", "data": map[string]any{
		"sheet_id": other.Sheets[0].ID, "base_version": 1, "idempotency_key": "cross-workbook",
		"cells": []map[string]any{{"row": 1, "column": 1, "value": "blocked"}},
	}})
	rejected := readEvent(t, bob, "server.error")
	var rejection map[string]string
	decodeEventData(t, rejected, &rejection)
	if rejection["code"] != "invalid_sheet" {
		t.Fatalf("cross-workbook rejection: %#v", rejection)
	}

	if err := bob.Close(websocket.StatusNormalClosure, "reconnect"); err != nil {
		t.Fatalf("close bob: %v", err)
	}
	readEvent(t, alice, "presence.leave")
	bob = dialCollaboration(t, server.URL, book.ID, "bob-reconnected", "bob")
	defer bob.CloseNow()
	reconnected := readEvent(t, bob, "presence.snapshot")
	if reconnected.ServerVersion != 3 || presenceCount(t, reconnected) != 2 {
		t.Fatalf("reconnected snapshot: %#v", reconnected)
	}

	cells, err := repository.ReadRange(context.Background(), other.Sheets[0].ID, mustRange(t, "A1"))
	if err != nil || len(cells) != 0 {
		t.Fatalf("other workbook was modified: cells=%#v err=%v", cells, err)
	}
}

func TestWebSocketReadScopeCannotSubmitOperation(t *testing.T) {
	t.Parallel()
	repository := workbook.NewMemoryRepository()
	book, err := repository.CreateWorkbook(context.Background(), workbook.CreateWorkbookInput{Title: "read only", OwnerID: "owner"})
	if err != nil {
		t.Fatalf("create workbook: %v", err)
	}
	hub := collaboration.New(repository, slog.New(slog.NewTextHandler(io.Discard, nil)))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hub.ServeHTTP(w, r, book.ID, "reader", false)
	}))
	defer server.Close()

	connection := dialCollaboration(t, server.URL, book.ID, "reader-client", "reader")
	defer connection.CloseNow()
	readEvent(t, connection, "presence.snapshot")
	writeEvent(t, connection, map[string]any{"id": "forbidden", "type": "operation.submit", "data": map[string]any{
		"sheet_id": book.Sheets[0].ID, "base_version": 1, "idempotency_key": "forbidden-write",
		"cells": []map[string]any{{"row": 1, "column": 1, "value": 1}},
	}})
	rejected := readEvent(t, connection, "server.error")
	var data map[string]string
	decodeEventData(t, rejected, &data)
	if data["code"] != "insufficient_scope" {
		t.Fatalf("read-only rejection: %#v", data)
	}
	cells, err := repository.ReadRange(context.Background(), book.Sheets[0].ID, mustRange(t, "A1"))
	if err != nil || len(cells) != 0 {
		t.Fatalf("read-only client wrote cells: cells=%#v err=%v", cells, err)
	}
}

func dialCollaboration(t *testing.T, baseURL, workbookID, clientID, actor string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url := "ws" + strings.TrimPrefix(baseURL, "http") + "/ws/v1/workbooks/" + workbookID + "?client_id=" + clientID
	connection, response, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPHeader: http.Header{"X-Kanpic-Actor": []string{actor}}})
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial collaboration (status %d): %v", status, err)
	}
	return connection
}

func writeEvent(t *testing.T, connection *websocket.Conn, event any) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := wsjson.Write(ctx, connection, event); err != nil {
		t.Fatalf("write websocket event: %v", err)
	}
}

func readEvent(t *testing.T, connection *websocket.Conn, eventType string) collaboration.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		var event collaboration.Event
		if err := wsjson.Read(ctx, connection, &event); err != nil {
			t.Fatalf("read %s event: %v", eventType, err)
		}
		if event.Type == eventType {
			return event
		}
	}
}

func decodeEventData(t *testing.T, event collaboration.Event, target any) {
	t.Helper()
	encoded, err := json.Marshal(event.Data)
	if err != nil || json.Unmarshal(encoded, target) != nil {
		t.Fatalf("decode event %s data: %#v", event.Type, event.Data)
	}
}

func presenceCount(t *testing.T, event collaboration.Event) int {
	t.Helper()
	var data struct {
		Users []collaboration.Presence `json:"users"`
	}
	decodeEventData(t, event, &data)
	return len(data.Users)
}

func mustRange(t *testing.T, value string) cellrange.Range {
	t.Helper()
	selected, err := cellrange.Parse(value)
	if err != nil {
		t.Fatalf("parse range: %v", err)
	}
	return selected
}
