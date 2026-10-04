// Package framestream implements a length-prefixed, bidirectional stream
// state machine. Each endpoint can half-close its send direction while
// continuing to receive frames from the peer. A reset terminates both
// directions immediately. If the transport closes in the middle of a
// frame, the truncation is reported.
//
// The state machine is transport-agnostic: the caller moves bytes between
// the Endpoint and the underlying connection via Pending/Input/InputEOF.
//
// Wire format: uint32 big-endian length prefix followed by payload bytes.
package framestream

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// MaxFrameSize bounds a single frame's payload to protect the parser
// from absurd length prefixes.
const MaxFrameSize = 1 << 20 // 1 MiB

const headerLen = 4

// SendState is the state of the local send direction.
type SendState int

const (
	// SendOpen: frames may be queued with Send.
	SendOpen SendState = iota
	// SendHalfClosed: CloseSend was called; no more frames may be sent,
	// but the receive direction stays open.
	SendHalfClosed
	// SendReset: Reset was called; the send direction is dead.
	SendReset
)

func (s SendState) String() string {
	switch s {
	case SendOpen:
		return "SendOpen"
	case SendHalfClosed:
		return "SendHalfClosed"
	case SendReset:
		return "SendReset"
	}
	return fmt.Sprintf("SendState(%d)", int(s))
}

// RecvState is the state of the local receive direction.
type RecvState int

const (
	// RecvOpen: frames may arrive via Input.
	RecvOpen RecvState = iota
	// RecvClosed: the peer finished sending and the transport reached a
	// clean end-of-stream exactly on a frame boundary.
	RecvClosed
	// RecvReset: Reset was called; the receive direction is dead.
	RecvReset
)

func (s RecvState) String() string {
	switch s {
	case RecvOpen:
		return "RecvOpen"
	case RecvClosed:
		return "RecvClosed"
	case RecvReset:
		return "RecvReset"
	}
	return fmt.Sprintf("RecvState(%d)", int(s))
}

// Errors returned by the state machine.
var (
	ErrSendClosed     = errors.New("framestream: send direction is closed")
	ErrRecvClosed     = errors.New("framestream: receive direction is closed")
	ErrReset          = errors.New("framestream: endpoint was reset")
	ErrFrameTooLarge  = errors.New("framestream: frame exceeds MaxFrameSize")
	ErrDoubleClose    = errors.New("framestream: send direction already closed")
	ErrDoubleInputEOF = errors.New("framestream: InputEOF called twice")
)

// TruncatedError reports that the transport reached end-of-stream while a
// frame was only partially received. This is how a peer that went away
// without finishing its last frame is distinguished from a clean close.
type TruncatedError struct {
	// Have is the number of bytes of the incomplete frame already received
	// (including any header bytes).
	Have int
	// Want is the total frame size in bytes (header + payload), or 0 if
	// the stream ended before the 4-byte length header was complete and
	// the total is therefore unknown.
	Want int
}

func (e *TruncatedError) Error() string {
	if e.Want == 0 {
		return fmt.Sprintf("framestream: truncated frame: %d of >=4 header bytes received", e.Have)
	}
	return fmt.Sprintf("framestream: truncated frame: %d of %d bytes received", e.Have, e.Want)
}

// Endpoint is one end of a bidirectional framed stream.
type Endpoint struct {
	send SendState
	recv RecvState

	out      []byte // encoded bytes waiting to be written to the transport
	in       []byte // unparsed bytes received from the transport
	inOffset int    // bytes of `in` already consumed

	onFrame func(payload []byte)
}

// NewEndpoint creates an Endpoint. onFrame is invoked synchronously from
// Input for every complete frame, with the frame payload. The payload
// slice is only valid for the duration of the call.
func NewEndpoint(onFrame func(payload []byte)) *Endpoint {
	if onFrame == nil {
		onFrame = func([]byte) {}
	}
	return &Endpoint{onFrame: onFrame}
}

// SendState returns the state of the send direction.
func (e *Endpoint) SendState() SendState { return e.send }

// RecvState returns the state of the receive direction.
func (e *Endpoint) RecvState() RecvState { return e.recv }

// Send queues one frame for transmission. It fails once the send
// direction is half-closed or reset.
func (e *Endpoint) Send(payload []byte) error {
	switch e.send {
	case SendReset:
		return ErrReset
	case SendHalfClosed:
		return ErrSendClosed
	}
	if len(payload) > MaxFrameSize {
		return ErrFrameTooLarge
	}
	var hdr [headerLen]byte
	binary.BigEndian.PutUint32(hdr[:], uint32(len(payload)))
	e.out = append(e.out, hdr[:]...)
	e.out = append(e.out, payload...)
	return nil
}

// CloseSend half-closes the send direction: no further frames may be
// queued, but the receive direction remains open and the peer's remaining
// frames are still delivered. The caller should flush Pending and then
// close the transport's write side so the peer observes end-of-stream.
func (e *Endpoint) CloseSend() error {
	switch e.send {
	case SendReset:
		return ErrReset
	case SendHalfClosed:
		return ErrDoubleClose
	}
	e.send = SendHalfClosed
	return nil
}

// Reset terminates both directions immediately. Queued outbound bytes and
// any partially received frame are discarded. All subsequent operations
// fail with ErrReset.
func (e *Endpoint) Reset() {
	e.send = SendReset
	e.recv = RecvReset
	e.out = nil
	e.in = nil
	e.inOffset = 0
}

// Pending returns the encoded bytes waiting to be written to the
// transport. The slice is valid until the next call to Send, Consume, or
// Reset.
func (e *Endpoint) Pending() []byte { return e.out }

// Consume discards n bytes from the front of the pending outbound buffer,
// after the caller has written them to the transport.
func (e *Endpoint) Consume(n int) {
	if n > len(e.out) {
		n = len(e.out)
	}
	e.out = e.out[n:]
}

// Input feeds bytes read from the transport into the state machine.
// Complete frames are delivered to the onFrame callback, in order. Input
// accepts arbitrary read boundaries: a frame may be split across any
// number of calls, and one call may carry several frames.
func (e *Endpoint) Input(p []byte) error {
	switch e.recv {
	case RecvReset:
		return ErrReset
	case RecvClosed:
		return ErrRecvClosed
	}
	e.in = append(e.in, p...)
	for {
		avail := len(e.in) - e.inOffset
		if avail < headerLen {
			return nil
		}
		n := binary.BigEndian.Uint32(e.in[e.inOffset : e.inOffset+headerLen])
		if n > MaxFrameSize {
			return ErrFrameTooLarge
		}
		if avail < headerLen+int(n) {
			return nil
		}
		payload := e.in[e.inOffset+headerLen : e.inOffset+headerLen+int(n)]
		e.onFrame(payload)
		e.inOffset += headerLen + int(n)
		if e.inOffset == len(e.in) {
			e.in = e.in[:0]
			e.inOffset = 0
		}
	}
}

// InputEOF tells the state machine that the transport's read side reached
// end-of-stream. A nil error means the peer finished cleanly: the stream
// ended exactly on a frame boundary. A *TruncatedError means the peer
// went away in the middle of a frame, i.e. it did not finish sending.
func (e *Endpoint) InputEOF() error {
	switch e.recv {
	case RecvReset:
		return ErrReset
	case RecvClosed:
		return ErrDoubleInputEOF
	}
	e.recv = RecvClosed
	pending := len(e.in) - e.inOffset
	if pending == 0 {
		return nil
	}
	err := &TruncatedError{Have: pending}
	if pending >= headerLen {
		n := binary.BigEndian.Uint32(e.in[e.inOffset : e.inOffset+headerLen])
		err.Want = headerLen + int(n)
	}
	e.in = nil
	e.inOffset = 0
	return err
}
