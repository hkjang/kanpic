package formula

import (
	"math"
	"strings"
)

// D 로 시작하는 함수들은 표 하나와 조건 표 하나를 받아, 조건에 맞는 줄만
// 골라 한 열을 셈한다. 엑셀과 시트가 오래 써 온 꼴이고, 업무용 표에서
// 아직 자주 보인다.
//
//	DSUM(표, 열, 조건표)
//
// 표와 조건표는 **첫 줄이 머리글** 이다. 조건표의 한 줄 안에 있는 조건은
// 모두 만족해야 하고(그리고), 줄이 여럿이면 그중 하나만 만족하면 된다
// (또는). 조건을 읽는 규칙은 COUNTIF 가 쓰는 것을 그대로 쓴다 — 두 곳에
// 나누어 적으면 ">=" 를 한쪽만 알아듣게 된다.
func evaluateDatabase(name string, values []any) (any, bool, error) {
	switch name {
	case "DSUM", "DAVERAGE", "DCOUNT", "DCOUNTA", "DMAX", "DMIN", "DPRODUCT",
		"DSTDEV", "DSTDEVP", "DVAR", "DVARP", "DGET":
	default:
		return nil, false, nil
	}
	if len(values) != 3 {
		return nil, true, argError(name)
	}
	database, err := toArray(values[0])
	if err != nil {
		return nil, true, err
	}
	criteria, err := toArray(values[2])
	if err != nil {
		return nil, true, err
	}
	if database.rows < 2 || database.columns < 1 {
		return nil, true, formulaError("#VALUE!", name+" needs a table with a header row and at least one row of data")
	}
	if criteria.rows < 1 || criteria.columns < 1 {
		return nil, true, formulaError("#VALUE!", name+" needs a criteria table with a header row")
	}
	column, err := databaseColumn(name, database, values[1])
	if err != nil {
		return nil, true, err
	}
	matched, err := databaseMatches(name, database, criteria)
	if err != nil {
		return nil, true, err
	}
	selected := make([]any, 0, len(matched))
	for _, row := range matched {
		selected = append(selected, database.values[row*database.columns+column])
	}
	return aggregateDatabase(name, selected)
}

// databaseColumn 은 셈할 열을 찾는다. 머리글 이름으로도, 몇 번째 열인지로도
// 가리킬 수 있다 — 엑셀과 시트가 둘 다 받는다.
func databaseColumn(name string, database arrayValue, field any) (int, error) {
	scalar, err := scalarValue(field)
	if err != nil {
		return 0, err
	}
	// 숫자로 가리키면 몇 번째 열인지다. 글자로 가리키면 머리글 이름이다.
	if _, isText := scalar.(string); !isText {
		number, ok := toNumber(scalar)
		if !ok {
			return 0, formulaError("#VALUE!", name+" needs a column name or number")
		}
		index := int(number)
		if float64(index) != number || index < 1 || index > database.columns {
			return 0, formulaError("#VALUE!", name+" field number is outside the table")
		}
		return index - 1, nil
	}
	wanted := strings.TrimSpace(strings.ToLower(display(scalar)))
	if wanted == "" {
		return 0, formulaError("#VALUE!", name+" needs a column name or number")
	}
	for column := 0; column < database.columns; column++ {
		if strings.TrimSpace(strings.ToLower(display(database.values[column]))) == wanted {
			return column, nil
		}
	}
	return 0, formulaError("#VALUE!", name+" cannot find the column "+wanted+" in the table")
}

// databaseMatches 는 조건에 맞는 줄 번호를 돌려준다. 0 번 줄은 머리글이므로
// 1 번부터 본다.
func databaseMatches(name string, database, criteria arrayValue) ([]int, error) {
	// 조건표의 머리글이 표의 몇 번째 열인지 미리 찾아 둔다. 표에 없는
	// 머리글로 거르려 하면 답을 낼 수 없다.
	columns := make([]int, criteria.columns)
	for column := 0; column < criteria.columns; column++ {
		header := display(criteria.values[column])
		if strings.TrimSpace(header) == "" {
			columns[column] = -1
			continue
		}
		index, err := databaseColumn(name, database, header)
		if err != nil {
			return nil, err
		}
		columns[column] = index
	}
	matched := make([]int, 0, database.rows-1)
	for row := 1; row < database.rows; row++ {
		for criterionRow := 1; criterionRow < criteria.rows; criterionRow++ {
			satisfied, used := true, false
			for column := 0; column < criteria.columns && satisfied; column++ {
				if columns[column] < 0 {
					continue
				}
				condition := criteria.values[criterionRow*criteria.columns+column]
				if condition == nil || display(condition) == "" {
					continue
				}
				used = true
				matcher, err := compileCriterion(condition)
				if err != nil {
					return nil, err
				}
				satisfied = matcher(database.values[row*database.columns+columns[column]])
			}
			// 조건이 하나도 적히지 않은 줄은 모든 줄을 고른다 — 엑셀이
			// 그렇게 센다. 그런 줄이 하나라도 있으면 표 전체가 대상이다.
			if satisfied && (used || criteria.columns > 0) {
				matched = append(matched, row)
				break
			}
		}
	}
	return matched, nil
}

func aggregateDatabase(name string, selected []any) (any, bool, error) {
	switch name {
	case "DCOUNTA":
		count := 0
		for _, value := range selected {
			if value != nil && display(value) != "" {
				count++
			}
		}
		return float64(count), true, nil
	case "DGET":
		if len(selected) == 0 {
			return nil, true, formulaError("#VALUE!", "DGET found no row matching the criteria")
		}
		if len(selected) > 1 {
			return nil, true, formulaError("#NUM!", "DGET found more than one row matching the criteria")
		}
		return selected[0], true, nil
	}
	numbers := numericValues(selected)
	switch name {
	case "DCOUNT":
		return float64(len(numbers)), true, nil
	case "DSUM":
		total := 0.0
		for _, number := range numbers {
			total += number
		}
		return total, true, nil
	case "DPRODUCT":
		if len(numbers) == 0 {
			return float64(0), true, nil
		}
		product := 1.0
		for _, number := range numbers {
			product *= number
		}
		return product, true, nil
	case "DMAX", "DMIN":
		if len(numbers) == 0 {
			return float64(0), true, nil
		}
		best := numbers[0]
		for _, number := range numbers[1:] {
			if (name == "DMAX" && number > best) || (name == "DMIN" && number < best) {
				best = number
			}
		}
		return best, true, nil
	case "DAVERAGE":
		if len(numbers) == 0 {
			return nil, true, formulaError("#DIV/0!", "DAVERAGE found no numbers to average")
		}
		return mean(numbers), true, nil
	case "DSTDEV", "DVAR":
		if len(numbers) < 2 {
			return nil, true, formulaError("#DIV/0!", name+" needs at least two numbers")
		}
		variance := populationVariance(numbers, true)
		if name == "DVAR" {
			return variance, true, nil
		}
		return math.Sqrt(variance), true, nil
	case "DSTDEVP", "DVARP":
		if len(numbers) == 0 {
			return nil, true, formulaError("#DIV/0!", name+" needs at least one number")
		}
		variance := populationVariance(numbers, false)
		if name == "DVARP" {
			return variance, true, nil
		}
		return math.Sqrt(variance), true, nil
	}
	return nil, false, nil
}
