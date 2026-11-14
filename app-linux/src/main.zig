const std = @import("std");
const adw = @import("adw");
const gio = @import("gio");
const gobject = @import("gobject");
const gtk = @import("gtk");

fn onActivate(app: *adw.Application, _: ?*anyopaque) callconv(.c) void {
    const window = adw.ApplicationWindow.new(app.as(gtk.Application));
    const gtk_window = window.as(gtk.Window);
    gtk_window.setTitle("Agent Command");
    gtk_window.setDefaultSize(1200, 800);
    gtk_window.present();
}

pub fn main() u8 {
    const app = adw.Application.new("com.agentcommand.ui", .{});
    defer app.as(gobject.Object).unref();

    _ = gio.Application.signals.activate.connect(
        app,
        ?*anyopaque,
        &onActivate,
        null,
        .{},
    );

    const status = app.as(gio.Application).run(0, null);
    return @intCast(@min(255, @max(0, status)));
}

test "smoke" {
    try std.testing.expectEqual(@as(usize, 1), 1);
}
