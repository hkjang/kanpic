package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listNamedRanges(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListNamedRanges(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createNamedRange(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateNamedRangeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateNamedRange(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getNamedRange(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetNamedRange(r.Context(), r.PathValue("namedRangeId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateNamedRange(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateNamedRangeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateNamedRange(r.Context(), r.PathValue("namedRangeId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteNamedRange(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("namedRangeId")
	item, err := s.repository.GetNamedRange(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteNamedRange(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}
