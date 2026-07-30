package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"sort"

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
	dependencies, formulaErr := s.formula.Dependencies(selectedCells[0].Formula)
	if formulaErr != nil {
		return formulaInfoResult{}, fmt.Errorf("%w: %s", workbook.ErrInvalid, formulaErr.Message)
	}
	allCells, err := s.repository.ReadAllCells(ctx, sheetID)
	if err != nil {
		return formulaInfoResult{}, err
	}
	dependents := make([]string, 0)
	for _, candidate := range allCells {
		if candidate.Formula == "" {
			continue
		}
		candidateDependencies, candidateErr := s.formula.Dependencies(candidate.Formula)
		if candidateErr != nil {
			continue
		}
		for _, dependency := range candidateDependencies {
			if dependency == canonical {
				dependents = append(dependents, cellrange.Address(candidate.Row, candidate.Column))
				break
			}
		}
	}
	sort.Strings(dependents)
	return formulaInfoResult{Cell: selectedCells[0], Dependencies: dependencies, Dependents: dependents}, nil
}
