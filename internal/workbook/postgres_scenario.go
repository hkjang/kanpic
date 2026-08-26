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

const scenarioColumns = `id::text,workbook_id::text,sheet_id::text,idempotency_key,name,inputs,note,revision,created_by,updated_by,created_at,updated_at`

type scenarioScanner interface{ Scan(...any) error }

func scanScenario(row scenarioScanner) (Scenario, error) {
	var item Scenario
	var inputs []byte
	if err := row.Scan(&item.ID, &item.WorkbookID, &item.SheetID, &item.CreateKey, &item.Name,
		&inputs, &item.Note, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return Scenario{}, err
	}
	if len(inputs) > 0 {
		if err := json.Unmarshal(inputs, &item.Inputs); err != nil {
			return Scenario{}, err
		}
	}
	return item, nil
}

func listScenariosFrom(ctx context.Context, source interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, workbookID string) ([]Scenario, error) {
	rows, err := source.Query(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE workbook_id=$1 ORDER BY name`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Scenario, 0)
	for rows.Next() {
		item, scanErr := scanScenario(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func insertScenarioTx(ctx context.Context, tx pgx.Tx, item Scenario) error {
	inputs, err := json.Marshal(item.Inputs)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO scenarios(id,workbook_id,sheet_id,idempotency_key,name,inputs,note,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		item.ID, item.WorkbookID, item.SheetID, item.CreateKey, item.Name, inputs, item.Note,
		item.Revision, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt)
	return mapPostgresError(err)
}

func (r *PostgresRepository) CreateScenario(ctx context.Context, workbookID, actor string, input CreateScenarioInput) (Scenario, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return Scenario{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Scenario{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	createKey := strings.TrimSpace(input.IdempotencyKey)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":scenario:"+createKey); err != nil {
		return Scenario{}, err
	}
	if existing, lookupErr := scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE workbook_id=$1 AND idempotency_key=$2`, workbookID, createKey)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return Scenario{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	} else if err != nil {
		return Scenario{}, err
	}
	var sheetWorkbook string
	if err := tx.QueryRow(ctx, `SELECT workbook_id::text FROM sheets WHERE id=$1`, input.SheetID).Scan(&sheetWorkbook); errors.Is(err, pgx.ErrNoRows) || sheetWorkbook != workbookID {
		return Scenario{}, fmt.Errorf("%w: unknown sheet", ErrInvalid)
	} else if err != nil {
		return Scenario{}, err
	}
	existing, err := listScenariosFrom(ctx, tx, workbookID)
	if err != nil {
		return Scenario{}, err
	}
	if len(existing) >= MaxScenarios {
		return Scenario{}, fmt.Errorf("%w: a workbook may contain at most %d scenarios", ErrInvalid, MaxScenarios)
	}
	item, err := normalizeScenario(Scenario{
		WorkbookID: workbookID, SheetID: input.SheetID, CreateKey: createKey,
		Name: input.Name, Inputs: input.Inputs, Note: input.Note,
		CreatedBy: actor, UpdatedBy: actor,
	})
	if err != nil {
		return Scenario{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if err := insertScenarioTx(ctx, tx, item); err != nil {
		return Scenario{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return Scenario{}, err
	}
	return item, tx.Commit(ctx)
}

func (r *PostgresRepository) ListScenarios(ctx context.Context, workbookID string) ([]Scenario, error) {
	items, err := listScenariosFrom(ctx, r.pool, workbookID)
	if err != nil {
		return nil, err
	}
	var version int64
	if err := r.pool.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&version); errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	} else if err != nil {
		return nil, err
	}
	for index := range items {
		items[index].WorkbookVersion = version
	}
	return items, nil
}

func (r *PostgresRepository) GetScenario(ctx context.Context, id string) (Scenario, error) {
	item, err := scanScenario(r.pool.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	} else if err != nil {
		return Scenario{}, err
	}
	_ = r.pool.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1`, item.WorkbookID).Scan(&item.WorkbookVersion)
	return item, nil
}

func (r *PostgresRepository) UpdateScenario(ctx context.Context, id, actor string, input UpdateScenarioInput) (Scenario, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Scenario{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Scenario{}, ErrNotFound
	} else if err != nil {
		return Scenario{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return Scenario{}, ErrRevision
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, current.WorkbookID).Scan(&currentVersion); err != nil {
		return Scenario{}, err
	}
	updated := current
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Inputs != nil {
		updated.Inputs = *input.Inputs
	}
	if input.Note != nil {
		updated.Note = *input.Note
	}
	normalized, err := normalizeScenario(updated)
	if err != nil {
		return Scenario{}, err
	}
	inputs, err := json.Marshal(normalized.Inputs)
	if err != nil {
		return Scenario{}, err
	}
	now := r.now()
	normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt = current.Revision+1, actor, now
	if _, err := tx.Exec(ctx, `UPDATE scenarios SET name=$2,inputs=$3,note=$4,revision=$5,updated_by=$6,updated_at=$7 WHERE id=$1`,
		id, normalized.Name, inputs, normalized.Note, normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt); err != nil {
		return Scenario{}, mapPostgresError(err)
	}
	normalized.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, normalized.WorkbookVersion, now); err != nil {
		return Scenario{}, err
	}
	return normalized, tx.Commit(ctx)
}

func (r *PostgresRepository) DeleteScenario(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanScenario(tx.QueryRow(ctx, `SELECT `+scenarioColumns+` FROM scenarios WHERE id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, current.WorkbookID).Scan(&currentVersion); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM scenarios WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, currentVersion+1, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
