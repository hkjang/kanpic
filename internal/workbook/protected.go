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
	MaxProtectedExceptions   = 20
	maxProtectionDescription = 200
)

// SheetProtectionRange is the range a sheet-wide protection stores. A sheet
// protection still needs a concrete range so every existing query, structural
// transform and client that reads one keeps working; what makes it sheet-wide
// is the scope, and the exceptions are the holes cut in it.
const SheetProtectionRange = "A1:XFD1048576"

// NormalizeProtectedRange cleans a rule and reports what makes it unusable.
func NormalizeProtectedRange(rule ProtectedRange) (ProtectedRange, cellrange.Range, error) {
	rule.Scope = strings.ToLower(strings.TrimSpace(rule.Scope))
	if rule.Scope == "" {
		rule.Scope = "range"
	}
	if rule.Scope != "range" && rule.Scope != "sheet" {
		return ProtectedRange{}, cellrange.Range{}, fmt.Errorf("%w: scope must be range or sheet", ErrInvalid)
	}
	if rule.Scope == "sheet" {
		rule.Range = SheetProtectionRange
	} else {
		rule.Exceptions = nil
	}
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
	exceptions, err := normalizeProtectionExceptions(rule.Exceptions)
	if err != nil {
		return ProtectedRange{}, cellrange.Range{}, err
	}
	rule.Exceptions = exceptions
	if rule.Revision < 1 {
		rule.Revision = 1
	}
	return rule, selected, nil
}

// normalizeProtectionExceptions cleans the ranges a sheet protection leaves
// editable. They are the input cells of a locked sheet, so an unreadable one is
// refused rather than dropped: silently locking a form field is worse than
// refusing to save the rule.
func normalizeProtectionExceptions(input []string) ([]string, error) {
	if len(input) == 0 {
		return nil, nil
	}
	if len(input) > MaxProtectedExceptions {
		return nil, fmt.Errorf("%w: a protection can leave at most %d ranges editable", ErrInvalid, MaxProtectedExceptions)
	}
	result := make([]string, 0, len(input))
	seen := make(map[string]struct{}, len(input))
	for _, item := range input {
		item = strings.ToUpper(strings.TrimSpace(item))
		if item == "" {
			continue
		}
		selected, err := cellrange.Parse(item)
		if err != nil {
			return nil, fmt.Errorf("%w: %s is not a valid range", ErrInvalid, item)
		}
		normalized := cellrange.Address(selected.Start.Row, selected.Start.Column) + ":" + cellrange.Address(selected.End.Row, selected.End.Column)
		if _, duplicate := seen[normalized]; duplicate {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	sort.Strings(result)
	if len(result) == 0 {
		return nil, nil
	}
	return result, nil
}

// protectionCovers reports whether a rule guards one cell: inside its range and
// outside every range it leaves editable.
func protectionCovers(selected cellrange.Range, exceptions []cellrange.Range, row, column int) bool {
	if !selected.Contains(row, column) {
		return false
	}
	for _, exception := range exceptions {
		if exception.Contains(row, column) {
			return false
		}
	}
	return true
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
		rule       ProtectedRange
		selected   cellrange.Range
		exceptions []cellrange.Range
		allowed    bool
	}
	prepared := make([]bounds, 0, len(rules))
	for _, rule := range rules {
		normalized, selected, err := NormalizeProtectedRange(rule)
		if err != nil {
			continue
		}
		exceptions := make([]cellrange.Range, 0, len(normalized.Exceptions))
		for _, item := range normalized.Exceptions {
			if parsed, parseErr := cellrange.Parse(item); parseErr == nil {
				exceptions = append(exceptions, parsed)
			}
		}
		prepared = append(prepared, bounds{rule: normalized, selected: selected, exceptions: exceptions, allowed: MayEditProtected(normalized, actor, ownerID)})
	}
	for _, input := range submitted {
		for _, item := range prepared {
			if item.allowed || !protectionCovers(item.selected, item.exceptions, input.Row, input.Column) {
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
	subject := rule.Range + " 범위는"
	if rule.Scope == "sheet" {
		subject = "이 시트는"
	}
	if rule.Description != "" {
		return fmt.Sprintf("%s 보호되어 있습니다: %s", subject, rule.Description)
	}
	return fmt.Sprintf("%s 보호되어 있어 편집 권한이 필요합니다.", subject)
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
	selected := rule.Range
	// A sheet protection already covers every row and column, so inserting one
	// cannot move it — and pushing its end past the last row would only look
	// like an out-of-bounds range.
	if rule.Scope != "sheet" {
		transformed, exists, err := formula.TransformRangeAddress(rule.Range, formulaStructuralChange(input, "", ""))
		if err != nil {
			return ProtectedRange{}, false, fmt.Errorf("%w: protected range exceeds spreadsheet bounds", ErrInvalid)
		}
		if !exists {
			return ProtectedRange{}, false, nil
		}
		selected = transformed
	}
	exceptions := make([]string, 0, len(rule.Exceptions))
	for _, item := range rule.Exceptions {
		moved, stillExists, transformErr := formula.TransformRangeAddress(item, formulaStructuralChange(input, "", ""))
		if transformErr != nil {
			return ProtectedRange{}, false, fmt.Errorf("%w: protected range exceeds spreadsheet bounds", ErrInvalid)
		}
		if stillExists {
			exceptions = append(exceptions, moved)
		}
	}
	movedExceptions := strings.Join(exceptions, ",") != strings.Join(rule.Exceptions, ",")
	rule.Exceptions = exceptions
	rule.Range = selected
	if rule.Range == original && !movedExceptions {
		return rule, true, nil
	}
	rule.Revision++
	rule.UpdatedBy, rule.UpdatedAt = actor, now
	return rule, true, nil
}
