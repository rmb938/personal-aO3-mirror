#!/usr/bin/env python3
"""Tests for guard.py. Run: python3 -I .claude/hooks/guard_test.py"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "guard.py")
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


def main():
    failed = 0
    for want, tool, ti in CASES:
        got = run(tool, ti)
        if got != want:
            failed += 1
            print(f"FAIL want_blocked={want} got={got}: {tool} {ti}")
    print(f"{len(CASES) - failed}/{len(CASES)} passed")
    sys.exit(1 if failed else 0)


if __name__ == "__main__":
    main()
