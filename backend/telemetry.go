package alfred

import (
	"context"
	"time"
)

// TelemetryScope selects a project, session, or run aggregation boundary.
type TelemetryScope struct {
	ProjectID ProjectID
	SessionID SessionID
	RunID     RunID
}

// TelemetryQuery selects telemetry within a scope and optional half-open time
// range. A zero Since or Before value leaves that bound open.
type TelemetryQuery struct {
	Scope  TelemetryScope
	Since  time.Time
	Before time.Time
}

// DurationStats aggregates duration samples. A zero Count means no samples
// were available.
type DurationStats struct {
	Count uint64
	Total time.Duration
	Min   time.Duration
	Max   time.Duration
}

// CostTotal aggregates reported and estimated costs in one currency.
type CostTotal struct {
	Currency        string
	ReportedMicros  uint64
	EstimatedMicros uint64
}

// TelemetryAggregate contains usage, cost, and latency totals for a query.
type TelemetryAggregate struct {
	RunCount       uint64
	ModelCallCount uint64

	Usage TokenUsage
	Costs []CostTotal

	ModelDuration          DurationStats
	ModelTimeToFirstEvent  DurationStats
	ModelTimeToFirstOutput DurationStats
}

// TelemetryReader reads rebuildable telemetry aggregates.
type TelemetryReader interface {
	AggregateTelemetry(context.Context, TelemetryQuery) (TelemetryAggregate, error)
}
