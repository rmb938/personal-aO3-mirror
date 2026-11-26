#!/usr/bin/env python3
"""Tests for guard.py. Run: python3 -I .claude/hooks/guard_test.py"""
import importlib.util
import json
import os
import random
import re
import subprocess
import sys
import time

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "guard.py")

sys.dont_write_bytecode = True  # importing guard.py must not leave a __pycache__ in the repo
_spec = importlib.util.spec_from_file_location("guard", GUARD)
guard = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(guard)

# The regexes strip_allowed and reveal_doc_path replaced (quadratic on long input). They stay
# here as the oracle: on short random input the new code must give exactly their results.
OLD_ALLOWED = re.compile(
    r"(\.?/?data/logs(/[^\s'\"|;&]*\.log[^\s'\"|;&]*)?"
    r"|\.?/?data/debug/[^\s'\"|;&]*\.skel\.html"
    r"|\.?/?data/debug/?(?=[\s'\"]|$))",
    re.IGNORECASE,
)
OLD_REVEAL_DOC_PATH = re.compile(
    r"(^|/)(docs/\S*\.md$|CLAUDE\.md$|\.claude/hooks/|\.claude/plans/)"
)
# Pieces the rules care about, plus case-folding traps (long s, Kelvin sign) and delimiters.
PIECES = ["data/", "DATA/", "data", "logs", "LOGS", "logſ", "debug", "Debug", "/", ".", "./",
          ".log", ".LOG", ".skel.html", ".SKEL.html", ".sKel.html", "docs/", "Docs/", ".md",
          "CLAUDE.md", ".claude/hooks/", ".claude/plans/", ".claude/", "x", "ſ", "K", " ",
          "\t", "\n", " ", "'", '"', "|", ";", "&", "=", ":", "(", "`", "images/", "a.db"]
HOST = "archiveofourown" + ".org"
REVEAL = "reveal_" + "fanwork"

CASES = [
    # (expect_blocked, tool_name, tool_input)
    (True, "Bash", {"command": f"curl -A x https://{HOST}/works/1"}),
    (True, "Bash", {"command": "wget https://ao3.org/works/1"}),
    (True, "Bash", {"command": "sqlite3 data/meta.db 'select 1'"}),
    (True, "Bash", {"command": "cat data/content.db"}),
    (True, "Bash", {"command": "ls ./data/"}),
    (True, "Bash", {"command": "zstd -dc foo.zst"}),
    (True, "Bash", {"command": "ls data/images/ab"}),
    (True, "Bash", {"command": "cat /home/u/proj/data/raw/1.html"}),
    (True, "Read", {"file_path": "/home/u/proj/data/meta.db"}),
    (True, "Read", {"file_path": "/home/u/proj/data/skeleton-input/work.html"}),
    (True, "Read", {"file_path": "/home/u/proj/data/debug/work-1.html"}),
    (True, "Grep", {"pattern": "x", "path": "/home/u/proj/data"}),
    (True, "Glob", {"pattern": "data/**/*.html"}),
    (True, "WebFetch", {"url": f"https://{HOST}/tags/x/works", "prompt": "p"}),
    (True, "Bash", {"command": "go run ./cmd/skeletonize data2/x.html"}),
    (True, "Bash", {"command": "go run ./cmd/skeletonize -o out.skel.html https://x.invalid/works/1"}),
    (True, "Bash", {"command": "go build -o bin/skeletonize ./cmd/skeletonize && bin/skeletonize f.html"}),
    (True, "Bash", {"command": "./skeletonize page.html"}),
    (True, "Bash", {"command": "cat f.html | skeletonize -"}),
    (True, "Bash", {"command": "./ao3mirror --config c.yaml source list --names"}),
    (True, "Bash", {"command": "go run ./cmd/ao3mirror source show 3 -names"}),
    (False, "Bash", {"command": "./ao3mirror --config c.yaml source list"}),
    (False, "Bash", {"command": "go test ./cmd/ao3mirror -run TestSourceCLI"}),
    (False, "Bash", {"command": "go test ./..."}),
    (False, "Bash", {"command": "go vet ./cmd/skeletonize"}),
    (False, "Bash", {"command": "go build -o /dev/null ./cmd/skeletonize"}),
    (False, "Bash", {"command": "git add cmd/skeletonize internal/skeleton"}),
    (False, "Bash", {"command": "go test ./internal/skeleton ./internal/parse -run Skeleton -v"}),
    (False, "Bash", {"command": "./ao3mirror status --config ao3mirror.local.yaml"}),
    (False, "Bash", {"command": "cat testdata/fixtures/work.html"}),
    (False, "Bash", {"command": "tail -n 50 data/logs/ao3mirror.log"}),
    (False, "Bash", {"command": "ls data/debug/"}),
    (False, "Bash", {"command": "cat data/debug/work-1-20261115.skel.html"}),
    (False, "Read", {"file_path": "/home/u/proj/data/logs/ao3mirror.log"}),
    (False, "Read", {"file_path": "/home/u/proj/data/debug/work-1.skel.html"}),
    (False, "Read", {"file_path": "/home/u/proj/testdata/skeletons/work.skel.html"}),
    (False, "Read", {"file_path": "/home/u/proj/internal/httpx/client.go"}),
    (False, "Write", {"file_path": "/home/u/proj/internal/httpx/ua.go",
                      "content": f"const base = \"https://{HOST}\" // data/meta.db"}),
    (False, "Grep", {"pattern": "meta.db", "path": "/home/u/proj/internal"}),
    (False, "WebFetch", {"url": "https://github.com/otwcode/otwarchive", "prompt": "p"}),
    # Part 2 API: the reveal param is never sent by Claude, from any tool, to any host.
    (True, "Bash", {"command": f"curl 'http://localhost:8080/api/v1/works/1?{REVEAL}=1'"}),
    (True, "Bash", {"command": f"curl http://example.invalid/x?a=1&{REVEAL.upper()}=0"}),
    (True, "Bash", {"command": f"curl --unix-socket s http://x/api/v1/works/1?{REVEAL} =1"}),
    (True, "Bash", {"command": f"curl 'http://127.0.0.1:8080/w?{REVEAL}%3D1'"}),
    (True, "Bash", {"command": "curl 'http://127.0.0.1:8080/w?reveal%5F" + "fanwork'"}),
    (True, "Bash", {"command": "wget 'http://h/w?reveal%255F" + "fanwork%253D1'"}),
    (True, "WebFetch", {"url": f"http://localhost:8080/api/v1/works/1?{REVEAL}=1", "prompt": "p"}),
    (True, "mcp__claude-in-chrome__navigate", {"url": f"http://localhost:8080/?{REVEAL}=1"}),
    (True, "mcp__Claude_Browser__navigate", {"url": f"https://{HOST}/works/1"}),
    (True, "Write", {"file_path": "/home/u/proj/scripts/probe.sh",
                     "content": f"curl localhost:8080/w?{REVEAL}=1"}),
    (True, "Edit", {"file_path": "/home/u/proj/internal/api/x_test.go",
                    "old_string": "a", "new_string": f"u := \"/w?{REVEAL}=1\""}),
    (True, "MultiEdit", {"file_path": "/home/u/proj/internal/api/x.go",
                         "edits": [{"old_string": "a", "new_string": f"{REVEAL}=1"}]}),
    (False, "Bash", {"command": f"grep -rn {REVEAL} internal/ cmd/"}),
    (False, "Bash", {"command": "curl http://localhost:8080/api/v1/works/1"}),
    (False, "WebFetch", {"url": "http://localhost:8080/api/v1/works/1", "prompt": "p"}),
    (False, "mcp__claude-in-chrome__navigate", {"url": "http://localhost:8080/api/v1/works/1"}),
    (False, "Write", {"file_path": "/home/u/proj/docs/part2-api.md",
                      "content": f"Reveal with `{REVEAL}=1`."}),
    (False, "Edit", {"file_path": "/home/u/proj/CLAUDE.md",
                     "old_string": "a", "new_string": f"never send {REVEAL}=1"}),
    (False, "Write", {"file_path": "/home/u/proj/internal/api/reveal.go",
                      "content": f"const RevealParam = \"{REVEAL}\""}),
    (False, "Edit", {"file_path": "/home/u/proj/internal/api/x_test.go",
                     "old_string": "a", "new_string": "u := \"/w?\" + api.RevealParam + \"=1\""}),
    # Any `ao3mirror serve` Claude starts must be hashed-only (--no-reveal).
    (True, "Bash", {"command": "./ao3mirror --config ao3mirror.local.yaml serve"}),
    (True, "Bash", {"command": "go run ./cmd/ao3mirror --config c.yaml serve --listen :9000"}),
    (True, "Bash", {"command": "make build && nohup ./ao3mirror serve > /tmp/s.log 2>&1 &"}),
    (True, "Bash", {"command": "/usr/local/bin/ao3mirror serve --no-reveal=false"}),
    (True, "Bash", {"command": "ao3mirror serve; ./ao3mirror serve --no-reveal"}),
    (False, "Bash", {"command": "./ao3mirror --config ao3mirror.local.yaml serve --no-reveal"}),
    (False, "Bash", {"command": "go run ./cmd/ao3mirror serve -no-reveal"}),
    (False, "Bash", {"command": "./ao3mirror serve --no-reveal=true &"}),
    (False, "Bash", {"command": "go test ./cmd/ao3mirror -run serve"}),
    (False, "Bash", {"command": "grep -n serve cmd/ao3mirror/main.go"}),
    (False, "Bash", {"command": "./ao3mirror --config c.yaml status"}),
    (True, "Bash", {"command": "cd /tmp\n./ao3mirror serve"}),
    (True, "Bash", {"command": "bash -c './ao3mirror serve'"}),
    (True, "Bash", {"command": "AO3MIRROR_X=1 timeout 30 ./ao3mirror serve"}),
    (True, "Bash", {"command": "(./ao3mirror serve &) && sleep 1"}),
    (False, "Bash", {"command": "git commit -m 'plan the ao3mirror serve API\n\nmore about serve'"}),
    (False, "Bash", {"command": "echo ao3mirror serve"}),
    (True, "Bash", {"command": "fnm exec --using=26 ./ao3mirror serve"}),
    (True, "Bash", {"command": "fnm exec --using 26 ./ao3mirror serve"}),
    # `make serve` is the user's revealing serve, with any SERVE_FLAGS.
    (True, "Bash", {"command": "make serve"}),
    (True, "Bash", {"command": "make build serve"}),
    (True, "Bash", {"command": "make serve SERVE_FLAGS=--no-reveal"}),
    (True, "Bash", {"command": "CONFIG=c.yaml make -j2 serve"}),
    (True, "Bash", {"command": "cd /repo && /usr/bin/make serve &"}),
    (True, "Bash", {"command": "fnm exec --using=26 make serve"}),
    (True, "Bash", {"command": "fnm exec --using 26 gmake serve"}),
    (True, "Bash", {"command": "timeout 60 make serve"}),
    (True, "Bash", {"command": "bash -c 'make serve'"}),
    (False, "Bash", {"command": "make check"}),
    (False, "Bash", {"command": "make -n help"}),
    (False, "Bash", {"command": "fnm exec --using=26 make ui-e2e"}),
    (False, "Bash", {"command": "grep -n serve Makefile"}),
    (False, "Bash", {"command": "git commit -m 'add a make serve target'"}),
    # `dev ui-server` reveals (synthetic data); only Playwright starts it, under make ui-e2e.
    (True, "Bash", {"command": "./bin/ao3mirror-e2e dev ui-server"}),
    (True, "Bash", {"command": "go run ./cmd/ao3mirror dev ui-server --addr 127.0.0.1:9000"}),
    (True, "Bash", {"command": "make build && ./ao3mirror --log-level debug dev ui-server &"}),
    (True, "Bash", {"command": "bash -c 'ao3mirror dev ui-server'"}),
    (True, "Bash", {"command": "./bin/ao3mirror-e2e serve"}),
    (False, "Bash", {"command": "make ui-e2e"}),
    (False, "Bash", {"command": "./ao3mirror dev fake-server"}),
    (False, "Bash", {"command": "go test ./cmd/ao3mirror -run UIServer"}),
    (False, "Bash", {"command": "git commit -m 'add ao3mirror dev ui-server'"}),
]


def run(tool, ti):
    p = subprocess.run(
        [sys.executable, "-I", GUARD],
        input=json.dumps({"tool_name": tool, "tool_input": ti}),
        capture_output=True, text=True,
    )
    return p.returncode == 2


def check_same_as_old_regexes():
    """strip_allowed and reveal_doc_path match the old regexes exactly on random input."""
    rng = random.Random(20261126)
    failed = 0
    for _ in range(200_000):
        s = "".join(rng.choice(PIECES) for _ in range(rng.randint(0, 12)))
        want, got = OLD_ALLOWED.sub(" ", s), guard.strip_allowed(s)
        if got != want:
            failed += 1
            print(f"FAIL strip_allowed({s!r}) = {got!r}, old regex gave {want!r}")
        want_doc, got_doc = bool(OLD_REVEAL_DOC_PATH.search(s)), guard.reveal_doc_path(s)
        if got_doc != want_doc:
            failed += 1
            print(f"FAIL reveal_doc_path({s!r}) = {got_doc}, old regex gave {want_doc}")
        if failed > 20:
            break
    return failed


def check_linear_time():
    """Inputs that made the old regexes quadratic (CodeQL py/polynomial-redos) stay fast."""
    failed = 0
    n = 200_000
    for name, fn, s in [
        ("strip_allowed, many data/logs/", guard.strip_allowed, "data/logs/" * n),
        ("strip_allowed, many data/debug/", guard.strip_allowed, "data/debug/" * n),
        ("strip_allowed, many ./data/logs", guard.strip_allowed, "./data/logs" * n),
        ("reveal_doc_path, many /docs/", guard.reveal_doc_path, "/docs/" * n),
        ("reveal_doc_path, many docs/ + space", guard.reveal_doc_path, "/docs/" * n + " .md"),
    ]:
        start = time.monotonic()
        fn(s)
        took = time.monotonic() - start
        if took > 2:
            failed += 1
            print(f"FAIL {name}: {took:.1f}s on {len(s)} chars")
    return failed


def main():
    failed = 0
    for want, tool, ti in CASES:
        got = run(tool, ti)
        if got != want:
            failed += 1
            print(f"FAIL want_blocked={want} got={got}: {tool} {ti}")
    print(f"{len(CASES) - failed}/{len(CASES)} cases passed")
    failed += check_same_as_old_regexes()
    failed += check_linear_time()
    print("FAILED" if failed else "all passed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
