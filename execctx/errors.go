package execctx

import "errors"

var (
	// ErrBudgetExhausted is returned when an entry cannot be charged because
	// the shared budget cannot cover the requested amount.
	ErrBudgetExhausted = errors.New("execctx: resource budget exhausted")
	// ErrCanceled is returned by entries taken after the request was canceled.
	ErrCanceled = errors.New("execctx: execution context canceled")
	// ErrDeadlineExceeded is returned by entries taken after the shared
	// deadline has elapsed.
	ErrDeadlineExceeded = errors.New("execctx: execution deadline exceeded")
	// ErrFinished is returned once the request has produced its end event.
	ErrFinished = errors.New("execctx: request already finished")
	// ErrInvalidCharge is returned for zero or negative charge requests.
	ErrInvalidCharge = errors.New("execctx: charge must be positive")
	// ErrInvalidLease is returned when a callback presents an unknown or
	// released lease, or the host detaches from an ineligible frame.
	ErrInvalidLease = errors.New("execctx: invalid or released lease")
	// ErrSettled is returned when a frame is completed or failed more than
	// once; double settlement never refunds twice.
	ErrSettled = errors.New("execctx: frame already settled")
	// ErrBadRefund is returned when a refund claims more than the frame was
	// charged.
	ErrBadRefund = errors.New("execctx: refund exceeds frame charge")
	// ErrReentered is returned when a settled or detached synchronous frame
	// is used as the parent of another entry.
	ErrReentered = errors.New("execctx: parent frame no longer active")
	// ErrMaxDepth is returned when an entry would exceed the configured
	// nesting limit.
	ErrMaxDepth = errors.New("execctx: maximum nesting depth exceeded")
	// ErrPluginFailed is the default failure cause when a frame is failed
	// with a nil error.
	ErrPluginFailed = errors.New("execctx: plugin reported failure")
)

// PanicError wraps a value recovered from a panicking plugin. The frame is
// failed with this error so a malicious plugin cannot crash the host.
type PanicError struct {
	Value any
}

func (e *PanicError) Error() string {
	return "execctx: plugin panic: " + panicText(e.Value)
}

func panicText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case error:
		return x.Error()
	default:
		return "panic"
	}
}
