package observability

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

type LogEntry struct {
	ID         int64          `json:"id"`
	LoggedAt   time.Time      `json:"logged_at"`
	Level      string         `json:"level"`
	Message    string         `json:"message"`
	Attributes map[string]any `json:"attributes"`
	TraceID    string         `json:"trace_id,omitempty"`
}

type Store struct{ pool *pgxpool.Pool }

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

func (s *Store) List(ctx context.Context, level, query string, limit int) ([]LogEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `SELECT id,logged_at,level,message,attributes,trace_id FROM system_logs WHERE ($1='' OR level=$1) AND ($2='' OR message ILIKE '%' || $2 || '%' OR attributes::text ILIKE '%' || $2 || '%') ORDER BY logged_at DESC LIMIT $3`, strings.ToUpper(level), query, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]LogEntry, 0)
	for rows.Next() {
		var item LogEntry
		var data []byte
		if err := rows.Scan(&item.ID, &item.LoggedAt, &item.Level, &item.Message, &data, &item.TraceID); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(data, &item.Attributes)
		result = append(result, item)
	}
	return result, rows.Err()
}

func (s *Store) PurgeBefore(ctx context.Context, before time.Time) (int64, error) {
	command, err := s.pool.Exec(ctx, `DELETE FROM system_logs WHERE logged_at<$1`, before)
	return command.RowsAffected(), err
}

type persistedRecord struct {
	time       time.Time
	level      string
	message    string
	attributes map[string]any
	traceID    string
}

type persistentHandler struct {
	console slog.Handler
	pool    *pgxpool.Pool
	queue   chan persistedRecord
	done    chan struct{}
	once    *sync.Once
}

func NewLogger(pool *pgxpool.Pool, output io.Writer) (*slog.Logger, func()) {
	handler := &persistentHandler{console: slog.NewJSONHandler(output, nil), pool: pool, queue: make(chan persistedRecord, 2048), done: make(chan struct{}), once: &sync.Once{}}
	go handler.run()
	closeFn := func() { handler.once.Do(func() { close(handler.queue); <-handler.done }) }
	return slog.New(handler), closeFn
}

func (h *persistentHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.console.Enabled(ctx, level)
}

func (h *persistentHandler) Handle(ctx context.Context, record slog.Record) error {
	if err := h.console.Handle(ctx, record); err != nil {
		return err
	}
	attributes := make(map[string]any)
	record.Attrs(func(attr slog.Attr) bool { attributes[attr.Key] = attr.Value.Any(); return true })
	traceID, _ := attributes["trace_id"].(string)
	item := persistedRecord{time: record.Time.UTC(), level: record.Level.String(), message: record.Message, attributes: attributes, traceID: traceID}
	select {
	case h.queue <- item:
	default:
	}
	return nil
}

func (h *persistentHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &persistentHandler{console: h.console.WithAttrs(attrs), pool: h.pool, queue: h.queue, done: h.done, once: h.once}
}
func (h *persistentHandler) WithGroup(name string) slog.Handler {
	return &persistentHandler{console: h.console.WithGroup(name), pool: h.pool, queue: h.queue, done: h.done, once: h.once}
}

func (h *persistentHandler) run() {
	defer close(h.done)
	for record := range h.queue {
		data, _ := json.Marshal(record.attributes)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		_, _ = h.pool.Exec(ctx, `INSERT INTO system_logs(logged_at,level,message,attributes,trace_id) VALUES($1,$2,$3,$4,$5)`, record.time, record.level, record.message, data, record.traceID)
		cancel()
	}
}
