# CI calls these same targets (see .github/workflows), so `make ci` locally
# matches what runs on GitHub.

SHELL := /bin/bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help
UNAME_S := $(shell uname -s)

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
ACTIONLINT_VERSION    := v1.7.12
# zizmor isn't a Go tool: scripts/install-zizmor.sh pins its version and hashes.
ZIZMOR_VERSION        := $(shell sed -n 's/^VERSION=//p' scripts/install-zizmor.sh)
BIN := $(TOOLS_DIR)/$(GOLANGCI_LINT_VERSION)_$(GOVULNCHECK_VERSION)_$(GITLEAKS_VERSION)_$(ACTIONLINT_VERSION)_$(ZIZMOR_VERSION)
export PATH := $(BIN):$(PATH)

# With libghostty-vt (scripts/build-ghostty.sh), session hosts keep the exact
# screen, as the app's build does. Without it they replay recent output, and
# full-screen programs need a redraw after attach. `acmd version` says which.
BUILD_TAGS := $(if $(wildcard build/ghostty/vt/include/ghostty/vt.h),libghostty,)
# On Linux, acmd is one static binary, so the brew formula's build runs on any
# distribution, glibc or musl: Go's own DNS and user lookups, and libghostty-vt
# and libc linked in. Without libghostty-vt there's no C code to link.
ifeq ($(UNAME_S),Linux)
ifneq ($(BUILD_TAGS),)
BUILD_TAGS   += netgo osusergo
STATIC_FLAGS := -linkmode external -extldflags "-static"
BUILD_CGO    := 1
else
BUILD_CGO    := 0
endif
else
BUILD_CGO    := 1
endif

# Coverage gates: all code, internal/core and internal/remote.
COVER_MIN_TOTAL  := 60
COVER_MIN_CORE   := 80
COVER_MIN_REMOTE := 80

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

$(BIN)/actionlint:
	$(call go_install,github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION))

$(BIN)/zizmor: scripts/install-zizmor.sh
	scripts/install-zizmor.sh $(BIN)

.PHONY: tools
tools: $(BIN)/golangci-lint $(BIN)/govulncheck $(BIN)/gitleaks $(BIN)/actionlint $(BIN)/zizmor ## Install pinned tools into $(TOOLS_DIR)

##@ Checks

.PHONY: fmt
fmt: ## Check formatting (gofmt)
	@out="$$(gofmt -l $$(git ls-files '*.go' 2>/dev/null || find . -name '*.go' -not -path './bin/*'))"; \
	if [ -n "$$out" ]; then echo "gofmt needed:"; echo "$$out"; exit 1; fi

.PHONY: tidy-check
tidy-check: ## Fail if go.mod/go.sum are not tidy
	go mod tidy -diff

# actionlint checks the workflows' syntax and expressions. zizmor checks their
# security habits, offline, with .github/zizmor.yml. The grep keeps workflows
# from running a ./bin tool directly (it bypasses make's PATH, see v0.1.0).
.PHONY: workflow-check
workflow-check: $(BIN)/actionlint $(BIN)/zizmor ## Lint the workflow files: actionlint, zizmor, and no ./bin tools run directly
	@if grep -nE 'run: *(\./)?bin/' .github/workflows/*.yml; then \
	  echo "workflows must call tools via make targets, not bin/<tool>"; exit 1; fi
	$(BIN)/actionlint
	$(BIN)/zizmor --offline --no-progress .github

# CHECK_GOOS=darwin checks the macOS build from another platform: CI lints and
# scans it on Ubuntu, which leaves the Mac runners free. cgo is off when
# cross-checking; the only cgo file needs the libghostty tag, which these
# checks don't set. The tools themselves are still built for this machine.
CHECK_ENV := $(if $(CHECK_GOOS),GOOS=$(CHECK_GOOS) CGO_ENABLED=0,)

.PHONY: lint
lint: fmt tidy-check workflow-check $(BIN)/golangci-lint ## gofmt, go vet, go mod tidy check, golangci-lint (CHECK_GOOS=darwin for another platform's build)
	$(CHECK_ENV) go vet ./...
	$(BIN)/golangci-lint config verify
	$(CHECK_ENV) $(BIN)/golangci-lint run

.PHONY: test
test: ## Run tests with -race and enforce coverage gates
	go test -race -tags noui -shuffle=on -covermode=atomic -coverprofile=coverage.out ./...
	scripts/coverage-gate.sh coverage.out $(COVER_MIN_TOTAL) $(COVER_MIN_CORE) $(COVER_MIN_REMOTE)

.PHONY: fuzz
fuzz: ## Run each fuzz target briefly (FUZZTIME=30s)
	scripts/fuzz.sh $${FUZZTIME:-30s}

.PHONY: vuln
vuln: $(BIN)/govulncheck ## Scan for known vulnerabilities (CHECK_GOOS=darwin for another platform's build)
	$(CHECK_ENV) $(BIN)/govulncheck ./...

.PHONY: secrets
secrets: $(BIN)/gitleaks ## Scan the git history for committed secrets
	$(BIN)/gitleaks git --redact --no-banner --log-level warn .

.PHONY: check-ghostty
check-ghostty: ## Report new Ghostty releases and advisories that haven't been reviewed
	go run ./scripts/check-ghostty

.PHONY: check-cask
check-cask: ## Render the Homebrew cask and formula with dummy values and run brew style and audit (macOS)
	scripts/check-cask.sh
	scripts/check-formula.sh

.PHONY: check-scoop
check-scoop: ## Render the Scoop manifest with dummy values and check it
	scripts/check-scoop.sh

.PHONY: speech-test
speech-test: ## Unit-test the Swift speech engine (helpers/acmd-speech)
	swift run --package-path $(SPEECH_PKG) --scratch-path $(SPEECH_SCRATCH) speech-core-checks

.PHONY: test-vt
test-vt: ## Run the host's tests with libghostty-vt (needs scripts/build-ghostty.sh)
	go test -race -shuffle=on -tags libghostty ./internal/vt ./internal/term

.PHONY: test-app
test-app: ## Run the app's tests and coverage gate (needs scripts/build-ghostty.sh)
	swift test --package-path app --enable-code-coverage
	go run ./scripts/app-coverage "$$(swift test --package-path app --show-codecov-path)"

ifeq ($(UNAME_S),Darwin)
CI_DARWIN_CHECKS := speech-test check-cask
endif
# The libghostty-vt and app tests need scripts/build-ghostty.sh first. Once it
# has run, make ci runs them too, so it is everything CI checks.
ifneq ($(BUILD_TAGS),)
CI_GHOSTTY_CHECKS := test-vt
ifeq ($(UNAME_S),Darwin)
CI_GHOSTTY_CHECKS += test-app
endif
endif

.PHONY: ci
ci: lint secrets test $(CI_DARWIN_CHECKS) $(CI_GHOSTTY_CHECKS) vuln build ## Everything CI checks on a PR (the Ghostty tests once scripts/build-ghostty.sh has run)

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

# The Linux UI's own logic (daemon client, sidebar layout, palette, notice
# routing) has unit tests that need no GTK.
.PHONY: test-ui-core
test-ui-core: ## Run the Linux UI's unit tests that need no GTK
	go test -race -tags noui -cover ./internal/linuxui/...

.PHONY: linux-ui
linux-ui: ## Build ./dist/agent-command-ui (needs GTK4, libadwaita, vte4)
	mkdir -p $(DIST)
	CGO_ENABLED=1 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/agent-command-ui ./cmd/agent-command-ui

.PHONY: build
build: ## Build ./dist/agent-command (+ acmd symlink, + acmd-speech on macOS) for this machine
	mkdir -p $(DIST)
	CGO_ENABLED=$(BUILD_CGO) go build -trimpath -tags '$(BUILD_TAGS)' -ldflags '$(LDFLAGS) $(STATIC_FLAGS)' -o $(DIST)/agent-command ./cmd/agent-command
	ln -sf agent-command $(DIST)/acmd
ifeq ($(UNAME_S),Darwin)
	$(MAKE) speech-helper SPEECH_OUT=$(DIST)/acmd-speech
else
	@if pkg-config --exists gtk4 libadwaita-1 vte-2.91-gtk4 2>/dev/null; then $(MAKE) linux-ui; fi
endif
	$(DIST)/acmd version

# Windows: acmd.exe, a client-only daemon (it connects to hosts, and runs no
# sessions), and acmdw.exe, the logon task's launcher, built with no console
# window. Cross-compiled with cgo off: no libghostty-vt, which only hosts need.
WINDOWS_ARCH ?= amd64
.PHONY: build-windows
build-windows: ## Build ./dist/windows-<arch>/acmd.exe and acmdw.exe (WINDOWS_ARCH=amd64|arm64)
	mkdir -p $(DIST)/windows-$(WINDOWS_ARCH)
	GOOS=windows GOARCH=$(WINDOWS_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags '$(LDFLAGS)' -o $(DIST)/windows-$(WINDOWS_ARCH)/acmd.exe ./cmd/agent-command
	GOOS=windows GOARCH=$(WINDOWS_ARCH) CGO_ENABLED=0 go build -trimpath -ldflags '-s -w -H=windowsgui' -o $(DIST)/windows-$(WINDOWS_ARCH)/acmdw.exe ./cmd/acmdw

.PHONY: install
install: build ## Dev install into $(INSTALL_DIR) (agent-command + acmd)
	mkdir -p $(INSTALL_DIR)
	install -m 0755 $(DIST)/agent-command $(INSTALL_DIR)/agent-command
ifeq ($(UNAME_S),Darwin)
	install -m 0755 $(DIST)/acmd-speech $(INSTALL_DIR)/acmd-speech
	install -m 0644 $(DIST)/mlx.metallib $(INSTALL_DIR)/mlx.metallib
endif
	ln -sf agent-command $(INSTALL_DIR)/acmd
	@echo "installed to $(INSTALL_DIR) (make sure it's on PATH)"


.PHONY: hooks
hooks: ## Use the repo's git hooks (pre-push runs lint and test)
	git config core.hooksPath .githooks
	@echo "git hooks enabled: .githooks/pre-push runs make lint and make test before each push"

.PHONY: clean
clean: ## Remove build output
	rm -rf $(DIST) coverage.out
