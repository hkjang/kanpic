package automation

import (
	"errors"
	"testing"
	"time"
)

func TestScheduleNextSupportsStepsRangesNamesAndTimezone(t *testing.T) {
	schedule, err := ParseSchedule("*/15 9-17 * * MON-FRI", "Asia/Seoul")
	if err != nil {
		t.Fatal(err)
	}
	after := time.Date(2026, time.August, 3, 8, 59, 30, 0, time.FixedZone("KST", 9*60*60))
	next, err := schedule.Next(after)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.August, 3, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) || schedule.Expression != "*/15 9-17 * * MON-FRI" || schedule.Timezone != "Asia/Seoul" {
		t.Fatalf("next=%s schedule=%#v", next, schedule)
	}
	next, err = schedule.Next(time.Date(2026, time.August, 3, 8, 59, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	want = time.Date(2026, time.August, 4, 0, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next workday=%s, want %s", next, want)
	}
}

func TestScheduleAliasesLeapDayAndCronDayOrSemantics(t *testing.T) {
	daily, err := ParseSchedule("@daily", "UTC")
	if err != nil || daily.Expression != "0 0 * * *" {
		t.Fatalf("daily=%#v, %v", daily, err)
	}
	leap, err := ParseSchedule("0 9 29 FEB *", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	next, err := leap.Next(time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !next.Equal(time.Date(2028, 2, 29, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("leap next=%s, %v", next, err)
	}
	orSchedule, err := ParseSchedule("0 9 1 * MON", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	next, err = orSchedule.Next(time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC))
	if err != nil || !next.Equal(time.Date(2026, 8, 3, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("cron OR next=%s, %v", next, err)
	}
	monthName, err := ParseSchedule("0 9 * JAN MON", "UTC")
	if err != nil {
		t.Fatal(err)
	}
	next, err = monthName.Next(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || !next.Equal(time.Date(2026, 1, 5, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("named month next=%s, %v", next, err)
	}
}

func TestScheduleSkipsNonexistentDSTWallTime(t *testing.T) {
	schedule, err := ParseSchedule("30 2 * * *", "America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	next, err := schedule.Next(time.Date(2026, 3, 8, 5, 0, 0, 0, time.UTC))
	want := time.Date(2026, 3, 9, 6, 30, 0, 0, time.UTC)
	if err != nil || !next.Equal(want) {
		t.Fatalf("DST next=%s, want %s, %v", next, want, err)
	}
}

func TestScheduleRejectsInvalidExpressions(t *testing.T) {
	for _, test := range []struct{ expression, timezone string }{
		{"* * * *", "UTC"},
		{"60 * * * *", "UTC"},
		{"*/0 * * * *", "UTC"},
		{"0 9 * * FUNDAY", "UTC"},
		{"0 9 * * *", "Mars/Base"},
	} {
		if _, err := ParseSchedule(test.expression, test.timezone); !errors.Is(err, ErrInvalid) {
			t.Fatalf("ParseSchedule(%q,%q) error=%v", test.expression, test.timezone, err)
		}
	}
}
