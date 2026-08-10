package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/ai"
	"kanpic/internal/workbook"
)

// requireWorkbookAccess guards the endpoints that name their workbook in the
// request body. The routing middleware reads the workbook from the URL, so
// these routes would otherwise reach any workbook by id.
func (s *Server) requireWorkbookAccess(w http.ResponseWriter, r *http.Request, workbookID string, capability workbook.Capability) bool {
	id := strings.TrimSpace(workbookID)
	if id == "" {
		s.writeError(w, r, fmt.Errorf("%w: workbook_id is required", workbook.ErrInvalid))
		return false
	}
	access, err := s.repository.ResolveWorkbookAccess(r.Context(), id, s.accessPrincipal(r))
	if err != nil {
		s.writeError(w, r, err)
		return false
	}
	if !access.Role.Allows(capability) {
		s.writeError(w, r, fmt.Errorf("%w: 이 워크북에 대한 %s 권한이 없습니다", workbook.ErrForbidden, capability))
		return false
	}
	return true
}

func (s *Server) aiConfig(w http.ResponseWriter, r *http.Request) {
	config, err := s.ai.PublicConfig(r.Context())
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, config)
}

// previewAIPrompt shows the request that would be sent for the current
// selection. It reads cells, so it needs the same scopes as planning, but it
// never contacts the gateway and works while AI is switched off.
func (s *Server) previewAIPrompt(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "range.read") {
		return
	}
	var input ai.PlanInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if !s.requireWorkbookAccess(w, r, input.WorkbookID, workbook.CapabilityRead) {
		return
	}
	input.ActorID = actorID(r)
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = "preview"
	}
	preview, err := s.ai.Preview(r.Context(), input)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) planAIAction(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	var input ai.PlanInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	if !s.requireWorkbookAccess(w, r, input.WorkbookID, workbook.CapabilityRead) {
		return
	}
	input.ActorID = actorID(r)
	action, err := s.ai.Plan(r.Context(), input)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, action)
}

func (s *Server) listAIActions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.ai.List(r.Context(), r.PathValue("workbookId"), actorID(r), limit)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getAIAction(w http.ResponseWriter, r *http.Request) {
	action, err := s.ai.Get(r.Context(), r.PathValue("actionId"), actorID(r))
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, action)
}

func (s *Server) executeAIAction(w http.ResponseWriter, r *http.Request) {
	action := r.PathValue("actionAction")
	var phase string
	switch {
	case strings.HasSuffix(action, ":approve"):
		phase = "approve"
		action = strings.TrimSuffix(action, ":approve")
	case strings.HasSuffix(action, ":undo"):
		phase = "undo"
		action = strings.TrimSuffix(action, ":undo")
		if !requireAPIScopes(w, r, "ai.use", "range.write") {
			return
		}
	default:
		s.writeAIError(w, r, ai.ErrNotFound)
		return
	}
	var input ai.ApprovalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key")); headerKey != "" {
		input.IdempotencyKey = headerKey
	}
	input.ActorID = actorID(r)
	var result ai.ExecutionResult
	var err error
	if phase == "approve" {
		planned, loadErr := s.ai.Get(r.Context(), action, input.ActorID)
		if loadErr != nil {
			s.writeAIError(w, r, loadErr)
			return
		}
		if !requireAPIScopes(w, r, "ai.use", ai.RequiredApprovalScope(planned.Mode)) {
			return
		}
		result, err = s.ai.Approve(r.Context(), action, input)
	} else {
		result, err = s.ai.Undo(r.Context(), action, input)
	}
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if !result.Operation.Duplicate {
		s.collab.PublishOperation(result.Action.WorkbookID, result.Action.SheetID, input.ActorID, input.ClientID, result.Changes, result.Operation)
	}
	writeJSON(w, http.StatusOK, result)
}

func requireAPIScopes(w http.ResponseWriter, r *http.Request, scopes ...string) bool {
	principal, ok := apiPrincipal(r)
	if !ok {
		return true
	}
	for _, scope := range scopes {
		if !principal.Allows(scope) {
			writeJSON(w, http.StatusForbidden, map[string]any{"error": map[string]string{"code": "insufficient_scope", "message": scope + " scope가 필요합니다."}})
			return false
		}
	}
	return true
}

func (s *Server) writeAIError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ai.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": map[string]string{"code": "not_found", "message": "AI 작업을 찾을 수 없습니다."}})
	case errors.Is(err, ai.ErrDisabled):
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": map[string]string{"code": "ai_disabled", "message": "관리자 화면에서 AI Gateway를 설정하고 활성화하세요."}})
	case errors.Is(err, ai.ErrRevision):
		writeJSON(w, http.StatusConflict, map[string]any{"error": map[string]string{"code": "revision_conflict", "message": "AI 작업 상태가 변경되었습니다. 다시 불러오세요."}})
	case errors.Is(err, ai.ErrInvalid):
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_ai_action", "message": err.Error()}})
	case errors.Is(err, ai.ErrGateway):
		s.logger.Error("AI gateway request failed", "error", err, "path", r.URL.Path)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": map[string]string{"code": "ai_gateway_error", "message": "사내 LLM Gateway가 유효한 계획을 반환하지 못했습니다."}})
	case errors.Is(err, workbook.ErrNotFound), errors.Is(err, workbook.ErrInvalid), errors.Is(err, workbook.ErrVersionConflict), errors.Is(err, workbook.ErrRevision):
		s.writeError(w, r, err)
	default:
		s.logger.Error("AI action failed", "error", err, "path", r.URL.Path)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": map[string]string{"code": "ai_action_failed", "message": "AI 작업을 처리하지 못했습니다."}})
	}
}
