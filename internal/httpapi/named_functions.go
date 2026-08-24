package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listNamedFunctions(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListNamedFunctions(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createNamedFunction(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateNamedFunctionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateNamedFunction(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getNamedFunction(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetNamedFunction(r.Context(), r.PathValue("namedFunctionId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateNamedFunction(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateNamedFunctionInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateNamedFunction(r.Context(), r.PathValue("namedFunctionId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteNamedFunction(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("namedFunctionId")
	current, err := s.repository.GetNamedFunction(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteNamedFunction(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	// 지우면 그것을 쓰던 칸이 #NAME? 이 되므로 보고 있는 사람에게 알린다.
	s.publishCurrentVersion(r.Context(), current.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}
