# Quick Start

## Prerequisites

- Go 1.26+
- `make` and a C toolchain (for `golangci-lint`)

## Build and test

```sh
make ci        # gofmt check + vet + build + test
make test-race
make lint      # golangci-lint
```

`make ci` runs gofmt check, `go vet`, build and the full test suite. CI runs the
same checks on Go 1.26.x and 1.27.x, plus golangci-lint and a dependency
vulnerability check.

## Minimal runtime

The minimal example wires a runtime with a module registry, a layered config,
and a graceful stop; it needs no chain service:

```sh
go run ./examples/minimal
```

The signing example demonstrates the test signer, audit hooks, allow-list
rejection, and generated dev keys (no key material is committed):

```sh
go run ./examples/signing
```

The event-stream example runs an in-process event source against the memory
store and the dispatcher, and shuts down cleanly on SIGTERM:

```sh
go run ./examples/eventstream
```

## Next steps

- Read the [spec](https://github.com/cuihairu/chame/blob/main/docs/design/chame-spec.md) for the full architecture.
- Read the [config / API notes](/config) for configuration keys and entry
  points.
- See `todo.md` for the phased implementation ledger.
