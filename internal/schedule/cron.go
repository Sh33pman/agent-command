// Package schedule is when scheduled jobs run: five-field cron expressions, a
// small way of saying them in words that translates both ways, and the times
// they next run.
package schedule

import (
	"errors"
	"fmt"
	"math/bits"
	"strconv"
	"strings"
	"time"
)

// Cron is a parsed five-field cron expression: minute, hour, day of the month,
// month and day of the week. Each field is a bit set of the values it matches.
type Cron struct {
	Minute, Hour, Dom, Month, Dow uint64
	// DomStar and DowStar say the field was "*". As in Vixie cron, when both day
	// fields are restricted a day matches either of them.
	DomStar, DowStar bool
}

type field struct {
	name     string
	min, max int
	names    map[string]int
}

var (
	minuteField = field{name: "minute", min: 0, max: 59}
	hourField   = field{name: "hour", min: 0, max: 23}
	domField    = field{name: "day of the month", min: 1, max: 31}
	monthField  = field{name: "month", min: 1, max: 12, names: map[string]int{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12}}
	// Day 7 is Sunday too, as in most crons.
	dowField = field{name: "day of the week", min: 0, max: 7, names: map[string]int{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6}}
)

// ParseCron parses a five-field cron expression. Each field takes *, numbers,
// ranges (1-5), steps (*/15, 0-30/10) and lists (1,15); months and days of the
// week also take their three-letter English names.
func ParseCron(expr string) (Cron, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return Cron{}, fmt.Errorf("cron needs 5 fields (minute hour day month weekday), got %d", len(fields))
	}
	var c Cron
	var err error
	if c.Minute, _, err = minuteField.parse(fields[0]); err != nil {
		return Cron{}, err
	}
	if c.Hour, _, err = hourField.parse(fields[1]); err != nil {
		return Cron{}, err
	}
	if c.Dom, c.DomStar, err = domField.parse(fields[2]); err != nil {
		return Cron{}, err
	}
	if c.Month, _, err = monthField.parse(fields[3]); err != nil {
		return Cron{}, err
	}
	if c.Dow, c.DowStar, err = dowField.parse(fields[4]); err != nil {
		return Cron{}, err
	}
	if c.Dow&(1<<7) != 0 { // 7 is Sunday
		c.Dow = c.Dow&^(1<<7) | 1
	}
	// A field listing every value is "*", so the canonical form reads back the same.
	c.DomStar = c.DomStar || c.Dom == fullSet(domField)
	c.DowStar = c.DowStar || c.Dow == fullSet(dowField)
	if !c.DomStar && c.DowStar && !c.canEverMatchDom() {
		return Cron{}, errors.New("that day of the month never comes in those months")
	}
	return c, nil
}

// canEverMatchDom reports whether some chosen month has some chosen day (so
// "30 2" for 30 February is refused rather than never running).
func (c Cron) canEverMatchDom() bool {
	days := [13]int{0, 31, 29, 31, 30, 31, 30, 31, 31, 30, 31, 30, 31}
	for m := 1; m <= 12; m++ {
		if c.Month&(1<<m) == 0 {
			continue
		}
		for d := 1; d <= days[m]; d++ {
			if c.Dom&(1<<d) != 0 {
				return true
			}
		}
	}
	return false
}

// parse turns one field into a bit set; star says it was "*" (or "*/1").
func (f field) parse(s string) (set uint64, star bool, err error) {
	if s == "" {
		return 0, false, fmt.Errorf("the %s is empty", f.name)
	}
	star = s == "*" || s == "*/1" || s == "?"
	for _, part := range strings.Split(s, ",") {
		bitsOf, err := f.parsePart(part)
		if err != nil {
			return 0, false, err
		}
		set |= bitsOf
	}
	return set, star, nil
}

func (f field) parsePart(part string) (uint64, error) {
	if part == "" {
		return 0, fmt.Errorf("the %s has an empty item", f.name)
	}
	rangePart, stepPart, hasStep := strings.Cut(part, "/")
	step := 1
	if hasStep {
		n, err := strconv.Atoi(stepPart)
		if err != nil || n < 1 || n > f.max {
			return 0, fmt.Errorf("bad step %q in the %s", stepPart, f.name)
		}
		step = n
	}
	lo, hi := f.min, f.max
	if f.name == dowField.name && (rangePart == "*" || rangePart == "?") {
		hi = 6 // * is every day once, not Sunday twice
	}
	switch {
	case rangePart == "*" || rangePart == "?":
	case strings.Contains(rangePart, "-"):
		a, b, _ := strings.Cut(rangePart, "-")
		var err error
		if lo, err = f.value(a); err != nil {
			return 0, err
		}
		if hi, err = f.value(b); err != nil {
			return 0, err
		}
		if lo > hi {
			return 0, fmt.Errorf("range %q in the %s goes backwards", rangePart, f.name)
		}
	default:
		v, err := f.value(rangePart)
		if err != nil {
			return 0, err
		}
		lo = v
		hi = v
		if hasStep { // "5/15" means from 5 to the end, every 15
			hi = f.max
		}
	}
	var set uint64
	for v := lo; v <= hi; v += step {
		set |= 1 << v
	}
	return set, nil
}

func (f field) value(s string) (int, error) {
	if v, ok := f.names[strings.ToLower(s)]; ok {
		return v, nil
	}
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%q isn't a valid %s", s, f.name)
	}
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("the %s %d is out of range (%d-%d)", f.name, v, f.min, f.max)
	}
	return v, nil
}

// dayMatches applies cron's day rule: with both day fields restricted, either one.
func (c Cron) dayMatches(t time.Time) bool {
	dom := c.Dom&(1<<t.Day()) != 0
	dow := c.Dow&(1<<int(t.Weekday())) != 0
	switch {
	case c.DomStar && c.DowStar:
		return true
	case c.DomStar:
		return dow
	case c.DowStar:
		return dom
	default:
		return dom || dow
	}
}

// searchYears bounds the search for the next run.
const searchYears = 5

// Next is the first time after t that c matches, in t's location. It's the zero
// time if there's none within five years.
//
// It goes by the wall clock, as cron does. A time the clock skips when it goes
// forward (02:30 that night) runs when the clock gets past it, and a time it
// repeats when it goes back runs once.
func (c Cron) Next(t time.Time) time.Time {
	loc := t.Location()
	// The wall clock's fields, as a time with no clock changes.
	w := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC).Add(time.Minute)
	end := w.AddDate(searchYears, 0, 0)
	for w.Before(end) {
		w = c.nextWall(w, end)
		if w.IsZero() {
			break
		}
		if at := onClock(w, loc); at.After(t) {
			return at
		}
		w = w.Add(time.Minute)
	}
	return time.Time{}
}

// onClock is when loc's clock shows wall time w. For a time the clock skips,
// it's the moment the clock jumps past it.
func onClock(w time.Time, loc *time.Location) time.Time {
	at := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), 0, 0, loc)
	if at.Hour() == w.Hour() && at.Minute() == w.Minute() {
		return at
	}
	// Go picks a time before the jump; walk to the first minute after it.
	for i := 0; i < 24*60; i++ {
		at = at.Add(time.Minute)
		shown := time.Date(at.Year(), at.Month(), at.Day(), at.Hour(), at.Minute(), 0, 0, time.UTC)
		if shown.After(w) {
			return at.Truncate(time.Minute)
		}
	}
	return at
}

// nextWall is the first wall-clock time from w (in UTC, which has no clock
// changes) that c matches.
func (c Cron) nextWall(w, end time.Time) time.Time {
	for w.Before(end) {
		switch {
		case c.Month&(1<<int(w.Month())) == 0:
			w = time.Date(w.Year(), w.Month()+1, 1, 0, 0, 0, 0, time.UTC)
		case !c.dayMatches(w):
			w = time.Date(w.Year(), w.Month(), w.Day()+1, 0, 0, 0, 0, time.UTC)
		case c.Hour&(1<<w.Hour()) == 0:
			w = w.Truncate(time.Hour).Add(time.Hour)
		case c.Minute&(1<<w.Minute()) == 0:
			w = w.Add(time.Minute)
		default:
			return w
		}
	}
	return time.Time{}
}

// String is the canonical form of the expression.
func (c Cron) String() string {
	return strings.Join([]string{
		formatField(c.Minute, minuteField, false),
		formatField(c.Hour, hourField, false),
		formatField(c.Dom, domField, c.DomStar),
		formatField(c.Month, monthField, false),
		formatField(c.Dow, dowField, c.DowStar),
	}, " ")
}

// formatField writes a set back as cron: *, */n, ranges and lists.
func formatField(set uint64, f field, star bool) string {
	hi := f.max
	if f.name == dowField.name {
		hi = 6
	}
	full := uint64(0)
	for v := f.min; v <= hi; v++ {
		full |= 1 << v
	}
	if star || set == full {
		return "*"
	}
	// */n: every nth value from the start, and nothing else. Only for minutes and
	// hours, or three values or more: "0,6" says weekends better than "*/6".
	if f.name == minuteField.name || f.name == hourField.name || bits.OnesCount64(set) >= 3 {
		for step := 2; step <= hi; step++ {
			var want uint64
			for v := f.min; v <= hi; v += step {
				want |= 1 << v
			}
			if set == want && bits.OnesCount64(want) > 1 {
				return "*/" + strconv.Itoa(step)
			}
		}
	}
	var parts []string
	for v := f.min; v <= hi; v++ {
		if set&(1<<v) == 0 {
			continue
		}
		end := v
		for end+1 <= hi && set&(1<<(end+1)) != 0 {
			end++
		}
		switch {
		case end-v >= 2:
			parts = append(parts, fmt.Sprintf("%d-%d", v, end))
		case end-v == 1:
			parts = append(parts, strconv.Itoa(v), strconv.Itoa(end))
		default:
			parts = append(parts, strconv.Itoa(v))
		}
		v = end
	}
	return strings.Join(parts, ",")
}

// values lists a set's members in order.
func values(set uint64, lo, hi int) []int {
	var out []int
	for v := lo; v <= hi; v++ {
		if set&(1<<v) != 0 {
			out = append(out, v)
		}
	}
	return out
}
