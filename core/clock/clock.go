// Package clock provides the time abstraction used across the framework
// so that time-dependent behavior stays deterministic under test.
package clock

import "time"

// Clock provides the current time. Framework components depend on this
// interface instead of calling time.Now directly.
type Clock interface {
	Now() time.Time
}

// System is the real-time clock.
type System struct{}

// Now returns the wall-clock time.
func (System) Now() time.Time { return time.Now() }
