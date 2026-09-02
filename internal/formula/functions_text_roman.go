package formula

import (
	"math"
	"strings"
	"time"
)

// 로마 숫자와, 주말을 직접 정하는 날짜 셈.

var romanSymbols = []struct {
	letter rune
	value  int
}{{'I', 1}, {'V', 5}, {'X', 10}, {'L', 50}, {'C', 100}, {'D', 500}, {'M', 1000}}

// romanForms 는 얼마나 짧게 적을지를 정한다. 0 은 흔히 쓰는 꼴이고, 숫자가
// 커질수록 앞에 빼는 글자를 더 멀리서 가져온다.
//
//	499 -> CDXCIX (0), LDVLIV (1), XDIX (2), VDIV (3), ID (4)
func romanNumeral(number, form int) string {
	if number == 0 {
		return ""
	}
	var out strings.Builder
	remaining := number
	for remaining > 0 {
		bestValue, bestText := 0, ""
		for index := len(romanSymbols) - 1; index >= 0; index-- {
			plain := romanSymbols[index]
			if plain.value <= remaining && plain.value > bestValue {
				bestValue, bestText = plain.value, string(plain.letter)
			}
			// 앞에 작은 글자를 두어 빼는 꼴. 흔한 꼴에서는 빼는 글자가
			// 10의 거듭제곱(I·X·C)이고 두 칸 안쪽이어야 한다.
			for smaller := index - 1; smaller >= 0; smaller-- {
				distance := index - smaller
				powerOfTen := smaller%2 == 0
				allowed := (powerOfTen && distance <= 2) || (form >= 1 && distance <= 2) ||
					(form >= 2 && distance <= 3) || (form >= 3 && distance <= 4) || (form >= 4 && distance <= 5)
				if !allowed {
					continue
				}
				value := plain.value - romanSymbols[smaller].value
				if value <= remaining && value > bestValue {
					bestValue = value
					bestText = string(romanSymbols[smaller].letter) + string(plain.letter)
				}
			}
		}
		if bestValue == 0 {
			break
		}
		out.WriteString(bestText)
		remaining -= bestValue
	}
	return out.String()
}

func evaluateRoman(name string, values []any) (any, bool, error) {
	switch name {
	case "ROMAN":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		number, ok := toNumber(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "ROMAN requires a number")
		}
		number = math.Trunc(number)
		if number < 0 || number > 3999 {
			return nil, true, formulaError("#VALUE!", "ROMAN takes a number from 0 to 3999")
		}
		form := 0
		if len(values) == 2 && !omitted(values[1]) {
			if truth, isBool := values[1].(bool); isBool {
				// TRUE 는 흔한 꼴, FALSE 는 가장 짧은 꼴이다.
				if truth {
					form = 0
				} else {
					form = 4
				}
			} else {
				chosen, chosenOK := toNumber(values[1])
				if !chosenOK || chosen < 0 || chosen > 4 || chosen != math.Trunc(chosen) {
					return nil, true, formulaError("#VALUE!", "ROMAN form must be a whole number from 0 to 4")
				}
				form = int(chosen)
			}
		}
		return romanNumeral(int(number), form), true, nil
	case "ARABIC":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		text := strings.ToUpper(strings.TrimSpace(display(values[0])))
		if text == "" {
			return float64(0), true, nil
		}
		sign := 1.0
		if strings.HasPrefix(text, "-") {
			sign, text = -1, text[1:]
		}
		letters := []rune(text)
		total := 0
		for index, letter := range letters {
			value := romanValue(letter)
			if value == 0 {
				return nil, true, formulaError("#VALUE!", "ARABIC needs a roman numeral")
			}
			// 뒤에 더 큰 글자가 오면 빼는 자리다.
			if index+1 < len(letters) && romanValue(letters[index+1]) > value {
				total -= value
				continue
			}
			total += value
		}
		return sign * float64(total), true, nil
	}
	return nil, false, nil
}

func romanValue(letter rune) int {
	for _, symbol := range romanSymbols {
		if symbol.letter == letter {
			return symbol.value
		}
	}
	return 0
}

// weekendDays 는 어느 요일을 쉬는지 정한다. 숫자로 고르거나 월요일부터
// 일요일까지 0·1 을 늘어놓은 일곱 글자로 적는다.
func weekendDays(name string, value any) (map[time.Weekday]bool, error) {
	rest := map[time.Weekday]bool{}
	if value == nil || omitted(value) {
		rest[time.Saturday], rest[time.Sunday] = true, true
		return rest, nil
	}
	if text, isText := value.(string); isText {
		pattern := strings.TrimSpace(text)
		if len(pattern) != 7 {
			return nil, formulaError("#VALUE!", name+" weekend pattern must be seven characters")
		}
		days := []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday}
		for index, character := range pattern {
			switch character {
			case '1':
				rest[days[index]] = true
			case '0':
			default:
				return nil, formulaError("#VALUE!", name+" weekend pattern may only contain 0 and 1")
			}
		}
		if len(rest) == 7 {
			return nil, formulaError("#VALUE!", name+" cannot rest every day")
		}
		return rest, nil
	}
	number, ok := toNumber(value)
	if !ok {
		return nil, formulaError("#VALUE!", name+" requires a weekend code or pattern")
	}
	pairs := map[int][2]time.Weekday{
		1: {time.Saturday, time.Sunday}, 2: {time.Sunday, time.Monday}, 3: {time.Monday, time.Tuesday},
		4: {time.Tuesday, time.Wednesday}, 5: {time.Wednesday, time.Thursday}, 6: {time.Thursday, time.Friday},
		7: {time.Friday, time.Saturday},
	}
	singles := map[int]time.Weekday{
		11: time.Sunday, 12: time.Monday, 13: time.Tuesday, 14: time.Wednesday,
		15: time.Thursday, 16: time.Friday, 17: time.Saturday,
	}
	code := int(math.Trunc(number))
	if pair, found := pairs[code]; found {
		rest[pair[0]], rest[pair[1]] = true, true
		return rest, nil
	}
	if single, found := singles[code]; found {
		rest[single] = true
		return rest, nil
	}
	return nil, formulaError("#NUM!", name+" weekend code must be 1 to 7 or 11 to 17")
}

func evaluateInternationalWorkdays(name string, values []any) (any, bool, error) {
	switch name {
	case "NETWORKDAYS.INTL", "WORKDAY.INTL":
	default:
		return nil, false, nil
	}
	// 휴일은 넷째 인자부터 전부다. 평가기는 범위와 배열을 낱개 값으로 펼쳐
	// 넘기므로, 인자 수를 넷으로 못 박으면 휴일 범위가 오는 순간 거절된다.
	if len(values) < 2 {
		return nil, true, argError(name)
	}
	start, ok := parseDate(values[0])
	if !ok {
		return nil, true, formulaError("#VALUE!", name+" requires a start date")
	}
	var weekendArgument any
	if len(values) >= 3 {
		weekendArgument = values[2]
	}
	rest, err := weekendDays(name, weekendArgument)
	if err != nil {
		return nil, true, err
	}
	holidays := map[string]struct{}{}
	if len(values) >= 4 {
		for _, argument := range values[3:] {
			holidayValues := []any{argument}
			if array, arrayErr := toArray(argument); arrayErr == nil {
				holidayValues = array.values
			}
			for _, value := range holidayValues {
				if moment, dateOK := parseDate(value); dateOK {
					holidays[moment.Format(dateLayout)] = struct{}{}
				}
			}
		}
	}
	working := func(day time.Time) bool {
		if rest[day.Weekday()] {
			return false
		}
		_, holiday := holidays[day.Format(dateLayout)]
		return !holiday
	}
	if name == "NETWORKDAYS.INTL" {
		end, endOK := parseDate(values[1])
		if !endOK {
			return nil, true, formulaError("#VALUE!", name+" requires an end date")
		}
		sign := 1.0
		if end.Before(start) {
			start, end, sign = end, start, -1
		}
		count := 0
		for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
			if working(day) {
				count++
			}
		}
		return sign * float64(count), true, nil
	}
	days, ok := toNumber(values[1])
	if !ok {
		return nil, true, formulaError("#VALUE!", name+" requires a number of days")
	}
	step := 1
	remaining := int(math.Trunc(days))
	if remaining < 0 {
		step, remaining = -1, -remaining
	}
	day := start
	for remaining > 0 {
		day = day.AddDate(0, 0, step)
		if working(day) {
			remaining--
		}
	}
	return day.Format(dateLayout), true, nil
}

// EPOCHTODATE 는 1970년부터 센 시각을 날짜로 바꾼다. 단위는 초·밀리초·
// 마이크로초 중에서 고른다.
func evaluateEpoch(name string, values []any) (any, bool, error) {
	if name != "EPOCHTODATE" {
		return nil, false, nil
	}
	if len(values) < 1 || len(values) > 2 {
		return nil, true, argError(name)
	}
	stamp, ok := toNumber(values[0])
	if !ok {
		return nil, true, formulaError("#VALUE!", "EPOCHTODATE requires a number")
	}
	unit := 1.0
	if len(values) == 2 && !omitted(values[1]) {
		if unit, ok = toNumber(values[1]); !ok || (unit != 1 && unit != 2 && unit != 3) {
			return nil, true, formulaError("#NUM!", "EPOCHTODATE unit must be 1, 2 or 3")
		}
	}
	seconds := stamp
	switch unit {
	case 2:
		seconds = stamp / 1000
	case 3:
		seconds = stamp / 1000000
	}
	moment := time.Unix(int64(math.Trunc(seconds)), 0).UTC()
	return moment.Format("2006-01-02 15:04:05"), true, nil
}
