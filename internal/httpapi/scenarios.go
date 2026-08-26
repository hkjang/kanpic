package httpapi

import (
	"fmt"
	"net/http"
	"strings"

	"kanpic/internal/formula"
	"kanpic/internal/workbook"
)

// MaxScenarioCells 는 시나리오를 견줄 때 읽을 워크북의 크기다.
const MaxScenarioCells = 50_000

// MaxScenarioWork 는 한 번의 견주기가 시킬 수 있는 셈의 양이다. 데이터 표와
// 같은 까닭으로, 벌 수와 워크북 크기의 곱을 본다.
const MaxScenarioWork = 8_000_000

func (s *Server) listScenarios(w http.ResponseWriter, r *http.Request) {
	items, err := s.repository.ListScenarios(r.Context(), r.PathValue("workbookId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createScenario(w http.ResponseWriter, r *http.Request) {
	var input workbook.CreateScenarioInput
	if !decodeJSON(w, r, &input) {
		return
	}
	if key := strings.TrimSpace(r.Header.Get("Idempotency-Key")); key != "" {
		input.IdempotencyKey = key
	}
	item, err := s.repository.CreateScenario(r.Context(), r.PathValue("workbookId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), item.SheetID, "", item.WorkbookVersion)
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) getScenario(w http.ResponseWriter, r *http.Request) {
	item, err := s.repository.GetScenario(r.Context(), r.PathValue("scenarioId"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) updateScenario(w http.ResponseWriter, r *http.Request) {
	var input workbook.UpdateScenarioInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.repository.UpdateScenario(r.Context(), r.PathValue("scenarioId"), actorID(r), input)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	s.collab.PublishVersion(item.WorkbookID, actorID(r), item.SheetID, "", item.WorkbookVersion)
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteScenario(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("scenarioId")
	current, err := s.repository.GetScenario(r.Context(), id)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	expected, err := optionalRevision(r.URL.Query().Get("expected_revision"))
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if err := s.repository.DeleteScenario(r.Context(), id, actorID(r), expected); err != nil {
		s.writeError(w, r, err)
		return
	}
	s.publishCurrentVersion(r.Context(), current.WorkbookID, actorID(r), current.SheetID)
	w.WriteHeader(http.StatusNoContent)
}

type scenarioCompareRequest struct {
	Targets     []string `json:"targets"`
	ScenarioIDs []string `json:"scenario_ids,omitempty"`
}

// compareScenarios 는 저장해 둔 시나리오들을 나란히 놓고 견준다. 아무것도
// 쓰지 않는다 — 시트는 그대로 두고 답만 낸다.
func (s *Server) compareScenarios(w http.ResponseWriter, r *http.Request) {
	var request scenarioCompareRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	sheetID := r.PathValue("sheetId")
	ctx := r.Context()
	workbookID, err := s.repository.WorkbookIDForResource(ctx, "sheetId", sheetID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	stored, err := s.repository.ListScenarios(ctx, workbookID)
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	wanted := map[string]bool{}
	for _, id := range request.ScenarioIDs {
		wanted[strings.TrimSpace(id)] = true
	}
	sets := make([]formula.ScenarioSet, 0, len(stored))
	for _, item := range stored {
		if len(wanted) > 0 && !wanted[item.ID] {
			continue
		}
		inputs := make(map[string]float64, len(item.Inputs))
		for _, assumption := range item.Inputs {
			if assumption.Value == nil {
				continue
			}
			inputs[formula.CellKey(item.SheetID, assumption.Cell)] = *assumption.Value
		}
		if len(inputs) == 0 {
			continue
		}
		sets = append(sets, formula.ScenarioSet{Name: item.Name, Inputs: inputs})
	}
	if len(sets) == 0 {
		s.writeError(w, r, fmt.Errorf("%w: 견줄 시나리오가 없습니다", workbook.ErrInvalid))
		return
	}
	state, evaluator, err := s.workbookFormulaState(ctx, sheetID, MaxScenarioCells, "시나리오 견주기")
	if err != nil {
		s.writeError(w, r, err)
		return
	}
	if len(sets)*len(state) > MaxScenarioWork {
		s.writeError(w, r, fmt.Errorf("%w: 이 워크북에는 견줄 시나리오가 너무 많습니다. 몇 개만 골라 보세요", workbook.ErrInvalid))
		return
	}
	targets := make([]string, 0, len(request.Targets))
	for _, target := range request.Targets {
		targets = append(targets, formula.CellKey(sheetID, strings.TrimSpace(target)))
	}
	result, err := evaluator.CompareScenarios(state, formula.ScenarioCompareInput{Targets: targets, Sets: sets})
	if err != nil {
		s.writeError(w, r, fmt.Errorf("%w: %s", workbook.ErrInvalid, err.Error()))
		return
	}
	labels := make([]string, 0, len(request.Targets))
	for _, target := range request.Targets {
		labels = append(labels, strings.ToUpper(strings.TrimSpace(target)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"targets": labels, "result": result})
}
