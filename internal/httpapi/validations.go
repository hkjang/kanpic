package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

func (s *Server) listDataValidations(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListDataValidations(r.Context(), r.PathValue("sheetId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createDataValidation(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateDataValidationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateDataValidation(r.Context(), r.PathValue("sheetId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getDataValidation(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetDataValidation(r.Context(), r.PathValue("validationId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateDataValidation(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateDataValidationInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateDataValidation(r.Context(), r.PathValue("validationId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), "", "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteDataValidation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("validationId")
	item, err := s.repository.GetDataValidation(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteDataValidation(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), item.WorkbookID, actorID(r), "")
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) evaluateDataValidation(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("validationAction")
	if !strings.HasSuffix(action, ":evaluate") {
		s.writeError(w, r, workbook.ErrNotFound)
		return
	}
	result, err := s.applyDataValidation(r.Context(), strings.TrimSuffix(action, ":evaluate"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) applyDataValidation(ctx context.Context, id string) (workbook.ValidationEvaluation, error) {
	rule, err := s.repository.GetDataValidation(ctx, id)
	if err != nil {
		return workbook.ValidationEvaluation{}, err
	}
	selected, err := cellrange.Parse(rule.Range)
	if err != nil {
		return workbook.ValidationEvaluation{}, fmt.Errorf("%w: invalid validation range", workbook.ErrInvalid)
	}
	cells, err := s.repository.ReadRange(ctx, rule.SheetID, selected)
	if err != nil {
		return workbook.ValidationEvaluation{}, err
	}
	return workbook.EvaluateDataValidation(rule, cells)
}

func optionalRevision(value string) (*int64, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	revision, err := strconv.ParseInt(value, 10, 64)
	if err != nil || revision < 1 {
		return nil, fmt.Errorf("%w: expected_revision must be a positive integer", workbook.ErrInvalid)
	}
	return &revision, nil
}
