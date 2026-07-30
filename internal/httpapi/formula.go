package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"

	"kanpic/internal/workbook"
	"kanpic/pkg/cellrange"
)

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

func (s *Server) calculateFormulaCells(r *http.Request, sheetID string, inputs []workbook.CellInput) error {
	values := make(map[string]any)
	for _, input := range inputs {
		if len(input.Value) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(input.Value, &value); err != nil {
			return fmt.Errorf("%w: invalid cell value", workbook.ErrInvalid)
		}
		values[cellrange.Address(input.Row, input.Column)] = value
	}
	for index := range inputs {
		if inputs[index].Formula == "" || len(inputs[index].Value) > 0 {
			continue
		}
		probe := s.formula.Evaluate(inputs[index].Formula, nil)
		for _, dependency := range probe.Dependencies {
			if _, exists := values[dependency]; exists {
				continue
			}
			selected, err := cellrange.Parse(dependency)
			if err != nil {
				continue
			}
			cells, err := s.repository.ReadRange(r.Context(), sheetID, selected)
			if err != nil {
				return err
			}
			if len(cells) > 0 && len(cells[0].Value) > 0 {
				var value any
				if json.Unmarshal(cells[0].Value, &value) == nil {
					values[dependency] = value
				}
			}
		}
		result := s.formula.Evaluate(inputs[index].Formula, values)
		value := result.Value
		if result.Error != nil {
			value = result.Error.Code
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return err
		}
		inputs[index].Value = encoded
		values[cellrange.Address(inputs[index].Row, inputs[index].Column)] = value
	}
	return nil
}
