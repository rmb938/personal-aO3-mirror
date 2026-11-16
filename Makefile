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

.PHONY: all build fmt fmt-check vet tidy-check lint vulncheck test test-short guard-test check bench clean help

all: check build

help:
	@echo "make build       build ./$(BIN)"
	@echo "make fmt         gofmt -w all Go files"
	@echo "make lint        fmt check + go vet + go mod tidy check + golangci-lint"
	@echo "make vulncheck   govulncheck $(GOVULNCHECK_VERSION) on the module and Go toolchain"
	@echo "make test        go test -race ./..."
	@echo "make test-short  go test -short ./... (skips tests that use real pacing)"
	@echo "make guard-test  test the Claude guard hook (.claude/hooks/guard.py)"
	@echo "make check       lint + vulncheck + test + guard-test: run before every commit"
	@echo "make bench       content store layout benchmarks (synthetic data)"

build:
	$(GO) build -o $(BIN) ./cmd/ao3mirror

fmt:
	gofmt -w cmd internal

fmt-check:
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then \
		echo "gofmt needed (run 'make fmt'):"; echo "$$out"; exit 1; fi

vet:
	$(GO) vet ./...

tidy-check:
	@$(GO) mod tidy -diff || { echo "go.mod/go.sum not tidy (run 'go mod tidy')"; exit 1; }

lint: fmt-check vet tidy-check
	$(GOLANGCI_LINT) run ./...

# Pinned version run via `go run`, so it needs no install and stays out of go.mod.
vulncheck:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

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

clean:
	rm -f $(BIN)
