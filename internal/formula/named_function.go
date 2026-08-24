package formula

import (
	"fmt"
	"strings"
)

// NamedFunction 은 워크북에 저장해 두고 이름으로 부르는 수식이다. 팀에서
// 쓰는 셈을 한 번 정의해 두면 `=마진율(매출, 원가)` 처럼 쓸 수 있다.
//
// 안쪽은 LAMBDA 와 같다. 매개변수 이름을 묶고 본문을 그 아래에서 푼다 —
// 셈하는 규칙을 새로 만들지 않는 이유는, 두 벌이 되면 한쪽만 고쳐지기
// 때문이다.
type NamedFunction struct {
	Parameters []string
	Body       string
}

// MaxNamedFunctionDepth 는 이름 있는 함수가 서로를 부르며 파고들 수 있는
// 깊이다. 자기 자신을 부르면 파싱이 끝나지 않으므로 반드시 막아야 한다.
const MaxNamedFunctionDepth = 16

// namedFunctionCall 은 `이름(인수…)` 을 LAMBDA 를 만들어 부르는 것으로 푼다.
// 여는 괄호는 이미 지나온 상태로 들어온다.
func (p *parser) namedFunctionCall(name string, definition NamedFunction) (node, error) {
	if p.namedDepth >= MaxNamedFunctionDepth {
		return nil, formulaError("#VALUE!", "named function "+name+" calls itself too deeply")
	}
	arguments := make([]node, 0, len(definition.Parameters))
	if p.current().kind != tokenRight {
		for {
			argument, err := p.expression(0)
			if err != nil {
				return nil, err
			}
			arguments = append(arguments, argument)
			if !p.atSeparator() {
				break
			}
			p.position++
		}
	}
	if p.current().kind != tokenRight {
		return nil, fmt.Errorf("missing closing parenthesis")
	}
	p.position++
	if len(arguments) != len(definition.Parameters) {
		return nil, formulaError("#N/A", fmt.Sprintf("%s takes %d argument(s), not %d", name, len(definition.Parameters), len(arguments)))
	}
	body, err := p.parseNamedFunctionBody(name, definition)
	if err != nil {
		return nil, err
	}
	return lambdaCallNode{target: body, arguments: arguments}, nil
}

// parseNamedFunctionBody 는 저장해 둔 본문을 매개변수가 묶인 채로 푼다.
// 셀 참조가 들어 있으면 그 셀도 이 수식이 기대는 곳이 되므로, 의존성은
// 부르는 쪽 것과 같은 자루에 담는다.
func (p *parser) parseNamedFunctionBody(name string, definition NamedFunction) (node, error) {
	text := strings.TrimSpace(definition.Body)
	text = strings.TrimPrefix(text, "=")
	if text == "" {
		return nil, formulaError("#VALUE!", "named function "+name+" has no formula")
	}
	tokens, err := lex(text)
	if err != nil {
		return nil, formulaError("#VALUE!", "named function "+name+" is not a formula: "+err.Error())
	}
	frame := &bindingFrame{}
	inner := &parser{tokens: tokens, dependencies: p.dependencies, scope: p.scope, namedDepth: p.namedDepth + 1}
	for index, parameter := range definition.Parameters {
		inner.bindings = append(inner.bindings, binding{name: normalizeSheetName(parameter), owner: frame, index: index})
	}
	parsed, err := inner.expression(0)
	if err != nil {
		// 안쪽에서 이미 셈 오류로 말했으면 그대로 올린다. 겹겹이 싸면
		// 자기 자신을 부르는 정의에서 같은 문장이 열여섯 번 쌓인다.
		if _, wrapped := err.(*Error); wrapped {
			return nil, err
		}
		return nil, formulaError("#VALUE!", "named function "+name+" is not a formula: "+err.Error())
	}
	if inner.current().kind != tokenEOF {
		return nil, formulaError("#VALUE!", "named function "+name+" has leftover text after its formula")
	}
	return lambdaNode{frame: frame, parameters: definition.Parameters, body: parsed}, nil
}

// IsBuiltInFunction 은 엔진이 이미 아는 이름인지 본다. 사람이 그 이름으로
// 자기 수식을 저장하면 그 워크북의 모든 SUM 이 뜻을 잃는다.
func IsBuiltInFunction(name string) bool {
	name = strings.ToUpper(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	// 인수 없이 불러 #NAME? 이 나면 모르는 이름이다. 인수 개수가 틀렸다는
	// 오류는 이름이 있다는 뜻이므로 이미 있는 것으로 본다.
	result := New().Evaluate("="+name+"()", map[string]any{})
	return result.Error == nil || result.Error.Code != "#NAME?"
}
