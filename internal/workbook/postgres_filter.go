package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

type filterScanner interface {
	Scan(...any) error
}

func (r *PostgresRepository) CreateFilterView(ctx context.Context, sheetID, actorID string, input CreateFilterViewInput) (FilterView, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return FilterView{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FilterView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sheetID+":"+actorID+":"+input.IdempotencyKey); err != nil {
		return FilterView{}, err
	}
	if existing, lookupErr := scanFilterView(tx.QueryRow(ctx, `SELECT id::text,sheet_id::text,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at FROM filter_views WHERE sheet_id=$1 AND actor_id=$2 AND idempotency_key=$3`, sheetID, actorID, input.IdempotencyKey)); lookupErr == nil {
		return existing, nil
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return FilterView{}, lookupErr
	}
	var sheetExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE id=$1)`, sheetID).Scan(&sheetExists); err != nil {
		return FilterView{}, err
	}
	if !sheetExists {
		return FilterView{}, ErrNotFound
	}
	now := r.now()
	view, _, err := NormalizeFilterView(FilterView{ID: identity.New(), SheetID: sheetID, ActorID: actorID, CreateKey: input.IdempotencyKey, Name: input.Name, Range: input.Range, HeaderRows: input.HeaderRows, Criteria: cloneFilterCriteria(input.Criteria), Active: input.Active, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		return FilterView{}, err
	}
	criteria, _ := json.Marshal(view.Criteria)
	if view.Active {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sheetID+":"+actorID+":active-filter"); err != nil {
			return FilterView{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE filter_views SET active=false,updated_at=$3 WHERE sheet_id=$1 AND actor_id=$2 AND active`, sheetID, actorID, now); err != nil {
			return FilterView{}, err
		}
	}
	command, err := tx.Exec(ctx, `INSERT INTO filter_views(id,sheet_id,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10) ON CONFLICT (sheet_id,actor_id,idempotency_key) DO NOTHING`, view.ID, sheetID, actorID, input.IdempotencyKey, view.Name, view.Range, view.HeaderRows, criteria, view.Active, now)
	if err != nil {
		return FilterView{}, mapPostgresError(err)
	}
	if command.RowsAffected() == 0 {
		return scanFilterView(tx.QueryRow(ctx, `SELECT id::text,sheet_id::text,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at FROM filter_views WHERE sheet_id=$1 AND actor_id=$2 AND idempotency_key=$3`, sheetID, actorID, input.IdempotencyKey))
	}
	if err := tx.Commit(ctx); err != nil {
		return FilterView{}, err
	}
	return view, nil
}

func (r *PostgresRepository) ListFilterViews(ctx context.Context, sheetID, actorID string) ([]FilterView, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE id=$1)`, sheetID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT id::text,sheet_id::text,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at FROM filter_views WHERE sheet_id=$1 AND actor_id=$2 ORDER BY active DESC,updated_at DESC`, sheetID, actorID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]FilterView, 0)
	for rows.Next() {
		view, err := scanFilterView(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, view)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GetFilterView(ctx context.Context, id, actorID string) (FilterView, error) {
	view, err := scanFilterView(r.pool.QueryRow(ctx, `SELECT id::text,sheet_id::text,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at FROM filter_views WHERE id=$1 AND actor_id=$2`, id, actorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return FilterView{}, ErrNotFound
	}
	return view, err
}

func (r *PostgresRepository) UpdateFilterView(ctx context.Context, id, actorID string, input UpdateFilterViewInput) (FilterView, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return FilterView{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sheetID string
	if err := tx.QueryRow(ctx, `SELECT sheet_id::text FROM filter_views WHERE id=$1 AND actor_id=$2`, id, actorID).Scan(&sheetID); errors.Is(err, pgx.ErrNoRows) {
		return FilterView{}, ErrNotFound
	} else if err != nil {
		return FilterView{}, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sheetID+":"+actorID+":active-filter"); err != nil {
		return FilterView{}, err
	}
	current, err := scanFilterView(tx.QueryRow(ctx, `SELECT id::text,sheet_id::text,actor_id,idempotency_key,name,cell_range,header_rows,criteria,active,created_at,updated_at FROM filter_views WHERE id=$1 AND actor_id=$2 FOR UPDATE`, id, actorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return FilterView{}, ErrNotFound
	}
	if err != nil {
		return FilterView{}, err
	}
	next := cloneFilterView(current)
	if input.Name != nil {
		next.Name = *input.Name
	}
	if input.Range != nil {
		next.Range = *input.Range
	}
	if input.HeaderRows != nil {
		next.HeaderRows = *input.HeaderRows
	}
	if input.Criteria != nil {
		next.Criteria = cloneFilterCriteria(*input.Criteria)
	}
	if input.Active != nil {
		next.Active = *input.Active
	}
	next, _, err = NormalizeFilterView(next)
	if err != nil {
		return FilterView{}, err
	}
	next.UpdatedAt = r.now()
	if next.Active {
		if _, err := tx.Exec(ctx, `UPDATE filter_views SET active=false,updated_at=$4 WHERE sheet_id=$1 AND actor_id=$2 AND id<>$3 AND active`, next.SheetID, actorID, id, next.UpdatedAt); err != nil {
			return FilterView{}, err
		}
	}
	criteria, _ := json.Marshal(next.Criteria)
	if _, err := tx.Exec(ctx, `UPDATE filter_views SET name=$3,cell_range=$4,header_rows=$5,criteria=$6,active=$7,updated_at=$8 WHERE id=$1 AND actor_id=$2`, id, actorID, next.Name, next.Range, next.HeaderRows, criteria, next.Active, next.UpdatedAt); err != nil {
		return FilterView{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return FilterView{}, err
	}
	return next, nil
}

func (r *PostgresRepository) DeleteFilterView(ctx context.Context, id, actorID string) error {
	command, err := r.pool.Exec(ctx, `DELETE FROM filter_views WHERE id=$1 AND actor_id=$2`, id, actorID)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func scanFilterView(row filterScanner) (FilterView, error) {
	var view FilterView
	var criteria []byte
	if err := row.Scan(&view.ID, &view.SheetID, &view.ActorID, &view.CreateKey, &view.Name, &view.Range, &view.HeaderRows, &criteria, &view.Active, &view.CreatedAt, &view.UpdatedAt); err != nil {
		return FilterView{}, err
	}
	if err := json.Unmarshal(criteria, &view.Criteria); err != nil {
		return FilterView{}, err
	}
	if view.Criteria == nil {
		view.Criteria = []FilterCriterion{}
	}
	return view, nil
}
