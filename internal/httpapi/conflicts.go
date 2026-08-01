package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listCellConflicts(w http.ResponseWriter, r *http.Request) {
	includeResolved := strings.EqualFold(r.URL.Query().Get("include_resolved"), "true")
	items, err := s.repository.ListCellConflicts(r.Context(), r.PathValue("workbookId"), includeResolved)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getCellConflict(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetCellConflict(r.Context(), r.PathValue("conflictId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) resolveCellConflict(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("conflictAction")
	if !strings.HasSuffix(action, ":resolve") {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	var input workbook.ResolveCellConflictInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	input.ActorID = actorID(r)
	result, err := s.repository.ResolveCellConflict(r.Context(), strings.TrimSuffix(action, ":resolve"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if !result.Operation.Duplicate {
		cell := result.Conflict.CurrentCell
		s.collab.PublishOperation(result.Conflict.WorkbookID, result.Conflict.SheetID, input.ActorID, input.ClientID, []workbook.CellInput{{Row: result.Conflict.Row, Column: result.Conflict.Column, Value: cell.Value, Formula: cell.Formula, Style: cell.Style}}, result.Operation)
	}
	writeJSON(w, http.StatusOK, result)
}
