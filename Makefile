# Development entry points. `make` on its own lists them.
#
# check is the one that earns its keep: CI runs it rather than restating the
# steps, so a green check here is a green build there. The gofmt gate in
# particular used to live only in .github/workflows/ci.yml, where a contributor
# could not run it and where its failure named no file.

# A scratch path, deliberately: scripts/demo-repo.sh opens by rm -rf'ing this.
DEMO_DIR ?= /tmp/git-reap-demo

.DEFAULT_GOAL := help
.PHONY: help check fmt vet test build demo clean

help: ## List these targets
	@awk 'BEGIN {FS = ":.*## "} /^[a-z-]+:.*## / {printf "  %-7s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

check: fmt vet test ## gofmt, vet, and tests -- exactly what CI runs

fmt: ## Fail if anything is not gofmt'd, naming the files
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "not gofmt'd:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet: ## Report what go vet knows to look for
	go vet ./...

test: ## Unit tests, plus the integration test against a real repository
	go test ./...

build: ## Build ./git-reap, which .gitignore already covers
	go build -o git-reap .

demo: build ## Build a repository with one of everything, to try the picker against
	@scripts/demo-repo.sh $(DEMO_DIR)
	@echo
	@echo "Ready. Make cannot leave you in the directory, so:"
	@echo
	@echo "    cd $(DEMO_DIR)/checkout-service"
	@echo "    $(CURDIR)/git-reap --debug --no-fetch"

clean: ## Remove the built binary and the demo repository
	rm -f git-reap
	rm -rf $(DEMO_DIR)
