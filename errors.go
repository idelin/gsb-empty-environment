package netsec

import "errors"

var (
	// ErrAdmissionClosed reports that the connection admission is shut down.
	ErrAdmissionClosed = errors.New("push admission: connection admission closed")
	// ErrGenerationMismatch reports a stale or foreign connection generation.
	ErrGenerationMismatch = errors.New("push admission: connection generation mismatch")
	// ErrInvalidStreamID reports a stream ID that cannot be used on this
	// connection.
	ErrInvalidStreamID = errors.New("push admission: invalid HTTP/2 stream ID")
	// ErrStreamIDReused reports a promised stream ID that has already been
	// observed on this connection generation.
	ErrStreamIDReused = errors.New("push admission: promised stream ID reused")
	// ErrStreamIDOutOfOrder reports a promised stream ID that is syntactically
	// valid but not greater than every previously promised stream ID.
	ErrStreamIDOutOfOrder = errors.New("push admission: promised stream ID out of order")
	// ErrParentStreamReused reports reuse of a parent stream ID.
	ErrParentStreamReused = errors.New("push admission: parent stream ID reused")
	// ErrParentNotFound reports a promise for an unknown parent stream.
	ErrParentNotFound = errors.New("push admission: parent request not found")
	// ErrParentCanceled reports a promise for a canceled parent request.
	ErrParentCanceled = errors.New("push admission: parent request canceled")
	// ErrParentEnded reports a promise arriving after the parent request
	// lifetime ended.
	ErrParentEnded = errors.New("push admission: parent request ended")
	// ErrUnauthorizedSource reports a promised origin that is outside the
	// parent's authorization snapshot.
	ErrUnauthorizedSource = errors.New("push admission: source not authorized by parent request")
	// ErrMalformedPromise reports invalid PUSH_PROMISE pseudo-headers or
	// request headers.
	ErrMalformedPromise = errors.New("push admission: malformed PUSH_PROMISE")
	// ErrAllocationFailed reports that the host allocator rejected the push.
	ErrAllocationFailed = errors.New("push admission: resource allocation failed")
	// ErrPushCanceled reports that a push was canceled before it became active.
	ErrPushCanceled = errors.New("push admission: push canceled")
	// ErrPushClosed reports an operation on a completed push.
	ErrPushClosed = errors.New("push admission: push closed")
	// ErrInvalidSource reports an invalid origin used to configure a parent.
	ErrInvalidSource = errors.New("push admission: invalid source")
)
