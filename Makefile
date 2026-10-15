GO ?= go
GOLANGCI_LINT ?= golangci-lint

.PHONY: build test test-race examples vet fmt fmt-check lint ci

build:
	$(GO) build ./...

# -p 1: package test binaries run serially, so the SQL suites (store and
# conformance runners) never share one database concurrently — each wipes
# its tables between subtests and would corrupt another package's run.
test:
	$(GO) test -p 1 ./...

test-race:
	$(GO) test -race -p 1 ./...

# The minimal example must start, serve, and shut down cleanly on SIGTERM
# with no chain service anywhere (spec §16.3, §22). --preserve-status
# makes the child's exit code ours: a hang fails the target.
examples:
	@bin=$$(mktemp -d); \
	$(GO) build -o $$bin/minimal ./examples/minimal; \
	status=$$?; \
	if [ $$status -eq 0 ]; then \
		timeout -k 5s --preserve-status 15s $$bin/minimal > $$bin/out 2>&1; status=$$?; \
		if ! grep -q "clean shutdown" $$bin/out; then \
			echo "minimal example did not shut down cleanly:"; cat $$bin/out; status=1; \
		fi; \
	fi; \
	rm -rf $$bin; exit $$status

vet:
	$(GO) vet ./...

fmt:
	gofmt -w .

fmt-check:
	@files=$$(gofmt -l .); if [ -n "$$files" ]; then echo "not gofmt'd:"; echo "$$files"; exit 1; fi

lint:
	$(GOLANGCI_LINT) run

ci: fmt-check vet build test
