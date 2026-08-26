package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

type formulaInfoResult struct {
	Cell         workbook.Cell `json:"cell"`
	Dependencies []string      `json:"dependencies"`
	Dependents   []string      `json:"dependents"`
}

func (s *Server) evaluateFormula(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Formula string         `json:"formula"`
		Cells   map[string]any `json:"cells"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if input.Formula == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": map[string]string{"code": "invalid_formula", "message": "formula is required"}})
		return
	}
	writeJSON(w, http.StatusOK, s.formula.Evaluate(input.Formula, input.Cells))
}

func (s *Server) formulaInfo(w http.ResponseWriter, r *http.Request) {
	result, err := s.getFormulaInfo(r.Context(), r.PathValue("sheetId"), r.PathValue("address"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *Server) getFormulaInfo(ctx context.Context, sheetID, address string) (formulaInfoResult, error) {
	selected, err := cellrange.Parse(address)
	if err != nil || selected.Start != selected.End {
		return formulaInfoResult{}, fmt.Errorf("%w: a single A1 address is required", workbook.ErrInvalid)
	}
	canonical := cellrange.Address(selected.Start.Row, selected.Start.Column)
	selectedCells, err := s.repository.ReadRange(ctx, sheetID, selected)
	if err != nil {
		return formulaInfoResult{}, err
	}
	if len(selectedCells) != 1 || selectedCells[0].Formula == "" {
		return formulaInfoResult{}, workbook.ErrNotFound
	}
	books, err := s.repository.ListWorkbooks(ctx, "", workbook.AccessPrincipal{Admin: true, Authenticated: true})
	if err != nil {
		return formulaInfoResult{}, err
	}
	var book workbook.Workbook
	found := false
	for _, candidate := range books {
		for _, sheet := range candidate.Sheets {
			if sheet.ID == sheetID {
				book, found = candidate, true
				break
			}
		}
		if found {
			break
		}
	}
	if !found {
		return formulaInfoResult{}, workbook.ErrNotFound
	}
	sheetNames := make(map[string]string, len(book.Sheets))
	idNames := make(map[string]string, len(book.Sheets))
	for _, sheet := range book.Sheets {
		sheetNames[sheet.Name] = sheet.ID
		idNames[strings.ToUpper(sheet.ID)] = sheet.Name
	}
	namedRanges, err := s.repository.ListNamedRanges(ctx, book.ID)
	if err != nil {
		return formulaInfoResult{}, err
	}
	formulaNames := make(map[string]formula.NamedRange, len(namedRanges))
	for _, item := range namedRanges {
		formulaNames[item.Name] = formula.NamedRange{SheetID: item.SheetID, Range: item.Range}
	}
	namedFunctions, err := s.repository.ListNamedFunctions(ctx, book.ID)
	if err != nil {
		return formulaInfoResult{}, err
	}
	functionDefinitions := workbook.NamedFunctionDefinitions(namedFunctions)
	// 표도 함께 넘긴다. 모르면 =SUM(매출표[금액]) 이 든 칸을 설명해 달라고
	// 했을 때 풀리지 않는 수식이라고 답한다 — 격자에서는 멀쩡히 셈되는데도.
	tableDefinitions, err := s.tablesForFormula(ctx, book.ID)
	if err != nil {
		return formulaInfoResult{}, err
	}
	evaluator := formula.NewScopedWithNames(sheetID, sheetNames, formulaNames)
	evaluator.SetNamedFunctions(functionDefinitions)
	evaluator.SetTables(tableDefinitions)
	dependencies, formulaErr := evaluator.Dependencies(selectedCells[0].Formula)
	if formulaErr != nil {
		return formulaInfoResult{}, fmt.Errorf("%w: %s", workbook.ErrInvalid, formulaErr.Message)
	}
	displayDependencies := make([]string, len(dependencies))
	for index, dependency := range dependencies {
		displayDependencies[index] = displayFormulaCell(dependency, sheetID, idNames)
	}
	dependents := make([]string, 0)
	target := formula.CellKey(sheetID, canonical)
	for _, sheet := range book.Sheets {
		allCells, readErr := s.repository.ReadAllCells(ctx, sheet.ID)
		if readErr != nil {
			return formulaInfoResult{}, readErr
		}
		candidateEvaluator := formula.NewScopedWithNames(sheet.ID, sheetNames, formulaNames)
		candidateEvaluator.SetNamedFunctions(functionDefinitions)
		candidateEvaluator.SetTables(tableDefinitions)
		for _, candidate := range allCells {
			if candidate.Formula == "" {
				continue
			}
			candidateDependencies, candidateErr := candidateEvaluator.Dependencies(candidate.Formula)
			if candidateErr != nil {
				continue
			}
			for _, dependency := range candidateDependencies {
				if dependency == target {
					dependentKey := formula.CellKey(sheet.ID, cellrange.Address(candidate.Row, candidate.Column))
					dependents = append(dependents, displayFormulaCell(dependentKey, sheetID, idNames))
					break
				}
			}
		}
	}
	sort.Strings(displayDependencies)
	sort.Strings(dependents)
	return formulaInfoResult{Cell: selectedCells[0], Dependencies: displayDependencies, Dependents: dependents}, nil
}

func displayFormulaCell(key, currentSheetID string, idNames map[string]string) string {
	sheetID, address, valid := formula.SplitCellKey(key)
	if !valid || sheetID == "" || strings.EqualFold(sheetID, currentSheetID) {
		return address
	}
	name := idNames[sheetID]
	if name == "" {
		name = sheetID
	}
	if strings.ContainsAny(name, " !'") {
		name = "'" + strings.ReplaceAll(name, "'", "''") + "'"
	}
	return name + "!" + address
}

// tablesForFormula 는 워크북의 표를 엔진이 아는 꼴로 읽어 온다.
//
// 워크북을 통째로 읽어 다시 셈하는 자리가 여럿이다 — 목표값 찾기, 수식 설명.
// 그 가운데 하나라도 표를 모르면 =SUM(매출표[금액]) 이 든 수식이 풀리지
// 않는다. 격자에서는 멀쩡히 셈되는데 그 자리에서만 아니라고 답하게 되므로,
// 읽어 오는 자리를 하나로 둔다.
func (s *Server) tablesForFormula(ctx context.Context, workbookID string) (map[string]formula.Table, error) {
	tables, err := s.repository.ListSheetTables(ctx, workbookID)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		return nil, nil
	}
	// 표마다 머리글 줄을 한 번만 읽는다. 열 이름은 거기서 온다.
	headers := make(map[string]string)
	for _, item := range tables {
		selected, parseErr := cellrange.Parse(item.Range)
		if parseErr != nil {
			continue
		}
		row := cellrange.Range{
			Start: cellrange.Position{Row: selected.Start.Row, Column: selected.Start.Column},
			End:   cellrange.Position{Row: selected.Start.Row, Column: selected.End.Column},
		}
		cells, readErr := s.repository.ReadRange(ctx, item.SheetID, row)
		if readErr != nil {
			return nil, readErr
		}
		for _, cell := range cells {
			var value any
			if len(cell.Value) > 0 {
				_ = json.Unmarshal(cell.Value, &value)
			}
			if text, ok := value.(string); ok {
				headers[formula.CellKey(item.SheetID, cellrange.Address(cell.Row, cell.Column))] = text
			}
		}
	}
	return workbook.TablesForFormula(tables, func(sheetID string, row, column int) string {
		return headers[formula.CellKey(sheetID, cellrange.Address(row, column))]
	}), nil
}
