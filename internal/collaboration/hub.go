package collaboration

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"kanpic/internal/workbook"
	"kanpic/pkg/identity"
)

const (
	maxMessageBytes = 2 << 20
	maxRow          = 1_048_576
	maxColumn       = 16_384
)

type Event struct {
	ID            string `json:"id"`
	Type          string `json:"type"`
	WorkbookID    string `json:"workbook_id"`
	ActorID       string `json:"actor_id,omitempty"`
	ClientID      string `json:"client_id,omitempty"`
	ServerVersion int64  `json:"server_version,omitempty"`
	Data          any    `json:"data,omitempty"`
}

type inboundEvent struct {
	ID   string          `json:"id"`
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

type Cursor struct {
	SheetID string `json:"sheet_id"`
	Row     int    `json:"row"`
	Column  int    `json:"column"`
}

type Coordinate struct {
	Row    int `json:"row"`
	Column int `json:"column"`
}

type Selection struct {
	SheetID string     `json:"sheet_id"`
	Start   Coordinate `json:"start"`
	End     Coordinate `json:"end"`
}

type Presence struct {
	ActorID   string     `json:"actor_id"`
	ClientID  string     `json:"client_id"`
	Cursor    *Cursor    `json:"cursor,omitempty"`
	Selection *Selection `json:"selection,omitempty"`
}

type operationSubmit struct {
	SheetID        string               `json:"sheet_id"`
	BaseVersion    int64                `json:"base_version"`
	IdempotencyKey string               `json:"idempotency_key"`
	Cells          []workbook.CellInput `json:"cells"`
}

type operationBroadcast struct {
	SheetID string                  `json:"sheet_id"`
	Cells   []workbook.CellInput    `json:"cells"`
	Result  workbook.MutationResult `json:"result"`
}

type client struct {
	actorID    string
	clientID   string
	workbookID string
	connection *websocket.Conn
	send       chan Event
	cancel     context.CancelFunc
	presence   Presence
	canWrite   bool
}

type Hub struct {
	repository workbook.Repository
	logger     *slog.Logger
	mu         sync.RWMutex
	rooms      map[string]map[*client]struct{}
	onMutation func(context.Context, workbook.MutationResult, []workbook.CellInput, string, string)
}

func New(repository workbook.Repository, logger *slog.Logger) *Hub {
	if logger == nil {
		logger = slog.Default()
	}
	return &Hub{repository: repository, logger: logger, rooms: make(map[string]map[*client]struct{})}
}

func (h *Hub) SetMutationListener(listener func(context.Context, workbook.MutationResult, []workbook.CellInput, string, string)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.onMutation = listener
}

func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request, workbookID, actorID string, canWrite bool) {
	book, err := h.repository.GetWorkbook(r.Context(), workbookID)
	if err != nil {
		h.writeHTTPError(w, err)
		return
	}
	clientID := strings.TrimSpace(r.URL.Query().Get("client_id"))
	if clientID == "" || len(clientID) > 128 {
		clientID = identity.New()
	}
	connection, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  []string{"localhost:5173", "127.0.0.1:5173"},
		CompressionMode: websocket.CompressionNoContextTakeover,
	})
	if err != nil {
		h.logger.Warn("websocket accept failed", "workbook_id", workbookID, "error", err)
		return
	}
	connection.SetReadLimit(maxMessageBytes)
	ctx, cancel := context.WithCancel(context.Background())
	peer := &client{actorID: actorID, clientID: clientID, workbookID: workbookID, connection: connection, send: make(chan Event, 64), cancel: cancel, presence: Presence{ActorID: actorID, ClientID: clientID}, canWrite: canWrite}
	defer func() {
		cancel()
		h.unregister(peer)
		connection.CloseNow()
	}()

	go h.writeLoop(ctx, peer)
	users := h.register(peer)
	h.enqueue(peer, Event{ID: identity.New(), Type: "presence.snapshot", WorkbookID: workbookID, ClientID: clientID, ServerVersion: book.Version, Data: map[string]any{"users": users}})
	h.broadcast(workbookID, Event{ID: identity.New(), Type: "presence.join", WorkbookID: workbookID, ActorID: actorID, ClientID: clientID, Data: peer.presence}, clientID)

	for {
		var incoming inboundEvent
		if err := wsjson.Read(ctx, connection, &incoming); err != nil {
			if websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
				h.logger.Debug("websocket read ended", "workbook_id", workbookID, "client_id", clientID, "error", err)
			}
			return
		}
		h.handle(ctx, peer, incoming)
	}
}

func (h *Hub) PublishOperation(workbookID, sheetID, actorID, clientID string, cells []workbook.CellInput, result workbook.MutationResult) {
	payload := operationBroadcast{SheetID: sheetID, Cells: cells, Result: result}
	h.broadcast(workbookID, Event{ID: identity.New(), Type: "operation.broadcast", WorkbookID: workbookID, ActorID: actorID, ClientID: clientID, ServerVersion: result.ServerVersion, Data: payload}, clientID)
	h.PublishVersion(workbookID, actorID, clientID, result.OperationID, result.ServerVersion)
}

func (h *Hub) PublishVersion(workbookID, actorID, clientID, operationID string, serverVersion int64) {
	h.broadcast(workbookID, Event{ID: identity.New(), Type: "workbook.version", WorkbookID: workbookID, ActorID: actorID, ClientID: clientID, ServerVersion: serverVersion, Data: map[string]any{"operation_id": operationID}}, "")
}

func (h *Hub) PublishStructure(result workbook.MutationResult, actorID, clientID string) {
	h.broadcast(result.WorkbookID, Event{ID: identity.New(), Type: "workbook.version", WorkbookID: result.WorkbookID, ActorID: actorID, ClientID: clientID, ServerVersion: result.ServerVersion, Data: map[string]any{
		"operation_id": result.OperationID,
		"structural":   true,
		"sheet_id":     result.SheetID,
		"axis":         result.StructuralAxis,
		"action":       result.StructuralAction,
		"index":        result.StructuralIndex,
		"count":        result.StructuralCount,
	}}, "")
}

func (h *Hub) PublishComment(workbookID, actorID string, data any) {
	h.broadcast(workbookID, Event{ID: identity.New(), Type: "comment.changed", WorkbookID: workbookID, ActorID: actorID, Data: data}, "")
}

func (h *Hub) handle(ctx context.Context, peer *client, incoming inboundEvent) {
	switch incoming.Type {
	case "cursor.update":
		var cursor Cursor
		if json.Unmarshal(incoming.Data, &cursor) != nil || !validCursor(cursor) {
			h.sendError(peer, incoming.ID, "invalid_cursor", "cursor row and column are outside the supported sheet bounds")
			return
		}
		h.updateCursor(peer, &cursor, nil)
		h.broadcast(peer.workbookID, Event{ID: incoming.ID, Type: "cursor.update", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, Data: cursor}, peer.clientID)
	case "selection.update":
		var selection Selection
		if json.Unmarshal(incoming.Data, &selection) != nil || strings.TrimSpace(selection.SheetID) == "" || !validCoordinate(selection.Start) || !validCoordinate(selection.End) {
			h.sendError(peer, incoming.ID, "invalid_selection", "selection is outside the supported sheet bounds")
			return
		}
		h.updateCursor(peer, nil, &selection)
		h.broadcast(peer.workbookID, Event{ID: incoming.ID, Type: "selection.update", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, Data: selection}, peer.clientID)
	case "operation.submit":
		if !peer.canWrite {
			h.sendError(peer, incoming.ID, "insufficient_scope", "range.write scope is required")
			return
		}
		var operation operationSubmit
		if err := json.Unmarshal(incoming.Data, &operation); err != nil {
			h.sendError(peer, incoming.ID, "invalid_operation", "operation payload is invalid")
			return
		}
		if len(operation.Cells) == 0 || len(operation.Cells) > workbook.MaxBatchCells {
			h.sendError(peer, incoming.ID, "invalid_operation", "operation must contain 1 to 1000 cells")
			return
		}
		book, err := h.repository.GetWorkbook(ctx, peer.workbookID)
		if err != nil {
			h.sendError(peer, incoming.ID, "operation_rejected", err.Error())
			return
		}
		belongs := false
		for _, sheet := range book.Sheets {
			if sheet.ID == operation.SheetID {
				belongs = true
				break
			}
		}
		if !belongs {
			h.sendError(peer, incoming.ID, "invalid_sheet", "sheet does not belong to the connected workbook")
			return
		}
		result, err := h.repository.ApplyCells(ctx, workbook.CellMutation{SheetID: operation.SheetID, ActorID: peer.actorID, ClientID: peer.clientID, BaseVersion: operation.BaseVersion, IdempotencyKey: operation.IdempotencyKey, Cells: operation.Cells})
		if err != nil {
			if errors.Is(err, workbook.ErrVersionAhead) {
				h.enqueue(peer, Event{ID: incoming.ID, Type: "server.resync", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, ServerVersion: book.Version, Data: map[string]any{"reason": "base_version_ahead"}})
				return
			}
			h.sendError(peer, incoming.ID, "operation_rejected", err.Error())
			return
		}
		h.enqueue(peer, Event{ID: incoming.ID, Type: "operation.accepted", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, ServerVersion: result.ServerVersion, Data: result})
		if len(result.Conflicts) > 0 {
			h.enqueue(peer, Event{ID: identity.New(), Type: "operation.conflict", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, ServerVersion: result.ServerVersion, Data: result.Conflicts})
		}
		if !result.Duplicate {
			h.PublishOperation(peer.workbookID, operation.SheetID, peer.actorID, peer.clientID, operation.Cells, result)
			h.mu.RLock()
			listener := h.onMutation
			h.mu.RUnlock()
			if listener != nil {
				listener(ctx, result, operation.Cells, peer.actorID, peer.clientID)
			}
		}
	default:
		h.sendError(peer, incoming.ID, "unknown_event", "event type is not supported")
	}
}

func (h *Hub) writeLoop(ctx context.Context, peer *client) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case message := <-peer.send:
			writeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err := wsjson.Write(writeCtx, peer.connection, message)
			cancel()
			if err != nil {
				peer.cancel()
				return
			}
		case <-ticker.C:
			pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := peer.connection.Ping(pingCtx)
			cancel()
			if err != nil {
				peer.cancel()
				return
			}
		}
	}
}

func (h *Hub) register(peer *client) []Presence {
	h.mu.Lock()
	defer h.mu.Unlock()
	room := h.rooms[peer.workbookID]
	if room == nil {
		room = make(map[*client]struct{})
		h.rooms[peer.workbookID] = room
	}
	room[peer] = struct{}{}
	users := make([]Presence, 0, len(room))
	for candidate := range room {
		users = append(users, candidate.presence)
	}
	return users
}

func (h *Hub) unregister(peer *client) {
	h.mu.Lock()
	room := h.rooms[peer.workbookID]
	if _, exists := room[peer]; !exists {
		h.mu.Unlock()
		return
	}
	delete(room, peer)
	if len(room) == 0 {
		delete(h.rooms, peer.workbookID)
	}
	h.mu.Unlock()
	h.broadcast(peer.workbookID, Event{ID: identity.New(), Type: "presence.leave", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, Data: peer.presence}, peer.clientID)
}

func (h *Hub) updateCursor(peer *client, cursor *Cursor, selection *Selection) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if cursor != nil {
		copy := *cursor
		peer.presence.Cursor = &copy
	}
	if selection != nil {
		copy := *selection
		peer.presence.Selection = &copy
	}
}

func (h *Hub) broadcast(workbookID string, event Event, excludeClientID string) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for peer := range h.rooms[workbookID] {
		if excludeClientID != "" && peer.clientID == excludeClientID {
			continue
		}
		h.enqueue(peer, event)
	}
}

func (h *Hub) enqueue(peer *client, event Event) {
	select {
	case peer.send <- event:
	default:
		peer.cancel()
	}
}

func (h *Hub) sendError(peer *client, eventID, code, message string) {
	h.enqueue(peer, Event{ID: eventID, Type: "server.error", WorkbookID: peer.workbookID, ActorID: peer.actorID, ClientID: peer.clientID, Data: map[string]string{"code": code, "message": message}})
}

func (h *Hub) writeHTTPError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	if errors.Is(err, workbook.ErrNotFound) {
		status = http.StatusNotFound
	}
	http.Error(w, http.StatusText(status), status)
}

func validCursor(cursor Cursor) bool {
	return strings.TrimSpace(cursor.SheetID) != "" && validCoordinate(Coordinate{Row: cursor.Row, Column: cursor.Column})
}

func validCoordinate(coordinate Coordinate) bool {
	return coordinate.Row >= 1 && coordinate.Row <= maxRow && coordinate.Column >= 1 && coordinate.Column <= maxColumn
}
