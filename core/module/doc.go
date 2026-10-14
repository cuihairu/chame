// Package module defines the Module interface, its registry, and the
// dependency-graph rules modules must satisfy (spec §4.3). The registry
// offers the controlled registration surface the spec requires: named
// capabilities, event handlers, health checks, and background tasks —
// with duplicate detection. It is deliberately not a container for
// reaching into other modules' internals.
package module
