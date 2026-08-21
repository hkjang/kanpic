package workbook

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const conditionalFormatColumns = `c.id::text,w.id::text,w.version,c.sheet_id::text,c.idempotency_key,c.name,c.cell_range,c.rule_type,c.operator,c.formula,c.value,c.value2,c.style,c.min_color,c.mid_color,c.max_color,c.bar_color,c.priority,c.stop_if_true,c.revision,c.created_by,c.updated_by,c.created_at,c.updated_at`

type conditionalFormatScanner interface{ Scan(...any) error }

func (r *PostgresRepository) CreateConditionalFormat(ctx context.Context, sheetID, actor string, input CreateConditionalFormatInput) (ConditionalFormat, error) {
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		return ConditionalFormat{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ConditionalFormat{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	key := strings.TrimSpace(input.IdempotencyKey)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, sheetID+":conditional-format:"+key); err != nil {
		return ConditionalFormat{}, err
	}
	if existing, lookupErr := scanConditionalFormat(tx.QueryRow(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE c.sheet_id=$1 AND c.idempotency_key=$2`, sheetID, key)); lookupErr == nil {
		return existing, nil
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return ConditionalFormat{}, lookupErr
	}
	var workbookID string
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT w.id::text,w.version FROM sheets s JOIN workbooks w ON w.id=s.workbook_id WHERE s.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF w`, sheetID).Scan(&workbookID, &currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return ConditionalFormat{}, ErrNotFound
	} else if err != nil {
		return ConditionalFormat{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM conditional_formats WHERE sheet_id=$1`, sheetID).Scan(&count); err != nil {
		return ConditionalFormat{}, err
	}
	if count >= MaxConditionalFormats {
		return ConditionalFormat{}, fmt.Errorf("%w: a sheet may contain at most %d conditional formats", ErrInvalid, MaxConditionalFormats)
	}
	rule, _, err := NewConditionalFormat(sheetID, actor, input)
	if err != nil {
		return ConditionalFormat{}, err
	}
	now := r.now()
	rule.ID, rule.WorkbookID, rule.WorkbookVersion, rule.Revision = identity.New(), workbookID, currentVersion+1, 1
	rule.CreatedAt, rule.UpdatedAt = now, now
	if err := insertConditionalFormatTx(ctx, tx, rule); err != nil {
		return ConditionalFormat{}, mapPostgresError(err)
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, rule.WorkbookVersion, now); err != nil {
		return ConditionalFormat{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConditionalFormat{}, err
	}
	return cloneConditionalFormat(rule), nil
}

func (r *PostgresRepository) ListConditionalFormats(ctx context.Context, sheetID string) ([]ConditionalFormat, error) {
	rows, err := r.pool.Query(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE c.sheet_id=$1 ORDER BY c.priority,c.created_at,c.id`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ConditionalFormat, 0)
	for rows.Next() {
		item, scanErr := scanConditionalFormat(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
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

func (r *PostgresRepository) GetConditionalFormat(ctx context.Context, id string) (ConditionalFormat, error) {
	item, err := scanConditionalFormat(r.pool.QueryRow(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE c.id=$1`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ConditionalFormat{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) EvaluateConditionalFormats(ctx context.Context, sheetID string, requested cellrange.Range) (ConditionalFormatEvaluation, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ConditionalFormatEvaluation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rules, err := listConditionalFormatsTx(ctx, tx, sheetID)
	if err != nil {
		return ConditionalFormatEvaluation{}, err
	}
	sources := make([]conditionalFormatSource, 0, len(rules))
	var workbookVersion int64
	for _, rule := range rules {
		selected := conditionalSourceRange(rule)
		cells, readErr := readRangeQuery(ctx, tx, sheetID, selected)
		if readErr != nil {
			return ConditionalFormatEvaluation{}, readErr
		}
		workbookVersion = rule.WorkbookVersion
		sources = append(sources, conditionalFormatSource{Rule: rule, Cells: cells})
	}
	if len(rules) == 0 {
		if err := tx.QueryRow(ctx, `SELECT w.version FROM sheets s JOIN workbooks w ON w.id=s.workbook_id WHERE s.id=$1`, sheetID).Scan(&workbookVersion); errors.Is(err, pgx.ErrNoRows) {
			return ConditionalFormatEvaluation{}, ErrNotFound
		} else if err != nil {
			return ConditionalFormatEvaluation{}, err
		}
	}
	result, err := EvaluateConditionalFormats(sheetID, workbookVersion, requested, sources)
	if err != nil {
		return ConditionalFormatEvaluation{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConditionalFormatEvaluation{}, err
	}
	return result, nil
}

func (r *PostgresRepository) UpdateConditionalFormat(ctx context.Context, id, actor string, input UpdateConditionalFormatInput) (ConditionalFormat, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return ConditionalFormat{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanConditionalFormat(tx.QueryRow(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE c.id=$1 FOR UPDATE OF c,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ConditionalFormat{}, ErrNotFound
	}
	if err != nil {
		return ConditionalFormat{}, err
	}
	updated, _, err := ApplyConditionalFormatUpdate(current, actor, input)
	if err != nil {
		return ConditionalFormat{}, err
	}
	now := r.now()
	updated.Revision, updated.UpdatedAt, updated.WorkbookVersion = current.Revision+1, now, current.WorkbookVersion+1
	command, err := tx.Exec(ctx, `UPDATE conditional_formats SET name=$2,cell_range=$3,rule_type=$4,operator=$5,formula=$19,value=$6,value2=$7,style=$8,min_color=$9,mid_color=$10,max_color=$11,bar_color=$12,priority=$13,stop_if_true=$14,revision=$15,updated_by=$16,updated_at=$17 WHERE id=$1 AND revision=$18`, id, updated.Name, updated.Range, updated.RuleType, updated.Operator, nullableJSON(updated.Value), nullableJSON(updated.Value2), nullableJSON(updated.Style), updated.MinColor, updated.MidColor, updated.MaxColor, updated.BarColor, updated.Priority, updated.StopIfTrue, updated.Revision, actor, now, current.Revision, updated.Formula)
	if err != nil {
		return ConditionalFormat{}, err
	}
	if command.RowsAffected() == 0 {
		return ConditionalFormat{}, ErrRevision
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, updated.WorkbookVersion, now); err != nil {
		return ConditionalFormat{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConditionalFormat{}, err
	}
	return cloneConditionalFormat(updated), nil
}

func (r *PostgresRepository) DeleteConditionalFormat(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanConditionalFormat(tx.QueryRow(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE c.id=$1 FOR UPDATE OF c,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	if _, err := tx.Exec(ctx, `DELETE FROM conditional_formats WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, current.WorkbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func listConditionalFormatsTx(ctx context.Context, tx pgx.Tx, sheetID string) ([]ConditionalFormat, error) {
	rows, err := tx.Query(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE c.sheet_id=$1 ORDER BY c.priority,c.created_at,c.id`, sheetID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ConditionalFormat, 0)
	for rows.Next() {
		item, scanErr := scanConditionalFormat(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func listWorkbookConditionalFormatsTx(ctx context.Context, tx pgx.Tx, workbookID string) ([]ConditionalFormat, error) {
	rows, err := tx.Query(ctx, `SELECT `+conditionalFormatColumns+` FROM conditional_formats c JOIN sheets s ON s.id=c.sheet_id JOIN workbooks w ON w.id=s.workbook_id WHERE w.id=$1 ORDER BY c.priority,c.created_at,c.id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ConditionalFormat, 0)
	for rows.Next() {
		item, scanErr := scanConditionalFormat(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func scanConditionalFormat(row conditionalFormatScanner) (ConditionalFormat, error) {
	var rule ConditionalFormat
	var value, value2, style []byte
	err := row.Scan(&rule.ID, &rule.WorkbookID, &rule.WorkbookVersion, &rule.SheetID, &rule.CreateKey, &rule.Name, &rule.Range, &rule.RuleType, &rule.Operator, &rule.Formula, &value, &value2, &style, &rule.MinColor, &rule.MidColor, &rule.MaxColor, &rule.BarColor, &rule.Priority, &rule.StopIfTrue, &rule.Revision, &rule.CreatedBy, &rule.UpdatedBy, &rule.CreatedAt, &rule.UpdatedAt)
	if err != nil {
		return ConditionalFormat{}, err
	}
	rule.Value, rule.Value2, rule.Style = cloneJSON(value), cloneJSON(value2), cloneJSON(style)
	return rule, nil
}

func insertConditionalFormatTx(ctx context.Context, tx pgx.Tx, rule ConditionalFormat) error {
	_, err := tx.Exec(ctx, `INSERT INTO conditional_formats(id,sheet_id,idempotency_key,name,cell_range,rule_type,operator,formula,value,value2,style,min_color,mid_color,max_color,bar_color,priority,stop_if_true,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22)`, rule.ID, rule.SheetID, rule.CreateKey, rule.Name, rule.Range, rule.RuleType, rule.Operator, rule.Formula, nullableJSON(rule.Value), nullableJSON(rule.Value2), nullableJSON(rule.Style), rule.MinColor, rule.MidColor, rule.MaxColor, rule.BarColor, rule.Priority, rule.StopIfTrue, rule.Revision, rule.CreatedBy, rule.UpdatedBy, rule.CreatedAt, rule.UpdatedAt)
	return err
}
