// Package config loads, merges, and validates application configuration
// (spec §5): layered sources, explicit validation, and sensitive-value
// handling that never leaks secrets into logs.
package config
