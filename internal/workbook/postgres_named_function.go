package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

const namedFunctionColumns = `f.id::text,f.workbook_id::text,w.version,f.idempotency_key,f.name,f.parameters,f.body,f.description,f.revision,f.created_by,f.updated_by,f.created_at,f.updated_at`

type namedFunctionScanner interface{ Scan(...any) error }

func scanNamedFunction(row namedFunctionScanner) (NamedFunction, error) {
	var item NamedFunction
	if err := row.Scan(&item.ID, &item.WorkbookID, &item.WorkbookVersion, &item.CreateKey, &item.Name,
		&item.Parameters, &item.Body, &item.Description, &item.Revision,
		&item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return NamedFunction{}, err
	}
	if item.Parameters == nil {
		item.Parameters = []string{}
	}
	return item, nil
}

func listNamedFunctionsFrom(ctx context.Context, source interface {
	Query(context.Context, string, ...any) (pgx.Rows, error)
}, workbookID string) ([]NamedFunction, error) {
	rows, err := source.Query(ctx, `SELECT `+namedFunctionColumns+` FROM named_functions f JOIN workbooks w ON w.id=f.workbook_id WHERE f.workbook_id=$1 AND w.deleted_at IS NULL ORDER BY lower(f.name), f.id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]NamedFunction, 0)
	for rows.Next() {
		item, scanErr := scanNamedFunction(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *PostgresRepository) CreateNamedFunction(ctx context.Context, workbookID, actor string, input CreateNamedFunctionInput) (NamedFunction, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return NamedFunction{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NamedFunction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	createKey := strings.TrimSpace(input.IdempotencyKey)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":named-function:"+createKey); err != nil {
		return NamedFunction{}, err
	}
	if existing, lookupErr := scanNamedFunction(tx.QueryRow(ctx, `SELECT `+namedFunctionColumns+` FROM named_functions f JOIN workbooks w ON w.id=f.workbook_id WHERE f.workbook_id=$1 AND f.idempotency_key=$2`, workbookID, createKey)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return NamedFunction{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return NamedFunction{}, ErrNotFound
	} else if err != nil {
		return NamedFunction{}, err
	}
	others, err := listNamedFunctionsFrom(ctx, tx, workbookID)
	if err != nil {
		return NamedFunction{}, err
	}
	if len(others) >= MaxNamedFunctions {
		return NamedFunction{}, fmt.Errorf("%w: a workbook may contain at most %d named functions", ErrInvalid, MaxNamedFunctions)
	}
	item, err := normalizeNamedFunction(NamedFunction{
		WorkbookID: workbookID, CreateKey: createKey, Name: input.Name, Parameters: input.Parameters,
		Body: input.Body, Description: input.Description, CreatedBy: actor, UpdatedBy: actor,
	}, others)
	if err != nil {
		return NamedFunction{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if _, err := tx.Exec(ctx, `INSERT INTO named_functions(id,workbook_id,idempotency_key,name,parameters,body,description,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,1,$8,$8,$9,$9)`,
		item.ID, item.WorkbookID, item.CreateKey, item.Name, item.Parameters, item.Body, item.Description, actor, now); err != nil {
		return NamedFunction{}, mapPostgresError(err)
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, workbookID); err != nil {
		return NamedFunction{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return NamedFunction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return NamedFunction{}, err
	}
	return item, nil
}

func (r *PostgresRepository) ListNamedFunctions(ctx context.Context, workbookID string) ([]NamedFunction, error) {
	items, err := listNamedFunctionsFrom(ctx, r.pool, workbookID)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return items, nil
}

func (r *PostgresRepository) GetNamedFunction(ctx context.Context, id string) (NamedFunction, error) {
	item, err := scanNamedFunction(r.pool.QueryRow(ctx, `SELECT `+namedFunctionColumns+` FROM named_functions f JOIN workbooks w ON w.id=f.workbook_id WHERE f.id=$1 AND w.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return NamedFunction{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) UpdateNamedFunction(ctx context.Context, id, actor string, input UpdateNamedFunctionInput) (NamedFunction, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return NamedFunction{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanNamedFunction(tx.QueryRow(ctx, `SELECT `+namedFunctionColumns+` FROM named_functions f JOIN workbooks w ON w.id=f.workbook_id WHERE f.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF f,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return NamedFunction{}, ErrNotFound
	}
	if err != nil {
		return NamedFunction{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return NamedFunction{}, ErrVersionConflict
	}
	updated := current
	updated.Parameters = append([]string(nil), current.Parameters...)
	if input.Name != nil {
		updated.Name = *input.Name
	}
	if input.Parameters != nil {
		updated.Parameters = append([]string(nil), (*input.Parameters)...)
	}
	if input.Body != nil {
		updated.Body = *input.Body
	}
	if input.Description != nil {
		updated.Description = *input.Description
	}
	updated.UpdatedBy = actor
	stored, err := listNamedFunctionsFrom(ctx, tx, current.WorkbookID)
	if err != nil {
		return NamedFunction{}, err
	}
	others := make([]NamedFunction, 0, len(stored))
	for _, item := range stored {
		if item.ID != id {
			others = append(others, item)
		}
	}
	normalized, err := normalizeNamedFunction(updated, others)
	if err != nil {
		return NamedFunction{}, err
	}
	now := r.now()
	if _, err := tx.Exec(ctx, `UPDATE named_functions SET name=$2,parameters=$3,body=$4,description=$5,revision=revision+1,updated_by=$6,updated_at=$7 WHERE id=$1`,
		id, normalized.Name, normalized.Parameters, normalized.Body, normalized.Description, actor, now); err != nil {
		return NamedFunction{}, mapPostgresError(err)
	}
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, current.WorkbookID); err != nil {
		return NamedFunction{}, err
	}
	normalized.Revision, normalized.UpdatedAt = current.Revision+1, now
	normalized.WorkbookVersion = current.WorkbookVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, normalized.WorkbookVersion, now); err != nil {
		return NamedFunction{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return NamedFunction{}, err
	}
	return normalized, nil
}

func (r *PostgresRepository) DeleteNamedFunction(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanNamedFunction(tx.QueryRow(ctx, `SELECT `+namedFunctionColumns+` FROM named_functions f JOIN workbooks w ON w.id=f.workbook_id WHERE f.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF f,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrVersionConflict
	}
	if _, err := tx.Exec(ctx, `DELETE FROM named_functions WHERE id=$1`, id); err != nil {
		return err
	}
	// 지우면 그것을 쓰던 칸은 #NAME? 이 된다. 조용히 예전 값을 남기면
	// 사람은 아직 살아 있는 줄 안다.
	if err := r.recalculateWorkbookFormulasTx(ctx, tx, current.WorkbookID); err != nil {
		return err
	}
	now := r.now()
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, current.WorkbookVersion+1, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
