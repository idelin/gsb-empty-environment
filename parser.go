package chain

import (
	"bytes"
	"errors"
	"io"
)

// Checkpoint anchors a stream at the last accepted frame. A suffix appended
// after a checkpoint must start with Seq+1 and link to LastDigest.
//
// The zero value is the genesis checkpoint: seq 0 and the zero digest.
type Checkpoint struct {
	Seq        uint64
	LastDigest []byte
}

// GenesisCheckpoint is the start of a fresh chain.
func GenesisCheckpoint() Checkpoint {
	return Checkpoint{Seq: 0, LastDigest: ZeroDigest()}
}

// Parser is a streaming, stateful frame parser.
//
// Bytes may be fed in arbitrary-sized chunks via Append/Write. Next parses
// one frame at a time; an unterminated last frame is merely "need more
// data", while any gap, reordering, wrong link, duplicate variant, bad
// magic, oversized length or CRC failure is a permanent chain error.
type Parser struct {
	maxPayload int
	nextSeq    uint64 // seq the next NEW frame must carry
	lastDigest []byte // digest the next frame must link against
	buf        []byte // undecoded bytes
	offset     int    // bytes consumed from the logical input stream
	closed     bool   // no more bytes will ever arrive
	fatal      error  // first permanent error, if any
}

// Option configures a Parser.
type Option func(*Parser)

// WithMaxPayload sets the per-frame payload length quota (default MaxPayload).
func WithMaxPayload(n int) Option {
	return func(p *Parser) { p.maxPayload = n }
}

// NewParser creates a parser anchored at the genesis checkpoint.
func NewParser(opts ...Option) *Parser {
	p, err := NewParserAt(GenesisCheckpoint(), opts...)
	if err != nil {
		panic(err) // genesis is always valid
	}
	return p
}

var (
	// ErrBadCheckpoint: checkpoint digest is malformed.
	ErrBadCheckpoint = errors.New("chain: malformed checkpoint")
)

// NewParserAt creates a parser that verifies a suffix against cp. A nil
// digest means genesis; a malformed (non-32-byte) digest is rejected so a
// suffix can never be accepted against a bad checkpoint. A suffix linked to
// a well-formed but wrong digest is rejected later by ErrBrokenLink.
func NewParserAt(cp Checkpoint, opts ...Option) (*Parser, error) {
	d := cp.LastDigest
	if d == nil {
		d = ZeroDigest()
	}
	if len(d) != digestSize {
		return nil, ErrBadCheckpoint
	}
	p := &Parser{
		maxPayload: MaxPayload,
		nextSeq:    cp.Seq,
		lastDigest: append([]byte(nil), d...),
	}
	for _, o := range opts {
		o(p)
	}
	return p, nil
}

// Append buffers arbitrary bytes for later parsing.
func (p *Parser) Append(b []byte) {
	if p.closed {
		panic("chain: append after close")
	}
	p.buf = append(p.buf, b...)
}

// Write implements io.Writer.
func (p *Parser) Write(b []byte) (int, error) {
	p.Append(b)
	return len(b), nil
}

// Close signals that no further bytes will arrive.
func (p *Parser) Close() { p.closed = true }

// Offset returns the number of input bytes consumed (including duplicates).
func (p *Parser) Offset() int { return p.offset }

// Checkpoint returns the parser's current chain position.
func (p *Parser) Checkpoint() Checkpoint {
	d := make([]byte, len(p.lastDigest))
	copy(d, p.lastDigest)
	return Checkpoint{Seq: p.nextSeq, LastDigest: d}
}

// EventKind classifies a parsed event.
type EventKind int

const (
	// EventFrame is a newly accepted, in-chain frame.
	EventFrame EventKind = iota
	// EventDuplicate is an exact retransmission (same wire bytes) of an
	// already accepted frame; it is never counted as a new frame.
	EventDuplicate
)

// Event is one step of parser output.
type Event struct {
	Kind   EventKind
	Frame  Frame
	Offset int // stream offset where the frame started
}

var (
	// ErrSequenceGap: a frame jumped over one or more missing frames.
	ErrSequenceGap = errors.New("chain: missing frames (sequence gap)")
	// ErrReordered: a frame arrived more than one position behind.
	ErrReordered = errors.New("chain: reordered frame")
	// ErrBrokenLink: the frame's prevDigest does not match its predecessor.
	ErrBrokenLink = errors.New("chain: broken frame link")
	// ErrConflictingDuplicate: same sequence number but different content.
	ErrConflictingDuplicate = errors.New("chain: duplicate seq with different content")
	// ErrClosed: the input ended with an unterminated final frame.
	ErrClosed = errors.New("chain: stream ended mid-frame")
)

// ChainError pinpoints where a chain invariant failed.
type ChainError struct {
	Kind   error
	Offset int // stream offset where the offending frame started
	Got    uint64
	Want   uint64
}

func (e *ChainError) Error() string {
	return e.Kind.Error()
}

func (e *ChainError) Unwrap() error { return e.Kind }

// Next returns the next event.
//
// An error wrapping ErrNeedMoreData means more bytes are required; this is
// the expected state for a truncated LAST frame (unfinished, not corrupt).
// After Close, an empty stream yields io.EOF and a truncated tail yields
// ErrClosed. Any other error is permanent: wire errors (bad magic,
// oversize length, CRC) or ChainErrors locating the broken frame.
func (p *Parser) Next() (Event, error) {
	if p.fatal != nil {
		return Event{}, p.fatal
	}
	fail := func(err error) (Event, error) {
		p.fatal = err
		return Event{}, err
	}
	for {
		f, n, err := DecodeFrame(p.buf, p.maxPayload)
		if err != nil {
			if !errors.Is(err, ErrNeedMoreData) {
				return fail(err) // bad magic / oversize / crc
			}
			if !p.closed {
				return Event{}, err
			}
			if len(p.buf) == 0 {
				return Event{}, io.EOF
			}
			return Event{}, ErrClosed
		}

		frameOffset := p.offset
		p.buf = p.buf[n:]
		p.offset += n

		switch {
		case f.Seq == p.nextSeq:
			if !bytes.Equal(f.PrevDigest, p.lastDigest) {
				return fail(&ChainError{Kind: ErrBrokenLink, Offset: frameOffset, Got: f.Seq, Want: p.nextSeq})
			}
			p.lastDigest = f.wireDigest()
			p.nextSeq++
			return Event{Kind: EventFrame, Frame: f, Offset: frameOffset}, nil

		case f.Seq == p.nextSeq-1:
			// An exact repeat of the previous frame: digest equality is a
			// commitment to identical wire bytes. Report, do not accept new.
			if bytes.Equal(f.wireDigest(), p.lastDigest) {
				return Event{Kind: EventDuplicate, Frame: f, Offset: frameOffset}, nil
			}
			return fail(&ChainError{Kind: ErrConflictingDuplicate, Offset: frameOffset, Got: f.Seq, Want: p.nextSeq})

		case f.Seq < p.nextSeq:
			return fail(&ChainError{Kind: ErrReordered, Offset: frameOffset, Got: f.Seq, Want: p.nextSeq})

		default: // f.Seq > p.nextSeq: missing frames in the middle
			return fail(&ChainError{Kind: ErrSequenceGap, Offset: frameOffset, Got: f.Seq, Want: p.nextSeq})
		}
	}
}
