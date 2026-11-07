#!/usr/bin/env python3
"""Says when Ghostty needs a look. Dependabot can't, as it's pinned in a shell script.

    check-ghostty.py [--issue]

It reports a release newer than scripts/build-ghostty.sh pins, and any security advisory
not listed in .github/ghostty-advisories-reviewed.txt. With --issue, it opens an issue
for them, or comments on the one already open. It only reads Ghostty's public data.

To clear an advisory, check it against the pinned commit, then add its id to that file.
"""
import json
import re
import subprocess
import sys

GHOSTTY = "ghostty-org/ghostty"
REVIEWED = ".github/ghostty-advisories-reviewed.txt"
TITLE = "Ghostty needs a security review"


def pinned(script):
    tag = re.search(r"^TAG=(\S+)", script, re.M)
    commit = re.search(r"^COMMIT=(\S+)", script, re.M)
    if not tag or not commit:
        raise ValueError("no TAG or COMMIT in the build script")
    return tag.group(1), commit.group(1)


def version(tag):
    """A release's numbers, or None for anything else, such as tip or vt-1.0.0."""
    m = re.fullmatch(r"v(\d+)\.(\d+)\.(\d+)", tag)
    return tuple(int(n) for n in m.groups()) if m else None


def newer_releases(tags, pin):
    current = version(pin)
    if current is None:
        raise ValueError(f"the pinned tag {pin} isn't a version")
    found = [t for t in tags if (version(t) or (0, 0, 0)) > current]
    return sorted(found, key=version, reverse=True)


def reviewed_ids(text):
    return {line.split("#")[0].strip() for line in text.splitlines()} - {""}


def unreviewed(advisories, reviewed):
    return [a for a in advisories if a["ghsa_id"] not in reviewed]


def report(pin, releases, advisories):
    """The text of an issue, or an empty string if there's nothing to look at."""
    if not releases and not advisories:
        return ""
    lines = [f"We pin Ghostty {pin[0]} ({pin[1]}) in `scripts/build-ghostty.sh`.", ""]
    if advisories:
        lines.append("**Advisories not reviewed yet:**")
        lines.append("")
        for a in advisories:
            lines.append(f"- [{a['ghsa_id']}]({a['html_url']}) ({a['severity']}): {a['summary']}")
        lines.append("")
        lines.append(
            "Check each against the pinned commit, as being on a version isn't always enough: "
            "some are fixed at a commit. Then add its id to `" + REVIEWED + "`."
        )
        lines.append("")
    if releases:
        lines.append(f"**Newer releases:** {', '.join(releases)}. Moving to one means updating `TAG` and `COMMIT`.")
        lines.append("")
    return "\n".join(lines)


def gh(*args):
    out = subprocess.run(["gh", *args], capture_output=True, text=True, check=True)
    return out.stdout


def fetch_all(path):
    items = []
    page = 1
    while True:
        got = json.loads(gh("api", f"repos/{GHOSTTY}/{path}?per_page=100&page={page}"))
        items += got
        if len(got) < 100:
            return items
        page += 1


def file_issue(body):
    found = json.loads(gh("issue", "list", "--state", "open", "--search", f'"{TITLE}" in:title', "--json", "number,title,body"))
    existing = [i for i in found if i["title"] == TITLE]
    if not existing:
        print(gh("issue", "create", "--title", TITLE, "--body", body).strip())
    elif existing[0]["body"] != body:
        number = str(existing[0]["number"])
        gh("issue", "edit", number, "--body", body)
        gh("issue", "comment", number, "--body", "There's something new, so the description above has changed.")
        print("updated issue", number)
    else:
        print("already reported in issue", existing[0]["number"])


def main(args):
    with open("scripts/build-ghostty.sh", encoding="utf-8") as f:
        pin = pinned(f.read())
    with open(REVIEWED, encoding="utf-8") as f:
        reviewed = reviewed_ids(f.read())
    tags = [t["name"] for t in fetch_all("tags")]
    advisories = fetch_all("security-advisories")
    body = report(pin, newer_releases(tags, pin[0]), unreviewed(advisories, reviewed))
    print(f"Ghostty {pin[0]}: {len(tags)} tags, {len(advisories)} advisories, {len(reviewed)} reviewed")
    if not body:
        print("nothing new")
        return 0
    print(body)
    if "--issue" in args:
        file_issue(body)
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
