#!/usr/bin/env python3
"""Enforces line coverage for the app's logic, file by file.

    coverage-gate-app.py <codecov.json>

Only the files below. Views and window handling need a running UI, so a target for
the whole app would only encourage low-value tests of them. Raise a minimum when a
file's coverage rises, and add a file when its logic gets tests.
"""
import json
import os
import sys

MINIMUMS = {
    "AppNotifier.swift": 30,
    "Daemon.swift": 85,
    "InboxModel.swift": 90,
    "Jobs.swift": 90,
    "Links.swift": 95,
    "Markdown.swift": 90,
    "MarkdownEditing.swift": 95,
    "Palette.swift": 95,
    "PaneSets.swift": 95,
    "PaneTree.swift": 95,
    # The rest reads the person's real config, which tests don't.
    "RemoteControl.swift": 60,
    "Report.swift": 95,
    # The rest opens panels and runs timers.
    "ScheduledModel.swift": 70,
    "ScheduleBuilder.swift": 95,
    "SessionStore.swift": 70,
    "SpeechController.swift": 90,
    "StatusSummary.swift": 95,
    "ThemeConfig.swift": 95,
}


def main():
    data = json.load(open(sys.argv[1]))["data"][0]
    found = {
        os.path.basename(f["filename"]): f["summary"]["lines"]["percent"]
        for f in data["files"]
        if "/Sources/AgentCommand/" in f["filename"]
    }
    failed = False
    for name, minimum in sorted(MINIMUMS.items()):
        if name not in found:
            print(f"FAIL {name}: not in the coverage report")
            failed = True
            continue
        ok = found[name] >= minimum
        failed = failed or not ok
        print(f"{'ok  ' if ok else 'FAIL'} {name}: {found[name]:.1f}% (minimum {minimum}%)")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
