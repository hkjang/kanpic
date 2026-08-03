package httpapi

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/workbook"
)

func queryFlag(r *http.Request, name string) bool {
	value := strings.TrimSpace(r.URL.Query().Get(name))
	if value == "" {
		return false
	}
	flag, err := strconv.ParseBool(value)
	return err == nil && flag
}

func (s *Server) searchWorkbook(w http.ResponseWriter, r *http.Request) {
	input := workbook.SearchWorkbookInput{
		Query:        r.URL.Query().Get("q"),
		SheetID:      strings.TrimSpace(r.URL.Query().Get("sheet_id")),
		MatchCase:    queryFlag(r, "match_case"),
		WholeCell:    queryFlag(r, "whole_cell"),
		UseRegex:     queryFlag(r, "regex"),
		SkipFormulas: queryFlag(r, "skip_formulas"),
	}
	var err error
	if value := strings.TrimSpace(r.URL.Query().Get("limit")); value != "" {
		input.Limit, err = strconv.Atoi(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: limit must be an integer", workbook.ErrInvalid))
			return
		}
	}
	if value := strings.TrimSpace(r.URL.Query().Get("offset")); value != "" {
		input.Offset, err = strconv.Atoi(value)
		if err != nil {
			s.writeError(w, r, fmt.Errorf("%w: offset must be an integer", workbook.ErrInvalid))
			return
		}
	}
	result, err := s.repository.SearchWorkbook(r.Context(), r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// replaceWorkbook previews or applies a workbook-wide find and replace. Each
// affected sheet becomes one atomic, undoable operation that is published to
// collaborators exactly like a manual edit.
func (s *Server) replaceWorkbook(w http.ResponseWriter, r *http.Request) {
	var input workbook.ReplaceWorkbookInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input.ActorID = actorID(r)
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	result, err := workbook.ReplaceWorkbookCells(r.Context(), s.repository, r.PathValue("workbookId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	for _, sheet := range result.Sheets {
		if sheet.Operation.Duplicate {
			continue
		}
		s.collab.PublishOperation(sheet.Operation.WorkbookID, sheet.SheetID, input.ActorID, input.ClientID, sheet.Cells, sheet.Operation)
		s.triggerCellAutomations(r, sheet.Operation, sheet.Cells)
	}
	writeJSON(w, http.StatusOK, result)
}
