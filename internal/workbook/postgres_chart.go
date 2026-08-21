package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const chartColumns = `c.id::text,c.workbook_id::text,w.version,c.sheet_id::text,coalesce(c.source_sheet_id::text,''),c.idempotency_key,c.chart_type,c.title,c.source_range,c.first_row_headers,c.first_column_labels,c.legend_position,c.x_axis_title,c.y_axis_title,c.secondary_axis,c.position_x,c.position_y,c.width,c.height,c.revision,c.created_by,c.updated_by,c.created_at,c.updated_at`

type chartScanner interface{ Scan(...any) error }

func (r *PostgresRepository) CreateChart(ctx context.Context, workbookID, actor string, input CreateChartInput) (Chart, error) {
	key := strings.TrimSpace(input.IdempotencyKey)
	if key == "" {
		return Chart{}, fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Chart{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, workbookID+":chart:"+actor+":"+key); err != nil {
		return Chart{}, err
	}
	if existing, lookupErr := scanChart(tx.QueryRow(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.workbook_id=$1 AND c.created_by=$2 AND c.idempotency_key=$3 AND w.deleted_at IS NULL`, workbookID, actor, key)); lookupErr == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return Chart{}, lookupErr
	}
	var currentVersion int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, workbookID).Scan(&currentVersion); errors.Is(err, pgx.ErrNoRows) {
		return Chart{}, ErrNotFound
	} else if err != nil {
		return Chart{}, err
	}
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM charts WHERE workbook_id=$1`, workbookID).Scan(&count); err != nil {
		return Chart{}, err
	}
	if count >= MaxChartsPerWorkbook {
		return Chart{}, fmt.Errorf("%w: a workbook may contain at most %d charts", ErrInvalid, MaxChartsPerWorkbook)
	}
	item, err := chartFromInput(workbookID, key, actor, input)
	if err != nil {
		return Chart{}, err
	}
	if err := validatePostgresChartSheets(ctx, tx, item); err != nil {
		return Chart{}, err
	}
	now := r.now()
	item.ID, item.Revision, item.CreatedAt, item.UpdatedAt = identity.New(), 1, now, now
	if err := insertChartTx(ctx, tx, item); err != nil {
		return Chart{}, err
	}
	item.WorkbookVersion = currentVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, workbookID, item.WorkbookVersion, now); err != nil {
		return Chart{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Chart{}, err
	}
	return item, nil
}

func (r *PostgresRepository) ListCharts(ctx context.Context, workbookID, sheetID string) ([]Chart, error) {
	query := `SELECT ` + chartColumns + ` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.workbook_id=$1 AND w.deleted_at IS NULL`
	args := []any{workbookID}
	if strings.TrimSpace(sheetID) != "" {
		query += ` AND c.sheet_id=$2`
		args = append(args, strings.TrimSpace(sheetID))
	}
	query += ` ORDER BY c.created_at,c.id`
	rows, err := r.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Chart, 0)
	for rows.Next() {
		item, scanErr := scanChart(rows)
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
		if err := r.pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM workbooks WHERE id=$1 AND deleted_at IS NULL)`, workbookID).Scan(&exists); err != nil {
			return nil, err
		}
		if !exists {
			return nil, ErrNotFound
		}
	}
	return items, nil
}

func (r *PostgresRepository) GetChart(ctx context.Context, id string) (Chart, error) {
	item, err := scanChart(r.pool.QueryRow(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.id=$1 AND w.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Chart{}, ErrNotFound
	}
	return item, err
}

func (r *PostgresRepository) GetChartData(ctx context.Context, id string) (ChartData, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ChartData{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	item, err := scanChart(tx.QueryRow(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.id=$1 AND w.deleted_at IS NULL`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ChartData{}, ErrNotFound
	}
	if err != nil {
		return ChartData{}, err
	}
	if item.SourceSheetID == "" || item.SourceRange == "#REF!" {
		result := ChartData{Chart: item, WorkbookVersion: item.WorkbookVersion, Series: []ChartSeries{}, Warning: "#REF!"}
		return result, tx.Commit(ctx)
	}
	selected, err := cellrange.Parse(item.SourceRange)
	if err != nil {
		return ChartData{}, err
	}
	cells, err := readRangeQuery(ctx, tx, item.SourceSheetID, selected)
	if err != nil {
		return ChartData{}, err
	}
	result, err := buildChartData(item, item.WorkbookVersion, cells)
	if err != nil {
		return ChartData{}, err
	}
	return result, tx.Commit(ctx)
}

func (r *PostgresRepository) UpdateChart(ctx context.Context, id, actor string, input UpdateChartInput) (Chart, error) {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return Chart{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanChart(tx.QueryRow(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF c,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return Chart{}, ErrNotFound
	}
	if err != nil {
		return Chart{}, err
	}
	if input.ExpectedRevision != nil && *input.ExpectedRevision != current.Revision {
		return Chart{}, ErrRevision
	}
	updated := current
	if input.SheetID != nil {
		updated.SheetID = *input.SheetID
	}
	if input.SourceSheetID != nil {
		updated.SourceSheetID = *input.SourceSheetID
	}
	if input.Type != nil {
		updated.Type = *input.Type
	}
	if input.Title != nil {
		updated.Title = *input.Title
	}
	if input.SourceRange != nil {
		updated.SourceRange = *input.SourceRange
	}
	if input.FirstRowHeaders != nil {
		updated.FirstRowHeaders = *input.FirstRowHeaders
	}
	if input.FirstColumnLabels != nil {
		updated.FirstColumnLabels = *input.FirstColumnLabels
	}
	if input.LegendPosition != nil {
		updated.LegendPosition = *input.LegendPosition
	}
	if input.XAxisTitle != nil {
		updated.XAxisTitle = *input.XAxisTitle
	}
	if input.SecondaryAxis != nil {
		updated.SecondaryAxis = *input.SecondaryAxis
	}
	if input.YAxisTitle != nil {
		updated.YAxisTitle = *input.YAxisTitle
	}
	if input.Position != nil {
		updated.Position = *input.Position
	}
	updated, err = normalizeChart(updated, false)
	if err != nil {
		return Chart{}, err
	}
	if err := validatePostgresChartSheets(ctx, tx, updated); err != nil {
		return Chart{}, err
	}
	now := r.now()
	updated.Revision, updated.UpdatedBy, updated.UpdatedAt = current.Revision+1, actor, now
	if _, err := tx.Exec(ctx, `UPDATE charts SET sheet_id=$2,source_sheet_id=$3,chart_type=$4,title=$5,source_range=$6,first_row_headers=$7,first_column_labels=$8,legend_position=$9,x_axis_title=$10,y_axis_title=$11,secondary_axis=$19,position_x=$12,position_y=$13,width=$14,height=$15,revision=$16,updated_by=$17,updated_at=$18 WHERE id=$1`, id, updated.SheetID, updated.SourceSheetID, updated.Type, updated.Title, updated.SourceRange, updated.FirstRowHeaders, updated.FirstColumnLabels, updated.LegendPosition, updated.XAxisTitle, updated.YAxisTitle, updated.Position.X, updated.Position.Y, updated.Position.Width, updated.Position.Height, updated.Revision, actor, now, updated.SecondaryAxis); err != nil {
		return Chart{}, mapPostgresError(err)
	}
	updated.WorkbookVersion = current.WorkbookVersion + 1
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=$2,updated_at=$3 WHERE id=$1`, current.WorkbookID, updated.WorkbookVersion, now); err != nil {
		return Chart{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Chart{}, err
	}
	return updated, nil
}

func (r *PostgresRepository) DeleteChart(ctx context.Context, id, _ string, expectedRevision *int64) error {
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	current, err := scanChart(tx.QueryRow(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.id=$1 AND w.deleted_at IS NULL FOR UPDATE OF c,w`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if expectedRevision != nil && *expectedRevision != current.Revision {
		return ErrRevision
	}
	if _, err := tx.Exec(ctx, `DELETE FROM charts WHERE id=$1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE workbooks SET version=version+1,updated_at=$2 WHERE id=$1`, current.WorkbookID, r.now()); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func validatePostgresChartSheets(ctx context.Context, tx pgx.Tx, item Chart) error {
	var count int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM sheets WHERE workbook_id=$1 AND id=ANY($2::uuid[])`, item.WorkbookID, []string{item.SheetID, item.SourceSheetID}).Scan(&count); err != nil {
		return err
	}
	expected := 2
	if item.SheetID == item.SourceSheetID {
		expected = 1
	}
	if count != expected {
		return fmt.Errorf("%w: chart and source sheets must belong to the workbook", ErrInvalid)
	}
	return nil
}

func scanChart(row chartScanner) (Chart, error) {
	var item Chart
	err := row.Scan(&item.ID, &item.WorkbookID, &item.WorkbookVersion, &item.SheetID, &item.SourceSheetID, &item.CreateKey, &item.Type, &item.Title, &item.SourceRange, &item.FirstRowHeaders, &item.FirstColumnLabels, &item.LegendPosition, &item.XAxisTitle, &item.YAxisTitle, &item.SecondaryAxis, &item.Position.X, &item.Position.Y, &item.Position.Width, &item.Position.Height, &item.Revision, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt)
	return item, err
}

func insertChartTx(ctx context.Context, tx pgx.Tx, item Chart) error {
	var source any = item.SourceSheetID
	if item.SourceSheetID == "" {
		source = nil
	}
	_, err := tx.Exec(ctx, `INSERT INTO charts(id,workbook_id,sheet_id,source_sheet_id,idempotency_key,chart_type,title,source_range,first_row_headers,first_column_labels,legend_position,x_axis_title,y_axis_title,secondary_axis,position_x,position_y,width,height,revision,created_by,updated_by,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23)`, item.ID, item.WorkbookID, item.SheetID, source, item.CreateKey, item.Type, item.Title, item.SourceRange, item.FirstRowHeaders, item.FirstColumnLabels, item.LegendPosition, item.XAxisTitle, item.YAxisTitle, item.SecondaryAxis, item.Position.X, item.Position.Y, item.Position.Width, item.Position.Height, item.Revision, item.CreatedBy, item.UpdatedBy, item.CreatedAt, item.UpdatedAt)
	return mapPostgresError(err)
}

func listChartsTx(ctx context.Context, tx pgx.Tx, workbookID string) ([]Chart, error) {
	rows, err := tx.Query(ctx, `SELECT `+chartColumns+` FROM charts c JOIN workbooks w ON w.id=c.workbook_id WHERE c.workbook_id=$1 ORDER BY c.created_at,c.id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Chart, 0)
	for rows.Next() {
		item, scanErr := scanChart(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func readRangeQuery(ctx context.Context, db queryer, sheetID string, selected cellrange.Range) ([]Cell, error) {
	rows, err := db.Query(ctx, `SELECT payload FROM cell_blocks WHERE sheet_id=$1 AND block_row BETWEEN $2 AND $3 AND block_column BETWEEN $4 AND $5`, sheetID, (selected.Start.Row-1)/cellBlockSize, (selected.End.Row-1)/cellBlockSize, (selected.Start.Column-1)/cellBlockSize, (selected.End.Column-1)/cellBlockSize)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]Cell, 0)
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var payload map[string]Cell
		if err := json.Unmarshal(data, &payload); err != nil {
			return nil, err
		}
		for _, cell := range payload {
			if selected.Contains(cell.Row, cell.Column) {
				result = append(result, cell)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Row == result[j].Row {
			return result[i].Column < result[j].Column
		}
		return result[i].Row < result[j].Row
	})
	return result, nil
}
