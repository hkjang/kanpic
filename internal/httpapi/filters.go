package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func (s *Server) createFilterView(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateFilterViewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.CreateFilterView(r.Context(), r.PathValue("sheetId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) listFilterViews(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListFilterViews(r.Context(), r.PathValue("sheetId"), actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getFilterView(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetFilterView(r.Context(), r.PathValue("filterViewId"), actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateFilterView(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateFilterViewInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateFilterView(r.Context(), r.PathValue("filterViewId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteFilterView(w http.ResponseWriter, r *http.Request) {
	if err := s.repository.DeleteFilterView(r.Context(), r.PathValue("filterViewId"), actorID(r)); err != nil {
		s.writeError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) evaluateFilterView(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("filterAction")
	if !strings.HasSuffix(action, ":evaluate") {
		http.NotFound(w, r)
		return
	}
	result, err := s.applyFilterView(r.Context(), strings.TrimSuffix(action, ":evaluate"), actorID(r))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applyFilterView(ctx context.Context, id, actor string) (workbook.FilterResult, error) {
	view, err := s.repository.GetFilterView(ctx, id, actor)
	if err != nil {
		return workbook.FilterResult{}, err
	}
	selected, err := cellrange.Parse(view.Range)
	if err != nil {
		return workbook.FilterResult{}, fmt.Errorf("%w: invalid filter range", workbook.ErrInvalid)
	}
	cells, err := s.repository.ReadRange(ctx, view.SheetID, selected)
	if err != nil {
		return workbook.FilterResult{}, err
	}
	return workbook.EvaluateFilter(view, cells)
}
