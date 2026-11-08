package alfred

import "time"

// Clock provides wall-clock timestamps carrying a monotonic reading for
// elapsed-time measurements.
type Clock interface {
	Now() time.Time
}
