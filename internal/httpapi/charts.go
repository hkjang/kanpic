package httpapi

import (
	"net/http"
	"strings"

	"kanpic/internal/workbook"
)

func (s *Server) listCharts(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListCharts(r.Context(), r.PathValue("workbookId"), r.URL.Query().Get("sheet_id"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createChart(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateChartInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateChart(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getChart(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetChart(r.Context(), r.PathValue("chartId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) getChartData(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetChartData(r.Context(), r.PathValue("chartId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateChart(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateChartInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateChart(r.Context(), r.PathValue("chartId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteChart(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("chartId")
	item, err := s.repository.GetChart(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteChart(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}
