package framemux

import "fmt"

// BadDataError reports corrupt input. It is non-fatal: the parser
// resynchronizes at the next frame boundary and keeps accepting input.
// "Not enough data yet" is never an error; incomplete input simply stays
// buffered inside the parser.
type BadDataError struct {
	Reason string
}

func (e *BadDataError) Error() string { return "framemux: bad data: " + e.Reason }

type parseEvent struct {
	frame *frame
	err   *BadDataError
}

type parserState int

const (
	stateHunt  parserState = iota // discarding until the next flag
	stateFrame                    // accumulating one frame's content
)

// parser is an incremental byte-layer frame decoder. Input may arrive in
// arbitrarily sized chunks: all state (including a pending escape
// introducer split across two feeds) lives in the struct, so feeding can
// stop and resume at any byte boundary without losing anything.
//
// Resynchronization is bounded in both memory and work:
//   - content buffered for one frame never exceeds maxContent bytes;
//   - every input byte is examined exactly once (BytesScanned == bytes
//     fed), because a corrupt frame's bytes are dropped wholesale and
//     never rescanned. A flag byte inside a corrupt frame's payload is
//     therefore never treated as the start of a new frame.
type parser struct {
	maxContent int

	state      parserState
	escPending bool
	content    []byte

	// Statistics, useful for tests and for bounding work.
	BytesScanned   int
	BytesDiscarded int
	MaxBuffered    int
}

func newParser(maxPayload int) *parser {
	return &parser{maxContent: headerLen + maxPayload + crcLen}
}

// feed consumes p and appends outcomes to out. It never blocks and never
// fails; corruption is reported as parseEvent entries carrying a
// *BadDataError.
func (p *parser) feed(b []byte, out []parseEvent) []parseEvent {
	for _, c := range b {
		p.BytesScanned++
		if p.state == stateHunt {
			if c == flagByte {
				p.state = stateFrame
				p.content = p.content[:0]
				p.escPending = false
			} else {
				p.BytesDiscarded++
			}
			continue
		}
		// stateFrame
		switch {
		case p.escPending:
			p.escPending = false
			if c == flagByte {
				// Dangling escape: introducer immediately followed by
				// a delimiter. The frame is corrupt; the flag still
				// terminates it and we stay in-frame for the next one.
				out = append(out, parseEvent{err: &BadDataError{Reason: "dangling escape before delimiter"}})
				p.content = p.content[:0]
				continue
			}
			out = p.appendByte(c^escXor, out)
		case c == escByte:
			p.escPending = true
		case c == flagByte:
			if len(p.content) > 0 {
				f, err := parseContent(p.content)
				if err != nil {
					out = append(out, parseEvent{err: &BadDataError{Reason: err.Error()}})
				} else {
					out = append(out, parseEvent{frame: &f})
				}
			}
			p.content = p.content[:0]
		default:
			out = p.appendByte(c, out)
		}
	}
	return out
}

// appendByte checks the length quota BEFORE growing the buffer. If the
// frame would exceed maxContent, the frame is abandoned (delimiter lost
// or garbage injected) and the parser falls back to hunting for the next
// delimiter; the dropped bytes are never revisited.
func (p *parser) appendByte(c byte, out []parseEvent) []parseEvent {
	if len(p.content) >= p.maxContent {
		out = append(out, parseEvent{err: &BadDataError{
			Reason: fmt.Sprintf("frame exceeds %d bytes, resyncing", p.maxContent)}})
		p.content = p.content[:0]
		p.escPending = false
		p.state = stateHunt
		return out
	}
	p.content = append(p.content, c)
	if len(p.content) > p.MaxBuffered {
		p.MaxBuffered = len(p.content)
	}
	return out
}
