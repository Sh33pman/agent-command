#!/usr/bin/env python3
"""Tests for the Python scripts: python3 scripts/scripts_test.py"""
import contextlib
import importlib.util
import io
import json
import os
import unittest


def load(name, file):
    path = os.path.join(os.path.dirname(os.path.abspath(__file__)), file)
    spec = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


scan = load("swift_scan", "swift-scan.py")
ghostty = load("check_ghostty", "check-ghostty.py")


def rules(text):
    return [rule for _, rule, _ in scan.scan_text(text)]


class SwiftScan(unittest.TestCase):
    def test_finds_each_rule(self):
        for line, rule in [
            ("let x = try! load()", "force-try"),
            ("let y = value as! String", "force-cast"),
            ('process.executableURL = URL(fileURLWithPath: "/bin/sh")', "shell-command"),
            ('let s = "/bin/zsh"', "shell-command"),
            ('URL(string: "http://example.com")', "plain-http"),
            ("let s = NSAppleScript(source: text)", "apple-script"),
            ('UserDefaults.standard.set(apiKey, forKey: "api_key")', "secret-in-defaults"),
            ('let f = dlopen(path, 0)', "runtime-code"),
            ("chmod(path, 0o644)", "open-permissions"),
            ('let p = "/tmp/thing"', "shared-temp-path"),
        ]:
            self.assertEqual(rules(line), [rule], line)

    def test_safe_code_passes(self):
        for line in [
            "let x = try? load()",
            "let y = value as? String",
            "let z = try load()",
            'URL(string: "https://example.com")',
            "chmod(path, 0o600)",
            'UserDefaults.standard.set(true, forKey: "popup.enabled")',
        ]:
            self.assertEqual(rules(line), [], line)

    def test_comments_dont_count(self):
        self.assertEqual(rules('// run "/bin/sh" with try! here'), [])
        self.assertEqual(rules('let a = 1 // not "/bin/sh"'), [])
        self.assertEqual(rules('/* try!\n "/bin/sh"\n as! */\nlet a = 1'), [])
        self.assertEqual(rules('/// docs mention NSAppleScript'), [])

    def test_a_url_in_a_string_isnt_a_comment(self):
        self.assertEqual(rules('let u = "https://x.com"; let y = try! f()'), ["force-try"])

    def test_allowing_needs_the_rule_and_a_reason(self):
        allowed = '// swift-scan:allow shell-command runs our own binary\nlet s = "/bin/sh"'
        self.assertEqual(rules(allowed), [])
        self.assertEqual(rules('let s = "/bin/sh" // swift-scan:allow shell-command our binary'), [])
        no_reason = '// swift-scan:allow shell-command\nlet s = "/bin/sh"'
        self.assertEqual(rules(no_reason), ["shell-command"])
        other_rule = '// swift-scan:allow force-try some reason\nlet s = "/bin/sh"'
        self.assertEqual(rules(other_rule), ["shell-command"])

    def test_allowing_covers_only_the_next_line(self):
        text = '// swift-scan:allow shell-command our binary\nlet a = "/bin/sh"\nlet b = "/bin/sh"'
        self.assertEqual([n for n, _, _ in scan.scan_text(text)], [3])


class CheckGhostty(unittest.TestCase):
    def advisory(self, id):
        return {"ghsa_id": id, "html_url": "https://x/" + id, "severity": "high", "summary": "bad " + id}

    def test_reads_the_pin(self):
        self.assertEqual(ghostty.pinned("set -e\nTAG=v1.4.0\nCOMMIT=83fb\n"), ("v1.4.0", "83fb"))
        with self.assertRaises(ValueError):
            ghostty.pinned("nothing here")

    def test_only_real_newer_versions_count(self):
        tags = ["tip", "vt-1.0.0", "v1.4.0", "v1.3.1", "v1.4.1", "v1.10.0", "v2.0.0-rc1"]
        self.assertEqual(ghostty.newer_releases(tags, "v1.4.0"), ["v1.10.0", "v1.4.1"])
        self.assertEqual(ghostty.newer_releases(["v1.4.0", "v1.3.1"], "v1.4.0"), [])
        with self.assertRaises(ValueError):
            ghostty.newer_releases([], "tip")

    def test_reviewed_list_ignores_comments_and_blanks(self):
        self.assertEqual(ghostty.reviewed_ids("# header\n\nGHSA-1  # fine\nGHSA-2\n"), {"GHSA-1", "GHSA-2"})

    def test_only_unreviewed_advisories_are_reported(self):
        got = ghostty.unreviewed([self.advisory("GHSA-1"), self.advisory("GHSA-2")], {"GHSA-1"})
        self.assertEqual([a["ghsa_id"] for a in got], ["GHSA-2"])

    def test_nothing_new_means_no_report(self):
        self.assertEqual(ghostty.report(("v1.4.0", "83fb"), [], []), "")

    def test_report_says_what_to_do(self):
        body = ghostty.report(("v1.4.0", "83fb"), ["v1.5.0"], [self.advisory("GHSA-9")])
        for part in ["v1.4.0", "83fb", "v1.5.0", "GHSA-9", "https://x/GHSA-9", "high", ghostty.REVIEWED]:
            self.assertIn(part, body)
        self.assertNotIn("Advisories", ghostty.report(("v1.4.0", "83fb"), ["v1.5.0"], []))
        self.assertNotIn("Newer", ghostty.report(("v1.4.0", "83fb"), [], [self.advisory("GHSA-9")]))


class FileIssue(unittest.TestCase):
    """Filing the issue, with gh replaced so nothing reaches GitHub."""

    def run_with(self, listed, body="the report"):
        calls = []

        def fake(*args):
            calls.append(args)
            return json.dumps(listed) if args[:2] == ("issue", "list") else "https://x/issues/1\n"

        real = ghostty.gh
        ghostty.gh = fake
        try:
            with contextlib.redirect_stdout(io.StringIO()):
                ghostty.file_issue(body)
        finally:
            ghostty.gh = real
        return [c[1] for c in calls[1:]]

    def test_opens_one_when_there_is_none(self):
        self.assertEqual(self.run_with([]), ["create"])

    def test_an_issue_with_another_title_doesnt_count(self):
        self.assertEqual(self.run_with([{"number": 4, "title": "Other", "body": "x"}]), ["create"])

    def test_leaves_the_same_report_alone(self):
        listed = [{"number": 4, "title": ghostty.TITLE, "body": "the report"}]
        self.assertEqual(self.run_with(listed), [])

    def test_updates_it_when_something_is_new(self):
        listed = [{"number": 4, "title": ghostty.TITLE, "body": "older"}]
        self.assertEqual(self.run_with(listed), ["edit", "comment"])


class ReviewedList(unittest.TestCase):
    def test_ids_look_right(self):
        root = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
        with open(os.path.join(root, ghostty.REVIEWED), encoding="utf-8") as f:
            ids = ghostty.reviewed_ids(f.read())
        self.assertTrue(ids)
        for id in ids:
            self.assertRegex(id, r"^GHSA-[a-z0-9]{4}-[a-z0-9]{4}-[a-z0-9]{4}$")


if __name__ == "__main__":
    unittest.main()
