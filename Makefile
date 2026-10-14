GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test test-race vet fmt fmt-check lint ci

build:
	$(GO) build ./...

# -p 1: package test binaries run serially, so the SQL suites (store and
# conformance runners) never share one database concurrently — each wipes
# its tables between subtests and would corrupt another package's run.
test:
	$(GO) test -p 1 ./...

test-race:
	$(GO) test -race -p 1 ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "not gofmt'd:"; echo "$$files"; exit 1; fi

lint:
	$(GOLANGCI_LINT) run

ci: fmt-check vet build test
