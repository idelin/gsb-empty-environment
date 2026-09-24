package negcache

import "time"

// LogEntry is one structured log record emitted by the cache. Cancellation
// is always flagged separately from source errors so logs never misreport a
// canceled caller as a source failure.
type LogEntry struct {
	Time         time.Time
	Tenant       string
	Key          string
	Class        ErrorClass
	Msg          string
	SourceErr    bool // true only when the source itself failed
	Cancellation bool // true when a caller/context was canceled
}

// Logger receives structured log records. Injectable via Config.
type Logger func(LogEntry)
