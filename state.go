package netsec

import "errors"

// Role identifies which side of a connection the local endpoint is.
// The role determines the parity of locally initiated stream IDs:
// clients use odd IDs, servers use even IDs.
type Role int

const (
	Client Role = iota
	Server
)

func (r Role) String() string {
	if r == Client {
		return "client"
	}
	return "server"
}

// localParity returns the low bit of locally initiated stream IDs.
func (r Role) localParity() uint32 {
	if r == Client {
		return 1
	}
	return 0
}

// remoteParity returns the low bit of peer initiated stream IDs.
func (r Role) remoteParity() uint32 { return 1 - r.localParity() }

// firstLocalID is the first stream ID the local endpoint may open.
func (r Role) firstLocalID() uint32 { return 2 - r.localParity() }

// firstRemoteID is the first stream ID the peer may open.
func (r Role) firstRemoteID() uint32 { return 2 - r.remoteParity() }

// StreamState is the lifecycle state of a single stream.
type StreamState int

const (
	StateIdle StreamState = iota
	StateReservedLocal
	StateReservedRemote
	StateOpen
	StateHalfClosedLocal
	StateHalfClosedRemote
	StateClosed
)

func (s StreamState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateReservedLocal:
		return "reserved(local)"
	case StateReservedRemote:
		return "reserved(remote)"
	case StateOpen:
		return "open"
	case StateHalfClosedLocal:
		return "half-closed(local)"
	case StateHalfClosedRemote:
		return "half-closed(remote)"
	case StateClosed:
		return "closed"
	}
	return "unknown"
}

// Direction tells whether a frame is sent by the local endpoint or
// received from the peer.
type Direction int

const (
	Send Direction = iota
	Recv
)

func (d Direction) String() string {
	if d == Send {
		return "send"
	}
	return "recv"
}

// FrameType is the kind of frame an Event carries.
type FrameType int

const (
	FrameHeaders FrameType = iota
	FrameData
	FrameRSTStream
	FramePushPromise
)

func (t FrameType) String() string {
	switch t {
	case FrameHeaders:
		return "HEADERS"
	case FrameData:
		return "DATA"
	case FrameRSTStream:
		return "RST_STREAM"
	case FramePushPromise:
		return "PUSH_PROMISE"
	}
	return "unknown"
}

// Event is a single frame presented to the stream state machine by the
// frame-dispatch host. Generation must match Conn.Generation; frames
// arriving from a previous connection incarnation are rejected.
type Event struct {
	Generation uint64
	Dir        Direction
	Type       FrameType
	StreamID   uint32
	PromisedID uint32 // PUSH_PROMISE only: ID of the promised stream
	EndStream  bool   // HEADERS/DATA only: END_STREAM flag
}

// Verdict is the dispatch decision for one frame. Frames that are not
// Deliver must never reach the business callback.
type Verdict int

const (
	// Deliver: the frame is state-legal; hand it to the business callback.
	Deliver Verdict = iota
	// Drop: the frame is a tolerated straggler (late RST on a closed
	// stream); do not deliver, do not treat as an error.
	Drop
	// Reject: the frame violates the stream state machine; do not deliver.
	Reject
)

func (v Verdict) String() string {
	switch v {
	case Deliver:
		return "deliver"
	case Drop:
		return "drop"
	case Reject:
		return "reject"
	}
	return "unknown"
}

// Sentinel errors returned by Dispatch. Use errors.Is to match.
var (
	// ErrStaleGeneration: the frame belongs to a previous connection
	// generation and must be discarded.
	ErrStaleGeneration = errors.New("netsec: stale connection generation")
	// ErrIdleStream: the frame references a stream that was never opened.
	ErrIdleStream = errors.New("netsec: frame on idle stream")
	// ErrClosedStream: the frame references a stream that is fully closed.
	ErrClosedStream = errors.New("netsec: frame on closed stream")
	// ErrHalfClosedSend: the local endpoint already sent END_STREAM and
	// must not send further data on this stream.
	ErrHalfClosedSend = errors.New("netsec: send on locally half-closed stream")
	// ErrHalfClosedRecv: the peer already sent END_STREAM and must not
	// send further data on this stream.
	ErrHalfClosedRecv = errors.New("netsec: receive on remotely half-closed stream")
	// ErrReservedStream: the frame is not valid for a reserved stream.
	ErrReservedStream = errors.New("netsec: frame invalid on reserved stream")
	// ErrStreamIDParity: the stream ID parity does not match the
	// initiating endpoint.
	ErrStreamIDParity = errors.New("netsec: stream ID parity mismatch")
	// ErrStreamIDOrder: stream IDs must be used in increasing order.
	ErrStreamIDOrder = errors.New("netsec: stream ID not increasing")
	// ErrRoleViolation: the frame is not permitted for this endpoint role.
	ErrRoleViolation = errors.New("netsec: frame not permitted for endpoint role")
)
