package netsec

import "errors"

// WebSocket opcodes handled by the receive component.
const (
	OpContinuation byte = 0x0
	OpText         byte = 0x1
	OpBinary       byte = 0x2
	OpClose        byte = 0x8
	OpPing         byte = 0x9
	OpPong         byte = 0xA
)

// rfc7692Tail is the 0x00 0x00 0xFF 0xFF DEFLATE sync-flush block that
// RFC 7692 requires a sender to strip and a receiver to append back.
var rfc7692Tail = [4]byte{0x00, 0x00, 0xff, 0xff}

// Sentinel errors returned by Receiver. They are wrapped with context, so
// callers should use errors.Is.
var (
	// ErrProtocol indicates a framing violation (bad opcode, out-of-order
	// fragments, fragmented or oversized control frame, ...).
	ErrProtocol = errors.New("netsec: websocket protocol error")
	// ErrRSV1 indicates the per-message-compression bit was set on a frame
	// that is not allowed to carry it.
	ErrRSV1 = errors.New("netsec: RSV1 set on a non-compressible frame")
	// ErrBudget indicates the shared receive budget was exhausted. The
	// partially assembled message is discarded.
	ErrBudget = errors.New("netsec: receive budget exhausted")
	// ErrIllegalTrailer indicates a compressed payload still carries the
	// 0x0000ffff trailer that the sender was required to strip.
	ErrIllegalTrailer = errors.New("netsec: compressed payload carries an illegal 0x0000ffff trailer")
	// ErrDecompression wraps any failure reported by the injected Decompressor.
	ErrDecompression = errors.New("netsec: decompression failed")
)

// Frame is one parsed WebSocket frame as handed over by the message host.
// Payload is owned by the caller; the receiver copies anything it retains.
type Frame struct {
	Fin     bool
	RSV1    bool
	Opcode  byte
	Payload []byte
}

// Message is a fully reassembled data message (text or binary).
type Message struct {
	Opcode  byte
	Payload []byte
}

// EventKind identifies what HandleFrame produced.
type EventKind int

const (
	// EventNone means the frame was absorbed as a fragment of a message
	// that is not complete yet.
	EventNone EventKind = iota
	// EventMessage carries a reassembled data message.
	EventMessage
	// EventControl carries an interleaved control frame (close/ping/pong).
	EventControl
)

// Event is the result of feeding one frame to the receiver.
type Event struct {
	Kind    EventKind
	Message *Message
	Frame   *Frame
}

func isControl(opcode byte) bool {
	return opcode >= 0x8
}
