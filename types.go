package batchclient

// Request is a single unit of work. ID is the identity used to align
// responses with requests; it is preserved across retries and recovery.
type Request struct {
	ID   string
	Data string
}

// Response is the server-side commit for one request ID. Version is the
// server's monotonic commit version for that ID and is the sole arbiter
// when multiple attempts for the same ID are in flight.
type Response struct {
	ID      string
	Version uint64
	Value   string
	Err     string
}

// IsErr reports whether the response is a committed item-level failure.
func (r Response) IsErr() bool { return r.Err != "" }

// Status is the terminal state of one submitted request.
type Status int

const (
	// StatusOK: a success response was confirmed.
	StatusOK Status = iota
	// StatusFailed: a committed item-level failure was confirmed.
	StatusFailed
	// StatusConflict: two responses carried the same commit version with
	// different results; the item is isolated as a conflict.
	StatusConflict
	// StatusMissing: attempts were exhausted without any response.
	StatusMissing
	// StatusCancelled: the item was never dispatched before cancellation.
	StatusCancelled
)

func (s Status) String() string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusFailed:
		return "Failed"
	case StatusConflict:
		return "Conflict"
	case StatusMissing:
		return "Missing"
	case StatusCancelled:
		return "Cancelled"
	}
	return "Unknown"
}

// Outcome is the final result for one input request, aligned to input order.
type Outcome struct {
	Request Request
	Status  Status
	Version uint64
	Value   string
	Err     string
}
