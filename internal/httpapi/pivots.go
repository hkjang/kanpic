package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listPivots(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListPivots(r.Context(), r.PathValue("workbookId"), r.URL.Query().Get("sheet_id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createPivot(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreatePivotInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreatePivot(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getPivot(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetPivot(r.Context(), r.PathValue("pivotId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) getPivotData(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetPivotData(r.Context(), r.PathValue("pivotId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) refreshPivot(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.RefreshPivot(r.Context(), r.PathValue("pivotId"), actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) drilldownPivot(w http.ResponseWriter, r *http.Request) {
	input := workbook.PivotDrilldownInput{RowKey: r.URL.Query().Get("row_key"), ColumnKey: r.URL.Query().Get("column_key")}
	var err error
	if input.Offset, err = pivotQueryInt(r.URL.Query().Get("offset"), 0); err != nil {
		s.writeError(w, r, err)
		return
	}
	if input.Limit, err = pivotQueryInt(r.URL.Query().Get("limit"), 100); err != nil {
		s.writeError(w, r, err)
		return
	}
	item, err := s.repository.PivotDrilldown(r.Context(), r.PathValue("pivotId"), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updatePivot(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdatePivotInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdatePivot(r.Context(), r.PathValue("pivotId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deletePivot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("pivotId")
	item, err := s.repository.GetPivot(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeletePivot(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}

func pivotQueryInt(raw string, fallback int) (int, error) {
	if strings.TrimSpace(raw) == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, workbook.ErrInvalid
	}
	return value, nil
}
