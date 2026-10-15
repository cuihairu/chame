// Package observability hosts concrete observability implementations
// (spec §15): metric collectors and health endpoints. The contracts live in
// core (core/metrics, core/health, core/logging); this package tree wires
// them to a backend.
//
// Status: the in-process metric collector landed with Phase 9 under
// observability/memory. The official Prometheus exporter and the HTTP
// health endpoints are deferred to the production infrastructure phase
// (ADR-0007).
package observability
