package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/identity"
)

type layoutOperationDocument struct {
	Layout SheetLayout `json:"layout"`
}

func (r *PostgresRepository) ApplySheetLayout(ctx context.Context, raw SheetLayoutMutation) (SheetLayoutResult, error) {
	input, err := normalizeSheetLayoutMutation(raw)
	if err != nil {
		return SheetLayoutResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return SheetLayoutResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var workbookID string
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.version FROM workbooks w JOIN sheets s ON s.workbook_id=w.id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, input.SheetID).Scan(&workbookID, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return SheetLayoutResult{}, ErrNotFound
	}
	if err != nil {
		return SheetLayoutResult{}, err
	}
	if existing, found, findErr := findLayoutDuplicateTx(ctx, tx, workbookID, input.ActorID, input.IdempotencyKey); findErr != nil {
		return SheetLayoutResult{}, findErr
	} else if found {
		existing.Duplicate = true
		return existing, tx.Commit(ctx)
	}
	var propertiesData []byte
	if err := tx.QueryRow(ctx, `SELECT properties FROM sheets WHERE id=$1 FOR UPDATE`, input.SheetID).Scan(&propertiesData); errors.Is(err, pgx.ErrNoRows) {
		return SheetLayoutResult{}, ErrNotFound
	} else if err != nil {
		return SheetLayoutResult{}, err
	}
	var properties sheetProperties
	_ = json.Unmarshal(propertiesData, &properties)
	properties.Layout = normalizeSheetLayout(properties.Layout)
	if properties.Layout.Revision != input.ExpectedRevision {
		return SheetLayoutResult{}, fmt.Errorf("%w: expected layout revision %d, current revision is %d", ErrRevision, input.ExpectedRevision, properties.Layout.Revision)
	}
	properties.Layout, err = applySheetLayoutMutation(properties.Layout, input)
	if err != nil {
		return SheetLayoutResult{}, err
	}
	layoutData, _ := json.Marshal(properties.Layout)
	now, serverVersion := r.now(), currentVersion+1
	if _, err := tx.Exec(ctx, `UPDATE sheets SET properties=jsonb_set(properties,'{layout}',$2::jsonb,true) WHERE id=$1`, input.SheetID, layoutData); err != nil {
		return SheetLayoutResult{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, serverVersion, now); err != nil {
		return SheetLayoutResult{}, err
	}
	result := SheetLayoutResult{OperationID: identity.New(), WorkbookID: workbookID, SheetID: input.SheetID, BaseVersion: currentVersion, ServerVersion: serverVersion, Layout: properties.Layout, CreatedAt: now}
	document, _ := json.Marshal(layoutOperationDocument{Layout: result.Layout})
	if _, err := tx.Exec(ctx, `INSERT INTO cell_operations(operation_id,idempotency_key,workbook_id,sheet_id,actor_id,client_id,base_version,server_version,operation_type,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, result.OperationID, input.IdempotencyKey, workbookID, input.SheetID, input.ActorID, input.ClientID, currentVersion, serverVersion, "layout."+input.Action, document, now); err != nil {
		return SheetLayoutResult{}, mapPostgresError(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SheetLayoutResult{}, err
	}
	return result, nil
}

func findLayoutDuplicateTx(ctx context.Context, tx pgx.Tx, workbookID, actorID, key string) (SheetLayoutResult, bool, error) {
	var result SheetLayoutResult
	var data []byte
	err := tx.QueryRow(ctx, `SELECT operation_id::text,workbook_id::text,coalesce(sheet_id::text,''),base_version,server_version,payload,created_at FROM cell_operations WHERE workbook_id=$1 AND actor_id=$2 AND idempotency_key=$3`, workbookID, actorID, key).Scan(&result.OperationID, &result.WorkbookID, &result.SheetID, &result.BaseVersion, &result.ServerVersion, &data, &result.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return SheetLayoutResult{}, false, nil
	}
	if err != nil {
		return SheetLayoutResult{}, false, err
	}
	var document layoutOperationDocument
	if err := json.Unmarshal(data, &document); err != nil {
		return SheetLayoutResult{}, false, err
	}
	result.Layout = normalizeSheetLayout(document.Layout)
	return result, true, nil
}
