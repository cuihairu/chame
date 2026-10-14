GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test test-race vet fmt fmt-check lint ci

build:
	$(GO) build ./...

test:
	$(GO) test ./...

test-race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "not gofmt'd:"; echo "$$files"; exit 1; fi

lint:
	$(GOLANGCI_LINT) run

ci: fmt-check vet build test
