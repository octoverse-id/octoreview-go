.DEFAULT_GOAL := help
SHELL := /bin/bash

# `make check` stays toolchain-only (the Go toolchain; no Docker, no network) and
# `make release-check` is the full gate.

help: ## List available targets
	@grep -E '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-20s %s\n", $$1, $$2}'

fmt: ## Format the code with gofmt
	gofmt -w .

fmt-check: ## Fail when a file is not gofmt-clean
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "gofmt needs to run on:"; echo "$$out"; exit 1; fi

vet: ## Run go vet (including the integration suite's build tag)
	go vet ./...
	go vet -tags integration ./...

test: ## Run the tests with the race detector (offline; includes the contract gate)
	go test -race -count=1 ./...

contract: ## Run the offline contract gate against testdata/contract (server docs/spec.md section 18)
	@set -o pipefail; go test -count=1 -run 'TestEvery|TestErrorCodes|TestTheReadme' -v ./... | \
		grep -E '^(=== RUN|--- |ok|FAIL|PASS)'

vendor-contract: ## Refresh testdata/contract from the server (SERVER=<ref or checkout path>, default main; network)
	@./scripts/vendor-contract.sh $(or $(SERVER),main)

# The server image the integration suite runs against: the release candidate 0.1.0 ships with
# (release tags never move), or a published `sha-<commit>` of the server's main branch between
# releases; named in docs/versioning.md and moved by a bump PR.
SERVER_IMAGE ?= ghcr.io/octoverse-id/octoreview:1.0.0-rc.3

integration: ## Run the quickstart and the integration suite against SERVER_IMAGE, with local and S3 media (needs Docker)
	@./scripts/integration.sh $(SERVER_IMAGE) local
	@./scripts/integration.sh $(SERVER_IMAGE) s3

fixtures: ## Like integration, and refresh testdata/fixtures from the real server (needs Docker)
	@WRITE_FIXTURES=1 ./scripts/integration.sh $(SERVER_IMAGE) local

lint: fmt-check vet ## gofmt, go vet and golangci-lint (needs golangci-lint on PATH)
	golangci-lint run ./...

vuln: ## Scan the module with govulncheck (needs govulncheck on PATH and network)
	govulncheck ./...

llms-check: ## Fail when llms.txt links a missing local path (and prove the check can fail)
	@./scripts/check-llms-txt.sh
	@./scripts/check-llms-txt-test.sh

version-check: ## Fail unless version.go and a CHANGELOG heading name one releasable version (a release PR's gate)
	@./scripts/version-check.sh

release-tooling-check: ## Prove the release scripts (version-check, check-release-merge, release-notes) pass and fail as documented
	@./scripts/release-tooling-test.sh

actionlint: ## Lint GitHub Actions workflows (needs actionlint on PATH)
	@actionlint -color

placeholder-check: ## Fail while any bootstrap CI placeholder job remains
	@if grep -rn 'octoreview:placeholder' .github/workflows; then \
		echo "placeholder-check: bootstrap placeholder jobs remain; replace them before a release" >&2; exit 1; \
	fi; echo "placeholder-check OK"

check: fmt-check vet test llms-check release-tooling-check ## Fast gate (toolchain only)

release-check: check lint vuln integration placeholder-check ## Full pre-release gate (fails while placeholders remain)

export-rulesets: ## Export the live rulesets into .github/rulesets/ (needs gh with repo admin)
	@./scripts/export-rulesets.sh

.PHONY: help fmt fmt-check vet test contract vendor-contract integration fixtures lint vuln llms-check actionlint \
	version-check release-tooling-check \
	placeholder-check check release-check export-rulesets
