package core

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Row is one line of the session list (SPEC §8.1, §8.4): a group header or
// a session. Number is the 1-based shortcut number of visible session rows
// (0 for headers and sessions in collapsed groups).
type Row struct {
	Group   *Group   // header rows (and the session's group on session rows)
	Session *Session // nil for header rows
	Number  int

	// Header rows only.
	Count     int    // sessions in the group (after filtering)
	Urgent    Status // most urgent status in the group, for collapsed headers
	Collapsed bool
	Focused   bool // the focused group (focus mode)
}

// IsHeader reports whether this is a group header row.
func (r Row) IsHeader() bool { return r.Session == nil }

// Layout orders and numbers sessions for display according to the shared
// view state: list or group view, sort key and direction, filter, collapse
// and focus (SPEC §8.4). It's used by the sidebar and by `acmd list`/`go N`,
// so numbers mean the same thing everywhere.
func Layout(sessions []*Session, groups []*Group, vs ViewState) []Row {
	byID := map[string]*Group{}
	var def *Group
	for _, g := range groups {
		byID[g.ID] = g
		if g.IsDefault {
			def = g
		}
	}
	if def == nil {
		def = &Group{ID: "default", Name: "Default", IsDefault: true}
		byID[def.ID] = def
	}
	groupOf := func(s *Session) *Group {
		if g, ok := byID[s.GroupID]; ok && g.ArchivedAt.IsZero() {
			return g
		}
		if g, ok := byID[s.GroupID]; ok && vs.ShowArchived {
			return g
		}
		return def
	}

	f := parseFilter(vs.Filter)
	var visible []*Session
	for _, s := range sessions {
		if s.Status == StatusArchived && !vs.ShowArchived {
			continue
		}
		if f.match(s, groupOf(s)) {
			visible = append(visible, s)
		}
	}
	sortSessions(visible, vs.Sort, vs.Desc)

	liveGroups := 0
	for _, g := range groups {
		if !g.IsDefault && g.ArchivedAt.IsZero() {
			liveGroups++
		}
	}
	// List view, or group view while only Default exists (no headers, §8.4).
	if vs.Mode == "list" || liveGroups == 0 {
		rows := make([]Row, len(visible))
		for i, s := range visible {
			rows[i] = Row{Group: groupOf(s), Session: s, Number: i + 1}
		}
		return rows
	}

	members := map[string][]*Session{}
	for _, s := range visible {
		g := groupOf(s)
		members[g.ID] = append(members[g.ID], s)
	}
	ordered := orderGroups(groups, vs.ShowArchived)
	if _, ok := members[def.ID]; ok && !containsGroup(ordered, def) {
		ordered = append(ordered, def)
	}

	var rows []Row
	n := 0
	for _, g := range ordered {
		ms := members[g.ID]
		if f.active() && len(ms) == 0 {
			continue // filtering hides groups without matches
		}
		collapsed := g.Collapsed && !f.active() // matches expand while filtering
		h := Row{Group: g, Count: len(ms), Urgent: mostUrgent(ms), Collapsed: collapsed, Focused: vs.FocusedGroup == g.ID}
		rows = append(rows, h)
		if collapsed {
			continue
		}
		for _, s := range ms {
			n++
			rows = append(rows, Row{Group: g, Session: s, Number: n})
		}
	}
	return rows
}

func orderGroups(groups []*Group, showArchived bool) []*Group {
	var out []*Group
	for _, g := range groups {
		if g.ArchivedAt.IsZero() || showArchived {
			out = append(out, g)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.IsDefault != b.IsDefault {
			return !a.IsDefault // Default last
		}
		if a.SortKey != b.SortKey {
			return a.SortKey < b.SortKey
		}
		return a.CreatedAt.Before(b.CreatedAt)
	})
	return out
}

func containsGroup(gs []*Group, g *Group) bool {
	for _, x := range gs {
		if x.ID == g.ID {
			return true
		}
	}
	return false
}

// statusRank orders statuses by urgency (SPEC §8.4 `status` sort).
func statusRank(s *Session) int {
	switch s.Status {
	case StatusWaiting:
		return 0
	case StatusError:
		return 1
	case StatusBusy:
		return 2
	case StatusDone:
		if s.Unseen {
			return 3
		}
		return 4
	case StatusStarting:
		return 5
	case StatusPaused:
		return 6
	default:
		return 7
	}
}

func mostUrgent(ss []*Session) Status {
	best, rank := Status(""), 99
	for _, s := range ss {
		if r := statusRank(s); r < rank {
			best, rank = s.Status, r
		}
	}
	return best
}

func sortSessions(ss []*Session, key string, desc bool) {
	less := func(a, b *Session) int {
		switch key {
		case "name":
			return naturalCompare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		case "activity":
			return a.LastEventAt.Compare(b.LastEventAt)
		case "status":
			return statusRank(a) - statusRank(b)
		case "cwd":
			if c := strings.Compare(strings.ToLower(filepath.Base(a.Cwd)), strings.ToLower(filepath.Base(b.Cwd))); c != 0 {
				return c
			}
			return naturalCompare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		default: // created
			return 0
		}
	}
	sort.SliceStable(ss, func(i, j int) bool {
		a, b := ss[i], ss[j]
		c := less(a, b)
		if c == 0 {
			// Stable tie-break: creation order, then id.
			if c = a.CreatedAt.Compare(b.CreatedAt); c == 0 {
				c = strings.Compare(a.ID, b.ID)
			}
		}
		if desc {
			return c > 0
		}
		return c < 0
	})
}

// naturalCompare compares strings treating digit runs as numbers, so
// "proj-9" < "proj-10". Digit runs are compared by length (after leading
// zeros) and then lexically, so arbitrarily long numbers can't overflow.
func naturalCompare(a, b string) int {
	ra, rb := []rune(a), []rune(b)
	i, j := 0, 0
	for i < len(ra) && j < len(rb) {
		if isDigit(ra[i]) && isDigit(rb[j]) {
			si, sj := i, j
			for i < len(ra) && isDigit(ra[i]) {
				i++
			}
			for j < len(rb) && isDigit(rb[j]) {
				j++
			}
			na := strings.TrimLeft(string(ra[si:i]), "0")
			nb := strings.TrimLeft(string(rb[sj:j]), "0")
			if c := cmpInt(len(na), len(nb)); c != 0 {
				return c
			}
			if c := strings.Compare(na, nb); c != 0 {
				return c
			}
			continue
		}
		if ra[i] != rb[j] {
			return cmpInt(int(ra[i]), int(rb[j]))
		}
		i++
		j++
	}
	return cmpInt(len(ra)-i, len(rb)-j)
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func cmpInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// filter is a parsed filter string: plain terms match name, description,
// cwd and group name; g:<text> terms match the group name. All terms must
// match (SPEC §8.4).
type filter struct{ text, group []string }

func parseFilter(s string) filter {
	var f filter
	for _, t := range strings.Fields(strings.ToLower(s)) {
		if g, ok := strings.CutPrefix(t, "g:"); ok {
			if g != "" {
				f.group = append(f.group, g)
			}
			continue
		}
		f.text = append(f.text, t)
	}
	return f
}

func (f filter) active() bool { return len(f.text)+len(f.group) > 0 }

func (f filter) match(s *Session, g *Group) bool {
	gname := strings.ToLower(g.Name)
	for _, t := range f.group {
		if !strings.Contains(gname, t) {
			return false
		}
	}
	hay := strings.ToLower(strings.Join([]string{s.Name, s.Description, s.Cwd, g.Name}, "\x00"))
	for _, t := range f.text {
		if !strings.Contains(hay, t) {
			return false
		}
	}
	return true
}

// FocusGroup expands the target group and collapses the others, saving the
// previous collapse state in vs.PreFocus (SPEC §8.4). It returns the
// desired collapsed state per group id; the caller persists both.
func FocusGroup(vs *ViewState, groups []*Group, id string) (map[string]bool, error) {
	found := false
	for _, g := range groups {
		if g.ID == id {
			found = true
		}
	}
	if !found {
		return nil, fmt.Errorf("group %s not found", id)
	}
	if vs.FocusedGroup == "" {
		vs.PreFocus = map[string]bool{}
		for _, g := range groups {
			vs.PreFocus[g.ID] = g.Collapsed
		}
	}
	vs.FocusedGroup = id
	vs.Mode = "groups"
	want := map[string]bool{}
	for _, g := range groups {
		want[g.ID] = g.ID != id
	}
	return want, nil
}

// ClearFocus leaves focus mode and returns the collapse state to restore.
func ClearFocus(vs *ViewState) map[string]bool {
	restore := vs.PreFocus
	vs.FocusedGroup, vs.PreFocus = "", nil
	if restore == nil {
		restore = map[string]bool{}
	}
	return restore
}

// Selector errors.
var (
	ErrNoMatch   = errors.New("no session matches")
	ErrAmbiguous = errors.New("more than one session matches")
)

// Resolve finds a session by a CLI selector (SPEC §8.7): its number in the
// current layout, its acmd id, its short background id, its Claude session
// id, or a unique case-insensitive name prefix.
func Resolve(rows []Row, all []*Session, sel string) (*Session, error) {
	sel = strings.TrimSpace(sel)
	if sel == "" {
		return nil, ErrNoMatch
	}
	if n, err := strconv.Atoi(sel); err == nil {
		for _, r := range rows {
			if r.Number == n && r.Session != nil {
				return r.Session, nil
			}
		}
		return nil, fmt.Errorf("%w: no session number %d in the current view", ErrNoMatch, n)
	}
	for _, s := range all {
		if s.ID == sel || (s.BgID != "" && s.BgID == sel) || s.ClaudeSessionID == sel {
			return s, nil
		}
	}
	lower := strings.ToLower(sel)
	var matches []*Session
	for _, s := range all {
		if strings.HasPrefix(strings.ToLower(s.Name), lower) {
			matches = append(matches, s)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("%w %q", ErrNoMatch, sel)
	case 1:
		return matches[0], nil
	default:
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = m.Name
		}
		return nil, fmt.Errorf("%w %q: %s", ErrAmbiguous, sel, strings.Join(names, ", "))
	}
}
