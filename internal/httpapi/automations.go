package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/automation"
	"kanpic/internal/workbook"
)

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	items, err := s.automations.List(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createAutomation(w http.ResponseWriter, r *http.Request) {
	var input automation.CreateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.automations.Create(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getAutomation(w http.ResponseWriter, r *http.Request) {
	item, err := s.automations.Get(r.Context(), r.PathValue("automationId"))
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateAutomation(w http.ResponseWriter, r *http.Request) {
	var input automation.UpdateInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.automations.Update(r.Context(), r.PathValue("automationId"), actorID(r), input)
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteAutomation(w http.ResponseWriter, r *http.Request) {
	revision, err := strconv.ParseInt(r.URL.Query().Get("expected_revision"), 10, 64)
	if err != nil || revision < 1 {
		s.writeAutomationError(w, r, automation.ErrInvalid)
		return
	}
	if err := s.automations.Delete(r.Context(), r.PathValue("automationId"), actorID(r), revision); err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) executeAutomationAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("automationAction")
	var phase string
	switch {
	case strings.HasSuffix(action, ":test"):
		phase, action = "test", strings.TrimSuffix(action, ":test")
	case strings.HasSuffix(action, ":run"):
		phase, action = "run", strings.TrimSuffix(action, ":run")
	default:
		s.writeAutomationError(w, r, automation.ErrNotFound)
		return
	}
	if phase == "test" {
		if !requireAPIScopes(w, r, "range.read") {
			return
		}
		preview, err := s.automations.Preview(r.Context(), action)
		if err != nil {
			s.writeAutomationError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, preview)
		return
	}
	item, err := s.automations.Get(r.Context(), action)
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	if !requireAPIScopes(w, r, "automation.run", automation.RequiredActionScope(item.Action.Type)) {
		return
	}
	var input automation.RunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	input.ActorID = actorID(r)
	result, err := s.automations.Run(r.Context(), action, input)
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	s.publishAutomationResult(input.ActorID, input.ClientID, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) listAutomationRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.automations.ListRuns(r.Context(), r.PathValue("automationId"), limit)
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) undoAutomationRun(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("runAction")
	if !strings.HasSuffix(action, ":undo") {
		s.writeAutomationError(w, r, automation.ErrNotFound)
		return
	}
	if !requireAPIScopes(w, r, "range.write") {
		return
	}
	var input automation.RunInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	input.ActorID = actorID(r)
	result, err := s.automations.Undo(r.Context(), strings.TrimSuffix(action, ":undo"), input)
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	s.publishAutomationResult(input.ActorID, input.ClientID, result)
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) triggerCellAutomations(r *http.Request, result workbook.MutationResult, cells []workbook.CellInput) {
	s.triggerCellAutomationsContext(r.Context(), result, cells, actorID(r))
}

func (s *Server) triggerCellAutomationsContext(ctx context.Context, result workbook.MutationResult, cells []workbook.CellInput, actor string) {
	if s.automations == nil || result.Duplicate || result.AppliedCells == 0 {
		return
	}
	runs, err := s.automations.TriggerCellChange(ctx, result, cells, actor)
	if err != nil {
		s.logger.Error("cell-change automation failed", "operation_id", result.OperationID, "workbook_id", result.WorkbookID, "error", err)
	}
	for _, run := range runs {
		s.publishAutomationResult(run.Run.ActorID, "automation:"+run.Run.AutomationID, run)
	}
}

func (s *Server) publishAutomationResult(actor, clientID string, result automation.ExecutionResult) {
	if result.Operation.OperationID == "" || result.Operation.Duplicate {
		return
	}
	s.collab.PublishOperation(result.Operation.WorkbookID, result.Operation.SheetID, actor, clientID, result.Changes, result.Operation)
}

func (s *Server) writeAutomationError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, automation.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found", "message": "자동화 또는 실행 이력을 찾을 수 없습니다."}})
	case errors.Is(err, automation.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_automation", "message": err.Error()}})
	case errors.Is(err, automation.ErrRevision), errors.Is(err, workbook.ErrVersionConflict), errors.Is(err, workbook.ErrRevision):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "revision_conflict", "message": "자동화 또는 워크북이 변경되었습니다. 다시 불러오세요."}})
	case errors.Is(err, automation.ErrDisabled):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "automation_disabled", "message": "관리자 화면에서 자동화를 활성화하세요."}})
	case errors.Is(err, automation.ErrRate):
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": map[string]string{"code": "automation_rate_limit", "message": err.Error()}})
	case errors.Is(err, workbook.ErrNotFound), errors.Is(err, workbook.ErrInvalid), errors.Is(err, workbook.ErrValidation):
		s.writeError(w, r, err)
	default:
		s.logger.Error("automation request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "automation_error", "message": "자동화 요청을 처리하지 못했습니다."}})
	}
}
