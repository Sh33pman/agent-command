package store

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/brettmostert/agent-command/internal/core"
)

// newSessions makes live sessions on this machine, in Default.
func newSessions(t *testing.T, s *Store, names ...string) []string {
	t.Helper()
	ids := make([]string, len(names))
	for i, n := range names {
		sess := &core.Session{Name: n, Status: core.StatusRunning}
		must(t, s.CreateSession(ctx, sess))
		ids[i] = sess.ID
	}
	return ids
}

// members lists a set's session names in order, "" when it has ended.
func members(t *testing.T, s *Store, setID string) string {
	t.Helper()
	sessions, err := s.Sessions(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	slices.SortFunc(sessions, func(a, b *core.Session) int { return a.SetOrder - b.SetOrder })
	var out []string
	for _, sess := range sessions {
		if sess.SetID == setID {
			out = append(out, sess.Name)
		}
	}
	return strings.Join(out, ",")
}

func setExists(t *testing.T, s *Store, id string) bool {
	t.Helper()
	_, err := s.Set(ctx, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		t.Fatal(err)
	}
	return err == nil
}

func TestCreateSet(t *testing.T) {
	s, _ := open(t)
	g, err := s.CreateGroup(ctx, "api", "")
	must(t, err)
	ids := newSessions(t, s, "a", "b")
	must(t, s.MoveSessions(ctx, g.ID, ids[0]))

	set, err := s.CreateSet(ctx, "", ids...)
	must(t, err)
	if set.ID == "" || set.GroupID != g.ID || set.Host != "" {
		t.Errorf("set = %+v, want a new id in the first session's group", set)
	}
	if got := members(t, s, set.ID); got != "a,b" {
		t.Errorf("members = %q, want a,b", got)
	}
	// The set lives in one group: the second session followed the first.
	b, err := s.Session(ctx, ids[1])
	must(t, err)
	if b.GroupID != g.ID {
		t.Errorf("b is in %s, want the set's group %s", b.GroupID, g.ID)
	}
	sets, err := s.Sets(ctx)
	must(t, err)
	if len(sets) != 1 || *sets[0] != *set {
		t.Errorf("Sets() = %v, want just %+v", sets, set)
	}

	// The upgrade from the app's layout keeps its own set ids.
	more := newSessions(t, s, "c", "d")
	kept, err := s.CreateSet(ctx, "from-layout", more...)
	if err != nil || kept.ID != "from-layout" {
		t.Errorf("CreateSet with an id = %+v, %v", kept, err)
	}
	if _, err := s.CreateSet(ctx, "from-layout", newSessions(t, s, "e", "f")...); err == nil {
		t.Error("made a second set with the same id")
	}
}

func TestCreateSetRefusesBreakingTheRules(t *testing.T) {
	s, _ := open(t)
	ids := newSessions(t, s, "a", "b", "c")
	_, err := s.CreateSet(ctx, "", ids[0], ids[1])
	must(t, err)

	onHost := &core.Session{Name: "h", Status: core.StatusRunning, Host: "mini", RemoteID: "r1"}
	must(t, s.CreateSession(ctx, onHost))
	archived := &core.Session{Name: "z", Status: core.StatusArchived, ArchivedAt: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)}
	must(t, s.CreateSession(ctx, archived))

	cases := []struct {
		name string
		ids  []string
		want error
	}{
		{"one session", []string{ids[2]}, ErrSetTooSmall},
		{"the same session twice", []string{ids[2], ids[2]}, ErrSetTooSmall},
		{"a session already in a set", []string{ids[2], ids[0]}, ErrInSet},
		{"two hosts", []string{ids[2], onHost.ID}, ErrSetHosts},
		{"an archived session", []string{ids[2], archived.ID}, ErrArchived},
		{"a missing session", []string{ids[2], "nope"}, ErrNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.CreateSet(ctx, "", tc.ids...); !errors.Is(err, tc.want) {
				t.Errorf("CreateSet = %v, want %v", err, tc.want)
			}
		})
	}
	// Nothing changed: c is still on its own.
	c, err := s.Session(ctx, ids[2])
	must(t, err)
	if c.SetID != "" {
		t.Errorf("a refused set left c in %q", c.SetID)
	}
}

func TestAddToSetPlacesAfterTheFocusedMember(t *testing.T) {
	s, _ := open(t)
	ids := newSessions(t, s, "a", "b", "c", "d")
	set, err := s.CreateSet(ctx, "", ids[0], ids[1])
	must(t, err)

	must(t, s.AddToSet(ctx, set.ID, ids[2], ids[0])) // split beside a
	if got := members(t, s, set.ID); got != "a,c,b" {
		t.Errorf("after adding c beside a: %q, want a,c,b", got)
	}
	must(t, s.AddToSet(ctx, set.ID, ids[3], "")) // at the end
	if got := members(t, s, set.ID); got != "a,c,b,d" {
		t.Errorf("after adding d at the end: %q, want a,c,b,d", got)
	}

	host := &core.Session{Name: "h", Status: core.StatusRunning, Host: "mini", RemoteID: "r1"}
	must(t, s.CreateSession(ctx, host))
	for name, err := range map[string]error{
		"from another host":  s.AddToSet(ctx, set.ID, host.ID, ""),
		"already a member":   s.AddToSet(ctx, set.ID, ids[0], ""),
		"into a missing set": s.AddToSet(ctx, "nope", host.ID, ""),
		"after a non-member": s.AddToSet(ctx, set.ID, newSessions(t, s, "e")[0], host.ID),
		"a missing session":  s.AddToSet(ctx, set.ID, "nope", ""),
	} {
		if err == nil {
			t.Errorf("AddToSet %s succeeded", name)
		}
	}
	if got := members(t, s, set.ID); got != "a,c,b,d" {
		t.Errorf("refused adds changed the set: %q", got)
	}
}

func TestSetEndsWhenOneIsLeft(t *testing.T) {
	s, _ := open(t)
	ids := newSessions(t, s, "a", "b", "c")
	set, err := s.CreateSet(ctx, "", ids...)
	must(t, err)

	must(t, s.RemoveFromSet(ctx, ids[1]))
	if got := members(t, s, set.ID); got != "a,c" {
		t.Errorf("after moving b out: %q, want a,c", got)
	}
	must(t, s.RemoveFromSet(ctx, ids[1])) // not in a set: nothing to do
	must(t, s.RemoveFromSet(ctx, ids[2]))
	if setExists(t, s, set.ID) || members(t, s, set.ID) != "" {
		t.Errorf("the set outlived its second-to-last member: %q", members(t, s, set.ID))
	}
	if err := s.RemoveFromSet(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("RemoveFromSet(missing) = %v", err)
	}
}

func TestArchivingTheSecondToLastMemberEndsTheSet(t *testing.T) {
	s, _ := open(t)
	ids := newSessions(t, s, "a", "b", "c")
	set, err := s.CreateSet(ctx, "", ids...)
	must(t, err)
	archive := func(id string) {
		sess, err := s.Session(ctx, id)
		must(t, err)
		sess.Status, sess.ArchivedAt = core.StatusArchived, time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC)
		must(t, s.UpdateSession(ctx, sess))
		if sess.SetID != "" {
			t.Errorf("UpdateSession left the archived session's SetID %q", sess.SetID)
		}
	}

	archive(ids[0])
	if got := members(t, s, set.ID); got != "b,c" {
		t.Errorf("after archiving a: %q, want b,c", got)
	}
	archive(ids[1])
	if setExists(t, s, set.ID) {
		t.Error("the set outlived archiving its second-to-last member")
	}
	c, err := s.Session(ctx, ids[2])
	must(t, err)
	if c.SetID != "" || c.SetOrder != 0 {
		t.Errorf("the last session is still in %q (%d)", c.SetID, c.SetOrder)
	}
}

func TestDeletingAMemberSettlesTheSet(t *testing.T) {
	s, _ := open(t)
	ids := newSessions(t, s, "a", "b", "c")
	set, err := s.CreateSet(ctx, "", ids...)
	must(t, err)

	must(t, s.DeleteSession(ctx, ids[0])) // purged, or removed from a host
	if got := members(t, s, set.ID); got != "b,c" {
		t.Errorf("after deleting a: %q, want b,c", got)
	}
	b, err := s.Session(ctx, ids[1])
	must(t, err)
	if b.SetOrder != 1 {
		t.Errorf("b is %d in the set, want 1 after renumbering", b.SetOrder)
	}
	must(t, s.DeleteSession(ctx, ids[1]))
	if setExists(t, s, set.ID) {
		t.Error("the set outlived deleting its second-to-last member")
	}
	if err := s.DeleteSession(ctx, ids[1]); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleting again = %v, want ErrNotFound", err)
	}
}

func TestMovingOneMemberMovesTheSet(t *testing.T) {
	s, _ := open(t)
	g, err := s.CreateGroup(ctx, "api", "")
	must(t, err)
	ids := newSessions(t, s, "a", "b", "loose")
	set, err := s.CreateSet(ctx, "", ids[0], ids[1])
	must(t, err)

	must(t, s.MoveSessions(ctx, g.ID, ids[1], ids[2]))
	for _, id := range ids {
		sess, err := s.Session(ctx, id)
		must(t, err)
		if sess.GroupID != g.ID {
			t.Errorf("%s is in %s, want %s: moving b moves its set", sess.Name, sess.GroupID, g.ID)
		}
	}
	got, err := s.Set(ctx, set.ID)
	must(t, err)
	if got.GroupID != g.ID {
		t.Errorf("the set is in %s, want %s", got.GroupID, g.ID)
	}

	// A stale write of one member can't take it out of its set's group.
	a, err := s.Session(ctx, ids[0])
	must(t, err)
	a.GroupID = DefaultGroupID
	must(t, s.UpdateSession(ctx, a))
	if a, _ = s.Session(ctx, ids[0]); a.GroupID != g.ID {
		t.Errorf("UpdateSession split the set across groups: a is in %s", a.GroupID)
	}

	// Deleting the group moves the whole set to Default, together.
	must(t, s.DeleteGroup(ctx, g.ID))
	if got := members(t, s, set.ID); got != "a,b" {
		t.Errorf("after deleting the group: %q, want a,b", got)
	}
	if got, _ := s.Set(ctx, set.ID); got.GroupID != DefaultGroupID {
		t.Errorf("the set is in %s, want Default", got.GroupID)
	}
}

func TestEndSet(t *testing.T) {
	s, _ := open(t)
	ids := newSessions(t, s, "a", "b")
	set, err := s.CreateSet(ctx, "", ids...)
	must(t, err)
	must(t, s.EndSet(ctx, set.ID))
	if setExists(t, s, set.ID) || members(t, s, set.ID) != "" {
		t.Error("EndSet left the set or its members")
	}
	if err := s.EndSet(ctx, set.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("ending it again = %v, want ErrNotFound", err)
	}
	// Its sessions can make a new set.
	if _, err := s.CreateSet(ctx, "", ids...); err != nil {
		t.Errorf("a new set of the same sessions: %v", err)
	}
}

func TestSetsOfHostSessions(t *testing.T) {
	s, _ := open(t)
	var ids []string
	for _, r := range []string{"r1", "r2"} {
		sess := &core.Session{Name: r, Status: core.StatusRunning, Host: "mini", RemoteID: r}
		must(t, s.CreateSession(ctx, sess))
		ids = append(ids, sess.ID)
	}
	set, err := s.CreateSet(ctx, "", ids...)
	must(t, err)
	if set.Host != "mini" {
		t.Errorf("set host = %q, want mini", set.Host)
	}
	// Renaming the host keeps the set.
	must(t, s.RenameHost(ctx, "mini", "studio"))
	if got, _ := s.Set(ctx, set.ID); got.Host != "studio" {
		t.Errorf("after renaming the host: %q", got.Host)
	}
}
