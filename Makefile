# Development tasks for ai-citation-monitor.
#
# `make check` is the gate CI runs (.github/workflows/ci.yml): change one, change
# the other. Every target is offline — none reads a provider credential or calls a
# real API (see CONTRIBUTING.md).

SHELL := /bin/sh
.DEFAULT_GOAL := help

BINARY := aicm
MODULE := github.com/routeflags/ai-citation-monitor

# Local builds take their version from git, so `aicm version` cannot report a
# release the binary was not built from. Release builds get it from goreleaser.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//'),dev)

# Pinned to the versions CI uses, so a local pass means the CI job would pass.
GOLANGCI_LINT_VERSION ?= v2.15.0
GOVULNCHECK_VERSION ?= v1.9.0
GORELEASER_VERSION ?= v2.20.2

.PHONY: help build aicm install fmt fmt-check vet test test-race lint vuln check \
	tools release-check snapshot clean

help: ## List the targets below.
	@printf 'Usage: make <target>\n\n'
	@awk 'BEGIN {FS = ":.*## "} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-14s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

build: ## Compile every package.
	go build ./...

aicm: ## Build ./aicm from this checkout, stamped with the git version.
	go build -trimpath -ldflags "-X $(MODULE)/internal/runner.Build=$(VERSION)" -o $(BINARY) ./cmd/aicm

install: ## Install aicm into $GOBIN.
	go install -ldflags "-X $(MODULE)/internal/runner.Build=$(VERSION)" ./cmd/aicm

fmt: ## Rewrite files with gofmt (CI only checks them).
	gofmt -w .

fmt-check: ## Fail if a file needs gofmt, as CI does.
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "these files need gofmt:"; echo "$$out"; exit 1; fi

vet: ## Run go vet.
	go vet ./...

test: ## Run the unit tests. No network, no credential.
	go test ./...

test-race: ## Run the unit tests under the race detector (needs a C toolchain; CI does this on Linux).
	go test -race ./...

lint: ## Run golangci-lint at the version CI pins.
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not found: run 'make tools'"; exit 1; }
	golangci-lint run

vuln: ## Run govulncheck. Only reachable vulnerabilities fail.
	go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION) ./...

check: fmt-check vet build test test-race lint vuln ## Run everything CI checks, in order.

tools: ## Install the pinned linters into $GOBIN.
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
	go install github.com/goreleaser/goreleaser/v2@$(GORELEASER_VERSION)

release-check: ## Validate .goreleaser.yaml, as CI does.
	goreleaser check

snapshot: ## Build the release artifacts in dist/ without publishing anything.
	goreleaser release --snapshot --clean

clean: ## Remove build output.
	rm -rf $(BINARY) $(BINARY).exe dist
