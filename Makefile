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

vet: ## Run go vet
	go vet ./...

test: ## Run the tests with the race detector (offline; includes the contract gate)
	go test -race -count=1 ./...

contract: ## Run the offline contract gate against testdata/contract (server docs/spec.md section 18)
	@set -o pipefail; go test -count=1 -run 'TestEvery|TestErrorCodes|TestTheReadme' -v ./... | \
		grep -E '^(=== RUN|--- |ok|FAIL|PASS)'

vendor-contract: ## Refresh testdata/contract from the server (SERVER=<ref or checkout path>, default main; network)
	@./scripts/vendor-contract.sh $(or $(SERVER),main)

lint: fmt-check vet ## gofmt, go vet and golangci-lint (needs golangci-lint on PATH)
	golangci-lint run ./...

vuln: ## Scan the module with govulncheck (needs govulncheck on PATH and network)
	govulncheck ./...

llms-check: ## Fail when llms.txt links a missing local path (and prove the check can fail)
	@./scripts/check-llms-txt.sh
	@./scripts/check-llms-txt-test.sh

actionlint: ## Lint GitHub Actions workflows (needs actionlint on PATH)
	@actionlint -color

placeholder-check: ## Fail while any bootstrap CI placeholder job remains
	@if grep -rn 'octoreview:placeholder' .github/workflows; then \
		echo "placeholder-check: bootstrap placeholder jobs remain; replace them before a release" >&2; exit 1; \
	fi; echo "placeholder-check OK"

check: fmt-check vet test llms-check ## Fast gate (toolchain only)

release-check: check lint vuln placeholder-check ## Full pre-release gate (fails while placeholders remain)

export-rulesets: ## Export the live rulesets into .github/rulesets/ (needs gh with repo admin)
	@./scripts/export-rulesets.sh

.PHONY: help fmt fmt-check vet test contract vendor-contract lint vuln llms-check actionlint \
	placeholder-check check release-check export-rulesets
