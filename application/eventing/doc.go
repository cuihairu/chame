// Package eventing implements event delivery orchestration (spec §11):
// it reads chain events from pluggable sources, persists them with
// identity deduplication, gates delivery on finality, retries failures,
// advances resumable cursors, and handles reorganizations by invalidating
// and rescanning. Delivery is at least once; consumers must be safe to
// run repeatedly for the same event (§11.4).
package eventing
