package workbook

import (
	"fmt"
	"strings"
	"time"

	"kanpic/pkg/cellrange"
)

// MaxWatchRules 는 한 워크북에서 지켜볼 수 있는 범위의 수다. 셀이 바뀔
// 때마다 이 목록을 훑으므로 한없이 늘리면 저장이 느려진다.
const MaxWatchRules = 200

// WatchRule 은 "이 범위가 바뀌면 알려줘" 하나다. 범위를 비워 두면 시트
// 전체를 지켜본다.
type WatchRule struct {
	ID         string    `json:"id"`
	WorkbookID string    `json:"workbook_id"`
	SheetID    string    `json:"sheet_id"`
	CreateKey  string    `json:"-"`
	Watcher    string    `json:"watcher"`
	Range      string    `json:"range,omitempty"`
	Label      string    `json:"label,omitempty"`
	Enabled    bool      `json:"enabled"`
	Revision   int64     `json:"revision"`
	CreatedBy  string    `json:"created_by"`
	UpdatedBy  string    `json:"updated_by"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type CreateWatchRuleInput struct {
	IdempotencyKey string `json:"idempotency_key"`
	SheetID        string `json:"sheet_id"`
	Watcher        string `json:"watcher,omitempty"`
	Range          string `json:"range,omitempty"`
	Label          string `json:"label,omitempty"`
}

type UpdateWatchRuleInput struct {
	Range            *string `json:"range,omitempty"`
	Label            *string `json:"label,omitempty"`
	Enabled          *bool   `json:"enabled,omitempty"`
	ExpectedRevision *int64  `json:"expected_revision,omitempty"`
}

// normalizeWatchRule 은 저장하기 전에 규칙을 다듬는다.
func normalizeWatchRule(item WatchRule) (WatchRule, error) {
	item.Watcher = strings.TrimSpace(item.Watcher)
	if item.Watcher == "" {
		return WatchRule{}, fmt.Errorf("%w: a watch rule needs someone to notify", ErrInvalid)
	}
	item.Label = strings.TrimSpace(item.Label)
	if len([]rune(item.Label)) > 200 {
		return WatchRule{}, fmt.Errorf("%w: a watch rule label may be at most 200 characters", ErrInvalid)
	}
	target := strings.TrimSpace(item.Range)
	if target == "" {
		// 범위를 적지 않으면 시트 전체다.
		item.Range = ""
		return item, nil
	}
	parsed, err := cellrange.Parse(target)
	if err != nil {
		return WatchRule{}, fmt.Errorf("%w: watch range is not a range", ErrInvalid)
	}
	item.Range = cellrange.Address(parsed.Start.Row, parsed.Start.Column) + ":" + cellrange.Address(parsed.End.Row, parsed.End.Column)
	return item, nil
}

// WatchRuleCovers 는 바뀐 칸이 이 규칙이 지켜보는 자리인지 본다. 범위가
// 비어 있으면 시트의 모든 칸이다.
func WatchRuleCovers(rule WatchRule, row, column int) bool {
	if strings.TrimSpace(rule.Range) == "" {
		return true
	}
	parsed, err := cellrange.Parse(rule.Range)
	if err != nil {
		return false
	}
	return row >= parsed.Start.Row && row <= parsed.End.Row &&
		column >= parsed.Start.Column && column <= parsed.End.Column
}

// WatchersToNotify 는 이번 변경으로 알려야 할 사람과, 각자에게 보여 줄
// 첫 칸을 고른다.
//
// 자기가 고친 것은 알리지 않는다. 자기 손으로 바꾼 것을 메일로 다시
// 받는 것은 알림이 아니라 소음이다.
//
// 한 번의 저장에 칸이 500개 바뀌어도 사람마다 한 통이다. 칸마다 보내면
// 받은 편지함이 막힌다.
func WatchersToNotify(rules []WatchRule, actor string, changed []CellCoordinate) map[string]WatchNotice {
	notices := map[string]WatchNotice{}
	for _, rule := range rules {
		if !rule.Enabled || strings.EqualFold(strings.TrimSpace(rule.Watcher), strings.TrimSpace(actor)) {
			continue
		}
		count := 0
		first := ""
		for _, cell := range changed {
			if !WatchRuleCovers(rule, cell.Row, cell.Column) {
				continue
			}
			if count == 0 {
				first = cellrange.Address(cell.Row, cell.Column)
			}
			count++
		}
		if count == 0 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(rule.Watcher))
		notice := notices[key]
		notice.Watcher = rule.Watcher
		notice.Cells += count
		if notice.FirstCell == "" {
			notice.FirstCell = first
		}
		if notice.Label == "" {
			notice.Label = rule.Label
		}
		notice.Ranges = appendUnique(notice.Ranges, rule.Range)
		notices[key] = notice
	}
	return notices
}

// WatchNotice 는 한 사람에게 보낼 한 통의 내용이다.
type WatchNotice struct {
	Watcher   string
	Label     string
	Ranges    []string
	FirstCell string
	Cells     int
}

func appendUnique(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

// transformWatchRuleForStructure 는 행이나 열이 끼워지고 지워질 때 지켜보는
// 범위를 같이 옮긴다. 조건부 서식이나 보호 범위와 같은 자리에 있어야 한다.
// 5행 위에 한 행을 끼웠는데 규칙이 그대로 B5:B9 를 보고 있으면, 사람은
// 자기가 정한 칸을 지켜본다고 믿는 채로 엉뚱한 칸의 알림을 받는다.
//
// 시트 전체를 보는 규칙은 범위가 비어 있으므로 옮길 것이 없다.
func transformWatchRuleForStructure(rule WatchRule, input StructuralMutation, actor string, now time.Time) (WatchRule, bool, error) {
	if strings.TrimSpace(rule.Range) == "" {
		return rule, true, nil
	}
	transformed, exists, err := transformRangeAddress(rule.Range, input)
	if err != nil {
		return WatchRule{}, false, fmt.Errorf("%w: watch rule range exceeds spreadsheet bounds", ErrInvalid)
	}
	// 지켜보던 칸이 통째로 지워졌으면 볼 것이 없다. 시트 전체를 보는 규칙으로
	// 바꾸면 사람이 부탁한 적 없는 알림을 받게 되므로 규칙을 지운다.
	if !exists {
		return WatchRule{}, false, nil
	}
	if transformed == rule.Range {
		return rule, true, nil
	}
	rule.Range, rule.Revision, rule.UpdatedBy, rule.UpdatedAt = transformed, rule.Revision+1, actor, now
	normalized, err := normalizeWatchRule(rule)
	return normalized, err == nil, err
}
