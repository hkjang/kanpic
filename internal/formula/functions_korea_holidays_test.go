package formula

import (
	"strings"
	"testing"
)

func holidayNames(t *testing.T, year int) map[string]string {
	t.Helper()
	items, ok := koreanHolidays(year)
	if !ok {
		t.Fatalf("%d년 표가 없다", year)
	}
	out := make(map[string]string, len(items))
	for _, item := range items {
		key := item.day.Format(dateLayout)
		if out[key] != "" {
			out[key] += "·"
		}
		out[key] += item.name
	}
	return out
}

// 실제 달력과 맞춰 본다. 대체공휴일은 법이 바뀐 시점에 따라 다르므로, 규칙이
// 바뀌기 전후의 해를 함께 본다.
func TestKoreanSubstituteHolidaysFollowTheLawOfEachYear(t *testing.T) {
	t.Parallel()
	cases := []struct {
		year     int
		expected map[string]string // 있어야 하는 날 → 이름의 일부
		absent   []string          // 없어야 하는 날
	}{
		{2020, map[string]string{"2020-01-24": "설날", "2020-01-27": "설날 대체공휴일", "2020-04-30": "부처님오신날", "2020-08-17": "임시공휴일", "2020-10-01": "추석"}, []string{"2020-08-14", "2020-08-16"}},
		{2021, map[string]string{"2021-08-16": "광복절 대체공휴일", "2021-10-04": "개천절 대체공휴일", "2021-10-11": "한글날 대체공휴일"}, []string{"2021-12-27"}},
		{2022, map[string]string{"2022-03-09": "대통령선거", "2022-09-12": "추석 대체공휴일", "2022-10-10": "한글날 대체공휴일"}, []string{"2022-12-26"}},
		{2023, map[string]string{"2023-01-24": "설날 대체공휴일", "2023-05-29": "부처님오신날 대체공휴일", "2023-10-02": "임시공휴일"}, []string{"2023-10-04"}},
		{2024, map[string]string{"2024-02-12": "설날 대체공휴일", "2024-04-10": "국회의원선거", "2024-05-06": "어린이날 대체공휴일", "2024-10-01": "국군의날"}, []string{"2024-09-19"}},
		{2025, map[string]string{"2025-01-27": "임시공휴일", "2025-03-03": "삼일절 대체공휴일", "2025-05-05": "어린이날·부처님오신날", "2025-05-06": "어린이날 대체공휴일", "2025-06-03": "대통령선거", "2025-10-08": "추석 대체공휴일"}, []string{"2025-05-07"}},
		{2026, map[string]string{"2026-03-02": "삼일절 대체공휴일", "2026-05-25": "부처님오신날 대체공휴일", "2026-06-03": "지방선거", "2026-08-17": "광복절 대체공휴일", "2026-10-05": "개천절 대체공휴일"}, []string{"2026-09-28", "2026-02-19"}},
		{2028, map[string]string{"2028-10-03": "개천절·추석", "2028-10-05": "추석 대체공휴일"}, []string{"2028-10-06"}},
	}
	for _, item := range cases {
		names := holidayNames(t, item.year)
		for day, want := range item.expected {
			if got := names[day]; !strings.Contains(got, want) {
				t.Errorf("%s: %q, %q 가 있어야 한다", day, got, want)
			}
		}
		for _, day := range item.absent {
			if got := names[day]; got != "" {
				t.Errorf("%s: %q 는 공휴일이 아니어야 한다", day, got)
			}
		}
	}
}

// 2025년 10월 첫 열흘: 개천절, 추석 연휴(5~7일), 대체공휴일(8일), 한글날. 평일은 1·2·10일 셋뿐이다.
func TestKoreanHolidaysFeedNetworkdays(t *testing.T) {
	t.Parallel()
	for formula, expected := range map[string]any{
		`=NETWORKDAYS("2025-10-01","2025-10-10",KOREANHOLIDAYS(2025))`:        3.0,
		`=NETWORKDAYS.INTL("2025-10-01","2025-10-10",1,KOREANHOLIDAYS(2025))`: 3.0,
		`=WORKDAY("2025-10-02",1,KOREANHOLIDAYS(2025))`:                       "2025-10-10",
		`=KOREANHOLIDAYNAME("2025-10-08")`:                                    "추석 대체공휴일",
		`=KOREANHOLIDAYNAME("2025-10-10")`:                                    "",
		`=ROWS(KOREANHOLIDAYS(2026))`:                                         nil, // 개수만 존재하면 된다
	} {
		result := New().Evaluate(formula, nil)
		if result.Error != nil {
			t.Errorf("%s: %v", formula, result.Error)
			continue
		}
		if expected != nil && result.Value != expected {
			t.Errorf("%s = %#v, want %#v", formula, result.Value, expected)
		}
	}
}

func TestKoreanHolidaysRefuseYearsOutsideTheTable(t *testing.T) {
	t.Parallel()
	for _, formula := range []string{`=KOREANHOLIDAYS(2019)`, `=KOREANHOLIDAYS(2031)`, `=KOREANHOLIDAYNAME("2035-01-01")`} {
		result := New().Evaluate(formula, nil)
		if result.Error == nil || !strings.Contains(result.Error.Error(), "#N/A") {
			t.Errorf("%s: %v, #N/A 여야 한다", formula, result.Error)
		}
	}
}
