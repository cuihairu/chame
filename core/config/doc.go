// Package config loads, merges, and validates application configuration
// (spec §5): layered sources, explicit validation, and sensitive-value
// handling that never leaks secrets into logs.
//
// Status: skeleton. Implementation lands with the phased plan in todo.md.
package config
