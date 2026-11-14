const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const gobject = b.dependency("gobject", .{
        .target = target,
        .optimize = optimize,
    });

    const exe_mod = b.createModule(.{
        .root_source_file = b.path("src/main.zig"),
        .target = target,
        .optimize = optimize,
        .link_libc = true,
    });

    const gobject_imports = .{
        .{ "adw", "adw1" },
        .{ "gdk", "gdk4" },
        .{ "gio", "gio2" },
        .{ "glib", "glib2" },
        .{ "gobject", "gobject2" },
        .{ "gtk", "gtk4" },
    };
    inline for (gobject_imports) |import| {
        exe_mod.addImport(import[0], gobject.module(import[1]));
    }

    exe_mod.linkSystemLibrary("gtk4", .{});
    exe_mod.linkSystemLibrary("libadwaita-1", .{});
    exe_mod.linkSystemLibrary("egl", .{});

    const ghostty_include = b.path("../build/ghostty/include");
    exe_mod.addIncludePath(ghostty_include);
    exe_mod.addObjectFile(b.path("../build/ghostty/lib/ghostty-internal.so"));
    exe_mod.addRPathSpecial("$ORIGIN");
    exe_mod.addRPathSpecial("$ORIGIN/../build/ghostty/lib");
    exe_mod.addRPathSpecial("$ORIGIN/../../../build/ghostty/lib");

    const exe = b.addExecutable(.{
        .name = "agent-command-ui",
        .root_module = exe_mod,
    });
    b.installArtifact(exe);

    const run_cmd = b.addRunArtifact(exe);
    run_cmd.step.dependOn(b.getInstallStep());
    const run_step = b.step("run", "Run agent-command-ui");
    run_step.dependOn(&run_cmd.step);

    const unit_tests = b.addTest(.{
        .root_module = exe_mod,
    });
    const run_unit_tests = b.addRunArtifact(unit_tests);
    const test_step = b.step("test", "Run Zig unit tests");
    test_step.dependOn(&run_unit_tests.step);
}
