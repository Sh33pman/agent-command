const std = @import("std");

pub const Env = struct {
    acmd_config_dir: ?[]const u8 = null,
    xdg_config_home: ?[]const u8 = null,
    home: ?[]const u8 = null,

    pub fn fromProcess() Env {
        return .{
            .acmd_config_dir = getEnv("ACMD_CONFIG_DIR"),
            .xdg_config_home = getEnv("XDG_CONFIG_HOME"),
            .home = getEnv("HOME"),
        };
    }

    fn getEnv(name: [:0]const u8) ?[]const u8 {
        const ptr = std.c.getenv(name.ptr) orelse return null;
        const slice = std.mem.span(ptr);
        return if (slice.len == 0) null else slice;
    }
};

pub const UiPrefs = struct {
    sidebar_width: i32 = 260,
    window_width: i32 = 1200,
    window_height: i32 = 800,
    theme: []const u8 = "",
    keybindings: std.StringHashMapUnmanaged([]const []const u8) = .empty,

    // Track whether `theme` was heap-allocated (vs default "").
    theme_owned: bool = false,

    pub fn deinit(self: *UiPrefs, allocator: std.mem.Allocator) void {
        if (self.theme_owned) {
            allocator.free(self.theme);
            self.theme = "";
            self.theme_owned = false;
        }
        var it = self.keybindings.iterator();
        while (it.next()) |entry| {
            allocator.free(entry.key_ptr.*);
            for (entry.value_ptr.*) |combo| {
                allocator.free(combo);
            }
            allocator.free(entry.value_ptr.*);
        }
        self.keybindings.deinit(allocator);
    }

    pub fn setTheme(self: *UiPrefs, allocator: std.mem.Allocator, value: []const u8) !void {
        const copy = try allocator.dupe(u8, value);
        if (self.theme_owned) allocator.free(self.theme);
        self.theme = copy;
        self.theme_owned = true;
    }

    pub fn setKeybinding(
        self: *UiPrefs,
        allocator: std.mem.Allocator,
        action: []const u8,
        combos: []const []const u8,
    ) !void {
        var dup_combos = try allocator.alloc([]const u8, combos.len);
        var built: usize = 0;
        errdefer {
            for (dup_combos[0..built]) |c| allocator.free(c);
            allocator.free(dup_combos);
        }
        for (combos, 0..) |combo, i| {
            dup_combos[i] = try allocator.dupe(u8, combo);
            built += 1;
        }

        if (self.keybindings.getEntry(action)) |entry| {
            for (entry.value_ptr.*) |old| allocator.free(old);
            allocator.free(entry.value_ptr.*);
            entry.value_ptr.* = dup_combos;
            return;
        }

        const key_copy = try allocator.dupe(u8, action);
        errdefer allocator.free(key_copy);
        try self.keybindings.put(allocator, key_copy, dup_combos);
    }
};

/// Resolves `$ACMD_CONFIG_DIR/ui.json`, `$XDG_CONFIG_HOME/agent-command/ui.json`,
/// or `$HOME/.config/agent-command/ui.json`.
pub fn resolvePrefsPath(allocator: std.mem.Allocator, env: Env) ![]u8 {
    if (env.acmd_config_dir) |dir| {
        return std.fs.path.join(allocator, &.{ dir, "ui.json" });
    }
    if (env.xdg_config_home) |xdg| {
        return std.fs.path.join(allocator, &.{ xdg, "agent-command", "ui.json" });
    }
    if (env.home) |home| {
        return std.fs.path.join(allocator, &.{ home, ".config", "agent-command", "ui.json" });
    }
    return error.MissingHomeDir;
}

pub fn load(allocator: std.mem.Allocator, io: std.Io, path: []const u8) !UiPrefs {
    const data = std.Io.Dir.cwd().readFileAlloc(io, path, allocator, .limited(1024 * 1024)) catch |err| switch (err) {
        error.FileNotFound => return UiPrefs{},
        else => return err,
    };
    defer allocator.free(data);

    return parseJson(allocator, data);
}

pub fn parseJson(allocator: std.mem.Allocator, json_bytes: []const u8) !UiPrefs {
    var prefs = UiPrefs{};
    errdefer prefs.deinit(allocator);

    var parsed = try std.json.parseFromSlice(std.json.Value, allocator, json_bytes, .{});
    defer parsed.deinit();

    if (parsed.value != .object) return error.InvalidPrefsFormat;
    const obj = parsed.value.object;

    if (obj.get("sidebar_width")) |v| {
        if (v == .integer) prefs.sidebar_width = @intCast(@max(120, @min(800, v.integer)));
    }
    if (obj.get("window_width")) |v| {
        if (v == .integer) prefs.window_width = @intCast(@max(400, @min(8192, v.integer)));
    }
    if (obj.get("window_height")) |v| {
        if (v == .integer) prefs.window_height = @intCast(@max(300, @min(8192, v.integer)));
    }
    if (obj.get("theme")) |v| {
        if (v == .string) try prefs.setTheme(allocator, v.string);
    }
    if (obj.get("keybindings")) |v| {
        if (v == .object) {
            var it = v.object.iterator();
            while (it.next()) |entry| {
                if (entry.value_ptr.* != .array) continue;
                var list: std.ArrayList([]const u8) = .empty;
                defer list.deinit(allocator);
                for (entry.value_ptr.*.array.items) |item| {
                    if (item == .string) try list.append(allocator, item.string);
                }
                try prefs.setKeybinding(allocator, entry.key_ptr.*, list.items);
            }
        }
    }

    return prefs;
}

pub fn save(allocator: std.mem.Allocator, io: std.Io, path: []const u8, prefs: *const UiPrefs) !void {
    if (std.fs.path.dirname(path)) |dir| {
        try std.Io.Dir.cwd().createDirPath(io, dir);
    }

    const encoded = try encodeJson(allocator, prefs);
    defer allocator.free(encoded);

    var atomic = try std.Io.Dir.cwd().createFileAtomic(io, path, .{ .replace = true });
    defer atomic.deinit(io);
    try atomic.file.writeStreamingAll(io, encoded);
    try atomic.replace(io);
}

pub fn encodeJson(allocator: std.mem.Allocator, prefs: *const UiPrefs) ![]u8 {
    var out: std.Io.Writer.Allocating = .init(allocator);
    errdefer out.deinit();

    var jw: std.json.Stringify = .{
        .writer = &out.writer,
        .options = .{ .whitespace = .indent_2 },
    };

    try jw.beginObject();
    try jw.objectField("sidebar_width");
    try jw.write(prefs.sidebar_width);
    try jw.objectField("window_width");
    try jw.write(prefs.window_width);
    try jw.objectField("window_height");
    try jw.write(prefs.window_height);
    try jw.objectField("theme");
    try jw.write(prefs.theme);

    if (prefs.keybindings.count() > 0) {
        try jw.objectField("keybindings");
        try jw.beginObject();
        var it = prefs.keybindings.iterator();
        while (it.next()) |entry| {
            try jw.objectField(entry.key_ptr.*);
            try jw.beginArray();
            for (entry.value_ptr.*) |combo| {
                try jw.write(combo);
            }
            try jw.endArray();
        }
        try jw.endObject();
    }

    try jw.endObject();
    try out.writer.writeByte('\n');
    return out.toOwnedSlice();
}

test "resolvePrefsPath respects ACMD_CONFIG_DIR, XDG_CONFIG_HOME, and HOME" {
    const allocator = std.testing.allocator;

    const p1 = try resolvePrefsPath(allocator, .{
        .acmd_config_dir = "/custom/acmd",
        .xdg_config_home = "/xdg",
        .home = "/home/u",
    });
    defer allocator.free(p1);
    try std.testing.expectEqualStrings("/custom/acmd/ui.json", p1);

    const p2 = try resolvePrefsPath(allocator, .{
        .xdg_config_home = "/xdg",
        .home = "/home/u",
    });
    defer allocator.free(p2);
    try std.testing.expectEqualStrings("/xdg/agent-command/ui.json", p2);

    const p3 = try resolvePrefsPath(allocator, .{
        .home = "/home/u",
    });
    defer allocator.free(p3);
    try std.testing.expectEqualStrings("/home/u/.config/agent-command/ui.json", p3);
}

test "load missing file returns defaults" {
    const allocator = std.testing.allocator;
    var prefs = try load(allocator, std.testing.io, "/tmp/nonexistent-acmd-ui-prefs-test.json");
    defer prefs.deinit(allocator);

    try std.testing.expectEqual(@as(i32, 260), prefs.sidebar_width);
    try std.testing.expectEqual(@as(i32, 1200), prefs.window_width);
    try std.testing.expectEqual(@as(i32, 800), prefs.window_height);
    try std.testing.expectEqualStrings("", prefs.theme);
    try std.testing.expectEqual(@as(usize, 0), prefs.keybindings.count());
}

test "save and load round-trips preferences and keybindings" {
    const allocator = std.testing.allocator;
    const io = std.testing.io;

    var tmp = std.testing.tmpDir(.{});
    defer tmp.cleanup();

    var dir_buf: [std.fs.max_path_bytes]u8 = undefined;
    const dir_len = try tmp.dir.realPath(io, &dir_buf);
    const dir_path = dir_buf[0..dir_len];
    const prefs_path = try std.fs.path.join(allocator, &.{ dir_path, "nested", "ui.json" });
    defer allocator.free(prefs_path);

    var original = UiPrefs{
        .sidebar_width = 320,
        .window_width = 1440,
        .window_height = 900,
    };
    defer original.deinit(allocator);
    try original.setTheme(allocator, "Tokyo Night");
    try original.setKeybinding(allocator, "focusSidebar", &.{"ctrl+shift+b"});
    try original.setKeybinding(allocator, "up", &.{ "up", "ctrl+k", "k" });

    try save(allocator, io, prefs_path, &original);

    var loaded = try load(allocator, io, prefs_path);
    defer loaded.deinit(allocator);

    try std.testing.expectEqual(@as(i32, 320), loaded.sidebar_width);
    try std.testing.expectEqual(@as(i32, 1440), loaded.window_width);
    try std.testing.expectEqual(@as(i32, 900), loaded.window_height);
    try std.testing.expectEqualStrings("Tokyo Night", loaded.theme);

    const focus_keys = loaded.keybindings.get("focusSidebar").?;
    try std.testing.expectEqual(@as(usize, 1), focus_keys.len);
    try std.testing.expectEqualStrings("ctrl+shift+b", focus_keys[0]);

    const up_keys = loaded.keybindings.get("up").?;
    try std.testing.expectEqual(@as(usize, 3), up_keys.len);
    try std.testing.expectEqualStrings("ctrl+k", up_keys[1]);
}
