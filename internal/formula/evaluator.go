package formula

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"kanpic/pkg/cellrange"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type Result struct {
	Value        any      `json:"value,omitempty"`
	Dependencies []string `json:"dependencies"`
	Error        *Error   `json:"error,omitempty"`
}

type Evaluator struct{ scope Scope }

func New() *Evaluator { return &Evaluator{} }

// NewScoped creates a workbook-aware evaluator. sheets maps user-visible sheet
// names to stable identifiers; currentSheet is the stable identifier used for
// unqualified A1 references.
func NewScoped(currentSheet string, sheets map[string]string) *Evaluator {
	return NewScopedWithNames(currentSheet, sheets, nil)
}

// NewScopedWithNames creates a workbook-aware evaluator with workbook-level
// named ranges in addition to visible sheet names.
func NewScopedWithNames(currentSheet string, sheets map[string]string, namedRanges map[string]NamedRange) *Evaluator {
	return &Evaluator{scope: newScope(currentSheet, sheets, namedRanges)}
}

func (e *Evaluator) Dependencies(input string) ([]string, *Error) {
	parser, err := e.newParser(input, e.scope.CurrentSheet)
	if err != nil {
		return []string{}, formulaError("#ERROR!", err.Error())
	}
	_, parseErr := parser.parse()
	dependencies := make([]string, 0, len(parser.dependencies))
	for dependency := range parser.dependencies {
		dependencies = append(dependencies, dependency)
	}
	sort.Strings(dependencies)
	if parseErr != nil {
		if typed, ok := parseErr.(*Error); ok {
			return dependencies, typed
		}
		return dependencies, formulaError("#ERROR!", parseErr.Error())
	}
	return dependencies, nil
}

func (e *Evaluator) Evaluate(input string, cells map[string]any) Result {
	parser, err := e.newParser(input, e.scope.CurrentSheet)
	if err != nil {
		return Result{Dependencies: []string{}, Error: formulaError("#ERROR!", err.Error())}
	}
	root, err := parser.parse()
	if err != nil {
		if typed, ok := err.(*Error); ok {
			return Result{Dependencies: []string{}, Error: typed}
		}
		return Result{Dependencies: []string{}, Error: formulaError("#ERROR!", err.Error())}
	}
	normalized := make(map[string]any, len(cells))
	for address, value := range cells {
		normalized[normalizeAddress(address)] = value
	}
	value, evalErr := root.eval(normalized)
	dependencies := make([]string, 0, len(parser.dependencies))
	for dependency := range parser.dependencies {
		dependencies = append(dependencies, dependency)
	}
	sort.Strings(dependencies)
	if evalErr != nil {
		if typed, ok := evalErr.(*Error); ok {
			return Result{Dependencies: dependencies, Error: typed}
		}
		return Result{Dependencies: dependencies, Error: formulaError("#ERROR!", evalErr.Error())}
	}
	return Result{Value: publicValue(value), Dependencies: dependencies}
}

type tokenKind int

const (
	tokenEOF tokenKind = iota
	tokenNumber
	tokenString
	tokenIdentifier
	tokenOperator
	tokenLeft
	tokenRight
	tokenComma
	tokenColon
	tokenBang
	tokenQuotedIdentifier
	tokenError
)

type token struct {
	kind tokenKind
	text string
}

func lex(input string) ([]token, error) {
	input = strings.TrimSpace(input)
	if strings.HasPrefix(input, "=") {
		input = strings.TrimSpace(input[1:])
	}
	tokens := make([]token, 0)
	for index := 0; index < len(input); {
		character, characterSize := utf8.DecodeRuneInString(input[index:])
		if character == utf8.RuneError && characterSize == 1 {
			return nil, fmt.Errorf("formula is not valid UTF-8")
		}
		if unicode.IsSpace(character) {
			index += characterSize
			continue
		}
		if character == '#' {
			matched := ""
			for _, code := range []string{"#CIRC!", "#DIV/0!", "#ERROR!", "#NAME?", "#NULL!", "#NUM!", "#REF!", "#SPILL!", "#VALUE!", "#N/A"} {
				if strings.HasPrefix(strings.ToUpper(input[index:]), code) && len(code) > len(matched) {
					matched = code
				}
			}
			if matched == "" {
				return nil, fmt.Errorf("unknown formula error literal")
			}
			tokens = append(tokens, token{tokenError, matched})
			index += len(matched)
			continue
		}
		if (character >= '0' && character <= '9') || character == '.' {
			start := index
			dots := 0
			for index < len(input) && ((input[index] >= '0' && input[index] <= '9') || input[index] == '.') {
				if input[index] == '.' {
					dots++
				}
				index++
			}
			if dots > 1 {
				return nil, fmt.Errorf("invalid number %q", input[start:index])
			}
			tokens = append(tokens, token{tokenNumber, input[start:index]})
			continue
		}
		if character == '"' {
			index++
			var builder strings.Builder
			closed := false
			for index < len(input) {
				if input[index] == '"' {
					if index+1 < len(input) && input[index+1] == '"' {
						builder.WriteByte('"')
						index += 2
						continue
					}
					index++
					closed = true
					break
				}
				builder.WriteByte(input[index])
				index++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated string")
			}
			tokens = append(tokens, token{tokenString, builder.String()})
			continue
		}
		if character == '\'' {
			index++
			var builder strings.Builder
			closed := false
			for index < len(input) {
				if input[index] == '\'' {
					if index+1 < len(input) && input[index+1] == '\'' {
						builder.WriteByte('\'')
						index += 2
						continue
					}
					index++
					closed = true
					break
				}
				builder.WriteByte(input[index])
				index++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted sheet name")
			}
			tokens = append(tokens, token{tokenQuotedIdentifier, builder.String()})
			continue
		}
		if unicode.IsLetter(character) || character == '_' || character == '$' {
			start := index
			for index < len(input) {
				current, size := utf8.DecodeRuneInString(input[index:])
				if !unicode.IsLetter(current) && !unicode.IsDigit(current) && current != '_' && current != '.' && current != '$' {
					break
				}
				index += size
			}
			tokens = append(tokens, token{tokenIdentifier, input[start:index]})
			continue
		}
		switch character {
		case '(':
			tokens = append(tokens, token{kind: tokenLeft})
			index++
		case ')':
			tokens = append(tokens, token{kind: tokenRight})
			index++
		case ',', ';':
			tokens = append(tokens, token{kind: tokenComma})
			index++
		case ':':
			tokens = append(tokens, token{kind: tokenColon})
			index++
		case '!':
			tokens = append(tokens, token{kind: tokenBang})
			index++
		case '+', '-', '*', '/', '^', '&', '=':
			tokens = append(tokens, token{tokenOperator, string(character)})
			index++
		case '<', '>':
			operator := string(character)
			if index+1 < len(input) && (input[index+1] == '=' || input[index+1] == '>') {
				operator += string(input[index+1])
				index++
			}
			tokens = append(tokens, token{tokenOperator, operator})
			index++
		default:
			return nil, fmt.Errorf("unexpected character %q", character)
		}
	}
	tokens = append(tokens, token{kind: tokenEOF})
	return tokens, nil
}

type node interface {
	eval(map[string]any) (any, error)
}
type literalNode struct{ value any }

func (n literalNode) eval(_ map[string]any) (any, error) { return n.value, nil }

type errorNode struct{ value *Error }

func (n errorNode) eval(_ map[string]any) (any, error) { return nil, n.value }

type referenceNode struct{ address string }

func (n referenceNode) eval(cells map[string]any) (any, error) {
	value, ok := cells[n.address]
	if !ok {
		return nil, nil
	}
	if formulaErr, ok := value.(*Error); ok {
		return nil, formulaErr
	}
	return value, nil
}

type rangeNode struct {
	rows, columns int
	addresses     []string
}

func (n rangeNode) eval(cells map[string]any) (any, error) {
	count := n.rows * n.columns
	if count > 100_000 {
		return nil, formulaError("#VALUE!", "range is too large")
	}
	values := make([]any, 0, count)
	for _, address := range n.addresses {
		value := cells[address]
		if formulaErr, ok := value.(*Error); ok {
			return nil, formulaErr
		}
		values = append(values, value)
	}
	return arrayValue{
		rows:    n.rows,
		columns: n.columns,
		values:  values,
	}, nil
}

type unaryNode struct {
	operator string
	value    node
}

func (n unaryNode) eval(cells map[string]any) (any, error) {
	value, err := n.value.eval(cells)
	if err != nil {
		return nil, err
	}
	return evaluateUnary(n.operator, value)
}

type binaryNode struct {
	operator    string
	left, right node
}

func (n binaryNode) eval(cells map[string]any) (any, error) {
	left, err := n.left.eval(cells)
	if err != nil {
		return nil, err
	}
	right, err := n.right.eval(cells)
	if err != nil {
		return nil, err
	}
	return evaluateBinary(n.operator, left, right)
}

type functionNode struct {
	name      string
	arguments []node
}

func (n functionNode) eval(cells map[string]any) (any, error) {
	name := strings.ToUpper(n.name)
	if name == "IF" {
		if len(n.arguments) < 2 || len(n.arguments) > 3 {
			return nil, argError(name)
		}
		condition, err := n.arguments[0].eval(cells)
		if err != nil {
			return nil, err
		}
		if truthy(condition) {
			return n.arguments[1].eval(cells)
		}
		if len(n.arguments) == 3 {
			return n.arguments[2].eval(cells)
		}
		return false, nil
	}
	if name == "IFERROR" {
		if len(n.arguments) != 2 {
			return nil, argError(name)
		}
		value, err := n.arguments[0].eval(cells)
		if err != nil {
			return n.arguments[1].eval(cells)
		}
		return value, nil
	}
	if name == "AND" || name == "OR" {
		if len(n.arguments) == 0 {
			return nil, argError(name)
		}
		expected := name == "AND"
		for _, argument := range n.arguments {
			value, err := argument.eval(cells)
			if err != nil {
				return nil, err
			}
			for _, item := range flatten(value) {
				result := truthy(item)
				if name == "AND" && !result {
					return false, nil
				}
				if name == "OR" && result {
					return true, nil
				}
			}
		}
		return expected, nil
	}
	evaluated := make([]any, 0, len(n.arguments))
	for _, argument := range n.arguments {
		value, err := argument.eval(cells)
		if err != nil {
			return nil, err
		}
		evaluated = append(evaluated, value)
	}
	switch name {
	case "SUMIF", "SUMIFS", "COUNTIF", "COUNTIFS":
		return evaluateConditionalAggregate(name, evaluated)
	case "VLOOKUP", "HLOOKUP", "INDEX", "MATCH":
		return evaluateLookup(name, evaluated)
	case "FILTER":
		return evaluateFilter(evaluated)
	case "SORT":
		return evaluateSort(evaluated)
	}
	values := make([]any, 0)
	for _, value := range evaluated {
		values = append(values, flatten(value)...)
	}
	switch name {
	case "SUM", "AVERAGE", "MIN", "MAX":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			return float64(0), nil
		}
		result := numbers[0]
		if name == "SUM" || name == "AVERAGE" {
			result = 0
			for _, number := range numbers {
				result += number
			}
			if name == "AVERAGE" {
				result /= float64(len(numbers))
			}
		} else {
			for _, number := range numbers[1:] {
				if name == "MIN" && number < result {
					result = number
				}
				if name == "MAX" && number > result {
					result = number
				}
			}
		}
		return result, nil
	case "COUNT":
		return float64(len(numericValues(values))), nil
	case "ROUND":
		if len(values) < 1 || len(values) > 2 {
			return nil, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, formulaError("#VALUE!", "ROUND requires a number")
		}
		digits := 0.0
		if len(values) == 2 {
			digits, _ = toNumber(values[1])
		}
		factor := math.Pow(10, digits)
		return math.Round(number*factor) / factor, nil
	case "CONCAT":
		var builder strings.Builder
		for _, value := range values {
			builder.WriteString(display(value))
		}
		return builder.String(), nil
	case "LEFT", "RIGHT":
		if len(values) < 1 || len(values) > 2 {
			return nil, argError(name)
		}
		text := []rune(display(values[0]))
		count := 1
		if len(values) == 2 {
			number, ok := toNumber(values[1])
			if !ok || number < 0 {
				return nil, formulaError("#VALUE!", "invalid character count")
			}
			count = int(number)
		}
		if count > len(text) {
			count = len(text)
		}
		if name == "LEFT" {
			return string(text[:count]), nil
		}
		return string(text[len(text)-count:]), nil
	case "MID":
		if len(values) != 3 {
			return nil, argError(name)
		}
		text := []rune(display(values[0]))
		start, ok1 := toNumber(values[1])
		count, ok2 := toNumber(values[2])
		if !ok1 || !ok2 || start < 1 || count < 0 {
			return nil, formulaError("#VALUE!", "invalid MID position")
		}
		from := min(int(start)-1, len(text))
		to := min(from+int(count), len(text))
		return string(text[from:to]), nil
	case "DATE":
		if len(values) != 3 {
			return nil, argError(name)
		}
		year, y := toNumber(values[0])
		month, m := toNumber(values[1])
		day, d := toNumber(values[2])
		if !y || !m || !d {
			return nil, formulaError("#VALUE!", "DATE requires numbers")
		}
		return time.Date(int(year), time.Month(int(month)), int(day), 0, 0, 0, 0, time.UTC).Format("2006-01-02"), nil
	}
	if result, handled, err := evaluateLibrary(name, values); handled {
		return result, err
	}
	return nil, formulaError("#NAME?", "unknown function "+name)
}

type parser struct {
	tokens       []token
	position     int
	dependencies map[string]struct{}
	scope        Scope
}

func (e *Evaluator) newParser(input, currentSheet string) (*parser, error) {
	tokens, err := lex(input)
	if err != nil {
		return nil, err
	}
	scope := e.scope
	if currentSheet != "" {
		scope.CurrentSheet = strings.ToUpper(strings.TrimSpace(currentSheet))
	}
	return &parser{tokens: tokens, dependencies: make(map[string]struct{}), scope: scope}, nil
}
func (p *parser) parse() (node, error) {
	result, err := p.expression(0)
	if err != nil {
		return nil, err
	}
	if p.current().kind != tokenEOF {
		return nil, fmt.Errorf("unexpected token %q", p.current().text)
	}
	return result, nil
}
func (p *parser) expression(minimum int) (node, error) {
	left, err := p.prefix()
	if err != nil {
		return nil, err
	}
	for p.current().kind == tokenOperator {
		operator := p.current().text
		precedence := operatorPrecedence(operator)
		if precedence < minimum {
			break
		}
		p.position++
		next := precedence + 1
		if operator == "^" {
			next = precedence
		}
		right, err := p.expression(next)
		if err != nil {
			return nil, err
		}
		left = binaryNode{operator, left, right}
	}
	return left, nil
}
func (p *parser) prefix() (node, error) {
	current := p.current()
	if current.kind == tokenOperator && (current.text == "+" || current.text == "-") {
		p.position++
		value, err := p.prefix()
		return unaryNode{current.text, value}, err
	}
	return p.primary()
}
func (p *parser) primary() (node, error) {
	current := p.current()
	p.position++
	switch current.kind {
	case tokenNumber:
		value, err := strconv.ParseFloat(current.text, 64)
		return literalNode{value}, err
	case tokenString:
		return literalNode{current.text}, nil
	case tokenError:
		return errorNode{formulaError(current.text, "formula contains "+current.text)}, nil
	case tokenLeft:
		value, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		if p.current().kind != tokenRight {
			return nil, fmt.Errorf("missing closing parenthesis")
		}
		p.position++
		return value, nil
	case tokenIdentifier, tokenQuotedIdentifier:
		if current.kind == tokenQuotedIdentifier && p.current().kind != tokenBang {
			return nil, fmt.Errorf("quoted name %q must qualify a cell reference", current.text)
		}
		if p.current().kind == tokenBang {
			p.position++
			start := p.current()
			if start.kind != tokenIdentifier {
				return nil, fmt.Errorf("sheet qualifier must be followed by a cell reference")
			}
			p.position++
			return p.cellReference(current.text, start.text)
		}
		name := strings.ToUpper(strings.ReplaceAll(current.text, "$", ""))
		if p.current().kind == tokenLeft {
			p.position++
			arguments := make([]node, 0)
			if p.current().kind != tokenRight {
				for {
					argument, err := p.expression(0)
					if err != nil {
						return nil, err
					}
					arguments = append(arguments, argument)
					if p.current().kind != tokenComma {
						break
					}
					p.position++
				}
			}
			if p.current().kind != tokenRight {
				return nil, fmt.Errorf("missing closing parenthesis")
			}
			p.position++
			return functionNode{name, arguments}, nil
		}
		if name == "TRUE" {
			return literalNode{true}, nil
		}
		if name == "FALSE" {
			return literalNode{false}, nil
		}
		if !isReference(name) {
			return p.namedRange(current.text)
		}
		return p.cellReference("", current.text)
	}
	return nil, fmt.Errorf("unexpected token %q", current.text)
}

func (p *parser) namedRange(name string) (node, error) {
	target, err := p.scope.resolveNamedRange(name)
	if err != nil {
		return nil, err
	}
	selected, parseErr := cellrange.Parse(target.Range)
	if parseErr != nil {
		return nil, formulaError("#REF!", "named range "+name+" has an invalid target")
	}
	count := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
	if count > 100_000 {
		return nil, formulaError("#VALUE!", "range is too large")
	}
	addresses := make([]string, 0, count)
	for row := selected.Start.Row; row <= selected.End.Row; row++ {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			key := CellKey(target.SheetID, cellrange.Address(row, column))
			p.dependencies[key] = struct{}{}
			addresses = append(addresses, key)
		}
	}
	if len(addresses) == 1 {
		return referenceNode{address: addresses[0]}, nil
	}
	return rangeNode{rows: selected.End.Row - selected.Start.Row + 1, columns: selected.End.Column - selected.Start.Column + 1, addresses: addresses}, nil
}

func (p *parser) cellReference(qualifier, startText string) (node, error) {
	start := normalizeCellAddress(startText)
	if !isReference(start) {
		return nil, fmt.Errorf("%q is not a cell reference", startText)
	}
	startKey, err := p.scope.resolveCell(qualifier, start)
	if err != nil {
		return nil, err
	}
	if p.current().kind != tokenColon {
		p.dependencies[startKey] = struct{}{}
		return referenceNode{startKey}, nil
	}
	p.position++
	end := p.current()
	if end.kind != tokenIdentifier {
		return nil, fmt.Errorf("range end must be a cell reference")
	}
	p.position++
	endAddress := normalizeCellAddress(end.text)
	selected, parseErr := cellrange.Parse(start + ":" + endAddress)
	if parseErr != nil {
		return nil, parseErr
	}
	count := int64(selected.End.Row-selected.Start.Row+1) * int64(selected.End.Column-selected.Start.Column+1)
	if count > 100_000 {
		return nil, formulaError("#VALUE!", "range is too large")
	}
	addresses := make([]string, 0, count)
	for row := selected.Start.Row; row <= selected.End.Row; row++ {
		for column := selected.Start.Column; column <= selected.End.Column; column++ {
			key, resolveErr := p.scope.resolveCell(qualifier, cellrange.Address(row, column))
			if resolveErr != nil {
				return nil, resolveErr
			}
			p.dependencies[key] = struct{}{}
			addresses = append(addresses, key)
		}
	}
	return rangeNode{rows: selected.End.Row - selected.Start.Row + 1, columns: selected.End.Column - selected.Start.Column + 1, addresses: addresses}, nil
}
func (p *parser) current() token { return p.tokens[p.position] }
func operatorPrecedence(operator string) int {
	switch operator {
	case "=", "<>", "<", ">", "<=", ">=":
		return 1
	case "&":
		return 2
	case "+", "-":
		return 3
	case "*", "/":
		return 4
	case "^":
		return 5
	}
	return -1
}

func isReference(value string) bool {
	index := 0
	column := 0
	for index < len(value) && value[index] >= 'A' && value[index] <= 'Z' {
		column = column*26 + int(value[index]-'A'+1)
		index++
	}
	if index == 0 || index == len(value) || column > 16_384 {
		return false
	}
	row, err := strconv.Atoi(value[index:])
	return err == nil && row > 0 && row <= 1_048_576
}
func toNumber(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		number, err := typed.Float64()
		return number, err == nil
	case nil:
		return 0, true
	case bool:
		if typed {
			return 1, true
		}
		return 0, true
	case string:
		number, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return number, err == nil
	}
	return 0, false
}
func numericValues(values []any) []float64 {
	result := make([]float64, 0)
	for _, value := range values {
		if value == nil {
			continue
		}
		if number, ok := toNumber(value); ok {
			result = append(result, number)
		}
	}
	return result
}
func flatten(value any) []any {
	switch typed := value.(type) {
	case arrayValue:
		return append([]any(nil), typed.values...)
	case []any:
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			result = append(result, flatten(item)...)
		}
		return result
	case [][]any:
		result := make([]any, 0)
		for _, row := range typed {
			result = append(result, row...)
		}
		return result
	}
	return []any{value}
}

func publicValue(value any) any {
	switch typed := value.(type) {
	case arrayValue:
		return typed.matrix()
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = publicValue(item)
		}
		return result
	default:
		return value
	}
}
func truthy(value any) bool {
	if value == nil {
		return false
	}
	if boolean, ok := value.(bool); ok {
		return boolean
	}
	if number, ok := toNumber(value); ok {
		return number != 0
	}
	return display(value) != ""
}
func display(value any) string {
	if value == nil {
		return ""
	}
	if number, ok := value.(float64); ok {
		return strconv.FormatFloat(number, 'f', -1, 64)
	}
	return fmt.Sprint(value)
}
func compare(a, b any) int {
	left, leftOK := toNumber(a)
	right, rightOK := toNumber(b)
	if leftOK && rightOK {
		if left < right {
			return -1
		}
		if left > right {
			return 1
		}
		return 0
	}
	return strings.Compare(strings.ToLower(display(a)), strings.ToLower(display(b)))
}
func formulaError(code, message string) *Error { return &Error{Code: code, Message: message} }
func argError(name string) *Error {
	return formulaError("#VALUE!", name+" received an invalid number of arguments")
}
