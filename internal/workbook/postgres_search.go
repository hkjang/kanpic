package workbook

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// searchPredicate renders the SQL boolean for one text expression so the
// search stays inside PostgreSQL for every option combination. $2 always
// carries the query, wrapped by searchQueryArgument when it is a pattern.
func searchPredicate(input SearchWorkbookInput, expression string) string {
	switch {
	case input.UseRegex && input.MatchCase:
		return expression + " ~ $2"
	case input.UseRegex:
		return expression + " ~* $2"
	case input.WholeCell && input.MatchCase:
		return expression + " = $2"
	case input.WholeCell:
		return "lower(" + expression + ")=lower($2)"
	case input.MatchCase:
		return "strpos(" + expression + ",$2)>0"
	default:
		return "strpos(lower(" + expression + "),lower($2))>0"
	}
}

func searchQueryArgument(input SearchWorkbookInput) string {
	if input.UseRegex && input.WholeCell {
		return "^(?:" + input.Query + ")$"
	}
	return input.Query
}

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
	valueMatchExpression := searchPredicate(normalized, `COALESCE((item.cell->'value') #>> '{}','')`)
	formulaMatchExpression := "false"
	if !normalized.SkipFormulas {
		formulaMatchExpression = searchPredicate(normalized, `COALESCE(item.cell->>'formula','')`)
	}
	statement := fmt.Sprintf(`
		SELECT s.id::text,s.name,item.cell,
		       %[1]s AS value_match,
		       %[2]s AS formula_match
		FROM sheets s
		JOIN cell_blocks b ON b.sheet_id=s.id
		CROSS JOIN LATERAL jsonb_each(b.payload) AS item(coordinate,cell)
		WHERE s.workbook_id=$1
		  AND ($5='' OR s.id::text=$5)
		  AND (%[1]s OR %[2]s)
		ORDER BY s.position,(item.cell->>'row')::integer,(item.cell->>'column')::integer
		LIMIT $3 OFFSET $4`, valueMatchExpression, formulaMatchExpression)
	rows, err := tx.Query(ctx, statement, workbookID, searchQueryArgument(normalized), normalized.Limit+1, normalized.Offset, normalized.SheetID)
	if err != nil {
		// An expression PostgreSQL rejects is user input, not a server fault.
		if normalized.UseRegex {
			return WorkbookSearchResult{}, fmt.Errorf("%w: query is not a valid regular expression", ErrInvalid)
		}
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
