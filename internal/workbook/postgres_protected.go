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

const protectedColumns = `p.id::text,p.sheet_id::text,p.idempotency_key,p.cell_range,p.scope,p.exceptions,p.description,p.editors,p.warning_only,p.revision,p.created_by,p.updated_by,p.created_at,p.updated_at`

type protectedScanner interface{ Scan(...any) error }

func scanProtectedRange(row protectedScanner) (ProtectedRange, error) {
	var rule ProtectedRange
	var editors []byte
	var exceptions []byte
	if err := row.Scan(&rule.ID, &rule.SheetID, &rule.CreateKey, &rule.Range, &rule.Scope, &exceptions, &rule.Description, &editors, &rule.WarningOnly,
		&rule.Revision, &rule.CreatedBy, &rule.UpdatedBy, &rule.CreatedAt, &rule.UpdatedAt); err != nil {
		return ProtectedRange{}, err
	}
	rule.Editors = []string{}
	if len(editors) > 0 {
		_ = json.Unmarshal(editors, &rule.Editors)
	}
	if len(exceptions) > 0 {
		_ = json.Unmarshal(exceptions, &rule.Exceptions)
	}
	return rule, nil
}

// protectionExceptionsJSON keeps the column a JSON array rather than null, so
// reading a row never has to tell "no exceptions" from "column missing".
func protectionExceptionsJSON(items []string) []byte {
	if items == nil {
		items = []string{}
	}
	encoded, _ := json.Marshal(items)
	return encoded
}

func (r *PostgresRepository) CreateProtectedRange(ctx context.Context, sheetID, actor string, input CreateProtectedRangeInput) (ProtectedRange, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return ProtectedRange{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProtectedRange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sheetID+":protection:"+key); err != nil {
		return ProtectedRange{}, err
	}
	if existing, lookupErr := scanProtectedRange(tx.QueryRow(ctx, `SELECT `+protectedColumns+` FROM protected_ranges p WHERE p.sheet_id=$1 AND p.idempotency_key=$2`, sheetID, key)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return ProtectedRange{}, lookupErr
	}
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT w.version FROM sheets s JOIN workbooks w ON w.id=s.workbook_id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, sheetID).Scan(&currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return ProtectedRange{}, ErrNotFound
	}
	if err != nil {
		return ProtectedRange{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM protected_ranges WHERE sheet_id=$1`, sheetID).Scan(&count); err != nil {
		return ProtectedRange{}, err
	}
	if count >= MaxProtectedRanges {
		return ProtectedRange{}, fmt.Errorf("%w: a sheet can hold %d protected ranges", ErrInvalid, MaxProtectedRanges)
	}
	rule, err := protectedFromInput(sheetID, key, actor, input)
	if err != nil {
		return ProtectedRange{}, err
	}
	now := r.now()
	rule.ID, rule.Revision, rule.CreatedAt, rule.UpdatedAt = identity.New(), 1, now, now
	editors, _ := json.Marshal(rule.Editors)
	if _, err := tx.Exec(ctx, `INSERT INTO protected_ranges(id,sheet_id,idempotency_key,cell_range,scope,exceptions,description,editors,warning_only,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$10,$11,$5,$6,$7,1,$8,$8,$9,$9)`,
		rule.ID, sheetID, rule.CreateKey, rule.Range, rule.Description, editors, rule.WarningOnly, actor, now, rule.Scope, protectionExceptionsJSON(rule.Exceptions)); err != nil {
		return ProtectedRange{}, mapPostgresError(err)
	}
	if err := bumpWorkbookVersionForSheet(ctx, tx, sheetID); err != nil {
		return ProtectedRange{}, err
	}
	return rule, tx.Commit(ctx)
}

func (r *PostgresRepository) ListProtectedRanges(ctx context.Context, sheetID string) ([]ProtectedRange, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+protectedColumns+` FROM protected_ranges p WHERE p.sheet_id=$1 ORDER BY p.cell_range`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProtectedRange, 0)
	for rows.Next() {
		rule, scanErr := scanProtectedRange(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

func (r *PostgresRepository) UpdateProtectedRange(ctx context.Context, id, actor string, input UpdateProtectedRangeInput) (ProtectedRange, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ProtectedRange{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanProtectedRange(tx.QueryRow(ctx, `SELECT `+protectedColumns+` FROM protected_ranges p WHERE p.id=$1 FOR UPDATE`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ProtectedRange{}, ErrNotFound
	}
	if err != nil {
		return ProtectedRange{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return ProtectedRange{}, ErrRevision
	}
	updated := current
	if input.Range != nil {
		updated.Range = *input.Range
	}
	if input.Scope != nil {
		updated.Scope = *input.Scope
	}
	if input.Exceptions != nil {
		updated.Exceptions = *input.Exceptions
	}
	if input.Description != nil {
		updated.Description = *input.Description
	}
	if input.Editors != nil {
		updated.Editors = *input.Editors
	}
	if input.WarningOnly != nil {
		updated.WarningOnly = *input.WarningOnly
	}
	normalized, _, err := NormalizeProtectedRange(updated)
	if err != nil {
		return ProtectedRange{}, err
	}
	now := r.now()
	normalized.Revision, normalized.UpdatedBy, normalized.UpdatedAt = current.Revision+1, actor, now
	editors, _ := json.Marshal(normalized.Editors)
	if _, err := tx.Exec(ctx, `UPDATE protected_ranges SET cell_range=$2,scope=$9,exceptions=$10,description=$3,editors=$4,warning_only=$5,revision=$6,updated_by=$7,updated_at=$8 WHERE id=$1`,
		id, normalized.Range, normalized.Description, editors, normalized.WarningOnly, normalized.Revision, actor, now, normalized.Scope, protectionExceptionsJSON(normalized.Exceptions)); err != nil {
		return ProtectedRange{}, mapPostgresError(err)
	}
	if err := bumpWorkbookVersionForSheet(ctx, tx, normalized.SheetID); err != nil {
		return ProtectedRange{}, err
	}
	return normalized, tx.Commit(ctx)
}

func (r *PostgresRepository) DeleteProtectedRange(ctx context.Context, id string) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var sheetID string
	if err := tx.QueryRow(ctx, `DELETE FROM protected_ranges WHERE id=$1 RETURNING sheet_id::text`, id).Scan(&sheetID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if err := bumpWorkbookVersionForSheet(ctx, tx, sheetID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// bumpWorkbookVersionForSheet moves the workbook version so open clients
// refresh after a protection changes.
func bumpWorkbookVersionForSheet(ctx context.Context, tx pgx.Tx, sheetID string) error {
	_, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=now() WHERE id=(SELECT workbook_id FROM sheets WHERE id=$1)`, sheetID)
	return err
}

// listProtectedRangesTx reads the rules inside an open transaction, which is
// what the cell write path needs before it applies anything.
func listProtectedRangesTx(ctx context.Context, tx pgx.Tx, sheetID string) ([]ProtectedRange, error) {
	rows, err := tx.Query(ctx, `SELECT `+protectedColumns+` FROM protected_ranges p WHERE p.sheet_id=$1`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]ProtectedRange, 0)
	for rows.Next() {
		rule, scanErr := scanProtectedRange(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		result = append(result, rule)
	}
	return result, rows.Err()
}

// insertProtectedRangeForCopy 는 이미 다듬어진 보호 범위를 그대로 담는다.
// 복제와 되돌리기가 쓴다 — 만들 때와 달리 새로 검사할 것이 없고, 오히려
// 그때의 모습 그대로여야 한다.
func insertProtectedRangeForCopy(ctx context.Context, tx pgx.Tx, rule ProtectedRange) error {
	editors := rule.Editors
	if editors == nil {
		editors = []string{}
	}
	_, err := tx.Exec(ctx, `INSERT INTO protected_ranges(id,sheet_id,idempotency_key,cell_range,scope,exceptions,description,editors,warning_only,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
		rule.ID, rule.SheetID, rule.CreateKey, rule.Range, rule.Scope, protectionExceptionsJSON(rule.Exceptions),
		rule.Description, editors, rule.WarningOnly, rule.Revision,
		rule.CreatedBy, rule.UpdatedBy, rule.CreatedAt, rule.UpdatedAt)
	return mapPostgresError(err)
}
