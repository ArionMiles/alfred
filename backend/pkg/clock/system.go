// Package clock provides time sources for Alfred.
package clock

import "time"

// System reads time from the operating system.
type System struct{}

// Now returns the current wall-clock time with its monotonic reading.
func (System) Now() time.Time {
	return time.Now()
}
