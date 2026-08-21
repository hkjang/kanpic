package httpapi

import (
	"net/http"

	"kanpic/internal/workbook"
)

// Protected ranges decide who may write to part of a sheet. The routes mirror
// the data validation ones so a client that knows one knows the other.

func (s *Server) listProtectedRanges(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListProtectedRanges(r.Context(), r.PathValue("sheetId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createProtectedRange(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateProtectedRangeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := s.repository.CreateProtectedRange(r.Context(), r.PathValue("sheetId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

func (s *Server) updateProtectedRange(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateProtectedRangeInput
	if !decodeJSON(w, r, &input) {
		return
	}
	rule, err := s.repository.UpdateProtectedRange(r.Context(), r.PathValue("protectionId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (s *Server) deleteProtectedRange(w http.ResponseWriter, r *http.Request) {
	if err := s.repository.DeleteProtectedRange(r.Context(), r.PathValue("protectionId")); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
