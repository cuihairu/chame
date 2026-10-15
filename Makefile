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

# Every example must start, run, and shut down cleanly on SIGTERM with no
# chain service anywhere (spec §16.3, §22). --preserve-status makes the
# child's exit code ours: a hang fails the target.
examples:
	@bin=$$(mktemp -d); status=0; \
	for ex in minimal eventstream; do \
		$(GO) build -o $$bin/$$ex ./examples/$$ex || { status=$$?; break; }; \
		timeout -k 5s --preserve-status 15s $$bin/$$ex > $$bin/$$ex.out 2>&1; run=$$?; \
		if [ $$run -ne 0 ]; then status=$$run; break; fi; \
		if ! grep -q "clean shutdown" $$bin/$$ex.out; then \
			echo "$$ex example did not shut down cleanly:"; cat $$bin/$$ex.out; status=1; break; \
		fi; \
	done; \
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
