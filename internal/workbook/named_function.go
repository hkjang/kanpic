package workbook

import (
	"fmt"
	"strings"
	"unicode"

	"kanpic/internal/formula"
)

// MaxNamedFunctions 는 한 워크북에 저장할 수 있는 이름 있는 수식의 수다.
// 이름은 수식을 풀 때마다 훑으므로 한없이 늘리면 계산이 느려진다.
const (
	MaxNamedFunctions          = 200
	MaxNamedFunctionParameters = 16
	maxNamedFunctionBody       = 4_000
)

// normalizeNamedFunction 은 저장하기 전에 정의를 다듬고 말이 되는지 본다.
// 셈이 되지 않는 것을 저장하면 그것을 쓰는 모든 칸이 한꺼번에 깨진다.
func normalizeNamedFunction(item NamedFunction, others []NamedFunction) (NamedFunction, error) {
	name, err := normalizeNamedFunctionName(item.Name)
	if err != nil {
		return NamedFunction{}, err
	}
	item.Name = name
	parameters := make([]string, 0, len(item.Parameters))
	seen := map[string]bool{}
	for _, parameter := range item.Parameters {
		parameter = strings.TrimSpace(parameter)
		if _, parameterErr := normalizeNamedFunctionName(parameter); parameterErr != nil {
			return NamedFunction{}, fmt.Errorf("%w: parameter %q is not a usable name", ErrInvalid, parameter)
		}
		if seen[strings.ToUpper(parameter)] {
			return NamedFunction{}, fmt.Errorf("%w: parameter %q appears twice", ErrInvalid, parameter)
		}
		seen[strings.ToUpper(parameter)] = true
		parameters = append(parameters, parameter)
	}
	if len(parameters) > MaxNamedFunctionParameters {
		return NamedFunction{}, fmt.Errorf("%w: a named function may take at most %d parameters", ErrInvalid, MaxNamedFunctionParameters)
	}
	item.Parameters = parameters
	body := strings.TrimSpace(item.Body)
	body = strings.TrimPrefix(body, "=")
	body = strings.TrimSpace(body)
	if body == "" {
		return NamedFunction{}, fmt.Errorf("%w: a named function needs a formula", ErrInvalid)
	}
	if len([]rune(body)) > maxNamedFunctionBody {
		return NamedFunction{}, fmt.Errorf("%w: a named function formula may be at most %d characters", ErrInvalid, maxNamedFunctionBody)
	}
	item.Body = body
	item.Description = strings.TrimSpace(item.Description)
	if len([]rune(item.Description)) > 500 {
		return NamedFunction{}, fmt.Errorf("%w: a named function description may be at most 500 characters", ErrInvalid)
	}
	if err := checkNamedFunctionFormula(item, others); err != nil {
		return NamedFunction{}, err
	}
	return item, nil
}

// checkNamedFunctionFormula 는 본문을 실제로 풀어 본다. 다른 이름 있는
// 수식을 부를 수 있으므로 그것들도 함께 알려 준다 — 자기 자신을 부르는
// 정의는 엔진이 깊이로 막는다.
func checkNamedFunctionFormula(item NamedFunction, others []NamedFunction) error {
	definitions := map[string]formula.NamedFunction{}
	for _, other := range others {
		if strings.EqualFold(other.Name, item.Name) {
			continue
		}
		definitions[other.Name] = formula.NamedFunction{Parameters: other.Parameters, Body: other.Body}
	}
	definitions[item.Name] = formula.NamedFunction{Parameters: item.Parameters, Body: item.Body}
	engine := formula.New()
	engine.SetNamedFunctions(definitions)
	arguments := make([]string, 0, len(item.Parameters))
	for range item.Parameters {
		arguments = append(arguments, "0")
	}
	call := "=" + item.Name + "(" + strings.Join(arguments, ",") + ")"
	if _, err := engine.Dependencies(call); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalid, err.Message)
	}
	return nil
}

// normalizeNamedFunctionName 은 이름 규칙을 본다. 이름 범위와 같은 규칙을
// 쓰되, 이미 있는 함수 이름은 따로 막는다 — 사람이 SUM 을 덮어쓰면 그
// 워크북의 모든 SUM 이 뜻을 잃는다.
func normalizeNamedFunctionName(value string) (string, error) {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) == 0 || len(runes) > 255 || (!unicode.IsLetter(runes[0]) && runes[0] != '_') {
		return "", fmt.Errorf("%w: name must start with a letter or underscore and contain at most 255 characters", ErrInvalid)
	}
	for _, character := range runes[1:] {
		if !unicode.IsLetter(character) && !unicode.IsDigit(character) && character != '_' && character != '.' {
			return "", fmt.Errorf("%w: name may contain only letters, numbers, underscores, and periods", ErrInvalid)
		}
	}
	upper := strings.ToUpper(value)
	if upper == "TRUE" || upper == "FALSE" || looksLikeCellReference(upper) {
		return "", fmt.Errorf("%w: name conflicts with a cell reference or reserved value", ErrInvalid)
	}
	if formula.IsBuiltInFunction(upper) {
		return "", fmt.Errorf("%w: %s is already a built-in function", ErrInvalid, value)
	}
	return value, nil
}

// NamedFunctionDefinitions 는 저장된 것을 엔진이 읽는 꼴로 바꾼다.
func NamedFunctionDefinitions(items []NamedFunction) map[string]formula.NamedFunction {
	if len(items) == 0 {
		return nil
	}
	definitions := make(map[string]formula.NamedFunction, len(items))
	for _, item := range items {
		definitions[item.Name] = formula.NamedFunction{Parameters: item.Parameters, Body: item.Body}
	}
	return definitions
}
