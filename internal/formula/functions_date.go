package formula

import (
	"math"
	"strings"
	"time"
)

const dateLayout = "2006-01-02"

// evaluateDate covers the calendar functions. Dates are text in kanpic, so
// every result that is a date is returned in the same yyyy-MM-dd form that
// DATE and TODAY produce.
func evaluateDate(name string, values []any) (any, bool, error) {
	switch name {
	case "EDATE", "EOMONTH":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		moment, ok := parseDate(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a date")
		}
		months, err := integerValue(values[1], name)
		if err != nil {
			return nil, true, err
		}
		shifted := time.Date(moment.Year(), moment.Month()+time.Month(months), 1, 0, 0, 0, 0, time.UTC)
		if name == "EOMONTH" {
			return shifted.AddDate(0, 1, -1).Format(dateLayout), true, nil
		}
		day := moment.Day()
		if last := shifted.AddDate(0, 1, -1).Day(); day > last {
			day = last
		}
		return shifted.AddDate(0, 0, day-1).Format(dateLayout), true, nil
	case "DAYS":
		if len(values) != 2 {
			return nil, true, argError(name)
		}
		end, endOK := parseDate(values[0])
		start, startOK := parseDate(values[1])
		if !endOK || !startOK {
			return nil, true, formulaError("#VALUE!", "DAYS requires two dates")
		}
		return math.Trunc(end.Sub(start).Hours() / 24), true, nil
	case "DAYS360":
		if len(values) < 2 || len(values) > 3 {
			return nil, true, argError(name)
		}
		start, startOK := parseDate(values[0])
		end, endOK := parseDate(values[1])
		if !startOK || !endOK {
			return nil, true, formulaError("#VALUE!", "DAYS360 requires two dates")
		}
		startDay, endDay := min(start.Day(), 30), min(end.Day(), 30)
		return float64((end.Year()-start.Year())*360 + (int(end.Month())-int(start.Month()))*30 + endDay - startDay), true, nil
	case "DATEDIF":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		start, startOK := parseDate(values[0])
		end, endOK := parseDate(values[1])
		if !startOK || !endOK {
			return nil, true, formulaError("#VALUE!", "DATEDIF requires two dates")
		}
		if end.Before(start) {
			return nil, true, formulaError("#NUM!", "DATEDIF needs the earlier date first")
		}
		return datedDifference(start, end, strings.ToUpper(strings.TrimSpace(display(values[2]))))
	case "YEARFRAC":
		if len(values) < 2 || len(values) > 3 {
			return nil, true, argError(name)
		}
		start, startOK := parseDate(values[0])
		end, endOK := parseDate(values[1])
		if !startOK || !endOK {
			return nil, true, formulaError("#VALUE!", "YEARFRAC requires two dates")
		}
		if end.Before(start) {
			start, end = end, start
		}
		basis := 0
		if len(values) == 3 && !omitted(values[2]) {
			supplied, err := integerValue(values[2], name)
			if err != nil {
				return nil, true, err
			}
			basis = supplied
		}
		return yearFraction(start, end, basis), true, nil
	case "NETWORKDAYS", "WORKDAY":
		if len(values) < 2 {
			return nil, true, argError(name)
		}
		start, startOK := parseDate(values[0])
		if !startOK {
			return nil, true, formulaError("#VALUE!", name+" requires a start date")
		}
		holidays := make(map[string]struct{}, len(values))
		for _, value := range values[2:] {
			if moment, ok := parseDate(value); ok {
				holidays[moment.Format(dateLayout)] = struct{}{}
			}
		}
		if name == "NETWORKDAYS" {
			end, endOK := parseDate(values[1])
			if !endOK {
				return nil, true, formulaError("#VALUE!", "NETWORKDAYS requires an end date")
			}
			sign := 1.0
			if end.Before(start) {
				start, end, sign = end, start, -1
			}
			count := 0
			for day := start; !day.After(end); day = day.AddDate(0, 0, 1) {
				if isWorkday(day, holidays) {
					count++
				}
			}
			return sign * float64(count), true, nil
		}
		days, err := integerValue(values[1], name)
		if err != nil {
			return nil, true, err
		}
		step := 1
		if days < 0 {
			step, days = -1, -days
		}
		day := start
		for remaining := days; remaining > 0; {
			day = day.AddDate(0, 0, step)
			if isWorkday(day, holidays) {
				remaining--
			}
		}
		return day.Format(dateLayout), true, nil
	case "WEEKNUM", "ISOWEEKNUM":
		if len(values) < 1 || len(values) > 2 {
			return nil, true, argError(name)
		}
		moment, ok := parseDate(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a date")
		}
		if name == "ISOWEEKNUM" {
			_, week := moment.ISOWeek()
			return float64(week), true, nil
		}
		start := time.Date(moment.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
		offset := int(start.Weekday())
		return float64((moment.YearDay()+offset-1)/7 + 1), true, nil
	case "TIME":
		if len(values) != 3 {
			return nil, true, argError(name)
		}
		hours, err := integerValue(values[0], name)
		if err != nil {
			return nil, true, err
		}
		minutes, err := integerValue(values[1], name)
		if err != nil {
			return nil, true, err
		}
		seconds, err := integerValue(values[2], name)
		if err != nil {
			return nil, true, err
		}
		total := ((hours*3600 + minutes*60 + seconds) % 86400 + 86400) % 86400
		return time.Date(2000, 1, 1, 0, 0, total, 0, time.UTC).Format("15:04:05"), true, nil
	case "HOUR", "MINUTE", "SECOND":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		moment, ok := parseTime(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", name+" requires a time")
		}
		switch name {
		case "HOUR":
			return float64(moment.Hour()), true, nil
		case "MINUTE":
			return float64(moment.Minute()), true, nil
		}
		return float64(moment.Second()), true, nil
	case "DATEVALUE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		moment, ok := parseDate(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "DATEVALUE requires a date")
		}
		return moment.Format(dateLayout), true, nil
	case "TIMEVALUE":
		if len(values) != 1 {
			return nil, true, argError(name)
		}
		moment, ok := parseTime(values[0])
		if !ok {
			return nil, true, formulaError("#VALUE!", "TIMEVALUE requires a time")
		}
		return float64(moment.Hour()*3600+moment.Minute()*60+moment.Second()) / 86400, true, nil
	}
	return nil, false, nil
}

func datedDifference(start, end time.Time, unit string) (any, bool, error) {
	switch unit {
	case "D":
		return math.Trunc(end.Sub(start).Hours() / 24), true, nil
	case "M", "Y", "YM":
		months := (end.Year()-start.Year())*12 + int(end.Month()) - int(start.Month())
		if end.Day() < start.Day() {
			months--
		}
		switch unit {
		case "M":
			return float64(months), true, nil
		case "Y":
			return float64(months / 12), true, nil
		}
		return float64(months % 12), true, nil
	case "MD":
		day := end.Day() - start.Day()
		if day < 0 {
			previous := time.Date(end.Year(), end.Month(), 0, 0, 0, 0, 0, time.UTC)
			day += previous.Day()
		}
		return float64(day), true, nil
	case "YD":
		anniversary := time.Date(end.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
		if anniversary.After(end) {
			anniversary = anniversary.AddDate(-1, 0, 0)
		}
		return math.Trunc(end.Sub(anniversary).Hours() / 24), true, nil
	}
	return nil, true, formulaError("#NUM!", "DATEDIF unit must be one of Y, M, D, MD, YM, YD")
}

func isWorkday(day time.Time, holidays map[string]struct{}) bool {
	if day.Weekday() == time.Saturday || day.Weekday() == time.Sunday {
		return false
	}
	_, holiday := holidays[day.Format(dateLayout)]
	return !holiday
}

func yearFraction(start, end time.Time, basis int) float64 {
	days := end.Sub(start).Hours() / 24
	switch basis {
	case 1:
		// Actual/actual, approximated by the average year length in the span.
		years := float64(end.Year()-start.Year()) + 1
		total := 0.0
		for year := start.Year(); year <= end.Year(); year++ {
			total += float64(time.Date(year, 12, 31, 0, 0, 0, 0, time.UTC).YearDay())
		}
		return days / (total / years)
	case 2:
		return days / 360
	case 3:
		return days / 365
	case 4:
		startDay, endDay := min(start.Day(), 30), min(end.Day(), 30)
		return float64((end.Year()-start.Year())*360+(int(end.Month())-int(start.Month()))*30+endDay-startDay) / 360
	}
	startDay, endDay := start.Day(), end.Day()
	if startDay == 31 {
		startDay = 30
	}
	if endDay == 31 && startDay >= 30 {
		endDay = 30
	}
	return float64((end.Year()-start.Year())*360+(int(end.Month())-int(start.Month()))*30+endDay-startDay) / 360
}

func parseTime(value any) (time.Time, bool) {
	if moment, ok := parseDate(value); ok {
		return moment, true
	}
	text := strings.TrimSpace(display(value))
	for _, layout := range []string{"15:04:05", "15:04"} {
		if moment, err := time.Parse(layout, text); err == nil {
			return moment, true
		}
	}
	if fraction, ok := toNumber(value); ok && fraction >= 0 && fraction < 1 {
		return time.Date(2000, 1, 1, 0, 0, int(math.Round(fraction*86400)), 0, time.UTC), true
	}
	return time.Time{}, false
}

// extremeDate finds the earliest or latest date among values that are all
// dates, which is what MIN and MAX fall back to when a column holds no numbers.
func extremeDate(values []any, earliest bool) (string, bool) {
	var best time.Time
	found := false
	for _, value := range values {
		if value == nil || display(value) == "" {
			continue
		}
		moment, ok := parseDate(value)
		if !ok {
			return "", false
		}
		if !found || (earliest && moment.Before(best)) || (!earliest && moment.After(best)) {
			best, found = moment, true
		}
	}
	if !found {
		return "", false
	}
	return best.Format(dateLayout), true
}
