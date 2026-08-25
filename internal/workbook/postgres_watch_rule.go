package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

const watchRuleColumns = `id::text,workbook_id::text,sheet_id::text,idempotency_key,watcher,cell_range,label,enabled,revision,created_by,updated_by,created_at,updated_at`

type watchRuleScanner interface{ Scan(...any) error }

func scanWatchRule(row watchRuleScanner) (WatchRule, error) {
	var item WatchRule
	if err := row.Scan(&item.ID, &item.WorkbookID, &item.SheetID, &item.CreateKey, &item.Watcher,
		&item.Range, &item.Label, &item.Enabled, &item.Revision,
		&item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return WatchRule{}, err
	}
	return item, nil
}

func (r *PostgresRepository) CreateWatchRule(ctx context.Context, workbookID, actor string, input CreateWatchRuleInput) (WatchRule, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return WatchRule{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	createKey := strings.TrimSpace(input.IdempotencyKey)
	if existing, err := scanWatchRule(r.pool.QueryRow(ctx, `SELECT `+watchRuleColumns+` FROM watch_rules WHERE workbook_id=$1 AND idempotency_key=$2`, workbookID, createKey)); err == nil {
		return existing, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return WatchRule{}, err
	}
	var owner string
	if err := r.pool.QueryRow(ctx, `SELECT s.workbook_id::text FROM sheets s JOIN workbooks w ON w.id=s.workbook_id WHERE s.id=$1 AND w.deleted_at IS NULL`, input.SheetID).Scan(&owner); errors.Is(err, pgx.ErrNoRows) {
		return WatchRule{}, ErrNotFound
	} else if err != nil {
		return WatchRule{}, err
	}
	if owner != workbookID {
		return WatchRule{}, fmt.Errorf("%w: the sheet does not belong to this workbook", ErrInvalid)
	}
	var count int
	if err := r.pool.QueryRow(ctx, `SELECT count(*) FROM watch_rules WHERE workbook_id=$1`, workbookID).Scan(&count); err != nil {
		return WatchRule{}, err
	}
	if count >= MaxWatchRules {
		return WatchRule{}, fmt.Errorf("%w: a workbook may contain at most %d watch rules", ErrInvalid, MaxWatchRules)
	}
	watcher := strings.TrimSpace(input.Watcher)
	if watcher == "" {
		watcher = actor
	}
	item, err := normalizeWatchRule(WatchRule{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: createKey,
		Watcher: watcher, Range: input.Range, Label: input.Label, Enabled: true,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		return WatchRule{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if _, err := r.pool.Exec(ctx, `INSERT INTO watch_rules(id,workbook_id,sheet_id,idempotency_key,watcher,cell_range,label,enabled,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,true,1,$8,$8,$9,$9)`,
		item.ID, item.WorkbookID, item.SheetID, item.CreateKey, item.Watcher, item.Range, item.Label, actor, now); err != nil {
		return WatchRule{}, mapPostgresError(err)
	}
	return item, nil
}

func (r *PostgresRepository) ListWatchRules(ctx context.Context, workbookID, watcher string) ([]WatchRule, error) {
	var exists bool
	if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
		return nil, err
	}
	if !exists {
		return nil, ErrNotFound
	}
	rows, err := r.pool.Query(ctx, `SELECT `+watchRuleColumns+` FROM watch_rules WHERE workbook_id=$1 AND lower(watcher)=lower($2) ORDER BY id`, workbookID, strings.TrimSpace(watcher))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WatchRule, 0)
	for rows.Next() {
		item, scanErr := scanWatchRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) SheetWatchRules(ctx context.Context, sheetID string) ([]WatchRule, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+watchRuleColumns+` FROM watch_rules WHERE sheet_id=$1 AND enabled ORDER BY id`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WatchRule, 0)
	for rows.Next() {
		item, scanErr := scanWatchRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) GetWatchRule(ctx context.Context, id string) (WatchRule, error) {
	item, err := scanWatchRule(r.pool.QueryRow(ctx, `SELECT `+watchRuleColumns+` FROM watch_rules WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return WatchRule{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) UpdateWatchRule(ctx context.Context, id, actor string, input UpdateWatchRuleInput) (WatchRule, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return WatchRule{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanWatchRule(tx.QueryRow(ctx, `SELECT `+watchRuleColumns+` FROM watch_rules WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return WatchRule{}, ErrNotFound
	}
	if err != nil {
		return WatchRule{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return WatchRule{}, ErrVersionConflict
	}
	updated := current
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.Label != nil {
		updated.Label = *input.Label
	}
	if input.Enabled != nil {
		updated.Enabled = *input.Enabled
	}
	updated.UpdatedBy = actor
	normalized, err := normalizeWatchRule(updated)
	if err != nil {
		return WatchRule{}, err
	}
	now := r.now()
	if _, err := tx.Exec(ctx, `UPDATE watch_rules SET cell_range=$2,label=$3,enabled=$4,revision=revision+1,updated_by=$5,updated_at=$6 WHERE id=$1`,
		id, normalized.Range, normalized.Label, normalized.Enabled, actor, now); err != nil {
		return WatchRule{}, mapPostgresError(err)
	}
	normalized.Revision, normalized.UpdatedAt = current.Revision+1, now
	return normalized, tx.Commit(ctx)
}

func (r *PostgresRepository) DeleteWatchRule(ctx context.Context, id, _ string, expectedRevision *int64) error {
	if expectedRevision != nil {
		command, err := r.pool.Exec(ctx, `DELETE FROM watch_rules WHERE id=$1 AND revision=$2`, id, *expectedRevision)
		if err != nil {
			return err
		}
		if command.RowsAffected() == 0 {
			var exists bool
			if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM watch_rules WHERE id=$1)`, id).Scan(&exists); err != nil {
				return err
			}
			if exists {
				return ErrVersionConflict
			}
			return ErrNotFound
		}
		return nil
	}
	command, err := r.pool.Exec(ctx, `DELETE FROM watch_rules WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// listSheetWatchRulesTx 는 열린 트랜잭션 안에서 그 시트의 규칙을 모두 읽는다.
// 꺼 둔 규칙도 읽는다. 다시 켰을 때 엉뚱한 칸을 보고 있으면 안 되므로
// 행과 열이 움직일 때는 꺼 둔 것도 같이 옮겨야 한다.
func listSheetWatchRulesTx(ctx context.Context, tx pgx.Tx, sheetID string) ([]WatchRule, error) {
	rows, err := tx.Query(ctx, `SELECT `+watchRuleColumns+` FROM watch_rules WHERE sheet_id=$1 ORDER BY id`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]WatchRule, 0)
	for rows.Next() {
		item, scanErr := scanWatchRule(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
