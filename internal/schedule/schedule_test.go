package schedule

import (
	"strings"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func at(loc *time.Location, s string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestParseCron(t *testing.T) {
	for in, want := range map[string]string{
		"0 8 * * 1-5":         "0 8 * * 1-5",
		"*/15 * * * *":        "*/15 * * * *",
		"0 9 * * mon,wed,FRI": "0 9 * * 1,3,5",
		"30 7 1 jan *":        "30 7 1 1 *",
		"0 0 * * 7":           "0 0 * * 0",
		"0 0 * * 0-7":         "0 0 * * *",
		"5/20 * * * *":        "5,25,45 * * * *",
		"0 0,12 * * *":        "0 */12 * * *",
		"0-4 * * * *":         "0-4 * * * *",
		"0 1,2 * * *":         "0 1,2 * * *",
		"0 0 1-31/2 * *":      "0 0 */2 * *",
		"* * ? * *":           "* * * * *",
	} {
		c, err := ParseCron(in)
		if err != nil {
			t.Errorf("%q: %v", in, err)
			continue
		}
		if got := c.String(); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
		// The canonical form parses to the same thing.
		again, err := ParseCron(c.String())
		if err != nil || again != c {
			t.Errorf("%q: canonical %q doesn't round-trip (%v)", in, c.String(), err)
		}
	}
}

func TestParseCronErrors(t *testing.T) {
	for in, want := range map[string]string{
		"":             "5 fields",
		"* * * *":      "5 fields",
		"60 * * * *":   "out of range",
		"* 24 * * *":   "out of range",
		"* * 0 * *":    "out of range",
		"* * * 13 *":   "out of range",
		"* * * * 8":    "out of range",
		"5-1 * * * *":  "backwards",
		"*/0 * * * *":  "bad step",
		"*/x * * * *":  "bad step",
		"x * * * *":    "isn't a valid minute",
		"1,,2 * * * *": "empty item",
		"0 0 30 2 *":   "never comes",
		"0 0 31 4,6 *": "never comes",
	} {
		_, err := ParseCron(in)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error with %q", in, err, want)
		}
	}
}

func TestNext(t *testing.T) {
	utc := time.UTC
	for _, tc := range []struct{ cron, from, want string }{
		{"0 8 * * 1-5", "2026-11-13 08:00", "2026-11-16 08:00"}, // Friday 08:00 → Monday
		{"0 8 * * 1-5", "2026-11-13 07:59", "2026-11-13 08:00"},
		{"*/15 * * * *", "2026-11-12 10:07", "2026-11-12 10:15"},
		{"0 0 1 * *", "2026-11-12 10:00", "2026-12-01 00:00"},
		{"0 0 29 2 *", "2026-03-01 00:00", "2028-02-29 00:00"},  // the next leap year
		{"0 12 13 * 5", "2026-11-12 00:00", "2026-11-13 12:00"}, // the 13th, or any Friday
		{"0 12 1 * 1", "2026-11-14 00:00", "2026-11-16 12:00"},  // a Monday before the 1st
		{"30 23 31 12 *", "2026-12-31 23:30", "2027-12-31 23:30"},
	} {
		c, err := ParseCron(tc.cron)
		if err != nil {
			t.Fatal(err)
		}
		if got := c.Next(at(utc, tc.from)); !got.Equal(at(utc, tc.want)) {
			t.Errorf("%s after %s: got %s, want %s", tc.cron, tc.from, got.Format("2006-01-02 15:04"), tc.want)
		}
	}
}

// Daylight saving: a time the clock skips runs when the clock gets there, and an
// hour the clock repeats runs once.
func TestNextAcrossClockChanges(t *testing.T) {
	ny := mustLoc(t, "America/New_York")
	c, _ := ParseCron("30 2 * * *")
	// 2026-03-08: clocks go from 02:00 to 03:00, so 02:30 doesn't exist.
	got := c.Next(at(ny, "2026-03-08 00:00"))
	if got.Day() != 8 || got.Hour() != 3 {
		t.Errorf("spring forward: got %s", got)
	}
	if next := c.Next(got); next.Day() != 9 || next.Hour() != 2 || next.Minute() != 30 {
		t.Errorf("the day after: got %s", next)
	}

	// 2026-11-01: 01:00-02:00 happens twice. An hourly job runs once at each
	// time on the clock, so the repeated hour runs once.
	hourly, _ := ParseCron("0 * * * *")
	start := time.Date(2026, 11, 1, 0, 30, 0, 0, ny)
	var runs []time.Time
	for t0 := start; len(runs) < 4; {
		t0 = hourly.Next(t0)
		runs = append(runs, t0)
	}
	for i, want := range []int{1, 2, 3, 4} {
		if runs[i].Hour() != want || (i > 0 && !runs[i].After(runs[i-1])) {
			t.Errorf("fall back: runs %v", runs)
			break
		}
	}

	// A daily job at 01:30 runs once on the night the hour repeats.
	daily, _ := ParseCron("30 1 * * *")
	first := daily.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, ny))
	second := daily.Next(first)
	if second.Day() != 2 {
		t.Errorf("fall back: ran twice on the 1st (%s, %s)", first, second)
	}
}

func TestNextNever(t *testing.T) {
	c := Cron{Minute: 1, Hour: 1, Dom: 1 << 31, Month: 1 << 2, DowStar: true} // 31 February
	if got := c.Next(time.Now()); !got.IsZero() {
		t.Errorf("got %s", got)
	}
}

// Words go to cron and back, and the words acmd writes read back to the same cron.
func TestWordsRoundTrip(t *testing.T) {
	for words, cron := range map[string]string{
		"every minute":                                "* * * * *",
		"every 15 minutes":                            "*/15 * * * *",
		"every hour":                                  "0 * * * *",
		"every hour at :30":                           "30 * * * *",
		"every 2 hours":                               "0 */2 * * *",
		"every 6 hours at :05":                        "5 */6 * * *",
		"every day at 08:00":                          "0 8 * * *",
		"every day at 08:00 and 17:00":                "0 8,17 * * *",
		"every day at 08:15, 12:15 and 17:15":         "15 8,12,17 * * *",
		"weekdays at 09:30":                           "30 9 * * 1-5",
		"weekends at 10:00":                           "0 10 * * 0,6",
		"every Monday at 09:00":                       "0 9 * * 1",
		"every Monday and Friday at 17:00":            "0 17 * * 1,5",
		"every Monday, Wednesday and Friday at 07:45": "45 7 * * 1,3,5",
		"every Saturday and Sunday at 11:00":          "0 11 * * 0,6",
		"every Tuesday and Sunday at 11:00":           "0 11 * * 0,2",
		"on the 1st at 07:00":                         "0 7 1 * *",
		"on the 1st and 15th at 07:00":                "0 7 1,15 * *",
		"on the 2nd, 3rd and 22nd at 06:00":           "0 6 2,3,22 * *",
		"on the 1st of January at 00:00":              "0 0 1 1 *",
		"on the 11th of January and July at 09:00":    "0 9 11 1,7 *",
	} {
		c, err := parseWords(words)
		if err != nil {
			t.Errorf("%q: %v", words, err)
			continue
		}
		if c.String() != cron {
			t.Errorf("%q: got cron %q, want %q", words, c.String(), cron)
		}
		back, _ := ParseCron(cron)
		got := Describe(back)
		want := words
		if words == "every Saturday and Sunday at 11:00" {
			want = "weekends at 11:00"
		}
		if words == "every Tuesday and Sunday at 11:00" {
			want = "every Tuesday and Sunday at 11:00"
		}
		if got != want {
			t.Errorf("%q → %q → %q", words, cron, got)
		}
	}
}

// Other ways of saying the same thing read to the same cron.
func TestWordsVariants(t *testing.T) {
	for words, cron := range map[string]string{
		"daily at 8am":                 "0 8 * * *",
		"Every Day At 8:00":            "0 8 * * *",
		"every weekday at 9:30am":      "30 9 * * 1-5",
		"every day at noon":            "0 12 * * *",
		"every day at midnight":        "0 0 * * *",
		"every day at 12am":            "0 0 * * *",
		"every day at 12pm":            "0 12 * * *",
		"every day at 5:15pm":          "15 17 * * *",
		"every mon and fri at 17:00":   "0 17 * * 1,5",
		"on mondays at 09:00":          "0 9 * * 1",
		"on the 1 at 07:00":            "0 7 1 * *",
		"on the 1st of jan at 07:00":   "0 7 1 1 *",
		"every 1 hours":                "0 * * * *",
		"every 30 minutes":             "*/30 * * * *",
		"every hour at 30 past":        "30 * * * *",
		"every day at 08:00 and 08:00": "0 8 * * *",
	} {
		c, err := parseWords(words)
		if err != nil {
			t.Errorf("%q: %v", words, err)
			continue
		}
		if c.String() != cron {
			t.Errorf("%q: got %q, want %q", words, c.String(), cron)
		}
	}
}

func TestWordsErrors(t *testing.T) {
	for words, want := range map[string]string{
		"sometimes":                        "couldn't read",
		"every day":                        "say when in the day",
		"every day at 25:00":               "isn't a time",
		"every day at 8":                   "isn't a time",
		"every day at 8:5":                 "isn't a time",
		"every day at 13pm":                "isn't a time",
		"every day at 08:00 and 17:30":     "different minutes",
		"every 7 minutes":                  "doesn't divide the hour",
		"every 5 hours":                    "doesn't divide the day",
		"every hour at 30":                 "every hour at :30",
		"every hour at :75":                "every hour at :30",
		"every funday at 08:00":            "couldn't read the days",
		"on the 32nd at 08:00":             "couldn't read the days",
		"on the 1st of smarch at 08:00":    "couldn't read the days",
		"on the 30th of February at 08:00": "never comes",
		"every day at":                     "say the time",
	} {
		_, err := parseWords(words)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want an error with %q", words, err, want)
		}
	}
}

// Cron with no wording is shown as cron only.
func TestDescribeHasNoWords(t *testing.T) {
	for _, cron := range []string{
		"*/7 * * * *",  // doesn't divide the hour
		"0,30 9 * * *", // two minutes
		"0 9 1 * 1",    // day of the month or Monday
		"0 9 * 6 *",    // every day in June
		"0 9 * 6 1",    // Mondays in June
		"0-10 * * * *",
	} {
		c, err := ParseCron(cron)
		if err != nil {
			t.Fatal(err)
		}
		if got := Describe(c); got != "" {
			t.Errorf("%s: got %q, want no words", cron, got)
		}
	}
}

func TestParseSchedule(t *testing.T) {
	loc := mustLoc(t, "Africa/Johannesburg")
	now := at(loc, "2026-11-12 09:00")
	s, err := Parse("weekdays at 08:00", loc, now)
	if err != nil || s.Cron != "0 8 * * 1-5" {
		t.Fatalf("words: %+v %v", s, err)
	}
	if s.Words(loc) != "weekdays at 08:00" {
		t.Errorf("words back: %q", s.Words(loc))
	}
	if got := s.Next(now, loc); !got.Equal(at(loc, "2026-11-13 08:00")) {
		t.Errorf("next: %s", got)
	}
	runs := s.NextN(now, loc, 3)
	if len(runs) != 3 || !runs[2].Equal(at(loc, "2026-11-17 08:00")) {
		t.Errorf("next 3: %v", runs)
	}

	c, err := Parse("  0 8 * * MON-FRI ", loc, now)
	if err != nil || c.Cron != "0 8 * * 1-5" || c.Words(loc) != "weekdays at 08:00" {
		t.Errorf("cron: %+v %v", c, err)
	}

	for _, in := range []string{"once on 2026-11-20 at 14:00", "on 2026-11-20 at 14:00", "2026-11-20 14:00", "once on 2026-11-20 at 2pm"} {
		o, err := Parse(in, loc, now)
		if err != nil || !o.Once.Equal(at(loc, "2026-11-20 14:00")) {
			t.Errorf("%q: %+v %v", in, o, err)
			continue
		}
		if o.Words(loc) != "once on 2026-11-20 at 14:00" {
			t.Errorf("%q: words %q", in, o.Words(loc))
		}
		if got := o.Next(now, loc); !got.Equal(o.Once) {
			t.Errorf("%q: next %s", in, got)
		}
		if got := o.Next(o.Once, loc); !got.IsZero() {
			t.Errorf("%q: runs again at %s", in, got)
		}
		if n := o.NextN(now, loc, 3); len(n) != 1 {
			t.Errorf("%q: next 3 %v", in, n)
		}
	}

	for in, want := range map[string]string{
		"":                            "empty",
		"once on 2026-11-01 at 14:00": "passed",
		"once on tuesday at 14:00":    "YYYY-MM-DD",
		"once on 2026-11-20":          "one time",
		"once on 2026-11-20 at 25:00": "isn't a time",
		"61 * * * *":                  "out of range",
		"whenever":                    "couldn't read",
	} {
		if _, err := Parse(in, loc, now); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: got %v, want %q", in, err, want)
		}
	}

	if (Schedule{}).Words(loc) != "" || !(Schedule{}).Next(now, loc).IsZero() || !(Schedule{}).IsZero() {
		t.Error("an empty schedule")
	}
	if (Schedule{Cron: "bad"}).Words(loc) != "" {
		t.Error("bad cron has words")
	}
}

func TestOrdinal(t *testing.T) {
	for n, want := range map[int]string{1: "1st", 2: "2nd", 3: "3rd", 4: "4th", 11: "11th", 12: "12th", 13: "13th", 21: "21st", 22: "22nd", 23: "23rd", 31: "31st"} {
		if got := ordinal(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"0 8 * * 1-5", "every day at 08:00", "weekdays at 9am", "once on 2027-01-01 at 00:00", "on the 1st of jan at 7am", "*/5 * * * *"} {
		f.Add(s)
	}
	now := time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, in string) {
		s, err := Parse(in, time.UTC, now)
		if err != nil {
			return
		}
		// Whatever parses has a next run and, when it has words, they read back to it.
		if s.Next(now, time.UTC).IsZero() {
			t.Fatalf("%q parsed to %+v with no next run", in, s)
		}
		if w := s.Words(time.UTC); w != "" {
			back, err := Parse(w, time.UTC, now)
			if err != nil || back != s {
				t.Fatalf("%q → %+v → %q → %+v (%v)", in, s, w, back, err)
			}
		}
	})
}
