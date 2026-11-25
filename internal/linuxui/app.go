//go:build linux && !noui

package linuxui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/jwijenbergh/puregotk/v4/adw"
	"github.com/jwijenbergh/puregotk/v4/gdk"
	"github.com/jwijenbergh/puregotk/v4/gio"
	"github.com/jwijenbergh/puregotk/v4/glib"
	"github.com/jwijenbergh/puregotk/v4/gobject"
	"github.com/jwijenbergh/puregotk/v4/gtk"
	"github.com/jwijenbergh/puregotk/v4/pango"
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
	winPath     string
	winState    WindowState
	themeName   string
	fontFamily  string
	fontSize    int
	palette     Palette
	cssProvider *gtk.CssProvider
	sel         Selection
	filter      string
	rowMap      map[int]rowTarget
	terminals   map[string]*VTETerminal // keyed by SessionKey(host, id)
	lastSeq     uint64
	acmdBin     string
	closing     bool
	cbs         []any // keep signal callbacks alive
}

func (u *uiApp) idle(fn func()) {
	cb := glib.SourceFunc(func(_ uintptr) bool {
		fn()
		return false
	})
	u.mu.Lock()
	u.cbs = append(u.cbs, &cb)
	u.mu.Unlock()
	glib.IdleAdd(&cb, 0)
}

func keep[T any](u *uiApp, cb T) *T {
	u.mu.Lock()
	u.cbs = append(u.cbs, &cb)
	u.mu.Unlock()
	return &cb
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
	adwApp := adw.NewApplication(AppID, gio.GApplicationFlagsNoneValue)

	km := DefaultKeymap()
	if kmPath, err := KeymapPath(); err == nil {
		if loaded, err := LoadKeymap(kmPath); err == nil {
			km = loaded
		}
	}
	themeName := ReadSavedTheme()
	fontFamily, fontSize := ReadSavedFont()
	palette := LoadTheme(themeName, ThemeFolders())
	winPath, _ := WindowStatePath()
	winState := LoadWindowState(winPath)

	u := &uiApp{
		client:     client,
		store:      store,
		app:        adwApp,
		keymap:     km,
		winPath:    winPath,
		winState:   winState,
		themeName:  themeName,
		fontFamily: fontFamily,
		fontSize:   fontSize,
		palette:    palette,
		rowMap:     make(map[int]rowTarget),
		terminals:  make(map[string]*VTETerminal),
	}
	if bin, err := ResolveACMD(); err == nil {
		u.acmdBin = bin
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	adwApp.ConnectActivate(keep(u, func(_ gio.Application) {
		if u.win != nil {
			u.win.Present()
			return
		}
		u.buildWindow(ctx)
		u.win.Present()
		go u.client.RunStream(ctx, u.store, func(_ ipc.Event) {
			u.idle(func() {
				u.onStreamEvent(ctx)
			})
		}, func(state string, err error) {
			u.idle(func() {
				u.onStreamStatus(state, err)
			})
		})
	}))

	adwApp.ConnectShutdown(keep(u, func(_ gio.Application) {
		u.mu.Lock()
		u.closing = true
		u.mu.Unlock()
		cancel()
	}))

	return int(adwApp.Run(int32(min(len(os.Args), 1<<16)), os.Args)) // #nosec G115 -- bounded to 1<<16
}

func (u *uiApp) buildWindow(ctx context.Context) {
	u.win = adw.NewApplicationWindow(&u.app.Application)
	u.win.SetTitle("Agent Command")
	w := min(max(u.winState.Width, 320), 16384)
	h := min(max(u.winState.Height, 240), 16384)
	u.win.SetDefaultSize(int32(w), int32(h)) // #nosec G115 -- clamped to 320..16384
	if u.winState.Maximized {
		u.win.Maximize()
	}
	u.applyTheme(u.themeName, false)

	u.toast = adw.NewToastOverlay()
	u.split = adw.NewNavigationSplitView()
	u.split.SetMinSidebarWidth(240)
	u.split.SetMaxSidebarWidth(360)
	u.split.SetSidebarWidthFraction(0.25)

	u.split.SetSidebar(u.buildSidebar(ctx))
	u.split.SetContent(u.buildContent(ctx))

	u.toast.SetChild(&u.split.Widget)
	u.win.SetContent(&u.toast.Widget)

	u.win.ConnectNotify(keep(u, func(_ gobject.Object, pspec uintptr) {
		name := gobject.ParamSpecNewFromInternalPtr(pspec).GetName()
		switch name {
		case "is-active":
			u.reportPresence(ctx)
		case "default-width", "default-height", "maximized":
			u.saveWindowState()
		}
	}))
	u.win.ConnectCloseRequest(keep(u, func(_ gtk.Window) bool {
		u.saveWindowState()
		return false
	}))

	winKey := gtk.NewEventControllerKey()
	winKey.SetPropagationPhase(gtk.PhaseCaptureValue)
	winKey.ConnectKeyPressed(keep(u, func(_ gtk.EventControllerKey, keyval, _ uint32, state gdk.ModifierType) bool {
		return u.handleKey(ctx, keyval, state)
	}))
	u.win.AddController(&winKey.EventController)
}

func (u *uiApp) buildSidebar(ctx context.Context) *adw.NavigationPage {
	box := gtk.NewBox(gtk.OrientationVerticalValue, 0)

	hdr := adw.NewHeaderBar()
	hdrLbl := gtk.NewLabel("Agent Command")
	hdr.SetTitleWidget(&hdrLbl.Widget)

	newBtn := gtk.NewButtonFromIconName("list-add-symbolic")
	newBtn.SetTooltipText("New Session")
	newBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		u.startNewSession(ctx, "", "")
	}))
	hdr.PackStart(&newBtn.Widget)

	u.searchToggle = gtk.NewToggleButton()
	u.searchToggle.SetIconName("system-search-symbolic")
	u.searchToggle.SetTooltipText("Filter sessions")
	hdr.PackEnd(&u.searchToggle.Widget)

	harnessesBtn := gtk.NewButtonFromIconName("preferences-system-symbolic")
	harnessesBtn.SetTooltipText("Harnesses")
	harnessesBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
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
	}))
	hdr.PackEnd(&harnessesBtn.Widget)

	themeBtn := u.buildThemeButton()
	hdr.PackEnd(&themeBtn.Widget)

	box.Append(&hdr.Widget)

	searchBar := gtk.NewSearchBar()
	u.searchEntry = gtk.NewSearchEntry()
	u.searchEntry.SetPlaceholderText("Filter sessions…")
	searchBar.SetChild(&u.searchEntry.Widget)
	searchBar.ConnectEntry(u.searchEntry)
	u.searchToggle.ConnectToggled(keep(u, func(_ gtk.ToggleButton) {
		searchBar.SetSearchMode(u.searchToggle.GetActive())
	}))
	u.searchEntry.ConnectSearchChanged(keep(u, func(_ gtk.SearchEntry) {
		u.mu.Lock()
		u.filter = u.searchEntry.GetText()
		u.mu.Unlock()
		u.renderSidebar(ctx)
	}))
	box.Append(&searchBar.Widget)

	u.list = gtk.NewListBox()
	u.list.SetSelectionMode(gtk.SelectionSingleValue)
	u.list.AddCssClass("navigation-sidebar")
	u.list.ConnectRowActivated(keep(u, func(_ gtk.ListBox, rowPtr uintptr) {
		if rowPtr == 0 {
			return
		}
		row := gtk.ListBoxRowNewFromInternalPtr(rowPtr)
		u.onRowActivated(ctx, int(row.GetIndex()))
	}))

	scroll := gtk.NewScrolledWindow()
	scroll.SetVexpand(true)
	scroll.SetChild(&u.list.Widget)
	box.Append(&scroll.Widget)

	statusBar := gtk.NewBox(gtk.OrientationHorizontalValue, 6)
	statusBar.SetMarginStart(12)
	statusBar.SetMarginEnd(12)
	statusBar.SetMarginTop(6)
	statusBar.SetMarginBottom(6)
	u.statusLabel = gtk.NewLabel("Connecting…")
	u.statusLabel.AddCssClass("dim-label")
	u.statusLabel.AddCssClass("caption")
	u.statusLabel.SetXalign(0)
	u.statusLabel.SetEllipsize(pango.EllipsizeEndValue)
	statusBar.Append(&u.statusLabel.Widget)
	box.Append(&statusBar.Widget)

	return adw.NewNavigationPage(&box.Widget, "Sessions")
}

func (u *uiApp) buildContent(ctx context.Context) *adw.NavigationPage {
	u.contentStack = gtk.NewStack()
	u.contentStack.SetHexpand(true)
	u.contentStack.SetVexpand(true)

	// Empty state
	emptyBox := gtk.NewBox(gtk.OrientationVerticalValue, 0)
	emptyHdr := adw.NewHeaderBar()
	emptyTitle := adw.NewWindowTitle("", "")
	emptyHdr.SetTitleWidget(&emptyTitle.Widget)
	emptyBox.Append(&emptyHdr.Widget)

	empty := adw.NewStatusPage()
	empty.SetVexpand(true)
	empty.SetIconName("utilities-terminal-symbolic")
	empty.SetTitle("No Session Selected")
	empty.SetDescription("Select a session or harness from the sidebar, or start a new one.")
	emptyBtn := gtk.NewButtonWithLabel("New Session")
	emptyBtn.AddCssClass("suggested-action")
	emptyBtn.AddCssClass("pill")
	emptyBtn.SetHalign(gtk.AlignCenterValue)
	emptyBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		u.startNewSession(ctx, "", "")
	}))
	empty.SetChild(&emptyBtn.Widget)
	emptyBox.Append(&empty.Widget)
	u.contentStack.AddNamed(&emptyBox.Widget, "empty")

	// Terminal view
	termBox := gtk.NewBox(gtk.OrientationVerticalValue, 0)
	termHdr := adw.NewHeaderBar()
	u.termTitle = adw.NewWindowTitle("Terminal", "")
	termHdr.SetTitleWidget(&u.termTitle.Widget)

	copyBtn := gtk.NewButtonFromIconName("edit-copy-symbolic")
	copyBtn.SetTooltipText("Copy (Ctrl+Shift+C)")
	copyBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		if vt := u.activeTerminal(); vt != nil {
			vt.CopyClipboard()
		}
	}))
	termHdr.PackEnd(&copyBtn.Widget)

	pasteBtn := gtk.NewButtonFromIconName("edit-paste-symbolic")
	pasteBtn.SetTooltipText("Paste (Ctrl+Shift+V)")
	pasteBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		if vt := u.activeTerminal(); vt != nil {
			vt.PasteClipboard()
		}
	}))
	termHdr.PackEnd(&pasteBtn.Widget)

	finishBtn := gtk.NewButtonFromIconName("process-stop-symbolic")
	finishBtn.SetTooltipText("Finish Session")
	finishBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		u.finishSelectedSession(ctx)
	}))
	termHdr.PackEnd(&finishBtn.Widget)

	termBox.Append(&termHdr.Widget)

	u.termStack = gtk.NewStack()
	u.termStack.SetHexpand(true)
	u.termStack.SetVexpand(true)
	termBox.Append(&u.termStack.Widget)
	u.contentStack.AddNamed(&termBox.Widget, "terminal")

	// Harness view
	harnessOuter := gtk.NewBox(gtk.OrientationVerticalValue, 0)
	harnessHdr := adw.NewHeaderBar()
	u.harnessTitle = adw.NewWindowTitle("Harness", "")
	harnessHdr.SetTitleWidget(&u.harnessTitle.Widget)
	harnessOuter.Append(&harnessHdr.Widget)

	u.harnessBox = gtk.NewBox(gtk.OrientationVerticalValue, 12)
	u.harnessBox.SetMarginTop(24)
	u.harnessBox.SetMarginBottom(24)
	u.harnessBox.SetMarginStart(24)
	u.harnessBox.SetMarginEnd(24)
	harnessScroll := gtk.NewScrolledWindow()
	harnessScroll.SetVexpand(true)
	harnessScroll.SetChild(&u.harnessBox.Widget)
	harnessOuter.Append(&harnessScroll.Widget)
	u.contentStack.AddNamed(&harnessOuter.Widget, "harness")

	u.contentStack.SetVisibleChildName("empty")
	return adw.NewNavigationPage(&u.contentStack.Widget, "Workspace")
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

func (u *uiApp) onStreamEvent(ctx context.Context) {
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
		child := u.list.GetFirstChild()
		if child == nil {
			break
		}
		u.list.Remove(child)
	}

	newMap := make(map[int]rowTarget)
	idx := 0

	for _, r := range model.Rows {
		if r.IsHeader() {
			row := gtk.NewListBoxRow()
			row.SetSelectable(false)
			box := gtk.NewBox(gtk.OrientationHorizontalValue, 6)
			box.SetMarginTop(8)
			box.SetMarginBottom(4)
			box.SetMarginStart(8)
			box.SetMarginEnd(8)
			arrow := "▾"
			if r.Collapsed {
				arrow = "▸"
			}
			lbl := gtk.NewLabel(fmt.Sprintf("%s %s (%d)", arrow, r.Group.Name, r.Count))
			lbl.AddCssClass("heading")
			if r.Group.Color != "" {
				lbl.AddCssClass("group-" + r.Group.Color)
			}
			lbl.SetXalign(0)
			lbl.SetHexpand(true)
			box.Append(&lbl.Widget)
			row.SetChild(&box.Widget)
			u.list.Append(&row.Widget)
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
		box := gtk.NewBox(gtk.OrientationHorizontalValue, 8)
		box.SetMarginTop(6)
		box.SetMarginBottom(6)
		box.SetMarginStart(8)
		box.SetMarginEnd(8)

		if r.Number > 0 {
			numLbl := gtk.NewLabel(fmt.Sprintf("%d", r.Number))
			numLbl.AddCssClass("dim-label")
			numLbl.AddCssClass("caption")
			box.Append(&numLbl.Widget)
		}

		vBox := gtk.NewBox(gtk.OrientationVerticalValue, 2)
		vBox.SetHexpand(true)
		nameLbl := gtk.NewLabel(title)
		nameLbl.SetXalign(0)
		nameLbl.SetEllipsize(pango.EllipsizeEndValue)
		vBox.Append(&nameLbl.Widget)

		sub := StatusLabel(r.Session.Status)
		if r.Session.Host != "" {
			sub = r.Session.Host + " · " + sub
		}
		subLbl := gtk.NewLabel(sub)
		subLbl.SetXalign(0)
		switch r.Session.Status {
		case core.StatusWaiting:
			subLbl.AddCssClass("status-waiting")
		case core.StatusError:
			subLbl.AddCssClass("status-error")
		default:
			subLbl.AddCssClass("dim-label")
		}
		subLbl.AddCssClass("caption")
		vBox.Append(&subLbl.Widget)
		box.Append(&vBox.Widget)

		if r.Session.Unseen {
			dot := gtk.NewLabel("●")
			dot.AddCssClass("accent")
			box.Append(&dot.Widget)
		}

		row.SetChild(&box.Widget)
		u.list.Append(&row.Widget)
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
	fontDesc := pangoFontDesc(u.fontFamily, u.fontSize)
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
		if fontDesc != "" {
			vt.SetFont(fontDesc)
		}
		vt.ConnectChildExited(func(_ int) {
			u.idle(func() {
				u.mu.Lock()
				if u.closing {
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
		if existing := u.termStack.GetChildByName(key); existing != nil {
			u.termStack.Remove(existing)
		}
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
		child := u.harnessBox.GetFirstChild()
		if child == nil {
			break
		}
		u.harnessBox.Remove(child)
	}

	group := adw.NewPreferencesGroup()
	group.SetTitle("Harness Details")
	group.Add(&makeInfoRow("Kind", h.Title).Widget)
	installed := "No"
	if h.Installed {
		installed = "Yes"
	}
	group.Add(&makeInfoRow("Installed", installed).Widget)
	cmd := h.Command
	if cmd == "" {
		cmd = core.KindCommand(h.Kind)
	}
	group.Add(&makeInfoRow("Command", cmd).Widget)
	if len(h.Args) > 0 {
		group.Add(&makeInfoRow("Args", strings.Join(h.Args, " ")).Widget)
	}
	if h.Folder != "" {
		group.Add(&makeInfoRow("Config Folder", h.Folder).Widget)
	} else if h.ConfigDir != "" {
		group.Add(&makeInfoRow("Config Dir", h.ConfigDir).Widget)
	}
	u.harnessBox.Append(&group.Widget)

	actions := gtk.NewBox(gtk.OrientationHorizontalValue, 12)
	newBtn := gtk.NewButtonWithLabel("New Session with Harness")
	newBtn.AddCssClass("suggested-action")
	newBtn.SetSensitive(h.Installed)
	newBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		u.startNewSession(ctx, h.ID, "")
	}))
	actions.Append(&newBtn.Widget)

	rmBtn := gtk.NewButtonWithLabel("Remove Harness")
	rmBtn.AddCssClass("destructive-action")
	rmBtn.ConnectClicked(keep(u, func(_ gtk.Button) {
		go func() {
			if err := u.client.Call(ctx, ipc.OpHarnessRemove, ipc.HarnessRef{ID: h.ID}, nil); err != nil {
				u.idle(func() { u.showToast(err.Error()) })
				return
			}
			u.idle(func() {
				u.mu.Lock()
				u.sel = Selection{}
				u.mu.Unlock()
				u.contentStack.SetVisibleChildName("empty")
			})
		}()
	}))
	actions.Append(&rmBtn.Widget)

	u.harnessBox.Append(&actions.Widget)
	u.contentStack.SetVisibleChildName("harness")
}

func makeInfoRow(title, value string) *adw.ActionRow {
	row := adw.NewActionRow()
	row.SetTitle(title)
	row.SetSubtitle(value)
	row.AddCssClass("property")
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
			u.idle(func() { u.showToast(err.Error()) })
			return
		}
		u.idle(func() {
			u.mu.Lock()
			u.sel = Selection{Kind: SelectSession, ID: s.ID, Host: s.Host}
			u.mu.Unlock()
			u.refreshContent(ctx)
		})
	}()
}

func (u *uiApp) handleKey(ctx context.Context, keyval uint32, state gdk.ModifierType) bool {
	if vt := u.activeTerminal(); vt != nil {
		shift := state&gdk.ShiftMaskValue != 0
		ctrl := state&gdk.ControlMaskValue != 0
		if shift && !ctrl && (keyval == uint32(gdk.KEY_Return) || keyval == uint32(gdk.KEY_KP_Enter)) {
			vt.FeedChild([]byte("\x1b[13;2u"))
			return true
		}
	}
	if u.keymap == nil {
		return false
	}
	var mods ModMask
	if state&gdk.ControlMaskValue != 0 {
		mods |= ModCtrl
	}
	if state&gdk.AltMaskValue != 0 {
		mods |= ModAlt
	}
	if state&gdk.ShiftMaskValue != 0 {
		mods |= ModShift
	}
	if state&gdk.SuperMaskValue != 0 {
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
			u.searchToggle.SetActive(!u.searchToggle.GetActive())
			if u.searchToggle.GetActive() && u.searchEntry != nil {
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
		u.saveWindowState()
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
			u.idle(func() { u.showToast(err.Error()) })
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

func (u *uiApp) saveWindowState() {
	if u.win == nil || u.winPath == "" {
		return
	}
	maximized := u.win.IsMaximized()
	u.mu.Lock()
	ws := u.winState
	ws.Maximized = maximized
	if !maximized {
		if w := int(u.win.GetWidth()); w > 0 {
			ws.Width = w
		}
		if h := int(u.win.GetHeight()); h > 0 {
			ws.Height = h
		}
	}
	u.winState = ws
	path := u.winPath
	u.mu.Unlock()
	_ = SaveWindowState(path, ws)
}

func (u *uiApp) applyTheme(name string, save bool) {
	pal := LoadTheme(name, ThemeFolders())
	u.mu.Lock()
	u.themeName = name
	u.palette = pal
	fontFamily := u.fontFamily
	fontSize := u.fontSize
	terms := make([]*VTETerminal, 0, len(u.terminals))
	for _, vt := range u.terminals {
		terms = append(terms, vt)
	}
	u.mu.Unlock()

	if u.cssProvider == nil {
		u.cssProvider = gtk.NewCssProvider()
		if disp := gdk.DisplayGetDefault(); disp != nil {
			gtk.StyleContextAddProviderForDisplay(disp, u.cssProvider, uint32(gtk.STYLE_PROVIDER_PRIORITY_APPLICATION))
		}
	}
	u.cssProvider.LoadFromString(ThemeCSS(pal, fontFamily, fontSize))

	fontDesc := pangoFontDesc(fontFamily, fontSize)
	for _, vt := range terms {
		vt.SetPalette(pal)
		if fontDesc != "" {
			vt.SetFont(fontDesc)
		}
	}
	if save {
		if err := SaveTheme(name); err != nil {
			u.showToast(fmt.Sprintf("save theme: %v", err))
		}
	}
}

func pangoFontDesc(family string, size int) string {
	switch {
	case family != "" && size > 0:
		return fmt.Sprintf("%s %d", family, size)
	case family != "":
		return family
	case size > 0:
		return fmt.Sprintf("Monospace %d", size)
	default:
		return ""
	}
}

func (u *uiApp) buildThemeButton() *gtk.Button {
	btn := gtk.NewButtonFromIconName("applications-graphics-symbolic")
	btn.SetTooltipText("Ghostty Theme")
	btn.ConnectClicked(keep(u, func(_ gtk.Button) {
		u.openThemeDialog()
	}))
	return btn
}

func (u *uiApp) openThemeDialog() {
	if u.win == nil {
		return
	}
	dlg := adw.NewDialog()
	dlg.SetTitle("Ghostty Theme")
	dlg.SetContentWidth(340)
	dlg.SetContentHeight(420)

	tv := adw.NewToolbarView()
	hdr := adw.NewHeaderBar()
	tv.AddTopBar(&hdr.Widget)

	box := gtk.NewBox(gtk.OrientationVerticalValue, 6)
	box.SetMarginTop(8)
	box.SetMarginBottom(8)
	box.SetMarginStart(12)
	box.SetMarginEnd(12)

	search := gtk.NewSearchEntry()
	search.SetPlaceholderText("Search themes…")
	box.Append(&search.Widget)

	list := gtk.NewListBox()
	list.SetSelectionMode(gtk.SelectionSingleValue)
	list.AddCssClass("navigation-sidebar")

	scroll := gtk.NewScrolledWindow()
	scroll.SetVexpand(true)
	scroll.SetChild(&list.Widget)
	box.Append(&scroll.Widget)

	allThemes := append([]string{DefaultThemeLabel}, DiscoverThemes(ThemeFolders())...)
	var visible []string
	u.mu.Lock()
	savedTheme := u.themeName
	u.mu.Unlock()
	committed := false
	rebuilding := false

	rebuild := func(query string) {
		rebuilding = true
		defer func() { rebuilding = false }()
		for {
			c := list.GetFirstChild()
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
			rowBox := gtk.NewBox(gtk.OrientationHorizontalValue, 8)
			rowBox.SetMarginTop(4)
			rowBox.SetMarginBottom(4)
			rowBox.SetMarginStart(6)
			rowBox.SetMarginEnd(6)
			lbl := gtk.NewLabel(name)
			lbl.SetXalign(0)
			lbl.SetHexpand(true)
			rowBox.Append(&lbl.Widget)
			if (savedTheme == "" && name == DefaultThemeLabel) || savedTheme == name {
				curLbl := gtk.NewLabel("current")
				curLbl.AddCssClass("dim-label")
				curLbl.AddCssClass("caption")
				rowBox.Append(&curLbl.Widget)
			}
			row.SetChild(&rowBox.Widget)
			list.Append(&row.Widget)
			if (cur == "" && name == DefaultThemeLabel) || cur == name {
				list.SelectRow(row)
			}
		}
	}

	themeForIdx := func(idx int) (string, bool) {
		if idx < 0 || idx >= len(visible) {
			return "", false
		}
		chosen := visible[idx]
		if chosen == DefaultThemeLabel {
			chosen = ""
		}
		return chosen, true
	}

	rebuild("")
	search.ConnectSearchChanged(keep(u, func(_ gtk.SearchEntry) {
		rebuild(search.GetText())
	}))
	list.ConnectRowSelected(keep(u, func(_ gtk.ListBox, rowPtr uintptr) {
		if rebuilding || rowPtr == 0 {
			return
		}
		row := gtk.ListBoxRowNewFromInternalPtr(rowPtr)
		if chosen, ok := themeForIdx(int(row.GetIndex())); ok {
			u.applyTheme(chosen, false)
		}
	}))
	list.ConnectRowActivated(keep(u, func(_ gtk.ListBox, rowPtr uintptr) {
		if rowPtr == 0 {
			return
		}
		row := gtk.ListBoxRowNewFromInternalPtr(rowPtr)
		if chosen, ok := themeForIdx(int(row.GetIndex())); ok {
			committed = true
			u.applyTheme(chosen, true)
			dlg.Close()
		}
	}))
	dlg.ConnectClosed(keep(u, func(_ adw.Dialog) {
		if !committed {
			u.applyTheme(savedTheme, false)
		}
	}))

	tv.SetContent(&box.Widget)
	dlg.SetChild(&tv.Widget)
	dlg.Present(&u.win.Widget)
	search.GrabFocus()
}
