package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listSheetTables(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListSheetTables(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createSheetTable(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateSheetTableInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateSheetTable(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), item.SheetID, "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getSheetTable(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetSheetTable(r.Context(), r.PathValue("sheetTableId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateSheetTable(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateSheetTableInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateSheetTable(r.Context(), r.PathValue("sheetTableId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), item.SheetID, "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteSheetTable(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("sheetTableId")
	current, err := s.repository.GetSheetTable(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteSheetTable(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	// 지우면 그 이름을 쓰던 칸이 #NAME? 이 되므로 보고 있는 사람에게 알린다.
	s.publishCurrentVersion(r.Context(), current.WorkbookID, actorID(r), current.SheetID)
	w.WriteHeader(http.StatusNoContent)
}
