package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"kanpic/internal/automation"
	"kanpic/internal/workbook"
)

const maxAutomationWebhookPayload = 1 << 20

func (s *Server) listAutomations(w http.ResponseWriter, r *http.Request) {
	overview, err := s.automations.Overview(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, overview)
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
	case strings.HasSuffix(action, ":webhook"):
		phase, action = "webhook", strings.TrimSuffix(action, ":webhook")
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
	if phase == "webhook" {
		s.invokeAutomationWebhook(w, r, action)
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

func (s *Server) invokeAutomationWebhook(w http.ResponseWriter, r *http.Request, automationID string) {
	principal, ok := apiPrincipal(r)
	if !ok {
		writeJSON(w, http.StatusUnauthorized, map[string]any{"error": map[string]string{"code": "api_key_required", "message": "웹훅 호출에는 개인 API 키 Bearer 인증이 필요합니다."}})
		return
	}
	if !principal.Allows("automation.webhook.invoke") {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "insufficient_scope", "message": "automation.webhook.invoke scope가 필요합니다."}})
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || utf8.RuneCountInString(idempotencyKey) > 200 {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_idempotency_key", "message": "Idempotency-Key 헤더는 1~200자여야 합니다."}})
		return
	}
	mediaType, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaErr != nil || mediaType != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]any{"error": map[string]string{"code": "unsupported_media_type", "message": "Content-Type application/json이 필요합니다."}})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAutomationWebhookPayload)
	payload, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": map[string]string{"code": "payload_too_large", "message": "웹훅 payload는 1MiB 이하여야 합니다."}})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_payload", "message": "웹훅 payload를 읽을 수 없습니다."}})
		return
	}
	if len(payload) > 0 && !json.Valid(payload) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_payload", "message": "웹훅 payload는 유효한 JSON이어야 합니다."}})
		return
	}
	digest := sha256.Sum256(payload)
	result, err := s.automations.Run(r.Context(), automationID, automation.RunInput{
		ActorID:        principal.UserID,
		ClientID:       "webhook:" + principal.KeyID,
		IdempotencyKey: idempotencyKey,
		TriggerType:    automation.TriggerWebhook,
		TriggerKeyID:   principal.KeyID,
		PayloadDigest:  hex.EncodeToString(digest[:]),
		PayloadBytes:   len(payload),
	})
	if err != nil {
		s.writeAutomationError(w, r, err)
		return
	}
	s.publishAutomationResult(principal.UserID, "webhook:"+principal.KeyID, result)
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

// automationFailure rides back on the write that set an automation off. The
// trigger runs before the response is written, so the editor can name the
// broken automation instead of leaving the failure in the server log alone.
type automationFailure struct {
	AutomationID string `json:"automation_id"`
	RunID        string `json:"run_id"`
	Message      string `json:"message"`
}

type cellMutationResponse struct {
	workbook.MutationResult
	AutomationFailures []automationFailure `json:"automation_failures,omitempty"`
}

func (s *Server) triggerCellAutomations(r *http.Request, result workbook.MutationResult, cells []workbook.CellInput) []automationFailure {
	return s.triggerCellAutomationsContext(r.Context(), result, cells, actorID(r))
}

func (s *Server) triggerCellAutomationsContext(ctx context.Context, result workbook.MutationResult, cells []workbook.CellInput, actor string) []automationFailure {
	if s.automations == nil || result.Duplicate || result.AppliedCells == 0 {
		return nil
	}
	triggerContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	runs, err := s.automations.TriggerCellChange(triggerContext, result, cells, actor)
	if err != nil {
		s.logger.Error("cell-change automation failed", "operation_id", result.OperationID, "workbook_id", result.WorkbookID, "error", err)
	}
	failures := make([]automationFailure, 0, len(runs))
	for _, run := range runs {
		if run.Run.Status == automation.StatusFailed {
			failures = append(failures, automationFailure{AutomationID: run.Run.AutomationID, RunID: run.Run.ID, Message: run.Run.ErrorMessage})
			continue
		}
		s.publishAutomationResult(run.Run.ActorID, "automation:"+run.Run.AutomationID, run)
	}
	if len(failures) == 0 {
		return nil
	}
	return failures
}

func (s *Server) publishAutomationResult(actor, clientID string, result automation.ExecutionResult) {
	if result.Run.Duplicate || result.Operation.OperationID == "" || result.Operation.Duplicate {
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
	case errors.Is(err, automation.ErrRunFailed):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "automation_run_failed", "message": "같은 멱등 키의 이전 자동화 실행이 실패했습니다. 실행 이력을 확인하세요."}})
	case errors.Is(err, workbook.ErrNotFound), errors.Is(err, workbook.ErrInvalid), errors.Is(err, workbook.ErrValidation):
		s.writeError(w, r, err)
	default:
		s.logger.Error("automation request failed", "path", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "automation_error", "message": "자동화 요청을 처리하지 못했습니다."}})
	}
}
