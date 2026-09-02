package formula

import (
	"fmt"
	"sort"
	"time"
)

// 한국의 공휴일. 설·추석·부처님오신날은 음력이라 닫힌 공식이 없다 — 한국천문연구원이
// 발표한 날짜를 해마다 표로 담는다. 표에 없는 해는 #N/A 로 답한다. 지어내지 않는다.
//
// 대체공휴일은 법이 바뀐 시점을 따른다. 2021년 광복절부터 삼일절·광복절·개천절·
// 한글날이, 2023년부터 부처님오신날·성탄절이 주말과 겹치면 다음 평일로 옮겨진다.
// 설·추석 연휴와 어린이날은 2014년부터다. 신정과 현충일은 옮기지 않는다.

type koreanLunarYear struct {
	seollal, buddha, chuseok string // 설날 당일, 부처님오신날, 추석 당일
}

const koreanHolidayFirstYear, koreanHolidayLastYear = 2020, 2030

var koreanLunarDates = map[int]koreanLunarYear{
	2020: {"2020-01-25", "2020-04-30", "2020-10-01"},
	2021: {"2021-02-12", "2021-05-19", "2021-09-21"},
	2022: {"2022-02-01", "2022-05-08", "2022-09-10"},
	2023: {"2023-01-22", "2023-05-27", "2023-09-29"},
	2024: {"2024-02-10", "2024-05-15", "2024-09-17"},
	2025: {"2025-01-29", "2025-05-05", "2025-10-06"},
	2026: {"2026-02-17", "2026-05-24", "2026-09-25"},
	2027: {"2027-02-07", "2027-05-13", "2027-09-15"},
	2028: {"2028-01-27", "2028-05-02", "2028-10-03"},
	2029: {"2029-02-13", "2029-05-20", "2029-09-22"},
	2030: {"2030-02-03", "2030-05-09", "2030-09-12"},
}

// 임시공휴일과 선거일. 정해진 규칙이 없으므로 지정된 것만 적는다.
var koreanOneOffHolidays = map[string]string{
	"2020-04-15": "국회의원선거", "2020-08-17": "임시공휴일",
	"2022-03-09": "대통령선거", "2022-06-01": "지방선거",
	"2023-10-02": "임시공휴일",
	"2024-04-10": "국회의원선거", "2024-10-01": "국군의날",
	"2025-01-27": "임시공휴일", "2025-06-03": "대통령선거",
	"2026-06-03": "지방선거",
}

type koreanHoliday struct {
	day  time.Time
	name string
	// substitute 는 주말이나 다른 공휴일과 겹칠 때 대체공휴일을 두는 규칙이다.
	substitute func(day time.Time, taken map[string]bool) bool
}

func koreanDay(text string) time.Time {
	day, _ := time.Parse(dateLayout, text)
	return day
}

func weekend(day time.Time) bool {
	return day.Weekday() == time.Saturday || day.Weekday() == time.Sunday
}

// koreanHolidays 는 한 해의 공휴일을 날짜 순으로 돌려준다. 대체공휴일이 포함된다.
func koreanHolidays(year int) ([]koreanHoliday, bool) {
	lunar, ok := koreanLunarDates[year]
	if !ok {
		return nil, false
	}
	fixed := func(month time.Month, day int) time.Time { return time.Date(year, month, day, 0, 0, 0, 0, time.UTC) }
	// 주말과 겹치면 옮기는 규칙. since 이전에는 옮기지 않았다.
	onWeekend := func(since time.Time) func(time.Time, map[string]bool) bool {
		return func(day time.Time, _ map[string]bool) bool { return !day.Before(since) && weekend(day) }
	}
	// 어린이날: 토·일요일이나 다른 공휴일과 겹치면 옮긴다(2014년부터).
	childrensDay := func(day time.Time, taken map[string]bool) bool {
		return year >= 2014 && (weekend(day) || taken[day.Format(dateLayout)])
	}
	never := func(time.Time, map[string]bool) bool { return false }
	seollal, buddha, chuseok := koreanDay(lunar.seollal), koreanDay(lunar.buddha), koreanDay(lunar.chuseok)
	items := []koreanHoliday{
		{fixed(time.January, 1), "신정", never},
		{fixed(time.March, 1), "삼일절", onWeekend(koreanDay("2021-08-04"))},
		{fixed(time.May, 5), "어린이날", childrensDay},
		{buddha, "부처님오신날", onWeekend(koreanDay("2023-05-04"))},
		{fixed(time.June, 6), "현충일", never},
		{fixed(time.August, 15), "광복절", onWeekend(koreanDay("2021-08-04"))},
		{fixed(time.October, 3), "개천절", onWeekend(koreanDay("2021-08-04"))},
		{fixed(time.October, 9), "한글날", onWeekend(koreanDay("2021-08-04"))},
		{fixed(time.December, 25), "성탄절", onWeekend(koreanDay("2023-05-04"))},
	}
	// 설·추석 연휴는 사흘이 한 덩어리다. 그중 하루라도 일요일이나 다른 공휴일과
	// 겹치면 연휴 다음 첫 평일을 쉰다. 토요일은 옮기지 않는다.
	for _, run := range []struct {
		center time.Time
		name   string
	}{{seollal, "설날"}, {chuseok, "추석"}} {
		for offset := -1; offset <= 1; offset++ {
			items = append(items, koreanHoliday{run.center.AddDate(0, 0, offset), run.name, never})
		}
	}
	for text, name := range koreanOneOffHolidays {
		if day := koreanDay(text); day.Year() == year {
			items = append(items, koreanHoliday{day, name, never})
		}
	}
	sort.SliceStable(items, func(i, j int) bool { return items[i].day.Before(items[j].day) })
	taken := make(map[string]bool, len(items)*2)
	for _, item := range items {
		taken[item.day.Format(dateLayout)] = true
	}
	// 대체공휴일은 날짜 순으로 정한다. 앞서 정한 대체일도 "이미 쉬는 날" 이다.
	next := func(from time.Time) time.Time {
		day := from.AddDate(0, 0, 1)
		for weekend(day) || taken[day.Format(dateLayout)] {
			day = day.AddDate(0, 0, 1)
		}
		return day
	}
	extra := make([]koreanHoliday, 0)
	for _, item := range items {
		if item.substitute(item.day, taken) {
			day := next(item.day)
			taken[day.Format(dateLayout)] = true
			extra = append(extra, koreanHoliday{day, item.name + " 대체공휴일", never})
		}
	}
	for _, run := range []struct {
		center time.Time
		name   string
	}{{seollal, "설날"}, {chuseok, "추석"}} {
		if year < 2014 {
			continue
		}
		first, last := run.center.AddDate(0, 0, -1), run.center.AddDate(0, 0, 1)
		clash := false
		for day := first; !day.After(last); day = day.AddDate(0, 0, 1) {
			if day.Weekday() == time.Sunday {
				clash = true
			}
			for _, other := range items {
				if other.name != run.name && other.day.Equal(day) {
					clash = true
				}
			}
		}
		if clash {
			day := next(last)
			taken[day.Format(dateLayout)] = true
			extra = append(extra, koreanHoliday{day, run.name + " 대체공휴일", never})
		}
	}
	items = append(items, extra...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].day.Before(items[j].day) })
	return items, true
}

func koreanHolidayRangeError() error {
	return formulaError("#N/A", fmt.Sprintf("한국 공휴일 표는 %d년부터 %d년까지 있습니다", koreanHolidayFirstYear, koreanHolidayLastYear))
}

// evaluateKoreanHolidays 는 한국 공휴일 함수다.
//
//	KOREANHOLIDAYS(연도)      그 해 공휴일을 날짜 배열로. NETWORKDAYS 의 휴일 인자에 그대로 쓴다.
//	KOREANHOLIDAYNAME(날짜)   그 날의 공휴일 이름. 공휴일이 아니면 빈 글자.
func evaluateKoreanHolidays(name string, values []any) (any, bool, error) {
	switch name {
	case "KOREANHOLIDAYS":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		year, err := integerValue(scalarOrFirst(values[0]), name)
		if err != nil {
			return nil, true, err
		}
		items, ok := koreanHolidays(year)
		if !ok {
			return nil, true, koreanHolidayRangeError()
		}
		dates := make([]any, 0, len(items))
		seen := make(map[string]bool, len(items))
		for _, item := range items {
			text := item.day.Format(dateLayout)
			if seen[text] {
				continue
			}
			seen[text] = true
			dates = append(dates, text)
		}
		return arrayValue{rows: len(dates), columns: 1, values: dates}, true, nil
	case "KOREANHOLIDAYNAME":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		moment, ok := parseDate(scalarOrFirst(values[0]))
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a date")
		}
		items, known := koreanHolidays(moment.Year())
		if !known {
			return nil, true, koreanHolidayRangeError()
		}
		names := make([]string, 0, 1)
		for _, item := range items {
			if item.day.Equal(time.Date(moment.Year(), moment.Month(), moment.Day(), 0, 0, 0, 0, time.UTC)) {
				names = append(names, item.name)
			}
		}
		if len(names) == 0 {
			return "", true, nil
		}
		return joinDistinct(names, "·"), true, nil
	}
	return nil, false, nil
}

func joinDistinct(items []string, separator string) string {
	out := ""
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		if seen[item] {
			continue
		}
		seen[item] = true
		if out != "" {
			out += separator
		}
		out += item
	}
	return out
}
