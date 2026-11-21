package schedule

import (
	"reflect"
	"testing"
	"time"
)

func TestFormRoundTrips(t *testing.T) {
	loc := time.UTC
	cases := []struct {
		form Form
		cron string
	}{
		{Form{Kind: FormMinutes, Every: 1}, "* * * * *"},
		{Form{Kind: FormMinutes, Every: 15}, "*/15 * * * *"},
		{Form{Kind: FormHours, Every: 1, Minute: 5}, "5 * * * *"},
		{Form{Kind: FormHours, Every: 6, Minute: 30}, "30 */6 * * *"},
		{Form{Kind: FormDaily, Hour: 9, Minute: 0}, "0 9 * * *"},
		{Form{Kind: FormWeekdays, Hour: 8, Minute: 30}, "30 8 * * 1-5"},
		{Form{Kind: FormWeekly, Hour: 9, WeekDays: []int{0, 3}}, "0 9 * * 0,3"},
		{Form{Kind: FormMonthly, Hour: 7, MonthDays: []int{1, 15}}, "0 7 1,15 * *"},
	}
	for _, c := range cases {
		if got := c.form.Schedule(); got != c.cron {
			t.Errorf("%+v: schedule %q, want %q", c.form, got, c.cron)
		}
		if got := FormOf(Schedule{Cron: c.cron}, loc); !reflect.DeepEqual(got, c.form) {
			t.Errorf("%q: form %+v, want %+v", c.cron, got, c.form)
		}
	}
}

func TestFormEdges(t *testing.T) {
	loc := time.UTC
	if got := (Form{Kind: FormWeekly, Hour: 9}).Schedule(); got != "0 9 * * 1" {
		t.Errorf("no days: %q", got)
	}
	if got := (Form{Kind: FormMonthly, MonthDays: []int{15, 1, 15}}).Schedule(); got != "0 0 1,15 * *" {
		t.Errorf("unsorted: %q", got)
	}
	if got := (Form{Kind: FormCustom, Cron: "0 9 1 1 *"}).Schedule(); got != "0 9 1 1 *" {
		t.Errorf("custom: %q", got)
	}
	once := Form{Kind: FormOnce, Date: "2026-12-01", Hour: 14, Minute: 5}
	if got := once.Schedule(); got != "once on 2026-12-01 at 14:05" {
		t.Errorf("once: %q", got)
	}
	if got := FormOf(Schedule{Once: time.Date(2026, 12, 1, 14, 5, 0, 0, loc)}, loc); !reflect.DeepEqual(got, once) {
		t.Errorf("once back: %+v", got)
	}
	// Cron the controls can't show stays as it is.
	for _, cron := range []string{"0 9 1 1 *", "*/7 * * * *", "0 9 1 * 1", "x 9 * * *", "0 25 * * *", "0 9 * * 9", "0 9 5-2 * *",
		"0 */5 * * *", "0 9 1-x * *", "1,2 * * * *", "*/x * * * *", "0 9 * *"} {
		if got := FormOf(Schedule{Cron: cron}, loc); got.Kind != FormCustom || got.Cron != cron {
			t.Errorf("%q: %+v", cron, got)
		}
	}
	if got := FormOf(Schedule{Cron: "0 9 * * 7"}, loc); !reflect.DeepEqual(got.WeekDays, []int{0}) {
		t.Errorf("Sunday as 7: %+v", got)
	}
	if got := FormOf(Schedule{Cron: "0 9 * * 1-3"}, loc); !reflect.DeepEqual(got.WeekDays, []int{1, 2, 3}) {
		t.Errorf("a range: %+v", got)
	}
}
