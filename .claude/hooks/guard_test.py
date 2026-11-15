#!/usr/bin/env python3
"""Tests for guard.py. Run: python3 -I .claude/hooks/guard_test.py"""
import json
import os
import subprocess
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "guard.py")
HOST = "archiveofourown" + ".org"

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
