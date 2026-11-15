#!/usr/bin/env python3
"""PreToolUse guard: keep Claude away from fanwork content.

Blocks (exit 2) tool calls that would let Claude read mirrored AO3 content or
fetch AO3 directly. See CLAUDE.md "HARD RULE". Do not weaken without explicit
user approval.

Allowed exceptions inside data/: data/logs/*.log* and data/debug/*.skel.html
(both are designed to be content-free).
"""
import json
import re
import sys

AO3_HOST = re.compile(
    r"(archiveofourown\.(org|com|net|gay)|(^|[^a-z0-9])ao3\.org|otwarchive\.org)",
    re.IGNORECASE,
)

# Paths that may hold fanwork content.
PROTECTED = re.compile(
    r"((^|[\s/'\"=:(<>|;&`])data(/|\s|$|['\"])"  # the data dir (not testdata/)
    r"|\.db(-wal|-shm|-journal)?\b"              # sqlite files
    r"|\.zst\b"                                  # compressed raw pages
    r"|(^|[\s/'\"=])images/)",                   # mirrored images
    re.IGNORECASE,
)

# Content-free exceptions; removed before checking PROTECTED.
ALLOWED = re.compile(
    r"(\.?/?data/logs(/[^\s'\"|;&]*\.log[^\s'\"|;&]*)?"
    r"|\.?/?data/debug/[^\s'\"|;&]*\.skel\.html"
    r"|\.?/?data/debug/?(?=[\s'\"]|$))",
    re.IGNORECASE,
)

SQLITE_CLI = re.compile(r"(^|[\s;&|`(])(sqlite3?|litecli|sqlite-utils|datasette)\b")


def block(reason: str) -> None:
    sys.stderr.write(
        "BLOCKED by .claude/hooks/guard.py: " + reason + "\n"
        "Claude must never read fanwork content or contact AO3 directly "
        "(see CLAUDE.md HARD RULE). Use `ao3mirror status`/`ao3mirror debug`, "
        "data/logs/, or *.skel.html skeletons instead.\n"
    )
    sys.exit(2)


def touches_protected(text: str) -> bool:
    return bool(PROTECTED.search(ALLOWED.sub(" ", text)))


def main() -> None:
    try:
        payload = json.load(sys.stdin)
    except Exception:
        block("could not parse hook input; failing closed")
        return
    tool = payload.get("tool_name", "")
    ti = payload.get("tool_input", {}) or {}

    if tool == "Bash":
        cmd = ti.get("command", "")
        if AO3_HOST.search(cmd):
            block("shell command references an AO3 host")
        if SQLITE_CLI.search(cmd):
            block("sqlite CLI is not allowed (databases hold fanwork content)")
        if touches_protected(cmd):
            block("shell command references the data dir, a .db/.zst file, or images/")
        return

    if tool in ("WebFetch", "WebSearch"):
        target = ti.get("url", "") + " " + ti.get("query", "")
        if AO3_HOST.search(target) and tool == "WebFetch":
            block("WebFetch of an AO3 host")
        return

    if tool in ("Read", "Edit", "Write", "MultiEdit", "NotebookEdit"):
        path = ti.get("file_path", "") or ti.get("notebook_path", "")
        if touches_protected(path):
            block(f"{tool} of a protected path")
        return

    if tool in ("Grep", "Glob"):
        fields = " ".join(str(ti.get(k, "")) for k in ("path", "glob", "pattern"))
        if tool == "Grep":
            fields = " ".join(str(ti.get(k, "")) for k in ("path", "glob"))
        if touches_protected(fields):
            block(f"{tool} over a protected path")
        return


if __name__ == "__main__":
    main()
