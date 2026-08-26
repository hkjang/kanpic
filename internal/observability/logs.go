package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
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

// logFilter 는 화면과 내보내기가 함께 쓰는 거르개다. 둘이 따로 거르면
// 감사에 넘긴 파일과 화면에서 본 것이 달라진다.
type logFilter struct {
	level string
	query string
	from  time.Time
	to    time.Time
}

func (f logFilter) where() (string, []any) {
	condition := `($1='' OR level=$1) AND ($2='' OR message ILIKE '%' || $2 || '%')`
	arguments := []any{f.level, f.query}
	if !f.from.IsZero() {
		arguments = append(arguments, f.from)
		condition += fmt.Sprintf(" AND logged_at>=$%d", len(arguments))
	}
	if !f.to.IsZero() {
		arguments = append(arguments, f.to)
		condition += fmt.Sprintf(" AND logged_at<=$%d", len(arguments))
	}
	return condition, arguments
}

func (s *Store) List(ctx context.Context, level, query string, limit int) ([]LogEntry, error) {
	return s.ListRange(ctx, level, query, time.Time{}, time.Time{}, limit)
}

// ListRange 는 기간까지 좁혀 낸다. 감사에서는 "그 기간 기록" 을 달라고 하지
// "최근 100건" 을 달라고 하지 않는다.
func (s *Store) ListRange(ctx context.Context, level, query string, from, to time.Time, limit int) ([]LogEntry, error) {
	if limit < 1 || limit > 500 {
		limit = 100
	}
	condition, arguments := logFilter{level: level, query: query, from: from, to: to}.where()
	arguments = append(arguments, limit)
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT id,logged_at,level,message,attributes,trace_id FROM system_logs WHERE %s ORDER BY logged_at DESC, id DESC LIMIT $%d`,
		condition, len(arguments)), arguments...)
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

// Stream 은 거른 기록을 하나씩 넘긴다. 내보내기는 개수를 자르지 않는다 —
// 감사에 넘길 파일이 조용히 잘려 있으면 없느니만 못하다. 한 번에 모두
// 메모리에 담지 않으려고 한 줄씩 흘린다.
func (s *Store) Stream(ctx context.Context, level, query string, from, to time.Time, visit func(LogEntry) error) error {
	condition, arguments := logFilter{level: level, query: query, from: from, to: to}.where()
	rows, err := s.pool.Query(ctx, fmt.Sprintf(
		`SELECT id,logged_at,level,message,attributes,trace_id FROM system_logs WHERE %s ORDER BY logged_at DESC, id DESC`,
		condition), arguments...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var item LogEntry
		var data []byte
		if err := rows.Scan(&item.ID, &item.LoggedAt, &item.Level, &item.Message, &data, &item.TraceID); err != nil {
			return err
		}
		_ = json.Unmarshal(data, &item.Attributes)
		if err := visit(item); err != nil {
			return err
		}
	}
	return rows.Err()
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
