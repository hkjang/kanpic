package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"kanpic/internal/workbook"
	"kanpic/pkg/identity"
)

func (s *Service) SendMessage(ctx context.Context, input AgentMessageInput) (AgentRun, error) {
	input = normalizeAgentMessage(input)
	if input.ActorID == "" || input.WorkbookID == "" || input.SheetID == "" || input.Selection == "" || input.IdempotencyKey == "" {
		return AgentRun{}, fmt.Errorf("%w: workbook_id, sheet_id, selection and idempotency_key are required", ErrInvalid)
	}
	if input.Message == "" || len([]rune(input.Message)) > 4000 || input.BaseVersion < 1 {
		return AgentRun{}, fmt.Errorf("%w: message must contain 1 to 4000 characters and base_version must be positive", ErrInvalid)
	}
	routed := routeIntent(input.Message)
	if input.Mode != "" {
		if !validMode(input.Mode) {
			return AgentRun{}, fmt.Errorf("%w: unsupported workbook agent mode", ErrInvalid)
		}
		if routed.Skill == "rollback" && input.Mode == ModeAgent {
			routed.Mode = input.Mode
		} else {
			routed = routedIntent{Mode: input.Mode, Skill: skillForMode(input.Mode)}
		}
	}
	duplicateInput := PlanInput{WorkbookID: input.WorkbookID, SheetID: input.SheetID, Range: input.Selection, Request: input.Message, Mode: routed.Mode, BaseVersion: input.BaseVersion}
	if existing, err := s.findByIdempotency(ctx, input.ActorID, input.IdempotencyKey); err == nil {
		if !samePlanRequest(existing, duplicateInput) || existing.ConversationID == "" {
			return AgentRun{}, fmt.Errorf("%w: idempotency_key was already used for a different agent message", ErrInvalid)
		}
		return s.GetRun(ctx, existing.ID, input.ActorID)
	} else if !errors.Is(err, ErrNotFound) {
		return AgentRun{}, err
	}
	if routed.Skill == "rollback" {
		return s.rollbackLatestByMessage(ctx, input)
	}
	contextView, err := workbook.BuildAgentContext(ctx, s.workbooks, input.WorkbookID, input.SheetID, input.Selection)
	if err != nil {
		return AgentRun{}, err
	}
	if contextView.WorkbookVersion != input.BaseVersion {
		return AgentRun{}, workbook.ErrVersionConflict
	}
	conversationID, err := s.ensureConversation(ctx, input)
	if err != nil {
		return AgentRun{}, err
	}
	action, err := s.Plan(ctx, PlanInput{
		WorkbookID: input.WorkbookID, SheetID: input.SheetID, Range: input.Selection,
		Request: input.Message, Mode: routed.Mode, BaseVersion: input.BaseVersion,
		IdempotencyKey: input.IdempotencyKey, ClientID: input.ClientID, ActorID: input.ActorID,
		ConversationID: conversationID, Context: &contextView,
	})
	if err != nil {
		return AgentRun{}, err
	}
	if existing, loadErr := s.GetRun(ctx, action.ID, input.ActorID); loadErr == nil {
		return existing, nil
	} else if !errors.Is(loadErr, ErrNotFound) {
		return AgentRun{}, loadErr
	}
	if err := s.persistAgentRun(ctx, conversationID, routed, contextView, action); err != nil {
		return AgentRun{}, err
	}
	return s.GetRun(ctx, action.ID, input.ActorID)
}

func (s *Service) rollbackLatestByMessage(ctx context.Context, input AgentMessageInput) (AgentRun, error) {
	var duplicateRunID string
	err := s.pool.QueryRow(ctx, `SELECT run_id::text FROM agent_audit_logs WHERE actor_id=$1 AND event_type='rollback_requested' AND payload->>'workbook_id'=$2 AND payload->>'idempotency_key'=$3 ORDER BY id DESC LIMIT 1`, input.ActorID, input.WorkbookID, input.IdempotencyKey).Scan(&duplicateRunID)
	if err == nil {
		return s.GetRun(ctx, duplicateRunID, input.ActorID)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, err
	}
	book, err := s.workbooks.GetWorkbook(ctx, input.WorkbookID)
	if err != nil {
		return AgentRun{}, err
	}
	if book.Version != input.BaseVersion {
		return AgentRun{}, workbook.ErrVersionConflict
	}
	var changeSetID, runID, conversationID string
	var revision int64
	err = s.pool.QueryRow(ctx, `SELECT c.id::text,c.run_id::text,r.conversation_id::text,a.revision FROM change_sets c JOIN agent_runs r ON r.id=c.run_id JOIN ai_actions a ON a.id=c.run_id WHERE c.workbook_id=$1 AND lower(c.actor_id)=lower($2) AND c.status IN ('completed','failed') AND a.status='applied' ORDER BY c.applied_at DESC NULLS LAST,c.created_at DESC LIMIT 1`, input.WorkbookID, input.ActorID).Scan(&changeSetID, &runID, &conversationID, &revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, fmt.Errorf("%w: 되돌릴 최근 Workbook Agent 작업이 없습니다", ErrNotFound)
	}
	if err != nil {
		return AgentRun{}, err
	}
	if _, err := s.RollbackChangeSet(ctx, changeSetID, ApprovalInput{ActorID: input.ActorID, ClientID: input.ClientID, IdempotencyKey: input.IdempotencyKey, ExpectedRevision: revision}); err != nil {
		return AgentRun{}, err
	}
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `INSERT INTO ai_messages(id,conversation_id,agent_run_id,actor_id,role,content,created_at) VALUES($1,$2,$3,$4,'user',$5,$6),($7,$2,$3,$4,'assistant',$8,$9)`, identity.New(), conversationID, runID, input.ActorID, input.Message, now, identity.New(), "최근 Workbook Agent ChangeSet을 전체 원복했습니다.", now.Add(time.Microsecond)); err != nil {
		return AgentRun{}, err
	}
	payload, _ := json.Marshal(map[string]string{"workbook_id": input.WorkbookID, "change_set_id": changeSetID, "idempotency_key": input.IdempotencyKey})
	if _, err := tx.Exec(ctx, `INSERT INTO agent_audit_logs(run_id,actor_id,event_type,payload,created_at) VALUES($1,$2,'rollback_requested',$3,$4)`, runID, input.ActorID, payload, now); err != nil {
		return AgentRun{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_conversations SET updated_at=$2 WHERE id=$1`, conversationID, now); err != nil {
		return AgentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentRun{}, err
	}
	return s.GetRun(ctx, runID, input.ActorID)
}

func (s *Service) ensureConversation(ctx context.Context, input AgentMessageInput) (string, error) {
	if input.ConversationID != "" {
		var id string
		err := s.pool.QueryRow(ctx, `SELECT id::text FROM ai_conversations WHERE id=$1 AND workbook_id=$2 AND lower(actor_id)=lower($3)`, input.ConversationID, input.WorkbookID, input.ActorID).Scan(&id)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return id, err
	}
	id := identity.New()
	title := trimLength(input.Message, 120)
	_, err := s.pool.Exec(ctx, `INSERT INTO ai_conversations(id,workbook_id,actor_id,title,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$5)`, id, input.WorkbookID, input.ActorID, title, s.now())
	return id, err
}

func (s *Service) persistAgentRun(ctx context.Context, conversationID string, routed routedIntent, contextView workbook.AgentContext, action Action) error {
	now := s.now()
	state, planStatus := AgentWaitingApproval, "waiting_approval"
	var completedAt any
	if IsReadOnlyMode(action.Mode) {
		state, planStatus, completedAt = AgentCompleted, "completed", now
	}
	contextJSON, _ := json.Marshal(contextView)
	validationJSON, _ := json.Marshal(action.Validation)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `INSERT INTO agent_runs(id,conversation_id,workbook_id,sheet_id,actor_id,selected_range,intent,state,goal,risk,context,validation,started_at,completed_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$13) ON CONFLICT(id) DO NOTHING`, action.ID, conversationID, action.WorkbookID, action.SheetID, action.ActorID, action.Range, routed.Skill, state, action.Summary, action.Risk, contextJSON, validationJSON, now, completedAt)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		return tx.Commit(ctx)
	}
	contextID := identity.New()
	if _, err := tx.Exec(ctx, `INSERT INTO workbook_contexts(id,workbook_id,sheet_id,workbook_version,selected_range,context,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, contextID, action.WorkbookID, action.SheetID, action.BaseVersion, action.Range, contextJSON, now); err != nil {
		return err
	}
	userMessageID, assistantMessageID := identity.New(), identity.New()
	if _, err := tx.Exec(ctx, `INSERT INTO ai_messages(id,conversation_id,agent_run_id,actor_id,role,content,created_at) VALUES($1,$2,$3,$4,'user',$5,$6),($7,$2,$3,$4,'assistant',$8,$9)`, userMessageID, conversationID, action.ID, action.ActorID, action.Request, now, assistantMessageID, strings.TrimSpace(action.Summary+"\n\n"+action.Explanation), now.Add(time.Microsecond)); err != nil {
		return err
	}
	planID := identity.New()
	if _, err := tx.Exec(ctx, `INSERT INTO agent_plans(id,run_id,goal,risk,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$6)`, planID, action.ID, action.Summary, action.Risk, planStatus, now); err != nil {
		return err
	}
	for index := range action.Plan {
		step := &action.Plan[index]
		step.ID = identity.New()
		switch step.ToolName {
		case "get_workbook":
			step.Result, _ = json.Marshal(map[string]any{"workbook_id": contextView.WorkbookID, "title": contextView.WorkbookTitle, "version": contextView.WorkbookVersion, "sheets": contextView.Sheets})
		case "get_selection":
			step.Result, _ = json.Marshal(map[string]any{"selection": contextView.SelectedRange, "semantic_map": contextView.SemanticMap})
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_steps(id,plan_id,position,tool_name,description,status,risk,arguments,result,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)`, step.ID, planID, step.Position, step.ToolName, step.Description, step.Status, step.Risk, jsonOrObject(step.Arguments), jsonOrObject(step.Result), now); err != nil {
			return err
		}
		if !actionHasTool(action, step.ToolName) {
			var completedAt any
			if step.Status == "completed" {
				completedAt = now
			}
			if _, err := tx.Exec(ctx, `INSERT INTO agent_tool_calls(id,run_id,step_id,tool_name,arguments,result,status,idempotency_key,created_at,completed_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, identity.New(), action.ID, step.ID, step.ToolName, jsonOrObject(step.Arguments), jsonOrObject(step.Result), step.Status, fmt.Sprintf("%s:step:%d", action.IdempotencyKey, step.Position), now, completedAt); err != nil {
				return err
			}
		}
	}
	for index := range action.ToolCalls {
		tool := &action.ToolCalls[index]
		tool.ID = identity.New()
		var stepID any
		for _, step := range action.Plan {
			if step.ToolName == tool.Name {
				stepID = step.ID
				break
			}
		}
		if _, err := tx.Exec(ctx, `INSERT INTO agent_tool_calls(id,run_id,step_id,tool_name,arguments,result,status,idempotency_key,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)`, tool.ID, action.ID, stepID, tool.Name, jsonOrObject(tool.Arguments), jsonOrObject(tool.Result), tool.Status, tool.IdempotencyKey, now); err != nil {
			return err
		}
	}
	if !IsReadOnlyMode(action.Mode) {
		changeSetID := identity.New()
		if _, err := tx.Exec(ctx, `INSERT INTO change_sets(id,run_id,workbook_id,sheet_id,actor_id,status,risk,base_version,created_at) VALUES($1,$2,$3,$4,$5,'planned',$6,$7,$8)`, changeSetID, action.ID, action.WorkbookID, action.SheetID, action.ActorID, action.Risk, action.BaseVersion, now); err != nil {
			return err
		}
		position := 1
		for _, change := range action.Changes {
			before, _ := json.Marshal(change.Before)
			after, _ := json.Marshal(change.After)
			if _, err := tx.Exec(ctx, `INSERT INTO change_operations(id,change_set_id,position,operation_type,sheet_id,selected_range,before_value,after_value,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, identity.New(), changeSetID, position, approvalToolName(action.Mode), action.SheetID, change.Address, before, after, []byte(`{}`), now); err != nil {
				return err
			}
			position++
		}
		for _, tool := range action.ToolCalls {
			if _, err := tx.Exec(ctx, `INSERT INTO change_operations(id,change_set_id,position,operation_type,sheet_id,selected_range,metadata,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8)`, identity.New(), changeSetID, position, tool.Name, action.SheetID, action.Range, jsonOrObject(tool.Arguments), now); err != nil {
				return err
			}
			position++
		}
	}
	payload, _ := json.Marshal(map[string]any{"intent": routed.Skill, "mode": action.Mode, "risk": action.Risk, "selection": action.Range, "steps": len(action.Plan), "changes": len(action.Changes), "tool_calls": len(action.ToolCalls)})
	if _, err := tx.Exec(ctx, `INSERT INTO agent_audit_logs(run_id,actor_id,event_type,payload,created_at) VALUES($1,$2,'planned',$3,$4)`, action.ID, action.ActorID, payload, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_conversations SET updated_at=$2 WHERE id=$1`, conversationID, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) GetRun(ctx context.Context, runID, actorID string) (AgentRun, error) {
	var run AgentRun
	var contextJSON, validationJSON []byte
	err := s.pool.QueryRow(ctx, `SELECT id::text,conversation_id::text,workbook_id::text,sheet_id::text,selected_range,intent,state,goal,risk,context,validation,started_at,completed_at,updated_at FROM agent_runs WHERE id=$1 AND lower(actor_id)=lower($2)`, runID, actorID).Scan(&run.ID, &run.ConversationID, &run.WorkbookID, &run.SheetID, &run.Selection, &run.Intent, &run.State, &run.Goal, &run.Risk, &contextJSON, &validationJSON, &run.StartedAt, &run.CompletedAt, &run.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, ErrNotFound
	}
	if err != nil {
		return AgentRun{}, err
	}
	if err := json.Unmarshal(contextJSON, &run.Context); err != nil {
		return AgentRun{}, err
	}
	if err := json.Unmarshal(validationJSON, &run.Validation); err != nil {
		return AgentRun{}, err
	}
	run.Action, err = s.Get(ctx, run.ID, actorID)
	if err != nil {
		return AgentRun{}, err
	}
	run.Plan, err = s.GetRunPlan(ctx, run.ID, actorID)
	if err != nil {
		return AgentRun{}, err
	}
	_ = s.pool.QueryRow(ctx, `SELECT id::text FROM change_sets WHERE run_id=$1`, run.ID).Scan(&run.ChangeSetID)
	run.Messages, err = s.listConversationMessages(ctx, run.ConversationID)
	return run, err
}

func (s *Service) GetRunPlan(ctx context.Context, runID, actorID string) (AgentPlan, error) {
	var plan AgentPlan
	err := s.pool.QueryRow(ctx, `SELECT p.id::text,p.run_id::text,p.goal,p.risk,p.status,p.created_at,p.updated_at FROM agent_plans p JOIN agent_runs r ON r.id=p.run_id WHERE p.run_id=$1 AND lower(r.actor_id)=lower($2)`, runID, actorID).Scan(&plan.ID, &plan.RunID, &plan.Goal, &plan.Risk, &plan.Status, &plan.CreatedAt, &plan.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentPlan{}, ErrNotFound
	}
	if err != nil {
		return AgentPlan{}, err
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text,position,tool_name,description,status,risk,arguments,result FROM agent_steps WHERE plan_id=$1 ORDER BY position`, plan.ID)
	if err != nil {
		return AgentPlan{}, err
	}
	defer rows.Close()
	plan.Steps = []PlanStep{}
	for rows.Next() {
		var step PlanStep
		if err := rows.Scan(&step.ID, &step.Position, &step.ToolName, &step.Description, &step.Status, &step.Risk, &step.Arguments, &step.Result); err != nil {
			return AgentPlan{}, err
		}
		plan.Steps = append(plan.Steps, step)
	}
	return plan, rows.Err()
}

func (s *Service) ListRuns(ctx context.Context, workbookID, actorID string, limit int) ([]AgentRun, error) {
	if limit < 1 || limit > 50 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT id::text FROM agent_runs WHERE workbook_id=$1 AND lower(actor_id)=lower($2) ORDER BY started_at DESC,id DESC LIMIT $3`, workbookID, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	items := make([]AgentRun, 0, len(ids))
	for _, id := range ids {
		item, err := s.GetRun(ctx, id, actorID)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, nil
}

func (s *Service) RunForChangeSet(ctx context.Context, changeSetID, actorID string) (AgentRun, error) {
	var runID string
	err := s.pool.QueryRow(ctx, `SELECT run_id::text FROM change_sets WHERE id=$1 AND lower(actor_id)=lower($2)`, changeSetID, actorID).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRun{}, ErrNotFound
	}
	if err != nil {
		return AgentRun{}, err
	}
	return s.GetRun(ctx, runID, actorID)
}

func (s *Service) listConversationMessages(ctx context.Context, conversationID string) ([]ConversationMessage, error) {
	rows, err := s.pool.Query(ctx, `SELECT id::text,conversation_id::text,coalesce(agent_run_id::text,''),role,content,created_at FROM ai_messages WHERE conversation_id=$1 ORDER BY created_at,id`, conversationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []ConversationMessage{}
	for rows.Next() {
		var item ConversationMessage
		if err := rows.Scan(&item.ID, &item.ConversationID, &item.AgentRunID, &item.Role, &item.Content, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func normalizeAgentMessage(input AgentMessageInput) AgentMessageInput {
	input.WorkbookID = strings.TrimSpace(input.WorkbookID)
	input.SheetID = strings.TrimSpace(input.SheetID)
	input.Selection = strings.ToUpper(strings.TrimSpace(input.Selection))
	input.Message = strings.TrimSpace(input.Message)
	input.Mode = strings.ToLower(strings.TrimSpace(input.Mode))
	input.ConversationID = strings.TrimSpace(input.ConversationID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	return input
}

func jsonOrObject(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return json.RawMessage(`{}`)
	}
	return value
}

func actionHasTool(action Action, name string) bool {
	for _, tool := range action.ToolCalls {
		if tool.Name == name {
			return true
		}
	}
	return false
}

func (s *Service) ApproveRun(ctx context.Context, runID string, input ApprovalInput) (AgentExecutionResult, error) {
	input = normalizeApprovalInput(input)
	if err := validateExecutionInput(input); err != nil {
		return AgentExecutionResult{}, err
	}
	action, err := s.Get(ctx, runID, input.ActorID)
	if err != nil {
		return AgentExecutionResult{}, err
	}
	if IsReadOnlyMode(action.Mode) {
		return AgentExecutionResult{}, fmt.Errorf("%w: read-only agent runs cannot be approved", ErrInvalid)
	}
	if err := s.setAgentState(ctx, runID, AgentExecuting, "executing"); err != nil {
		return AgentExecutionResult{}, err
	}
	var operation *workbook.MutationResult
	var changes []workbook.CellInput
	if len(action.Changes) > 0 {
		result, err := s.Approve(ctx, runID, input)
		if err != nil {
			_ = s.setAgentState(ctx, runID, AgentFailed, "failed")
			return AgentExecutionResult{}, err
		}
		action = result.Action
		operation, changes = &result.Operation, result.Changes
	} else if err := s.claimToolOnlyApproval(ctx, action, input); err != nil {
		_ = s.setAgentState(ctx, runID, AgentFailed, "failed")
		return AgentExecutionResult{}, err
	}
	if err := s.executeAgentTools(ctx, &action, input.ActorID); err != nil {
		if operation != nil {
			fresh, loadErr := s.Get(ctx, runID, input.ActorID)
			if loadErr == nil {
				_, _ = s.Undo(ctx, runID, ApprovalInput{ActorID: input.ActorID, ClientID: input.ClientID, IdempotencyKey: input.IdempotencyKey + ":compensate", ExpectedRevision: fresh.Revision})
			}
		}
		_ = s.setAgentState(ctx, runID, AgentFailed, "failed")
		return AgentExecutionResult{}, err
	}
	if len(action.Changes) == 0 {
		if err := s.completeToolOnlyApproval(ctx, action, input); err != nil {
			return AgentExecutionResult{}, err
		}
	}
	if err := s.setAgentState(ctx, runID, AgentValidating, "validating"); err != nil {
		return AgentExecutionResult{}, err
	}
	validation := validateAgentExecution(action, operation)
	if err := s.completeAgentValidation(ctx, runID, validation, operation); err != nil {
		return AgentExecutionResult{}, err
	}
	run, err := s.GetRun(ctx, runID, input.ActorID)
	if err != nil {
		return AgentExecutionResult{}, err
	}
	return AgentExecutionResult{Run: run, Operation: operation, Changes: changes}, nil
}

func (s *Service) claimToolOnlyApproval(ctx context.Context, action Action, input ApprovalInput) error {
	if action.Status == StatusApplying && action.approvalIdempotencyKey == input.IdempotencyKey {
		return nil
	}
	if action.Status == StatusApplied && action.approvalIdempotencyKey == input.IdempotencyKey {
		return nil
	}
	if action.Status != StatusPlanned || action.Revision != input.ExpectedRevision {
		return ErrRevision
	}
	command, err := s.pool.Exec(ctx, `UPDATE ai_actions SET status=$3,revision=revision+1,approval_idempotency_key=$4,updated_at=$5 WHERE id=$1 AND actor_id=$2 AND status=$6 AND revision=$7`, action.ID, action.ActorID, StatusApplying, input.IdempotencyKey, s.now(), StatusPlanned, input.ExpectedRevision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrRevision
	}
	return nil
}

func (s *Service) executeAgentTools(ctx context.Context, action *Action, actorID string) error {
	for index := range action.ToolCalls {
		tool := &action.ToolCalls[index]
		if tool.Status == "completed" {
			continue
		}
		started := time.Now()
		switch tool.Name {
		case "create_chart":
			var arguments createChartArguments
			if json.Unmarshal(tool.Arguments, &arguments) != nil {
				return fmt.Errorf("%w: stored create_chart arguments are invalid", ErrInvalid)
			}
			chart, err := s.workbooks.CreateChart(ctx, action.WorkbookID, actorID, workbook.CreateChartInput{
				IdempotencyKey: tool.IdempotencyKey, SheetID: arguments.SheetID, SourceSheetID: arguments.SourceSheetID,
				Type: arguments.Type, Title: arguments.Title, SourceRange: arguments.SourceRange,
				FirstRowHeaders: arguments.FirstRowHeaders, FirstColumnLabels: arguments.FirstColumnLabels,
				LegendPosition: arguments.LegendPosition, XAxisTitle: arguments.XAxisTitle, YAxisTitle: arguments.YAxisTitle,
			})
			if err != nil {
				tool.Status = "failed"
				tool.Result, _ = json.Marshal(map[string]string{"error": err.Error()})
				_ = s.saveToolCall(ctx, action.ID, *tool)
				return err
			}
			tool.Result, _ = json.Marshal(chart)
			tool.Status = "completed"
		case "create_report_sheet":
			if err := s.executeReportSheetTool(ctx, action, tool, actorID); err != nil {
				tool.Status = "failed"
				tool.Result, _ = json.Marshal(map[string]string{"error": err.Error()})
				_ = s.saveToolCall(ctx, action.ID, *tool)
				return err
			}
		default:
			return fmt.Errorf("%w: unsupported stored workbook tool %q", ErrInvalid, tool.Name)
		}
		tool.DurationMS = time.Since(started).Milliseconds()
		if err := s.saveToolCall(ctx, action.ID, *tool); err != nil {
			return err
		}
	}
	encoded, _ := json.Marshal(action.ToolCalls)
	_, err := s.pool.Exec(ctx, `UPDATE ai_actions SET tool_calls=$2,updated_at=$3 WHERE id=$1`, action.ID, encoded, s.now())
	return err
}

func (s *Service) executeReportSheetTool(ctx context.Context, action *Action, tool *ToolCall, actorID string) error {
	var arguments createReportSheetArguments
	if json.Unmarshal(tool.Arguments, &arguments) != nil {
		return fmt.Errorf("%w: stored create_report_sheet arguments are invalid", ErrInvalid)
	}
	sheet, err := s.workbooks.CreateSheet(ctx, action.WorkbookID, workbook.CreateSheetInput{Name: arguments.Name})
	if err != nil {
		return err
	}
	compensate := func() { _ = s.workbooks.DeleteSheet(context.WithoutCancel(ctx), sheet.ID) }
	book, err := s.workbooks.GetWorkbook(ctx, action.WorkbookID)
	if err != nil {
		compensate()
		return err
	}
	cells := make([]workbook.CellInput, 0, len(arguments.Cells))
	for _, candidate := range arguments.Cells {
		cell := workbook.CellInput{Row: candidate.Row, Column: candidate.Column}
		switch {
		case strings.TrimSpace(candidate.Formula) != "":
			cell.Formula = strings.TrimSpace(candidate.Formula)
		case len(candidate.Style) > 0:
			cell.Style = cloneRaw(candidate.Style)
		case !candidate.Clear:
			cell.Value = cloneRaw(candidate.Value)
		}
		cells = append(cells, cell)
	}
	operation, err := s.workbooks.ApplyCells(ctx, workbook.CellMutation{
		SheetID: sheet.ID, ActorID: actorID, ClientID: action.ClientID, BaseVersion: book.Version,
		IdempotencyKey: executionKey(action.ID, "report", tool.IdempotencyKey), Cells: cells,
		OperationType: "ai.agent.report", RequireExactVersion: true,
	})
	if err != nil {
		compensate()
		return err
	}
	result := createReportSheetResult{Sheet: sheet, CellOperation: operation}
	if arguments.Chart != nil {
		chart, err := s.workbooks.CreateChart(ctx, action.WorkbookID, actorID, workbook.CreateChartInput{
			IdempotencyKey: tool.IdempotencyKey + ":chart", SheetID: sheet.ID, SourceSheetID: sheet.ID,
			Type: arguments.Chart.Type, Title: arguments.Chart.Title, SourceRange: arguments.Chart.SourceRange,
			FirstRowHeaders: arguments.Chart.FirstRowHeaders, FirstColumnLabels: arguments.Chart.FirstColumnLabels,
			LegendPosition: arguments.Chart.LegendPosition, XAxisTitle: arguments.Chart.XAxisTitle, YAxisTitle: arguments.Chart.YAxisTitle,
		})
		if err != nil {
			compensate()
			return err
		}
		result.Chart = &chart
	}
	tool.Result, _ = json.Marshal(result)
	tool.Status = "completed"
	return nil
}

func (s *Service) saveToolCall(ctx context.Context, runID string, tool ToolCall) error {
	completed := s.now()
	_, err := s.pool.Exec(ctx, `UPDATE agent_tool_calls SET result=$3,status=$4,duration_ms=$5,completed_at=$6 WHERE run_id=$1 AND idempotency_key=$2`, runID, tool.IdempotencyKey, jsonOrObject(tool.Result), tool.Status, tool.DurationMS, completed)
	return err
}

func (s *Service) completeToolOnlyApproval(ctx context.Context, action Action, input ApprovalInput) error {
	now := s.now()
	_, err := s.pool.Exec(ctx, `UPDATE ai_actions SET status=$3,approved_at=$4,updated_at=$4 WHERE id=$1 AND actor_id=$2 AND status IN ($5,$3) AND approval_idempotency_key=$6`, action.ID, action.ActorID, StatusApplied, now, StatusApplying, input.IdempotencyKey)
	return err
}

func validateAgentExecution(action Action, operation *workbook.MutationResult) ValidationResult {
	now := time.Now().UTC()
	checks := []ValidationCheck{{Name: "permission", Passed: true, Message: "승인한 사용자의 워크북 권한으로 실행했습니다."}}
	if len(action.Changes) > 0 {
		applied := operation != nil && operation.AppliedCells == len(action.Changes)
		checks = append(checks, ValidationCheck{Name: "changed_cells", Passed: applied, Message: fmt.Sprintf("예상 %d셀, 적용 %d셀", len(action.Changes), appliedCellCount(operation))})
		formulaOK := operation != nil && len(operation.FormulaErrors) == 0
		checks = append(checks, ValidationCheck{Name: "formula", Passed: formulaOK, Message: fmt.Sprintf("수식 오류 %d개", formulaErrorCount(operation))})
	}
	for _, tool := range action.ToolCalls {
		switch tool.Name {
		case "create_chart":
			var chart workbook.Chart
			passed := tool.Status == "completed" && json.Unmarshal(tool.Result, &chart) == nil && chart.ID != ""
			checks = append(checks, ValidationCheck{Name: "chart_source", Passed: passed, Message: "차트 생성과 소스 범위를 확인했습니다."})
		case "create_report_sheet":
			var arguments createReportSheetArguments
			var result createReportSheetResult
			decoded := json.Unmarshal(tool.Arguments, &arguments) == nil && json.Unmarshal(tool.Result, &result) == nil
			cellsOK := decoded && result.Sheet.ID != "" && result.CellOperation.AppliedCells == len(arguments.Cells) && len(result.CellOperation.FormulaErrors) == 0
			chartOK := decoded && (arguments.Chart == nil || result.Chart != nil && result.Chart.ID != "")
			checks = append(checks,
				ValidationCheck{Name: "report_sheet", Passed: cellsOK, Message: fmt.Sprintf("보고서 시트에 예상 %d셀, 적용 %d셀", len(arguments.Cells), result.CellOperation.AppliedCells)},
				ValidationCheck{Name: "report_chart", Passed: chartOK, Message: "보고서 차트 생성을 확인했습니다."},
			)
		default:
			checks = append(checks, ValidationCheck{Name: "tool_allowlist", Passed: false, Message: "허용되지 않은 도구가 실행 결과에 포함되었습니다."})
		}
	}
	passed := true
	for _, check := range checks {
		passed = passed && check.Passed
	}
	return ValidationResult{Passed: passed, Checks: checks, CheckedAt: &now}
}

func appliedCellCount(operation *workbook.MutationResult) int {
	if operation == nil {
		return 0
	}
	return operation.AppliedCells
}

func formulaErrorCount(operation *workbook.MutationResult) int {
	if operation == nil {
		return 0
	}
	return len(operation.FormulaErrors)
}

func (s *Service) completeAgentValidation(ctx context.Context, runID string, validation ValidationResult, operation *workbook.MutationResult) error {
	encoded, _ := json.Marshal(validation)
	now := s.now()
	state, status := AgentCompleted, "completed"
	if !validation.Passed {
		state, status = AgentFailed, "failed"
	}
	var workbookID string
	if err := s.pool.QueryRow(ctx, `SELECT workbook_id::text FROM agent_runs WHERE id=$1`, runID).Scan(&workbookID); err != nil {
		return err
	}
	book, err := s.workbooks.GetWorkbook(ctx, workbookID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `UPDATE agent_runs SET state=$2,validation=$3,completed_at=$4,updated_at=$4 WHERE id=$1`, runID, state, encoded, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_plans SET status=$2,updated_at=$3 WHERE run_id=$1`, runID, status, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_steps SET status=CASE WHEN tool_name='validate_changeset' THEN $2 WHEN status IN ('waiting_approval','pending') THEN 'completed' ELSE status END,result=CASE WHEN tool_name='validate_changeset' THEN $3 ELSE result END,updated_at=$4 WHERE plan_id=(SELECT id FROM agent_plans WHERE run_id=$1)`, runID, status, encoded, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_tool_calls SET status=CASE WHEN tool_name='validate_changeset' THEN $2 WHEN status IN ('waiting_approval','pending','planned') THEN 'completed' ELSE status END,result=CASE WHEN tool_name='validate_changeset' THEN $3 ELSE result END,completed_at=coalesce(completed_at,$4) WHERE run_id=$1`, runID, status, encoded, now); err != nil {
		return err
	}
	var operationID any
	if operation != nil && operation.OperationID != "" {
		operationID = operation.OperationID
	}
	if _, err := tx.Exec(ctx, `UPDATE change_sets SET status=$2,operation_id=$3,applied_version=$4,applied_at=$5 WHERE run_id=$1`, runID, status, operationID, book.Version, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE ai_actions SET validation=$2,plan=(SELECT coalesce(jsonb_agg(jsonb_build_object('id',s.id::text,'position',s.position,'tool',s.tool_name,'description',s.description,'status',s.status,'risk',s.risk,'arguments',s.arguments,'result',s.result) ORDER BY s.position),'[]'::jsonb) FROM agent_steps s JOIN agent_plans p ON p.id=s.plan_id WHERE p.run_id=$1),updated_at=$3 WHERE id=$1`, runID, encoded, now); err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{"passed": validation.Passed, "checks": validation.Checks})
	if _, err := tx.Exec(ctx, `INSERT INTO agent_audit_logs(run_id,actor_id,event_type,payload,created_at) SELECT id,actor_id,'validated',$2,$3 FROM agent_runs WHERE id=$1`, runID, payload, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) setAgentState(ctx context.Context, runID, state, planStatus string) error {
	now := s.now()
	_, err := s.pool.Exec(ctx, `UPDATE agent_runs SET state=$2,updated_at=$3 WHERE id=$1`, runID, state, now)
	if err == nil && planStatus != "" {
		_, err = s.pool.Exec(ctx, `UPDATE agent_plans SET status=$2,updated_at=$3 WHERE run_id=$1`, runID, planStatus, now)
	}
	return err
}

func (s *Service) CancelRun(ctx context.Context, runID string, input ApprovalInput) (AgentRun, error) {
	input = normalizeApprovalInput(input)
	if err := validateExecutionInput(input); err != nil {
		return AgentRun{}, err
	}
	action, err := s.Get(ctx, runID, input.ActorID)
	if err != nil {
		return AgentRun{}, err
	}
	if action.Status == StatusCancelled {
		return s.GetRun(ctx, runID, input.ActorID)
	}
	if action.Status != StatusPlanned || action.Revision != input.ExpectedRevision {
		return AgentRun{}, ErrRevision
	}
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AgentRun{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE ai_actions SET status=$3,revision=revision+1,updated_at=$4 WHERE id=$1 AND actor_id=$2 AND status=$5 AND revision=$6`, runID, input.ActorID, StatusCancelled, now, StatusPlanned, input.ExpectedRevision)
	if err != nil || command.RowsAffected() != 1 {
		if err != nil {
			return AgentRun{}, err
		}
		return AgentRun{}, ErrRevision
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_runs SET state=$2,completed_at=$3,updated_at=$3 WHERE id=$1`, runID, AgentCancelled, now); err != nil {
		return AgentRun{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_plans SET status='cancelled',updated_at=$2 WHERE run_id=$1`, runID, now); err != nil {
		return AgentRun{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_steps SET status=CASE WHEN status IN ('waiting_approval','pending') THEN 'cancelled' ELSE status END,updated_at=$2 WHERE plan_id=(SELECT id FROM agent_plans WHERE run_id=$1)`, runID, now); err != nil {
		return AgentRun{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE agent_tool_calls SET status=CASE WHEN status IN ('waiting_approval','pending','planned') THEN 'cancelled' ELSE status END,completed_at=coalesce(completed_at,$2) WHERE run_id=$1`, runID, now); err != nil {
		return AgentRun{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE change_sets SET status='cancelled' WHERE run_id=$1`, runID); err != nil {
		return AgentRun{}, err
	}
	payload, _ := json.Marshal(map[string]string{"idempotency_key": input.IdempotencyKey})
	if _, err := tx.Exec(ctx, `INSERT INTO agent_audit_logs(run_id,actor_id,event_type,payload,created_at) VALUES($1,$2,'cancelled',$3,$4)`, runID, input.ActorID, payload, now); err != nil {
		return AgentRun{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return AgentRun{}, err
	}
	return s.GetRun(ctx, runID, input.ActorID)
}

func (s *Service) RollbackChangeSet(ctx context.Context, changeSetID string, input ApprovalInput) (AgentExecutionResult, error) {
	input = normalizeApprovalInput(input)
	if err := validateExecutionInput(input); err != nil {
		return AgentExecutionResult{}, err
	}
	var runID string
	var appliedVersion int64
	err := s.pool.QueryRow(ctx, `SELECT run_id::text,coalesce(applied_version,0) FROM change_sets WHERE id=$1 AND lower(actor_id)=lower($2)`, changeSetID, input.ActorID).Scan(&runID, &appliedVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentExecutionResult{}, ErrNotFound
	}
	if err != nil {
		return AgentExecutionResult{}, err
	}
	action, err := s.Get(ctx, runID, input.ActorID)
	if err != nil {
		return AgentExecutionResult{}, err
	}
	if action.Status == StatusUndone && action.undoIdempotencyKey == input.IdempotencyKey {
		run, err := s.GetRun(ctx, runID, input.ActorID)
		return AgentExecutionResult{Run: run}, err
	}
	if action.Status != StatusApplied || action.Revision != input.ExpectedRevision {
		return AgentExecutionResult{}, ErrRevision
	}
	book, err := s.workbooks.GetWorkbook(ctx, action.WorkbookID)
	if err != nil {
		return AgentExecutionResult{}, err
	}
	if appliedVersion > 0 && book.Version != appliedVersion {
		return AgentExecutionResult{}, workbook.ErrVersionConflict
	}
	for index := len(action.ToolCalls) - 1; index >= 0; index-- {
		tool := action.ToolCalls[index]
		if tool.Status != "completed" {
			continue
		}
		switch tool.Name {
		case "create_chart":
			var chart workbook.Chart
			if json.Unmarshal(tool.Result, &chart) == nil && chart.ID != "" {
				revision := chart.Revision
				if err := s.workbooks.DeleteChart(ctx, chart.ID, input.ActorID, &revision); err != nil && !errors.Is(err, workbook.ErrNotFound) {
					return AgentExecutionResult{}, err
				}
			}
		case "create_report_sheet":
			var result createReportSheetResult
			if json.Unmarshal(tool.Result, &result) == nil && result.Sheet.ID != "" {
				if err := s.workbooks.DeleteSheet(ctx, result.Sheet.ID); err != nil && !errors.Is(err, workbook.ErrNotFound) {
					return AgentExecutionResult{}, err
				}
			}
		}
	}
	var operation *workbook.MutationResult
	if action.OperationID != "" {
		result, err := s.Undo(ctx, runID, input)
		if err != nil {
			return AgentExecutionResult{}, err
		}
		operation = &result.Operation
		action = result.Action
	} else {
		now := s.now()
		command, err := s.pool.Exec(ctx, `UPDATE ai_actions SET status=$3,revision=revision+1,undo_idempotency_key=$4,undone_at=$5,updated_at=$5 WHERE id=$1 AND actor_id=$2 AND status=$6 AND revision=$7`, runID, input.ActorID, StatusUndone, input.IdempotencyKey, now, StatusApplied, input.ExpectedRevision)
		if err != nil || command.RowsAffected() != 1 {
			if err != nil {
				return AgentExecutionResult{}, err
			}
			return AgentExecutionResult{}, ErrRevision
		}
	}
	now := s.now()
	var undoID any
	if operation != nil && operation.OperationID != "" {
		undoID = operation.OperationID
	}
	if _, err := s.pool.Exec(ctx, `UPDATE change_sets SET status='rolled_back',undo_operation_id=$2,rolled_back_at=$3 WHERE id=$1`, changeSetID, undoID, now); err != nil {
		return AgentExecutionResult{}, err
	}
	if _, err := s.pool.Exec(ctx, `UPDATE agent_runs SET state=$2,completed_at=$3,updated_at=$3 WHERE id=$1`, runID, AgentCompleted, now); err != nil {
		return AgentExecutionResult{}, err
	}
	if _, err := s.pool.Exec(ctx, `INSERT INTO agent_audit_logs(run_id,actor_id,event_type,payload,created_at) VALUES($1,$2,'rolled_back',$3,$4)`, runID, input.ActorID, []byte(`{}`), now); err != nil {
		return AgentExecutionResult{}, err
	}
	run, err := s.GetRun(ctx, runID, input.ActorID)
	if err != nil {
		return AgentExecutionResult{}, err
	}
	return AgentExecutionResult{Run: run, Operation: operation}, nil
}
