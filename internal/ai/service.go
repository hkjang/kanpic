package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const actionColumns = `id::text,workbook_id::text,sheet_id::text,actor_id,client_id,idempotency_key,mode,selected_range,request,status,base_version,model,summary,explanation,changes,findings,input_cell_count,revision,approval_idempotency_key,coalesce(operation_id::text,''),operation_result,undo_idempotency_key,coalesce(undo_operation_id::text,''),undo_result,error_message,approved_at,undone_at,created_at,updated_at,coalesce(conversation_id::text,''),risk,plan,tool_calls,validation`

type Service struct {
	pool       *pgxpool.Pool
	settings   settingsProvider
	workbooks  workbook.Repository
	logger     *slog.Logger
	httpClient *http.Client
	now        func() time.Time
	limitsMu   sync.RWMutex
	limits     map[string]cachedLimits
}

type cachedLimits struct {
	limits  ModelLimits
	expires time.Time
}

func NewService(pool *pgxpool.Pool, settings settingsProvider, workbooks workbook.Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, settings: settings, workbooks: workbooks, logger: logger, now: func() time.Time { return time.Now().UTC() }, limits: map[string]cachedLimits{}}
}

// SetHTTPClient replaces the gateway transport. It is primarily useful for a
// deterministic in-process gateway in tests; production uses the configured
// timeout and private CA bundle.
func (s *Service) SetHTTPClient(client *http.Client) { s.httpClient = client }

func (s *Service) PublicConfig(ctx context.Context) (Config, error) {
	config, err := readConfig(ctx, s.settings)
	if err != nil {
		return Config{}, err
	}
	config.GatewayURL, config.APIKey, config.CAPEM = "", "", ""
	return config, nil
}

func (s *Service) Plan(ctx context.Context, input PlanInput) (Action, error) {
	input = normalizePlanInput(input)
	if err := validatePlanInput(input); err != nil {
		return Action{}, err
	}
	if duplicate, err := s.findByIdempotency(ctx, input.ActorID, input.IdempotencyKey); err == nil {
		if !samePlanRequest(duplicate, input) {
			return Action{}, fmt.Errorf("%w: idempotency_key was already used for a different AI plan", ErrInvalid)
		}
		duplicate.Duplicate = true
		return duplicate, nil
	} else if !errors.Is(err, ErrNotFound) {
		return Action{}, err
	}
	config, err := readConfig(ctx, s.settings)
	if err != nil {
		return Action{}, err
	}
	if !config.Enabled {
		return Action{}, ErrDisabled
	}
	book, err := s.workbooks.GetWorkbook(ctx, input.WorkbookID)
	if err != nil {
		return Action{}, err
	}
	if input.BaseVersion != book.Version {
		return Action{}, workbook.ErrVersionConflict
	}
	if !sheetBelongsToWorkbook(book, input.SheetID) {
		return Action{}, workbook.ErrNotFound
	}
	if (input.Mode == ModeChart || input.Mode == ModeAgent) && input.Charts == nil {
		input.Charts, err = s.workbooks.ListCharts(ctx, input.WorkbookID, "")
		if err != nil {
			return Action{}, err
		}
	}
	if chartUpdateRequested(input) {
		target := recommendedChartTarget(input)
		if target == nil {
			return Action{}, fmt.Errorf("%w: 수정할 차트를 찾을 수 없습니다. 현재 차트 ID나 정확한 제목을 요청에 포함하세요", ErrInvalid)
		}
		if desired := requestedChartType(input.Request); desired != "" && chartTypeOnlyUpdate(input.Request) {
			for _, chart := range input.Charts {
				if chart.ID == target.ChartID && normalizeChartType(chart.Type) == desired {
					return Action{}, fmt.Errorf("%w: 선택한 차트는 이미 %s 형식입니다", ErrInvalid, desired)
				}
			}
		}
	}
	selected, err := cellrange.Parse(input.Range)
	if err != nil {
		return Action{}, fmt.Errorf("%w: range is invalid", ErrInvalid)
	}
	rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	if rows < 1 || columns < 1 || rows > config.MaxInputCells || columns > config.MaxInputCells || rows > config.MaxInputCells/columns {
		return Action{}, fmt.Errorf("%w: selected range exceeds ai.max_input_cells (%d)", ErrInvalid, config.MaxInputCells)
	}
	cells, err := s.workbooks.ReadRange(ctx, input.SheetID, selected)
	if err != nil {
		return Action{}, err
	}
	client := s.httpClient
	if client == nil {
		client, err = gatewayHTTPClient(config)
		if err != nil {
			return Action{}, err
		}
	}
	planningContext, cancelPlanning := context.WithTimeout(ctx, maxPlanningDuration)
	defer cancelPlanning()
	limitsContext, cancel := context.WithTimeout(planningContext, modelLimitsLookupTimeout(config))
	limits := s.modelLimits(limitsContext, client, config)
	cancel()
	generated, usage, err := requestGatewayPlan(planningContext, client, config, input, selected, cells, limits)
	if err != nil {
		s.logger.Warn("AI plan gateway failed", "actor_id", input.ActorID, "workbook_id", input.WorkbookID, "error", err)
		return Action{}, err
	}
	changes, findings, err := validateGatewayPlan(input.Mode, selected, cells, generated, config.MaxChanges)
	if err != nil {
		return Action{}, err
	}
	toolCalls, err := validateGatewayTools(input, selected, generated.ToolCalls, config.MaxChanges)
	if err != nil {
		return Action{}, err
	}
	now := s.now()
	status := StatusPlanned
	eventType := "planned"
	if IsReadOnlyMode(input.Mode) {
		status = StatusCompleted
		eventType = "completed"
	}
	risk := riskForMode(input.Mode, len(changes))
	steps := buildPlanSteps(input.Mode, risk, changes, toolCalls)
	preflight := ValidationResult{Passed: true, Checks: []ValidationCheck{{Name: "selection_bounds", Passed: true, Message: "모든 셀 변경이 선택 범위 안에 있습니다."}, {Name: "tool_authorization", Passed: true, Message: "지원되고 허용된 도구만 계획에 포함되었습니다."}}}
	action := Action{
		ID: identity.New(), WorkbookID: input.WorkbookID, SheetID: input.SheetID,
		ActorID: input.ActorID, ClientID: input.ClientID, IdempotencyKey: input.IdempotencyKey,
		Mode: input.Mode, Range: input.Range, Request: input.Request, Status: status,
		BaseVersion: book.Version, Model: planModel(config, limits, usage), Summary: trimLength(generated.Summary, 2000),
		Explanation: trimLength(generated.Explanation, 12000), Changes: changes, Findings: findings,
		InputCellCount: rows * columns, Revision: 1, CreatedAt: now, UpdatedAt: now,
		ConversationID: input.ConversationID, Risk: risk, Plan: steps, ToolCalls: toolCalls, Validation: preflight,
	}
	encodedChanges, _ := json.Marshal(action.Changes)
	encodedFindings, _ := json.Marshal(action.Findings)
	encodedPlan, _ := json.Marshal(action.Plan)
	encodedTools, _ := json.Marshal(action.ToolCalls)
	encodedValidation, _ := json.Marshal(action.Validation)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `INSERT INTO ai_actions(id,workbook_id,sheet_id,actor_id,client_id,idempotency_key,mode,selected_range,request,status,base_version,model,summary,explanation,changes,findings,input_cell_count,revision,created_at,updated_at,conversation_id,risk,plan,tool_calls,validation) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,1,$18,$18,$19,$20,$21,$22,$23) ON CONFLICT(actor_id,idempotency_key) DO NOTHING`, action.ID, action.WorkbookID, action.SheetID, action.ActorID, action.ClientID, action.IdempotencyKey, action.Mode, action.Range, action.Request, action.Status, action.BaseVersion, action.Model, action.Summary, action.Explanation, encodedChanges, encodedFindings, action.InputCellCount, now, nullableUUID(action.ConversationID), action.Risk, encodedPlan, encodedTools, encodedValidation)
	if err != nil {
		return Action{}, err
	}
	if command.RowsAffected() == 0 {
		if err := tx.Rollback(ctx); err != nil {
			return Action{}, err
		}
		duplicate, err := s.findByIdempotency(ctx, input.ActorID, input.IdempotencyKey)
		duplicate.Duplicate = err == nil
		return duplicate, err
	}
	payload, _ := json.Marshal(map[string]any{"mode": action.Mode, "range": action.Range, "request": action.Request, "changes": len(action.Changes), "findings": len(action.Findings), "input_cell_count": action.InputCellCount, "usage": usage})
	if err := insertEvent(ctx, tx, action.ID, action.ActorID, eventType, action.Model, "range.read", payload, now); err != nil {
		return Action{}, err
	}
	if err := insertAudit(ctx, tx, action.ActorID, "ai.action.plan", action.ID, "success", payload, now); err != nil {
		return Action{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return Action{}, err
	}
	action.Events = []Event{{ActorID: action.ActorID, EventType: eventType, Model: action.Model, ToolName: "range.read", Payload: payload, CreatedAt: now}}
	spent := usage
	action.Usage = &spent
	s.logger.Info("AI action planned", "action_id", action.ID, "actor_id", action.ActorID, "workbook_id", action.WorkbookID, "model", action.Model, "changes", len(action.Changes), "findings", len(action.Findings))
	return action, nil
}

// Preview reports what the gateway request would contain without sending it,
// so people can read the prompt and the exact cells before they share them.
// It works while AI is switched off, which is how an administrator can review
// the wording before enabling the feature.
func (s *Service) Preview(ctx context.Context, input PlanInput) (PromptPreview, error) {
	input = normalizePlanInput(input)
	if err := validatePlanInput(input); err != nil {
		return PromptPreview{}, err
	}
	config, err := readConfig(ctx, s.settings)
	if err != nil {
		return PromptPreview{}, err
	}
	book, err := s.workbooks.GetWorkbook(ctx, input.WorkbookID)
	if err != nil {
		return PromptPreview{}, err
	}
	if !sheetBelongsToWorkbook(book, input.SheetID) {
		return PromptPreview{}, workbook.ErrNotFound
	}
	if (input.Mode == ModeChart || input.Mode == ModeAgent) && input.Charts == nil {
		input.Charts, err = s.workbooks.ListCharts(ctx, input.WorkbookID, "")
		if err != nil {
			return PromptPreview{}, err
		}
	}
	selected, err := cellrange.Parse(input.Range)
	if err != nil {
		return PromptPreview{}, fmt.Errorf("%w: range is invalid", ErrInvalid)
	}
	rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	if rows < 1 || columns < 1 || rows > config.MaxInputCells || columns > config.MaxInputCells || rows > config.MaxInputCells/columns {
		return PromptPreview{}, fmt.Errorf("%w: selected range exceeds ai.max_input_cells (%d)", ErrInvalid, config.MaxInputCells)
	}
	cells, err := s.workbooks.ReadRange(ctx, input.SheetID, selected)
	if err != nil {
		return PromptPreview{}, err
	}
	client := s.httpClient
	if client == nil {
		if client, err = gatewayHTTPClient(config); err != nil {
			return PromptPreview{}, err
		}
	}
	limitsContext, cancel := context.WithTimeout(ctx, modelLimitsLookupTimeout(config))
	defer cancel()
	return BuildPrompt(config, input, selected, cells, s.modelLimits(limitsContext, client, config)), nil
}

// planModel prefers the model the gateway actually reports over the configured
// name, which matters when the name was left blank on purpose.
func planModel(config Config, limits ModelLimits, usage Usage) string {
	for _, candidate := range []string{usage.Model, limits.Model, config.Model} {
		if strings.TrimSpace(candidate) != "" {
			return strings.TrimSpace(candidate)
		}
	}
	return "unknown"
}

// modelLimits caches what the gateway publishes about the model. The context
// length rarely changes, and a plan should not pay for an extra round trip.
func (s *Service) modelLimits(ctx context.Context, client *http.Client, config Config) ModelLimits {
	key := config.GatewayURL + "|" + config.Model
	s.limitsMu.RLock()
	cached, ok := s.limits[key]
	s.limitsMu.RUnlock()
	if ok && s.now().Before(cached.expires) {
		return cached.limits
	}
	limits, err := fetchModelLimits(ctx, client, config)
	if err != nil {
		s.logger.Debug("AI model limits unavailable", "error", err)
		limits = ModelLimits{Model: config.Model}
	}
	s.limitsMu.Lock()
	s.limits[key] = cachedLimits{limits: limits, expires: s.now().Add(10 * time.Minute)}
	s.limitsMu.Unlock()
	return limits
}

func (s *Service) Get(ctx context.Context, actionID, actorID string) (Action, error) {
	action, err := scanAction(s.pool.QueryRow(ctx, `SELECT `+actionColumns+` FROM ai_actions WHERE id=$1 AND actor_id=$2`, actionID, actorID))
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	if err != nil {
		return Action{}, err
	}
	action.Events, err = s.listEvents(ctx, action.ID)
	return action, err
}

func (s *Service) List(ctx context.Context, workbookID, actorID string, limit int) ([]Action, error) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	rows, err := s.pool.Query(ctx, `SELECT `+actionColumns+` FROM ai_actions WHERE workbook_id=$1 AND actor_id=$2 ORDER BY created_at DESC,id LIMIT $3`, workbookID, actorID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Action, 0)
	for rows.Next() {
		item, err := scanAction(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) Approve(ctx context.Context, actionID string, input ApprovalInput) (ExecutionResult, error) {
	input = normalizeApprovalInput(input)
	if err := validateExecutionInput(input); err != nil {
		return ExecutionResult{}, err
	}
	action, err := s.Get(ctx, actionID, input.ActorID)
	if err != nil {
		return ExecutionResult{}, err
	}
	if IsReadOnlyMode(action.Mode) || len(action.Changes) == 0 {
		return ExecutionResult{}, fmt.Errorf("%w: read-only AI actions cannot be approved", ErrInvalid)
	}
	if action.Status == StatusApplied && action.approvalIdempotencyKey == input.IdempotencyKey && action.Operation != nil {
		action.Duplicate = true
		operation := *action.Operation
		operation.Duplicate = true
		return ExecutionResult{Action: action, Operation: operation}, nil
	}
	if action.Status != StatusPlanned && !(action.Status == StatusApplying && action.approvalIdempotencyKey == input.IdempotencyKey) {
		return ExecutionResult{}, ErrRevision
	}
	if action.Status == StatusPlanned && action.Revision != input.ExpectedRevision {
		return ExecutionResult{}, ErrRevision
	}
	if action.Status == StatusPlanned {
		book, err := s.workbooks.GetWorkbook(ctx, action.WorkbookID)
		if err != nil {
			return ExecutionResult{}, err
		}
		if book.Version != action.BaseVersion {
			return ExecutionResult{}, workbook.ErrVersionConflict
		}
		command, err := s.pool.Exec(ctx, `UPDATE ai_actions SET status=$3,revision=revision+1,approval_idempotency_key=$4,updated_at=$5,error_message='' WHERE id=$1 AND actor_id=$2 AND status=$6 AND revision=$7`, action.ID, action.ActorID, StatusApplying, input.IdempotencyKey, s.now(), StatusPlanned, input.ExpectedRevision)
		if err != nil {
			return ExecutionResult{}, err
		}
		if command.RowsAffected() != 1 {
			return s.Approve(ctx, actionID, input)
		}
		action, err = s.Get(ctx, actionID, input.ActorID)
		if err != nil {
			return ExecutionResult{}, err
		}
	}
	cells, expected := actionCellInputs(action)
	result, err := s.workbooks.ApplyCells(ctx, workbook.CellMutation{
		SheetID: action.SheetID, ActorID: action.ActorID, ClientID: input.ClientID,
		BaseVersion: action.BaseVersion, IdempotencyKey: executionKey(action.ID, "approve", input.IdempotencyKey),
		Cells: cells, Expected: expected, OperationType: "ai." + action.Mode, RequireExactVersion: true,
	})
	if err != nil {
		s.markFailed(ctx, action, input.ActorID, "approval_failed", approvalToolName(action.Mode), err)
		return ExecutionResult{}, err
	}
	if err := s.completeApproval(ctx, action, result); err != nil {
		return ExecutionResult{}, err
	}
	action, err = s.Get(ctx, action.ID, input.ActorID)
	if err != nil {
		return ExecutionResult{}, err
	}
	s.logger.Info("AI action approved", "action_id", action.ID, "actor_id", action.ActorID, "operation_id", result.OperationID, "server_version", result.ServerVersion)
	return ExecutionResult{Action: action, Operation: result, Changes: cells}, nil
}

func (s *Service) Undo(ctx context.Context, actionID string, input ApprovalInput) (ExecutionResult, error) {
	input = normalizeApprovalInput(input)
	if err := validateExecutionInput(input); err != nil {
		return ExecutionResult{}, err
	}
	action, err := s.Get(ctx, actionID, input.ActorID)
	if err != nil {
		return ExecutionResult{}, err
	}
	if action.Status == StatusUndone && action.undoIdempotencyKey == input.IdempotencyKey && action.UndoOperation != nil {
		action.Duplicate = true
		operation := *action.UndoOperation
		operation.Duplicate = true
		return ExecutionResult{Action: action, Operation: operation}, nil
	}
	if action.Status != StatusApplied && !(action.Status == StatusUndoing && action.undoIdempotencyKey == input.IdempotencyKey) {
		return ExecutionResult{}, ErrRevision
	}
	if action.Status == StatusApplied && action.Revision != input.ExpectedRevision {
		return ExecutionResult{}, ErrRevision
	}
	if action.Status == StatusApplied {
		command, err := s.pool.Exec(ctx, `UPDATE ai_actions SET status=$3,revision=revision+1,undo_idempotency_key=$4,updated_at=$5,error_message='' WHERE id=$1 AND actor_id=$2 AND status=$6 AND revision=$7`, action.ID, action.ActorID, StatusUndoing, input.IdempotencyKey, s.now(), StatusApplied, input.ExpectedRevision)
		if err != nil {
			return ExecutionResult{}, err
		}
		if command.RowsAffected() != 1 {
			return s.Undo(ctx, actionID, input)
		}
		action, err = s.Get(ctx, actionID, input.ActorID)
		if err != nil {
			return ExecutionResult{}, err
		}
	}
	result, err := s.workbooks.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: action.OperationID, ActorID: action.ActorID, ClientID: input.ClientID, IdempotencyKey: executionKey(action.ID, "undo", input.IdempotencyKey)})
	if err != nil {
		s.markFailed(ctx, action, input.ActorID, "undo_failed", "operation.undo", err)
		return ExecutionResult{}, err
	}
	if err := s.completeUndo(ctx, action, result); err != nil {
		return ExecutionResult{}, err
	}
	action, err = s.Get(ctx, action.ID, input.ActorID)
	if err != nil {
		return ExecutionResult{}, err
	}
	s.logger.Info("AI action undone", "action_id", action.ID, "actor_id", action.ActorID, "operation_id", result.OperationID, "server_version", result.ServerVersion)
	return ExecutionResult{Action: action, Operation: result}, nil
}

func (s *Service) completeApproval(ctx context.Context, action Action, result workbook.MutationResult) error {
	data, _ := json.Marshal(result)
	payload, _ := json.Marshal(map[string]any{"operation_id": result.OperationID, "server_version": result.ServerVersion, "applied_cells": result.AppliedCells, "formula_errors": result.FormulaErrors})
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE ai_actions SET status=$3,operation_id=$4,operation_result=$5,approved_at=$6,updated_at=$6,error_message='' WHERE id=$1 AND actor_id=$2 AND status=$7 AND approval_idempotency_key=$8`, action.ID, action.ActorID, StatusApplied, result.OperationID, data, now, StatusApplying, action.approvalIdempotencyKey)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrRevision
	}
	if err := insertEvent(ctx, tx, action.ID, action.ActorID, "applied", action.Model, approvalToolName(action.Mode), payload, now); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, action.ActorID, "ai.action.approve", action.ID, "success", payload, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) completeUndo(ctx context.Context, action Action, result workbook.MutationResult) error {
	data, _ := json.Marshal(result)
	payload, _ := json.Marshal(map[string]any{"operation_id": result.OperationID, "server_version": result.ServerVersion, "restored_cells": result.AppliedCells})
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE ai_actions SET status=$3,undo_operation_id=$4,undo_result=$5,undone_at=$6,updated_at=$6,error_message='' WHERE id=$1 AND actor_id=$2 AND status=$7 AND undo_idempotency_key=$8`, action.ID, action.ActorID, StatusUndone, result.OperationID, data, now, StatusUndoing, action.undoIdempotencyKey)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrRevision
	}
	if err := insertEvent(ctx, tx, action.ID, action.ActorID, "undone", action.Model, "operation.undo", payload, now); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, action.ActorID, "ai.action.undo", action.ID, "success", payload, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) markFailed(ctx context.Context, action Action, actorID, eventType, toolName string, cause error) {
	payload, _ := json.Marshal(map[string]any{"error": trimLength(cause.Error(), 2000)})
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, _ = tx.Exec(ctx, `UPDATE ai_actions SET status=$2,error_message=$3,updated_at=$4 WHERE id=$1 AND status IN ($5,$6)`, action.ID, StatusFailed, trimLength(cause.Error(), 2000), now, StatusApplying, StatusUndoing)
	_ = insertEvent(ctx, tx, action.ID, actorID, eventType, action.Model, toolName, payload, now)
	_ = insertAudit(ctx, tx, actorID, "ai.action."+eventType, action.ID, "failure", payload, now)
	_ = tx.Commit(ctx)
}

func (s *Service) findByIdempotency(ctx context.Context, actorID, key string) (Action, error) {
	action, err := scanAction(s.pool.QueryRow(ctx, `SELECT `+actionColumns+` FROM ai_actions WHERE actor_id=$1 AND idempotency_key=$2`, actorID, key))
	if errors.Is(err, pgx.ErrNoRows) {
		return Action{}, ErrNotFound
	}
	return action, err
}

type actionScanner interface{ Scan(...any) error }

func scanAction(row actionScanner) (Action, error) {
	var action Action
	var changes, findings, operation, undo, plan, tools, validation []byte
	err := row.Scan(&action.ID, &action.WorkbookID, &action.SheetID, &action.ActorID, &action.ClientID, &action.IdempotencyKey, &action.Mode, &action.Range, &action.Request, &action.Status, &action.BaseVersion, &action.Model, &action.Summary, &action.Explanation, &changes, &findings, &action.InputCellCount, &action.Revision, &action.approvalIdempotencyKey, &action.OperationID, &operation, &action.undoIdempotencyKey, &action.UndoOperationID, &undo, &action.ErrorMessage, &action.ApprovedAt, &action.UndoneAt, &action.CreatedAt, &action.UpdatedAt, &action.ConversationID, &action.Risk, &plan, &tools, &validation)
	if err != nil {
		return Action{}, err
	}
	if err := json.Unmarshal(changes, &action.Changes); err != nil {
		return Action{}, err
	}
	if err := json.Unmarshal(findings, &action.Findings); err != nil {
		return Action{}, err
	}
	if err := json.Unmarshal(plan, &action.Plan); err != nil {
		return Action{}, err
	}
	if err := json.Unmarshal(tools, &action.ToolCalls); err != nil {
		return Action{}, err
	}
	if err := json.Unmarshal(validation, &action.Validation); err != nil {
		return Action{}, err
	}
	if len(operation) > 0 && string(operation) != "null" {
		var result workbook.MutationResult
		if err := json.Unmarshal(operation, &result); err != nil {
			return Action{}, err
		}
		action.Operation = &result
	}
	if len(undo) > 0 && string(undo) != "null" {
		var result workbook.MutationResult
		if err := json.Unmarshal(undo, &result); err != nil {
			return Action{}, err
		}
		action.UndoOperation = &result
	}
	return action, nil
}

func (s *Service) listEvents(ctx context.Context, actionID string) ([]Event, error) {
	rows, err := s.pool.Query(ctx, `SELECT id,actor_id,event_type,model,tool_name,payload,created_at FROM ai_action_events WHERE action_id=$1 ORDER BY created_at,id`, actionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Event, 0)
	for rows.Next() {
		var item Event
		if err := rows.Scan(&item.ID, &item.ActorID, &item.EventType, &item.Model, &item.ToolName, &item.Payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func insertEvent(ctx context.Context, tx pgx.Tx, actionID, actorID, eventType, model, tool string, payload []byte, createdAt time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO ai_action_events(action_id,actor_id,event_type,model,tool_name,payload,created_at) VALUES($1,$2,$3,$4,$5,$6,$7)`, actionID, actorID, eventType, model, tool, payload, createdAt)
	return err
}

func insertAudit(ctx context.Context, tx pgx.Tx, actorID, action, resourceID, result string, payload []byte, createdAt time.Time) error {
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource_type,resource_id,result,metadata,created_at) VALUES($1,$2,'ai_action',$3,$4,$5,$6)`, actorID, action, resourceID, result, payload, createdAt)
	return err
}

func validatePlanInput(input PlanInput) error {
	if input.ActorID == "" || input.WorkbookID == "" || input.SheetID == "" || input.Range == "" || input.IdempotencyKey == "" {
		return fmt.Errorf("%w: workbook_id, sheet_id, range and idempotency_key are required", ErrInvalid)
	}
	if !validMode(input.Mode) {
		return fmt.Errorf("%w: unsupported workbook agent mode", ErrInvalid)
	}
	if input.Request == "" || len(input.Request) > 4000 {
		return fmt.Errorf("%w: request must contain 1 to 4000 characters", ErrInvalid)
	}
	if input.BaseVersion < 1 {
		return fmt.Errorf("%w: base_version must be positive", ErrInvalid)
	}
	return nil
}

func validateExecutionInput(input ApprovalInput) error {
	if input.ActorID == "" || input.IdempotencyKey == "" {
		return fmt.Errorf("%w: idempotency_key is required", ErrInvalid)
	}
	if input.ExpectedRevision < 1 {
		return fmt.Errorf("%w: expected_revision must be positive", ErrInvalid)
	}
	return nil
}

func validateGatewayPlan(mode string, selected cellrange.Range, cells []workbook.Cell, plan gatewayPlan, maxChanges int) ([]ProposedChange, []Finding, error) {
	plan.Summary = strings.TrimSpace(plan.Summary)
	plan.Explanation = strings.TrimSpace(plan.Explanation)
	if plan.Summary == "" {
		return nil, nil, fmt.Errorf("%w: model plan summary is required", ErrGateway)
	}
	current := make(map[string]workbook.Cell, len(cells))
	for _, cell := range cells {
		current[coordinateKey(cell.Row, cell.Column)] = cell
	}
	if IsReadOnlyMode(mode) {
		if len(plan.Changes) != 0 {
			return nil, nil, fmt.Errorf("%w: read-only AI modes cannot propose changes", ErrGateway)
		}
		if plan.Explanation == "" {
			return nil, nil, fmt.Errorf("%w: read-only AI modes require an explanation", ErrGateway)
		}
		findings, err := validateFindings(mode, selected, current, plan.Findings, maxChanges)
		return []ProposedChange{}, findings, err
	}
	if len(plan.Findings) != 0 {
		return nil, nil, fmt.Errorf("%w: write AI modes cannot return findings", ErrGateway)
	}
	if mode == ModeChart {
		if len(plan.Changes) != 0 || len(plan.ToolCalls) != 1 {
			return nil, nil, fmt.Errorf("%w: chart mode requires one tool call and no cell changes", ErrGateway)
		}
		return []ProposedChange{}, []Finding{}, nil
	}
	if (len(plan.Changes) < 1 && !(mode == ModeAgent && len(plan.ToolCalls) > 0)) || len(plan.Changes) > maxChanges {
		return nil, nil, fmt.Errorf("%w: model must propose 1 to %d changes", ErrGateway, maxChanges)
	}
	seen := make(map[string]struct{}, len(plan.Changes))
	changes := make([]ProposedChange, 0, len(plan.Changes))
	for _, candidate := range plan.Changes {
		if candidate.Row < selected.Start.Row || candidate.Row > selected.End.Row || candidate.Column < selected.Start.Column || candidate.Column > selected.End.Column {
			return nil, nil, fmt.Errorf("%w: model proposed a cell outside selected range", ErrGateway)
		}
		key := coordinateKey(candidate.Row, candidate.Column)
		if _, exists := seen[key]; exists {
			return nil, nil, fmt.Errorf("%w: model proposed the same cell more than once", ErrGateway)
		}
		seen[key] = struct{}{}
		before := snapshotFromCell(current[key])
		after := CellSnapshot{Style: cloneRaw(before.Style), Value: cloneRaw(before.Value), Formula: before.Formula, SpillSource: before.SpillSource}
		if mode == ModeClean {
			if err := validateCleanChange(candidate); err != nil {
				return nil, nil, err
			}
			after.Value, after.Formula, after.SpillSource = nil, "", ""
			if !candidate.Clear {
				after.Value = cloneRaw(candidate.Value)
			}
		} else if mode == ModeFormat {
			if len(bytes.TrimSpace(candidate.Value)) != 0 || candidate.Clear || strings.TrimSpace(candidate.Formula) != "" || len(bytes.TrimSpace(candidate.Style)) == 0 {
				return nil, nil, fmt.Errorf("%w: format changes require only a complete style object", ErrGateway)
			}
			if err := workbook.ValidateCellStyle(workbook.CellInput{Row: candidate.Row, Column: candidate.Column, Style: candidate.Style}); err != nil {
				return nil, nil, fmt.Errorf("%w: invalid format style: %v", ErrGateway, err)
			}
			after.Style = cloneRaw(candidate.Style)
		} else if mode == ModeAgent {
			if err := applyAgentCandidate(&after, candidate); err != nil {
				return nil, nil, err
			}
		} else {
			if len(bytes.TrimSpace(candidate.Value)) != 0 || candidate.Clear {
				return nil, nil, fmt.Errorf("%w: formula modes cannot propose literal values or clear operations", ErrGateway)
			}
			formula := strings.TrimSpace(candidate.Formula)
			if !strings.HasPrefix(formula, "=") || len(formula) > 8192 {
				return nil, nil, fmt.Errorf("%w: every proposed formula must begin with '=' and be at most 8192 characters", ErrGateway)
			}
			after.Value, after.Formula, after.SpillSource = nil, formula, ""
		}
		if snapshotsEqual(before, after) {
			continue
		}
		changes = append(changes, ProposedChange{Row: candidate.Row, Column: candidate.Column, Address: cellrange.Address(candidate.Row, candidate.Column), Before: before, After: after})
	}
	if len(changes) == 0 && !(mode == ModeAgent && len(plan.ToolCalls) > 0) {
		return nil, nil, fmt.Errorf("%w: model proposed no effective changes", ErrGateway)
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Row == changes[j].Row {
			return changes[i].Column < changes[j].Column
		}
		return changes[i].Row < changes[j].Row
	})
	return changes, []Finding{}, nil
}

func actionCellInputs(action Action) ([]workbook.CellInput, map[string]workbook.Cell) {
	cells := make([]workbook.CellInput, 0, len(action.Changes))
	expected := make(map[string]workbook.Cell, len(action.Changes))
	for _, change := range action.Changes {
		cells = append(cells, workbook.CellInput{Row: change.Row, Column: change.Column, Value: cloneRaw(change.After.Value), Formula: change.After.Formula, Style: cloneRaw(change.After.Style)})
		expected[coordinateKey(change.Row, change.Column)] = workbook.Cell{SheetID: action.SheetID, Row: change.Row, Column: change.Column, Value: cloneRaw(change.Before.Value), Formula: change.Before.Formula, Style: cloneRaw(change.Before.Style), SpillSource: change.Before.SpillSource}
	}
	return cells, expected
}

func validMode(mode string) bool {
	return mode == ModeFormula || mode == ModeExplain || mode == ModeFix || mode == ModeSummarize || mode == ModeAnomaly || mode == ModeClean || mode == ModeFormat || mode == ModeChart || mode == ModeAgent
}

func validateCleanChange(change gatewayChange) error {
	if strings.TrimSpace(change.Formula) != "" {
		return fmt.Errorf("%w: clean mode cannot propose formulas", ErrGateway)
	}
	hasValue := len(bytes.TrimSpace(change.Value)) != 0
	if hasValue == change.Clear {
		return fmt.Errorf("%w: clean changes require exactly one scalar value or clear=true", ErrGateway)
	}
	if change.Clear {
		return nil
	}
	if len(change.Value) > 65536 || !json.Valid(change.Value) {
		return fmt.Errorf("%w: clean values must be valid scalar JSON up to 65536 bytes", ErrGateway)
	}
	var value any
	if err := json.Unmarshal(change.Value, &value); err != nil {
		return fmt.Errorf("%w: clean value is invalid", ErrGateway)
	}
	switch value.(type) {
	case string, float64, bool:
		return nil
	default:
		return fmt.Errorf("%w: clean values must be strings, numbers or booleans; use clear=true to clear a cell", ErrGateway)
	}
}

func applyAgentCandidate(after *CellSnapshot, change gatewayChange) error {
	hasFormula := strings.TrimSpace(change.Formula) != ""
	hasStyle := len(bytes.TrimSpace(change.Style)) != 0
	hasValue := len(bytes.TrimSpace(change.Value)) != 0
	count := 0
	for _, present := range []bool{hasFormula, hasStyle, hasValue, change.Clear} {
		if present {
			count++
		}
	}
	if count != 1 {
		return fmt.Errorf("%w: agent cell changes require exactly one formula, style, scalar value, or clear operation", ErrGateway)
	}
	switch {
	case hasFormula:
		formula := strings.TrimSpace(change.Formula)
		if !strings.HasPrefix(formula, "=") || len(formula) > 8192 {
			return fmt.Errorf("%w: agent formulas must begin with '=' and be at most 8192 characters", ErrGateway)
		}
		after.Value, after.Formula, after.SpillSource = nil, formula, ""
	case hasStyle:
		if err := workbook.ValidateCellStyle(workbook.CellInput{Style: change.Style}); err != nil {
			return fmt.Errorf("%w: invalid agent style: %v", ErrGateway, err)
		}
		after.Style = cloneRaw(change.Style)
	case hasValue, change.Clear:
		if err := validateCleanChange(change); err != nil {
			return err
		}
		after.Value, after.Formula, after.SpillSource = nil, "", ""
		if !change.Clear {
			after.Value = cloneRaw(change.Value)
		}
	}
	return nil
}

// createConditionalFormatArguments 는 "상위 10%에 색 칠해줘" 같은 요청이
// 만들어 내는 규칙이다. 조건부 서식은 값에 따라 그때그때 칠하므로, 셀을
// 직접 물들이는 것과 달리 자료가 바뀌면 색도 따라 바뀐다.
//
// 규칙 종류마다 필요한 값이 다르다. 색조(color_scale)는 양 끝 색을, 막대
// (data_bar)는 막대 색을, 나머지는 칠할 서식을 받는다.
type createConditionalFormatArguments struct {
	SheetID  string          `json:"sheet_id,omitempty"`
	Name     string          `json:"name,omitempty"`
	Range    string          `json:"range"`
	RuleType string          `json:"rule_type"`
	Operator string          `json:"operator,omitempty"`
	Formula  string          `json:"formula,omitempty"`
	Value    json.RawMessage `json:"value,omitempty"`
	Value2   json.RawMessage `json:"value2,omitempty"`
	Style    json.RawMessage `json:"style,omitempty"`
	MinColor string          `json:"min_color,omitempty"`
	MidColor string          `json:"mid_color,omitempty"`
	MaxColor string          `json:"max_color,omitempty"`
	BarColor string          `json:"bar_color,omitempty"`
}

// validateCreateConditionalFormat 은 모델이 적어 보낸 규칙이 말이 되는지
// 본다. 범위는 사람이 고른 범위 안에 있어야 한다 — 고르지도 않은 곳을
// 물들이는 것은 사람이 시킨 일이 아니다.
func validateCreateConditionalFormat(input PlanInput, raw json.RawMessage) (createConditionalFormatArguments, error) {
	var arguments createConditionalFormatArguments
	if err := json.Unmarshal(raw, &arguments); err != nil {
		return arguments, fmt.Errorf("%w: create_conditional_format arguments are not an object", ErrGateway)
	}
	arguments.Range = strings.TrimSpace(arguments.Range)
	arguments.RuleType = strings.ToLower(strings.TrimSpace(arguments.RuleType))
	arguments.Operator = strings.ToLower(strings.TrimSpace(arguments.Operator))
	if arguments.Range == "" {
		arguments.Range = input.Range
	}
	target, err := cellrange.Parse(arguments.Range)
	if err != nil {
		return arguments, fmt.Errorf("%w: create_conditional_format range is not a range", ErrGateway)
	}
	selected, err := cellrange.Parse(input.Range)
	if err != nil {
		return arguments, fmt.Errorf("%w: the selected range is not a range", ErrGateway)
	}
	if target.Start.Row < selected.Start.Row || target.Start.Column < selected.Start.Column ||
		target.End.Row > selected.End.Row || target.End.Column > selected.End.Column {
		return arguments, fmt.Errorf("%w: create_conditional_format range must stay inside the selected range", ErrGateway)
	}
	if arguments.RuleType == "" {
		return arguments, fmt.Errorf("%w: create_conditional_format needs a rule_type", ErrGateway)
	}
	return arguments, nil
}

type createChartArguments struct {
	SheetID           string `json:"sheet_id,omitempty"`
	SourceSheetID     string `json:"source_sheet_id,omitempty"`
	Type              string `json:"type"`
	Title             string `json:"title,omitempty"`
	SourceRange       string `json:"source_range"`
	FirstRowHeaders   *bool  `json:"first_row_headers,omitempty"`
	FirstColumnLabels *bool  `json:"first_column_labels,omitempty"`
	LegendPosition    string `json:"legend_position,omitempty"`
	XAxisTitle        string `json:"x_axis_title,omitempty"`
	YAxisTitle        string `json:"y_axis_title,omitempty"`
}

type updateChartArguments struct {
	ChartID           string  `json:"chart_id"`
	ExpectedRevision  int64   `json:"expected_revision"`
	Type              *string `json:"type,omitempty"`
	Title             *string `json:"title,omitempty"`
	SourceRange       *string `json:"source_range,omitempty"`
	FirstRowHeaders   *bool   `json:"first_row_headers,omitempty"`
	FirstColumnLabels *bool   `json:"first_column_labels,omitempty"`
	LegendPosition    *string `json:"legend_position,omitempty"`
	XAxisTitle        *string `json:"x_axis_title,omitempty"`
	YAxisTitle        *string `json:"y_axis_title,omitempty"`
}

type updateChartResult struct {
	Before workbook.Chart `json:"before"`
	After  workbook.Chart `json:"after"`
}

type reportChartArguments struct {
	Type              string `json:"type"`
	Title             string `json:"title,omitempty"`
	SourceRange       string `json:"source_range"`
	FirstRowHeaders   *bool  `json:"first_row_headers,omitempty"`
	FirstColumnLabels *bool  `json:"first_column_labels,omitempty"`
	LegendPosition    string `json:"legend_position,omitempty"`
	XAxisTitle        string `json:"x_axis_title,omitempty"`
	YAxisTitle        string `json:"y_axis_title,omitempty"`
}

type createReportSheetArguments struct {
	Name  string                `json:"name"`
	Cells []gatewayChange       `json:"cells"`
	Chart *reportChartArguments `json:"chart,omitempty"`
}

type createReportSheetResult struct {
	Sheet         workbook.Sheet          `json:"sheet"`
	CellOperation workbook.MutationResult `json:"cell_operation"`
	Chart         *workbook.Chart         `json:"chart,omitempty"`
}

func validateGatewayTools(input PlanInput, selected cellrange.Range, candidates []gatewayToolCall, maxChanges int) ([]ToolCall, error) {
	if input.Mode != ModeChart && input.Mode != ModeAgent {
		if len(candidates) != 0 {
			return nil, fmt.Errorf("%w: %s mode cannot call workbook tools", ErrGateway, input.Mode)
		}
		return []ToolCall{}, nil
	}
	if len(candidates) > 10 {
		return nil, fmt.Errorf("%w: an agent plan may contain at most 10 workbook tool calls", ErrGateway)
	}
	if input.Mode == ModeChart {
		if len(candidates) != 1 {
			return nil, fmt.Errorf("%w: chart mode requires exactly one create_chart or update_chart tool", ErrGateway)
		}
		if expected := expectedChartTool(input); expected != "" && strings.TrimSpace(candidates[0].Name) != expected {
			return nil, fmt.Errorf("%w: this chart request requires %s, not %s", ErrGateway, expected, strings.TrimSpace(candidates[0].Name))
		}
	}
	items := make([]ToolCall, 0, len(candidates))
	reportSheets := 0
	createdCharts := 0
	chartMutationTools := 0
	updatedChartIDs := map[string]struct{}{}
	for index, candidate := range candidates {
		name := strings.TrimSpace(candidate.Name)
		switch name {
		case "create_chart":
			if len(input.Charts)+createdCharts >= workbook.MaxChartsPerWorkbook {
				return nil, fmt.Errorf("%w: a workbook may contain at most %d charts", ErrGateway, workbook.MaxChartsPerWorkbook)
			}
			var arguments createChartArguments
			if json.Unmarshal(candidate.Arguments, &arguments) != nil {
				return nil, fmt.Errorf("%w: create_chart arguments are invalid", ErrGateway)
			}
			arguments.SheetID = strings.TrimSpace(arguments.SheetID)
			arguments.SourceSheetID = strings.TrimSpace(arguments.SourceSheetID)
			if arguments.SheetID == "" {
				arguments.SheetID = input.SheetID
			}
			if arguments.SourceSheetID == "" {
				arguments.SourceSheetID = input.SheetID
			}
			if strings.TrimSpace(arguments.SourceRange) == "" {
				arguments.SourceRange = normalizedSelection(selected)
			}
			if arguments.SheetID != input.SheetID || arguments.SourceSheetID != input.SheetID {
				return nil, fmt.Errorf("%w: create_chart may only use the active sheet", ErrGateway)
			}
			source, err := cellrange.Parse(strings.TrimSpace(arguments.SourceRange))
			if err != nil || source.Start.Row < selected.Start.Row || source.End.Row > selected.End.Row || source.Start.Column < selected.Start.Column || source.End.Column > selected.End.Column {
				return nil, fmt.Errorf("%w: create_chart source_range must stay inside selected_range", ErrGateway)
			}
			arguments.SourceRange = normalizedSelection(source)
			cellCount := int64(source.End.Row-source.Start.Row+1) * int64(source.End.Column-source.Start.Column+1)
			if cellCount > workbook.MaxChartSourceCells {
				return nil, fmt.Errorf("%w: create_chart source_range may contain at most %d cells", ErrGateway, workbook.MaxChartSourceCells)
			}
			arguments.Type = normalizeChartType(arguments.Type)
			if arguments.Type == "" {
				return nil, fmt.Errorf("%w: create_chart type is invalid", ErrGateway)
			}
			arguments.Title = strings.TrimSpace(arguments.Title)
			arguments.XAxisTitle = strings.TrimSpace(arguments.XAxisTitle)
			arguments.YAxisTitle = strings.TrimSpace(arguments.YAxisTitle)
			if len([]rune(arguments.Title)) > 200 || len([]rune(arguments.XAxisTitle)) > 100 || len([]rune(arguments.YAxisTitle)) > 100 {
				return nil, fmt.Errorf("%w: create_chart title or axis title is too long", ErrGateway)
			}
			arguments.LegendPosition = strings.ToLower(strings.TrimSpace(arguments.LegendPosition))
			if arguments.LegendPosition == "" {
				arguments.LegendPosition = "right"
			}
			if !validChartLegendPosition(arguments.LegendPosition) {
				return nil, fmt.Errorf("%w: create_chart legend_position is invalid", ErrGateway)
			}
			encoded, _ := json.Marshal(arguments)
			items = append(items, ToolCall{Name: name, Arguments: encoded, Status: "planned", Risk: RiskMedium, IdempotencyKey: fmt.Sprintf("%s:tool:%d", input.IdempotencyKey, index+1)})
			createdCharts++
			chartMutationTools++
		case "update_chart":
			arguments, err := validateUpdateChart(input, selected, candidate.Arguments)
			if err != nil {
				return nil, err
			}
			if _, duplicate := updatedChartIDs[arguments.ChartID]; duplicate {
				return nil, fmt.Errorf("%w: an agent plan may update chart_id %s only once", ErrGateway, arguments.ChartID)
			}
			updatedChartIDs[arguments.ChartID] = struct{}{}
			chartMutationTools++
			encoded, _ := json.Marshal(arguments)
			items = append(items, ToolCall{Name: name, Arguments: encoded, Status: "planned", Risk: RiskMedium, IdempotencyKey: fmt.Sprintf("%s:tool:%d", input.IdempotencyKey, index+1)})
		case "create_report_sheet":
			if input.Mode != ModeAgent {
				return nil, fmt.Errorf("%w: create_report_sheet requires agent mode", ErrGateway)
			}
			reportSheets++
			if reportSheets > 1 {
				return nil, fmt.Errorf("%w: a plan may create at most one report sheet", ErrGateway)
			}
			arguments, err := validateCreateReportSheet(input, candidate.Arguments, maxChanges)
			if err != nil {
				return nil, err
			}
			if arguments.Chart != nil {
				if len(input.Charts)+createdCharts >= workbook.MaxChartsPerWorkbook {
					return nil, fmt.Errorf("%w: a workbook may contain at most %d charts", ErrGateway, workbook.MaxChartsPerWorkbook)
				}
				createdCharts++
				chartMutationTools++
			}
			encoded, _ := json.Marshal(arguments)
			items = append(items, ToolCall{Name: name, Arguments: encoded, Status: "planned", Risk: RiskHigh, IdempotencyKey: fmt.Sprintf("%s:tool:%d", input.IdempotencyKey, index+1)})
		case "create_conditional_format":
			if input.Mode != ModeAgent {
				return nil, fmt.Errorf("%w: create_conditional_format requires agent mode", ErrGateway)
			}
			arguments, err := validateCreateConditionalFormat(input, candidate.Arguments)
			if err != nil {
				return nil, err
			}
			encoded, _ := json.Marshal(arguments)
			// 규칙은 셀 값을 바꾸지 않고, 되돌리면 규칙만 지우면 된다. 셀을
			// 직접 물들이는 것보다 위험이 낮다.
			items = append(items, ToolCall{Name: name, Arguments: encoded, Status: "planned", Risk: RiskMedium, IdempotencyKey: fmt.Sprintf("%s:tool:%d", input.IdempotencyKey, index+1)})
		default:
			return nil, fmt.Errorf("%w: unsupported workbook tool %q", ErrGateway, name)
		}
	}
	if input.Mode == ModeAgent && chartMutationTools > 0 && len(items) > 1 {
		return nil, fmt.Errorf("%w: an agent plan with a chart mutation may contain only one workbook tool call", ErrGateway)
	}
	return items, nil
}

func requestedChartType(request string) string {
	value := strings.ToLower(request)
	type match struct {
		name  string
		index int
	}
	best := match{index: -1}
	for _, candidate := range []struct {
		name    string
		markers []string
	}{
		{name: "bar", markers: []string{"막대", "bar"}},
		{name: "line", markers: []string{"선 차트", "선형", "선으로", "라인", "line"}},
		{name: "area", markers: []string{"영역", "area"}},
		{name: "pie", markers: []string{"원형", "파이", "pie"}},
		{name: "scatter", markers: []string{"분산형", "산점", "scatter"}},
		{name: "histogram", markers: []string{"히스토그램", "histogram"}},
	} {
		for _, marker := range candidate.markers {
			if index := strings.LastIndex(value, marker); index > best.index {
				best = match{name: candidate.name, index: index}
			}
		}
	}
	return best.name
}

func chartTypeOnlyUpdate(request string) bool {
	value := strings.ToLower(request)
	return !containsAny(value, "제목", "범례", "축 제목", "x축", "y축", "원본", "범위", "헤더", "레이블", "title", "legend", "axis", "source", "range", "header", "label")
}

func expectedChartTool(input PlanInput) string {
	if input.Mode != ModeChart {
		return ""
	}
	switch chartSkillForInput(input) {
	case "chart_generation":
		return "create_chart"
	case "chart_update":
		return "update_chart"
	default:
		return ""
	}
}

func validChartLegendPosition(value string) bool {
	return value == "none" || value == "top" || value == "right" || value == "bottom" || value == "left"
}

func validateUpdateChart(input PlanInput, selected cellrange.Range, raw json.RawMessage) (updateChartArguments, error) {
	var arguments updateChartArguments
	if json.Unmarshal(raw, &arguments) != nil {
		return arguments, fmt.Errorf("%w: update_chart arguments are invalid", ErrGateway)
	}
	arguments.ChartID = strings.TrimSpace(arguments.ChartID)
	if input.Mode == ModeAgent && !chartUpdateRequested(input) {
		return arguments, fmt.Errorf("%w: update_chart requires an explicit chart update request", ErrGateway)
	}
	if chartUpdateRequested(input) {
		target := recommendedChartTarget(input)
		if target == nil {
			return arguments, fmt.Errorf("%w: chart update target is ambiguous; name one current chart ID or exact title", ErrGateway)
		}
		if arguments.ChartID != target.ChartID {
			return arguments, fmt.Errorf("%w: chart update must use current chart_id %s", ErrGateway, target.ChartID)
		}
	}
	var current *workbook.Chart
	for index := range input.Charts {
		if input.Charts[index].ID == arguments.ChartID {
			current = &input.Charts[index]
			break
		}
	}
	if current == nil || current.WorkbookID != "" && current.WorkbookID != input.WorkbookID {
		return arguments, fmt.Errorf("%w: update_chart chart_id must reference a current workbook chart (%s)", ErrGateway, currentChartIDs(input.Charts))
	}
	if strings.TrimSpace(current.SourceSheetID) == "" || strings.TrimSpace(current.SourceRange) == "#REF!" {
		return arguments, fmt.Errorf("%w: update_chart cannot modify a chart with a broken source reference", ErrGateway)
	}
	if arguments.Type != nil {
		value := normalizeChartType(*arguments.Type)
		if value == "" {
			return arguments, fmt.Errorf("%w: update_chart type is invalid", ErrGateway)
		}
		if value == normalizeChartType(current.Type) {
			arguments.Type = nil
		} else {
			arguments.Type = &value
		}
	}
	if arguments.Title != nil {
		value := strings.TrimSpace(*arguments.Title)
		if len([]rune(value)) > 200 {
			return arguments, fmt.Errorf("%w: update_chart title is too long", ErrGateway)
		}
		if value == strings.TrimSpace(current.Title) {
			arguments.Title = nil
		} else {
			arguments.Title = &value
		}
	}
	if arguments.SourceRange != nil {
		source, err := cellrange.Parse(strings.TrimSpace(*arguments.SourceRange))
		if err != nil {
			return arguments, fmt.Errorf("%w: update_chart source_range is invalid", ErrGateway)
		}
		value := normalizedSelection(source)
		cellCount := int64(source.End.Row-source.Start.Row+1) * int64(source.End.Column-source.Start.Column+1)
		if cellCount > workbook.MaxChartSourceCells {
			return arguments, fmt.Errorf("%w: update_chart source_range may contain at most %d cells", ErrGateway, workbook.MaxChartSourceCells)
		}
		currentSource := strings.TrimSpace(current.SourceRange)
		if parsed, parseErr := cellrange.Parse(currentSource); parseErr == nil {
			currentSource = normalizedSelection(parsed)
		}
		if value == currentSource {
			arguments.SourceRange = nil
		} else if current.SourceSheetID != input.SheetID {
			return arguments, fmt.Errorf("%w: update_chart may only change a source range on the active sheet", ErrGateway)
		} else if source.Start.Row < selected.Start.Row || source.End.Row > selected.End.Row || source.Start.Column < selected.Start.Column || source.End.Column > selected.End.Column {
			return arguments, fmt.Errorf("%w: update_chart source_range must stay inside selected_range", ErrGateway)
		} else {
			arguments.SourceRange = &value
		}
	}
	if arguments.FirstRowHeaders != nil && *arguments.FirstRowHeaders == current.FirstRowHeaders {
		arguments.FirstRowHeaders = nil
	}
	if arguments.FirstColumnLabels != nil && *arguments.FirstColumnLabels == current.FirstColumnLabels {
		arguments.FirstColumnLabels = nil
	}
	if arguments.LegendPosition != nil {
		value := strings.ToLower(strings.TrimSpace(*arguments.LegendPosition))
		if value == strings.ToLower(strings.TrimSpace(current.LegendPosition)) {
			arguments.LegendPosition = nil
		} else if !validChartLegendPosition(value) {
			return arguments, fmt.Errorf("%w: update_chart legend_position is invalid", ErrGateway)
		} else {
			arguments.LegendPosition = &value
		}
	}
	for name, value := range map[string]struct {
		candidate **string
		current   string
	}{
		"x_axis_title": {candidate: &arguments.XAxisTitle, current: current.XAxisTitle},
		"y_axis_title": {candidate: &arguments.YAxisTitle, current: current.YAxisTitle},
	} {
		if *value.candidate == nil {
			continue
		}
		trimmed := strings.TrimSpace(**value.candidate)
		if len([]rune(trimmed)) > 100 {
			return arguments, fmt.Errorf("%w: update_chart %s is too long", ErrGateway, name)
		}
		if trimmed == strings.TrimSpace(value.current) {
			*value.candidate = nil
		} else {
			*value.candidate = &trimmed
		}
	}
	if arguments.Type == nil && arguments.Title == nil && arguments.SourceRange == nil && arguments.FirstRowHeaders == nil && arguments.FirstColumnLabels == nil && arguments.LegendPosition == nil && arguments.XAxisTitle == nil && arguments.YAxisTitle == nil {
		return arguments, fmt.Errorf("%w: update_chart proposed no effective change", ErrGateway)
	}
	arguments.ExpectedRevision = current.Revision
	return arguments, nil
}

func currentChartIDs(charts []workbook.Chart) string {
	ids := make([]string, 0, minInt(len(charts), 10))
	for _, chart := range charts {
		if id := strings.TrimSpace(chart.ID); id != "" {
			ids = append(ids, id)
			if len(ids) == 10 {
				break
			}
		}
	}
	if len(ids) == 0 {
		return "no current chart IDs are available"
	}
	return "available chart_ids: " + strings.Join(ids, ", ")
}

func validateCreateReportSheet(input PlanInput, raw json.RawMessage, maxChanges int) (createReportSheetArguments, error) {
	var arguments createReportSheetArguments
	if json.Unmarshal(raw, &arguments) != nil {
		return arguments, fmt.Errorf("%w: create_report_sheet arguments are invalid", ErrGateway)
	}
	arguments.Name = strings.TrimSpace(arguments.Name)
	if arguments.Name == "" || len([]rune(arguments.Name)) > 100 || strings.ContainsAny(arguments.Name, "\x00\r\n") {
		return arguments, fmt.Errorf("%w: report sheet name must contain 1 to 100 safe characters", ErrGateway)
	}
	if input.Context != nil {
		for _, sheet := range input.Context.Sheets {
			if strings.EqualFold(sheet.Name, arguments.Name) {
				return arguments, fmt.Errorf("%w: report sheet name already exists", ErrGateway)
			}
		}
	}
	if maxChanges < 1 {
		maxChanges = 100
	}
	if len(arguments.Cells) < 1 || len(arguments.Cells) > maxChanges || len(arguments.Cells) > workbook.MaxBatchCells {
		return arguments, fmt.Errorf("%w: create_report_sheet requires 1 to %d cells", ErrGateway, min(maxChanges, workbook.MaxBatchCells))
	}
	seen := make(map[string]struct{}, len(arguments.Cells))
	minRow, minColumn, maxRow, maxColumn := 1<<30, 1<<30, 0, 0
	for index := range arguments.Cells {
		cell := arguments.Cells[index]
		if cell.Row < 1 || cell.Row > 1_000_000 || cell.Column < 1 || cell.Column > 16_384 {
			return arguments, fmt.Errorf("%w: report cell coordinates are invalid", ErrGateway)
		}
		key := coordinateKey(cell.Row, cell.Column)
		if _, exists := seen[key]; exists {
			return arguments, fmt.Errorf("%w: report contains a duplicate cell", ErrGateway)
		}
		seen[key] = struct{}{}
		blank := CellSnapshot{}
		if err := applyAgentCandidate(&blank, cell); err != nil {
			return arguments, err
		}
		minRow, minColumn = min(minRow, cell.Row), min(minColumn, cell.Column)
		maxRow, maxColumn = max(maxRow, cell.Row), max(maxColumn, cell.Column)
	}
	if arguments.Chart != nil {
		arguments.Chart.Type = normalizeChartType(arguments.Chart.Type)
		if arguments.Chart.Type == "" {
			return arguments, fmt.Errorf("%w: report chart type is invalid", ErrGateway)
		}
		source, err := cellrange.Parse(strings.TrimSpace(arguments.Chart.SourceRange))
		if err != nil || source.Start.Row < minRow || source.End.Row > maxRow || source.Start.Column < minColumn || source.End.Column > maxColumn {
			return arguments, fmt.Errorf("%w: report chart source_range must stay inside the report cells", ErrGateway)
		}
		arguments.Chart.SourceRange = normalizedSelection(source)
		cellCount := int64(source.End.Row-source.Start.Row+1) * int64(source.End.Column-source.Start.Column+1)
		if cellCount > workbook.MaxChartSourceCells {
			return arguments, fmt.Errorf("%w: report chart source_range may contain at most %d cells", ErrGateway, workbook.MaxChartSourceCells)
		}
		arguments.Chart.Title = strings.TrimSpace(arguments.Chart.Title)
		arguments.Chart.XAxisTitle = strings.TrimSpace(arguments.Chart.XAxisTitle)
		arguments.Chart.YAxisTitle = strings.TrimSpace(arguments.Chart.YAxisTitle)
		if len([]rune(arguments.Chart.Title)) > 200 || len([]rune(arguments.Chart.XAxisTitle)) > 100 || len([]rune(arguments.Chart.YAxisTitle)) > 100 {
			return arguments, fmt.Errorf("%w: report chart title or axis title is too long", ErrGateway)
		}
		arguments.Chart.LegendPosition = strings.ToLower(strings.TrimSpace(arguments.Chart.LegendPosition))
		if arguments.Chart.LegendPosition == "" {
			arguments.Chart.LegendPosition = "right"
		}
		if !validChartLegendPosition(arguments.Chart.LegendPosition) {
			return arguments, fmt.Errorf("%w: report chart legend_position is invalid", ErrGateway)
		}
	}
	return arguments, nil
}

func normalizeChartType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if !map[string]bool{"bar": true, "line": true, "area": true, "pie": true, "scatter": true, "histogram": true}[value] {
		return ""
	}
	return value
}

func normalizedSelection(selected cellrange.Range) string {
	start, end := cellrange.Address(selected.Start.Row, selected.Start.Column), cellrange.Address(selected.End.Row, selected.End.Column)
	if start == end {
		return start
	}
	return start + ":" + end
}

func buildPlanSteps(mode, risk string, changes []ProposedChange, tools []ToolCall) []PlanStep {
	steps := []PlanStep{
		{Position: 1, ToolName: "get_workbook", Description: "워크북과 현재 시트 구조 확인", Status: "completed", Risk: RiskRead},
		{Position: 2, ToolName: "get_selection", Description: "선택 범위와 의미 구조 분석", Status: "completed", Risk: RiskRead},
	}
	position := 3
	if len(changes) > 0 {
		name := approvalToolName(mode)
		steps = append(steps, PlanStep{Position: position, ToolName: name, Description: fmt.Sprintf("선택 범위의 %d개 셀 변경", len(changes)), Status: "waiting_approval", Risk: risk})
		position++
	}
	for _, tool := range tools {
		description := "차트 소스 검증 후 워크북에 차트 생성"
		if tool.Name == "update_chart" {
			description = "현재 차트 리비전을 확인하고 요청한 속성 변경"
		} else if tool.Name == "create_report_sheet" {
			description = "새 보고서 시트에 수식과 차트를 함께 생성"
		}
		steps = append(steps, PlanStep{Position: position, ToolName: tool.Name, Description: description, Status: "waiting_approval", Risk: tool.Risk, Arguments: cloneRaw(tool.Arguments)})
		position++
	}
	steps = append(steps, PlanStep{Position: position, ToolName: "validate_changeset", Description: "적용 결과와 수식 오류 검증", Status: "pending", Risk: RiskRead})
	return steps
}

func validateFindings(mode string, selected cellrange.Range, current map[string]workbook.Cell, candidates []gatewayFinding, limit int) ([]Finding, error) {
	if mode == ModeExplain && len(candidates) != 0 {
		return nil, fmt.Errorf("%w: explain mode cannot return findings", ErrGateway)
	}
	if len(candidates) > limit {
		return nil, fmt.Errorf("%w: model returned more than %d findings", ErrGateway, limit)
	}
	findings := make([]Finding, 0, len(candidates))
	for _, candidate := range candidates {
		severity := strings.ToLower(strings.TrimSpace(candidate.Severity))
		if severity != "info" && severity != "warning" && severity != "critical" {
			return nil, fmt.Errorf("%w: finding severity must be info, warning or critical", ErrGateway)
		}
		title := trimLength(candidate.Title, 200)
		description := trimLength(candidate.Description, 2000)
		if title == "" || description == "" {
			return nil, fmt.Errorf("%w: every finding requires a title and description", ErrGateway)
		}
		finding := Finding{Row: candidate.Row, Column: candidate.Column, Severity: severity, Title: title, Description: description}
		if candidate.Row == 0 && candidate.Column == 0 {
			if mode != ModeSummarize {
				return nil, fmt.Errorf("%w: anomaly findings must identify a selected cell", ErrGateway)
			}
		} else {
			if candidate.Row < selected.Start.Row || candidate.Row > selected.End.Row || candidate.Column < selected.Start.Column || candidate.Column > selected.End.Column {
				return nil, fmt.Errorf("%w: model returned a finding outside selected range", ErrGateway)
			}
			finding.Address = cellrange.Address(candidate.Row, candidate.Column)
			finding.Cell = snapshotFromCell(current[coordinateKey(candidate.Row, candidate.Column)])
		}
		findings = append(findings, finding)
	}
	return findings, nil
}

func snapshotsEqual(left, right CellSnapshot) bool {
	return bytes.Equal(bytes.TrimSpace(left.Value), bytes.TrimSpace(right.Value)) && left.Formula == right.Formula && bytes.Equal(bytes.TrimSpace(left.Style), bytes.TrimSpace(right.Style)) && left.SpillSource == right.SpillSource
}

func approvalToolName(mode string) string {
	if mode == ModeClean {
		return "data.clean"
	}
	if mode == ModeFormat {
		return "range.format"
	}
	if mode == ModeChart {
		return "chart.create"
	}
	if mode == ModeAgent {
		return "agent.execute"
	}
	return "formula.set"
}

func snapshotFromCell(cell workbook.Cell) CellSnapshot {
	return CellSnapshot{Value: cloneRaw(cell.Value), Formula: cell.Formula, Style: cloneRaw(cell.Style), SpillSource: cell.SpillSource}
}

func sheetBelongsToWorkbook(book workbook.Workbook, sheetID string) bool {
	for _, sheet := range book.Sheets {
		if sheet.ID == sheetID {
			return true
		}
	}
	return false
}

func executionKey(actionID, phase, key string) string {
	return "ai:" + actionID + ":" + phase + ":" + key
}
func coordinateKey(row, column int) string { return fmt.Sprintf("%d:%d", row, column) }

func trimLength(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func normalizePlanInput(input PlanInput) PlanInput {
	input.WorkbookID = strings.TrimSpace(input.WorkbookID)
	input.SheetID = strings.TrimSpace(input.SheetID)
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	input.Mode = strings.TrimSpace(input.Mode)
	input.Skill = strings.TrimSpace(input.Skill)
	input.Range = strings.TrimSpace(input.Range)
	input.Request = strings.TrimSpace(input.Request)
	return input
}

func normalizeApprovalInput(input ApprovalInput) ApprovalInput {
	input.ActorID = strings.TrimSpace(input.ActorID)
	input.ClientID = strings.TrimSpace(input.ClientID)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	return input
}

func samePlanRequest(action Action, input PlanInput) bool {
	return action.WorkbookID == input.WorkbookID && action.SheetID == input.SheetID &&
		action.Mode == input.Mode && action.Range == input.Range && action.Request == input.Request &&
		action.BaseVersion == input.BaseVersion
}

func nullableUUID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}
