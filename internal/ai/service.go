package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const actionColumns = `id::text,workbook_id::text,sheet_id::text,actor_id,client_id,idempotency_key,mode,selected_range,request,status,base_version,model,summary,explanation,changes,input_cell_count,revision,approval_idempotency_key,coalesce(operation_id::text,''),operation_result,undo_idempotency_key,coalesce(undo_operation_id::text,''),undo_result,error_message,approved_at,undone_at,created_at,updated_at`

type Service struct {
	pool       *pgxpool.Pool
	settings   settingsProvider
	workbooks  workbook.Repository
	logger     *slog.Logger
	httpClient *http.Client
	now        func() time.Time
}

func NewService(pool *pgxpool.Pool, settings settingsProvider, workbooks workbook.Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, settings: settings, workbooks: workbooks, logger: logger, now: func() time.Time { return time.Now().UTC() }}
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
	planContext, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()
	generated, err := requestGatewayPlan(planContext, client, config, input, selected, cells)
	if err != nil {
		s.logger.Warn("AI plan gateway failed", "actor_id", input.ActorID, "workbook_id", input.WorkbookID, "error", err)
		return Action{}, err
	}
	changes, err := validateGatewayPlan(input.Mode, selected, cells, generated, config.MaxChanges)
	if err != nil {
		return Action{}, err
	}
	now := s.now()
	status := StatusPlanned
	eventType := "planned"
	if input.Mode == ModeExplain {
		status = StatusCompleted
		eventType = "completed"
	}
	action := Action{
		ID: identity.New(), WorkbookID: input.WorkbookID, SheetID: input.SheetID,
		ActorID: input.ActorID, ClientID: input.ClientID, IdempotencyKey: input.IdempotencyKey,
		Mode: input.Mode, Range: input.Range, Request: input.Request, Status: status,
		BaseVersion: book.Version, Model: config.Model, Summary: trimLength(generated.Summary, 2000),
		Explanation: trimLength(generated.Explanation, 12000), Changes: changes,
		InputCellCount: rows * columns, Revision: 1, CreatedAt: now, UpdatedAt: now,
	}
	encodedChanges, _ := json.Marshal(action.Changes)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Action{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `INSERT INTO ai_actions(id,workbook_id,sheet_id,actor_id,client_id,idempotency_key,mode,selected_range,request,status,base_version,model,summary,explanation,changes,input_cell_count,revision,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,1,$17,$17) ON CONFLICT(actor_id,idempotency_key) DO NOTHING`, action.ID, action.WorkbookID, action.SheetID, action.ActorID, action.ClientID, action.IdempotencyKey, action.Mode, action.Range, action.Request, action.Status, action.BaseVersion, action.Model, action.Summary, action.Explanation, encodedChanges, action.InputCellCount, now)
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
	payload, _ := json.Marshal(map[string]any{"mode": action.Mode, "range": action.Range, "request": action.Request, "changes": len(action.Changes), "input_cell_count": action.InputCellCount})
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
	s.logger.Info("AI action planned", "action_id", action.ID, "actor_id", action.ActorID, "workbook_id", action.WorkbookID, "model", action.Model, "changes", len(action.Changes))
	return action, nil
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
	if action.Mode == ModeExplain || len(action.Changes) == 0 {
		return ExecutionResult{}, fmt.Errorf("%w: explain-only actions cannot be approved", ErrInvalid)
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
		s.markFailed(ctx, action, input.ActorID, "approval_failed", "formula.set", err)
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
	if err := insertEvent(ctx, tx, action.ID, action.ActorID, "applied", action.Model, "formula.set", payload, now); err != nil {
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
	var changes, operation, undo []byte
	err := row.Scan(&action.ID, &action.WorkbookID, &action.SheetID, &action.ActorID, &action.ClientID, &action.IdempotencyKey, &action.Mode, &action.Range, &action.Request, &action.Status, &action.BaseVersion, &action.Model, &action.Summary, &action.Explanation, &changes, &action.InputCellCount, &action.Revision, &action.approvalIdempotencyKey, &action.OperationID, &operation, &action.undoIdempotencyKey, &action.UndoOperationID, &undo, &action.ErrorMessage, &action.ApprovedAt, &action.UndoneAt, &action.CreatedAt, &action.UpdatedAt)
	if err != nil {
		return Action{}, err
	}
	if err := json.Unmarshal(changes, &action.Changes); err != nil {
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
	if input.Mode != ModeFormula && input.Mode != ModeExplain && input.Mode != ModeFix {
		return fmt.Errorf("%w: mode must be formula, explain or fix", ErrInvalid)
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

func validateGatewayPlan(mode string, selected cellrange.Range, cells []workbook.Cell, plan gatewayPlan, maxChanges int) ([]ProposedChange, error) {
	plan.Summary = strings.TrimSpace(plan.Summary)
	plan.Explanation = strings.TrimSpace(plan.Explanation)
	if plan.Summary == "" {
		return nil, fmt.Errorf("%w: model plan summary is required", ErrGateway)
	}
	if mode == ModeExplain {
		if len(plan.Changes) != 0 {
			return nil, fmt.Errorf("%w: explain mode cannot propose changes", ErrGateway)
		}
		if plan.Explanation == "" {
			return nil, fmt.Errorf("%w: explain mode requires an explanation", ErrGateway)
		}
		return []ProposedChange{}, nil
	}
	if len(plan.Changes) < 1 || len(plan.Changes) > maxChanges {
		return nil, fmt.Errorf("%w: model must propose 1 to %d formula changes", ErrGateway, maxChanges)
	}
	current := make(map[string]workbook.Cell, len(cells))
	for _, cell := range cells {
		current[coordinateKey(cell.Row, cell.Column)] = cell
	}
	seen := make(map[string]struct{}, len(plan.Changes))
	changes := make([]ProposedChange, 0, len(plan.Changes))
	for _, candidate := range plan.Changes {
		if candidate.Row < selected.Start.Row || candidate.Row > selected.End.Row || candidate.Column < selected.Start.Column || candidate.Column > selected.End.Column {
			return nil, fmt.Errorf("%w: model proposed a cell outside selected range", ErrGateway)
		}
		formula := strings.TrimSpace(candidate.Formula)
		if !strings.HasPrefix(formula, "=") || len(formula) > 8192 {
			return nil, fmt.Errorf("%w: every proposed formula must begin with '=' and be at most 8192 characters", ErrGateway)
		}
		key := coordinateKey(candidate.Row, candidate.Column)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%w: model proposed the same cell more than once", ErrGateway)
		}
		seen[key] = struct{}{}
		before := snapshotFromCell(current[key])
		after := CellSnapshot{Formula: formula, Style: cloneRaw(before.Style)}
		changes = append(changes, ProposedChange{Row: candidate.Row, Column: candidate.Column, Address: cellrange.Address(candidate.Row, candidate.Column), Before: before, After: after})
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].Row == changes[j].Row {
			return changes[i].Column < changes[j].Column
		}
		return changes[i].Row < changes[j].Row
	})
	return changes, nil
}

func actionCellInputs(action Action) ([]workbook.CellInput, map[string]workbook.Cell) {
	cells := make([]workbook.CellInput, 0, len(action.Changes))
	expected := make(map[string]workbook.Cell, len(action.Changes))
	for _, change := range action.Changes {
		cells = append(cells, workbook.CellInput{Row: change.Row, Column: change.Column, Formula: change.After.Formula, Style: cloneRaw(change.Before.Style)})
		expected[coordinateKey(change.Row, change.Column)] = workbook.Cell{SheetID: action.SheetID, Row: change.Row, Column: change.Column, Value: cloneRaw(change.Before.Value), Formula: change.Before.Formula, Style: cloneRaw(change.Before.Style), SpillSource: change.Before.SpillSource}
	}
	return cells, expected
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
