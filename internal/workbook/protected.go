package workbook

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"kanpic/internal/formula"
	"kanpic/pkg/cellrange"
)

const (
	MaxProtectedRanges       = 200
	MaxProtectedEditors      = 200
	maxProtectionDescription = 200
)

// NormalizeProtectedRange cleans a rule and reports what makes it unusable.
func NormalizeProtectedRange(rule ProtectedRange) (ProtectedRange, cellrange.Range, error) {
	rule.Range = strings.ToUpper(strings.TrimSpace(rule.Range))
	rule.Description = strings.TrimSpace(rule.Description)
	selected, err := cellrange.Parse(rule.Range)
	if err != nil {
		return ProtectedRange{}, cellrange.Range{}, fmt.Errorf("%w: invalid protected range", ErrInvalid)
	}
	if len([]rune(rule.Description)) > maxProtectionDescription {
		return ProtectedRange{}, cellrange.Range{}, fmt.Errorf("%w: description exceeds %d characters", ErrInvalid, maxProtectionDescription)
	}
	editors := make([]string, 0, len(rule.Editors))
	seen := make(map[string]struct{}, len(rule.Editors))
	for _, editor := range rule.Editors {
		editor = strings.TrimSpace(editor)
		if editor == "" {
			continue
		}
		key := strings.ToLower(editor)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		editors = append(editors, editor)
	}
	if len(editors) > MaxProtectedEditors {
		return ProtectedRange{}, cellrange.Range{}, fmt.Errorf("%w: a protected range can list %d editors", ErrInvalid, MaxProtectedEditors)
	}
	sort.Strings(editors)
	rule.Editors = editors
	if rule.Revision < 1 {
		rule.Revision = 1
	}
	return rule, selected, nil
}

// MayEditProtected reports whether an actor is allowed to write inside a
// protected range. The workbook owner always is: protection guards a shared
// model from its collaborators, not the owner from their own sheet.
func MayEditProtected(rule ProtectedRange, actor, ownerID string) bool {
	if actor == "" {
		return false
	}
	if strings.EqualFold(actor, ownerID) || strings.EqualFold(actor, rule.CreatedBy) {
		return true
	}
	for _, editor := range rule.Editors {
		if strings.EqualFold(editor, actor) {
			return true
		}
	}
	return false
}

// ProtectionViolation names one cell an actor may not write to.
type ProtectionViolation struct {
	ProtectionID string `json:"protection_id"`
	Row          int    `json:"row"`
	Column       int    `json:"column"`
	Message      string `json:"message"`
}

// CheckProtectedRanges separates the writes an actor may not make from the
// ones that are only worth warning about. A blocked write stops the whole
// operation, because half-applying a paste over a protected block would be
// worse than refusing it.
func CheckProtectedRanges(rules []ProtectedRange, actor, ownerID string, submitted []CellInput) (blocked []ProtectionViolation, warnings []ProtectionViolation) {
	if len(rules) == 0 || len(submitted) == 0 {
		return nil, nil
	}
	type bounds struct {
		rule     ProtectedRange
		selected cellrange.Range
		allowed  bool
	}
	prepared := make([]bounds, 0, len(rules))
	for _, rule := range rules {
		normalized, selected, err := NormalizeProtectedRange(rule)
		if err != nil {
			continue
		}
		prepared = append(prepared, bounds{rule: normalized, selected: selected, allowed: MayEditProtected(normalized, actor, ownerID)})
	}
	for _, input := range submitted {
		for _, item := range prepared {
			if item.allowed || !item.selected.Contains(input.Row, input.Column) {
				continue
			}
			violation := ProtectionViolation{
				ProtectionID: item.rule.ID, Row: input.Row, Column: input.Column,
				Message: protectionMessage(item.rule),
			}
			if item.rule.WarningOnly {
				warnings = append(warnings, violation)
			} else {
				blocked = append(blocked, violation)
			}
			break
		}
	}
	return blocked, warnings
}

func protectionMessage(rule ProtectedRange) string {
	if rule.Description != "" {
		return fmt.Sprintf("%s 범위는 보호되어 있습니다: %s", rule.Range, rule.Description)
	}
	return fmt.Sprintf("%s 범위는 보호되어 있어 편집 권한이 필요합니다.", rule.Range)
}

// ProtectionFailure is returned when a write lands on a protected range.
type ProtectionFailure struct {
	Violations []ProtectionViolation
}

func (f *ProtectionFailure) Error() string {
	if len(f.Violations) == 0 {
		return "보호된 범위입니다."
	}
	return f.Violations[0].Message
}

// transformProtectedForStructure moves a protected range with the rows and
// columns it guards, and reports whether the rule survived.
func transformProtectedForStructure(rule ProtectedRange, targetSheetID string, input StructuralMutation, actor string, now time.Time) (ProtectedRange, bool, error) {
	if rule.SheetID != targetSheetID {
		return rule, true, nil
	}
	original := rule.Range
	selected, exists, err := formula.TransformRangeAddress(rule.Range, formulaStructuralChange(input, "", ""))
	if err != nil {
		return ProtectedRange{}, false, fmt.Errorf("%w: protected range exceeds spreadsheet bounds", ErrInvalid)
	}
	if !exists {
		return ProtectedRange{}, false, nil
	}
	rule.Range = selected
	if rule.Range == original {
		return rule, true, nil
	}
	rule.Revision++
	rule.UpdatedBy, rule.UpdatedAt = actor, now
	return rule, true, nil
}
