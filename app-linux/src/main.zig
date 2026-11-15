const std = @import("std");
const adw = @import("adw");
const gio = @import("gio");
const gobject = @import("gobject");
const gtk = @import("gtk");

pub const prefs = @import("config/prefs.zig");
pub const ipc = @import("ipc/client.zig");

const AppState = struct {
    allocator: std.mem.Allocator,
    io: std.Io,
    ui_prefs: prefs.UiPrefs,
    prefs_path: ?[]u8,
    window: ?*gtk.Window = null,
    watch_loop: ?*ipc.WatchLoop = null,
    alive: std.atomic.Value(bool) = std.atomic.Value(bool).init(true),
};

fn onSnapshot(_: *ipc.Decoded(ipc.Snapshot), _: ?*anyopaque) void {
    // PR 4 will update the sidebar and status bar from incoming snapshots.
}

fn onCloseRequest(window: *gtk.Window, userdata: ?*anyopaque) callconv(.c) c_int {
    const raw = userdata orelse return 0;
    const state: *AppState = @ptrCast(@alignCast(raw));

    var w: c_int = 0;
    var h: c_int = 0;
    window.getDefaultSize(&w, &h);
    if ((w < 400 or h < 300) and window.isMaximized() == 0 and window.isFullscreen() == 0) {
        const widget = window.as(gtk.Widget);
        w = widget.getWidth();
        h = widget.getHeight();
    }
    if (w >= 400 and h >= 300) {
        state.ui_prefs.window_width = w;
        state.ui_prefs.window_height = h;
    }

    if (state.prefs_path) |path| {
        prefs.save(state.allocator, state.io, path, &state.ui_prefs) catch {};
    }
    state.alive.store(false, .release);
    if (state.watch_loop) |loop| {
        loop.stop();
        state.watch_loop = null;
    }
    state.window = null;
    return 0; // Propagate close.
}

fn onActivate(app: *adw.Application, userdata: ?*anyopaque) callconv(.c) void {
    const raw = userdata orelse return;
    const state: *AppState = @ptrCast(@alignCast(raw));

    if (state.window) |existing| {
        existing.present();
        return;
    }

    const window = adw.ApplicationWindow.new(app.as(gtk.Application));
    const gtk_window = window.as(gtk.Window);
    state.window = gtk_window;
    gtk_window.setTitle("Agent Command");
    gtk_window.setDefaultSize(state.ui_prefs.window_width, state.ui_prefs.window_height);

    _ = gtk.Window.signals.close_request.connect(
        gtk_window,
        ?*anyopaque,
        &onCloseRequest,
        state,
        .{},
    );

    if (state.watch_loop == null) {
        if (ipc.resolveSocketPath(state.allocator, ipc.Env.fromProcess())) |sock_path| {
            defer state.allocator.free(sock_path);
            const acmd_bin = ipc.resolveAcmdPath(state.allocator, state.io) catch null;
            defer if (acmd_bin) |b| state.allocator.free(b);
            state.watch_loop = ipc.WatchLoop.start(
                state.io,
                sock_path,
                acmd_bin orelse "acmd",
                "0.1.0",
                &onSnapshot,
                state,
                &state.alive,
            ) catch null;
        } else |_| {}
    }

    gtk_window.present();
}

pub fn main(init: std.process.Init) u8 {
    const allocator = init.gpa;
    const io = init.io;

    const prefs_path = prefs.resolvePrefsPath(allocator, prefs.Env.fromProcess()) catch null;
    defer if (prefs_path) |p| allocator.free(p);

    var state = AppState{
        .allocator = allocator,
        .io = io,
        .ui_prefs = if (prefs_path) |p|
            prefs.load(allocator, io, p) catch prefs.UiPrefs{}
        else
            prefs.UiPrefs{},
        .prefs_path = prefs_path,
    };
    defer state.ui_prefs.deinit(allocator);
    defer {
        state.alive.store(false, .release);
        if (state.watch_loop) |loop| loop.stop();
    }

    const app = adw.Application.new("com.agentcommand.ui", .{});
    defer app.as(gobject.Object).unref();

    _ = gio.Application.signals.activate.connect(
        app,
        ?*anyopaque,
        &onActivate,
        &state,
        .{},
    );

    const status = app.as(gio.Application).run(0, null);
    return @intCast(@min(255, @max(0, status)));
}

comptime {
    _ = prefs;
    _ = ipc;
}

test "smoke" {
    try std.testing.expectEqual(@as(usize, 1), 1);
}
