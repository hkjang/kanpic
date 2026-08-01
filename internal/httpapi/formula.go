package httpapi

import (
	"context"
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
	books, err := s.repository.ListWorkbooks(ctx, "")
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
	evaluator := formula.NewScoped(sheetID, sheetNames)
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
		candidateEvaluator := formula.NewScoped(sheet.ID, sheetNames)
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
