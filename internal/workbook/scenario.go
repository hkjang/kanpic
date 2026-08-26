package workbook

import (
	"fmt"
	"strings"
	"time"

	"kanpic/pkg/cellrange"
)

// MaxScenarios 는 한 워크북에 담을 수 있는 시나리오의 수다.
const MaxScenarios = 100

// MaxScenarioInputs 는 한 시나리오가 담을 수 있는 가정의 수다. 견줄 때 그
// 수만큼 칸을 바꿔 다시 셈한다.
const MaxScenarioInputs = 50

// Scenario 는 가정 한 벌에 붙인 이름이다.
//
// 데이터 표가 한두 칸을 여러 값으로 바꿔 보는 것이라면, 시나리오는 여러 칸을
// 한 벌로 묶어 "낙관", "보수" 처럼 이름을 붙이는 것이다. 회의에서 두 안을
// 나란히 놓고 보는 그 일이다.
type Scenario struct {
	ID              string          `json:"id"`
	WorkbookID      string          `json:"workbook_id"`
	WorkbookVersion int64           `json:"workbook_version"`
	SheetID         string          `json:"sheet_id"`
	CreateKey       string          `json:"-"`
	Name            string          `json:"name"`
	Inputs          []ScenarioInput `json:"inputs"`
	Note            string          `json:"note,omitempty"`
	Revision        int64           `json:"revision"`
	CreatedBy       string          `json:"created_by"`
	UpdatedBy       string          `json:"updated_by"`
	CreatedAt       time.Time       `json:"created_at"`
	UpdatedAt       time.Time       `json:"updated_at"`
}

// ScenarioInput 은 어느 칸을 어떤 값으로 볼지다.
type ScenarioInput struct {
	Cell  string   `json:"cell"`
	Value *float64 `json:"value"`
}

type CreateScenarioInput struct {
	IdempotencyKey string          `json:"idempotency_key"`
	SheetID        string          `json:"sheet_id"`
	Name           string          `json:"name"`
	Inputs         []ScenarioInput `json:"inputs"`
	Note           string          `json:"note,omitempty"`
}

type UpdateScenarioInput struct {
	Name             *string          `json:"name,omitempty"`
	Inputs           *[]ScenarioInput `json:"inputs,omitempty"`
	Note             *string          `json:"note,omitempty"`
	ExpectedRevision *int64           `json:"expected_revision,omitempty"`
}

// normalizeScenario 는 담기 전에 한 가지 모양으로 다듬는다.
func normalizeScenario(item Scenario) (Scenario, error) {
	item.Name = strings.TrimSpace(item.Name)
	if item.Name == "" || len([]rune(item.Name)) > 100 {
		return Scenario{}, fmt.Errorf("%w: a scenario needs a name of at most 100 characters", ErrInvalid)
	}
	if strings.TrimSpace(item.SheetID) == "" {
		return Scenario{}, fmt.Errorf("%w: a scenario needs a sheet", ErrInvalid)
	}
	if len(item.Inputs) == 0 {
		return Scenario{}, fmt.Errorf("%w: a scenario needs at least one assumption", ErrInvalid)
	}
	if len(item.Inputs) > MaxScenarioInputs {
		return Scenario{}, fmt.Errorf("%w: a scenario may hold at most %d assumptions", ErrInvalid, MaxScenarioInputs)
	}
	seen := make(map[string]struct{}, len(item.Inputs))
	inputs := make([]ScenarioInput, 0, len(item.Inputs))
	for _, input := range item.Inputs {
		parsed, err := cellrange.Parse(strings.ReplaceAll(strings.TrimSpace(input.Cell), "$", ""))
		if err != nil || parsed.Start != parsed.End {
			return Scenario{}, fmt.Errorf("%w: %s is not a single cell", ErrInvalid, input.Cell)
		}
		address := cellrange.Address(parsed.Start.Row, parsed.Start.Column)
		// 같은 칸에 값이 둘이면 어느 쪽으로 셈할지 알 수 없다.
		if _, duplicate := seen[address]; duplicate {
			return Scenario{}, fmt.Errorf("%w: %s appears twice", ErrInvalid, address)
		}
		seen[address] = struct{}{}
		inputs = append(inputs, ScenarioInput{Cell: address, Value: input.Value})
	}
	item.Inputs = inputs
	item.Note = strings.TrimSpace(item.Note)
	return item, nil
}

// transformScenarioForStructure 는 행과 열이 움직일 때 가정이 가리키는 칸을
// 따라 옮긴다.
//
// 옮기지 않으면 "낙관" 시나리오가 엉뚱한 칸에 값을 넣게 되는데, 이름은 그대로
// 라서 사람은 그것이 여전히 그 가정인 줄 안다.
func transformScenarioForStructure(item Scenario, input StructuralMutation, actor string, now time.Time) (Scenario, bool, error) {
	inputs := make([]ScenarioInput, 0, len(item.Inputs))
	moved := false
	for _, assumption := range item.Inputs {
		transformed, exists, err := transformRangeAddress(assumption.Cell+":"+assumption.Cell, input)
		if err != nil {
			return Scenario{}, false, fmt.Errorf("%w: scenario cell exceeds spreadsheet bounds", ErrInvalid)
		}
		// 가리키던 칸이 지워졌으면 그 가정은 갈 곳이 없다. 나머지는 남긴다 —
		// 한 칸이 사라졌다고 시나리오 전체를 버리면 사람이 적어 둔 것을
		// 통째로 잃는다.
		if !exists {
			moved = true
			continue
		}
		address := strings.Split(transformed, ":")[0]
		if address != assumption.Cell {
			moved = true
		}
		inputs = append(inputs, ScenarioInput{Cell: address, Value: assumption.Value})
	}
	if !moved {
		return item, true, nil
	}
	// 가정이 하나도 남지 않으면 시나리오가 아니다.
	if len(inputs) == 0 {
		return Scenario{}, false, nil
	}
	item.Inputs, item.Revision, item.UpdatedBy, item.UpdatedAt = inputs, item.Revision+1, actor, now
	return item, true, nil
}

func cloneScenario(item Scenario) Scenario {
	item.Inputs = append([]ScenarioInput(nil), item.Inputs...)
	return item
}

// cloneScenariosForWorkbook 은 버전을 찍을 때 담아 둔다. 되돌렸는데 시나리오가
// 없으면, 그때 견주던 가정들이 사라진다.
func cloneScenariosForWorkbook(source map[string]Scenario, workbookID string) map[string]Scenario {
	result := make(map[string]Scenario)
	for id, item := range source {
		if item.WorkbookID == workbookID {
			result[id] = cloneScenario(item)
		}
	}
	return result
}
