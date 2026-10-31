# CI calls these same targets (see .github/workflows), so `make ci` locally
# matches what runs on GitHub.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

MODULE  := github.com/brettmostert/agent-command
# Tools live in $(TOOLS_DIR)/<versions>: a new pinned version gets a fresh
# directory instead of a stale binary. In GitHub Actions they go to the
# runner's tool cache, which on the self-hosted runner persists outside the
# checkout (so `git clean` doesn't wipe them).
ifdef RUNNER_TOOL_CACHE
TOOLS_DIR ?= $(RUNNER_TOOL_CACHE)/acmd-tools
else
TOOLS_DIR ?= $(CURDIR)/bin
endif
DIST    := $(CURDIR)/dist

# Pinned tool versions (Dependabot doesn't track these; bump deliberately).
GOLANGCI_LINT_VERSION := v2.15.0
GORELEASER_VERSION    := v2.19.0
GOVULNCHECK_VERSION   := v1.9.0
SYFT_VERSION          := v1.55.0
BIN := $(TOOLS_DIR)/$(GOLANGCI_LINT_VERSION)_$(GORELEASER_VERSION)_$(GOVULNCHECK_VERSION)_$(SYFT_VERSION)
export PATH := $(BIN):$(PATH)

# Coverage gates (SPEC §12 Testing).
COVER_MIN_TOTAL := 60
COVER_MIN_CORE  := 80

GIT_SHA   := $(shell git rev-parse HEAD 2>/dev/null)
GIT_SHORT := $(shell git rev-parse --short HEAD 2>/dev/null)
GIT_DIRTY := $(shell git diff --quiet HEAD 2>/dev/null || echo .dirty)
VERSION   ?= 0.0.0-dev$(if $(GIT_SHORT),+$(GIT_SHORT)$(GIT_DIRTY))
DATE      := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS   := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(GIT_SHA) \
	-X $(MODULE)/internal/version.Date=$(DATE)

INSTALL_DIR ?= $(HOME)/.local/bin

##@ General

.PHONY: help
help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2} /^##@/ {printf "\n%s\n", substr($$0, 5)}' $(MAKEFILE_LIST)

##@ Tools

# go install with retries: the module proxy occasionally drops a download
# mid-stream (seen in CI as "stream error ... INTERNAL_ERROR").
define go_install
	@for i in 1 2 3; do \
	  GOBIN=$(BIN) go install $(1) && exit 0; \
	  echo "go install $(1) failed (attempt $$i/3)"; [ $$i -lt 3 ] && sleep 10; \
	done; exit 1
endef

$(BIN)/golangci-lint:
	$(call go_install,github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION))

$(BIN)/goreleaser:
	$(call go_install,github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION))

$(BIN)/govulncheck:
	$(call go_install,golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION))

$(BIN)/syft:
	$(call go_install,github.com/anchore/syft/cmd/syft@$(SYFT_VERSION))

.PHONY: tools
tools: $(BIN)/golangci-lint $(BIN)/goreleaser $(BIN)/govulncheck $(BIN)/syft ## Install pinned tools into $(TOOLS_DIR)

##@ Checks

.PHONY: fmt
fmt: ## Check formatting (gofmt)
	@out="$$(gofmt -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './bin/*'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	go mod tidy -diff

.PHONY: workflow-check
workflow-check: ## Fail if a workflow runs a ./bin tool directly (bypasses make's PATH, see v0.1.0)
	@if grep -nE 'run: *(\./)?bin/' .github/workflows/*.yml; then \
	  echo "workflows must call tools via make targets, not bin/<tool>"; exit 1; fi

.PHONY: lint
lint: fmt tidy-check workflow-check $(BIN)/golangci-lint ## gofmt, go vet, go mod tidy check, golangci-lint
	go vet ./...
	$(BIN)/golangci-lint config verify
	$(BIN)/golangci-lint run

.PHONY: test
test: ## Run tests with -race and enforce coverage gates
	go test -race -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	scripts/coverage-gate.sh coverage.out $(COVER_MIN_TOTAL) $(COVER_MIN_CORE)

.PHONY: fuzz
fuzz: ## Run each fuzz target briefly (FUZZTIME=30s)
	scripts/fuzz.sh $${FUZZTIME:-30s}

.PHONY: vuln
vuln: $(BIN)/govulncheck ## Scan for known vulnerabilities
	$(BIN)/govulncheck ./...

.PHONY: ci
ci: lint test vuln build ## Everything CI checks on a PR (except snapshot/formula)

##@ Build & release

.PHONY: build
build: ## Build ./dist/agent-command (+ acmd symlink) for this machine
	mkdir -p $(DIST)
	CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/agent-command ./cmd/agent-command
	ln -sf agent-command $(DIST)/acmd
	$(DIST)/acmd version

.PHONY: snapshot
snapshot: $(BIN)/goreleaser $(BIN)/syft ## Full release dry-run (no publish) into ./dist
	$(BIN)/goreleaser check
	$(BIN)/goreleaser release --snapshot --clean
	scripts/check-dist.sh $(DIST)

.PHONY: release
release: $(BIN)/goreleaser $(BIN)/syft ## CI only: publish the tagged release with GoReleaser (needs GITHUB_TOKEN)
	$(BIN)/goreleaser release --clean

.PHONY: formula-test
formula-test: ## Install the snapshot universal tarball via a throwaway local tap and run brew test
	scripts/test-formula.sh $(DIST)

.PHONY: install
install: build ## Dev install into $(INSTALL_DIR) (agent-command + acmd)
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(DIST)/agent-command $(INSTALL_DIR)/agent-command
	ln -sf agent-command $(INSTALL_DIR)/acmd
	@echo "installed to $(INSTALL_DIR) (make sure it's on PATH)"

##@ Integration (opt-in, real terminals)

.PHONY: it-kitty it-ghostty
it-kitty: ## Integration tests against a real Kitty (added in M2)
	ACMD_IT_KITTY=1 go test -run Integration ./...
it-ghostty: ## Integration tests against a real Ghostty (local only; added in M4)
	ACMD_IT_GHOSTTY=1 go test -run Integration ./...

.PHONY: clean
clean: ## Remove build output
	rm -rf $(DIST) coverage.out
