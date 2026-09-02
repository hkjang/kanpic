package formula

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// QUERY runs a small SQL-like language over a range, the way Google Sheets
// does. It is the one function that replaces a pivot, a filter and a sort at
// once, so the supported grammar covers what people actually type:
//
//	select A, sum(C) where B = '서울' group by A order by sum(C) desc limit 5
//
// Columns are named by their position inside the range, so A is the range's
// first column rather than the sheet's.

type queryPlan struct {
	selects  []queryColumn
	where    queryCondition
	groupBy  []int
	orderBy  []queryOrder
	limit    int
	offset   int
	labels   map[string]string
	starOnly bool
}

// queryColumn is one item in the select list: either a plain column or an
// aggregate over one.
type queryColumn struct {
	column    int
	aggregate string
}

type queryOrder struct {
	item       queryColumn
	descending bool
}

type queryCondition interface{ matches(row []any) bool }

// evaluateQuery is the entry point used by the function dispatch.
func evaluateQuery(arguments []any) (any, bool, error) {
	if len(arguments) < 2 || len(arguments) > 3 {
		return nil, true, argError("QUERY")
	}
	data, err := toArray(arguments[0])
	if err != nil {
		return nil, true, err
	}
	text := strings.TrimSpace(display(scalarOrFirst(arguments[1])))
	headers := -1
	if len(arguments) == 3 && !omitted(arguments[2]) {
		count, headerErr := integerValue(scalarOrFirst(arguments[2]), "QUERY")
		if headerErr != nil {
			return nil, true, headerErr
		}
		headers = count
	}
	if headers < 0 {
		headers = guessHeaderRows(data)
	}
	if headers > data.rows {
		return nil, true, formulaError("#VALUE!", "QUERY header count is larger than the range")
	}
	titles := headerTitles(data, headers)
	rows := make([][]any, 0, data.rows-headers)
	for row := headers; row < data.rows; row++ {
		values := make([]any, 0, data.columns)
		for column := 0; column < data.columns; column++ {
			values = append(values, data.at(row, column))
		}
		rows = append(rows, values)
	}
	plan, err := parseQuery(text, data.columns)
	if err != nil {
		return nil, true, err
	}
	result, err := plan.run(rows, titles, headers > 0)
	if err != nil {
		return nil, true, err
	}
	return result, true, nil
}

// guessHeaderRows treats a first row of text over a body that is not all text
// as a header, which is what makes QUERY work without a third argument.
func guessHeaderRows(data arrayValue) int {
	if data.rows < 2 {
		return 0
	}
	for column := 0; column < data.columns; column++ {
		if _, isText := data.at(0, column).(string); !isText && data.at(0, column) != nil {
			return 0
		}
	}
	for row := 1; row < data.rows; row++ {
		for column := 0; column < data.columns; column++ {
			value := data.at(row, column)
			if value == nil {
				continue
			}
			if _, isText := value.(string); !isText {
				return 1
			}
		}
	}
	return 0
}

func headerTitles(data arrayValue, headers int) []string {
	titles := make([]string, data.columns)
	for column := 0; column < data.columns; column++ {
		if headers > 0 {
			titles[column] = display(data.at(headers-1, column))
		}
		if titles[column] == "" {
			titles[column] = columnName(column + 1)
		}
	}
	return titles
}

func (p queryPlan) run(rows [][]any, titles []string, hadHeaders bool) (arrayValue, error) {
	filtered := rows
	if p.where != nil {
		filtered = make([][]any, 0, len(rows))
		for _, row := range rows {
			if p.where.matches(row) {
				filtered = append(filtered, row)
			}
		}
	}
	aggregated := len(p.groupBy) > 0 || p.hasAggregate()
	var output [][]any
	if aggregated {
		grouped, err := p.aggregate(filtered)
		if err != nil {
			return arrayValue{}, err
		}
		output = grouped
	} else {
		output = make([][]any, 0, len(filtered))
		for _, row := range filtered {
			values := make([]any, 0, len(p.selects))
			for _, item := range p.selects {
				values = append(values, row[item.column])
			}
			output = append(output, values)
		}
	}
	p.sortRows(output)
	if p.offset > 0 {
		if p.offset >= len(output) {
			output = nil
		} else {
			output = output[p.offset:]
		}
	}
	if p.limit > 0 && p.limit < len(output) {
		output = output[:p.limit]
	}
	header := p.headerRow(titles)
	includeHeader := hadHeaders || aggregated
	if len(output) == 0 && !includeHeader {
		return arrayValue{}, formulaError("#N/A", "QUERY found no rows")
	}
	columns := len(p.selects)
	values := make([]any, 0, (len(output)+1)*columns)
	if includeHeader {
		for _, title := range header {
			values = append(values, title)
		}
	}
	for _, row := range output {
		values = append(values, row...)
	}
	count := len(values) / columns
	if count == 0 {
		return arrayValue{}, formulaError("#N/A", "QUERY found no rows")
	}
	return arrayValue{rows: count, columns: columns, values: values}, nil
}

func (p queryPlan) hasAggregate() bool {
	for _, item := range p.selects {
		if item.aggregate != "" {
			return true
		}
	}
	return false
}

// aggregate groups the rows and reduces every aggregate in the select list.
func (p queryPlan) aggregate(rows [][]any) ([][]any, error) {
	for _, item := range p.selects {
		if item.aggregate != "" || containsInt(p.groupBy, item.column) {
			continue
		}
		return nil, formulaError("#VALUE!", "QUERY: "+columnName(item.column+1)+" must be grouped or aggregated")
	}
	order := make([]string, 0)
	groups := make(map[string][][]any)
	for _, row := range rows {
		parts := make([]string, 0, len(p.groupBy))
		for _, column := range p.groupBy {
			parts = append(parts, display(row[column]))
		}
		key := strings.Join(parts, "\x00")
		if _, seen := groups[key]; !seen {
			order = append(order, key)
		}
		groups[key] = append(groups[key], row)
	}
	// With no group by, every row folds into one summary row.
	if len(p.groupBy) == 0 {
		if len(rows) == 0 {
			return nil, nil
		}
		order, groups = []string{""}, map[string][][]any{"": rows}
	}
	output := make([][]any, 0, len(order))
	for _, key := range order {
		members := groups[key]
		values := make([]any, 0, len(p.selects))
		for _, item := range p.selects {
			if item.aggregate == "" {
				values = append(values, members[0][item.column])
				continue
			}
			values = append(values, reduceColumn(item.aggregate, members, item.column))
		}
		output = append(output, values)
	}
	return output, nil
}

func reduceColumn(aggregate string, rows [][]any, column int) any {
	numbers := make([]float64, 0, len(rows))
	filled := 0
	for _, row := range rows {
		value := row[column]
		if value == nil || display(value) == "" {
			continue
		}
		filled++
		if number, ok := toNumber(value); ok {
			numbers = append(numbers, number)
		}
	}
	switch aggregate {
	case "count":
		return float64(filled)
	case "sum", "avg":
		total := 0.0
		for _, number := range numbers {
			total += number
		}
		if aggregate == "sum" {
			return total
		}
		if len(numbers) == 0 {
			return nil
		}
		return total / float64(len(numbers))
	case "min", "max":
		if len(numbers) == 0 {
			return nil
		}
		result := numbers[0]
		for _, number := range numbers[1:] {
			if (aggregate == "min" && number < result) || (aggregate == "max" && number > result) {
				result = number
			}
		}
		return result
	}
	return nil
}

func (p queryPlan) sortRows(rows [][]any) {
	if len(p.orderBy) == 0 {
		return
	}
	positions := make([]int, 0, len(p.orderBy))
	for _, order := range p.orderBy {
		position := -1
		for index, item := range p.selects {
			if item.column == order.item.column && item.aggregate == order.item.aggregate {
				position = index
				break
			}
		}
		positions = append(positions, position)
	}
	sort.SliceStable(rows, func(first, second int) bool {
		for index, order := range p.orderBy {
			position := positions[index]
			if position < 0 {
				continue
			}
			difference := compare(rows[first][position], rows[second][position])
			if difference == 0 {
				continue
			}
			if order.descending {
				return difference > 0
			}
			return difference < 0
		}
		return false
	})
}

func (p queryPlan) headerRow(titles []string) []string {
	header := make([]string, 0, len(p.selects))
	for _, item := range p.selects {
		title := titles[item.column]
		if item.aggregate != "" {
			title = item.aggregate + " " + title
		}
		if label, found := p.labels[item.key()]; found {
			title = label
		}
		header = append(header, title)
	}
	return header
}

func (c queryColumn) key() string {
	if c.aggregate == "" {
		return columnName(c.column + 1)
	}
	return c.aggregate + "(" + columnName(c.column+1) + ")"
}

func containsInt(values []int, wanted int) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------- parsing

type queryParser struct {
	tokens  []string
	at      int
	columns int
}

var queryAggregates = map[string]struct{}{"sum": {}, "count": {}, "avg": {}, "min": {}, "max": {}}

func parseQuery(text string, columns int) (queryPlan, error) {
	tokens, err := lexQuery(text)
	if err != nil {
		return queryPlan{}, err
	}
	parser := &queryParser{tokens: tokens, columns: columns}
	plan := queryPlan{labels: map[string]string{}}
	// A query with no select at all means every column, which is what an empty
	// query string does in Sheets.
	if !parser.peekKeyword("select") {
		plan.selects = allColumns(columns)
		plan.starOnly = true
	}
	for parser.at < len(parser.tokens) {
		switch {
		case parser.takeKeyword("select"):
			if parser.takeSymbol("*") {
				plan.selects = allColumns(columns)
				plan.starOnly = true
				continue
			}
			items, err := parser.columnList()
			if err != nil {
				return queryPlan{}, err
			}
			plan.selects = items
		case parser.takeKeyword("where"):
			condition, err := parser.condition()
			if err != nil {
				return queryPlan{}, err
			}
			plan.where = condition
		case parser.takeKeyword("group"):
			if !parser.takeKeyword("by") {
				return queryPlan{}, queryError("group must be followed by by")
			}
			items, err := parser.columnList()
			if err != nil {
				return queryPlan{}, err
			}
			for _, item := range items {
				plan.groupBy = append(plan.groupBy, item.column)
			}
		case parser.takeKeyword("order"):
			if !parser.takeKeyword("by") {
				return queryPlan{}, queryError("order must be followed by by")
			}
			orders, err := parser.orderList()
			if err != nil {
				return queryPlan{}, err
			}
			plan.orderBy = orders
		case parser.takeKeyword("limit"), parser.takeKeyword("offset"):
			keyword := strings.ToLower(parser.tokens[parser.at-1])
			number, err := parser.number()
			if err != nil {
				return queryPlan{}, err
			}
			if keyword == "limit" {
				plan.limit = number
			} else {
				plan.offset = number
			}
		case parser.takeKeyword("label"):
			if err := parser.labelList(plan.labels); err != nil {
				return queryPlan{}, err
			}
		default:
			return queryPlan{}, queryError("unexpected " + parser.tokens[parser.at])
		}
	}
	if len(plan.selects) == 0 {
		plan.selects = allColumns(columns)
		plan.starOnly = true
	}
	// select * with a group by means the grouped columns, as in Sheets.
	if plan.starOnly && len(plan.groupBy) > 0 {
		plan.selects = nil
		for _, column := range plan.groupBy {
			plan.selects = append(plan.selects, queryColumn{column: column})
		}
	}
	return plan, nil
}

func allColumns(columns int) []queryColumn {
	items := make([]queryColumn, 0, columns)
	for index := 0; index < columns; index++ {
		items = append(items, queryColumn{column: index})
	}
	return items
}

func queryError(message string) error {
	return formulaError("#VALUE!", "QUERY: "+message)
}

func lexQuery(text string) ([]string, error) {
	tokens := make([]string, 0, 16)
	for index := 0; index < len(text); {
		letter := text[index]
		switch {
		case letter == ' ' || letter == '\t' || letter == '\n' || letter == '\r':
			index++
		case letter == '\'' || letter == '"':
			quote := letter
			end := index + 1
			var builder strings.Builder
			for end < len(text) && text[end] != quote {
				builder.WriteByte(text[end])
				end++
			}
			if end >= len(text) {
				return nil, queryError("unterminated text value")
			}
			tokens = append(tokens, "'"+builder.String())
			index = end + 1
		case letter == '(' || letter == ')' || letter == ',' || letter == '*':
			tokens = append(tokens, string(letter))
			index++
		case letter == '<' || letter == '>' || letter == '=' || letter == '!':
			end := index + 1
			if end < len(text) && (text[end] == '=' || text[end] == '>') {
				end++
			}
			tokens = append(tokens, text[index:end])
			index = end
		default:
			end := index
			for end < len(text) && !strings.ContainsRune(" \t\n\r'\"(),*<>=!", rune(text[end])) {
				end++
			}
			if end == index {
				return nil, queryError("unexpected character " + string(letter))
			}
			tokens = append(tokens, text[index:end])
			index = end
		}
	}
	return tokens, nil
}

func (p *queryParser) peekKeyword(word string) bool {
	return p.at < len(p.tokens) && strings.EqualFold(p.tokens[p.at], word)
}

func (p *queryParser) takeKeyword(word string) bool {
	if !p.peekKeyword(word) {
		return false
	}
	p.at++
	return true
}

func (p *queryParser) takeSymbol(symbol string) bool {
	if p.at < len(p.tokens) && p.tokens[p.at] == symbol {
		p.at++
		return true
	}
	return false
}

func (p *queryParser) number() (int, error) {
	if p.at >= len(p.tokens) {
		return 0, queryError("a number is missing")
	}
	value, err := strconv.Atoi(p.tokens[p.at])
	if err != nil {
		return 0, queryError(p.tokens[p.at] + " is not a number")
	}
	p.at++
	return value, nil
}

// columnItem reads A or sum(A).
func (p *queryParser) columnItem() (queryColumn, error) {
	if p.at >= len(p.tokens) {
		return queryColumn{}, queryError("a column is missing")
	}
	token := p.tokens[p.at]
	lower := strings.ToLower(token)
	if _, isAggregate := queryAggregates[lower]; isAggregate && p.at+1 < len(p.tokens) && p.tokens[p.at+1] == "(" {
		p.at += 2
		column, err := p.columnReference()
		if err != nil {
			return queryColumn{}, err
		}
		if !p.takeSymbol(")") {
			return queryColumn{}, queryError("a closing bracket is missing")
		}
		return queryColumn{column: column, aggregate: lower}, nil
	}
	column, err := p.columnReference()
	if err != nil {
		return queryColumn{}, err
	}
	return queryColumn{column: column}, nil
}

func (p *queryParser) columnReference() (int, error) {
	if p.at >= len(p.tokens) {
		return 0, queryError("a column is missing")
	}
	token := strings.ToUpper(p.tokens[p.at])
	column, ok := columnOnlyReference(token)
	if !ok || column > p.columns {
		return 0, queryError(p.tokens[p.at] + " is not a column of the range")
	}
	p.at++
	return column - 1, nil
}

func (p *queryParser) columnList() ([]queryColumn, error) {
	items := make([]queryColumn, 0, 4)
	for {
		item, err := p.columnItem()
		if err != nil {
			return nil, err
		}
		items = append(items, item)
		if !p.takeSymbol(",") {
			return items, nil
		}
	}
}

func (p *queryParser) orderList() ([]queryOrder, error) {
	orders := make([]queryOrder, 0, 2)
	for {
		item, err := p.columnItem()
		if err != nil {
			return nil, err
		}
		order := queryOrder{item: item}
		if p.takeKeyword("desc") {
			order.descending = true
		} else {
			p.takeKeyword("asc")
		}
		orders = append(orders, order)
		if !p.takeSymbol(",") {
			return orders, nil
		}
	}
}

func (p *queryParser) labelList(labels map[string]string) error {
	for {
		item, err := p.columnItem()
		if err != nil {
			return err
		}
		if p.at >= len(p.tokens) || !strings.HasPrefix(p.tokens[p.at], "'") {
			return queryError("a label needs quoted text")
		}
		labels[item.key()] = p.tokens[p.at][1:]
		p.at++
		if !p.takeSymbol(",") {
			return nil
		}
	}
}

// ------------------------------------------------------------- conditions

type queryComparison struct {
	column   int
	operator string
	value    any
	pattern  *regexp.Regexp
}

func (c queryComparison) matches(row []any) bool {
	value := row[c.column]
	switch c.operator {
	case "is null":
		return value == nil || display(value) == ""
	case "is not null":
		return value != nil && display(value) != ""
	case "contains":
		return strings.Contains(display(value), display(c.value))
	case "starts with":
		return strings.HasPrefix(display(value), display(c.value))
	case "ends with":
		return strings.HasSuffix(display(value), display(c.value))
	case "matches", "like":
		return c.pattern != nil && c.pattern.MatchString(display(value))
	}
	difference := compare(value, c.value)
	switch c.operator {
	case "=":
		return difference == 0
	case "!=", "<>":
		return difference != 0
	case "<":
		return difference < 0
	case "<=":
		return difference <= 0
	case ">":
		return difference > 0
	case ">=":
		return difference >= 0
	}
	return false
}

type queryLogical struct {
	operator string
	operands []queryCondition
}

func (c queryLogical) matches(row []any) bool {
	if c.operator == "not" {
		return !c.operands[0].matches(row)
	}
	for _, operand := range c.operands {
		matched := operand.matches(row)
		if c.operator == "and" && !matched {
			return false
		}
		if c.operator == "or" && matched {
			return true
		}
	}
	return c.operator == "and"
}

func (p *queryParser) condition() (queryCondition, error) {
	return p.orCondition()
}

func (p *queryParser) orCondition() (queryCondition, error) {
	left, err := p.andCondition()
	if err != nil {
		return nil, err
	}
	operands := []queryCondition{left}
	for p.takeKeyword("or") {
		next, err := p.andCondition()
		if err != nil {
			return nil, err
		}
		operands = append(operands, next)
	}
	if len(operands) == 1 {
		return left, nil
	}
	return queryLogical{operator: "or", operands: operands}, nil
}

func (p *queryParser) andCondition() (queryCondition, error) {
	left, err := p.notCondition()
	if err != nil {
		return nil, err
	}
	operands := []queryCondition{left}
	for p.takeKeyword("and") {
		next, err := p.notCondition()
		if err != nil {
			return nil, err
		}
		operands = append(operands, next)
	}
	if len(operands) == 1 {
		return left, nil
	}
	return queryLogical{operator: "and", operands: operands}, nil
}

func (p *queryParser) notCondition() (queryCondition, error) {
	if p.takeKeyword("not") {
		inner, err := p.notCondition()
		if err != nil {
			return nil, err
		}
		return queryLogical{operator: "not", operands: []queryCondition{inner}}, nil
	}
	if p.takeSymbol("(") {
		inner, err := p.orCondition()
		if err != nil {
			return nil, err
		}
		if !p.takeSymbol(")") {
			return nil, queryError("a closing bracket is missing")
		}
		return inner, nil
	}
	return p.comparison()
}

func (p *queryParser) comparison() (queryCondition, error) {
	column, err := p.columnReference()
	if err != nil {
		return nil, err
	}
	if p.at >= len(p.tokens) {
		return nil, queryError("a comparison is incomplete")
	}
	// The word operators are two or three tokens long.
	for _, phrase := range [][]string{{"is", "not", "null"}, {"is", "null"}, {"starts", "with"}, {"ends", "with"}} {
		if p.matchPhrase(phrase) {
			operator := strings.Join(phrase, " ")
			if operator == "starts with" || operator == "ends with" {
				value, valueErr := p.value()
				if valueErr != nil {
					return nil, valueErr
				}
				return queryComparison{column: column, operator: operator, value: value}, nil
			}
			return queryComparison{column: column, operator: operator}, nil
		}
	}
	operator := strings.ToLower(p.tokens[p.at])
	switch operator {
	case "=", "!=", "<>", "<", "<=", ">", ">=", "contains", "matches", "like":
		p.at++
	default:
		return nil, queryError("unexpected " + p.tokens[p.at])
	}
	value, err := p.value()
	if err != nil {
		return nil, err
	}
	condition := queryComparison{column: column, operator: operator, value: value}
	if operator == "matches" || operator == "like" {
		pattern := display(value)
		if operator == "like" {
			pattern = "^" + strings.ReplaceAll(strings.ReplaceAll(regexp.QuoteMeta(pattern), "%", ".*"), "_", ".") + "$"
		} else {
			pattern = "^(?:" + pattern + ")$"
		}
		compiled, compileErr := regexp.Compile(pattern)
		if compileErr != nil {
			return nil, queryError("invalid pattern " + display(value))
		}
		condition.pattern = compiled
	}
	return condition, nil
}

func (p *queryParser) matchPhrase(phrase []string) bool {
	if p.at+len(phrase) > len(p.tokens) {
		return false
	}
	for index, word := range phrase {
		if !strings.EqualFold(p.tokens[p.at+index], word) {
			return false
		}
	}
	p.at += len(phrase)
	return true
}

func (p *queryParser) value() (any, error) {
	if p.at >= len(p.tokens) {
		return nil, queryError("a value is missing")
	}
	token := p.tokens[p.at]
	p.at++
	if strings.HasPrefix(token, "'") {
		return token[1:], nil
	}
	// 질의문의 값도 셀 값과 같은 자로 읽는다. Go 의 리터럴 문법(1_000, Inf)은
	// 스프레드시트의 숫자가 아니다.
	if number, ok := numberFromText(token); ok {
		return number, nil
	}
	switch strings.ToLower(token) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	case "null":
		return nil, nil
	}
	// A bare word is treated as text so `where B = 서울` still works.
	return token, nil
}
