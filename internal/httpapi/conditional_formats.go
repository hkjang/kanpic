package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func (s *Server) listConditionalFormats(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListConditionalFormats(r.Context(), r.PathValue("sheetId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createConditionalFormat(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateConditionalFormatInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateConditionalFormat(r.Context(), r.PathValue("sheetId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getConditionalFormat(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetConditionalFormat(r.Context(), r.PathValue("conditionalFormatId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) evaluateConditionalFormats(w http.ResponseWriter, r *http.Request) {
	selected, err := cellrange.Parse(r.URL.Query().Get("range"))
	if err != nil {
		s.writeError(w, r, workbook.ErrInvalid)
		return
	}
	result, err := s.repository.EvaluateConditionalFormats(r.Context(), r.PathValue("sheetId"), selected)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) updateConditionalFormat(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateConditionalFormatInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateConditionalFormat(r.Context(), r.PathValue("conditionalFormatId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteConditionalFormat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("conditionalFormatId")
	item, err := s.repository.GetConditionalFormat(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteConditionalFormat(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}
