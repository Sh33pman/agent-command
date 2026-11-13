# CI calls these same targets (see .github/workflows), so `make ci` locally
# matches what runs on GitHub.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

MODULE  := github.com/brettmostert/agent-command
# Tools live in $(TOOLS_DIR)/<versions>: a new pinned version gets a fresh
# directory instead of a stale binary. In GitHub Actions they go to the
# runner's tool cache, outside the checkout.
ifdef RUNNER_TOOL_CACHE
TOOLS_DIR ?= $(RUNNER_TOOL_CACHE)/acmd-tools
else
TOOLS_DIR ?= $(CURDIR)/bin
endif
DIST    := $(CURDIR)/dist

# Pinned tool versions (Dependabot doesn't track these; bump deliberately).
GOLANGCI_LINT_VERSION := v2.15.0
GOVULNCHECK_VERSION   := v1.9.0
GITLEAKS_VERSION      := v8.30.1
BIN := $(TOOLS_DIR)/$(GOLANGCI_LINT_VERSION)_$(GOVULNCHECK_VERSION)_$(GITLEAKS_VERSION)
export PATH := $(BIN):$(PATH)

# Coverage gates.
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

# The Swift speech engine (helpers/acmd-speech): arm64 only, built
# with Xcode, which is the only way to compile MLX's Metal shaders. They ship
# next to the helper as mlx.metallib, where MLX looks first. On the runner its
# build folder lives in the tool cache, so checkouts don't rebuild MLX from
# scratch.
SPEECH_PKG := $(CURDIR)/helpers/acmd-speech
ifdef RUNNER_TOOL_CACHE
SPEECH_SCRATCH ?= $(RUNNER_TOOL_CACHE)/acmd-swift-build
else
SPEECH_SCRATCH ?= $(SPEECH_PKG)/.build
endif
SPEECH_OUT ?= $(CURDIR)/build/acmd-speech
SPEECH_PRODUCTS := $(SPEECH_SCRATCH)/xcode/Build/Products/Release

##@ General

.PHONY: help hooks
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

$(BIN)/govulncheck:
	$(call go_install,golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION))

$(BIN)/gitleaks:
	$(call go_install,github.com/zricethezav/gitleaks/v8@$(GITLEAKS_VERSION))

.PHONY: tools
tools: $(BIN)/golangci-lint $(BIN)/govulncheck $(BIN)/gitleaks ## Install pinned tools into $(TOOLS_DIR)

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

.PHONY: secrets
secrets: $(BIN)/gitleaks ## Scan the git history for committed secrets
	$(BIN)/gitleaks git --redact --no-banner --log-level warn .

.PHONY: check-ghostty
check-ghostty: ## Report new Ghostty releases and advisories that haven't been reviewed
	go run ./scripts/check-ghostty

.PHONY: speech-test
speech-test: ## Unit-test the Swift speech engine (helpers/acmd-speech)
	swift run --package-path $(SPEECH_PKG) --scratch-path $(SPEECH_SCRATCH) speech-core-checks

.PHONY: ci
test-vt: ## Run the host's tests with libghostty-vt (needs scripts/build-ghostty.sh)
	go test -race -shuffle=on -tags libghostty ./internal/vt ./internal/term

.PHONY: test-app
test-app: ## Run the app's tests and coverage gate (needs scripts/build-ghostty.sh)
	swift test --package-path app --enable-code-coverage
	go run ./scripts/app-coverage "$$(swift test --package-path app --show-codecov-path)"

ci: lint secrets test speech-test vuln build ## Everything CI checks on a PR (except snapshot/formula)

##@ Build & release

.PHONY: speech-helper
# With SPEECH_CACHE set (CI restores it, keyed on the helper's sources and
# Xcode's version), a helper built there before is used as is, and a new build
# is kept there for the next run.
speech-helper: ## Build the Swift speech engine into $(SPEECH_OUT)
	@if [ -n "$(SPEECH_CACHE)" ] && [ -x "$(SPEECH_CACHE)/acmd-speech" ] && [ -f "$(SPEECH_CACHE)/mlx.metallib" ]; then \
		echo "acmd-speech: using the build in $(SPEECH_CACHE)"; \
	else \
		set -e; \
		cd $(SPEECH_PKG) && xcodebuild -quiet -scheme acmd-speech -destination 'platform=macOS,arch=arm64' \
			-derivedDataPath $(SPEECH_SCRATCH)/xcode -configuration Release \
			-skipPackagePluginValidation -skipMacroValidation CODE_SIGNING_ALLOWED=NO build; \
		if [ -n "$(SPEECH_CACHE)" ]; then \
			mkdir -p $(SPEECH_CACHE); \
			install -m 0755 $(SPEECH_PRODUCTS)/acmd-speech $(SPEECH_CACHE)/acmd-speech; \
			install -m 0644 $(SPEECH_PRODUCTS)/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib $(SPEECH_CACHE)/mlx.metallib; \
		fi; \
	fi
	mkdir -p $(dir $(SPEECH_OUT))
	@if [ -n "$(SPEECH_CACHE)" ]; then \
		install -m 0755 $(SPEECH_CACHE)/acmd-speech $(SPEECH_OUT) && \
		install -m 0644 $(SPEECH_CACHE)/mlx.metallib $(dir $(SPEECH_OUT))mlx.metallib; \
	else \
		install -m 0755 $(SPEECH_PRODUCTS)/acmd-speech $(SPEECH_OUT) && \
		install -m 0644 $(SPEECH_PRODUCTS)/mlx-swift_Cmlx.bundle/Contents/Resources/default.metallib $(dir $(SPEECH_OUT))mlx.metallib; \
	fi
	@if otool -L $(SPEECH_OUT) | grep -qi espeak; then echo "acmd-speech links espeak (GPL)"; exit 1; fi
	$(SPEECH_OUT) --version

.PHONY: app
app: ## Build "dist/Agent Command.app" (needs scripts/build-ghostty.sh first)
	scripts/build-app.sh

.PHONY: build
build: ## Build ./dist/agent-command (+ acmd symlink, + acmd-speech) for this machine
	mkdir -p $(DIST)
	CGO_ENABLED=1 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/agent-command ./cmd/agent-command
	ln -sf agent-command $(DIST)/acmd
	$(MAKE) speech-helper SPEECH_OUT=$(DIST)/acmd-speech
	$(DIST)/acmd version

.PHONY: install
install: build ## Dev install into $(INSTALL_DIR) (agent-command + acmd)
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(DIST)/agent-command $(INSTALL_DIR)/agent-command
	install -m 0755 $(DIST)/acmd-speech $(INSTALL_DIR)/acmd-speech
	install -m 0644 $(DIST)/mlx.metallib $(INSTALL_DIR)/mlx.metallib
	ln -sf agent-command $(INSTALL_DIR)/acmd
	@echo "installed to $(INSTALL_DIR) (make sure it's on PATH)"


.PHONY: clean
hooks: ## Use the repo's git hooks (pre-push runs lint and test)
	git config core.hooksPath .githooks
	@echo "git hooks enabled: .githooks/pre-push runs make lint and make test before each push"

clean: ## Remove build output
	rm -rf $(DIST) coverage.out
