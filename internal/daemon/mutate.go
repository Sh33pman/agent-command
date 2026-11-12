package daemon

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
	"github.com/brettmostert/agent-command/internal/store"
)

// Remover deletes a background session (claudecli.CLI.Remove).
type Remover interface {
	Remove(ctx context.Context, id string) error
}

// Stopper stops a background session (claudecli.CLI.Stop in production).
type Stopper interface {
	Stop(ctx context.Context, id string) error
}

var validSorts = map[string]bool{"created": true, "name": true, "activity": true, "status": true, "cwd": true}

func (d *Daemon) snapshot(ctx context.Context) (ipc.Snapshot, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	sessions, err := d.store.Sessions(ctx, true)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	groups, err := d.store.Groups(ctx, true)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	vs, err := d.store.ViewState(ctx)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	queue, err := d.store.InboxQueue(ctx)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	jobs, err := d.jobs(ctx)
	if err != nil {
		return ipc.Snapshot{}, err
	}
	return ipc.Snapshot{Sessions: sessions, Groups: groups, ViewState: vs, Notices: d.notices(), Inbox: queue, Jobs: jobs}, nil
}

// mutate applies one client change. It returns a value for group.create.
func (d *Daemon) mutate(ctx context.Context, m ipc.Mutation) (any, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	st := d.store
	switch m.Kind {
	case ipc.MutSeen:
		return nil, d.updateSession(ctx, m.ID, func(s *core.Session) error {
			s.Unseen = false
			return nil
		})
	case ipc.MutRename:
		return nil, d.updateSession(ctx, m.ID, func(s *core.Session) error {
			name := strings.TrimSpace(m.Text)
			if name == "" {
				return errors.New("name is empty")
			}
			s.Name, s.NameSource = name, core.NameUser
			return nil
		})
	case ipc.MutDescribe:
		return nil, d.updateSession(ctx, m.ID, func(s *core.Session) error {
			s.Description = strings.TrimSpace(m.Text)
			return nil
		})
	case ipc.MutArchive:
		return nil, d.archive(ctx, m.ID)
	case ipc.MutFinish:
		return nil, d.finish(ctx, m.ID)
	case ipc.MutUnarchive:
		return nil, d.updateSession(ctx, m.ID, func(s *core.Session) error {
			if s.IsTerminal() {
				return errors.New("an archived terminal's shell has ended: start a new one")
			}
			core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
			return nil
		})
	case ipc.MutMove:
		return nil, st.MoveSessions(ctx, m.GroupID, m.IDs...)
	case ipc.MutGroupCreate:
		return st.CreateGroup(ctx, m.Text, m.Color)
	case ipc.MutGroupRename:
		return nil, st.RenameGroup(ctx, m.GroupID, m.Text)
	case ipc.MutGroupColor:
		return nil, st.SetGroupColor(ctx, m.GroupID, m.Color)
	case ipc.MutGroupDelete:
		return nil, st.DeleteGroup(ctx, m.GroupID)
	case ipc.MutGroupArchive:
		sessions, err := st.Sessions(ctx, false)
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			if s.GroupID == m.GroupID {
				if err := d.archive(ctx, s.ID); err != nil {
					return nil, err
				}
			}
		}
		return nil, st.SetGroupArchived(ctx, m.GroupID, true)
	case ipc.MutGroupUnarchive:
		if err := st.SetGroupArchived(ctx, m.GroupID, false); err != nil {
			return nil, err
		}
		if !m.Flag {
			return nil, nil
		}
		sessions, err := st.Sessions(ctx, true)
		if err != nil {
			return nil, err
		}
		for _, s := range sessions {
			if s.GroupID == m.GroupID && s.Status == core.StatusArchived && !s.IsTerminal() {
				core.Apply(s, core.Event{Kind: core.EventUnarchived, Time: d.eventTime(s)})
				if err := st.UpdateSession(ctx, s); err != nil {
					return nil, err
				}
			}
		}
		return nil, nil
	case ipc.MutGroupCollapse:
		if m.GroupID != "" {
			return nil, st.SetGroupCollapsed(ctx, m.GroupID, m.Flag)
		}
		groups, err := st.Groups(ctx, false)
		if err != nil {
			return nil, err
		}
		for _, g := range groups {
			if err := st.SetGroupCollapsed(ctx, g.ID, m.Flag); err != nil {
				return nil, err
			}
		}
		return nil, nil
	case ipc.MutPurge:
		n := 0
		for _, id := range m.IDs {
			sess, err := st.Session(ctx, id)
			if err != nil {
				return n, err
			}
			if sess.Status != core.StatusArchived {
				return n, fmt.Errorf("%s isn't archived", id)
			}
			if sess.Kind == core.KindBackground && sess.BgID != "" && d.cfg.Remover != nil {
				if err := d.cfg.Remover.Remove(ctx, sess.BgID); err != nil {
					d.cfg.Log.Printf("purge: claude rm %s: %v", sess.BgID, err) // already gone: fine
				}
			}
			if err := st.DeleteSession(ctx, id); err != nil {
				return n, err
			}
			n++
		}
		return n, nil
	case ipc.MutGroupMove:
		return nil, d.moveGroup(ctx, m.GroupID, m.Flag)
	case ipc.MutViewMode:
		if m.Text != "groups" && m.Text != "list" {
			return nil, fmt.Errorf("view mode must be groups or list, not %q", m.Text)
		}
		return nil, d.updateViewState(ctx, func(vs *core.ViewState) { vs.Mode = m.Text })
	case ipc.MutViewSort:
		if !validSorts[m.Text] {
			return nil, fmt.Errorf("sort must be created, name, activity, status or cwd, not %q", m.Text)
		}
		return nil, d.updateViewState(ctx, func(vs *core.ViewState) { vs.Sort, vs.Desc = m.Text, m.Flag })
	case ipc.MutViewFilter:
		return nil, d.updateViewState(ctx, func(vs *core.ViewState) { vs.Filter = m.Text })
	case ipc.MutViewArchived:
		return nil, d.updateViewState(ctx, func(vs *core.ViewState) { vs.ShowArchived = m.Flag })
	case ipc.MutViewFocus:
		return nil, d.focus(ctx, m.GroupID)
	case ipc.MutInboxRead:
		return nil, d.store.SetInboxRead(ctx, m.ItemIDs, m.Flag)
	case ipc.MutInboxDismiss:
		for _, id := range m.ItemIDs {
			if err := d.store.HandleInboxItem(ctx, id, core.InboxDismissed); err != nil && !errors.Is(err, store.ErrNotFound) {
				return nil, err
			}
		}
		return nil, nil
	default:
		return nil, fmt.Errorf("unknown mutation %q", m.Kind)
	}
}

func (d *Daemon) updateSession(ctx context.Context, id string, fn func(*core.Session) error) error {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return d.store.UpdateSession(ctx, s)
}

func (d *Daemon) updateViewState(ctx context.Context, fn func(*core.ViewState)) error {
	vs, err := d.store.ViewState(ctx)
	if err != nil {
		return err
	}
	fn(&vs)
	return d.store.PutViewState(ctx, vs)
}

// finish stops a background session (claude stop; Claude keeps the
// conversation) and marks it finished; views showing it go back to their
// list. Showing it again resumes it.
func (d *Daemon) finish(ctx context.Context, id string) error {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	switch {
	case s.Status == core.StatusArchived:
		return errors.New("that session is archived")
	case !s.Managed():
		return errors.New("that session runs in your own pane: exit Claude there, or move it into acmd first")
	case s.Status == core.StatusFinished:
		return nil
	}
	d.stopHost(s)
	if s.Kind == core.KindBackground && s.BgID != "" && s.Status != core.StatusPaused {
		if d.cfg.Stopper == nil {
			return errors.New("can't stop the background session: claude not available")
		}
		if err := d.cfg.Stopper.Stop(ctx, s.BgID); err != nil {
			return fmt.Errorf("stop %s: %w", s.BgID, err)
		}
	}
	core.Apply(s, core.Event{Kind: core.EventFinished, Time: d.eventTime(s)})
	if err := d.store.CloseInboxItems(ctx, s.ID); err != nil {
		return err
	}
	if err := d.store.UpdateSession(ctx, s); err != nil {
		return err
	}
	return d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
		Kind: string(core.EventFinished), Detail: string(s.Status)})
}

// archive stops a running background session (Claude keeps the
// conversation) and marks it archived. External sessions are never
// killed: acmd doesn't own the user's pane.
func (d *Daemon) archive(ctx context.Context, id string) error {
	s, err := d.store.Session(ctx, id)
	if err != nil {
		return err
	}
	if s.Status == core.StatusArchived {
		return nil
	}
	d.stopHost(s)
	if s.Kind == core.KindBackground && s.BgID != "" && !s.Stopped() {
		if d.cfg.Stopper == nil {
			return errors.New("can't stop the background session: claude not available")
		}
		if err := d.cfg.Stopper.Stop(ctx, s.BgID); err != nil {
			return fmt.Errorf("stop %s: %w", s.BgID, err)
		}
	}
	core.Apply(s, core.Event{Kind: core.EventArchived, Time: d.eventTime(s)})
	if err := d.store.CloseInboxItems(ctx, s.ID); err != nil {
		return err
	}
	if err := d.store.UpdateSession(ctx, s); err != nil {
		return err
	}
	return d.store.AppendEvent(ctx, store.LoggedEvent{SessionID: s.ID, ClaudeSessionID: s.ClaudeSessionID,
		Kind: string(core.EventArchived), Detail: string(s.Status)})
}

// focus enters (groupID != "") or leaves focus mode, persisting the
// collapse plan from core.FocusGroup / core.ClearFocus.
func (d *Daemon) focus(ctx context.Context, groupID string) error {
	vs, err := d.store.ViewState(ctx)
	if err != nil {
		return err
	}
	groups, err := d.store.Groups(ctx, false)
	if err != nil {
		return err
	}
	var plan map[string]bool
	if groupID == "" {
		plan = core.ClearFocus(&vs)
	} else if plan, err = core.FocusGroup(&vs, groups, groupID); err != nil {
		return err
	}
	for _, g := range groups {
		if want, ok := plan[g.ID]; ok && want != g.Collapsed {
			if err := d.store.SetGroupCollapsed(ctx, g.ID, want); err != nil {
				return err
			}
		}
	}
	return d.store.PutViewState(ctx, vs)
}

// moveGroup swaps a group with its neighbor in the sidebar order (SPEC
// §8.2 K/J). Default stays last.
func (d *Daemon) moveGroup(ctx context.Context, id string, down bool) error {
	groups, err := d.store.Groups(ctx, false)
	if err != nil {
		return err
	}
	var order []*core.Group
	for _, g := range groups {
		if !g.IsDefault {
			order = append(order, g)
		}
	}
	sort.SliceStable(order, func(i, j int) bool { return order[i].SortKey < order[j].SortKey })
	at := -1
	for i, g := range order {
		if g.ID == id {
			at = i
		}
	}
	if at < 0 {
		return errors.New("that group can't be moved")
	}
	to := at - 1
	if down {
		to = at + 1
	}
	if to < 0 || to >= len(order) {
		return nil // already at the edge
	}
	order[at], order[to] = order[to], order[at]
	for i, g := range order {
		if g.SortKey != i+1 {
			if err := d.store.SetGroupSortKey(ctx, g.ID, i+1); err != nil {
				return err
			}
		}
	}
	return nil
}
