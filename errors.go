package netsec

import "errors"

// Sentinel errors returned by [Machine.Handle].
var (
	// ErrNilEvent is returned when Handle receives a nil event.
	ErrNilEvent = errors.New("netsec: nil event")

	// ErrNoEarlyData is returned when early-data records arrive without an
	// [EventBeginEarlyData], or after the server rejected 0-RTT.
	ErrNoEarlyData = errors.New("netsec: early data is not accepted on this connection")

	// ErrEarlyDataLimitExceeded is returned when the cumulative amount of
	// 0-RTT data crosses the ticket's max_early_data limit. Bytes above the
	// limit are withheld and the machine moves to [StateFailed].
	ErrEarlyDataLimitExceeded = errors.New("netsec: cumulative early data exceeds max_early_data limit")

	// ErrUnexpectedEvent is returned when an event is not valid in the
	// current state. The machine moves to [StateFailed] and stays there.
	ErrUnexpectedEvent = errors.New("netsec: unexpected event for current state")

	// ErrConnectionFailed is the terminal error used when the peer sends a
	// fatal TLS alert or otherwise aborts the connection.
	ErrConnectionFailed = errors.New("netsec: connection aborted by peer alert")
)
