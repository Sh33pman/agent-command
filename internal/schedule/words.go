package schedule

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is when a job runs: a cron expression, or once at a time.
type Schedule struct {
	Cron string    `json:"cron,omitempty"`
	Once time.Time `json:"once,omitzero"`
}

// IsZero reports whether s says nothing.
func (s Schedule) IsZero() bool { return s.Cron == "" && s.Once.IsZero() }

// Parse reads a schedule given as cron ("0 8 * * 1-5") or in words ("weekdays
// at 08:00", "once on 2026-11-20 at 14:00"). Times are in loc. A one-off time
// must be after now.
func Parse(input string, loc *time.Location, now time.Time) (Schedule, error) {
	text := strings.TrimSpace(input)
	if text == "" {
		return Schedule{}, errors.New("the schedule is empty")
	}
	if looksLikeCron(text) {
		c, err := ParseCron(text)
		if err != nil {
			return Schedule{}, err
		}
		return Schedule{Cron: c.String()}, nil
	}
	if once, ok, err := parseOnce(text, loc); ok {
		if err != nil {
			return Schedule{}, err
		}
		if !once.After(now) {
			return Schedule{}, errors.New("that time has passed")
		}
		return Schedule{Once: once.UTC()}, nil
	}
	c, err := parseWords(text)
	if err != nil {
		return Schedule{}, err
	}
	return Schedule{Cron: c.String()}, nil
}

// Words says s in words, in loc. It's "" for a cron expression with no
// wording (s is then shown as cron only).
func (s Schedule) Words(loc *time.Location) string {
	if !s.Once.IsZero() {
		t := s.Once.In(loc)
		return "once on " + t.Format("2006-01-02") + " at " + t.Format("15:04")
	}
	c, err := ParseCron(s.Cron)
	if err != nil {
		return ""
	}
	return Describe(c)
}

// Next is the first run after t, in loc; zero when there's none.
func (s Schedule) Next(t time.Time, loc *time.Location) time.Time {
	if !s.Once.IsZero() {
		if s.Once.After(t) {
			return s.Once
		}
		return time.Time{}
	}
	c, err := ParseCron(s.Cron)
	if err != nil {
		return time.Time{}
	}
	return c.Next(t.In(loc))
}

// NextN is up to n runs after t.
func (s Schedule) NextN(t time.Time, loc *time.Location, n int) []time.Time {
	var out []time.Time
	for len(out) < n {
		t = s.Next(t, loc)
		if t.IsZero() {
			break
		}
		out = append(out, t)
	}
	return out
}

// looksLikeCron: five fields made of cron's characters (and month or day names).
func looksLikeCron(s string) bool {
	fields := strings.Fields(s)
	if len(fields) != 5 {
		return false
	}
	for _, f := range fields {
		for _, part := range strings.FieldsFunc(f, func(r rune) bool { return strings.ContainsRune(",-/", r) }) {
			if part == "*" || part == "?" {
				continue
			}
			if _, err := strconv.Atoi(part); err == nil {
				continue
			}
			l := strings.ToLower(part)
			if _, ok := monthField.names[l]; ok {
				continue
			}
			if _, ok := dowField.names[l]; ok {
				continue
			}
			return false
		}
	}
	return true
}

// --- words to cron ----------------------------------------------------------

var dayNames = []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"}

var monthNames = []string{"", "january", "february", "march", "april", "may", "june",
	"july", "august", "september", "october", "november", "december"}

// minuteSteps and hourSteps divide the hour and the day evenly, so "every 15
// minutes" really is every 15 minutes (cron's */7 restarts each hour).
var (
	minuteSteps = map[int]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 10: true, 12: true, 15: true, 20: true, 30: true}
	hourSteps   = map[int]bool{1: true, 2: true, 3: true, 4: true, 6: true, 8: true, 12: true}
)

// parseOnce reads "once on 2026-11-20 at 14:00", "on 2026-11-20 at 14:00" or
// "2026-11-20 14:00". ok says it was meant as a one-off.
func parseOnce(s string, loc *time.Location) (time.Time, bool, error) {
	w := strings.Fields(strings.ToLower(s))
	if len(w) > 0 && w[0] == "once" {
		w = w[1:]
	}
	if len(w) > 0 && w[0] == "on" {
		w = w[1:]
	}
	if len(w) == 0 {
		return time.Time{}, false, nil
	}
	date, err := time.ParseInLocation("2006-01-02", w[0], loc)
	if err != nil {
		if strings.HasPrefix(strings.ToLower(s), "once") {
			return time.Time{}, true, errors.New("a one-off run needs a date as YYYY-MM-DD, like \"once on 2026-11-20 at 14:00\"")
		}
		return time.Time{}, false, nil
	}
	rest := w[1:]
	if len(rest) > 0 && rest[0] == "at" {
		rest = rest[1:]
	}
	if len(rest) != 1 {
		return time.Time{}, true, errors.New("give one time for a one-off run, like \"once on 2026-11-20 at 14:00\"")
	}
	h, m, err := parseClock(rest[0])
	if err != nil {
		return time.Time{}, true, err
	}
	return time.Date(date.Year(), date.Month(), date.Day(), h, m, 0, 0, loc), true, nil
}

// parseWords reads the repeating forms. See Describe for what it writes back.
func parseWords(s string) (Cron, error) {
	text := strings.ToLower(strings.TrimSpace(s))
	text = strings.ReplaceAll(text, ",", " , ")
	w := strings.Fields(text)
	bad := fmt.Errorf("couldn't read %q: try \"every day at 08:00\", \"weekdays at 09:30\", \"every Monday and Friday at 17:00\", \"on the 1st at 07:00\", \"every 15 minutes\" or cron like \"0 8 * * 1-5\"", s)
	if len(w) == 0 {
		return Cron{}, bad
	}

	// every minute / every N minutes / every hour [at :MM] / every N hours [at :MM]
	if w[0] == "every" && len(w) >= 2 {
		n, unit, rest := 1, w[1], w[2:]
		if v, err := strconv.Atoi(w[1]); err == nil && len(w) >= 3 {
			n, unit, rest = v, w[2], w[3:]
		}
		switch strings.TrimSuffix(unit, "s") {
		case "minute":
			if len(rest) != 0 {
				return Cron{}, bad
			}
			if !minuteSteps[n] {
				return Cron{}, fmt.Errorf("every %d minutes doesn't divide the hour evenly: use cron (*/%d * * * *) for that", n, n)
			}
			return ParseCron(fmt.Sprintf("*/%d * * * *", n))
		case "hour":
			minute := 0
			if len(rest) > 0 {
				var err error
				if minute, err = parsePast(rest); err != nil {
					return Cron{}, err
				}
			}
			if !hourSteps[n] {
				return Cron{}, fmt.Errorf("every %d hours doesn't divide the day evenly: use cron (%d */%d * * *) for that", n, minute, n)
			}
			return ParseCron(fmt.Sprintf("%d */%d * * *", minute, n))
		}
	}

	// <days> at <times>
	at := indexOf(w, "at")
	if at < 0 {
		if _, _, _, err := parseDays(w); err == nil {
			return Cron{}, fmt.Errorf("say when in the day, like %q", s+" at 08:00")
		}
		return Cron{}, bad
	}
	days, times := w[:at], w[at+1:]
	minute, hours, err := parseTimes(times)
	if err != nil {
		return Cron{}, err
	}
	dom, month, dow, err := parseDays(days)
	if err != nil {
		return Cron{}, err
	}
	return ParseCron(fmt.Sprintf("%d %s %s %s %s", minute, joinInts(hours), dom, month, dow))
}

// parsePast reads "at :30" or "at 30 past" after "every hour".
func parsePast(w []string) (int, error) {
	if w[0] != "at" || len(w) < 2 {
		return 0, errors.New(`say the minute like "every hour at :30"`)
	}
	colon := strings.HasPrefix(w[1], ":")
	past := len(w) == 3 && w[2] == "past"
	m, err := strconv.Atoi(strings.TrimPrefix(w[1], ":"))
	if err != nil || m < 0 || m > 59 || colon == past || (len(w) > 2 && !past) {
		return 0, errors.New(`say the minute like "every hour at :30"`)
	}
	return m, nil
}

// parseTimes reads "08:00", "08:00 and 17:00" or "8am , noon and 5pm". The times
// must share their minute: one cron line can't say 08:00 and 17:30.
func parseTimes(w []string) (int, []int, error) {
	var hours []int
	minute := -1
	for _, tok := range w {
		if tok == "and" || tok == "," {
			continue
		}
		h, m, err := parseClock(tok)
		if err != nil {
			return 0, nil, err
		}
		if minute >= 0 && m != minute {
			return 0, nil, errors.New("times with different minutes can't share one schedule: make a job for each, or use cron")
		}
		minute = m
		hours = appendUnique(hours, h)
	}
	if len(hours) == 0 {
		return 0, nil, errors.New(`say the time, like "at 08:00"`)
	}
	return minute, hours, nil
}

// parseClock reads 08:00, 8:00, 8am, 8:30pm, noon and midnight.
func parseClock(s string) (int, int, error) {
	switch s {
	case "noon", "midday":
		return 12, 0, nil
	case "midnight":
		return 0, 0, nil
	}
	bad := fmt.Errorf("%q isn't a time: use 24-hour time like 08:00 or 17:30, or 8am", s)
	suffix := ""
	for _, x := range []string{"am", "pm"} {
		if strings.HasSuffix(s, x) {
			suffix, s = x, strings.TrimSuffix(s, x)
		}
	}
	hs, ms, hasMinutes := strings.Cut(s, ":")
	h, err := strconv.Atoi(hs)
	if err != nil {
		return 0, 0, bad
	}
	m := 0
	if hasMinutes {
		if len(ms) != 2 {
			return 0, 0, bad
		}
		if m, err = strconv.Atoi(ms); err != nil || m > 59 || m < 0 {
			return 0, 0, bad
		}
	} else if suffix == "" {
		return 0, 0, bad
	}
	switch suffix {
	case "am", "pm":
		if h < 1 || h > 12 {
			return 0, 0, bad
		}
		h %= 12
		if suffix == "pm" {
			h += 12
		}
	default:
		if h < 0 || h > 23 {
			return 0, 0, bad
		}
	}
	return h, m, nil
}

// parseDays reads the days part: every day, weekdays, weekends, every Monday
// and Friday, on the 1st and 15th [of January and July].
func parseDays(w []string) (dom, month, dow string, err error) {
	phrase := strings.Join(w, " ")
	switch phrase {
	case "every day", "daily", "each day":
		return "*", "*", "*", nil
	case "weekdays", "every weekday", "on weekdays":
		return "*", "*", "1-5", nil
	case "weekends", "every weekend", "on weekends":
		return "*", "*", "0,6", nil
	}
	bad := fmt.Errorf("couldn't read the days %q: try \"every day\", \"weekdays\", \"every Monday and Friday\" or \"on the 1st\"", phrase)
	if len(w) >= 2 && (w[0] == "every" || w[0] == "on") && dayIndex(w[1]) >= 0 {
		var days []int
		for _, tok := range w[1:] {
			if tok == "and" || tok == "," {
				continue
			}
			d := dayIndex(tok)
			if d < 0 {
				return "", "", "", bad
			}
			days = appendUnique(days, d)
		}
		return "*", "*", joinInts(days), nil
	}
	if len(w) >= 3 && w[0] == "on" && w[1] == "the" {
		of := indexOf(w, "of")
		dayWords, monthWords := w[2:], []string(nil)
		if of >= 0 {
			dayWords, monthWords = w[2:of], w[of+1:]
		}
		var doms []int
		for _, tok := range dayWords {
			if tok == "and" || tok == "," || tok == "the" {
				continue
			}
			d, ok := parseOrdinal(tok)
			if !ok {
				return "", "", "", bad
			}
			doms = appendUnique(doms, d)
		}
		if len(doms) == 0 {
			return "", "", "", bad
		}
		month = "*"
		if of >= 0 {
			var months []int
			for _, tok := range monthWords {
				if tok == "and" || tok == "," {
					continue
				}
				m := monthIndex(tok)
				if m < 0 {
					return "", "", "", bad
				}
				months = appendUnique(months, m)
			}
			if len(months) == 0 {
				return "", "", "", bad
			}
			month = joinInts(months)
		}
		return joinInts(doms), month, "*", nil
	}
	return "", "", "", bad
}

func dayIndex(s string) int {
	s = strings.TrimSuffix(s, "s") // "mondays"
	for i, d := range dayNames {
		if s == d || s == d[:3] {
			return i
		}
	}
	return -1
}

func monthIndex(s string) int {
	for i, m := range monthNames {
		if i > 0 && (s == m || s == m[:3]) {
			return i
		}
	}
	return -1
}

// parseOrdinal reads 1st, 2nd, 3rd, 4th … 31st (and a bare number).
func parseOrdinal(s string) (int, bool) {
	for _, suffix := range []string{"st", "nd", "rd", "th"} {
		s = strings.TrimSuffix(s, suffix)
	}
	n, err := strconv.Atoi(s)
	return n, err == nil && n >= 1 && n <= 31
}

// --- cron to words ----------------------------------------------------------

// Describe says c in words, the form parseWords reads back to the same cron.
// It's "" when there's no such wording.
func Describe(c Cron) string {
	minutes := values(c.Minute, 0, 59)
	hours := values(c.Hour, 0, 23)
	allDays := c.DomStar && c.DowStar && c.Month == fullSet(monthField)
	allHours := c.Hour == fullSet(hourField)

	if allDays && allHours {
		if step := everyStep(c.Minute, 0, 59); step > 0 && minuteSteps[step] {
			if step == 1 {
				return "every minute"
			}
			return fmt.Sprintf("every %d minutes", step)
		}
	}
	if len(minutes) != 1 {
		return ""
	}
	minute := minutes[0]
	if allDays {
		if step := everyStep(c.Hour, 0, 23); step > 0 && hourSteps[step] {
			s := "every hour"
			if step > 1 {
				s = fmt.Sprintf("every %d hours", step)
			}
			if minute != 0 {
				s += fmt.Sprintf(" at :%02d", minute)
			}
			return s
		}
	}
	days := describeDays(c)
	if days == "" {
		return ""
	}
	times := make([]string, len(hours))
	for i, h := range hours {
		times[i] = fmt.Sprintf("%02d:%02d", h, minute)
	}
	return days + " at " + list(times)
}

func describeDays(c Cron) string {
	allMonths := c.Month == fullSet(monthField)
	switch {
	case c.DomStar && c.DowStar && allMonths:
		return "every day"
	case c.DomStar && !c.DowStar && allMonths:
		days := values(c.Dow, 0, 6)
		switch c.Dow {
		case 0b0111110:
			return "weekdays"
		case 0b1000001:
			return "weekends"
		}
		// Monday first, Sunday last, as a week is said.
		if days[0] == 0 {
			days = append(days[1:], 0)
		}
		names := make([]string, len(days))
		for i, d := range days {
			names[i] = title(dayNames[d])
		}
		return "every " + list(names)
	case !c.DomStar && c.DowStar:
		doms := values(c.Dom, 1, 31)
		words := make([]string, len(doms))
		for i, d := range doms {
			words[i] = ordinal(d)
		}
		s := "on the " + list(words)
		if !allMonths {
			months := values(c.Month, 1, 12)
			names := make([]string, len(months))
			for i, m := range months {
				names[i] = title(monthNames[m])
			}
			s += " of " + list(names)
		}
		return s
	}
	return ""
}

// everyStep is n when set is exactly every nth value from lo, else 0.
func everyStep(set uint64, lo, hi int) int {
	for step := 1; step <= hi-lo; step++ {
		var want uint64
		for v := lo; v <= hi; v += step {
			want |= 1 << v
		}
		if want == set {
			return step
		}
	}
	return 0
}

func fullSet(f field) uint64 {
	hi := f.max
	if f.name == dowField.name {
		hi = 6
	}
	var set uint64
	for v := f.min; v <= hi; v++ {
		set |= 1 << v
	}
	return set
}

func ordinal(n int) string {
	suffix := "th"
	switch {
	case n%100 >= 11 && n%100 <= 13:
	case n%10 == 1:
		suffix = "st"
	case n%10 == 2:
		suffix = "nd"
	case n%10 == 3:
		suffix = "rd"
	}
	return strconv.Itoa(n) + suffix
}

// list joins "a", "a and b", "a, b and c".
func list(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	}
	return strings.Join(items[:len(items)-1], ", ") + " and " + items[len(items)-1]
}

func title(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

func indexOf(w []string, s string) int {
	for i, x := range w {
		if x == s {
			return i
		}
	}
	return -1
}

func appendUnique(xs []int, x int) []int {
	for _, y := range xs {
		if y == x {
			return xs
		}
	}
	return append(xs, x)
}

func joinInts(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = strconv.Itoa(x)
	}
	return strings.Join(parts, ",")
}
