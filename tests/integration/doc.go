// Package integration hosts integration tests (spec §16.3): multi-module
// and adapter behavior that needs real infrastructure. Tests here must be
// build-tagged so unit runs do not require network or database services.
//
// Status: cases that need a live service are build-tagged; cases that only
// compose framework pieces against fakes run in the default suite. The
// observability composition case landed with Phase 9.
package integration
