# Developer tasks. Run `make check` before every commit.
#
# Nothing here touches AO3 or the data dir: tests run against internal/fakeao3 with synthetic
# fixtures. The only network use is the toolchains themselves: Go (module downloads, and
# govulncheck reading the Go vulnerability database at vuln.go.dev) and npm (`npm ci` from the
# registry, `npm audit` reading its advisories, `playwright install` downloading Chromium).

GO       ?= go
GOLANGCI_LINT ?= golangci-lint
PYTHON   ?= python3
NPM      ?= npm
BIN      ?= ao3mirror
# The web UI (docs/part4-ui.md): its sources, and where `make ui` puts the build for go:embed.
WEB      := web
UI_DIST  := internal/web/ui/dist
# npm ci writes this file, so web deps are reinstalled only when the lockfile changes.
WEB_DEPS := $(WEB)/node_modules/.package-lock.json
# The binary the Playwright tests run (`dev ui-server`), with the UI embedded.
# web/tests/global-setup.ts runs this fixed path; keep the two in step.
E2E_BIN  := bin/ao3mirror-e2e
# Extra flags for `playwright install`. CI sets --with-deps (installs the browser's system
# libraries with apt); locally they are usually present already.
PLAYWRIGHT_INSTALL_FLAGS ?=
# Per fuzz target. FUZZFLAGS passes extra flags, e.g. FUZZFLAGS='-parallel 4' to use fewer
# cores (the default is one fuzz worker per core).
FUZZTIME ?= 30s
FUZZFLAGS ?=
# `make serve`: the config it runs with, and extra flags for `serve` (e.g. SERVE_FLAGS=--no-reveal).
CONFIG   ?= ao3mirror.local.yaml
SERVE_FLAGS ?=

.PHONY: all build serve ui ui-check ui-e2e web-deps web-browser generate fmt fmt-check vet tidy-check lint vulncheck deadcode test test-short guard-test check bench fuzz clean help

all: check build

help:
	@echo "make build       build the web UI, then ./$(BIN) with the UI embedded"
	@echo "make serve       build, then run ./$(BIN) serve with CONFIG (default $(CONFIG)) and SERVE_FLAGS"
	@echo "make ui          build the web UI into $(UI_DIST)"
	@echo "make ui-check    web UI checks: svelte-check, eslint, vitest, build, npm audit"
	@echo "make ui-e2e      web UI Playwright tests against a seeded synthetic server (dev ui-server)"
	@echo "make generate    regenerate the API client and the UI's generated TypeScript"
	@echo "make fmt         gofmt + goimports (grouping) on all Go files"
	@echo "make lint        fmt check + go vet + go mod tidy check + golangci-lint"
	@echo "make vulncheck   govulncheck on the module and Go toolchain"
	@echo "make deadcode    unreachable funcs (not part of check; run now and then, e.g. after refactors)"
	@echo "make test        go test -race ./..."
	@echo "make test-short  go test -short ./... (skips tests that use real pacing)"
	@echo "make guard-test  test the Claude guard hook (.claude/hooks/guard.py)"
	@echo "make check       lint + vulncheck + test + guard-test + ui-check: run before every commit"
	@echo "make bench       content store layout benchmarks (synthetic data)"
	@echo "make fuzz        run every Fuzz* target for FUZZTIME each (default 30s; uses all cores)"

build: ui
	$(GO) build -o $(BIN) ./cmd/ao3mirror

# Runs the API and the web UI over the real data dir, revealing (unless SERVE_FLAGS has
# --no-reveal), so only the user runs it; Claude never does (CLAUDE.md).
serve: build
	./$(BIN) --config $(CONFIG) serve $(SERVE_FLAGS)

$(WEB_DEPS): $(WEB)/package.json $(WEB)/package-lock.json
	$(NPM) --prefix $(WEB) ci
	@touch $@

web-deps: $(WEB_DEPS)

# Headless Chromium, for Vitest's browser tests (ui-check) and Playwright (ui-e2e). Downloaded
# once, cached in ~/.cache/ms-playwright.
web-browser: web-deps
	cd $(WEB) && $(NPM) exec -- playwright install --only-shell $(PLAYWRIGHT_INSTALL_FLAGS) chromium

# The glob skips dotfiles, so dist/.gitkeep (needed by go:embed) stays.
ui: web-deps
	$(NPM) --prefix $(WEB) run build
	rm -rf $(UI_DIST)/*
	cp -R $(WEB)/build/. $(UI_DIST)/

# The build runs here too: the adapter fails it if the page would need an inline script or style.
# Vitest runs the sanitizer tests in headless Chromium (web-browser).
ui-check: web-browser
	$(NPM) --prefix $(WEB) run generate:check
	$(NPM) --prefix $(WEB) run check
	$(NPM) --prefix $(WEB) run lint
	$(NPM) --prefix $(WEB) test
	$(NPM) --prefix $(WEB) run build
	$(NPM) --prefix $(WEB) audit

# Playwright starts the binary itself (web/tests/global-setup.ts): `dev ui-server` makes a new
# temp data dir of synthetic works, serves it with reveal on, and deletes it when the run ends.
ui-e2e: ui web-browser
	$(GO) build -o $(E2E_BIN) ./cmd/ao3mirror
	$(NPM) --prefix $(WEB) run e2e

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

# internal/web/ui's go:generate runs orval (the UI's API client), so it needs the web deps.
generate: web-deps
	$(GO) generate ./...

# Pinned version run via `go run`, so it needs no install and stays out of go.mod.
vulncheck:
	$(GO) tool govulncheck ./...

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

check: lint vulncheck test guard-test ui-check ui-e2e
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
