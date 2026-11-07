#!/usr/bin/env python3
"""Security and crash checks for the Swift code, as gosec does for the Go code.

    swift-scan.py [folder ...]

CodeQL needs a paid plan on a private repo, so this does what it can without one. It
matches patterns, one line at a time, and doesn't follow data through the code, so a
clean result doesn't mean the code is safe.

Where a match is fine, say why on the line before or at the end of the line:

    // swift-scan:allow shell-command runs only our own binary, passed as $0
"""
import os
import re
import sys

RULES = {
    "force-try": (r"\btry!", "This crashes the app on any error. Handle the error."),
    "force-cast": (r"\bas!", "This crashes the app on an unexpected type. Use as? and handle it."),
    "shell-command": (
        r'"/bin/(ba|z|da)?sh"',
        "A shell runs whatever is put into its command. Run the program directly.",
    ),
    "plain-http": (r'"http://', "Anything on the way can read or change plain http. Use https."),
    "apple-script": (r"\b(NSAppleScript|OSAScript)\b", "AppleScript built from text can run anything."),
    "secret-in-defaults": (
        r"(?i)UserDefaults.*(password|secret|token|api_?key)",
        "UserDefaults is a plain file. Secrets belong in the keychain.",
    ),
    "runtime-code": (r"\b(dlopen|NSClassFromString)\b", "Code loaded at runtime skips the app's signature."),
    "open-permissions": (
        r"\b0o(777|775|666|664|755|644)\b",
        "Other users could read this. Keep files private to the user.",
    ),
    "shared-temp-path": (
        r'"/tmp/',
        "Other users can create this path first. Use the user's own temp folder.",
    ),
}

ALLOW = re.compile(r"swift-scan:allow\s+([a-z-]+)\s+\S")


def code_part(line):
    """The line without a comment, and without the contents of strings a comment marker
    could be in. Strings stay, as several rules look inside them."""
    in_string = False
    i = 0
    while i < len(line) - 1:
        c = line[i]
        if c == "\\" and in_string:
            i += 2
            continue
        if c == '"':
            in_string = not in_string
        if not in_string and line[i:i + 2] == "//":
            return line[:i]
        i += 1
    return line


def scan_text(text):
    """Returns (line number, rule, message) for each match that isn't allowed."""
    found = []
    allowed_next = set()
    in_block_comment = False
    for number, line in enumerate(text.splitlines(), 1):
        allowed = allowed_next | set(ALLOW.findall(line))
        allowed_next = set()
        stripped = line.strip()
        if in_block_comment:
            in_block_comment = "*/" not in stripped
            continue
        if stripped.startswith("/*"):
            in_block_comment = "*/" not in stripped
            continue
        if stripped.startswith("//"):
            allowed_next = set(ALLOW.findall(line))
            continue
        code = code_part(line)
        for rule, (pattern, message) in RULES.items():
            if rule not in allowed and re.search(pattern, code):
                found.append((number, rule, message))
    return found


def swift_files(folder):
    for root, dirs, files in os.walk(folder):
        dirs[:] = [d for d in dirs if d not in (".build", "vendor")]
        for name in sorted(files):
            if name.endswith(".swift"):
                yield os.path.join(root, name)


def main(folders):
    count = 0
    problems = 0
    for folder in folders:
        for path in swift_files(folder):
            count += 1
            with open(path, encoding="utf-8") as f:
                for number, rule, message in scan_text(f.read()):
                    problems += 1
                    print(f"{path}:{number}: {rule}: {message}")
    print(f"swift-scan: {count} files, {problems} problems")
    return 1 if problems or not count else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:] or ["app/Sources", "helpers/acmd-speech/Sources"]))
