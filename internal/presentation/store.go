package presentation

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ErrRecordNotFound is returned when kanpic holds no record of a deck. It is
// deliberately not the same as "the provider does not have it": kanpic refuses
// to act on a deck it did not make, whatever the provider thinks.
var ErrRecordNotFound = errors.New("presentation record not found")

// Record is kanpic's own note of a deck it made: which range it came from and
// which workbook governs who may see it.
type Record struct {
	ID            string    `json:"id"`
	Provider      string    `json:"provider"`
	WorkbookID    string    `json:"workbook_id"`
	SheetID       string    `json:"sheet_id"`
	Range         string    `json:"range"`
	SourceVersion int64     `json:"source_version"`
	Title         string    `json:"title"`
	Template      string    `json:"template,omitempty"`
	SlideCount    int       `json:"slide_count"`
	EditURL       string    `json:"edit_url,omitempty"`
	CreatedBy     string    `json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
	// Stale says the workbook has moved on since the deck was made. It is
	// computed on read rather than stored, because the answer changes without
	// anybody touching the deck.
	Stale bool `json:"stale"`
}

type Store interface {
	Save(ctx context.Context, record Record) error
	Get(ctx context.Context, id string) (Record, error)
	ListForWorkbook(ctx context.Context, workbookID string) ([]Record, error)
}

type MemoryStore struct {
	mutex   sync.RWMutex
	records map[string]Record
}

func NewMemoryStore() *MemoryStore { return &MemoryStore{records: map[string]Record{}} }

func (s *MemoryStore) Save(_ context.Context, record Record) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	s.records[record.ID] = record
	return nil
}

func (s *MemoryStore) Get(_ context.Context, id string) (Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	record, found := s.records[id]
	if !found {
		return Record{}, ErrRecordNotFound
	}
	return record, nil
}

func (s *MemoryStore) ListForWorkbook(_ context.Context, workbookID string) ([]Record, error) {
	s.mutex.RLock()
	defer s.mutex.RUnlock()
	items := []Record{}
	for _, record := range s.records {
		if record.WorkbookID == workbookID {
			items = append(items, record)
		}
	}
	sort.Slice(items, func(first, second int) bool { return items[first].UpdatedAt.After(items[second].UpdatedAt) })
	return items, nil
}

type PostgresStore struct{ pool *pgxpool.Pool }

func NewPostgresStore(pool *pgxpool.Pool) *PostgresStore { return &PostgresStore{pool: pool} }

const recordColumns = `id,provider,workbook_id::text,sheet_id::text,cell_range,source_version,title,template,slide_count,edit_url,created_by,created_at,updated_at`

func (s *PostgresStore) Save(ctx context.Context, record Record) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO presentations(id,provider,workbook_id,sheet_id,cell_range,source_version,title,template,slide_count,edit_url,created_by,created_at,updated_at)
		VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$12)
		ON CONFLICT (id) DO UPDATE SET
			cell_range=EXCLUDED.cell_range, source_version=EXCLUDED.source_version, title=EXCLUDED.title,
			template=EXCLUDED.template, slide_count=EXCLUDED.slide_count, edit_url=EXCLUDED.edit_url,
			updated_at=EXCLUDED.updated_at`,
		record.ID, record.Provider, record.WorkbookID, record.SheetID, record.Range, record.SourceVersion,
		record.Title, record.Template, record.SlideCount, record.EditURL, record.CreatedBy, record.CreatedAt)
	return err
}

func (s *PostgresStore) Get(ctx context.Context, id string) (Record, error) {
	record, err := scanRecord(s.pool.QueryRow(ctx, `SELECT `+recordColumns+` FROM presentations WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Record{}, ErrRecordNotFound
	}
	return record, err
}

func (s *PostgresStore) ListForWorkbook(ctx context.Context, workbookID string) ([]Record, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+recordColumns+` FROM presentations WHERE workbook_id=$1 ORDER BY updated_at DESC`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Record{}
	for rows.Next() {
		record, scanErr := scanRecord(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, record)
	}
	return items, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanRecord(row rowScanner) (Record, error) {
	var record Record
	err := row.Scan(&record.ID, &record.Provider, &record.WorkbookID, &record.SheetID, &record.Range,
		&record.SourceVersion, &record.Title, &record.Template, &record.SlideCount, &record.EditURL,
		&record.CreatedBy, &record.CreatedAt, &record.UpdatedAt)
	return record, err
}
