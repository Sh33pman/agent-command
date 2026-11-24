//go:build linux && !noui

package linuxui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/diamondburned/gotk4-adwaita/pkg/adw"
	"github.com/diamondburned/gotk4/pkg/gdk/v4"
	"github.com/diamondburned/gotk4/pkg/gio/v2"
	"github.com/diamondburned/gotk4/pkg/glib/v2"
	"github.com/diamondburned/gotk4/pkg/gtk/v4"
	"github.com/oklog/ulid/v2"

	"github.com/brettmostert/agent-command/internal/core"
	"github.com/brettmostert/agent-command/internal/ipc"
)

// AppID is the desktop application ID for GNotification and .desktop integration.
const AppID = "com.brettmostert.agent-command"

type rowTarget struct {
	kind    SelectionKind
	id      string
	host    string
	groupID string
	isGroup bool
}

type uiApp struct {
	client *Client
	store  *Store
	app    *adw.Application
	win    *adw.ApplicationWindow
	toast  *adw.ToastOverlay

	split        *adw.NavigationSplitView
	list         *gtk.ListBox
	searchToggle *gtk.ToggleButton
	searchEntry  *gtk.SearchEntry
	statusLabel  *gtk.Label
	contentStack *gtk.Stack
	termStack    *gtk.Stack
	termTitle    *adw.WindowTitle
	harnessBox   *gtk.Box
	harnessTitle *adw.WindowTitle

	mu          sync.Mutex
	keymap      *Keymap
	themeName   string
	palette     Palette
	cssProvider *gtk.CSSProvider
	sel         Selection
	filter      string
	rowMap      map[int]rowTarget
	terminals   map[string]*VTETerminal // keyed by SessionKey(host, id)
	lastSeq     uint64
	acmdBin     string
	cancelling  bool
}

// Run starts the GTK4/libadwaita Linux UI and returns the process exit code.
func Run(opts Options) int {
	client, err := NewClient(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "agent-command-ui: %v\n", err)
		return 1
	}
	defer client.Close()

	store := NewStore()
	adwApp := adw.NewApplication(AppID, gio.ApplicationFlagsNone)

	km := DefaultKeymap()
	if kmPath, err := KeymapPath(); err == nil {
		if loaded, err := LoadKeymap(kmPath); err == nil {
			km = loaded
		}
	}

	themeName := ReadSavedTheme()
	palette := LoadTheme(themeName, ThemeFolders())

	u := &uiApp{
		client:    client,
		store:     store,
		app:       adwApp,
		keymap:    km,
		themeName: themeName,
		palette:   palette,
		rowMap:    make(map[int]rowTarget),
		terminals: make(map[string]*VTETerminal),
	}
	if bin, err := ResolveACMD(); err == nil {
		u.acmdBin = bin
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adwApp.ConnectActivate(func() {
		if u.win != nil {
			u.win.Present()
			return
		}
		u.buildWindow(ctx)
		u.win.Present()
		go u.client.RunStream(ctx, u.store, func(ev ipc.Event) {
			glib.IdleAdd(func() {
				u.onStreamEvent(ctx, ev)
			})
		}, func(state string, err error) {
			glib.IdleAdd(func() {
				u.onStreamStatus(state, err)
			})
		})
	})

	adwApp.ConnectShutdown(func() {
		u.mu.Lock()
		u.cancelling = true
		u.mu.Unlock()
		cancel()
	})

	return adwApp.Run(os.Args)
}

func (u *uiApp) buildWindow(ctx context.Context) {
	u.win = adw.NewApplicationWindow(&u.app.Application)
	u.win.SetTitle("Agent Command")
	u.win.SetDefaultSize(1200, 760)
	u.applyTheme(u.themeName, false)

	u.toast = adw.NewToastOverlay()
	u.split = adw.NewNavigationSplitView()
	u.split.SetMinSidebarWidth(240)
	u.split.SetMaxSidebarWidth(360)
	u.split.SetSidebarWidthFraction(0.25)

	u.split.SetSidebar(u.buildSidebar(ctx))
	u.split.SetContent(u.buildContent(ctx))

	u.toast.SetChild(u.split)
	u.win.SetContent(u.toast)

	u.win.NotifyProperty("is-active", func() {
		u.reportPresence(ctx)
	})

	winKey := gtk.NewEventControllerKey()
	winKey.SetPropagationPhase(gtk.PhaseCapture)
	winKey.ConnectKeyPressed(func(keyval, _ uint, state gdk.ModifierType) bool {
		return u.handleKey(ctx, keyval, state)
	})
	u.win.AddController(winKey)
}

func (u *uiApp) buildSidebar(ctx context.Context) *adw.NavigationPage {
	box := gtk.NewBox(gtk.OrientationVertical, 0)

	hdr := adw.NewHeaderBar()
	hdr.SetTitleWidget(gtk.NewLabel("Agent Command"))

	newBtn := gtk.NewButtonFromIconName("list-add-symbolic")
	newBtn.SetTooltipText("New Session")
	newBtn.ConnectClicked(func() {
		u.startNewSession(ctx, "", "")
	})
	hdr.PackStart(newBtn)

	u.searchToggle = gtk.NewToggleButton()
	u.searchToggle.SetIconName("system-search-symbolic")
	u.searchToggle.SetTooltipText("Filter sessions")
	hdr.PackEnd(u.searchToggle)

	harnessesBtn := gtk.NewButtonFromIconName("preferences-system-symbolic")
	harnessesBtn.SetTooltipText("Harnesses")
	harnessesBtn.ConnectClicked(func() {
		snap, _, ok := u.store.Snapshot()
		if !ok || len(snap.Harnesses) == 0 {
			u.showToast("No harnesses configured")
			return
		}
		u.mu.Lock()
		u.sel = Selection{Kind: SelectHarness, ID: snap.Harnesses[0].ID}
		u.mu.Unlock()
		u.split.SetShowContent(true)
		u.refreshContent(ctx)
	})
	hdr.PackEnd(harnessesBtn)

	themeBtn := u.buildThemeButton()
	hdr.PackEnd(themeBtn)

	box.Append(hdr)

	searchBar := gtk.NewSearchBar()
	u.searchEntry = gtk.NewSearchEntry()
	u.searchEntry.SetPlaceholderText("Filter sessions…")
	searchBar.SetChild(u.searchEntry)
	searchBar.ConnectEntry(u.searchEntry)
	u.searchToggle.ConnectToggled(func() {
		searchBar.SetSearchMode(u.searchToggle.Active())
	})
	u.searchEntry.ConnectSearchChanged(func() {
		u.mu.Lock()
		u.filter = u.searchEntry.Text()
		u.mu.Unlock()
		u.renderSidebar(ctx)
	})
	box.Append(searchBar)

	u.list = gtk.NewListBox()
	u.list.SetSelectionMode(gtk.SelectionSingle)
	u.list.AddCSSClass("navigation-sidebar")
	u.list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if row == nil {
			return
		}
		u.onRowActivated(ctx, row.Index())
	})

	scroll := gtk.NewScrolledWindow()
	scroll.SetVExpand(true)
	scroll.SetChild(u.list)
	box.Append(scroll)

	statusBar := gtk.NewBox(gtk.OrientationHorizontal, 6)
	statusBar.SetMarginStart(12)
	statusBar.SetMarginEnd(12)
	statusBar.SetMarginTop(6)
	statusBar.SetMarginBottom(6)
	u.statusLabel = gtk.NewLabel("Connecting…")
	u.statusLabel.AddCSSClass("dim-label")
	u.statusLabel.AddCSSClass("caption")
	u.statusLabel.SetXAlign(0)
	u.statusLabel.SetEllipsize(3) // PangoEllipsizeEnd
	statusBar.Append(u.statusLabel)
	box.Append(statusBar)

	return adw.NewNavigationPage(box, "Sessions")
}

func (u *uiApp) buildContent(ctx context.Context) *adw.NavigationPage {
	u.contentStack = gtk.NewStack()
	u.contentStack.SetHExpand(true)
	u.contentStack.SetVExpand(true)

	// Empty state
	emptyBox := gtk.NewBox(gtk.OrientationVertical, 0)
	emptyHdr := adw.NewHeaderBar()
	emptyHdr.SetTitleWidget(adw.NewWindowTitle("", ""))
	emptyBox.Append(emptyHdr)

	empty := adw.NewStatusPage()
	empty.SetVExpand(true)
	empty.SetIconName("utilities-terminal-symbolic")
	empty.SetTitle("No Session Selected")
	empty.SetDescription("Select a session or harness from the sidebar, or start a new one.")
	emptyBtn := gtk.NewButtonWithLabel("New Session")
	emptyBtn.AddCSSClass("suggested-action")
	emptyBtn.AddCSSClass("pill")
	emptyBtn.SetHAlign(gtk.AlignCenter)
	emptyBtn.ConnectClicked(func() {
		u.startNewSession(ctx, "", "")
	})
	empty.SetChild(emptyBtn)
	emptyBox.Append(empty)
	u.contentStack.AddNamed(emptyBox, "empty")

	// Terminal view
	termBox := gtk.NewBox(gtk.OrientationVertical, 0)
	termHdr := adw.NewHeaderBar()
	u.termTitle = adw.NewWindowTitle("Terminal", "")
	termHdr.SetTitleWidget(u.termTitle)

	copyBtn := gtk.NewButtonFromIconName("edit-copy-symbolic")
	copyBtn.SetTooltipText("Copy (Ctrl+Shift+C)")
	copyBtn.ConnectClicked(func() {
		if vt := u.activeTerminal(); vt != nil {
			vt.CopyClipboard()
		}
	})
	termHdr.PackEnd(copyBtn)

	pasteBtn := gtk.NewButtonFromIconName("edit-paste-symbolic")
	pasteBtn.SetTooltipText("Paste (Ctrl+Shift+V)")
	pasteBtn.ConnectClicked(func() {
		if vt := u.activeTerminal(); vt != nil {
			vt.PasteClipboard()
		}
	})
	termHdr.PackEnd(pasteBtn)

	finishBtn := gtk.NewButtonFromIconName("process-stop-symbolic")
	finishBtn.SetTooltipText("Finish Session")
	finishBtn.ConnectClicked(func() {
		u.finishSelectedSession(ctx)
	})
	termHdr.PackEnd(finishBtn)

	termBox.Append(termHdr)

	u.termStack = gtk.NewStack()
	u.termStack.SetHExpand(true)
	u.termStack.SetVExpand(true)
	termBox.Append(u.termStack)
	u.contentStack.AddNamed(termBox, "terminal")

	// Harness view
	harnessOuter := gtk.NewBox(gtk.OrientationVertical, 0)
	harnessHdr := adw.NewHeaderBar()
	u.harnessTitle = adw.NewWindowTitle("Harness", "")
	harnessHdr.SetTitleWidget(u.harnessTitle)
	harnessOuter.Append(harnessHdr)

	u.harnessBox = gtk.NewBox(gtk.OrientationVertical, 12)
	u.harnessBox.SetMarginTop(24)
	u.harnessBox.SetMarginBottom(24)
	u.harnessBox.SetMarginStart(24)
	u.harnessBox.SetMarginEnd(24)
	harnessScroll := gtk.NewScrolledWindow()
	harnessScroll.SetVExpand(true)
	harnessScroll.SetChild(u.harnessBox)
	harnessOuter.Append(harnessScroll)
	u.contentStack.AddNamed(harnessOuter, "harness")

	u.contentStack.SetVisibleChildName("empty")
	return adw.NewNavigationPage(u.contentStack, "Workspace")
}

func (u *uiApp) onStreamStatus(state string, err error) {
	if u.statusLabel == nil {
		return
	}
	if err != nil {
		u.statusLabel.SetText(fmt.Sprintf("%s: %v", state, err))
		return
	}
	u.statusLabel.SetText("Daemon: " + state)
	if state == "connected" && u.keymap != nil {
		payload := u.keymap.DaemonPayload()
		go func() {
			_ = u.client.Call(context.Background(), ipc.OpKeysSet, payload, nil)
		}()
	}
}

func (u *uiApp) onStreamEvent(ctx context.Context, ev ipc.Event) {
	u.renderSidebar(ctx)
	u.refreshContent(ctx)
	u.dispatchNotices()
}

func (u *uiApp) renderSidebar(ctx context.Context) {
	if u.list == nil {
		return
	}
	snap, _, ok := u.store.Snapshot()
	if !ok {
		return
	}

	u.mu.Lock()
	filter := u.filter
	sel := u.sel
	u.mu.Unlock()

	model := BuildSidebar(snap, filter)

	for {
		child := u.list.FirstChild()
		if child == nil {
			break
		}
		u.list.Remove(child)
	}

	newMap := make(map[int]rowTarget)
	idx := 0

	// Sessions section
	for _, r := range model.Rows {
		if r.IsHeader() {
			row := gtk.NewListBoxRow()
			row.SetSelectable(false)
			box := gtk.NewBox(gtk.OrientationHorizontal, 6)
			box.SetMarginTop(8)
			box.SetMarginBottom(4)
			box.SetMarginStart(8)
			box.SetMarginEnd(8)
			arrow := "▾"
			if r.Collapsed {
				arrow = "▸"
			}
			lbl := gtk.NewLabel(fmt.Sprintf("%s %s (%d)", arrow, r.Group.Name, r.Count))
			lbl.AddCSSClass("heading")
			lbl.SetXAlign(0)
			lbl.SetHExpand(true)
			box.Append(lbl)
			row.SetChild(box)
			u.list.Append(row)
			newMap[idx] = rowTarget{isGroup: true, groupID: r.Group.ID}
			idx++
			continue
		}

		ws := model.Sessions[SessionKey(r.Session.Host, r.Session.ID)]
		title := r.Session.Name
		if ws != nil && ws.DisplayName != "" {
			title = ws.DisplayName
		}
		if title == "" {
			title = r.Session.ID
		}

		row := gtk.NewListBoxRow()
		box := gtk.NewBox(gtk.OrientationHorizontal, 8)
		box.SetMarginTop(6)
		box.SetMarginBottom(6)
		box.SetMarginStart(8)
		box.SetMarginEnd(8)

		if r.Number > 0 {
			numLbl := gtk.NewLabel(fmt.Sprintf("%d", r.Number))
			numLbl.AddCSSClass("dim-label")
			numLbl.AddCSSClass("caption")
			box.Append(numLbl)
		}

		vBox := gtk.NewBox(gtk.OrientationVertical, 2)
		vBox.SetHExpand(true)
		nameLbl := gtk.NewLabel(title)
		nameLbl.SetXAlign(0)
		nameLbl.SetEllipsize(3) // PangoEllipsizeEnd
		vBox.Append(nameLbl)

		sub := StatusLabel(r.Session.Status)
		if r.Session.Host != "" {
			sub = r.Session.Host + " · " + sub
		}
		subLbl := gtk.NewLabel(sub)
		subLbl.SetXAlign(0)
		subLbl.AddCSSClass("dim-label")
		subLbl.AddCSSClass("caption")
		vBox.Append(subLbl)
		box.Append(vBox)

		if r.Session.Unseen {
			dot := gtk.NewLabel("●")
			dot.AddCSSClass("accent")
			box.Append(dot)
		}

		row.SetChild(box)
		u.list.Append(row)
		host := ""
		if ws != nil {
			host = ws.Host
		}
		newMap[idx] = rowTarget{kind: SelectSession, id: r.Session.ID, host: host}
		if sel.Kind == SelectSession && sel.ID == r.Session.ID && sel.Host == host {
			u.list.SelectRow(row)
		}
		idx++
	}

	u.mu.Lock()
	u.rowMap = newMap
	u.mu.Unlock()
	_ = ctx
}

func (u *uiApp) onRowActivated(ctx context.Context, index int) {
	u.mu.Lock()
	target, ok := u.rowMap[index]
	u.mu.Unlock()
	if !ok {
		return
	}

	if target.isGroup {
		snap, _, has := u.store.Snapshot()
		if !has {
			return
		}
		collapsed := false
		for _, id := range snap.ViewState.Collapsed {
			if id == target.groupID {
				collapsed = true
				break
			}
		}
		go func() {
			_ = u.client.Call(ctx, ipc.OpMutate, ipc.Mutation{
				Kind:    ipc.MutGroupCollapse,
				GroupID: target.groupID,
				Flag:    !collapsed,
			}, nil)
		}()
		return
	}

	u.mu.Lock()
	u.sel = Selection{Kind: target.kind, ID: target.id, Host: target.host}
	u.mu.Unlock()

	u.split.SetShowContent(true)
	u.refreshContent(ctx)
	u.reportPresence(ctx)

	if target.kind == SelectSession {
		go func() {
			_ = u.client.Call(ctx, ipc.OpMutate, ipc.Mutation{
				Kind:    ipc.MutSeen,
				Targets: []ipc.Target{{Host: target.host, ID: target.id}},
			}, nil)
		}()
	}
}

func (u *uiApp) refreshContent(ctx context.Context) {
	snap, _, ok := u.store.Snapshot()
	if !ok {
		return
	}
	u.mu.Lock()
	sel := u.sel
	u.mu.Unlock()

	switch sel.Kind {
	case SelectSession:
		s, found := FindSession(snap, sel.ID, sel.Host)
		if !found {
			u.contentStack.SetVisibleChildName("empty")
			return
		}
		u.showTerminalView(ctx, s)
	case SelectHarness:
		h, found := FindHarness(snap, sel.ID)
		if !found {
			u.contentStack.SetVisibleChildName("empty")
			return
		}
		u.showHarnessView(ctx, h)
	default:
		u.contentStack.SetVisibleChildName("empty")
	}
}

func (u *uiApp) showTerminalView(ctx context.Context, s *ipc.Session) {
	title := s.DisplayName
	if title == "" {
		title = s.Name
	}
	if title == "" {
		title = s.ID
	}
	u.termTitle.SetTitle(title)
	subtitle := StatusLabel(s.Status)
	if s.Cwd != "" {
		subtitle += " · " + s.Cwd
	}
	u.termTitle.SetSubtitle(subtitle)

	key := SessionKey(s.Host, s.ID)
	u.mu.Lock()
	vt, exists := u.terminals[key]
	pal := u.palette
	u.mu.Unlock()

	if !exists {
		if u.acmdBin == "" {
			if bin, err := ResolveACMD(); err == nil {
				u.acmdBin = bin
			}
		}
		if u.acmdBin == "" {
			u.showToast("acmd binary not found on PATH")
			return
		}
		vt = NewVTETerminal()
		vt.SetPalette(pal)
		vt.ConnectChildExited(func(_ int) {
			glib.IdleAdd(func() {
				u.mu.Lock()
				if u.cancelling {
					u.mu.Unlock()
					return
				}
				delete(u.terminals, key)
				u.mu.Unlock()
				u.termStack.Remove(vt.Widget())
			})
		})
		u.mu.Lock()
		u.terminals[key] = vt
		u.mu.Unlock()
		u.termStack.AddNamed(vt.Widget(), key)
		vt.SpawnAsync(s.Cwd, AttachArgv(u.acmdBin, s.ID))
	}

	u.termStack.SetVisibleChildName(key)
	u.contentStack.SetVisibleChildName("terminal")
	vt.GrabFocus()
	_ = ctx
}

func (u *uiApp) showHarnessView(ctx context.Context, h ipc.Harness) {
	u.harnessTitle.SetTitle(h.Name)
	u.harnessTitle.SetSubtitle(h.Title)

	for {
		child := u.harnessBox.FirstChild()
		if child == nil {
			break
		}
		u.harnessBox.Remove(child)
	}

	group := adw.NewPreferencesGroup()
	group.SetTitle("Harness Details")
	group.Add(makeInfoRow("Kind", h.Title))
	installed := "No"
	if h.Installed {
		installed = "Yes"
	}
	group.Add(makeInfoRow("Installed", installed))
	cmd := h.Command
	if cmd == "" {
		cmd = core.KindCommand(h.Kind)
	}
	group.Add(makeInfoRow("Command", cmd))
	if len(h.Args) > 0 {
		group.Add(makeInfoRow("Args", strings.Join(h.Args, " ")))
	}
	if h.Folder != "" {
		group.Add(makeInfoRow("Config Folder", h.Folder))
	} else if h.ConfigDir != "" {
		group.Add(makeInfoRow("Config Dir", h.ConfigDir))
	}
	u.harnessBox.Append(group)

	actions := gtk.NewBox(gtk.OrientationHorizontal, 12)
	newBtn := gtk.NewButtonWithLabel("New Session with Harness")
	newBtn.AddCSSClass("suggested-action")
	newBtn.SetSensitive(h.Installed)
	newBtn.ConnectClicked(func() {
		u.startNewSession(ctx, h.ID, "")
	})
	actions.Append(newBtn)

	rmBtn := gtk.NewButtonWithLabel("Remove Harness")
	rmBtn.AddCSSClass("destructive-action")
	rmBtn.ConnectClicked(func() {
		go func() {
			if err := u.client.Call(ctx, ipc.OpHarnessRemove, ipc.HarnessRef{ID: h.ID}, nil); err != nil {
				glib.IdleAdd(func() { u.showToast(err.Error()) })
				return
			}
			glib.IdleAdd(func() {
				u.mu.Lock()
				u.sel = Selection{}
				u.mu.Unlock()
				u.contentStack.SetVisibleChildName("empty")
			})
		}()
	})
	actions.Append(rmBtn)

	u.harnessBox.Append(actions)
	u.contentStack.SetVisibleChildName("harness")
}

func makeInfoRow(title, value string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	row.SetSubtitle(value)
	row.AddCSSClass("property")
	return row
}

func (u *uiApp) activeTerminal() *VTETerminal {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.sel.Kind != SelectSession {
		return nil
	}
	return u.terminals[SessionKey(u.sel.Host, u.sel.ID)]
}

func (u *uiApp) startNewSession(ctx context.Context, harnessID, harnessKind string) {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = os.Getenv("HOME")
	}
	reqID := ulid.Make().String()
	go func() {
		var s ipc.Session
		err := u.client.CallOnce(ctx, ipc.OpNew, reqID, ipc.NewRequest{
			Cwd:         cwd,
			HarnessID:   harnessID,
			HarnessKind: harnessKind,
		}, &s)
		if err != nil {
			glib.IdleAdd(func() { u.showToast(err.Error()) })
			return
		}
		glib.IdleAdd(func() {
			u.mu.Lock()
			u.sel = Selection{Kind: SelectSession, ID: s.ID, Host: s.Host}
			u.mu.Unlock()
			u.refreshContent(ctx)
		})
	}()
}

func (u *uiApp) handleKey(ctx context.Context, keyval uint, state gdk.ModifierType) bool {
	if vt := u.activeTerminal(); vt != nil {
		shift := state&gdk.ShiftMask != 0
		ctrl := state&gdk.ControlMask != 0
		if shift && !ctrl && (keyval == gdk.KEY_Return || keyval == gdk.KEY_KP_Enter) {
			vt.FeedChild([]byte("\x1b[13;2u"))
			return true
		}
	}
	if u.keymap == nil {
		return false
	}
	var mods ModMask
	if state&gdk.ControlMask != 0 {
		mods |= ModCtrl
	}
	if state&gdk.AltMask != 0 {
		mods |= ModAlt
	}
	if state&gdk.ShiftMask != 0 {
		mods |= ModShift
	}
	if state&gdk.SuperMask != 0 {
		mods |= ModSuper
	}
	name := gdk.KeyvalName(keyval)
	action, ok := u.keymap.Match(mods, name)
	if !ok {
		return false
	}
	switch action {
	case ActionNewSession:
		u.startNewSession(ctx, "", "")
		return true
	case ActionNewTerminal:
		u.startNewSession(ctx, "", core.HarnessTerminal)
		return true
	case ActionFilter:
		if u.searchToggle != nil {
			u.searchToggle.SetActive(!u.searchToggle.Active())
			if u.searchToggle.Active() && u.searchEntry != nil {
				u.searchEntry.GrabFocus()
			}
		}
		return true
	case ActionCopy:
		if vt := u.activeTerminal(); vt != nil {
			vt.CopyClipboard()
			return true
		}
	case ActionPaste:
		if vt := u.activeTerminal(); vt != nil {
			vt.PasteClipboard()
			return true
		}
	case ActionQuit:
		u.app.Quit()
		return true
	default:
		if strings.HasPrefix(action, "session_") {
			var n int
			if _, err := fmt.Sscanf(action, "session_%d", &n); err == nil && n >= 1 && n <= 9 {
				u.selectSessionNumber(ctx, n)
				return true
			}
		}
	}
	return false
}

func (u *uiApp) selectSessionNumber(ctx context.Context, num int) {
	snap, _, ok := u.store.Snapshot()
	if !ok {
		return
	}
	u.mu.Lock()
	filter := u.filter
	u.mu.Unlock()
	model := BuildSidebar(snap, filter)
	for _, r := range model.Rows {
		if !r.IsHeader() && r.Number == num && r.Session != nil {
			host := ""
			if ws := model.Sessions[SessionKey(r.Session.Host, r.Session.ID)]; ws != nil {
				host = ws.Host
			}
			u.mu.Lock()
			u.sel = Selection{Kind: SelectSession, ID: r.Session.ID, Host: host}
			u.mu.Unlock()
			u.split.SetShowContent(true)
			u.renderSidebar(ctx)
			u.refreshContent(ctx)
			u.reportPresence(ctx)
			return
		}
	}
}

func (u *uiApp) finishSelectedSession(ctx context.Context) {
	u.mu.Lock()
	sel := u.sel
	u.mu.Unlock()
	if sel.Kind != SelectSession {
		return
	}
	go func() {
		if err := u.client.Call(ctx, ipc.OpMutate, ipc.Mutation{
			Kind:    ipc.MutFinish,
			Targets: []ipc.Target{{Host: sel.Host, ID: sel.ID}},
		}, nil); err != nil {
			glib.IdleAdd(func() { u.showToast(err.Error()) })
		}
	}()
}

func (u *uiApp) reportPresence(ctx context.Context) {
	if u.win == nil {
		return
	}
	presence := ipc.PresenceBackground
	if u.win.IsActive() {
		presence = ipc.PresenceActive
	}
	u.mu.Lock()
	focus := ""
	if u.sel.Kind == SelectSession {
		focus = u.sel.ID
	}
	u.mu.Unlock()

	go func() {
		_ = u.client.SetPresence(ctx, presence, focus)
	}()
}

func (u *uiApp) dispatchNotices() {
	snap, _, ok := u.store.Snapshot()
	if !ok {
		return
	}
	u.mu.Lock()
	after := u.lastSeq
	u.mu.Unlock()

	notices, maxSeq := NoticesForClient(snap.Notices, u.client.ID(), after)
	if maxSeq > after {
		u.mu.Lock()
		u.lastSeq = maxSeq
		u.mu.Unlock()
	}
	for _, n := range notices {
		if n.Kind == "withdraw" {
			u.app.WithdrawNotification(n.SessionID)
			continue
		}
		notif := gio.NewNotification(n.Title)
		if n.Body != "" {
			notif.SetBody(n.Body)
		}
		u.app.SendNotification(n.SessionID, notif)
	}
}

func (u *uiApp) showToast(msg string) {
	if u.toast == nil || msg == "" {
		return
	}
	u.toast.AddToast(adw.NewToast(msg))
}

func (u *uiApp) applyTheme(name string, save bool) {
	pal := LoadTheme(name, ThemeFolders())
	u.mu.Lock()
	u.themeName = name
	u.palette = pal
	terms := make([]*VTETerminal, 0, len(u.terminals))
	for _, vt := range u.terminals {
		terms = append(terms, vt)
	}
	u.mu.Unlock()

	if u.cssProvider == nil {
		u.cssProvider = gtk.NewCSSProvider()
		if disp := gdk.DisplayGetDefault(); disp != nil {
			gtk.StyleContextAddProviderForDisplay(disp, u.cssProvider, gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
		}
	}
	u.cssProvider.LoadFromString(ThemeCSS(pal))

	for _, vt := range terms {
		vt.SetPalette(pal)
	}
	if save {
		if err := SaveTheme(name); err != nil {
			u.showToast(fmt.Sprintf("save theme: %v", err))
		}
	}
}

func (u *uiApp) buildThemeButton() *gtk.MenuButton {
	btn := gtk.NewMenuButton()
	btn.SetIconName("applications-graphics-symbolic")
	btn.SetTooltipText("Ghostty Theme")

	pop := gtk.NewPopover()
	box := gtk.NewBox(gtk.OrientationVertical, 6)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(8)
	box.SetMarginEnd(8)
	box.SetSizeRequest(260, 340)

	search := gtk.NewSearchEntry()
	search.SetPlaceholderText("Search themes…")
	box.Append(search)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingle)

	scroll := gtk.NewScrolledWindow()
	scroll.SetVExpand(true)
	scroll.SetChild(list)
	box.Append(scroll)

	allThemes := append([]string{DefaultThemeLabel}, DiscoverThemes(ThemeFolders())...)
	var visible []string

	rebuild := func(query string) {
		for {
			c := list.FirstChild()
			if c == nil {
				break
			}
			list.Remove(c)
		}
		visible = visible[:0]
		q := strings.ToLower(strings.TrimSpace(query))
		u.mu.Lock()
		cur := u.themeName
		u.mu.Unlock()
		for _, name := range allThemes {
			if q != "" && !strings.Contains(strings.ToLower(name), q) {
				continue
			}
			visible = append(visible, name)
			row := gtk.NewListBoxRow()
			lbl := gtk.NewLabel(name)
			lbl.SetXAlign(0)
			lbl.SetMarginTop(4)
			lbl.SetMarginBottom(4)
			lbl.SetMarginStart(6)
			lbl.SetMarginEnd(6)
			row.SetChild(lbl)
			list.Append(row)
			if (cur == "" && name == DefaultThemeLabel) || cur == name {
				list.SelectRow(row)
			}
		}
	}

	rebuild("")
	search.ConnectSearchChanged(func() {
		rebuild(search.Text())
	})
	list.ConnectRowActivated(func(row *gtk.ListBoxRow) {
		if row == nil {
			return
		}
		idx := row.Index()
		if idx < 0 || idx >= len(visible) {
			return
		}
		chosen := visible[idx]
		if chosen == DefaultThemeLabel {
			chosen = ""
		}
		u.applyTheme(chosen, true)
		pop.Popdown()
	})

	pop.SetChild(box)
	btn.SetPopover(pop)
	return btn
}
