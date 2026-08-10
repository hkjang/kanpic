package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// Administrators need to answer three questions about AI use: who asked for
// what, what did it cost, and what should be kept. History serves the first
// two; retention and purging serve the third.

const HistoryPageLimit = 100

type HistoryFilter struct {
	Actor      string
	WorkbookID string
	Mode       string
	Status     string
	Query      string
	Since      time.Time
	Until      time.Time
	Limit      int
	Offset     int
}

type HistoryItem struct {
	ID               string    `json:"id"`
	WorkbookID       string    `json:"workbook_id"`
	WorkbookTitle    string    `json:"workbook_title"`
	ActorID          string    `json:"actor_id"`
	Mode             string    `json:"mode"`
	Range            string    `json:"range"`
	Request          string    `json:"request"`
	Status           string    `json:"status"`
	Model            string    `json:"model"`
	Summary          string    `json:"summary,omitempty"`
	ErrorMessage     string    `json:"error_message,omitempty"`
	ChangeCount      int       `json:"change_count"`
	FindingCount     int       `json:"finding_count"`
	InputCellCount   int       `json:"input_cell_count"`
	PromptTokens     int       `json:"prompt_tokens"`
	CompletionTokens int       `json:"completion_tokens"`
	Attempts         int       `json:"attempts"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

type HistoryActor struct {
	ActorID string `json:"actor_id"`
	Count   int    `json:"count"`
	Tokens  int    `json:"tokens"`
}

type HistorySummary struct {
	Total            int            `json:"total"`
	ByStatus         map[string]int `json:"by_status"`
	ByMode           map[string]int `json:"by_mode"`
	PromptTokens     int            `json:"prompt_tokens"`
	CompletionTokens int            `json:"completion_tokens"`
	AppliedCells     int            `json:"applied_cells"`
	TopActors        []HistoryActor `json:"top_actors"`
	OldestAt         *time.Time     `json:"oldest_at,omitempty"`
	NewestAt         *time.Time     `json:"newest_at,omitempty"`
	RetentionDays    int            `json:"retention_days"`
}

type HistoryPage struct {
	Items      []HistoryItem  `json:"items"`
	Summary    HistorySummary `json:"summary"`
	NextOffset *int           `json:"next_offset,omitempty"`
}

// historyRow reads the usage numbers out of the planning event, which is where
// the gateway cost is recorded.
const historyColumns = `a.id::text,a.workbook_id::text,coalesce(w.title,''),a.actor_id,a.mode,a.selected_range,a.request,a.status,a.model,a.summary,a.error_message,
	coalesce(jsonb_array_length(a.changes),0),coalesce(jsonb_array_length(a.findings),0),a.input_cell_count,
	coalesce(u.prompt_tokens,0),coalesce(u.completion_tokens,0),coalesce(u.attempts,0),a.created_at,a.updated_at`

const historyFrom = ` FROM ai_actions a
	LEFT JOIN workbooks w ON w.id = a.workbook_id
	LEFT JOIN LATERAL (
		SELECT (e.payload->'usage'->>'prompt_tokens')::int AS prompt_tokens,
		       (e.payload->'usage'->>'completion_tokens')::int AS completion_tokens,
		       (e.payload->'usage'->>'attempts')::int AS attempts
		FROM ai_action_events e
		WHERE e.action_id = a.id AND e.payload ? 'usage'
		ORDER BY e.id LIMIT 1
	) u ON true`

func (f HistoryFilter) where() (string, []any) {
	clauses, args := []string{"1=1"}, []any{}
	add := func(clause string, value any) {
		args = append(args, value)
		clauses = append(clauses, fmt.Sprintf(clause, len(args)))
	}
	if actor := strings.TrimSpace(f.Actor); actor != "" {
		add("a.actor_id ILIKE '%%' || $%d || '%%'", actor)
	}
	if id := strings.TrimSpace(f.WorkbookID); id != "" {
		add("a.workbook_id::text = $%d", id)
	}
	if mode := strings.TrimSpace(f.Mode); mode != "" {
		add("a.mode = $%d", mode)
	}
	if status := strings.TrimSpace(f.Status); status != "" {
		add("a.status = $%d", status)
	}
	if query := strings.TrimSpace(f.Query); query != "" {
		args = append(args, query)
		clauses = append(clauses, fmt.Sprintf("(a.request ILIKE '%%' || $%d || '%%' OR a.summary ILIKE '%%' || $%d || '%%')", len(args), len(args)))
	}
	if !f.Since.IsZero() {
		add("a.created_at >= $%d", f.Since)
	}
	if !f.Until.IsZero() {
		add("a.created_at < $%d", f.Until)
	}
	return strings.Join(clauses, " AND "), args
}

// History lists AI calls across every workbook for an administrator, together
// with the totals that make the list worth reading.
func (s *Service) History(ctx context.Context, filter HistoryFilter) (HistoryPage, error) {
	limit := filter.Limit
	if limit < 1 || limit > HistoryPageLimit {
		limit = 50
	}
	offset := filter.Offset
	if offset < 0 {
		offset = 0
	}
	where, args := filter.where()
	page := HistoryPage{Items: []HistoryItem{}, Summary: HistorySummary{ByStatus: map[string]int{}, ByMode: map[string]int{}}}

	rows, err := s.pool.Query(ctx, `SELECT `+historyColumns+historyFrom+` WHERE `+where+
		fmt.Sprintf(` ORDER BY a.created_at DESC, a.id LIMIT $%d OFFSET $%d`, len(args)+1, len(args)+2),
		append(append([]any{}, args...), limit+1, offset)...)
	if err != nil {
		return HistoryPage{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var item HistoryItem
		if err := rows.Scan(&item.ID, &item.WorkbookID, &item.WorkbookTitle, &item.ActorID, &item.Mode, &item.Range, &item.Request, &item.Status, &item.Model,
			&item.Summary, &item.ErrorMessage, &item.ChangeCount, &item.FindingCount, &item.InputCellCount,
			&item.PromptTokens, &item.CompletionTokens, &item.Attempts, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return HistoryPage{}, err
		}
		page.Items = append(page.Items, item)
	}
	if err := rows.Err(); err != nil {
		return HistoryPage{}, err
	}
	if len(page.Items) > limit {
		page.Items = page.Items[:limit]
		next := offset + limit
		page.NextOffset = &next
	}
	summary, err := s.historySummary(ctx, where, args)
	if err != nil {
		return HistoryPage{}, err
	}
	page.Summary = summary
	return page, nil
}

func (s *Service) historySummary(ctx context.Context, where string, args []any) (HistorySummary, error) {
	summary := HistorySummary{ByStatus: map[string]int{}, ByMode: map[string]int{}, TopActors: []HistoryActor{}}
	row := s.pool.QueryRow(ctx, `SELECT count(*),
		coalesce(sum(coalesce(u.prompt_tokens,0)),0),coalesce(sum(coalesce(u.completion_tokens,0)),0),
		coalesce(sum(CASE WHEN a.status='applied' THEN coalesce(jsonb_array_length(a.changes),0) ELSE 0 END),0),
		min(a.created_at),max(a.created_at)`+historyFrom+` WHERE `+where, args...)
	var oldest, newest *time.Time
	if err := row.Scan(&summary.Total, &summary.PromptTokens, &summary.CompletionTokens, &summary.AppliedCells, &oldest, &newest); err != nil {
		return HistorySummary{}, err
	}
	summary.OldestAt, summary.NewestAt = oldest, newest
	for _, group := range []struct {
		column string
		target map[string]int
	}{{"a.status", summary.ByStatus}, {"a.mode", summary.ByMode}} {
		rows, err := s.pool.Query(ctx, `SELECT `+group.column+`, count(*)`+historyFrom+` WHERE `+where+` GROUP BY 1`, args...)
		if err != nil {
			return HistorySummary{}, err
		}
		for rows.Next() {
			var key string
			var count int
			if err := rows.Scan(&key, &count); err != nil {
				rows.Close()
				return HistorySummary{}, err
			}
			group.target[key] = count
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return HistorySummary{}, err
		}
	}
	actors, err := s.pool.Query(ctx, `SELECT a.actor_id, count(*), coalesce(sum(coalesce(u.prompt_tokens,0)+coalesce(u.completion_tokens,0)),0)`+
		historyFrom+` WHERE `+where+` GROUP BY 1 ORDER BY 2 DESC, 3 DESC, 1 LIMIT 5`, args...)
	if err != nil {
		return HistorySummary{}, err
	}
	defer actors.Close()
	for actors.Next() {
		var actor HistoryActor
		if err := actors.Scan(&actor.ActorID, &actor.Count, &actor.Tokens); err != nil {
			return HistorySummary{}, err
		}
		summary.TopActors = append(summary.TopActors, actor)
	}
	return summary, actors.Err()
}

// AdminGet reads any action with its event trail. Ownership is deliberately not
// checked: the caller is an administrator reviewing other people's use.
func (s *Service) AdminGet(ctx context.Context, actionID string) (Action, error) {
	action, err := scanAction(s.pool.QueryRow(ctx, `SELECT `+actionColumns+` FROM ai_actions WHERE id=$1`, actionID))
	if err != nil {
		return Action{}, err
	}
	events, err := s.listEvents(ctx, action.ID)
	if err != nil {
		return Action{}, err
	}
	action.Events = events
	return action, nil
}

// PurgeHistory deletes finished actions older than the cutoff and records who
// asked for it. Actions still waiting for a decision are kept.
func (s *Service) PurgeHistory(ctx context.Context, before time.Time, actorID string) (int64, error) {
	if before.IsZero() {
		return 0, fmt.Errorf("%w: 삭제 기준 시각이 필요합니다", ErrInvalid)
	}
	command, err := s.pool.Exec(ctx, `DELETE FROM ai_actions WHERE created_at < $1 AND status <> 'planned'`, before)
	if err != nil {
		return 0, err
	}
	removed := command.RowsAffected()
	if removed > 0 {
		payload, _ := json.Marshal(map[string]any{"before": before, "removed": removed})
		tx, err := s.pool.Begin(ctx)
		if err != nil {
			return removed, nil
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if err := insertAudit(ctx, tx, actorID, "ai.history.purge", "", "success", payload, s.now()); err == nil {
			_ = tx.Commit(ctx)
		}
	}
	s.logger.Info("AI history purged", "actor_id", actorID, "before", before, "removed", removed)
	return removed, nil
}

// RetentionDays reads how long finished actions are kept. Zero keeps them.
func (s *Service) RetentionDays(ctx context.Context) int {
	values, err := s.settings.Values(ctx)
	if err != nil {
		return 0
	}
	days, _ := numberSetting(values, "ai.history_retention_days")
	if days < 0 {
		return 0
	}
	return days
}

// RunRetention applies the retention window in the background so an operator
// does not have to remember to prune the table.
func (s *Service) RunRetention(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		s.applyRetention(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) applyRetention(ctx context.Context) {
	days := s.RetentionDays(ctx)
	if days <= 0 {
		return
	}
	if _, err := s.PurgeHistory(ctx, s.now().AddDate(0, 0, -days), "system"); err != nil {
		s.logger.Warn("AI history retention failed", "error", err)
	}
}
