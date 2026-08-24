package httpapi

import (
	"net/http"
	"strconv"
	"strings"

	"kanpic/internal/ai"
	"kanpic/internal/workbook"
	"kanpic/pkg/identity"
)

func (s *Server) workbookAgent() ai.WorkbookAgent {
	agent, _ := s.ai.(ai.WorkbookAgent)
	return agent
}

func (s *Server) getAgentContext(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	workbookID := r.PathValue("workbookId")
	if !s.requireWorkbookAccess(w, r, workbookID, workbook.CapabilityRead) {
		return
	}
	contextView, err := workbook.BuildAgentContext(r.Context(), s.repository, workbookID, r.URL.Query().Get("sheet_id"), r.URL.Query().Get("selection"))
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, contextView)
}

func (s *Server) sendAgentMessage(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	var payload struct {
		SheetID             string `json:"sheet_id"`
		SheetIDCamel        string `json:"sheetId"`
		Selection           string `json:"selection"`
		Message             string `json:"message"`
		Mode                string `json:"mode"`
		ConversationID      string `json:"conversation_id"`
		ConversationIDCamel string `json:"conversationId"`
		BaseVersion         int64  `json:"base_version"`
		BaseVersionCamel    int64  `json:"baseVersion"`
		IdempotencyKey      string `json:"idempotency_key"`
		ClientID            string `json:"client_id"`
		ClientIDCamel       string `json:"clientId"`
	}
	if !decodeJSON(w, r, &payload) {
		return
	}
	input := ai.AgentMessageInput{
		WorkbookID:     r.PathValue("workbookId"),
		SheetID:        firstNonEmpty(payload.SheetID, payload.SheetIDCamel),
		Selection:      payload.Selection,
		Message:        payload.Message,
		Mode:           payload.Mode,
		ConversationID: firstNonEmpty(payload.ConversationID, payload.ConversationIDCamel),
		BaseVersion:    firstPositive(payload.BaseVersion, payload.BaseVersionCamel),
		IdempotencyKey: payload.IdempotencyKey,
		ClientID:       firstNonEmpty(payload.ClientID, payload.ClientIDCamel),
		ActorID:        actorID(r),
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	if !s.requireWorkbookAccess(w, r, input.WorkbookID, workbook.CapabilityRead) {
		return
	}
	if input.BaseVersion < 1 {
		book, err := s.repository.GetWorkbook(r.Context(), input.WorkbookID)
		if err != nil {
			s.writeAIError(w, r, err)
			return
		}
		input.BaseVersion = book.Version
	}
	if strings.TrimSpace(input.IdempotencyKey) == "" {
		input.IdempotencyKey = identity.New()
	}
	run, err := s.workbookAgent().SendMessage(r.Context(), input)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, run)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstPositive(values ...int64) int64 {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func (s *Server) listAgentRuns(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	workbookID := r.PathValue("workbookId")
	if !s.requireWorkbookAccess(w, r, workbookID, workbook.CapabilityRead) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.workbookAgent().ListRuns(r.Context(), workbookID, actorID(r), limit)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) listAgentConversations(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	workbookID := r.PathValue("workbookId")
	if !s.requireWorkbookAccess(w, r, workbookID, workbook.CapabilityRead) {
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	items, err := s.workbookAgent().ListConversations(r.Context(), workbookID, actorID(r), limit)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) getAgentRun(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	run, err := s.workbookAgent().GetRun(r.Context(), r.PathValue("runId"), actorID(r))
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if !s.requireWorkbookAccess(w, r, run.WorkbookID, workbook.CapabilityRead) {
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) getAgentPlan(w http.ResponseWriter, r *http.Request) {
	if !requireAPIScopes(w, r, "ai.use", "range.read") {
		return
	}
	run, err := s.workbookAgent().GetRun(r.Context(), r.PathValue("runId"), actorID(r))
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if !s.requireWorkbookAccess(w, r, run.WorkbookID, workbook.CapabilityRead) {
		return
	}
	plan, err := s.workbookAgent().GetRunPlan(r.Context(), run.ID, actorID(r))
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, plan)
}

func (s *Server) executeAgentRun(w http.ResponseWriter, r *http.Request) {
	actionID := r.PathValue("runAction")
	phase := ""
	switch {
	case strings.HasSuffix(actionID, ":approve"):
		phase, actionID = "approve", strings.TrimSuffix(actionID, ":approve")
	case strings.HasSuffix(actionID, ":cancel"):
		phase, actionID = "cancel", strings.TrimSuffix(actionID, ":cancel")
	default:
		s.writeAIError(w, r, ai.ErrNotFound)
		return
	}
	s.executeAgentRunPhase(w, r, actionID, phase)
}

func (s *Server) approveAgentRun(w http.ResponseWriter, r *http.Request) {
	s.executeAgentRunPhase(w, r, r.PathValue("runId"), "approve")
}

func (s *Server) cancelAgentRun(w http.ResponseWriter, r *http.Request) {
	s.executeAgentRunPhase(w, r, r.PathValue("runId"), "cancel")
}

func (s *Server) executeAgentRunPhase(w http.ResponseWriter, r *http.Request, actionID, phase string) {
	run, err := s.workbookAgent().GetRun(r.Context(), actionID, actorID(r))
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if !s.requireWorkbookAccess(w, r, run.WorkbookID, workbook.CapabilityRead) {
		return
	}
	var input ai.ApprovalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	input.ActorID = actorID(r)
	if phase == "cancel" {
		if !requireAPIScopes(w, r, "ai.use") {
			return
		}
		cancelled, err := s.workbookAgent().CancelRun(r.Context(), actionID, input)
		if err != nil {
			s.writeAIError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, cancelled)
		return
	}
	requiredScopes := append([]string{"ai.use"}, ai.RequiredApprovalScopes(run.Action)...)
	if !s.requireWorkbookAccess(w, r, run.WorkbookID, workbook.CapabilityWrite) || !requireAPIScopes(w, r, requiredScopes...) {
		return
	}
	result, err := s.workbookAgent().ApproveRun(r.Context(), actionID, input)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if result.Operation != nil && !result.Operation.Duplicate {
		s.publishCells(r.Context(), result.Run.WorkbookID, result.Run.SheetID, input.ActorID, input.ClientID, result.Changes, *result.Operation)
		s.triggerCellAutomations(r, *result.Operation, result.Changes)
	}
	if result.Operation == nil || len(result.Run.Action.ToolCalls) > 0 {
		s.publishCurrentVersion(r.Context(), result.Run.WorkbookID, input.ActorID, input.ClientID)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) rollbackAgentChangeSet(w http.ResponseWriter, r *http.Request) {
	changeSetID := strings.TrimSpace(r.PathValue("changeSetId"))
	if changeSetID == "" {
		changeSetID = r.PathValue("changeSetAction")
		if !strings.HasSuffix(changeSetID, ":rollback") {
			s.writeAIError(w, r, ai.ErrNotFound)
			return
		}
		changeSetID = strings.TrimSuffix(changeSetID, ":rollback")
	}
	if !requireAPIScopes(w, r, "ai.use", "range.write") {
		return
	}
	var input ai.ApprovalInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	input.ActorID = actorID(r)
	run, err := s.workbookAgent().RunForChangeSet(r.Context(), changeSetID, input.ActorID)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if !s.requireWorkbookAccess(w, r, run.WorkbookID, workbook.CapabilityWrite) {
		return
	}
	result, err := s.workbookAgent().RollbackChangeSet(r.Context(), changeSetID, input)
	if err != nil {
		s.writeAIError(w, r, err)
		return
	}
	if result.Operation != nil && !result.Operation.Duplicate {
		s.publishCells(r.Context(), result.Run.WorkbookID, result.Run.SheetID, input.ActorID, input.ClientID, nil, *result.Operation)
	} else {
		s.publishCurrentVersion(r.Context(), result.Run.WorkbookID, input.ActorID, input.ClientID)
	}
	writeJSON(w, http.StatusOK, result)
}
