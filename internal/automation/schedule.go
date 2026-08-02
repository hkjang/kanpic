package automation

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

const maxScheduleLookaheadYears = 8

var cronAliases = map[string]string{
	"@hourly":  "0 * * * *",
	"@daily":   "0 0 * * *",
	"@weekly":  "0 0 * * 0",
	"@monthly": "0 0 1 * *",
	"@yearly":  "0 0 1 1 *",
}

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var weekdayNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

type cronField struct {
	allowed  []bool
	wildcard bool
}

type Schedule struct {
	Expression string
	Timezone   string
	location   *time.Location
	minute     cronField
	hour       cronField
	day        cronField
	month      cronField
	weekday    cronField
}

func ParseSchedule(expression, timezone string) (*Schedule, error) {
	expression = strings.TrimSpace(expression)
	if alias, ok := cronAliases[strings.ToLower(expression)]; ok {
		expression = alias
	}
	parts := strings.Fields(expression)
	if len(parts) != 5 {
		return nil, fmt.Errorf("%w: schedule cron must contain five fields", ErrInvalid)
	}
	timezone = strings.TrimSpace(timezone)
	if timezone == "" {
		timezone = "UTC"
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, fmt.Errorf("%w: unknown schedule timezone %q", ErrInvalid, timezone)
	}
	minute, err := parseCronField(parts[0], 0, 59, nil, false)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid schedule minute: %v", ErrInvalid, err)
	}
	hour, err := parseCronField(parts[1], 0, 23, nil, false)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid schedule hour: %v", ErrInvalid, err)
	}
	day, err := parseCronField(parts[2], 1, 31, nil, false)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid schedule day: %v", ErrInvalid, err)
	}
	month, err := parseCronField(parts[3], 1, 12, monthNames, false)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid schedule month: %v", ErrInvalid, err)
	}
	weekday, err := parseCronField(parts[4], 0, 7, weekdayNames, true)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid schedule weekday: %v", ErrInvalid, err)
	}
	return &Schedule{Expression: strings.Join(parts, " "), Timezone: timezone, location: location, minute: minute, hour: hour, day: day, month: month, weekday: weekday}, nil
}

func (s *Schedule) Next(after time.Time) (time.Time, error) {
	localAfter := after.In(s.location)
	start := localAfter.Truncate(time.Minute).Add(time.Minute)
	limit := start.AddDate(maxScheduleLookaheadYears, 0, 0)
	for date := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, s.location); date.Before(limit); date = time.Date(date.Year(), date.Month(), date.Day()+1, 0, 0, 0, 0, s.location) {
		if !s.month.allowed[int(date.Month())] || !s.matchesDay(date) {
			continue
		}
		for hour := 0; hour <= 23; hour++ {
			if !s.hour.allowed[hour] {
				continue
			}
			for minute := 0; minute <= 59; minute++ {
				if !s.minute.allowed[minute] {
					continue
				}
				candidate := time.Date(date.Year(), date.Month(), date.Day(), hour, minute, 0, 0, s.location)
				local := candidate.In(s.location)
				if local.Year() != date.Year() || local.Month() != date.Month() || local.Day() != date.Day() || local.Hour() != hour || local.Minute() != minute {
					continue
				}
				if !candidate.Before(start) && candidate.After(after) {
					return candidate.UTC(), nil
				}
			}
		}
	}
	return time.Time{}, fmt.Errorf("%w: schedule has no occurrence within %d years", ErrInvalid, maxScheduleLookaheadYears)
}

func (s *Schedule) matchesDay(date time.Time) bool {
	dayMatch := s.day.allowed[date.Day()]
	weekdayMatch := s.weekday.allowed[int(date.Weekday())]
	switch {
	case s.day.wildcard && s.weekday.wildcard:
		return true
	case s.day.wildcard:
		return weekdayMatch
	case s.weekday.wildcard:
		return dayMatch
	default:
		return dayMatch || weekdayMatch
	}
}

func parseCronField(raw string, minimum, maximum int, names map[string]int, sundaySeven bool) (cronField, error) {
	field := cronField{allowed: make([]bool, maximum+1), wildcard: raw == "*"}
	for _, segment := range strings.Split(strings.ToUpper(raw), ",") {
		segment = strings.TrimSpace(segment)
		if segment == "" {
			return cronField{}, fmt.Errorf("empty list item")
		}
		base, stepRaw, hasStep := strings.Cut(segment, "/")
		step := 1
		if hasStep {
			if strings.Contains(stepRaw, "/") {
				return cronField{}, fmt.Errorf("too many step separators")
			}
			value, err := strconv.Atoi(stepRaw)
			if err != nil || value < 1 || value > maximum-minimum+1 {
				return cronField{}, fmt.Errorf("step must be between 1 and %d", maximum-minimum+1)
			}
			step = value
		}
		start, end := minimum, maximum
		switch {
		case base == "*":
		case strings.Contains(base, "-"):
			left, right, ok := strings.Cut(base, "-")
			if !ok || strings.Contains(right, "-") {
				return cronField{}, fmt.Errorf("invalid range %q", base)
			}
			var err error
			start, err = cronValue(left, minimum, maximum, names)
			if err != nil {
				return cronField{}, err
			}
			end, err = cronValue(right, minimum, maximum, names)
			if err != nil {
				return cronField{}, err
			}
			if start > end {
				return cronField{}, fmt.Errorf("range start exceeds end")
			}
		default:
			value, err := cronValue(base, minimum, maximum, names)
			if err != nil {
				return cronField{}, err
			}
			start = value
			if !hasStep {
				end = value
			}
		}
		for value := start; value <= end; value += step {
			index := value
			if sundaySeven && value == 7 {
				index = 0
			}
			field.allowed[index] = true
		}
	}
	for _, allowed := range field.allowed {
		if allowed {
			return field, nil
		}
	}
	return cronField{}, fmt.Errorf("field selects no values")
}

func cronValue(raw string, minimum, maximum int, names map[string]int) (int, error) {
	if value, ok := names[raw]; ok {
		return value, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("value %q must be between %d and %d", raw, minimum, maximum)
	}
	return value, nil
}
