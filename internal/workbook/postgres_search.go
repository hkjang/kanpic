package workbook

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// SearchWorkbook evaluates value and formula predicates inside PostgreSQL so
// a search does not deserialize every cell block into the API process.
func (r *PostgresRepository) SearchWorkbook(ctx context.Context, workbookID string, input SearchWorkbookInput) (WorkbookSearchResult, error) {
	normalized, err := normalizeSearchInput(input)
	if err != nil {
		return WorkbookSearchResult{}, err
	}
	tx, err := r.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return WorkbookSearchResult{}, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck -- a committed transaction returns pgx.ErrTxClosed
	var version int64
	if err := tx.QueryRow(ctx, `SELECT version FROM workbooks WHERE id=$1 AND deleted_at IS NULL`, workbookID).Scan(&version); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return WorkbookSearchResult{}, ErrNotFound
		}
		return WorkbookSearchResult{}, err
	}
	rows, err := tx.Query(ctx, `
		SELECT s.id::text,s.name,item.cell,
		       strpos(lower(COALESCE((item.cell->'value') #>> '{}','')),lower($2))>0 AS value_match,
		       strpos(lower(COALESCE(item.cell->>'formula','')),lower($2))>0 AS formula_match
		FROM sheets s
		JOIN cell_blocks b ON b.sheet_id=s.id
		CROSS JOIN LATERAL jsonb_each(b.payload) AS item(coordinate,cell)
		WHERE s.workbook_id=$1
		  AND (
			strpos(lower(COALESCE((item.cell->'value') #>> '{}','')),lower($2))>0
			OR strpos(lower(COALESCE(item.cell->>'formula','')),lower($2))>0
		  )
		ORDER BY s.position,(item.cell->>'row')::integer,(item.cell->>'column')::integer
		LIMIT $3 OFFSET $4`, workbookID, normalized.Query, normalized.Limit+1, normalized.Offset)
	if err != nil {
		return WorkbookSearchResult{}, err
	}
	defer rows.Close()
	result := WorkbookSearchResult{WorkbookID: workbookID, WorkbookVersion: version, Query: normalized.Query, Items: make([]WorkbookSearchMatch, 0, normalized.Limit+1)}
	for rows.Next() {
		var sheet Sheet
		var payload []byte
		var valueMatch, formulaMatch bool
		if err := rows.Scan(&sheet.ID, &sheet.Name, &payload, &valueMatch, &formulaMatch); err != nil {
			return WorkbookSearchResult{}, err
		}
		var cell Cell
		if err := json.Unmarshal(payload, &cell); err != nil {
			return WorkbookSearchResult{}, err
		}
		fields := make([]string, 0, 2)
		if valueMatch {
			fields = append(fields, "value")
		}
		if formulaMatch {
			fields = append(fields, "formula")
		}
		result.Items = append(result.Items, newSearchMatch(sheet, cell, fields))
	}
	if err := rows.Err(); err != nil {
		return WorkbookSearchResult{}, err
	}
	rows.Close()
	if err := tx.Commit(ctx); err != nil {
		return WorkbookSearchResult{}, err
	}
	if len(result.Items) > normalized.Limit {
		result.Items = result.Items[:normalized.Limit]
		next := normalized.Offset + normalized.Limit
		result.NextOffset = &next
	}
	return result, nil
}
