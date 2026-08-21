package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const validationColumns = `d.id::text,w.id::text,w.version,d.sheet_id::text,d.idempotency_key,d.cell_range,d.rule_type,d.operator,d.options,d.source_range,d.value,d.value2,d.formula,d.allow_blank,d.reject_input,d.show_dropdown,d.display_style,d.help_text,d.revision,d.created_by,d.updated_by,d.created_at,d.updated_at`

type validationScanner interface{ Scan(...any) error }

func (r *PostgresRepository) CreateDataValidation(ctx context.Context, sheetID, actor string, input CreateDataValidationInput) (DataValidation, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return DataValidation{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DataValidation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sheetID+":validation:"+strings.TrimSpace(input.IdempotencyKey)); err != nil {
		return DataValidation{}, err
	}
	if existing, lookupErr := scanDataValidation(tx.QueryRow(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.sheet_id=$1 AND d.idempotency_key=$2`, sheetID, strings.TrimSpace(input.IdempotencyKey))); lookupErr == nil {
		return existing, nil
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return DataValidation{}, lookupErr
	}
	var workbookID string
	var currentVersion int64
	err = tx.QueryRow(ctx, `SELECT w.id::text,w.version FROM sheets s JOIN workbooks w ON w.id=s.workbook_id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, sheetID).Scan(&workbookID, &currentVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return DataValidation{}, ErrNotFound
	}
	if err != nil {
		return DataValidation{}, err
	}
	rule, selected, err := NewDataValidation(sheetID, actor, input)
	if err != nil {
		return DataValidation{}, err
	}
	if err := ensurePostgresValidationRangeAvailable(ctx, tx, sheetID, "", selected); err != nil {
		return DataValidation{}, err
	}
	now := r.now()
	rule.ID = identity.New()
	rule.WorkbookID = workbookID
	rule.Revision = 1
	rule.CreatedAt = now
	rule.UpdatedAt = now
	options, _ := json.Marshal(rule.Options)
	if _, err := tx.Exec(ctx, `INSERT INTO data_validations(id,sheet_id,idempotency_key,cell_range,rule_type,operator,options,source_range,value,value2,formula,allow_blank,reject_input,show_dropdown,display_style,help_text,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,1,$17,$17,$18,$18)`, rule.ID, sheetID, rule.CreateKey, rule.Range, rule.RuleType, rule.Operator, options, rule.SourceRange, nullableJSON(rule.Value), nullableJSON(rule.Value2), rule.Formula, rule.AllowBlank, rule.RejectInput, rule.ShowDropdown, rule.DisplayStyle, rule.HelpText, actor, now); err != nil {
		return DataValidation{}, mapPostgresError(err)
	}
	rule.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, rule.WorkbookVersion, now); err != nil {
		return DataValidation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DataValidation{}, err
	}
	return cloneDataValidation(rule), nil
}

func (r *PostgresRepository) ListDataValidations(ctx context.Context, sheetID string) ([]DataValidation, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.sheet_id=$1 ORDER BY d.cell_range,d.created_at,d.id`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DataValidation, 0)
	for rows.Next() {
		item, err := scanDataValidation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for index := range items {
		items[index] = r.resolveValidationSource(ctx, r.pool, items[index])
	}
	if len(items) == 0 {
		var exists bool
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM sheets WHERE id=$1)`, sheetID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return items, nil
}

func (r *PostgresRepository) GetDataValidation(ctx context.Context, id string) (DataValidation, error) {
	item, err := scanDataValidation(r.pool.QueryRow(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return DataValidation{}, ErrNotFound
	}
	if err != nil {
		return DataValidation{}, err
	}
	return r.resolveValidationSource(ctx, r.pool, item), nil
}

func (r *PostgresRepository) UpdateDataValidation(ctx context.Context, id, actor string, input UpdateDataValidationInput) (DataValidation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return DataValidation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanDataValidation(tx.QueryRow(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.id=$1 FOR UPDATE OF d,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return DataValidation{}, ErrNotFound
	}
	if err != nil {
		return DataValidation{}, err
	}
	updated, selected, err := ApplyDataValidationUpdate(current, actor, input)
	if err != nil {
		return DataValidation{}, err
	}
	if err := ensurePostgresValidationRangeAvailable(ctx, tx, current.SheetID, id, selected); err != nil {
		return DataValidation{}, err
	}
	now := r.now()
	updated.Revision = current.Revision + 1
	updated.UpdatedAt = now
	updated.WorkbookVersion = current.WorkbookVersion + 1
	options, _ := json.Marshal(updated.Options)
	command, err := tx.Exec(ctx, `UPDATE data_validations SET cell_range=$2,rule_type=$3,operator=$4,options=$5,source_range=$18,value=$6,value2=$7,formula=$8,allow_blank=$9,reject_input=$10,show_dropdown=$11,display_style=$12,help_text=$13,revision=$14,updated_by=$15,updated_at=$16 WHERE id=$1 AND revision=$17`, id, updated.Range, updated.RuleType, updated.Operator, options, nullableJSON(updated.Value), nullableJSON(updated.Value2), updated.Formula, updated.AllowBlank, updated.RejectInput, updated.ShowDropdown, updated.DisplayStyle, updated.HelpText, updated.Revision, actor, now, current.Revision, updated.SourceRange)
	if err != nil {
		return DataValidation{}, err
	}
	if command.RowsAffected() == 0 {
		return DataValidation{}, ErrRevision
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, updated.WorkbookVersion, now); err != nil {
		return DataValidation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return DataValidation{}, err
	}
	return cloneDataValidation(updated), nil
}

func (r *PostgresRepository) DeleteDataValidation(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanDataValidation(tx.QueryRow(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.id=$1 FOR UPDATE OF d,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	if _, err := tx.Exec(ctx, `DELETE FROM data_validations WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, current.WorkbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func listDataValidationsTx(ctx context.Context, tx pgx.Tx, sheetID string) ([]DataValidation, error) {
	rows, err := tx.Query(ctx, `SELECT `+validationColumns+` FROM data_validations d JOIN sheets s ON s.id=d.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE d.sheet_id=$1 ORDER BY d.created_at,d.id`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]DataValidation, 0)
	for rows.Next() {
		item, err := scanDataValidation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func ensurePostgresValidationRangeAvailable(ctx context.Context, tx pgx.Tx, sheetID, excludingID string, selected cellrange.Range) error {
	rows, err := tx.Query(ctx, `SELECT id::text,cell_range FROM data_validations WHERE sheet_id=$1 FOR UPDATE`, sheetID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id, value string
		if err := rows.Scan(&id, &value); err != nil {
			return err
		}
		if id == excludingID {
			continue
		}
		other, err := cellrange.Parse(value)
		if err != nil {
			return err
		}
		if ValidationRangesOverlap(selected, other) {
			return fmt.Errorf("%w: data validation ranges cannot overlap", ErrInvalid)
		}
	}
	return rows.Err()
}

func scanDataValidation(row validationScanner) (DataValidation, error) {
	var rule DataValidation
	var options, value, value2 []byte
	err := row.Scan(&rule.ID, &rule.WorkbookID, &rule.WorkbookVersion, &rule.SheetID, &rule.CreateKey, &rule.Range, &rule.RuleType, &rule.Operator, &options, &rule.SourceRange, &value, &value2, &rule.Formula, &rule.AllowBlank, &rule.RejectInput, &rule.ShowDropdown, &rule.DisplayStyle, &rule.HelpText, &rule.Revision, &rule.CreatedBy, &rule.UpdatedBy, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return DataValidation{}, err
	}
	if err := json.Unmarshal(options, &rule.Options); err != nil {
		return DataValidation{}, err
	}
	if rule.Options == nil {
		rule.Options = []ValidationOption{}
	}
	rule.Value, rule.Value2 = cloneJSON(value), cloneJSON(value2)
	return rule, nil
}

func nullableJSON(value json.RawMessage) any {
	if len(value) == 0 {
		return nil
	}
	return []byte(value)
}

func insertDataValidationTx(ctx context.Context, tx pgx.Tx, rule DataValidation) error {
	options, err := json.Marshal(rule.Options)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO data_validations(id,sheet_id,idempotency_key,cell_range,rule_type,operator,options,value,value2,formula,allow_blank,reject_input,show_dropdown,display_style,help_text,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20)`, rule.ID, rule.SheetID, rule.CreateKey, rule.Range, rule.RuleType, rule.Operator, options, nullableJSON(rule.Value), nullableJSON(rule.Value2), rule.Formula, rule.AllowBlank, rule.RejectInput, rule.ShowDropdown, rule.DisplayStyle, rule.HelpText, rule.Revision, rule.CreatedBy, rule.UpdatedBy, rule.CreatedAt, rule.UpdatedAt)
	return err
}

// resolveValidationSource fills in what a range dropdown currently offers. The
// options are never stored, so the rule cannot drift from the list it points
// at; the cost is this read wherever the rule is used.
func (r *PostgresRepository) resolveValidationSource(ctx context.Context, db queryer, rule DataValidation) DataValidation {
	sheetName, selected, ok := ValidationSource(rule)
	if !ok {
		return rule
	}
	sourceSheetID := rule.SheetID
	if sheetName != "" {
		rows, err := db.Query(ctx, `SELECT id::text FROM sheets WHERE workbook_id=(SELECT workbook_id FROM sheets WHERE id=$1) AND upper(btrim(name))=upper(btrim($2)) ORDER BY position LIMIT 1`, rule.SheetID, sheetName)
		if err != nil {
			return rule
		}
		found := false
		for rows.Next() {
			if err := rows.Scan(&sourceSheetID); err != nil {
				rows.Close()
				return rule
			}
			found = true
		}
		rows.Close()
		if !found {
			return rule
		}
	}
	cells, err := readRangeQuery(ctx, db, sourceSheetID, selected)
	if err != nil {
		return rule
	}
	rule.SourceOptions = ValidationOptionsFromCells(cells)
	return rule
}
