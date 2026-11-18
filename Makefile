# Developer tasks. Run `make check` before every commit.
#
# Nothing here touches AO3 or the data dir: tests run against internal/fakeao3 with synthetic
# fixtures. The only network use is the Go toolchain itself (module downloads, and govulncheck
# reading the Go vulnerability database at vuln.go.dev).

GO       ?= go
GOLANGCI_LINT ?= golangci-lint
GOVULNCHECK_VERSION ?= v1.9.0
PYTHON   ?= python3
BIN      ?= ao3mirror
# Per fuzz target. FUZZFLAGS passes extra flags, e.g. FUZZFLAGS='-parallel 4' to use fewer
# cores (the default is one fuzz worker per core).
FUZZTIME ?= 30s
FUZZFLAGS ?=

.PHONY: all build fmt fmt-check vet tidy-check lint vulncheck deadcode test test-short guard-test check bench fuzz clean help

all: check build

help:
	@echo "make build       build ./$(BIN)"
	@echo "make fmt         gofmt + goimports (grouping) on all Go files"
	@echo "make lint        fmt check + go vet + go mod tidy check + golangci-lint"
	@echo "make vulncheck   govulncheck $(GOVULNCHECK_VERSION) on the module and Go toolchain"
	@echo "make deadcode    unreachable funcs (not part of check; run now and then, e.g. after refactors)"
	@echo "make test        go test -race ./..."
	@echo "make test-short  go test -short ./... (skips tests that use real pacing)"
	@echo "make guard-test  test the Claude guard hook (.claude/hooks/guard.py)"
	@echo "make check       lint + vulncheck + test + guard-test: run before every commit"
	@echo "make bench       content store layout benchmarks (synthetic data)"
	@echo "make fuzz        run every Fuzz* target for FUZZTIME each (default 30s; uses all cores)"

build:
	$(GO) build -o $(BIN) ./cmd/ao3mirror

# Formatters (gofmt, goimports) are configured in .golangci.yml.
fmt:
	$(GOLANGCI_LINT) fmt

fmt-check:
	@out=$$($(GOLANGCI_LINT) fmt --diff); if [ -n "$$out" ]; then \
		echo "formatting needed (run 'make fmt'):"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

tidy-check:
	@$(GO) mod tidy -diff || { echo "go.mod/go.sum not tidy (run 'go mod tidy')"; exit 1; }

lint: fmt-check vet tidy-check
	$(GOLANGCI_LINT) run ./...

# Pinned version run via `go run`, so it needs no install and stays out of go.mod.
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

# golangci-lint's `unused` only sees unexported code; deadcode also finds exported funcs nothing
# calls. First list: unreachable from the binaries (test-only helpers and reads kept for Part 2
# show up here). Second list: unreachable even from tests, which is almost always safe to delete.
# Advisory, so it is not part of `check`. The tool is pinned in go.mod (go tool).
deadcode:
	@echo "== unreachable from cmd/ (production)"
	@$(GO) tool deadcode ./...
	@echo "== unreachable even from tests"
	@$(GO) tool deadcode -test ./...

test:
	$(GO) test -race ./...

test-short:
	$(GO) test -short ./...

guard-test:
	$(PYTHON) -I .claude/hooks/guard_test.py

check: lint vulncheck test guard-test
	@echo "check: all passed"

bench:
	$(GO) test ./internal/store -run '^$$' -bench 'Blob|ChapterColumnOrder' -benchtime 2000x

# Real fuzzing; `make test` only replays the seed inputs. `go test -fuzz` takes one target per
# run, so this finds every Fuzz* func and runs each in turn, stopping at the first failure. A
# failing input is saved under the package's testdata/fuzz/; commit it as a regression seed.
# Not part of `check`: it is slow and keeps all cores busy.
fuzz:
	@set -e; for dir in $$(grep -rlE '^func Fuzz' --include='*_test.go' internal cmd | xargs -n1 dirname | sort -u); do \
		for fn in $$($(GO) test -list '^Fuzz' ./$$dir | grep '^Fuzz'); do \
			echo "== ./$$dir $$fn ($(FUZZTIME))"; \
			$(GO) test ./$$dir -run '^$$' -fuzz "^$$fn$$" -fuzztime $(FUZZTIME) $(FUZZFLAGS); \
		done; \
	done

clean:
	rm -f $(BIN)
