const std = @import("std");
const glib = @import("glib");

/// The protocol versions this UI speaks (internal/ipc.ProtoMin, Proto): it
/// uses the highest it shares with the daemon (design 8.2).
pub const proto_min: i32 = 2;
pub const proto_version: i32 = 2;
pub const max_sun_path: usize = 104;
pub const max_line: usize = 2 << 20; // 2 MiB, matching internal/ipc/ipc.go MaxLine

pub const Env = struct {
    acmd_runtime_dir: ?[]const u8 = null,
    xdg_runtime_dir: ?[]const u8 = null,
    acmd_state_dir: ?[]const u8 = null,
    xdg_state_home: ?[]const u8 = null,
    home: ?[]const u8 = null,

    pub fn fromProcess() Env {
        return .{
            .acmd_runtime_dir = getEnv("ACMD_RUNTIME_DIR"),
            .xdg_runtime_dir = getEnv("XDG_RUNTIME_DIR"),
            .acmd_state_dir = getEnv("ACMD_STATE_DIR"),
            .xdg_state_home = getEnv("XDG_STATE_HOME"),
            .home = getEnv("HOME"),
        };
    }

    fn getEnv(name: [:0]const u8) ?[]const u8 {
        const ptr = std.c.getenv(name.ptr) orelse return null;
        const slice = std.mem.span(ptr);
        return if (slice.len == 0) null else slice;
    }
};

/// Mirrors `internal/paths/paths.go` socket resolution on Linux.
pub fn resolveSocketPath(allocator: std.mem.Allocator, env: Env) ![]u8 {
    if (env.acmd_runtime_dir) |dir| {
        return std.fs.path.join(allocator, &.{ dir, "daemon.sock" });
    }

    // With XDG_RUNTIME_DIR the sockets are there, unless that path is too long
    // for a socket.
    if (env.xdg_runtime_dir) |xdg| {
        const candidate = try std.fs.path.join(allocator, &.{ xdg, "agent-command", "daemon.sock" });
        if (candidate.len < max_sun_path) {
            return candidate;
        }
        allocator.free(candidate);
    }

    // Without it (cron, some SSH and container sessions), and when it is too
    // long, they are in run/ in the state folder. Never in the temp folder:
    // on Linux that is the shared /tmp, where another user could make the
    // folder first.
    const state_dir = if (env.acmd_state_dir) |s|
        try allocator.dupe(u8, s)
    else if (env.xdg_state_home) |xdg_state|
        try std.fs.path.join(allocator, &.{ xdg_state, "agent-command" })
    else if (env.home) |home|
        try std.fs.path.join(allocator, &.{ home, ".local", "state", "agent-command" })
    else
        return error.MissingHomeDir;
    defer allocator.free(state_dir);

    return std.fs.path.join(allocator, &.{ state_dir, "run", "daemon.sock" });
}

pub const Hello = struct {
    proto: i32 = proto_version,
    proto_min: i32 = proto_min,
    proto_max: i32 = proto_version,
    version: []const u8,
    pid: i32 = 0,
    /// The daemon's ops, in its reply: what this UI can use.
    ops: ?[]const []const u8 = null,
};

pub const WatchRequest = struct {
    rev: u64,
    wait_ms: i32 = 25000,
};

pub const WatchResponse = struct {
    rev: u64 = 0,
};

/// Mirrors Go `ipc.Target`: a session, group or set on its host (null for
/// the machine the daemon is on). Ids are unique only within their host.
pub const Target = struct {
    host: ?[]const u8 = null,
    id: []const u8,
};

pub const Mutation = struct {
    kind: []const u8,
    /// The sessions it changes, each on its host.
    targets: ?[]const Target = null,
    /// The host its group, set and neighbor are on (null: this machine).
    host: ?[]const u8 = null,
    group_id: ?[]const u8 = null,
    text: ?[]const u8 = null,
    color: ?[]const u8 = null,
    flag: ?bool = null,
};

/// Mirrors Go `ipc.Session` (`internal/ipc/wire.go`): snake_case, and values
/// that aren't set are left out.
pub const Session = struct {
    id: []const u8,
    name: []const u8 = "",
    description: []const u8 = "",
    group_id: []const u8 = "",
    parent: []const u8 = "",
    order: i32 = 0,
    cwd: []const u8 = "",
    status: []const u8 = "",
    status_detail: []const u8 = "",
    unseen: bool = false,
    kind: []const u8 = "",
    /// claude, antigravity or terminal, and the harness it runs in ("" for a terminal).
    harness_kind: []const u8 = "",
    harness_id: []const u8 = "",
    pid: i32 = 0,
    host: []const u8 = "",
    created_at: []const u8 = "",
    last_event_at: []const u8 = "",
    archived_at: []const u8 = "",
    display_name: []const u8 = "",
};

/// Mirrors Go `ipc.Harness`: what sessions run in, a kind (claude,
/// antigravity) with its own name. This UI only shows them.
pub const Harness = struct {
    id: []const u8,
    host: []const u8 = "",
    name: []const u8 = "",
    kind: []const u8 = "",
    title: []const u8 = "",
    installed: bool = false,
};

/// Mirrors Go `ipc.Group`.
pub const Group = struct {
    id: []const u8,
    /// The Remote host it's on ("" for this machine's): ids are unique only
    /// within their host.
    host: []const u8 = "",
    name: []const u8 = "",
    color: []const u8 = "",
    order: i32 = 0,
    is_default: bool = false,
    created_at: []const u8 = "",
    archived_at: []const u8 = "",
};

/// Mirrors Go `ipc.ViewState`.
pub const ViewState = struct {
    mode: []const u8 = "groups",
    sort: []const u8 = "created",
    desc: bool = false,
    filter: []const u8 = "",
    focused_group: []const u8 = "",
    show_archived: bool = false,
};

/// Mirrors Go `ipc.Snapshot` (`internal/ipc/ipc.go`).
pub const Snapshot = struct {
    sessions: ?[]const Session = null,
    groups: ?[]const Group = null,
    sets: ?[]const EntityRef = null,
    /// Entities of types with no list of their own, which this UI may not
    /// know (design 7.2): kept for their children's sake.
    others: ?[]const EntityRef = null,
    harnesses: ?[]const Harness = null,
    view_state: ViewState = .{},

    /// What a session runs in, as a name: its harness's, its kind's when that
    /// was removed (or it's a host's), or "Terminal".
    pub fn harnessName(self: Snapshot, s: Session) []const u8 {
        if (s.host.len == 0 and s.harness_id.len > 0) {
            for (self.harnesses orelse &.{}) |h| {
                if (std.mem.eql(u8, h.id, s.harness_id)) return h.name;
            }
        }
        if (std.mem.eql(u8, s.harness_kind, "terminal")) return "Terminal";
        if (std.mem.eql(u8, s.harness_kind, "antigravity")) return "Antigravity";
        return "Claude";
    }

    /// The group a session shows in (design 7.2): the one it names, else its
    /// nearest ancestor that's a group, following parents through sets and
    /// entities of types this UI doesn't know, on its own host. null: its
    /// host's Default.
    pub fn groupOf(self: Snapshot, s: Session) ?[]const u8 {
        if (s.group_id.len > 0) return s.group_id;
        var at = s.parent;
        var hops: usize = 0;
        while (at.len > 0 and hops < 16) : (hops += 1) {
            for (self.groups orelse &.{}) |g| {
                if (std.mem.eql(u8, g.host, s.host) and std.mem.eql(u8, g.id, at)) return g.id;
            }
            at = parentOf(self.sets, s.host, at) orelse parentOf(self.others, s.host, at) orelse return null;
        }
        return null;
    }

    fn parentOf(list: ?[]const EntityRef, host: []const u8, id: []const u8) ?[]const u8 {
        for (list orelse &.{}) |e| {
            if (std.mem.eql(u8, e.host, host) and std.mem.eql(u8, e.id, id)) return e.parent;
        }
        return null;
    }
};

/// What every entity has (design 7.1): its type, id, host and parent.
pub const EntityRef = struct {
    type: []const u8 = "",
    id: []const u8 = "",
    host: []const u8 = "",
    parent: []const u8 = "",
};

const RawResponse = struct {
    ok: bool = false,
    @"error": ?[]const u8 = null,
    /// What kind of error (internal/errcode): every failed response has one.
    code: ?[]const u8 = null,
    data: ?std.json.Value = null,
};

pub fn Decoded(comptime T: type) type {
    return struct {
        value: T,
        parsed: std.json.Parsed(T),

        pub fn deinit(self: *@This()) void {
            self.parsed.deinit();
        }
    };
}

pub fn encodeRequest(allocator: std.mem.Allocator, op: []const u8, data: anytype) ![]u8 {
    var out: std.Io.Writer.Allocating = .init(allocator);
    errdefer out.deinit();

    var jw: std.json.Stringify = .{
        .writer = &out.writer,
        .options = .{ .emit_null_optional_fields = false },
    };
    try jw.beginObject();
    try jw.objectField("op");
    try jw.write(op);
    if (@TypeOf(data) != @TypeOf(null)) {
        try jw.objectField("data");
        try jw.write(data);
    }
    try jw.endObject();
    try out.writer.writeByte('\n');
    return out.toOwnedSlice();
}

pub fn decodeResponse(comptime T: type, allocator: std.mem.Allocator, line: []const u8) !Decoded(T) {
    const trimmed = std.mem.trimEnd(u8, line, "\r\n");

    var envelope = try std.json.parseFromSlice(RawResponse, allocator, trimmed, .{
        .ignore_unknown_fields = true,
    });
    defer envelope.deinit();

    if (!envelope.value.ok) {
        if (envelope.value.@"error") |msg| {
            std.log.warn("daemon error ({s}): {s}", .{ envelope.value.code orelse "internal", msg });
        }
        return error.DaemonRemoteError;
    }

    if (T == void) {
        const parsed = try std.json.parseFromSlice(void, allocator, "null", .{});
        return .{ .value = {}, .parsed = parsed };
    }

    const data_val = envelope.value.data orelse return error.MissingResponseData;
    const data_json = try std.json.Stringify.valueAlloc(allocator, data_val, .{});
    defer allocator.free(data_json);

    const parsed = try std.json.parseFromSlice(T, allocator, data_json, .{
        .ignore_unknown_fields = true,
        .allocate = .alloc_always,
    });
    return .{ .value = parsed.value, .parsed = parsed };
}

pub const Client = struct {
    allocator: std.mem.Allocator,
    io: std.Io,
    stream: std.Io.net.Stream,
    daemon_hello: Decoded(Hello),

    pub fn initFromStream(
        allocator: std.mem.Allocator,
        io: std.Io,
        stream: std.Io.net.Stream,
        hello: Hello,
    ) !Client {
        var client = Client{
            .allocator = allocator,
            .io = io,
            .stream = stream,
            .daemon_hello = undefined,
        };
        client.daemon_hello = try client.call(Hello, "hello", hello);
        errdefer client.daemon_hello.deinit();

        // A daemon with no version in common refuses the hello
        // (proto_unsupported); one that picked a version this UI doesn't
        // speak is refused here.
        const agreed = client.daemon_hello.value.proto;
        if (agreed < proto_min or agreed > proto_version) {
            return error.ProtocolMismatch;
        }
        return client;
    }

    pub fn connect(
        allocator: std.mem.Allocator,
        io: std.Io,
        socket_path: []const u8,
        hello: Hello,
    ) !Client {
        const addr = try std.Io.net.UnixAddress.init(socket_path);
        const stream = try addr.connect(io);
        errdefer stream.close(io);
        return initFromStream(allocator, io, stream, hello);
    }

    fn reapChild(child_val: std.process.Child, io: std.Io) void {
        var child = child_val;
        _ = child.wait(io) catch {};
    }

    fn connectUnixCancellable(
        socket_path: []const u8,
        running: ?*const std.atomic.Value(bool),
    ) !std.Io.net.Stream {
        var sa: std.c.sockaddr.un = .{ .family = std.c.AF.UNIX, .path = undefined };
        if (socket_path.len >= sa.path.len) return error.NameTooLong;
        @memset(&sa.path, 0);
        @memcpy(sa.path[0..socket_path.len], socket_path);
        const addr_len: std.c.socklen_t = @intCast(@offsetOf(std.c.sockaddr.un, "path") + socket_path.len + 1);

        const fd = std.c.socket(std.c.AF.UNIX, std.c.SOCK.STREAM | std.c.SOCK.CLOEXEC | std.c.SOCK.NONBLOCK, 0);
        if (fd < 0) return error.SocketFailed;
        errdefer _ = std.c.close(fd);

        if (std.c.connect(fd, @ptrCast(&sa), addr_len) < 0) {
            const err_code: std.c.E = @enumFromInt(std.c._errno().*);
            switch (err_code) {
                .CONNREFUSED => return error.ConnectionRefused,
                .NOENT => return error.FileNotFound,
                // On Linux AF_UNIX, EAGAIN means the listener backlog is full (unconnected), not pending.
                .AGAIN => return error.WouldBlock,
                .INPROGRESS => {
                    var pfd = [1]std.posix.pollfd{.{
                        .fd = fd,
                        .events = std.posix.POLL.OUT,
                        .revents = 0,
                    }};
                    var waited_ms: u32 = 0;
                    const max_wait_ms: u32 = 2000;
                    while (waited_ms < max_wait_ms) {
                        if (running) |r| {
                            if (!r.load(.acquire)) return error.Cancelled;
                        }
                        const n = try std.posix.poll(&pfd, 100);
                        if (n > 0) {
                            var so_err: c_int = 0;
                            var opt_len: std.c.socklen_t = @sizeOf(c_int);
                            if (std.c.getsockopt(fd, std.c.SOL.SOCKET, std.c.SO.ERROR, @ptrCast(&so_err), &opt_len) < 0) {
                                return error.ConnectFailed;
                            }
                            if (so_err != 0) {
                                return switch (@as(std.c.E, @enumFromInt(so_err))) {
                                    .CONNREFUSED => error.ConnectionRefused,
                                    .NOENT => error.FileNotFound,
                                    else => error.ConnectFailed,
                                };
                            }
                            break;
                        }
                        waited_ms += 100;
                    } else {
                        return error.TimedOut;
                    }
                },
                else => return error.ConnectFailed,
            }
        }

        // Restore blocking mode so std.Io.net.Stream reader/writer work normally;
        // shutdown(fd, SHUT_RDWR) from stop() still unblocks them immediately.
        const flags = std.c.fcntl(fd, std.c.F.GETFL, @as(c_int, 0));
        if (flags >= 0) {
            const nonblock_bit: c_int = @bitCast(std.c.O{ .NONBLOCK = true });
            _ = std.c.fcntl(fd, std.c.F.SETFL, flags & ~nonblock_bit);
        }

        return .{ .socket = .{
            .handle = fd,
            .address = .{ .ip4 = .loopback(0) },
        } };
    }

    fn sleepCancellable(io: std.Io, total_ms: u64, running: ?*const std.atomic.Value(bool)) !void {
        var remaining = total_ms;
        while (remaining > 0) {
            if (running) |r| {
                if (!r.load(.acquire)) return error.Cancelled;
            }
            const step: u64 = @min(remaining, 100);
            try io.sleep(.fromMilliseconds(@intCast(step)), .awake);
            remaining -= step;
        }
    }

    /// Connects to `socket_path` without performing the `hello` exchange yet,
    /// spawning `acmd_bin daemon` and retrying with backoff if `acmd_bin` is
    /// provided and the daemon is not yet listening. Stops early if `running` flips false.
    fn connectStream(
        io: std.Io,
        socket_path: []const u8,
        acmd_bin: ?[]const u8,
        running: ?*const std.atomic.Value(bool),
    ) !std.Io.net.Stream {
        return connectUnixCancellable(socket_path, running) catch |err| switch (err) {
            error.ConnectionRefused, error.FileNotFound => {
                const bin = acmd_bin orelse return err;
                if (running) |r| {
                    if (!r.load(.acquire)) return error.Cancelled;
                }
                const child = try std.process.spawn(io, .{
                    .argv = &.{ bin, "daemon" },
                    .stdin = .ignore,
                    .stdout = .ignore,
                    .stderr = .ignore,
                    .pgid = 0, // Detach from UI's foreground process group so terminal SIGINT doesn't kill the daemon.
                });
                if (std.Thread.spawn(.{}, reapChild, .{ child, io })) |reaper| {
                    reaper.detach();
                } else |_| {}

                const backoffs_ms = [_]u64{ 50, 100, 200, 400, 800, 1000 };
                for (backoffs_ms) |ms| {
                    try sleepCancellable(io, ms, running);
                    if (running) |r| {
                        if (!r.load(.acquire)) return error.Cancelled;
                    }
                    if (connectUnixCancellable(socket_path, running)) |stream| {
                        return stream;
                    } else |retry_err| switch (retry_err) {
                        error.ConnectionRefused, error.FileNotFound, error.WouldBlock => continue,
                        else => return retry_err,
                    }
                }
                return connectUnixCancellable(socket_path, running);
            },
            else => return err,
        };
    }

    /// Connects to `socket_path`, spawning `acmd_bin daemon` and retrying with
    /// backoff if the daemon is not yet listening.
    pub fn connectOrStart(
        allocator: std.mem.Allocator,
        io: std.Io,
        socket_path: []const u8,
        hello: Hello,
        acmd_bin: []const u8,
    ) !Client {
        const stream = try connectStream(io, socket_path, acmd_bin, null);
        errdefer stream.close(io);
        return initFromStream(allocator, io, stream, hello);
    }

    pub fn deinit(self: *Client) void {
        self.daemon_hello.deinit();
        self.stream.close(self.io);
    }

    pub fn call(self: *Client, comptime T: type, op: []const u8, payload: anytype) !Decoded(T) {
        const req_line = try encodeRequest(self.allocator, op, payload);
        defer self.allocator.free(req_line);

        var write_buf: [4096]u8 = undefined;
        var w = self.stream.writer(self.io, &write_buf);
        try w.interface.writeAll(req_line);
        try w.interface.flush();

        var read_buf: [65536]u8 = undefined;
        var r = self.stream.reader(self.io, &read_buf);
        var line_out: std.Io.Writer.Allocating = .init(self.allocator);
        defer line_out.deinit();

        _ = try r.interface.streamDelimiterLimit(&line_out.writer, '\n', .limited(max_line));
        // Consume the trailing '\n' if present.
        if (r.interface.peekByte()) |b| {
            if (b == '\n') r.interface.toss(1);
        } else |_| {}

        return decodeResponse(T, self.allocator, line_out.written());
    }

    pub fn snapshot(self: *Client) !Decoded(Snapshot) {
        return self.call(Snapshot, "snapshot", null);
    }

    pub fn mutate(self: *Client, mut: Mutation) !void {
        var res = try self.call(void, "mutate", mut);
        res.deinit();
    }

    pub fn watch(self: *Client, req: WatchRequest) !WatchResponse {
        var res = try self.call(WatchResponse, "watch", req);
        defer res.deinit();
        return res.value;
    }
};

/// Resolves `acmd` sibling to the running executable first, falling back to `"acmd"` on `PATH`.
pub fn resolveAcmdPath(allocator: std.mem.Allocator, io: std.Io) ![]u8 {
    if (std.process.executableDirPathAlloc(io, allocator)) |exe_dir| {
        defer allocator.free(exe_dir);
        const sibling = try std.fs.path.join(allocator, &.{ exe_dir, "acmd" });
        if (std.Io.Dir.cwd().access(io, sibling, .{ .execute = true })) |_| {
            return sibling;
        } else |_| {
            allocator.free(sibling);
        }
    } else |_| {}
    return allocator.dupe(u8, "acmd");
}

pub const SnapshotCallback = *const fn (snapshot: *Decoded(Snapshot), userdata: ?*anyopaque) void;

const IdlePayload = struct {
    allocator: std.mem.Allocator,
    snapshot: Decoded(Snapshot),
    callback: SnapshotCallback,
    userdata: ?*anyopaque,
    active: *std.atomic.Value(bool),
};

fn onIdleDispatch(ptr: ?*anyopaque) callconv(.c) c_int {
    const raw = ptr orelse return 0;
    const payload: *IdlePayload = @ptrCast(@alignCast(raw));
    defer {
        payload.snapshot.deinit();
        payload.allocator.destroy(payload);
    }
    if (payload.active.load(.acquire)) {
        payload.callback(&payload.snapshot, payload.userdata);
    }
    return 0; // G_SOURCE_REMOVE
}

/// Runs a background `watch` loop against `daemon.sock`, bridging incoming
/// snapshots onto the GTK main loop via `glib.idleAdd`.
pub const WatchLoop = struct {
    allocator: std.mem.Allocator,
    io: std.Io,
    socket_path: []u8,
    acmd_bin: []u8,
    version: []u8,
    callback: SnapshotCallback,
    userdata: ?*anyopaque,
    alive: *std.atomic.Value(bool),
    running: std.atomic.Value(bool) = std.atomic.Value(bool).init(false),
    fd_mutex: std.Io.Mutex = .init,
    active_fd: std.posix.fd_t = -1,
    thread: ?std.Thread = null,

    pub fn start(
        io: std.Io,
        socket_path: []const u8,
        acmd_bin: []const u8,
        version: []const u8,
        callback: SnapshotCallback,
        userdata: ?*anyopaque,
        alive: *std.atomic.Value(bool),
    ) !*WatchLoop {
        // Use thread-safe c_allocator because IdlePayload/Snapshot are allocated
        // on the background thread and freed on the GTK main thread.
        const allocator = std.heap.c_allocator;
        const self = try allocator.create(WatchLoop);
        errdefer allocator.destroy(self);

        const sock_copy = try allocator.dupe(u8, socket_path);
        errdefer allocator.free(sock_copy);
        const bin_copy = try allocator.dupe(u8, acmd_bin);
        errdefer allocator.free(bin_copy);
        const ver_copy = try allocator.dupe(u8, version);
        errdefer allocator.free(ver_copy);

        self.* = .{
            .allocator = allocator,
            .io = io,
            .socket_path = sock_copy,
            .acmd_bin = bin_copy,
            .version = ver_copy,
            .callback = callback,
            .userdata = userdata,
            .alive = alive,
        };
        self.running.store(true, .release);
        self.thread = try std.Thread.spawn(.{}, run, .{self});
        return self;
    }

    pub fn stop(self: *WatchLoop) void {
        self.running.store(false, .release);
        {
            self.fd_mutex.lockUncancelable(self.io);
            defer self.fd_mutex.unlock(self.io);
            if (self.active_fd >= 0) {
                _ = std.c.shutdown(self.active_fd, std.c.SHUT.RDWR);
            }
        }
        if (self.thread) |t| {
            t.join();
            self.thread = null;
        }
        self.allocator.free(self.socket_path);
        self.allocator.free(self.acmd_bin);
        self.allocator.free(self.version);
        self.allocator.destroy(self);
    }

    fn publishFd(self: *WatchLoop, fd: std.posix.fd_t) bool {
        self.fd_mutex.lockUncancelable(self.io);
        defer self.fd_mutex.unlock(self.io);
        if (!self.running.load(.acquire)) return false;
        self.active_fd = fd;
        return true;
    }

    fn clearAndCloseStream(self: *WatchLoop, stream: std.Io.net.Stream) void {
        self.fd_mutex.lockUncancelable(self.io);
        defer self.fd_mutex.unlock(self.io);
        self.active_fd = -1;
        stream.close(self.io);
    }

    fn clearAndCloseClient(self: *WatchLoop, client: *Client) void {
        self.fd_mutex.lockUncancelable(self.io);
        defer self.fd_mutex.unlock(self.io);
        self.active_fd = -1;
        client.deinit();
    }

    fn sleepInterruptible(self: *WatchLoop, total_ms: u64) void {
        var remaining = total_ms;
        while (remaining > 0 and self.running.load(.acquire)) {
            const step: u64 = @min(remaining, 100);
            self.io.sleep(.fromMilliseconds(@intCast(step)), .awake) catch break;
            remaining -= step;
        }
    }

    fn postSnapshot(self: *WatchLoop, snap: Decoded(Snapshot)) void {
        if (!self.running.load(.acquire)) {
            var s = snap;
            s.deinit();
            return;
        }
        const payload = self.allocator.create(IdlePayload) catch {
            var s = snap;
            s.deinit();
            return;
        };
        payload.* = .{
            .allocator = self.allocator,
            .snapshot = snap,
            .callback = self.callback,
            .userdata = self.userdata,
            .active = self.alive,
        };
        _ = glib.idleAdd(&onIdleDispatch, payload);
    }

    fn run(self: *WatchLoop) void {
        const pid: i32 = @intCast(std.c.getpid());
        var rev: u64 = 0;
        var spawn_attempts: u32 = 0;

        while (self.running.load(.acquire)) {
            const bin: ?[]const u8 = if (spawn_attempts == 0 or spawn_attempts % 10 == 0)
                self.acmd_bin
            else
                null;
            const stream = Client.connectStream(self.io, self.socket_path, bin, &self.running) catch {
                spawn_attempts +|= 1;
                self.sleepInterruptible(1000);
                continue;
            };
            if (!self.publishFd(stream.socket.handle)) {
                stream.close(self.io);
                break;
            }

            const hello = Hello{ .version = self.version, .pid = pid };
            var client = Client.initFromStream(self.allocator, self.io, stream, hello) catch {
                self.clearAndCloseStream(stream);
                spawn_attempts +|= 1;
                self.sleepInterruptible(1000);
                continue;
            };
            defer self.clearAndCloseClient(&client);
            spawn_attempts = 0;

            if (client.snapshot()) |snap| {
                self.postSnapshot(snap);
            } else |_| {
                self.sleepInterruptible(500);
                continue;
            }

            while (self.running.load(.acquire)) {
                // Keep app=false until notification rendering is implemented in the UI,
                // otherwise the daemon suppresses its fallback notifier.
                const resp = client.watch(.{ .rev = rev, .wait_ms = 25000 }) catch break;
                if (resp.rev != rev) {
                    rev = resp.rev;
                    const snap = client.snapshot() catch break;
                    self.postSnapshot(snap);
                }
            }
        }
    }
};

test "resolveSocketPath matches internal/paths/paths.go" {
    const allocator = std.testing.allocator;

    const p1 = try resolveSocketPath(allocator, .{
        .acmd_runtime_dir = "/custom/rt",
        .xdg_runtime_dir = "/run/user/1000",
    });
    defer allocator.free(p1);
    try std.testing.expectEqualStrings("/custom/rt/daemon.sock", p1);

    const p2 = try resolveSocketPath(allocator, .{
        .xdg_runtime_dir = "/run/user/1000",
        .home = "/home/u",
    });
    defer allocator.free(p2);
    try std.testing.expectEqualStrings("/run/user/1000/agent-command/daemon.sock", p2);

    // No XDG_RUNTIME_DIR: the state folder, not /tmp.
    const p3 = try resolveSocketPath(allocator, .{
        .home = "/home/u",
    });
    defer allocator.free(p3);
    try std.testing.expectEqualStrings("/home/u/.local/state/agent-command/run/daemon.sock", p3);

    const p3_state = try resolveSocketPath(allocator, .{
        .xdg_state_home = "/data/state",
        .home = "/home/u",
    });
    defer allocator.free(p3_state);
    try std.testing.expectEqualStrings("/data/state/agent-command/run/daemon.sock", p3_state);

    const p3_override = try resolveSocketPath(allocator, .{
        .acmd_state_dir = "/x/state",
        .home = "/home/u",
    });
    defer allocator.free(p3_override);
    try std.testing.expectEqualStrings("/x/state/run/daemon.sock", p3_override);

    const long_xdg = "/very/long/xdg/runtime/directory/path/that/exceeds/macos/sun_path/limit/for/unix/domain/sockets/in/paths";
    const p4 = try resolveSocketPath(allocator, .{
        .xdg_runtime_dir = long_xdg,
        .home = "/home/u",
    });
    defer allocator.free(p4);
    try std.testing.expectEqualStrings("/home/u/.local/state/agent-command/run/daemon.sock", p4);
}

test "encodeRequest and decodeResponse match internal/ipc/ipc.go schemas" {
    const allocator = std.testing.allocator;

    const hello_req = try encodeRequest(allocator, "hello", Hello{
        .proto = 2,
        .version = "0.51.0",
        .pid = 42,
    });
    defer allocator.free(hello_req);
    try std.testing.expectEqualStrings(
        "{\"op\":\"hello\",\"data\":{\"proto\":2,\"proto_min\":2,\"proto_max\":2,\"version\":\"0.51.0\",\"pid\":42}}\n",
        hello_req,
    );

    const mut_req = try encodeRequest(allocator, "mutate", Mutation{
        .kind = "session.rename",
        .targets = &.{ .{ .id = "sess-1" }, .{ .host = "studio", .id = "r1" } },
        .text = "api - 1",
    });
    defer allocator.free(mut_req);
    try std.testing.expectEqualStrings(
        "{\"op\":\"mutate\",\"data\":{\"kind\":\"session.rename\",\"targets\":[{\"id\":\"sess-1\"},{\"host\":\"studio\",\"id\":\"r1\"}],\"text\":\"api - 1\"}}\n",
        mut_req,
    );

    const go_snapshot_resp =
        \\{"ok":true,"data":{"sessions":[{"id":"s1","name":"api","description":"fix bug","group_id":"g1","status":"waiting","unseen":true,"kind":"hosted","harness_kind":"claude","pid":123}],"groups":[{"id":"g1","type":"group","name":"Default","color":"#4EA7FC","is_default":true}],"view_state":{"mode":"groups","sort":"activity","desc":true}}}
    ;

    var snap = try decodeResponse(Snapshot, allocator, go_snapshot_resp);
    defer snap.deinit();

    const sessions = snap.value.sessions.?;
    try std.testing.expectEqual(@as(usize, 1), sessions.len);
    try std.testing.expectEqualStrings("s1", sessions[0].id);
    try std.testing.expectEqualStrings("api", sessions[0].name);
    try std.testing.expectEqualStrings("waiting", sessions[0].status);
    try std.testing.expect(sessions[0].unseen);

    const groups = snap.value.groups.?;
    try std.testing.expectEqual(@as(usize, 1), groups.len);
    try std.testing.expectEqualStrings("Default", groups[0].name);
    try std.testing.expect(groups[0].is_default);

    try std.testing.expectEqualStrings("groups", snap.value.view_state.mode);
    try std.testing.expectEqualStrings("activity", snap.value.view_state.sort);
    try std.testing.expect(snap.value.view_state.desc);
}

// The snapshot as Go writes it (internal/ipc/testdata/wire): a field renamed
// on either side fails here and in Go's tests. Run from app-linux.
test "decodes the daemon's snapshot fixture" {
    const allocator = std.testing.allocator;
    const data = try std.Io.Dir.cwd().readFileAlloc(std.testing.io, "../internal/ipc/testdata/wire/snapshot.json", allocator, .limited(1024 * 1024));
    defer allocator.free(data);
    var snap = try std.json.parseFromSlice(Snapshot, allocator, data, .{ .ignore_unknown_fields = true });
    defer snap.deinit();

    const sessions = snap.value.sessions.?;
    try std.testing.expectEqual(@as(usize, 5), sessions.len);
    // s5 is in a folder, a type this UI doesn't know: it shows in the
    // folder's group (design 7.2).
    try std.testing.expectEqualStrings("folder", snap.value.others.?[0].type);
    try std.testing.expectEqualStrings("g1", snap.value.groupOf(sessions[4]).?);
    try std.testing.expectEqualStrings("g1", snap.value.groupOf(sessions[0]).?);
    try std.testing.expectEqualStrings("api", sessions[0].name);
    try std.testing.expectEqualStrings("waiting", sessions[0].status);
    try std.testing.expectEqualStrings("Allow Bash?", sessions[0].status_detail);
    try std.testing.expect(sessions[0].unseen);
    try std.testing.expectEqualStrings("set1", sessions[1].parent);
    try std.testing.expectEqual(@as(i32, 2), sessions[1].order);
    try std.testing.expectEqualStrings("", sessions[1].status_detail);
    try std.testing.expectEqualStrings("studio", sessions[3].host);
    try std.testing.expectEqualStrings("2026-11-20T09:00:00Z", sessions[0].created_at);
    try std.testing.expectEqualStrings("", sessions[0].archived_at);
    // Each session's harness: its name; a removed one's kind; a terminal.
    try std.testing.expectEqual(@as(usize, 3), snap.value.harnesses.?.len);
    try std.testing.expectEqualStrings("Work", snap.value.harnessName(sessions[0]));
    try std.testing.expectEqualStrings("Claude", snap.value.harnessName(sessions[1]));
    try std.testing.expectEqualStrings("Terminal", snap.value.harnessName(sessions[2]));
    try std.testing.expect(!snap.value.harnesses.?[2].installed);

    const groups = snap.value.groups.?;
    try std.testing.expectEqualStrings("Backend", groups[0].name);
    try std.testing.expectEqual(@as(i32, 10), groups[0].order);
    try std.testing.expect(groups[1].is_default);
    try std.testing.expectEqualStrings("activity", snap.value.view_state.sort);
    try std.testing.expect(snap.value.view_state.desc);
}

// The daemon's hello as Go writes it: the version agreed, its range and its
// ops, which this UI speaks. Run from app-linux.
test "decodes the daemon's hello fixture" {
    const allocator = std.testing.allocator;
    const data = try std.Io.Dir.cwd().readFileAlloc(std.testing.io, "../internal/ipc/testdata/wire/hello.json", allocator, .limited(64 * 1024));
    defer allocator.free(data);
    var hello = try std.json.parseFromSlice(Hello, allocator, data, .{ .ignore_unknown_fields = true });
    defer hello.deinit();
    try std.testing.expectEqual(@as(i32, 2), hello.value.proto);
    try std.testing.expectEqual(@as(i32, 3), hello.value.proto_max);
    try std.testing.expect(hello.value.proto >= proto_min and hello.value.proto <= proto_version);
    try std.testing.expectEqualStrings("snapshot", hello.value.ops.?[1]);
}

test "a session in containers this UI doesn't know finds its group" {
    const groups = [_]Group{ .{ .id = "g1" }, .{ .id = "g1", .host = "studio" } };
    const others = [_]EntityRef{
        .{ .type = "folder", .id = "f1", .parent = "g1" },
        .{ .type = "folder", .id = "f2", .parent = "f1" },
        .{ .type = "workspace", .id = "w1" },
        .{ .type = "folder", .id = "f1", .host = "studio", .parent = "g1" },
        .{ .type = "folder", .id = "loop", .parent = "loop" },
    };
    const snap = Snapshot{ .groups = &groups, .others = &others };
    try std.testing.expectEqualStrings("g1", snap.groupOf(.{ .id = "a", .name = "a", .parent = "f2" }).?);
    try std.testing.expect(snap.groupOf(.{ .id = "b", .name = "b", .parent = "w1" }) == null);
    try std.testing.expectEqualStrings("g1", snap.groupOf(.{ .id = "c", .name = "c", .parent = "f1", .host = "studio" }).?);
    try std.testing.expect(snap.groupOf(.{ .id = "d", .name = "d", .parent = "loop" }) == null);
    try std.testing.expect(snap.groupOf(.{ .id = "e", .name = "e", .parent = "nowhere" }) == null);
}
