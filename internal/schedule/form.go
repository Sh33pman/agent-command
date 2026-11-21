package schedule

import (
	"slices"
	"strconv"
	"strings"
	"time"
)

// Form is a schedule as a form's controls set it: how often, which days,
// what time. The daemon maps it to and from cron, so no client does. Cron
// the controls can't show is Custom, and stays as typed.
type Form struct {
	Kind string `json:"kind"` // a Form* kind
	// Every is the step for FormMinutes and FormHours.
	Every  int `json:"every,omitempty"`
	Hour   int `json:"hour"`
	Minute int `json:"minute"`
	// WeekDays for FormWeekly (0 is Sunday), MonthDays for FormMonthly (1-31).
	WeekDays  []int `json:"week_days,omitempty"`
	MonthDays []int `json:"month_days,omitempty"`
	// Date is FormOnce's day, "2026-12-01", at Hour:Minute.
	Date string `json:"date,omitempty"`
	// Cron is FormCustom's, as typed.
	Cron string `json:"cron,omitempty"`
}

// Form kinds.
const (
	FormMinutes  = "minutes"
	FormHours    = "hours"
	FormDaily    = "daily"
	FormWeekdays = "weekdays"
	FormWeekly   = "weekly"
	FormMonthly  = "monthly"
	FormOnce     = "once"
	FormCustom   = "custom"
)

// Steps that divide the hour and the day evenly, so "every 15 minutes" is
// what it says (cron's */7 restarts each hour).
var (
	MinuteSteps = []int{1, 2, 3, 4, 5, 6, 10, 12, 15, 20, 30}
	HourSteps   = []int{1, 2, 3, 4, 6, 8, 12}
)

// Schedule is the form as a job's schedule: cron, or "once on … at …".
func (f Form) Schedule() string {
	at := strconv.Itoa(f.Minute) + " " + strconv.Itoa(f.Hour)
	join := func(days []int, fallback int) string {
		if len(days) == 0 {
			days = []int{fallback}
		}
		days = slices.Clone(days)
		slices.Sort(days)
		days = slices.Compact(days)
		parts := make([]string, len(days))
		for i, d := range days {
			parts[i] = strconv.Itoa(d)
		}
		return strings.Join(parts, ",")
	}
	switch f.Kind {
	case FormMinutes:
		if f.Every <= 1 {
			return "* * * * *"
		}
		return "*/" + strconv.Itoa(f.Every) + " * * * *"
	case FormHours:
		if f.Every <= 1 {
			return strconv.Itoa(f.Minute) + " * * * *"
		}
		return strconv.Itoa(f.Minute) + " */" + strconv.Itoa(f.Every) + " * * *"
	case FormDaily:
		return at + " * * *"
	case FormWeekdays:
		return at + " * * 1-5"
	case FormWeekly:
		return at + " * * " + join(f.WeekDays, 1)
	case FormMonthly:
		return at + " " + join(f.MonthDays, 1) + " * *"
	case FormOnce:
		return "once on " + f.Date + " at " + twoDigits(f.Hour) + ":" + twoDigits(f.Minute)
	default:
		return f.Cron
	}
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// FormOf is the form for a schedule, in loc: a one-off, cron the controls
// can show, or Custom with the cron as it is.
func FormOf(s Schedule, loc *time.Location) Form {
	if !s.Once.IsZero() {
		t := s.Once.In(loc)
		return Form{Kind: FormOnce, Date: t.Format("2006-01-02"), Hour: t.Hour(), Minute: t.Minute()}
	}
	fields := strings.Fields(s.Cron)
	if len(fields) == 5 {
		if f, ok := readCron(fields); ok {
			return f
		}
	}
	return Form{Kind: FormCustom, Cron: s.Cron}
}

// readCron is the form for five cron fields, when they fit one.
func readCron(f []string) (Form, bool) {
	minute, hour, dom, month, dow := f[0], f[1], f[2], f[3], f[4]
	if month != "*" {
		return Form{}, false
	}
	if hour == "*" && dom == "*" && dow == "*" {
		if minute == "*" {
			return Form{Kind: FormMinutes, Every: 1}, true
		}
		if n, ok := step(minute); ok && slices.Contains(MinuteSteps, n) {
			return Form{Kind: FormMinutes, Every: n}, true
		}
		if m, ok := num(minute, 0, 59); ok {
			return Form{Kind: FormHours, Every: 1, Minute: m}, true
		}
		return Form{}, false
	}
	m, ok := num(minute, 0, 59)
	if !ok {
		return Form{}, false
	}
	if dom == "*" && dow == "*" {
		if n, ok := step(hour); ok && slices.Contains(HourSteps, n) {
			return Form{Kind: FormHours, Every: n, Minute: m}, true
		}
	}
	h, ok := num(hour, 0, 23)
	if !ok {
		return Form{}, false
	}
	out := Form{Hour: h, Minute: m}
	switch {
	case dom == "*" && dow == "*":
		out.Kind = FormDaily
	case dom == "*" && dow == "1-5":
		out.Kind = FormWeekdays
	case dom == "*":
		days, ok := numList(dow, 0, 7)
		if !ok {
			return Form{}, false
		}
		for i, d := range days {
			if d == 7 {
				days[i] = 0
			}
		}
		slices.Sort(days)
		out.Kind, out.WeekDays = FormWeekly, slices.Compact(days)
	case dow == "*":
		days, ok := numList(dom, 1, 31)
		if !ok {
			return Form{}, false
		}
		out.Kind, out.MonthDays = FormMonthly, days
	default:
		return Form{}, false
	}
	return out, true
}

func num(s string, lo, hi int) (int, bool) {
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= lo && n <= hi
}

func step(s string) (int, bool) {
	rest, ok := strings.CutPrefix(s, "*/")
	if !ok {
		return 0, false
	}
	n, err := strconv.Atoi(rest)
	return n, err == nil
}

// numList reads "1,3,5", "1-5" or a mix.
func numList(s string, lo, hi int) ([]int, bool) {
	var out []int
	for part := range strings.SplitSeq(s, ",") {
		a, b, isRange := strings.Cut(part, "-")
		x, ok := num(a, lo, hi)
		if !ok {
			return nil, false
		}
		if !isRange {
			out = append(out, x)
			continue
		}
		y, ok := num(b, lo, hi)
		if !ok || y < x {
			return nil, false
		}
		for i := x; i <= y; i++ {
			out = append(out, i)
		}
	}
	return out, len(out) > 0
}
