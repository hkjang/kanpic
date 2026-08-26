package formula

import (
	"fmt"
	"strings"
)

// LET and LAMBDA give a formula its own names. The names have to be known
// while parsing, because otherwise `x` in `LET(x, 5, x*2)` would be looked up
// as a named range and fail before anything is evaluated.
type binding struct {
	name  string
	owner *bindingFrame
	index int
}

// bindingFrame holds the values one LET or one LAMBDA call bound. The values
// live on the frame rather than in the cell map so that nothing has to copy
// the sheet to add three names to it.
type bindingFrame struct{ values []any }

type bindingNode struct {
	owner *bindingFrame
	index int
}

func (n bindingNode) eval(map[string]any) (any, error) {
	if n.index >= len(n.owner.values) {
		return nil, formulaError("#VALUE!", "a name was used before it was given a value")
	}
	return n.owner.values[n.index], nil
}

// letNode evaluates each value in turn, so a later name can be written in
// terms of an earlier one, and then evaluates the calculation.
type letNode struct {
	frame       *bindingFrame
	values      []node
	calculation node
}

func (n letNode) eval(cells map[string]any) (any, error) {
	// The frame is restored afterwards so a LET inside a repeated evaluation
	// never sees the values of an earlier pass.
	saved := n.frame.values
	n.frame.values = make([]any, 0, len(n.values))
	defer func() { n.frame.values = saved }()
	for _, value := range n.values {
		evaluated, err := value.eval(cells)
		if err != nil {
			return nil, err
		}
		n.frame.values = append(n.frame.values, evaluated)
	}
	return n.calculation.eval(cells)
}

// callableValue 는 부를 수 있는 값이다. 수식으로 적은 LAMBDA 와, 이름만
// 적어 넘긴 함수가 둘 다 여기에 든다. MAP 이나 GROUPBY 는 어느 쪽인지
// 가리지 않는다.
type callableValue interface {
	call(cells map[string]any, arguments []any) (any, error)
}

// functionValue 는 이름만 적어 넘긴 함수다. 엑셀은 =MAP(A1:A3,ABS) 처럼
// LAMBDA(v,ABS(v)) 를 줄여 적는 것을 받아 준다. GROUPBY 의 셋째 인수도
// 이 꼴이므로, 이것이 없으면 사람이 쓰는 모양 그대로는 쓸 수 없다.
//
// 값으로 만들어 두기만 하고, 자료로 쓰이면 예전처럼 #NAME? 이 된다.
// =SUM 을 칸에 적는 것은 여전히 이름을 잘못 적은 것이다.
type functionValue struct{ name string }

func (f functionValue) call(cells map[string]any, arguments []any) (any, error) {
	nodes := make([]node, len(arguments))
	for index, argument := range arguments {
		nodes[index] = literalNode{argument}
	}
	return functionNode{name: f.name, arguments: nodes}.eval(cells)
}

// lambdaValue is a function written in a formula. It is a value like any
// other, which is what lets MAP and REDUCE take one as an argument.
type lambdaValue struct {
	frame      *bindingFrame
	parameters []string
	body       node
}

// call binds the arguments and evaluates the body. Missing arguments are left
// omitted rather than zero, so ISOMITTED-style defaults stay possible.
func (l lambdaValue) call(cells map[string]any, arguments []any) (any, error) {
	if len(arguments) > len(l.parameters) {
		return nil, formulaError("#VALUE!", "LAMBDA received more arguments than it names")
	}
	saved := l.frame.values
	values := make([]any, len(l.parameters))
	for index := range values {
		if index < len(arguments) {
			values[index] = arguments[index]
			continue
		}
		values[index] = omittedValue{}
	}
	l.frame.values = values
	defer func() { l.frame.values = saved }()
	return l.body.eval(cells)
}

type lambdaNode struct {
	frame      *bindingFrame
	parameters []string
	body       node
}

func (n lambdaNode) eval(map[string]any) (any, error) {
	return lambdaValue{frame: n.frame, parameters: n.parameters, body: n.body}, nil
}

// lambdaCallNode is `LAMBDA(x, x+1)(4)`: a function written and called on the
// spot, which is how a LAMBDA is tried out before it is given a name.
type lambdaCallNode struct {
	target    node
	arguments []node
}

func (n lambdaCallNode) eval(cells map[string]any) (any, error) {
	target, err := n.target.eval(cells)
	if err != nil {
		return nil, err
	}
	function, ok := target.(lambdaValue)
	if !ok {
		return nil, formulaError("#VALUE!", "only a LAMBDA can be called")
	}
	arguments := make([]any, 0, len(n.arguments))
	for _, argument := range n.arguments {
		value, argumentErr := argument.eval(cells)
		if argumentErr != nil {
			return nil, argumentErr
		}
		arguments = append(arguments, value)
	}
	return function.call(cells, arguments)
}

// bindingName reads one name from the argument list. A name that could be read
// as a cell address is refused, because `LET(A1, 5, A1)` would then mean two
// different things depending on where the formula sits.
func (p *parser) bindingName(owner string) (string, error) {
	current := p.current()
	if current.kind != tokenIdentifier {
		return "", fmt.Errorf("%s expects a name", owner)
	}
	name := strings.ToUpper(strings.ReplaceAll(current.text, "$", ""))
	if isReference(name) {
		return "", formulaError("#VALUE!", owner+" cannot use "+current.text+" as a name because it is a cell reference")
	}
	p.position++
	return name, nil
}

func (p *parser) lookupBinding(name string) (node, bool) {
	for index := len(p.bindings) - 1; index >= 0; index-- {
		if p.bindings[index].name == name {
			return bindingNode{owner: p.bindings[index].owner, index: p.bindings[index].index}, true
		}
	}
	return nil, false
}

// parseLet reads `LET(name, value, …, calculation)`. Each value is parsed with
// the names before it already in scope, which is what makes the steps read in
// the order they are computed.
func (p *parser) parseLet() (node, error) {
	frame := &bindingFrame{}
	values := make([]node, 0, 2)
	depth := len(p.bindings)
	defer func() { p.bindings = p.bindings[:depth] }()
	for {
		name, err := p.bindingName("LET")
		if err != nil {
			return nil, err
		}
		if !p.atSeparator() {
			return nil, fmt.Errorf("LET needs a value for %s", name)
		}
		p.position++
		value, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
		p.bindings = append(p.bindings, binding{name: name, owner: frame, index: len(values) - 1})
		if !p.atSeparator() {
			return nil, fmt.Errorf("LET needs a calculation after the last name")
		}
		p.position++
		if p.startsBindingName() {
			continue
		}
		calculation, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if p.current().kind != tokenRight {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.position++
		return letNode{frame: frame, values: values, calculation: calculation}, nil
	}
}

// parseLambda reads `LAMBDA(parameter, …, body)`. Everything that looks like a
// bare name followed by a separator is a parameter; the rest is the body.
func (p *parser) parseLambda() (node, error) {
	frame := &bindingFrame{}
	parameters := make([]string, 0, 2)
	depth := len(p.bindings)
	defer func() { p.bindings = p.bindings[:depth] }()
	for p.startsBindingName() {
		name, err := p.bindingName("LAMBDA")
		if err != nil {
			return nil, err
		}
		p.bindings = append(p.bindings, binding{name: name, owner: frame, index: len(parameters)})
		parameters = append(parameters, name)
		p.position++
	}
	body, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if p.current().kind != tokenRight {
		return nil, fmt.Errorf("missing closing parenthesis")
	}
	p.position++
	return lambdaNode{frame: frame, parameters: parameters, body: body}, nil
}

func (p *parser) atSeparator() bool {
	kind := p.current().kind
	return kind == tokenComma || kind == tokenSemicolon
}

// startsBindingName reports whether the next argument is a bare name being
// declared rather than the calculation or body that ends the call.
func (p *parser) startsBindingName() bool {
	if p.current().kind != tokenIdentifier {
		return false
	}
	if kind := p.tokens[p.position+1].kind; kind != tokenComma && kind != tokenSemicolon {
		return false
	}
	return !isReference(strings.ToUpper(strings.ReplaceAll(p.current().text, "$", "")))
}

// callable lets a LAMBDA be written and used in one go, as in
// `LAMBDA(x, x+1)(4)`, which is how one is tried out before it is named.
func (p *parser) callable(target node, err error) (node, error) {
	if err != nil || p.current().kind != tokenLeft {
		return target, err
	}
	p.position++
	arguments := make([]node, 0)
	if p.current().kind != tokenRight {
		for {
			argument, argumentErr := p.expression(0)
			if argumentErr != nil {
				return nil, argumentErr
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
	return lambdaCallNode{target: target, arguments: arguments}, nil
}

// lambdaHelpers are the functions that take a LAMBDA and apply it. They are
// evaluated as nodes rather than values because calling the function needs the
// cells the formula was evaluated against.
var lambdaHelpers = map[string]struct{}{
	"MAP": {}, "BYROW": {}, "BYCOL": {}, "REDUCE": {}, "SCAN": {}, "ISOMITTED": {},
}

func evaluateLambdaHelper(name string, arguments []node, cells map[string]any) (any, error) {
	if name == "ISOMITTED" {
		if len(arguments) != 1 {
			return nil, argError(name)
		}
		value, err := arguments[0].eval(cells)
		if err != nil {
			return nil, err
		}
		return omitted(value), nil
	}
	if len(arguments) < 2 {
		return nil, argError(name)
	}
	values := make([]any, 0, len(arguments))
	for _, argument := range arguments {
		value, err := argument.eval(cells)
		if err != nil {
			return nil, err
		}
		values = append(values, value)
	}
	function, ok := values[len(values)-1].(callableValue)
	if !ok {
		return nil, formulaError("#VALUE!", name+" needs a LAMBDA as its last argument")
	}
	switch name {
	case "MAP":
		arrays := make([]arrayValue, 0, len(values)-1)
		for _, value := range values[:len(values)-1] {
			array, err := toArray(value)
			if err != nil {
				return nil, err
			}
			arrays = append(arrays, array)
		}
		for _, array := range arrays[1:] {
			if array.rows != arrays[0].rows || array.columns != arrays[0].columns {
				return nil, formulaError("#VALUE!", "MAP needs arrays of the same shape")
			}
		}
		result := arrayValue{rows: arrays[0].rows, columns: arrays[0].columns, values: make([]any, 0, len(arrays[0].values))}
		for index := range arrays[0].values {
			row := make([]any, 0, len(arrays))
			for _, array := range arrays {
				row = append(row, array.values[index])
			}
			computed, err := function.call(cells, row)
			if err != nil {
				return nil, err
			}
			result.values = append(result.values, computed)
		}
		return oneOrArray(result), nil
	case "BYROW", "BYCOL":
		if len(values) != 2 {
			return nil, argError(name)
		}
		array, err := toArray(values[0])
		if err != nil {
			return nil, err
		}
		count, length := array.rows, array.columns
		if name == "BYCOL" {
			count, length = array.columns, array.rows
		}
		result := arrayValue{rows: count, columns: 1, values: make([]any, 0, count)}
		if name == "BYCOL" {
			result = arrayValue{rows: 1, columns: count, values: make([]any, 0, count)}
		}
		for index := 0; index < count; index++ {
			slice := arrayValue{rows: 1, columns: length, values: make([]any, 0, length)}
			if name == "BYCOL" {
				slice = arrayValue{rows: length, columns: 1, values: make([]any, 0, length)}
			}
			for position := 0; position < length; position++ {
				if name == "BYROW" {
					slice.values = append(slice.values, array.at(index, position))
					continue
				}
				slice.values = append(slice.values, array.at(position, index))
			}
			computed, callErr := function.call(cells, []any{slice})
			if callErr != nil {
				return nil, callErr
			}
			result.values = append(result.values, computed)
		}
		return oneOrArray(result), nil
	case "REDUCE", "SCAN":
		if len(values) != 3 {
			return nil, argError(name)
		}
		array, err := toArray(values[1])
		if err != nil {
			return nil, err
		}
		carried := values[0]
		result := arrayValue{rows: array.rows, columns: array.columns, values: make([]any, 0, len(array.values))}
		for _, value := range array.values {
			carried, err = function.call(cells, []any{carried, value})
			if err != nil {
				return nil, err
			}
			result.values = append(result.values, carried)
		}
		if name == "REDUCE" {
			return carried, nil
		}
		return oneOrArray(result), nil
	}
	return nil, formulaError("#NAME?", "unknown function "+name)
}

// oneOrArray keeps a single result a plain value, so `MAP(A1,LAMBDA(x,x*2))`
// writes one cell rather than spilling a one-cell array.
func oneOrArray(result arrayValue) any {
	if result.rows == 1 && result.columns == 1 && len(result.values) == 1 {
		return result.values[0]
	}
	return result
}
