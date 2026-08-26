package formula

import (
	"sort"
	"strings"
)

// GROUPBY 와 PIVOTBY 는 피벗 테이블을 만들지 않고 수식 하나로 집계한다.
//
//	=GROUPBY(A2:A100, B2:B100, SUM)          부서별 합계
//	=PIVOTBY(A2:A100, B2:B100, C2:C100, SUM) 부서 × 분기 합계
//
// 셋째(GROUPBY)와 넷째(PIVOTBY) 인수는 함수 자체다. LAMBDA 로 적어도 되고
// 이름만 적어도 된다 — 그것을 받게 하려고 함수 이름을 값으로 만들어 두었다.
//
// 엑셀이 받는 인수를 다 받지는 않는다. 받지 않는 자리에 값을 적으면 조용히
// 다르게 셈하지 않고 무엇까지 되는지 말한다. 엑셀에서 옮겨 온 수식이 말없이
// 다른 답을 내는 것이 가장 나쁘다.

const groupTotalLabel = "총합계"

// groupKey 는 이름표 여러 칸을 하나의 열쇠로 만든다. 부서와 분기를 함께
// 묶을 때 쓴다.
type groupKey struct {
	parts string
	label []any
}

func makeGroupKey(fields arrayValue, row int) groupKey {
	labels := make([]any, fields.columns)
	pieces := make([]string, fields.columns)
	for column := 0; column < fields.columns; column++ {
		value := fields.at(row, column)
		labels[column] = value
		pieces[column] = strings.ToUpper(strings.TrimSpace(displayChartKey(value)))
	}
	return groupKey{parts: strings.Join(pieces, "\x00"), label: labels}
}

// displayChartKey 는 이름표를 견주기 좋은 글자로 바꾼다.
func displayChartKey(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		if typed {
			return "TRUE"
		}
		return "FALSE"
	default:
		return display(value)
	}
}

// groupOptions 는 GROUPBY 와 PIVOTBY 가 함께 쓰는 선택지다.
type groupOptions struct {
	headers    bool
	totalDepth int
	sortOrder  int
}

func readGroupOptions(arguments []any, first int, name string) (groupOptions, error) {
	options := groupOptions{headers: false, totalDepth: 1, sortOrder: 1}
	if len(arguments) > first {
		value, err := optionalFlag(arguments[first], name)
		if err != nil {
			return options, err
		}
		options.headers = value
	}
	if len(arguments) > first+1 {
		depth, err := optionalCount(arguments[first+1], name)
		if err != nil {
			return options, err
		}
		options.totalDepth = depth
	}
	if len(arguments) > first+2 {
		order, err := optionalCount(arguments[first+2], name)
		if err != nil {
			return options, err
		}
		options.sortOrder = order
	}
	return options, nil
}

func optionalFlag(value any, name string) (bool, error) {
	if _, omitted := value.(omittedValue); omitted {
		return false, nil
	}
	scalar, err := scalarValue(value)
	if err != nil {
		return false, formulaError("#VALUE!", name+" header flag must be a single value")
	}
	return truthy(scalar), nil
}

func optionalCount(value any, name string) (int, error) {
	if _, omitted := value.(omittedValue); omitted {
		return 1, nil
	}
	scalar, err := scalarValue(value)
	if err != nil {
		return 0, formulaError("#VALUE!", name+" option must be a single number")
	}
	number, ok := numericValue(scalar)
	if !ok {
		return 0, formulaError("#VALUE!", name+" option must be a number")
	}
	return int(number), nil
}

// gatherGroups 는 줄을 이름표별로 모은다. 처음 나온 차례를 기억해 두어,
// 정렬하지 않기로 한 경우에 표에 적힌 차례 그대로 낼 수 있다.
type gathered struct {
	keys  []groupKey
	index map[string]int
	rows  [][]int
}

func gatherGroups(fields arrayValue, rowStart, rowEnd int) gathered {
	result := gathered{index: map[string]int{}}
	for row := rowStart; row < rowEnd; row++ {
		key := makeGroupKey(fields, row)
		position, found := result.index[key.parts]
		if !found {
			position = len(result.keys)
			result.index[key.parts] = position
			result.keys = append(result.keys, key)
			result.rows = append(result.rows, nil)
		}
		result.rows[position] = append(result.rows[position], row)
	}
	return result
}

// orderGroups 는 이름표 차례를 정한다. 0 이면 표에 적힌 차례를 그대로 둔다.
//
// 내림차순은 오름차순의 거울이다. 예전에는 "오름차순이 아니면 내림차순" 으로
// 흉내 냈는데, 그러면 견주기가 앞뒤가 맞지 않아 정렬이 흔들릴 수 있다.
func orderGroups(groups gathered, order int) []int {
	positions := make([]int, len(groups.keys))
	for index := range positions {
		positions[index] = index
	}
	if order == 0 {
		return positions
	}
	sort.SliceStable(positions, func(left, right int) bool {
		first, second := groups.keys[positions[left]].label, groups.keys[positions[right]].label
		if order < 0 {
			first, second = second, first
		}
		return lessGroupLabels(first, second)
	})
	return positions
}

// lessGroupLabels 는 이름표를 앞 칸부터 차례로 견준다.
//
// 부서로 묶고 분기로 다시 묶었으면 분기도 차례가 있어야 한다. 첫 칸만 보면
// 같은 부서 안에서 2분기가 1분기보다 앞에 서고, 사람은 정렬이 망가졌다고
// 읽는다.
func lessGroupLabels(left, right []any) bool {
	for index := range left {
		if index >= len(right) {
			return false
		}
		switch compareGroupLabel(left[index], right[index]) {
		case -1:
			return true
		case 1:
			return false
		}
	}
	return false
}

// compareGroupLabel 은 이름표 한 칸을 견준다. 숫자는 숫자로, 글자는 글자로
// 본다 — 10 이 2 보다 앞에 오면 사람은 정렬이 망가졌다고 읽는다.
func compareGroupLabel(left, right any) int {
	leftNumber, leftOK := numericValue(left)
	rightNumber, rightOK := numericValue(right)
	if leftOK && rightOK {
		switch {
		case leftNumber < rightNumber:
			return -1
		case leftNumber > rightNumber:
			return 1
		default:
			return 0
		}
	}
	// 숫자가 글자보다 앞이다. 엑셀도 그렇게 세운다.
	if leftOK != rightOK {
		if leftOK {
			return -1
		}
		return 1
	}
	leftText, rightText := displayChartKey(left), displayChartKey(right)
	switch {
	case leftText < rightText:
		return -1
	case leftText > rightText:
		return 1
	default:
		return 0
	}
}

// applyGroupFunction 은 모은 줄의 값 하나하나를 함수에 넘긴다.
func applyGroupFunction(function callableValue, cells map[string]any, values arrayValue, rows []int, column int) (any, error) {
	slice := arrayValue{rows: len(rows), columns: 1, values: make([]any, 0, len(rows))}
	for _, row := range rows {
		slice.values = append(slice.values, values.at(row, column))
	}
	return function.call(cells, []any{slice})
}

func evaluateGroupBy(arguments []any, cells map[string]any) (any, error) {
	if len(arguments) < 3 {
		return nil, argError("GROUPBY")
	}
	if len(arguments) > 6 {
		return nil, formulaError("#VALUE!", "GROUPBY takes row_fields, values, function, [headers], [total_depth] and [sort_order]")
	}
	fields, err := toArray(arguments[0])
	if err != nil {
		return nil, err
	}
	values, err := toArray(arguments[1])
	if err != nil {
		return nil, err
	}
	function, ok := arguments[2].(callableValue)
	if !ok {
		return nil, formulaError("#VALUE!", "GROUPBY needs a function such as SUM or a LAMBDA")
	}
	if fields.rows != values.rows {
		return nil, formulaError("#VALUE!", "GROUPBY row fields and values must have the same number of rows")
	}
	options, err := readGroupOptions(arguments, 3, "GROUPBY")
	if err != nil {
		return nil, err
	}
	rowStart := 0
	if options.headers {
		rowStart = 1
	}
	if fields.rows <= rowStart || fields.columns == 0 || values.columns == 0 {
		return nil, formulaError("#N/A", "GROUPBY has no rows to group")
	}
	groups := gatherGroups(fields, rowStart, fields.rows)
	order := orderGroups(groups, options.sortOrder)

	width := fields.columns + values.columns
	rows := make([][]any, 0, len(order)+2)
	if options.headers {
		header := make([]any, 0, width)
		for column := 0; column < fields.columns; column++ {
			header = append(header, fields.at(0, column))
		}
		for column := 0; column < values.columns; column++ {
			header = append(header, values.at(0, column))
		}
		rows = append(rows, header)
	}
	for _, position := range order {
		line := make([]any, 0, width)
		line = append(line, groups.keys[position].label...)
		for column := 0; column < values.columns; column++ {
			computed, callErr := applyGroupFunction(function, cells, values, groups.rows[position], column)
			if callErr != nil {
				return nil, callErr
			}
			line = append(line, computed)
		}
		rows = append(rows, line)
	}
	if options.totalDepth > 0 {
		everyRow := make([]int, 0, fields.rows-rowStart)
		for row := rowStart; row < fields.rows; row++ {
			everyRow = append(everyRow, row)
		}
		line := make([]any, 0, width)
		line = append(line, groupTotalLabel)
		for column := 1; column < fields.columns; column++ {
			line = append(line, nil)
		}
		for column := 0; column < values.columns; column++ {
			computed, callErr := applyGroupFunction(function, cells, values, everyRow, column)
			if callErr != nil {
				return nil, callErr
			}
			line = append(line, computed)
		}
		rows = append(rows, line)
	}
	return matrixToArray(rows, width), nil
}

func evaluatePivotBy(arguments []any, cells map[string]any) (any, error) {
	if len(arguments) < 4 {
		return nil, argError("PIVOTBY")
	}
	if len(arguments) > 7 {
		return nil, formulaError("#VALUE!", "PIVOTBY takes row_fields, col_fields, values, function, [headers], [total_depth] and [sort_order]")
	}
	rowFields, err := toArray(arguments[0])
	if err != nil {
		return nil, err
	}
	columnFields, err := toArray(arguments[1])
	if err != nil {
		return nil, err
	}
	values, err := toArray(arguments[2])
	if err != nil {
		return nil, err
	}
	function, ok := arguments[3].(callableValue)
	if !ok {
		return nil, formulaError("#VALUE!", "PIVOTBY needs a function such as SUM or a LAMBDA")
	}
	if rowFields.rows != values.rows || columnFields.rows != values.rows {
		return nil, formulaError("#VALUE!", "PIVOTBY fields and values must have the same number of rows")
	}
	if values.columns != 1 {
		return nil, formulaError("#VALUE!", "PIVOTBY takes a single column of values")
	}
	options, err := readGroupOptions(arguments, 4, "PIVOTBY")
	if err != nil {
		return nil, err
	}
	rowStart := 0
	if options.headers {
		rowStart = 1
	}
	if values.rows <= rowStart {
		return nil, formulaError("#N/A", "PIVOTBY has no rows to group")
	}
	downGroups := gatherGroups(rowFields, rowStart, values.rows)
	acrossGroups := gatherGroups(columnFields, rowStart, values.rows)
	downOrder := orderGroups(downGroups, options.sortOrder)
	acrossOrder := orderGroups(acrossGroups, options.sortOrder)

	// 어느 칸이 어느 교차점에 드는지 미리 모아 둔다. 교차점마다 표를 다시
	// 훑으면 줄 수와 칸 수를 곱한 만큼 훑게 된다.
	buckets := make(map[int]map[int][]int, len(downOrder))
	for row := rowStart; row < values.rows; row++ {
		down := downGroups.index[makeGroupKey(rowFields, row).parts]
		across := acrossGroups.index[makeGroupKey(columnFields, row).parts]
		if buckets[down] == nil {
			buckets[down] = map[int][]int{}
		}
		buckets[down][across] = append(buckets[down][across], row)
	}

	width := rowFields.columns + len(acrossOrder)
	if options.totalDepth > 0 {
		width++
	}
	rows := make([][]any, 0, len(downOrder)+2)
	// 열 이름표 칸 수만큼 머리글 줄을 낸다. 첫 칸만 적으면 연도 × 분기로
	// 묶었을 때 "2026" 이 두 번 나오고 어느 쪽이 1분기인지 알 수 없다.
	// 값은 맞는데 표가 거짓말을 하는 것이 가장 나쁘다.
	headerDepth := columnFields.columns
	for depth := 0; depth < headerDepth; depth++ {
		last := depth == headerDepth-1
		header := make([]any, 0, width)
		for column := 0; column < rowFields.columns; column++ {
			// 행 이름표의 이름은 마지막 머리글 줄에 적는다.
			if options.headers && last {
				header = append(header, rowFields.at(0, column))
				continue
			}
			header = append(header, nil)
		}
		for _, position := range acrossOrder {
			label := acrossGroups.keys[position].label
			if depth < len(label) {
				header = append(header, label[depth])
				continue
			}
			header = append(header, nil)
		}
		if options.totalDepth > 0 {
			if last {
				header = append(header, groupTotalLabel)
			} else {
				header = append(header, nil)
			}
		}
		rows = append(rows, header)
	}

	emit := func(label []any, byAcross map[int][]int, everyRow []int) error {
		line := make([]any, 0, width)
		line = append(line, label...)
		for _, position := range acrossOrder {
			cellRows := byAcross[position]
			if len(cellRows) == 0 {
				// 그 교차점에 자료가 없으면 비워 둔다. 0 을 적으면 "그만큼
				// 팔았다" 는 뜻이 되어 없는 것과 구별되지 않는다.
				line = append(line, nil)
				continue
			}
			computed, callErr := applyGroupFunction(function, cells, values, cellRows, 0)
			if callErr != nil {
				return callErr
			}
			line = append(line, computed)
		}
		if options.totalDepth > 0 {
			computed, callErr := applyGroupFunction(function, cells, values, everyRow, 0)
			if callErr != nil {
				return callErr
			}
			line = append(line, computed)
		}
		rows = append(rows, line)
		return nil
	}

	for _, position := range downOrder {
		if err := emit(downGroups.keys[position].label, buckets[position], downGroups.rows[position]); err != nil {
			return nil, err
		}
	}
	if options.totalDepth > 0 {
		byAcross := map[int][]int{}
		everyRow := make([]int, 0, values.rows-rowStart)
		for row := rowStart; row < values.rows; row++ {
			across := acrossGroups.index[makeGroupKey(columnFields, row).parts]
			byAcross[across] = append(byAcross[across], row)
			everyRow = append(everyRow, row)
		}
		label := make([]any, rowFields.columns)
		label[0] = groupTotalLabel
		if err := emit(label, byAcross, everyRow); err != nil {
			return nil, err
		}
	}
	return matrixToArray(rows, width), nil
}

// matrixToArray 는 줄 목록을 배열 값으로 만든다. 짧은 줄은 빈 칸으로 채워
// 네모를 맞춘다.
func matrixToArray(rows [][]any, width int) arrayValue {
	result := arrayValue{rows: len(rows), columns: width, values: make([]any, 0, len(rows)*width)}
	for _, line := range rows {
		for column := 0; column < width; column++ {
			if column < len(line) {
				result.values = append(result.values, line[column])
				continue
			}
			result.values = append(result.values, nil)
		}
	}
	return result
}
