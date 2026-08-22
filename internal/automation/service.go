package automation

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
	"kanpic/pkg/identity"
)

const (
	defaultMaxCells           = 1_000
	defaultMaxRuns            = 100
	defaultSchedulerPoll      = 15 * time.Second
	defaultSchedulerBatch     = 50
	maxExecutionSnapshotBytes = 8 << 20
	definitionColumns         = `id::text,workbook_id::text,name,enabled,trigger_definition,action_definition,revision,idempotency_key,created_by,updated_by,created_at,updated_at,next_run_at`
	dueDefinitionColumns      = `a.id::text,a.workbook_id::text,a.name,a.enabled,a.trigger_definition,a.action_definition,a.revision,a.idempotency_key,a.created_by,a.updated_by,a.created_at,a.updated_at,a.next_run_at`
	runColumns                = `id::text,automation_id::text,workbook_id::text,actor_id,idempotency_key,trigger_type,coalesce(trigger_operation_id::text,''),scheduled_for,coalesce(trigger_key_id::text,''),payload_digest,payload_bytes,status,counts_toward_rate,base_version,action_snapshot,cells_snapshot,expected_snapshot,coalesce(operation_id::text,''),operation_result,undo_idempotency_key,coalesce(undo_operation_id::text,''),undo_result,error_message,started_at,completed_at,updated_at`
	scheduledAutomationActor  = "system:scheduler"
)

type settingsProvider interface {
	Values(context.Context) (map[string]any, error)
}

type runtimeConfig struct {
	Enabled        bool
	MaxCellsPerRun int
	MaxRunsPerHour int
	SchedulerPoll  time.Duration
}

type Service struct {
	pool              *pgxpool.Pool
	settings          settingsProvider
	books             workbook.Repository
	logger            *slog.Logger
	now               func() time.Time
	scheduledListener func(ExecutionResult)
}

func NewService(pool *pgxpool.Pool, settings settingsProvider, books workbook.Repository, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{pool: pool, settings: settings, books: books, logger: logger, now: func() time.Time { return time.Now().UTC() }}
}

func (s *Service) SetScheduledExecutionListener(listener func(ExecutionResult)) {
	s.scheduledListener = listener
}

func (s *Service) Create(ctx context.Context, workbookID, actorID string, input CreateInput) (Automation, error) {
	if strings.TrimSpace(actorID) == "" || !validIdempotencyKey(input.IdempotencyKey) {
		return Automation{}, fmt.Errorf("%w: actor and a 1 to 200 character idempotency_key are required", ErrInvalid)
	}
	config, err := s.config(ctx)
	if err != nil {
		return Automation{}, err
	}
	name, trigger, action, err := s.validateDefinition(ctx, workbookID, input.Name, input.Trigger, input.Action, config.MaxCellsPerRun)
	if err != nil {
		return Automation{}, err
	}
	now := s.now()
	nextRunAt, err := nextScheduleTime(trigger, now)
	if err != nil {
		return Automation{}, err
	}
	if !input.Enabled {
		nextRunAt = nil
	}
	item := Automation{ID: identity.New(), WorkbookID: workbookID, Name: name, Enabled: input.Enabled, Trigger: trigger, Action: action, Revision: 1, CreatedBy: actorID, UpdatedBy: actorID, CreatedAt: now, UpdatedAt: now, NextRunAt: nextRunAt, idempotencyKey: input.IdempotencyKey}
	triggerJSON, _ := json.Marshal(trigger)
	actionJSON, _ := json.Marshal(action)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Automation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `INSERT INTO automations(id,workbook_id,name,enabled,trigger_definition,action_definition,revision,idempotency_key,created_by,updated_by,created_at,updated_at,next_run_at) VALUES($1,$2,$3,$4,$5,$6,1,$7,$8,$8,$9,$9,$10) ON CONFLICT(workbook_id,created_by,idempotency_key) DO NOTHING`, item.ID, workbookID, name, input.Enabled, triggerJSON, actionJSON, input.IdempotencyKey, actorID, now, item.NextRunAt)
	if err != nil {
		return Automation{}, mapConstraintError(err)
	}
	if command.RowsAffected() == 0 {
		duplicate, err := scanAutomation(tx.QueryRow(ctx, `SELECT `+definitionColumns+` FROM automations WHERE workbook_id=$1 AND created_by=$2 AND idempotency_key=$3 AND deleted_at IS NULL`, workbookID, actorID, input.IdempotencyKey))
		if errors.Is(err, ErrNotFound) {
			return Automation{}, fmt.Errorf("%w: create replay targets a deleted automation", ErrRevision)
		}
		if err != nil {
			return Automation{}, err
		}
		duplicate.Duplicate = true
		return duplicate, tx.Commit(ctx)
	}
	if err := insertAudit(ctx, tx, actorID, "automation.create", item.ID, map[string]any{"workbook_id": workbookID, "name": name, "trigger": trigger.Type, "action": action.Type}, now); err != nil {
		return Automation{}, err
	}
	return item, tx.Commit(ctx)
}

func (s *Service) List(ctx context.Context, workbookID string) ([]Automation, error) {
	rows, err := s.pool.Query(ctx, `SELECT `+definitionColumns+` FROM automations WHERE workbook_id=$1 AND deleted_at IS NULL ORDER BY updated_at DESC,id`, workbookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Automation, 0)
	for rows.Next() {
		item, err := scanAutomation(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

// Overview answers the automation panel: the definitions, whichever of them
// last failed, and whether the admin switch lets any of them run at all.
func (s *Service) Overview(ctx context.Context, workbookID string) (Overview, error) {
	items, err := s.List(ctx, workbookID)
	if err != nil {
		return Overview{}, err
	}
	config, err := s.config(ctx)
	if err != nil {
		return Overview{}, err
	}
	failures, err := s.latestFailures(ctx, workbookID)
	if err != nil {
		return Overview{}, err
	}
	for index := range items {
		if failure, ok := failures[items[index].ID]; ok {
			items[index].LastFailure = &failure
		}
	}
	return Overview{Items: items, ExecutionEnabled: config.Enabled}, nil
}

// latestFailures reports only automations whose most recent run failed: a later
// success means the automation recovered and the panel should stop warning.
func (s *Service) latestFailures(ctx context.Context, workbookID string) (map[string]RunFailure, error) {
	rows, err := s.pool.Query(ctx, `SELECT a.id::text,r.id::text,r.trigger_type,r.error_message,r.started_at FROM automations a JOIN LATERAL (SELECT id,trigger_type,status,error_message,started_at FROM automation_runs WHERE automation_id=a.id ORDER BY started_at DESC,id LIMIT 1) r ON true WHERE a.workbook_id=$1 AND a.deleted_at IS NULL AND r.status=$2`, workbookID, StatusFailed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	failures := make(map[string]RunFailure)
	for rows.Next() {
		var automationID string
		var failure RunFailure
		if err := rows.Scan(&automationID, &failure.RunID, &failure.TriggerType, &failure.Message, &failure.At); err != nil {
			return nil, err
		}
		failures[automationID] = failure
	}
	return failures, rows.Err()
}

func (s *Service) Get(ctx context.Context, id string) (Automation, error) {
	return scanAutomation(s.pool.QueryRow(ctx, `SELECT `+definitionColumns+` FROM automations WHERE id=$1 AND deleted_at IS NULL`, id))
}

func (s *Service) Update(ctx context.Context, id, actorID string, input UpdateInput) (Automation, error) {
	if input.ExpectedRevision < 1 || strings.TrimSpace(actorID) == "" {
		return Automation{}, fmt.Errorf("%w: actor and positive expected_revision are required", ErrInvalid)
	}
	current, err := s.Get(ctx, id)
	if err != nil {
		return Automation{}, err
	}
	config, err := s.config(ctx)
	if err != nil {
		return Automation{}, err
	}
	name, trigger, action, err := s.validateDefinition(ctx, current.WorkbookID, input.Name, input.Trigger, input.Action, config.MaxCellsPerRun)
	if err != nil {
		return Automation{}, err
	}
	triggerJSON, _ := json.Marshal(trigger)
	actionJSON, _ := json.Marshal(action)
	now := s.now()
	nextRunAt, err := nextScheduleTime(trigger, now)
	if err != nil {
		return Automation{}, err
	}
	if !input.Enabled {
		nextRunAt = nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Automation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	row := tx.QueryRow(ctx, `UPDATE automations SET name=$2,enabled=$3,trigger_definition=$4,action_definition=$5,revision=revision+1,updated_by=$6,updated_at=$7,next_run_at=$9 WHERE id=$1 AND deleted_at IS NULL AND revision=$8 RETURNING `+definitionColumns, id, name, input.Enabled, triggerJSON, actionJSON, actorID, now, input.ExpectedRevision, nextRunAt)
	item, err := scanAutomation(row)
	if errors.Is(err, ErrNotFound) {
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return Automation{}, getErr
		}
		return Automation{}, ErrRevision
	}
	if err != nil {
		return Automation{}, mapConstraintError(err)
	}
	if err := insertAudit(ctx, tx, actorID, "automation.update", id, map[string]any{"revision": item.Revision, "enabled": item.Enabled}, now); err != nil {
		return Automation{}, err
	}
	return item, tx.Commit(ctx)
}

func (s *Service) Delete(ctx context.Context, id, actorID string, expectedRevision int64) error {
	if expectedRevision < 1 || strings.TrimSpace(actorID) == "" {
		return fmt.Errorf("%w: actor and positive expected_revision are required", ErrInvalid)
	}
	now := s.now()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE automations SET deleted_at=$3,enabled=false,revision=revision+1,updated_by=$2,updated_at=$3 WHERE id=$1 AND deleted_at IS NULL AND revision=$4`, id, actorID, now, expectedRevision)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		if _, getErr := s.Get(ctx, id); getErr != nil {
			return getErr
		}
		return ErrRevision
	}
	if err := insertAudit(ctx, tx, actorID, "automation.delete", id, map[string]any{"revision": expectedRevision}, now); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) Preview(ctx context.Context, id string) (Preview, error) {
	item, err := s.Get(ctx, id)
	if err != nil {
		return Preview{}, err
	}
	config, err := s.config(ctx)
	if err != nil {
		return Preview{}, err
	}
	preview, err := s.buildPreview(ctx, item, config.MaxCellsPerRun)
	if errors.Is(err, ErrNoChanges) {
		return preview, nil
	}
	return preview, err
}

func (s *Service) Run(ctx context.Context, id string, input RunInput) (ExecutionResult, error) {
	if strings.TrimSpace(input.ActorID) == "" || !validIdempotencyKey(input.IdempotencyKey) {
		return ExecutionResult{}, fmt.Errorf("%w: actor and a 1 to 200 character idempotency_key are required", ErrInvalid)
	}
	triggerType := input.TriggerType
	if triggerType == "" {
		triggerType = TriggerManual
	}
	if input.ScheduledFor != nil {
		if existing, err := s.getRunBySchedule(ctx, id, *input.ScheduledFor); err == nil {
			if existing.Status == StatusRunning {
				return s.executeRun(ctx, existing, input.ClientID)
			}
			return replayRun(existing)
		} else if !errors.Is(err, ErrNotFound) {
			return ExecutionResult{}, err
		}
	}
	if existing, err := s.getRunByKey(ctx, id, input.ActorID, input.IdempotencyKey); err == nil {
		if existing.TriggerType != triggerType {
			return ExecutionResult{}, fmt.Errorf("%w: idempotency_key was already used by another trigger", ErrInvalid)
		}
		if existing.Status == StatusRunning {
			return s.executeRun(ctx, existing, input.ClientID)
		}
		return replayRun(existing)
	} else if !errors.Is(err, ErrNotFound) {
		return ExecutionResult{}, err
	}
	if input.TriggerOperationID != "" {
		if existing, err := s.getRunByTrigger(ctx, id, input.TriggerOperationID); err == nil {
			if existing.Status == StatusRunning {
				return s.executeRun(ctx, existing, input.ClientID)
			}
			return replayRun(existing)
		} else if !errors.Is(err, ErrNotFound) {
			return ExecutionResult{}, err
		}
	}
	item, err := s.Get(ctx, id)
	if err != nil {
		return ExecutionResult{}, err
	}
	if triggerType == TriggerManual {
		if input.ExpectedRevision < 1 || input.ExpectedBaseVersion < 1 {
			return ExecutionResult{}, fmt.Errorf("%w: manual run requires positive expected_revision and expected_base_version from preview", ErrInvalid)
		}
		if input.ExpectedRevision != item.Revision {
			return ExecutionResult{}, ErrRevision
		}
	} else if input.ExpectedRevision > 0 && input.ExpectedRevision != item.Revision {
		return ExecutionResult{}, ErrRevision
	}
	config, err := s.config(ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	if !config.Enabled || !item.Enabled {
		return ExecutionResult{}, ErrDisabled
	}
	if triggerType != TriggerManual && triggerType != TriggerCellChange && triggerType != TriggerSchedule && triggerType != TriggerWebhook {
		return ExecutionResult{}, fmt.Errorf("%w: unsupported run trigger", ErrInvalid)
	}
	if triggerType == TriggerCellChange && item.Trigger.Type != TriggerCellChange {
		return ExecutionResult{}, fmt.Errorf("%w: automation does not use a cell-change trigger", ErrInvalid)
	}
	if triggerType == TriggerSchedule && (item.Trigger.Type != TriggerSchedule || input.ScheduledFor == nil) {
		return ExecutionResult{}, fmt.Errorf("%w: scheduled run requires a schedule trigger and due time", ErrInvalid)
	}
	if triggerType == TriggerWebhook {
		if item.Trigger.Type != TriggerWebhook || strings.TrimSpace(input.TriggerKeyID) == "" {
			return ExecutionResult{}, fmt.Errorf("%w: webhook run requires a webhook trigger and API key", ErrInvalid)
		}
		decoded, digestErr := hex.DecodeString(input.PayloadDigest)
		if digestErr != nil || len(decoded) != 32 || input.PayloadBytes < 0 || input.PayloadBytes > 1<<20 {
			return ExecutionResult{}, fmt.Errorf("%w: invalid webhook payload metadata", ErrInvalid)
		}
	}
	preview, cells, expected, err := s.buildExecution(ctx, item, config.MaxCellsPerRun)
	noChanges := errors.Is(err, ErrNoChanges)
	if err != nil && !noChanges {
		return ExecutionResult{}, err
	}
	if triggerType == TriggerManual && preview.BaseVersion != input.ExpectedBaseVersion {
		return ExecutionResult{}, workbook.ErrVersionConflict
	}
	skipped := triggerType != TriggerManual && noChanges
	if triggerType == TriggerManual && noChanges {
		return ExecutionResult{}, err
	}
	now := s.now()
	status := StatusRunning
	var completedAt *time.Time
	if skipped {
		status, completedAt = StatusSkipped, &now
	}
	run := Run{ID: identity.New(), AutomationID: item.ID, WorkbookID: item.WorkbookID, ActorID: input.ActorID, TriggerType: triggerType, TriggerOperationID: input.TriggerOperationID, ScheduledFor: cloneTime(input.ScheduledFor), TriggerKeyID: input.TriggerKeyID, PayloadDigest: input.PayloadDigest, PayloadBytes: input.PayloadBytes, Status: status, BaseVersion: preview.BaseVersion, Action: item.Action, StartedAt: now, CompletedAt: completedAt, UpdatedAt: now, cells: cells, expected: expected, idempotencyKey: input.IdempotencyKey, automationRevision: item.Revision}
	created, err := s.insertRun(ctx, run, config.MaxRunsPerHour)
	if err != nil {
		if isUniqueViolation(err) && run.TriggerOperationID != "" {
			duplicate, getErr := s.getRunByTrigger(ctx, run.AutomationID, run.TriggerOperationID)
			if getErr != nil {
				return ExecutionResult{}, getErr
			}
			if duplicate.Status == StatusRunning {
				return s.executeRun(ctx, duplicate, input.ClientID)
			}
			return replayRun(duplicate)
		}
		if isUniqueViolation(err) && run.ScheduledFor != nil {
			duplicate, getErr := s.getRunBySchedule(ctx, run.AutomationID, *run.ScheduledFor)
			if getErr != nil {
				return ExecutionResult{}, getErr
			}
			if duplicate.Status == StatusRunning {
				return s.executeRun(ctx, duplicate, input.ClientID)
			}
			return replayRun(duplicate)
		}
		return ExecutionResult{}, err
	}
	if !created {
		duplicate, err := s.getRunByKey(ctx, item.ID, input.ActorID, input.IdempotencyKey)
		if err != nil {
			return ExecutionResult{}, err
		}
		if duplicate.Status == StatusRunning {
			return s.executeRun(ctx, duplicate, input.ClientID)
		}
		return replayRun(duplicate)
	}
	if skipped {
		s.logger.Info("automation run skipped", "automation_id", run.AutomationID, "run_id", run.ID, "trigger", run.TriggerType, "scheduled_for", run.ScheduledFor)
		return ExecutionResult{Run: run}, nil
	}
	return s.executeRun(ctx, run, input.ClientID)
}

func (s *Service) insertRun(ctx context.Context, run Run, hourlyLimit int) (bool, error) {
	actionJSON, _ := json.Marshal(run.Action)
	cellsJSON, _ := json.Marshal(run.cells)
	expectedJSON, _ := json.Marshal(run.expected)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if run.automationRevision > 0 {
		var currentRevision int64
		var enabled bool
		err := tx.QueryRow(ctx, `SELECT revision,enabled FROM automations WHERE id=$1 AND deleted_at IS NULL FOR SHARE`, run.AutomationID).Scan(&currentRevision, &enabled)
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		if err != nil {
			return false, err
		}
		if currentRevision != run.automationRevision {
			return false, ErrRevision
		}
		if !enabled {
			return false, ErrDisabled
		}
	}
	if hourlyLimit > 0 {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, run.WorkbookID); err != nil {
			return false, err
		}
		var duplicate bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM automation_runs WHERE automation_id=$1 AND actor_id=$2 AND idempotency_key=$3)`, run.AutomationID, run.ActorID, run.idempotencyKey).Scan(&duplicate); err != nil {
			return false, err
		}
		if !duplicate {
			var count int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM automation_runs WHERE workbook_id=$1 AND counts_toward_rate AND started_at >= now()-interval '1 hour'`, run.WorkbookID).Scan(&count); err != nil {
				return false, err
			}
			if count >= hourlyLimit {
				return false, fmt.Errorf("%w: workbook allows %d runs per hour", ErrRate, hourlyLimit)
			}
		}
	}
	command, err := tx.Exec(ctx, `INSERT INTO automation_runs(id,automation_id,workbook_id,actor_id,idempotency_key,trigger_type,trigger_operation_id,scheduled_for,trigger_key_id,payload_digest,payload_bytes,status,counts_toward_rate,base_version,action_snapshot,cells_snapshot,expected_snapshot,error_message,started_at,completed_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,nullif($7,'')::uuid,$8,nullif($9,'')::uuid,$10,$11,$12,$13,$14,$15,$16,$17,$20,$18,$19,$18) ON CONFLICT(automation_id,actor_id,idempotency_key) DO NOTHING`, run.ID, run.AutomationID, run.WorkbookID, run.ActorID, run.idempotencyKey, run.TriggerType, run.TriggerOperationID, run.ScheduledFor, run.TriggerKeyID, run.PayloadDigest, run.PayloadBytes, run.Status, !run.excludedFromRate, run.BaseVersion, actionJSON, cellsJSON, expectedJSON, run.StartedAt, run.CompletedAt, run.ErrorMessage)
	if err != nil {
		return false, err
	}
	if command.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	if run.Status == StatusSkipped || run.Status == StatusFailed {
		action := "automation.run.skip"
		if run.Status == StatusFailed {
			action = "automation.run.failed"
		}
		if err := insertAudit(ctx, tx, run.ActorID, action, run.ID, runAuditMetadata(run, map[string]any{"base_version": run.BaseVersion, "error": run.ErrorMessage}), run.UpdatedAt); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

func (s *Service) ListRuns(ctx context.Context, automationID string, limit int) ([]Run, error) {
	if limit < 1 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE automation_id=$1 ORDER BY started_at DESC,id LIMIT $2`, automationID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Run, 0)
	for rows.Next() {
		item, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Service) GetRunWorkbookID(ctx context.Context, id string) (string, error) {
	var workbookID string
	err := s.pool.QueryRow(ctx, `SELECT workbook_id::text FROM automation_runs WHERE id=$1`, id).Scan(&workbookID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return workbookID, err
}

func (s *Service) Undo(ctx context.Context, runID string, input RunInput) (ExecutionResult, error) {
	if strings.TrimSpace(input.ActorID) == "" || !validIdempotencyKey(input.IdempotencyKey) {
		return ExecutionResult{}, fmt.Errorf("%w: actor and a 1 to 200 character idempotency_key are required", ErrInvalid)
	}
	run, err := s.getRun(ctx, runID)
	if err != nil {
		return ExecutionResult{}, err
	}
	if run.Status == StatusUndone && run.undoKey == input.IdempotencyKey && run.UndoOperation != nil {
		run.Duplicate = true
		return ExecutionResult{Run: run, Operation: *run.UndoOperation}, nil
	}
	if run.Status != StatusSucceeded && !(run.Status == StatusUndoing && run.undoKey == input.IdempotencyKey) {
		return ExecutionResult{}, fmt.Errorf("%w: only a succeeded run can be undone", ErrInvalid)
	}
	if run.Status == StatusSucceeded {
		command, err := s.pool.Exec(ctx, `UPDATE automation_runs SET status='undoing',undo_idempotency_key=$2,updated_at=$3 WHERE id=$1 AND status='succeeded'`, run.ID, input.IdempotencyKey, s.now())
		if err != nil {
			return ExecutionResult{}, err
		}
		if command.RowsAffected() != 1 {
			return ExecutionResult{}, ErrRevision
		}
		run.Status, run.undoKey = StatusUndoing, input.IdempotencyKey
	}
	result, err := s.books.UndoOperation(ctx, workbook.UndoOperationInput{OperationID: run.OperationID, ActorID: automationActor(run.AutomationID), ClientID: input.ClientID, IdempotencyKey: "automation-undo:" + run.ID})
	if err != nil {
		resetContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, _ = s.pool.Exec(resetContext, `UPDATE automation_runs SET status='succeeded',error_message=$2,updated_at=$3 WHERE id=$1 AND status='undoing'`, run.ID, trimLength(err.Error(), 2000), s.now())
		return ExecutionResult{}, err
	}
	now := s.now()
	payload, _ := json.Marshal(result)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE automation_runs SET status='undone',undo_operation_id=$2,undo_result=$3,error_message='',completed_at=$4,updated_at=$4 WHERE id=$1 AND status='undoing' AND undo_idempotency_key=$5`, run.ID, result.OperationID, payload, now, input.IdempotencyKey)
	if err != nil {
		return ExecutionResult{}, err
	}
	if command.RowsAffected() != 1 {
		return ExecutionResult{}, ErrRevision
	}
	if err := insertAudit(ctx, tx, input.ActorID, "automation.run.undo", run.ID, map[string]any{"automation_id": run.AutomationID, "operation_id": result.OperationID, "server_version": result.ServerVersion}, now); err != nil {
		return ExecutionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionResult{}, err
	}
	run.Status, run.UndoOperationID, run.UndoOperation, run.ErrorMessage, run.CompletedAt, run.UpdatedAt = StatusUndone, result.OperationID, &result, "", &now, now
	return ExecutionResult{Run: run, Operation: result}, nil
}

func (s *Service) TriggerCellChange(ctx context.Context, mutation workbook.MutationResult, changed []workbook.CellInput, actorID string) ([]ExecutionResult, error) {
	if mutation.Duplicate || mutation.AppliedCells == 0 || mutation.OperationID == "" || len(changed) == 0 {
		return []ExecutionResult{}, nil
	}
	config, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	if !config.Enabled {
		return []ExecutionResult{}, nil
	}
	items, err := s.List(ctx, mutation.WorkbookID)
	if err != nil {
		return nil, err
	}
	results := make([]ExecutionResult, 0)
	errs := make([]error, 0)
	for _, item := range items {
		if !item.Enabled || item.Trigger.Type != TriggerCellChange || item.Trigger.SheetID != mutation.SheetID || !triggerMatches(item.Trigger.Range, changed) {
			continue
		}
		result, runErr := s.Run(ctx, item.ID, RunInput{ActorID: actorID, ClientID: "automation:" + item.ID, IdempotencyKey: "trigger:" + mutation.OperationID, ExpectedRevision: item.Revision, TriggerType: TriggerCellChange, TriggerOperationID: mutation.OperationID})
		if runErr != nil {
			errs = append(errs, fmt.Errorf("%s: %w", item.ID, runErr))
			// Nobody is watching a cell-change trigger, so a failure that never
			// reached a run row would exist only in the server log. Record it and
			// hand it back so the editor can say which automation broke.
			if failed, recordErr := s.recordTriggerFailure(ctx, item, mutation, actorID, runErr); recordErr != nil {
				errs = append(errs, fmt.Errorf("record %s failure: %w", item.ID, recordErr))
			} else if failed.ID != "" {
				results = append(results, ExecutionResult{Run: failed})
			}
			continue
		}
		results = append(results, result)
	}
	return results, errors.Join(errs...)
}

func (s *Service) RunDueSchedules(ctx context.Context, now time.Time, limit int) ([]ExecutionResult, error) {
	config, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	if !config.Enabled {
		return []ExecutionResult{}, nil
	}
	if limit < 1 || limit > 200 {
		limit = defaultSchedulerBatch
	}
	now = now.UTC()
	rows, err := s.pool.Query(ctx, `SELECT `+dueDefinitionColumns+` FROM automations a JOIN workbooks w ON w.id=a.workbook_id WHERE a.enabled AND a.deleted_at IS NULL AND w.deleted_at IS NULL AND a.next_run_at IS NOT NULL AND a.next_run_at<=$1 ORDER BY a.next_run_at,a.id LIMIT $2`, now, limit)
	if err != nil {
		return nil, err
	}
	items := make([]Automation, 0)
	for rows.Next() {
		item, scanErr := scanAutomation(rows)
		if scanErr != nil {
			rows.Close()
			return nil, scanErr
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	results := make([]ExecutionResult, 0, len(items))
	errList := make([]error, 0)
	for _, item := range items {
		if item.NextRunAt == nil || item.Trigger.Type != TriggerSchedule {
			continue
		}
		due := item.NextRunAt.UTC()
		input := RunInput{ActorID: scheduledAutomationActor, ClientID: "scheduler", IdempotencyKey: "schedule:" + due.Format(time.RFC3339), ExpectedRevision: item.Revision, TriggerType: TriggerSchedule, ScheduledFor: &due}
		result, runErr := s.Run(ctx, item.ID, input)
		if runErr != nil {
			if existing, getErr := s.getRunBySchedule(ctx, item.ID, due); getErr == nil {
				if existing.Status == StatusRunning {
					errList = append(errList, fmt.Errorf("schedule %s at %s remains recoverable: %w", item.ID, due.Format(time.RFC3339), runErr))
					continue
				}
				result = executionFromRun(existing)
			} else {
				failed, recordErr := s.recordScheduledFailure(ctx, item, due, runErr)
				if recordErr != nil {
					errList = append(errList, fmt.Errorf("schedule %s at %s: %w", item.ID, due.Format(time.RFC3339), errors.Join(runErr, recordErr)))
					continue
				}
				result = executionFromRun(failed)
			}
			errList = append(errList, fmt.Errorf("schedule %s at %s: %w", item.ID, due.Format(time.RFC3339), runErr))
		}
		results = append(results, result)
		if err := s.advanceSchedule(ctx, item, due, now); err != nil {
			errList = append(errList, fmt.Errorf("advance schedule %s: %w", item.ID, err))
			continue
		}
	}
	return results, errors.Join(errList...)
}

func (s *Service) RunScheduler(ctx context.Context) {
	s.logger.Info("automation scheduler started")
	defer s.logger.Info("automation scheduler stopped")
	for {
		poll := defaultSchedulerPoll
		config, err := s.config(ctx)
		if err != nil {
			s.logger.Error("automation scheduler settings failed", "error", err)
		} else {
			poll = config.SchedulerPoll
			if config.Enabled {
				results, runErr := s.RunDueSchedules(ctx, s.now(), defaultSchedulerBatch)
				if runErr != nil {
					s.logger.Error("automation scheduler tick failed", "error", runErr)
				}
				if len(results) > 0 {
					s.logger.Info("automation scheduler tick completed", "runs", len(results))
					for _, result := range results {
						if s.scheduledListener != nil && !result.Run.Duplicate && result.Operation.OperationID != "" {
							s.scheduledListener(result)
						}
					}
				}
			}
		}
		timer := time.NewTimer(poll)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
		}
	}
}

// recordTriggerFailure writes the failed run a cell-change trigger never got to
// create. Rate-limit refusals collapse into one row per hour: they repeat on
// every keystroke, and recording each one would undo the limit it reports.
func (s *Service) recordTriggerFailure(ctx context.Context, item Automation, mutation workbook.MutationResult, actorID string, runErr error) (Run, error) {
	if errors.Is(runErr, ErrNoChanges) || errors.Is(runErr, ErrDisabled) || errors.Is(runErr, ErrNotFound) {
		return Run{}, nil
	}
	now := s.now()
	run := Run{ID: identity.New(), AutomationID: item.ID, WorkbookID: item.WorkbookID, ActorID: actorID, TriggerType: TriggerCellChange, TriggerOperationID: mutation.OperationID, Status: StatusFailed, BaseVersion: mutation.ServerVersion, Action: item.Action, ErrorMessage: trimLength(runErr.Error(), 2000), StartedAt: now, CompletedAt: &now, UpdatedAt: now, cells: []workbook.CellInput{}, expected: map[string]workbook.Cell{}, idempotencyKey: "trigger-failed:" + mutation.OperationID, excludedFromRate: true}
	if errors.Is(runErr, ErrRate) {
		run.TriggerOperationID, run.idempotencyKey = "", "trigger-rate:"+now.UTC().Format("2006-01-02T15")
	}
	created, err := s.insertRun(ctx, run, 0)
	if err != nil {
		if isUniqueViolation(err) {
			return Run{}, nil
		}
		return Run{}, err
	}
	if !created {
		return Run{}, nil
	}
	return run, nil
}

func (s *Service) recordScheduledFailure(ctx context.Context, item Automation, due time.Time, runErr error) (Run, error) {
	book, err := s.books.GetWorkbook(ctx, item.WorkbookID)
	if err != nil {
		return Run{}, err
	}
	now := s.now()
	run := Run{ID: identity.New(), AutomationID: item.ID, WorkbookID: item.WorkbookID, ActorID: scheduledAutomationActor, TriggerType: TriggerSchedule, ScheduledFor: &due, Status: StatusFailed, BaseVersion: book.Version, Action: item.Action, ErrorMessage: trimLength(runErr.Error(), 2000), StartedAt: now, CompletedAt: &now, UpdatedAt: now, cells: []workbook.CellInput{}, expected: map[string]workbook.Cell{}, idempotencyKey: "schedule:" + due.Format(time.RFC3339), excludedFromRate: true}
	created, err := s.insertRun(ctx, run, 0)
	if err != nil {
		if isUniqueViolation(err) {
			return s.getRunBySchedule(ctx, item.ID, due)
		}
		return Run{}, err
	}
	if !created {
		return s.getRunByKey(ctx, item.ID, run.ActorID, run.idempotencyKey)
	}
	s.logger.Error("scheduled automation failed", "automation_id", item.ID, "run_id", run.ID, "scheduled_for", due, "error", runErr)
	return run, nil
}

func (s *Service) advanceSchedule(ctx context.Context, item Automation, due, now time.Time) error {
	schedule, err := ParseSchedule(item.Trigger.Cron, item.Trigger.Timezone)
	if err != nil {
		return err
	}
	base := now
	if due.After(base) {
		base = due
	}
	next, err := schedule.Next(base)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `UPDATE automations SET next_run_at=$4 WHERE id=$1 AND revision=$2 AND next_run_at=$3 AND deleted_at IS NULL`, item.ID, item.Revision, due, next)
	return err
}

func (s *Service) executeRun(ctx context.Context, run Run, clientID string) (ExecutionResult, error) {
	result, err := s.books.ApplyCells(ctx, workbook.CellMutation{SheetID: run.Action.SheetID, ActorID: automationActor(run.AutomationID), ClientID: clientID, BaseVersion: run.BaseVersion, IdempotencyKey: "automation-run:" + run.ID, Cells: run.cells, Expected: run.expected, OperationType: "automation." + run.Action.Type, RequireExactVersion: true})
	if err != nil {
		if terminalExecutionError(err) {
			s.markRunFailed(run, err)
		} else {
			s.markRunRetryable(run, err)
		}
		return ExecutionResult{}, err
	}
	now := s.now()
	payload, _ := json.Marshal(result)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ExecutionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	command, err := tx.Exec(ctx, `UPDATE automation_runs SET status='succeeded',operation_id=$2,operation_result=$3,error_message='',completed_at=$4,updated_at=$4 WHERE id=$1 AND status='running'`, run.ID, result.OperationID, payload, now)
	if err != nil {
		return ExecutionResult{}, err
	}
	if command.RowsAffected() != 1 {
		current, getErr := s.getRun(ctx, run.ID)
		if getErr == nil && current.Status == StatusSucceeded && current.Operation != nil {
			current.Duplicate = true
			return executionFromRun(current), nil
		}
		return ExecutionResult{}, ErrRevision
	}
	if err := insertAudit(ctx, tx, run.ActorID, "automation.run", run.ID, runAuditMetadata(run, map[string]any{"operation_id": result.OperationID, "server_version": result.ServerVersion}), now); err != nil {
		return ExecutionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ExecutionResult{}, err
	}
	run.Status, run.OperationID, run.Operation, run.CompletedAt, run.UpdatedAt = StatusSucceeded, result.OperationID, &result, &now, now
	s.logger.Info("automation run succeeded", "automation_id", run.AutomationID, "run_id", run.ID, "trigger", run.TriggerType, "operation_id", result.OperationID)
	return ExecutionResult{Run: run, Operation: result, Changes: cloneInputs(run.cells)}, nil
}

func (s *Service) buildPreview(ctx context.Context, item Automation, maxCells int) (Preview, error) {
	preview, _, _, err := s.buildExecution(ctx, item, maxCells)
	return preview, err
}

func (s *Service) buildExecution(ctx context.Context, item Automation, maxCells int) (Preview, []workbook.CellInput, map[string]workbook.Cell, error) {
	selected, err := cellrange.Parse(item.Action.Range)
	if err != nil {
		return Preview{}, nil, nil, fmt.Errorf("%w: invalid action range", ErrInvalid)
	}
	if !supportedRange(selected) {
		return Preview{}, nil, nil, fmt.Errorf("%w: action range exceeds XFD1048576", ErrInvalid)
	}
	book, err := s.books.GetWorkbook(ctx, item.WorkbookID)
	if err != nil {
		return Preview{}, nil, nil, err
	}
	existing, err := s.books.ReadRange(ctx, item.Action.SheetID, selected)
	if err != nil {
		return Preview{}, nil, nil, err
	}
	current := make(map[string]workbook.Cell, len(existing))
	for _, cell := range existing {
		current[coordinateKey(cell.Row, cell.Column)] = cell
	}
	changes := make([]PreviewChange, 0)
	inputs := make([]workbook.CellInput, 0)
	expected := make(map[string]workbook.Cell)
	snapshotBytes := 0
	for row := selected.Start.Row; row <= selected.End.Row; row++ {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			beforeCell, exists := current[coordinateKey(row, column)]
			if !exists {
				beforeCell = workbook.Cell{SheetID: item.Action.SheetID, Row: row, Column: column}
			}
			before := snapshotFromCell(beforeCell)
			if before.SpillSource != "" {
				return Preview{}, nil, nil, fmt.Errorf("%w: %s is an array spill result", ErrInvalid, cellrange.Address(row, column))
			}
			after := CellSnapshot{Style: cloneRaw(before.Style)}
			switch item.Action.Type {
			case ActionSetValue:
				after.Value = cloneRaw(item.Action.Value)
			case ActionSetFormula:
				after.Formula = formula.ShiftReferences(item.Action.Formula, row-selected.Start.Row, column-selected.Start.Column)
			case ActionClear:
			}
			if snapshotsEquivalentForAction(before, after, item.Action.Type) {
				continue
			}
			snapshotBytes += len(before.Value) + len(before.Formula) + len(before.Style) + len(before.SpillSource) + len(after.Value) + len(after.Formula) + len(after.Style)
			if snapshotBytes > maxExecutionSnapshotBytes {
				return Preview{}, nil, nil, fmt.Errorf("%w: expanded execution snapshot exceeds %d MiB", ErrInvalid, maxExecutionSnapshotBytes>>20)
			}
			changes = append(changes, PreviewChange{Row: row, Column: column, Address: cellrange.Address(row, column), Before: before, After: after})
			inputs = append(inputs, workbook.CellInput{Row: row, Column: column, Value: cloneRaw(after.Value), Formula: after.Formula, Style: cloneRaw(after.Style)})
			expected[coordinateKey(row, column)] = beforeCell
		}
	}
	if len(inputs) == 0 {
		return Preview{AutomationID: item.ID, AutomationName: item.Name, AutomationRevision: item.Revision, WorkbookID: item.WorkbookID, BaseVersion: book.Version, Action: item.Action, Changes: []PreviewChange{}}, []workbook.CellInput{}, expected, fmt.Errorf("%w: %w", ErrInvalid, ErrNoChanges)
	}
	if len(inputs) > maxCells {
		return Preview{}, nil, nil, fmt.Errorf("%w: automation exceeds the %d cell limit", ErrInvalid, maxCells)
	}
	sortPreview(changes)
	return Preview{AutomationID: item.ID, AutomationName: item.Name, AutomationRevision: item.Revision, WorkbookID: item.WorkbookID, BaseVersion: book.Version, Action: item.Action, Changes: changes}, inputs, expected, nil
}

func (s *Service) validateDefinition(ctx context.Context, workbookID, name string, trigger TriggerDefinition, action ActionDefinition, maxCells int) (string, TriggerDefinition, ActionDefinition, error) {
	name = strings.TrimSpace(name)
	if len([]rune(name)) < 1 || len([]rune(name)) > 120 {
		return "", trigger, action, fmt.Errorf("%w: name must contain 1 to 120 characters", ErrInvalid)
	}
	book, err := s.books.GetWorkbook(ctx, workbookID)
	if err != nil {
		return "", trigger, action, err
	}
	sheets := make(map[string]struct{}, len(book.Sheets))
	for _, sheet := range book.Sheets {
		sheets[sheet.ID] = struct{}{}
	}
	trigger.Type = strings.TrimSpace(trigger.Type)
	switch trigger.Type {
	case TriggerManual:
		trigger.SheetID, trigger.Range, trigger.Cron, trigger.Timezone = "", "", "", ""
	case TriggerCellChange:
		trigger.Cron, trigger.Timezone = "", ""
		if _, ok := sheets[trigger.SheetID]; !ok {
			return "", trigger, action, fmt.Errorf("%w: trigger sheet must belong to the workbook", ErrInvalid)
		}
		selected, err := cellrange.Parse(trigger.Range)
		if err != nil {
			return "", trigger, action, fmt.Errorf("%w: invalid trigger range", ErrInvalid)
		}
		if !supportedRange(selected) {
			return "", trigger, action, fmt.Errorf("%w: trigger range exceeds XFD1048576", ErrInvalid)
		}
		trigger.Range = rangeAddress(selected)
	case TriggerSchedule:
		trigger.SheetID, trigger.Range = "", ""
		schedule, err := ParseSchedule(trigger.Cron, trigger.Timezone)
		if err != nil {
			return "", trigger, action, err
		}
		trigger.Cron, trigger.Timezone = schedule.Expression, schedule.Timezone
	case TriggerWebhook:
		trigger.SheetID, trigger.Range, trigger.Cron, trigger.Timezone = "", "", "", ""
	default:
		return "", trigger, action, fmt.Errorf("%w: trigger type must be manual, cell_change, schedule or webhook", ErrInvalid)
	}
	if _, ok := sheets[action.SheetID]; !ok {
		return "", trigger, action, fmt.Errorf("%w: action sheet must belong to the workbook", ErrInvalid)
	}
	selected, err := cellrange.Parse(action.Range)
	if err != nil {
		return "", trigger, action, fmt.Errorf("%w: invalid action range", ErrInvalid)
	}
	if !supportedRange(selected) {
		return "", trigger, action, fmt.Errorf("%w: action range exceeds XFD1048576", ErrInvalid)
	}
	rows, columns := selected.End.Row-selected.Start.Row+1, selected.End.Column-selected.Start.Column+1
	if rows < 1 || columns < 1 || rows > maxCells || columns > maxCells || rows > maxCells/columns {
		return "", trigger, action, fmt.Errorf("%w: action range must contain 1 to %d cells", ErrInvalid, maxCells)
	}
	action.Range = rangeAddress(selected)
	action.Formula = strings.TrimSpace(action.Formula)
	switch action.Type {
	case ActionSetValue:
		if action.Formula != "" || !validScalar(action.Value) {
			return "", trigger, action, fmt.Errorf("%w: set_value requires one JSON string, number or boolean", ErrInvalid)
		}
	case ActionSetFormula:
		if len(bytes.TrimSpace(action.Value)) != 0 || !strings.HasPrefix(action.Formula, "=") || len(action.Formula) > 8192 {
			return "", trigger, action, fmt.Errorf("%w: set_formula requires a formula beginning with '=' up to 8192 characters", ErrInvalid)
		}
	case ActionClear:
		if len(bytes.TrimSpace(action.Value)) != 0 || action.Formula != "" {
			return "", trigger, action, fmt.Errorf("%w: clear cannot contain a value or formula", ErrInvalid)
		}
	default:
		return "", trigger, action, fmt.Errorf("%w: action type must be set_value, set_formula or clear", ErrInvalid)
	}
	return name, trigger, action, nil
}

func (s *Service) config(ctx context.Context) (runtimeConfig, error) {
	config := runtimeConfig{Enabled: false, MaxCellsPerRun: defaultMaxCells, MaxRunsPerHour: defaultMaxRuns, SchedulerPoll: defaultSchedulerPoll}
	if s.settings == nil {
		return config, nil
	}
	values, err := s.settings.Values(ctx)
	if err != nil {
		return config, err
	}
	if value, ok := values["automation.enabled"].(bool); ok {
		config.Enabled = value
	}
	config.MaxCellsPerRun = boundedSetting(values["automation.max_cells_per_run"], defaultMaxCells, 1, workbook.MaxPasteCells)
	config.MaxRunsPerHour = boundedSetting(values["automation.max_runs_per_hour"], defaultMaxRuns, 1, 10_000)
	config.SchedulerPoll = time.Duration(boundedSetting(values["automation.scheduler_poll_seconds"], int(defaultSchedulerPoll/time.Second), 5, 300)) * time.Second
	return config, nil
}

func (s *Service) markRunFailed(run Run, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := s.now()
	errorMessage := trimLength(runErr.Error(), 2000)
	tx, err := s.pool.Begin(ctx)
	if err == nil {
		defer func() { _ = tx.Rollback(ctx) }()
		var command pgconn.CommandTag
		command, err = tx.Exec(ctx, `UPDATE automation_runs SET status='failed',error_message=$2,completed_at=$3,updated_at=$3 WHERE id=$1 AND status='running'`, run.ID, errorMessage, now)
		if err == nil && command.RowsAffected() == 1 {
			err = insertAudit(ctx, tx, run.ActorID, "automation.run.failed", run.ID, runAuditMetadata(run, map[string]any{"base_version": run.BaseVersion, "error": errorMessage}), now)
		}
		if err == nil {
			err = tx.Commit(ctx)
		}
	}
	if err != nil {
		s.logger.Error("automation run failure persistence failed", "run_id", run.ID, "error", err)
	}
	s.logger.Error("automation run failed", "automation_id", run.AutomationID, "run_id", run.ID, "trigger", run.TriggerType, "error", runErr)
}

func (s *Service) markRunRetryable(run Run, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	now := s.now()
	if _, err := s.pool.Exec(ctx, `UPDATE automation_runs SET error_message=$2,updated_at=$3 WHERE id=$1 AND status='running'`, run.ID, trimLength(runErr.Error(), 2000), now); err != nil {
		s.logger.Error("automation retryable failure persistence failed", "run_id", run.ID, "error", err)
	}
	s.logger.Warn("automation run remains retryable", "automation_id", run.AutomationID, "run_id", run.ID, "trigger", run.TriggerType, "error", runErr)
}

func terminalExecutionError(err error) bool {
	return errors.Is(err, workbook.ErrNotFound) || errors.Is(err, workbook.ErrInvalid) || errors.Is(err, workbook.ErrVersionAhead) ||
		errors.Is(err, workbook.ErrVersionConflict) || errors.Is(err, workbook.ErrDuplicateName) || errors.Is(err, workbook.ErrValidation) ||
		errors.Is(err, workbook.ErrRevision) || errors.Is(err, workbook.ErrForbidden)
}

type scanner interface{ Scan(...any) error }

func scanAutomation(row scanner) (Automation, error) {
	var item Automation
	var triggerJSON, actionJSON []byte
	err := row.Scan(&item.ID, &item.WorkbookID, &item.Name, &item.Enabled, &triggerJSON, &actionJSON, &item.Revision, &item.idempotencyKey, &item.CreatedBy, &item.UpdatedBy, &item.CreatedAt, &item.UpdatedAt, &item.NextRunAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Automation{}, ErrNotFound
	}
	if err != nil {
		return Automation{}, err
	}
	if err := json.Unmarshal(triggerJSON, &item.Trigger); err != nil {
		return Automation{}, err
	}
	if err := json.Unmarshal(actionJSON, &item.Action); err != nil {
		return Automation{}, err
	}
	return item, nil
}

func scanRun(row scanner) (Run, error) {
	var item Run
	var countsTowardRate bool
	var actionJSON, cellsJSON, expectedJSON, operationJSON, undoJSON []byte
	err := row.Scan(&item.ID, &item.AutomationID, &item.WorkbookID, &item.ActorID, &item.idempotencyKey, &item.TriggerType, &item.TriggerOperationID, &item.ScheduledFor, &item.TriggerKeyID, &item.PayloadDigest, &item.PayloadBytes, &item.Status, &countsTowardRate, &item.BaseVersion, &actionJSON, &cellsJSON, &expectedJSON, &item.OperationID, &operationJSON, &item.undoKey, &item.UndoOperationID, &undoJSON, &item.ErrorMessage, &item.StartedAt, &item.CompletedAt, &item.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Run{}, ErrNotFound
	}
	if err != nil {
		return Run{}, err
	}
	item.excludedFromRate = !countsTowardRate
	if err := json.Unmarshal(actionJSON, &item.Action); err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal(cellsJSON, &item.cells); err != nil {
		return Run{}, err
	}
	if err := json.Unmarshal(expectedJSON, &item.expected); err != nil {
		return Run{}, err
	}
	if len(operationJSON) > 0 && string(operationJSON) != "null" {
		var result workbook.MutationResult
		if err := json.Unmarshal(operationJSON, &result); err != nil {
			return Run{}, err
		}
		item.Operation = &result
	}
	if len(undoJSON) > 0 && string(undoJSON) != "null" {
		var result workbook.MutationResult
		if err := json.Unmarshal(undoJSON, &result); err != nil {
			return Run{}, err
		}
		item.UndoOperation = &result
	}
	return item, nil
}

func (s *Service) getRun(ctx context.Context, id string) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE id=$1`, id))
}

func (s *Service) getRunByKey(ctx context.Context, automationID, actorID, key string) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE automation_id=$1 AND actor_id=$2 AND idempotency_key=$3`, automationID, actorID, key))
}

func (s *Service) getRunByTrigger(ctx context.Context, automationID, operationID string) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE automation_id=$1 AND trigger_operation_id=$2`, automationID, operationID))
}

func (s *Service) getRunBySchedule(ctx context.Context, automationID string, scheduledFor time.Time) (Run, error) {
	return scanRun(s.pool.QueryRow(ctx, `SELECT `+runColumns+` FROM automation_runs WHERE automation_id=$1 AND scheduled_for=$2`, automationID, scheduledFor))
}

func executionFromRun(run Run) ExecutionResult {
	result := ExecutionResult{Run: run, Changes: cloneInputs(run.cells)}
	if run.Operation != nil {
		result.Operation = *run.Operation
	}
	return result
}

func replayRun(run Run) (ExecutionResult, error) {
	run.Duplicate = true
	result := executionFromRun(run)
	if run.Status == StatusFailed {
		return result, fmt.Errorf("%w: %s", ErrRunFailed, run.ErrorMessage)
	}
	return result, nil
}

func insertAudit(ctx context.Context, tx pgx.Tx, actor, action, resourceID string, metadata any, now time.Time) error {
	payload, _ := json.Marshal(metadata)
	_, err := tx.Exec(ctx, `INSERT INTO audit_logs(actor_id,action,resource_type,resource_id,result,metadata,created_at) VALUES($1,$2,'automation',$3,'success',$4,$5)`, actor, action, resourceID, payload, now)
	return err
}

func validScalar(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || len(trimmed) > 65536 || !json.Valid(trimmed) {
		return false
	}
	var value any
	if json.Unmarshal(trimmed, &value) != nil {
		return false
	}
	switch value.(type) {
	case string, float64, bool:
		return true
	default:
		return false
	}
}

func triggerMatches(rawRange string, cells []workbook.CellInput) bool {
	selected, err := cellrange.Parse(rawRange)
	if err != nil {
		return false
	}
	for _, cell := range cells {
		if cell.Row >= selected.Start.Row && cell.Row <= selected.End.Row && cell.Column >= selected.Start.Column && cell.Column <= selected.End.Column {
			return true
		}
	}
	return false
}

func snapshotFromCell(cell workbook.Cell) CellSnapshot {
	return CellSnapshot{Value: cloneRaw(cell.Value), Formula: cell.Formula, Style: cloneRaw(cell.Style), SpillSource: cell.SpillSource}
}

func snapshotsEqual(left, right CellSnapshot) bool {
	return bytes.Equal(bytes.TrimSpace(left.Value), bytes.TrimSpace(right.Value)) && left.Formula == right.Formula && bytes.Equal(bytes.TrimSpace(left.Style), bytes.TrimSpace(right.Style)) && left.SpillSource == right.SpillSource
}

func snapshotsEquivalentForAction(before, after CellSnapshot, actionType string) bool {
	if actionType == ActionSetFormula && before.Formula == after.Formula && !formula.IsVolatile(after.Formula) {
		return bytes.Equal(bytes.TrimSpace(before.Style), bytes.TrimSpace(after.Style)) && before.SpillSource == after.SpillSource
	}
	return snapshotsEqual(before, after)
}

func supportedRange(value cellrange.Range) bool {
	return value.Start.Row >= 1 && value.Start.Column >= 1 && value.End.Row <= formula.MaxRows && value.End.Column <= formula.MaxColumns
}

func cloneRaw(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

func cloneInputs(values []workbook.CellInput) []workbook.CellInput {
	result := make([]workbook.CellInput, len(values))
	for index, value := range values {
		result[index] = value
		result[index].Value = cloneRaw(value.Value)
		result[index].Style = cloneRaw(value.Style)
	}
	return result
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}

func nextScheduleTime(trigger TriggerDefinition, after time.Time) (*time.Time, error) {
	if trigger.Type != TriggerSchedule {
		return nil, nil
	}
	schedule, err := ParseSchedule(trigger.Cron, trigger.Timezone)
	if err != nil {
		return nil, err
	}
	next, err := schedule.Next(after)
	if err != nil {
		return nil, err
	}
	return &next, nil
}

func coordinateKey(row, column int) string { return fmt.Sprintf("%d:%d", row, column) }

func rangeAddress(value cellrange.Range) string {
	start, end := cellrange.Address(value.Start.Row, value.Start.Column), cellrange.Address(value.End.Row, value.End.Column)
	if start == end {
		return start
	}
	return start + ":" + end
}

func automationActor(id string) string { return "automation:" + id }

func trimLength(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) > limit {
		return string(runes[:limit])
	}
	return value
}

func validIdempotencyKey(value string) bool {
	return strings.TrimSpace(value) != "" && utf8.RuneCountInString(value) <= 200
}

func boundedSetting(value any, fallback, minimum, maximum int) int {
	number, ok := value.(float64)
	if !ok || number < float64(minimum) || number > float64(maximum) {
		return fallback
	}
	return int(number)
}

func mapConstraintError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: an automation with the same name already exists", ErrInvalid)
	}
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

func runAuditMetadata(run Run, values map[string]any) map[string]any {
	metadata := map[string]any{
		"automation_id": run.AutomationID,
		"trigger":       run.TriggerType,
		"scheduled_for": run.ScheduledFor,
	}
	if run.TriggerKeyID != "" {
		metadata["trigger_key_id"] = run.TriggerKeyID
		metadata["payload_digest"] = run.PayloadDigest
		metadata["payload_bytes"] = run.PayloadBytes
	}
	for key, value := range values {
		metadata[key] = value
	}
	return metadata
}

func sortPreview(changes []PreviewChange) {
	sort.SliceStable(changes, func(i, j int) bool {
		if changes[i].Row == changes[j].Row {
			return changes[i].Column < changes[j].Column
		}
		return changes[i].Row < changes[j].Row
	})
}
