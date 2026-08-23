package formula

import (
	"encoding/json"
	"fmt"
	"math"
	"math/big"
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

// WithImports supplies the cross-workbook blocks IMPORTRANGE calls asked for.
// They are fetched and permission-checked before evaluation, so the evaluator
// itself stays a pure function of what it was handed.
func (e *Evaluator) WithImports(imports map[string]ImportedRange) *Evaluator {
	e.scope.Imports = imports
	return e
}

func (e *Evaluator) Dependencies(input string) ([]string, *Error) {
	parser, err := e.newParser(input, e.scope.CurrentSheet, "")
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
	scoped := *e
	scoped.scope.Extent = measureExtent(keysOf(cells))
	e = &scoped
	parser, err := e.newParser(input, e.scope.CurrentSheet, "")
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
	value, evalErr := storableResult(root.eval(normalized))
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

// numericDigits is how many significant decimal digits a result keeps. Excel
// and Google Sheets both use fifteen, which is the most a float64 represents
// without the binary remainder showing through.
const numericDigits = 15

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
	tokenSemicolon
	tokenArrayOpen
	tokenArrayClose
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
			for _, code := range ErrorCodes {
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
			// 지수 표기(1E-10, 2E3). 값으로 칠 때는 이미 받아들이면서 수식
			// 안에서는 "unexpected token E" 였다. E 뒤에 숫자가 오지 않으면
			// 셀 참조(E3 같은)이므로 숫자에 삼키지 않는다.
			if index < len(input) && (input[index] == 'e' || input[index] == 'E') {
				exponent := index + 1
				if exponent < len(input) && (input[exponent] == '+' || input[exponent] == '-') {
					exponent++
				}
				digits := exponent
				for digits < len(input) && input[digits] >= '0' && input[digits] <= '9' {
					digits++
				}
				if digits > exponent {
					index = digits
				}
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
		case ',':
			tokens = append(tokens, token{kind: tokenComma})
			index++
		case ';':
			tokens = append(tokens, token{kind: tokenSemicolon})
			index++
		case '{':
			tokens = append(tokens, token{kind: tokenArrayOpen})
			index++
		case '}':
			tokens = append(tokens, token{kind: tokenArrayClose})
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
	if _, helper := lambdaHelpers[name]; helper {
		return evaluateLambdaHelper(name, n.arguments, cells)
	}
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
	if result, handled, err := n.evaluateLazy(name, cells); handled {
		return result, err
	}
	evaluated := make([]any, 0, len(n.arguments))
	for _, argument := range n.arguments {
		value, err := argument.eval(cells)
		if err != nil {
			return nil, err
		}
		evaluated = append(evaluated, value)
	}
	if result, handled, err := evaluateConditionalExtra(name, evaluated); handled {
		return result, err
	}
	if result, handled, err := evaluateArray(name, evaluated); handled {
		return result, err
	}
	if result, handled, err := evaluateCashflow(name, evaluated); handled {
		return result, err
	}
	if result, handled, err := evaluatePairedStatistics(name, evaluated); handled {
		return result, err
	}
	if result, handled, err := evaluateTextArray(name, evaluated); handled {
		return result, err
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
		// A skipped argument keeps its slot here. Dropping it would slide
		// every later argument one position left, so `FIXED(1234.5,,TRUE)`
		// would read TRUE as the number of decimals instead of the flag.
		if omitted(value) {
			values = append(values, omittedValue{})
			continue
		}
		values = append(values, flatten(value)...)
	}
	switch name {
	case "SUM", "AVERAGE", "MIN", "MAX":
		numbers := numericValues(values)
		if len(numbers) == 0 {
			// Dates are stored as text here, so the earliest and latest date in
			// a column would otherwise come back as zero.
			if name == "MIN" || name == "MAX" {
				if moment, ok := extremeDate(values, name == "MIN"); ok {
					return moment, nil
				}
			}
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
		if len(values) == 2 && !omitted(values[1]) {
			digits, _ = toNumber(values[1])
		}
		return decimalRound(number, int(digits), roundHalfAway), nil
	// CONCATENATE 는 CONCAT 의 옛 이름이다. 엑셀 강의와 오래된 통합 문서가
	// 아직 이 이름을 쓰므로 둘 다 알아듣는다.
	case "CONCAT", "CONCATENATE":
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
		if len(values) == 2 && !omitted(values[1]) {
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
	for _, group := range []func(string, []any) (any, bool, error){
		evaluateMath, evaluateStatistics, evaluateFinance, evaluateText, evaluateDate, evaluateInformation,
	} {
		if result, handled, err := group(name, values); handled {
			return result, err
		}
	}
	return nil, formulaError("#NAME?", "unknown function "+name)
}

// evaluateLazy handles the functions that must decide whether to evaluate an
// argument at all: the conditionals, and the error tests that only mean
// something when a failing argument is caught rather than propagated.
func (n functionNode) evaluateLazy(name string, cells map[string]any) (any, bool, error) {
	switch name {
	case "IFS":
		if len(n.arguments) < 2 || len(n.arguments)%2 != 0 {
			return nil, true, argError(name)
		}
		for index := 0; index < len(n.arguments); index += 2 {
			condition, err := n.arguments[index].eval(cells)
			if err != nil {
				return nil, true, err
			}
			if truthy(condition) {
				value, evalErr := n.arguments[index+1].eval(cells)
				return value, true, evalErr
			}
		}
		return nil, true, formulaError("#N/A", "IFS found no true condition")
	case "SWITCH":
		if len(n.arguments) < 3 {
			return nil, true, argError(name)
		}
		subject, err := n.arguments[0].eval(cells)
		if err != nil {
			return nil, true, err
		}
		index := 1
		for ; index+1 < len(n.arguments); index += 2 {
			candidate, caseErr := n.arguments[index].eval(cells)
			if caseErr != nil {
				return nil, true, caseErr
			}
			if compare(subject, candidate) == 0 {
				value, evalErr := n.arguments[index+1].eval(cells)
				return value, true, evalErr
			}
		}
		if index < len(n.arguments) {
			value, evalErr := n.arguments[index].eval(cells)
			return value, true, evalErr
		}
		return nil, true, formulaError("#N/A", "SWITCH found no matching case")
	case "IFNA":
		if len(n.arguments) != 2 {
			return nil, true, argError(name)
		}
		value, err := n.arguments[0].eval(cells)
		if typed, ok := err.(*Error); ok && typed.Code == "#N/A" {
			fallback, fallbackErr := n.arguments[1].eval(cells)
			return fallback, true, fallbackErr
		}
		return value, true, err
	case "ISERROR", "ISERR", "ISNA", "ISFORMULA":
		if len(n.arguments) != 1 {
			return nil, true, argError(name)
		}
		if name == "ISFORMULA" {
			// The evaluator sees values, not the formulas behind them.
			return nil, true, formulaError("#N/A", "ISFORMULA is not supported")
		}
		value, err := n.arguments[0].eval(cells)
		typed, isFormulaError := err.(*Error)
		if !isFormulaError {
			if err != nil {
				return true, true, nil
			}
			// A stored error code reaches here as ordinary text.
			if code, ok := value.(string); ok && isFormulaErrorCode(code) {
				return name != "ISNA" || code == "#N/A", true, nil
			}
			return false, true, nil
		}
		switch name {
		case "ISNA":
			return typed.Code == "#N/A", true, nil
		case "ISERR":
			return typed.Code != "#N/A", true, nil
		}
		return true, true, nil
	}
	return nil, false, nil
}

type parser struct {
	tokens       []token
	position     int
	dependencies map[string]struct{}
	scope        Scope
	// bindings are the names LET and LAMBDA introduced around the point
	// being parsed, innermost last so an inner name shadows an outer one.
	bindings []binding
}

// measureExtent finds how far each sheet's content reaches so unbounded
// references such as A:A cover the rows in use rather than the whole grid.
func measureExtent(addresses ...[]string) map[string]SheetExtent {
	extent := make(map[string]SheetExtent)
	for _, group := range addresses {
		for _, address := range group {
			sheetID, cell, valid := SplitCellKey(address)
			if !valid {
				continue
			}
			selected, err := cellrange.Parse(cell)
			if err != nil {
				continue
			}
			current := extent[sheetID]
			if selected.End.Row > current.Rows {
				current.Rows = selected.End.Row
			}
			if selected.End.Column > current.Columns {
				current.Columns = selected.End.Column
			}
			extent[sheetID] = current
		}
	}
	return extent
}

func keysOf(values map[string]any) []string {
	result := make([]string, 0, len(values))
	for key := range values {
		result = append(result, key)
	}
	return result
}

func (e *Evaluator) newParser(input, currentSheet, anchor string) (*parser, error) {
	tokens, err := lex(input)
	if err != nil {
		return nil, err
	}
	scope := e.scope
	if currentSheet != "" {
		scope.CurrentSheet = strings.ToUpper(strings.TrimSpace(currentSheet))
	}
	scope.Anchor = anchor
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
		// 2:5 names whole rows; anywhere else a number is just a number.
		if p.current().kind == tokenColon && p.tokens[p.position+1].kind == tokenNumber {
			if row, err := strconv.Atoi(current.text); err == nil && row >= 1 && row <= MaxRows {
				return p.rowBand("", row)
			}
		}
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
	case tokenArrayOpen:
		return p.arrayLiteral()
	case tokenIdentifier, tokenQuotedIdentifier:
		if current.kind == tokenQuotedIdentifier && p.current().kind != tokenBang {
			return nil, fmt.Errorf("quoted name %q must qualify a cell reference", current.text)
		}
		if p.current().kind == tokenBang {
			p.position++
			start := p.current()
			if start.kind == tokenNumber && p.tokens[p.position+1].kind == tokenColon {
				row, err := strconv.Atoi(start.text)
				if err != nil || row < 1 || row > MaxRows {
					return nil, fmt.Errorf("%q is not a row number", start.text)
				}
				p.position++
				return p.rowBand(current.text, row)
			}
			if start.kind != tokenIdentifier {
				return nil, fmt.Errorf("sheet qualifier must be followed by a cell reference")
			}
			p.position++
			return p.cellReference(current.text, start.text)
		}
		name := strings.ToUpper(strings.ReplaceAll(current.text, "$", ""))
		if p.current().kind == tokenLeft {
			p.position++
			switch name {
			case "LET":
				return p.parseLet()
			case "LAMBDA":
				return p.callable(p.parseLambda())
			}
			// A name bound to a LAMBDA is called like any other function, so
			// `LET(double, LAMBDA(x, x*2), double(21))` reads naturally.
			if bound, found := p.lookupBinding(name); found {
				p.position--
				return p.callable(bound, nil)
			}
			arguments := make([]node, 0)
			if p.current().kind != tokenRight {
				for {
					// `SEQUENCE(3,,5)` skips an argument to reach a later one.
					// Excel and Sheets both allow it, so an empty slot parses
					// as an explicit omission rather than a syntax error.
					if kind := p.current().kind; kind == tokenComma || kind == tokenSemicolon || kind == tokenRight {
						arguments = append(arguments, literalNode{omittedValue{}})
						if kind == tokenRight {
							break
						}
						p.position++
						continue
					}
					argument, err := p.expression(0)
					if err != nil {
						return nil, err
					}
					arguments = append(arguments, argument)
					// A semicolon separates arguments too, which some locales
					// type and older kanpic formulas already contain.
					if p.current().kind != tokenComma && p.current().kind != tokenSemicolon {
						break
					}
					p.position++
				}
			}
			if p.current().kind != tokenRight {
				return nil, fmt.Errorf("missing closing parenthesis")
			}
			p.position++
			if resolved, handled, err := p.referenceFunction(name, arguments); handled {
				return resolved, err
			}
			if name == "TRUE" || name == "FALSE" {
				return literalNode{name == "TRUE"}, nil
			}
			return functionNode{name, arguments}, nil
		}
		if name == "TRUE" {
			return literalNode{true}, nil
		}
		if name == "FALSE" {
			return literalNode{false}, nil
		}
		if _, columnOnly := columnOnlyReference(name); columnOnly && p.current().kind == tokenColon {
			return p.cellReference("", current.text)
		}
		if bound, found := p.lookupBinding(name); found {
			return bound, nil
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

// cellReference parses everything that starts with an A1-style token: a single
// cell, an ordinary range, and the unbounded forms Google Sheets users rely on
// to keep a formula correct as a table grows — A:A, A:C and A2:A.
func (p *parser) cellReference(qualifier, startText string) (node, error) {
	start := normalizeCellAddress(startText)
	startColumn, startIsColumn := columnOnlyReference(start)
	if !startIsColumn && !isReference(start) {
		return nil, fmt.Errorf("%q is not a cell reference", startText)
	}
	if p.current().kind != tokenColon {
		if startIsColumn {
			return nil, fmt.Errorf("%q is not a cell reference", startText)
		}
		startKey, err := p.scope.resolveCell(qualifier, start)
		if err != nil {
			return nil, err
		}
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
	endColumn, endIsColumn := columnOnlyReference(endAddress)
	if !endIsColumn && !isReference(endAddress) {
		return nil, fmt.Errorf("%q is not a cell reference", end.text)
	}
	if !startIsColumn && !endIsColumn {
		selected, parseErr := cellrange.Parse(start + ":" + endAddress)
		if parseErr != nil {
			return nil, parseErr
		}
		return p.buildRange(qualifier, selected.Start.Row, selected.Start.Column, selected.End.Row, selected.End.Column)
	}
	// At least one side names a column only, so the range runs to the end of
	// the sheet's content on the row axis.
	firstColumn, lastColumn := startColumn, endColumn
	firstRow, lastRow := 1, p.extent(qualifier).Rows
	if !startIsColumn {
		selected, parseErr := cellrange.Parse(start)
		if parseErr != nil {
			return nil, parseErr
		}
		firstColumn, firstRow = selected.Start.Column, selected.Start.Row
	}
	if !endIsColumn {
		selected, parseErr := cellrange.Parse(endAddress)
		if parseErr != nil {
			return nil, parseErr
		}
		lastColumn, lastRow = selected.End.Column, selected.End.Row
	}
	if lastRow < firstRow {
		lastRow = firstRow
	}
	if lastColumn < firstColumn {
		firstColumn, lastColumn = lastColumn, firstColumn
	}
	return p.buildRange(qualifier, firstRow, firstColumn, lastRow, lastColumn)
}

// rowBand parses the 2:5 form, which spans every used column of the sheet.
func (p *parser) rowBand(qualifier string, firstRow int) (node, error) {
	p.position++
	end := p.current()
	if end.kind != tokenNumber {
		return nil, fmt.Errorf("row range end must be a row number")
	}
	p.position++
	lastRow, err := strconv.Atoi(end.text)
	if err != nil || lastRow < 1 || lastRow > MaxRows {
		return nil, fmt.Errorf("%q is not a row number", end.text)
	}
	if lastRow < firstRow {
		firstRow, lastRow = lastRow, firstRow
	}
	return p.buildRange(qualifier, firstRow, 1, lastRow, p.extent(qualifier).Columns)
}

func (p *parser) extent(qualifier string) SheetExtent {
	sheetID, err := p.sheetFor(qualifier)
	if err != nil {
		return SheetExtent{Rows: 1, Columns: 1}
	}
	return p.scope.extentOf(sheetID)
}

// sheetFor resolves a formula's sheet qualifier to the stable identifier the
// cell keys are built from.
func (p *parser) sheetFor(qualifier string) (string, error) {
	if qualifier == "" {
		return p.scope.CurrentSheet, nil
	}
	if p.scope.Sheets == nil {
		return normalizeSheetName(qualifier), nil
	}
	sheetID, found := p.scope.Sheets[normalizeSheetName(qualifier)]
	if !found {
		return "", formulaError("#REF!", "unknown sheet "+qualifier)
	}
	return sheetID, nil
}

func (p *parser) buildRange(qualifier string, firstRow, firstColumn, lastRow, lastColumn int) (node, error) {
	sheetID, err := p.sheetFor(qualifier)
	if err != nil {
		return nil, err
	}
	return p.buildRangeAt(sheetID, firstRow, firstColumn, lastRow, lastColumn)
}

func (p *parser) buildRangeAt(sheetID string, firstRow, firstColumn, lastRow, lastColumn int) (node, error) {
	count := int64(lastRow-firstRow+1) * int64(lastColumn-firstColumn+1)
	if count > 100_000 {
		return nil, formulaError("#VALUE!", "range is too large")
	}
	addresses := make([]string, 0, count)
	for row := firstRow; row <= lastRow; row++ {
		for column := firstColumn; column <= lastColumn; column++ {
			key := CellKey(sheetID, cellrange.Address(row, column))
			p.dependencies[key] = struct{}{}
			addresses = append(addresses, key)
		}
	}
	if count == 1 {
		return referenceNode{addresses[0]}, nil
	}
	return rangeNode{rows: lastRow - firstRow + 1, columns: lastColumn - firstColumn + 1, addresses: addresses}, nil
}

// arrayLiteral parses {1,2;3,4}: commas separate columns and semicolons rows,
// which is how a fixed table is written inline in a formula.
func (p *parser) arrayLiteral() (node, error) {
	rows := make([][]node, 0, 2)
	current := make([]node, 0, 2)
	for {
		if p.current().kind == tokenArrayClose {
			p.position++
			break
		}
		value, err := p.expression(0)
		if err != nil {
			return nil, err
		}
		current = append(current, value)
		switch p.current().kind {
		case tokenComma:
			p.position++
		case tokenSemicolon:
			p.position++
			rows = append(rows, current)
			current = make([]node, 0, len(current))
		case tokenArrayClose:
			p.position++
			rows = append(rows, current)
			current = nil
		default:
			return nil, fmt.Errorf("array literal expects , between values and ; between rows")
		}
		if current == nil {
			break
		}
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return nil, formulaError("#VALUE!", "array literal is empty")
	}
	columns := len(rows[0])
	count := 0
	for _, row := range rows {
		if len(row) != columns {
			return nil, formulaError("#VALUE!", "every row of an array literal needs the same number of values")
		}
		count += len(row)
	}
	if count > 100_000 {
		return nil, formulaError("#VALUE!", "range is too large")
	}
	return arrayLiteralNode{rows: len(rows), columns: columns, values: flattenNodes(rows)}, nil
}

func flattenNodes(rows [][]node) []node {
	values := make([]node, 0, len(rows)*len(rows[0]))
	for _, row := range rows {
		values = append(values, row...)
	}
	return values
}

// arrayLiteralNode evaluates its members in place, so {A1,A2;B1,B2} works as
// well as a literal table of numbers.
type arrayLiteralNode struct {
	rows, columns int
	values        []node
}

func (n arrayLiteralNode) eval(cells map[string]any) (any, error) {
	values := make([]any, 0, len(n.values))
	for _, item := range n.values {
		value, err := item.eval(cells)
		if err != nil {
			return nil, err
		}
		scalar, scalarErr := scalarValue(value)
		if scalarErr != nil {
			return nil, scalarErr
		}
		values = append(values, scalar)
	}
	return arrayValue{rows: n.rows, columns: n.columns, values: values}, nil
}

// columnOnlyReference recognises the column half of an unbounded reference.
func columnOnlyReference(value string) (int, bool) {
	if value == "" || len(value) > 3 {
		return 0, false
	}
	column := 0
	for index := 0; index < len(value); index++ {
		if value[index] < 'A' || value[index] > 'Z' {
			return 0, false
		}
		column = column*26 + int(value[index]-'A'+1)
	}
	return column, column >= 1 && column <= MaxColumns
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

// omittedValue marks an argument the author skipped. It is not a value: it
// travels far enough for each function to fall back to that argument's
// default, and disappears everywhere else.
type omittedValue struct{}

// omitted reports whether an optional argument was left out, so a function can
// use its default instead of reading an empty slot as zero or false.
func omitted(value any) bool {
	if _, skipped := value.(omittedValue); skipped {
		return true
	}
	if items, ok := value.([]any); ok && len(items) == 1 {
		_, skipped := items[0].(omittedValue)
		return skipped
	}
	return false
}

func flatten(value any) []any {
	if _, skipped := value.(omittedValue); skipped {
		return nil
	}
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
	if _, skipped := value.(omittedValue); skipped {
		return nil
	}
	switch typed := value.(type) {
	case arrayValue:
		return typed.matrix()
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = publicValue(item)
		}
		return result
	case float64:
		return significantDigits(typed)
	default:
		return value
	}
}

// storableResult applies the checks a value has to pass before anything can
// keep it. 수식은 두 갈래로 셈한다 — 미리보기는 Evaluate 로, 칸에 실제로
// 담기는 값은 graph.go 의 Recalculate 로 간다. 두 갈래가 같은 답을 내야
// 하므로 검사도 한자리에 모아 둔다.
//
// 예전에는 이 검사가 Evaluate 에만 있었다. 그래서 =EXP(1000) 을 미리보기로
// 보면 #NUM! 인데 칸에 적으면 무한대가 그대로 흘러갔고, JSON 으로 옮길 수
// 없어 저장 요청이 통째로 500 이 되었다. 저장 대기줄은 500 을 지우지 않고
// 다시 보내므로, 그 워크북은 그 뒤로 아무것도 저장하지 못했다.
func storableResult(value any, evalErr error) (any, error) {
	if evalErr != nil {
		return value, evalErr
	}
	// A LAMBDA on its own is a function, not a result. Storing it would put
	// something in the cell that nothing can display or calculate with.
	if _, isFunction := value.(lambdaValue); isFunction {
		return value, formulaError("#VALUE!", "a LAMBDA has to be called or passed to MAP, BYROW, BYCOL, REDUCE or SCAN")
	}
	// Infinity and NaN cannot be written as JSON.
	if !finiteValue(value) {
		return value, formulaError("#NUM!", "the result is outside the range a number can hold")
	}
	return value, nil
}

// finiteValue reports whether every number in a result can be written down.
func finiteValue(value any) bool {
	switch typed := value.(type) {
	case float64:
		return !math.IsNaN(typed) && !math.IsInf(typed, 0)
	case arrayValue:
		for _, item := range typed.values {
			if !finiteValue(item) {
				return false
			}
		}
		return true
	case []any:
		for _, item := range typed {
			if !finiteValue(item) {
				return false
			}
		}
		return true
	default:
		return true
	}
}

// significantDigits is what keeps `=0.1+0.2` from reading 0.30000000000000004.
// Binary floating point cannot hold those decimals exactly, and every
// spreadsheet hides the difference the same way: a result carries fifteen
// significant decimal digits, which is all a float64 can promise anyway.
// 반올림은 사람이 적은 십진수를 기준으로 해야 한다. 이진 실수로 셈하면
// 1.005 는 실제로 1.00499999999999989… 로 담기므로 두 자리에서 반올림해도
// 1.00 이 되지만, 엑셀과 시트는 1.01 을 낸다. 돈을 다루는 표에서는 1원이
// 어긋난다.
//
// 두 곳이 다르게 굴러가면 안 되므로 순서를 정해 둔다.
//
//  1. 먼저 열다섯 자리로 다듬는다. 표 프로그램이 값을 보는 방식이고,
//     이 파일의 significantDigits 가 이미 쓰는 방식이다. 이 단계가 있어야
//     =ROUNDUP(0.1+0.2,1) 이 0.4 가 아니라 0.3 이 된다.
//  2. 그 십진수를 그대로 분수로 옮겨 어긋남 없이 자른다.
//
// 자릿수가 음수면 정수 쪽으로 자른다. ROUND(1234,-2) 는 1200 이다.
func decimalRound(number float64, digits int, mode roundMode) float64 {
	if number == 0 || math.IsNaN(number) || math.IsInf(number, 0) {
		return number
	}
	value, ok := decimalRat(number)
	if !ok {
		return number
	}
	distance := digits
	if distance < 0 {
		distance = -distance
	}
	scale := new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(distance)), nil))
	scaled := new(big.Rat)
	if digits >= 0 {
		scaled.Mul(value, scale)
	} else {
		scaled.Quo(value, scale)
	}
	result := new(big.Rat).SetInt(roundRat(scaled, mode))
	if digits >= 0 {
		result.Quo(result, scale)
	} else {
		result.Mul(result, scale)
	}
	rounded, _ := result.Float64()
	return rounded
}

// decimalRat 은 실수를 표 프로그램이 보는 십진수로 옮긴다. 열다섯 자리로
// 다듬은 뒤 분수로 담으므로 이진 실수의 어긋남이 따라오지 않는다.
func decimalRat(number float64) (*big.Rat, bool) {
	if math.IsNaN(number) || math.IsInf(number, 0) {
		return nil, false
	}
	return new(big.Rat).SetString(strconv.FormatFloat(number, 'g', numericDigits, 64))
}

// decimalMultiple 은 어떤 수를 배수 단위로 맞춘다. CEILING, FLOOR, MROUND 가
// 쓴다. 나눗셈을 이진 실수로 하면 0.1+0.2 를 0.1 단위로 올릴 때 0.4 가
// 나온다. 0.30000000000000004 를 0.1 로 나누면 3.0000000000000004 가 되기
// 때문이다. 십진수로 나누면 3 이 되어 0.3 이 나온다.
//
// 부호가 다른 경우는 부르는 쪽에서 이미 걸러내므로 몫은 늘 0 이상이다.
func decimalMultiple(number, factor float64, mode roundMode) (float64, bool) {
	value, ok := decimalRat(number)
	if !ok {
		return 0, false
	}
	step, stepOK := decimalRat(factor)
	if !stepOK || step.Sign() == 0 {
		return 0, false
	}
	quotient := new(big.Rat).Quo(value, step)
	rounded := new(big.Rat).SetInt(roundRat(quotient, mode))
	result, _ := new(big.Rat).Mul(rounded, step).Float64()
	return result, true
}

// roundMode 는 딱 중간에 놓인 값과 나머지를 어느 쪽으로 보낼지 정한다.
type roundMode int

const (
	// roundHalfAway 는 중간값을 0 에서 먼 쪽으로 보낸다. ROUND 가 쓴다.
	roundHalfAway roundMode = iota
	// roundAwayFromZero 는 조금이라도 남으면 0 에서 먼 쪽으로 올린다.
	roundAwayFromZero
	// roundTowardZero 는 남은 것을 버린다.
	roundTowardZero
)

func roundRat(value *big.Rat, mode roundMode) *big.Int {
	quotient, remainder := new(big.Int).QuoRem(value.Num(), value.Denom(), new(big.Int))
	if remainder.Sign() == 0 {
		return quotient
	}
	step := big.NewInt(1)
	if value.Sign() < 0 {
		step = big.NewInt(-1)
	}
	switch mode {
	case roundAwayFromZero:
		return quotient.Add(quotient, step)
	case roundTowardZero:
		return quotient
	}
	doubled := new(big.Int).Abs(remainder)
	doubled.Lsh(doubled, 1)
	if doubled.Cmp(value.Denom()) >= 0 {
		return quotient.Add(quotient, step)
	}
	return quotient
}

func significantDigits(value float64) float64 {
	if value == 0 || math.IsNaN(value) || math.IsInf(value, 0) {
		return value
	}
	magnitude := math.Log10(math.Abs(value))
	// Past what a float64 can represent exactly, rounding would move the value
	// rather than tidy it.
	if magnitude >= 15 || magnitude < -300 {
		return value
	}
	rounded, err := strconv.ParseFloat(strconv.FormatFloat(value, 'g', numericDigits, 64), 64)
	if err != nil {
		return value
	}
	return rounded
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
	if _, skipped := value.(omittedValue); skipped {
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
		// `=0.1+0.2=0.3` has to be TRUE. The two sides differ only in the
		// binary remainder neither of them is meant to carry, and a check like
		// `=IF(합계=예상,"OK","불일치")` would otherwise report a mismatch
		// that is not there.
		left, right = significantDigits(left), significantDigits(right)
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
