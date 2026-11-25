#!/usr/bin/env python3
"""PreToolUse guard: keep Claude away from fanwork content.

Blocks (exit 2) tool calls that would let Claude read mirrored AO3 content or
fetch AO3 directly. See CLAUDE.md "HARD RULE". Do not weaken without explicit
user approval.

Allowed exceptions inside data/: data/logs/*.log* and data/debug/*.skel.html
(both are designed to be content-free).

Part 2 API (docs/part2-api.md): blocks sending the reveal param from Bash, WebFetch or MCP
tools, writing its literal outside docs, and starting `ao3mirror serve` without --no-reveal.

Part 4 UI (docs/part4-ui.md): blocks starting `ao3mirror dev ui-server` (a revealing server over
synthetic data); only Playwright starts it, under `make ui-e2e`.
"""
import json
import re
import shlex
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

# The skeletonizer processes real AO3 pages; only the user runs it. Block `go run` of it and
# executing a built binary. Mentions in other commands (git add cmd/skeletonize, go build,
# go vet, go test) are allowed.
# ao3mirror hides tag/user/collection names unless --names is passed; that flag is user-only.
AO3MIRROR_NAMES = re.compile(r"ao3mirror\b[^;&|]*\s--?names\b")

SKELETONIZE_RUN = re.compile(r"\bgo\s+run\b[^;&|]*skeletonize")
SKELETONIZE_EXEC = re.compile(r"(^|[\s;&|`(])((?:[^\s;&|`(]*/)?)skeletonize(?=$|[\s;&|`)])")

# The API reveals fanwork only when a request carries the reveal param (docs/part2-api.md).
# Claude must never send it: block the param with a value (`=`, also URL-encoded) and the
# URL-encoded name anywhere. The bare name (grep, constants) is fine.
REVEAL_PARAM = re.compile(
    r"reveal(_|%5f|%255f)fanwork\s*(=|%3d|%253d)|reveal(%5f|%255f)fanwork",
    re.IGNORECASE,
)
# Files where the literal may appear (docs and rules, not code that could send it).
REVEAL_DOC_PATH = re.compile(
    r"(^|/)(docs/\S*\.md$|CLAUDE\.md$|\.claude/hooks/|\.claude/plans/)"
)

# Any `ao3mirror serve` Claude starts must carry --no-reveal (hashed-only server).
# The command is tokenized like a shell would (quotes kept together), split into simple
# commands, and only commands that run ao3mirror (in command position, or via `go run`)
# are checked. Quoted strings with spaces (bash -c "...") are checked recursively, so a
# commit message that mentions serve is not a run, but `bash -c "ao3mirror serve"` is.
# Also matches suffixed builds such as bin/ao3mirror-e2e (make ui-e2e).
AO3MIRROR_TOKEN = re.compile(r"^(\S*/)?ao3mirror(-[A-Za-z0-9_.-]+)?$")
NO_REVEAL = re.compile(r"^--?no-reveal(=(true|1))?$")
CONTROL = set(";&|\n()`")
PREFIX_CMDS = {"nohup", "exec", "env", "time", "sudo", "command", "setsid", "nice"}
ASSIGN = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=")


def _shell_tokens(cmd: str):
    lex = shlex.shlex(cmd, posix=True, punctuation_chars=";&|\n()`")
    lex.whitespace = " \t\r"
    lex.whitespace_split = True
    return list(lex)


def _runs_ao3mirror(words) -> bool:
    i = 0
    while i < len(words) and (words[i] in PREFIX_CMDS or ASSIGN.match(words[i])):
        i += 1
    if i < len(words) and words[i] == "timeout":
        i += 2  # timeout DURATION cmd...
    if i >= len(words):
        return False
    if AO3MIRROR_TOKEN.match(words[i]):
        return True
    return words[i] == "go" and "run" in words[i + 1:i + 2] and any(
        AO3MIRROR_TOKEN.match(w) for w in words[i + 2:]
    )


def _serve_without_no_reveal(words) -> bool:
    return "serve" in words and not any(NO_REVEAL.match(w) for w in words)


def _ui_server(words) -> bool:
    # `ao3mirror dev ui-server` serves seeded synthetic data with reveal on, for the web UI's
    # Playwright tests (docs/part4-ui.md stage 4). Only Playwright starts it (`make ui-e2e`,
    # as its own subprocess); Claude never starts it directly, so never drives a browser at a
    # revealing server.
    return any(a == "dev" and b == "ui-server" for a, b in zip(words, words[1:]))


def runs_ao3mirror_with(cmd: str, pred, depth: int = 0) -> bool:
    """Whether any simple command in cmd runs ao3mirror with words matching pred."""
    try:
        toks = _shell_tokens(cmd)
    except ValueError:
        # Unbalanced quotes: fall back to a plain scan, failing closed.
        toks = cmd.split()
        return any(AO3MIRROR_TOKEN.match(t) for t in toks) and pred(toks)
    words = []
    for t in toks + [";"]:
        if t and all(c in CONTROL for c in t):
            if words and _runs_ao3mirror(words) and pred(words):
                return True
            words = []
            continue
        if depth < 3 and (" " in t or "\n" in t) and runs_ao3mirror_with(t, pred, depth + 1):
            return True
        words.append(t)
    return False


def serve_without_no_reveal(cmd: str) -> bool:
    return runs_ao3mirror_with(cmd, _serve_without_no_reveal)


def starts_ui_server(cmd: str) -> bool:
    return runs_ao3mirror_with(cmd, _ui_server)


def written_text(ti: dict) -> str:
    parts = [ti.get(k, "") for k in ("content", "new_string", "new_source")]
    for e in ti.get("edits", []) or []:
        if isinstance(e, dict):
            parts.append(e.get("new_string", ""))
    return " ".join(str(p) for p in parts)


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
        if SKELETONIZE_RUN.search(cmd) or any(
            not m.group(2).endswith("cmd/") for m in SKELETONIZE_EXEC.finditer(cmd)
        ):
            block("running skeletonize is user-only (it processes real AO3 pages)")
        if AO3MIRROR_NAMES.search(cmd):
            block("ao3mirror --names shows tag/user names; user-only")
        if REVEAL_PARAM.search(cmd):
            block("the API reveal param is user-only; Claude never sends it")
        if serve_without_no_reveal(cmd):
            block("Claude may only start `ao3mirror serve` with --no-reveal")
        if starts_ui_server(cmd):
            block("`ao3mirror dev ui-server` reveals; only Playwright starts it (make ui-e2e)")
        if touches_protected(cmd):
            block("shell command references the data dir, a .db/.zst file, or images/")
        return

    if tool in ("WebFetch", "WebSearch"):
        target = ti.get("url", "") + " " + ti.get("query", "")
        if AO3_HOST.search(target) and tool == "WebFetch":
            block("WebFetch of an AO3 host")
        if REVEAL_PARAM.search(ti.get("url", "")) and tool == "WebFetch":
            block("the API reveal param is user-only; Claude never sends it")
        return

    if tool.startswith("mcp__"):
        # Browser and other MCP tools: check the whole input.
        blob = json.dumps(ti, ensure_ascii=False)
        if REVEAL_PARAM.search(blob):
            block("the API reveal param is user-only; Claude never sends it")
        if AO3_HOST.search(blob):
            block("MCP tool input references an AO3 host")
        return

    if tool in ("Read", "Edit", "Write", "MultiEdit", "NotebookEdit"):
        path = ti.get("file_path", "") or ti.get("notebook_path", "")
        if touches_protected(path):
            block(f"{tool} of a protected path")
        if (
            tool != "Read"
            and REVEAL_PARAM.search(written_text(ti))
            and not REVEAL_DOC_PATH.search(path)
        ):
            block("the reveal param literal belongs only in docs; use api.RevealParam in code")
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
