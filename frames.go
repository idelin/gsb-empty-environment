package netsec

import (
	"context"
	"errors"
)

// Frame kinds exchanged during an event-stream subscription handshake and
// replay. They map onto the relay's wire frames; the cursor package only
// defines the vocabulary so it stays independent of any transport codec.
const (
	// FrameEvent is a replayed or live event. Seq is its event sequence.
	FrameEvent = "event"
	// FrameOutdatedCursor is an informational frame sent once when the
	// requested cursor predates the retained window. Replay then starts at
	// the window's earliest sequence.
	FrameOutdatedCursor = "outdated_cursor"
	// FrameFutureCursor is an error frame: the cursor is ahead of the newest
	// sequence. The stream is closed after it is written.
	FrameFutureCursor = "future_cursor"
	// FrameReplayLimit is an error frame: the source already runs the maximum
	// number of concurrent replays. The stream is closed after it is written.
	FrameReplayLimit = "replay_limit"
	// FrameReplayFailed is an error frame for terminal replay failures such as
	// the retention window rolling past an in-flight replay. The stream is
	// closed after it is written.
	FrameReplayFailed = "replay_failed"
)

// Frame is one unit written to a subscriber.
type Frame struct {
	Kind string
	// Seq is the event sequence for FrameEvent and the reference sequence for
	// diagnostic frames (requested, earliest, or newest cursor, depending on
	// the kind).
	Seq uint64
	// Earliest is the window's earliest retained sequence, when relevant.
	Earliest uint64
	// Newest is the newest known sequence, when relevant.
	Newest uint64
	// Payload is the event payload for FrameEvent; nil for other kinds.
	Payload []byte
	// Err carries the underlying error for error frames.
	Err error
}

// FrameWriter is the sink a replay stream writes to. WriteFrame must be safe
// for the relay to transport; Close shuts the subscriber stream down. Both
// methods must be safe to call concurrently with transport-level activity.
type FrameWriter interface {
	WriteFrame(context.Context, Frame) error
	Close() error
}

// Handshake-level sentinel errors. A relay can map these straight onto wire
// error frames; Handshake performs that mapping itself when given a FrameWriter.
var (
	// ErrFutureCursor indicates a cursor greater than the newest sequence.
	ErrFutureCursor = errors.New("netsec: cursor is ahead of the newest event")
	// ErrReplayLimit indicates the per-source concurrent replay cap is full.
	ErrReplayLimit = errors.New("netsec: too many concurrent replays from source")
	// ErrWindowRolled indicates the retention window advanced past a sequence
	// that a replay still needed, so contiguous delivery can no longer be
	// guaranteed.
	ErrWindowRolled = errors.New("netsec: retention window rolled past replay cursor")
)

// errorFrame maps a terminal handshake/replay error onto its wire frame kind.
func errorFrame(err error, ref, earliest, newest uint64) Frame {
	switch {
	case errors.Is(err, ErrFutureCursor):
		return Frame{Kind: FrameFutureCursor, Seq: ref, Earliest: earliest, Newest: newest, Err: err}
	case errors.Is(err, ErrReplayLimit):
		return Frame{Kind: FrameReplayLimit, Seq: ref, Earliest: earliest, Newest: newest, Err: err}
	default:
		return Frame{Kind: FrameReplayFailed, Seq: ref, Earliest: earliest, Newest: newest, Err: err}
	}
}
