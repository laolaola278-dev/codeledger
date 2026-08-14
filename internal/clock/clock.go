// Package clock provides an injectable time source so lease and lock logic
// can be tested deterministically. Production code uses RealClock; tests use
// FixedClock (or a synthetic clock) to control time without sleeping.
package clock

import "time"

// Clock abstracts time.Now so callers can inject a deterministic time source.
type Clock interface {
	// Now returns the current time in UTC.
	Now() time.Time
}

// RealClock returns the real system time.
type RealClock struct{}

// Now implements Clock using time.Now.
func (RealClock) Now() time.Time { return time.Now().UTC() }

// FixedClock returns a fixed time. It implements Clock and is safe for
// concurrent use because it holds no mutable state.
type FixedClock struct {
	T time.Time
}

// Now implements Clock, always returning the fixed time.
func (c FixedClock) Now() time.Time { return c.T.UTC() }
