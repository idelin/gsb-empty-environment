// Package termfilter filters dangerous terminal escape sequences from
// untrusted remote-program output before it reaches a browser terminal.
package termfilter

import (
	"io"
	"unicode/utf8"
)

// defaultMaxSequenceBytes bounds the length of any single escape sequence the
// parser retains before giving up on it.
const defaultMaxSequenceBytes = 1024

// Option configures a Writer.
type Option func(*Writer)

// MaxSequenceBytes overrides the maximum number of bytes a single
// unterminated escape sequence may occupy before it is truncated. A value
// smaller than 1 restores the built-in default.
func MaxSequenceBytes(n int) Option {
	return func(w *Writer) {
		if n > 0 {
			w.maxSeq = n
		}
	}
}

// Writer is an io.Writer that strips dangerous terminal escape sequences from
// Write inputs and forwards the surviving bytes to an underlying writer.
//
// A Writer is safe only for serial use, matching the io.Writer contract.
type Writer struct {
	dst    io.Writer
	maxSeq int

	// state is the current parser state.
	state state

	// seq buffers an in-progress ESC or CSI sequence whose disposition is not
	// yet known. OSC, DCS, APC and PM sequences are consumed without buffering
	// because they are always dropped.
	seq []byte

	// csiDrop marks a CSI sequence that exceeded maxSeq: its bytes are still
	// consumed up to the final byte, but none are retained or emitted.
	csiDrop bool

	// seqLen counts bytes consumed by the open OSC/DCS/APC/PM string,
	// including its introducer. String payload is never retained; the counter
	// only marks when a string has grown past maxSeq so it stays suppressed.
	seqLen int

	// stringOverlong marks a string sequence that passed maxSeq; its bytes
	// keep being swallowed until the terminator and are never emitted.
	stringOverlong bool

	// stringKind remembers which string was open when an ESC of a possible
	// ESC-backslash terminator was seen.
	stringKind state

	// pending holds trailing bytes of the last Write that started a UTF-8
	// rune but did not complete it. It is used only in stateText.
	pending []byte

	// err records the first failure of the underlying writer.
	err error
}

type state byte

const (
	stateText state = iota
	stateESC
	stateCSI
	stateOSC
	stateDCS
	stateAPC
	statePM
	// stateStringESC is the one-byte window after ESC inside an OSC, DCS,
	// APC or PM sequence, used to recognize an ESC-backslash (ST) terminator.
	stateStringESC
)

// NewWriter returns a filter forwarding safe output to dst.
func NewWriter(dst io.Writer, opts ...Option) *Writer {
	w := &Writer{dst: dst, maxSeq: defaultMaxSequenceBytes}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// writeFunc adapts a function into an io.Writer.
type writeFunc func([]byte) (int, error)

func (f writeFunc) Write(p []byte) (int, error) { return f(p) }

// Filter returns the result of filtering p in one shot. It is equivalent to
// feeding all of p to a fresh Writer and calling Flush, and is the reference
// behavior that incremental filtering must match.
func Filter(p []byte, opts ...Option) []byte {
	var out []byte
	w := NewWriter(writeFunc(func(b []byte) (int, error) {
		out = append(out, b...)
		return len(b), nil
	}), opts...)
	_, _ = w.Write(p)
	_ = w.Flush()
	return out
}

// Write consumes p, emitting filtered output to the underlying writer as soon
// as each byte or sequence is resolved. It always reports len(p) consumed;
// the only possible error comes from the underlying writer.
func (w *Writer) Write(p []byte) (int, error) {
	if w.err != nil {
		return len(p), w.err
	}

	// Rejoin a UTF-8 rune left incomplete by the previous Write before
	// resuming text-state parsing.
	if w.state == stateText && len(w.pending) > 0 {
		joined := make([]byte, 0, len(w.pending)+len(p))
		joined = append(joined, w.pending...)
		joined = append(joined, p...)
		w.pending = nil
		p = joined
	}

	for i := 0; i < len(p); {
		switch w.state {
		case stateText:
			i = w.handleText(p, i)
		case stateESC:
			i = w.handleESC(p, i)
		case stateCSI:
			i = w.handleCSI(p, i)
		case stateOSC, stateDCS, stateAPC, statePM:
			i = w.handleString(p, i)
		case stateStringESC:
			i = w.handleStringESC(p, i)
		default:
			i++
		}
		if w.err != nil {
			return len(p), w.err
		}
	}
	return len(p), nil
}

// Flush must be called once after the final Write. Any incomplete UTF-8 rune
// is emitted verbatim and any unterminated escape sequence still buffered is
// discarded, so no half sequence can reach the terminal.
func (w *Writer) Flush() error {
	if w.err != nil {
		return w.err
	}
	if w.state == stateText && len(w.pending) > 0 {
		w.emit(w.pending)
		w.pending = nil
	}
	w.resetSequence()
	return w.err
}

// Reset returns the filter to its initial state while keeping the configured
// underlying writer and options. Buffered output is discarded.
func (w *Writer) Reset() {
	w.state = stateText
	w.seq = nil
	w.pending = nil
	w.csiDrop = false
	w.seqLen = 0
	w.stringOverlong = false
	w.stringKind = stateText
	w.err = nil
}

// leadWidth returns the total UTF-8 encoding size implied by a lead byte, or
// 1 when the byte cannot legally start an encoding, so the invalid byte is
// forwarded on its own and reprocessed as ordinary data.
func leadWidth(b byte) int {
	switch {
	case b < 0xC2:
		return 1 // continuation bytes or overlong 2-byte leads
	case b <= 0xDF:
		return 2
	case b <= 0xEF:
		return 3
	case b <= 0xF4:
		return 4
	default:
		return 1
	}
}

// handleText processes p starting at index i in the ordinary text state and
// returns the next unconsumed index.
func (w *Writer) handleText(p []byte, i int) int {
	b := p[i]

	switch {
	case b == 0x1B: // ESC
		w.state = stateESC
		w.seq = append(w.seq[:0], b)
		return i + 1

	case b < 0x20 || b == 0x7F:
		// C0 controls and DEL are harmless and forwarded verbatim.
		return w.emitByte(p, i)

	case b >= 0x80 && b <= 0x9F:
		// C1 control block: introducers start sequences, the rest are
		// stripped.
		w.handleC1(b)
		return i + 1

	case b >= 0xC0:
		// Lead byte of a multi-byte UTF-8 rune.
		want := leadWidth(b)
		if want == 1 {
			// 0xC0/0xC1 are overlong leads and 0xF5-0xFF are out of range:
			// neither can start a legal rune, forward verbatim.
			return w.emitByte(p, i)
		}
		if i+want > len(p) {
			// Not enough continuation bytes are available yet. Buffer the
			// whole partial rune without emitting anything, so a multi-byte
			// rune can never reach the terminal split across writes. When the
			// next Write arrives it is rejoined and decoded as a unit.
			w.pending = append(w.pending, p[i:]...)
			return len(p)
		}
		r, size := utf8.DecodeRune(p[i : i+want])
		if r == utf8.RuneError && size == 1 {
			// The expected continuations are present but invalid: forward the
			// lead verbatim; following bytes are reprocessed on their own.
			return w.emitByte(p, i)
		}
		return w.emitBytes(p, i, i+size)

	case b >= 0x80 && b <= 0xBF:
		// Either a lone continuation (invalid UTF-8) or a C1 control. C1
		// controls are never valid continuation bytes of a real rune because a
		// lead only starts a rune in text state once the C1 window is past.
		if b <= 0x9F {
			// Standalone C1 control outside an escape: drop.
			return i + 1
		}
		// Continuation byte without a lead: invalid. Forward verbatim so the
		// downstream terminal renders its own replacement rune.
		return w.emitByte(p, i)

	default:
		return w.emitByte(p, i)
	}
}

// handleC1 reacts to a byte from the C1 control block (0x80-0x9F). The five
// sequence introducers transition out of the text state; all other C1
// controls are dropped.
func (w *Writer) handleC1(b byte) {
	switch b {
	case 0x90: // DCS
		w.enterString(stateDCS, b)
	case 0x9B: // CSI
		w.state = stateCSI
		w.csiDrop = false
		w.seq = append(w.seq[:0], b)
	case 0x9D: // OSC
		w.enterString(stateOSC, b)
	case 0x9E: // PM
		w.enterString(statePM, b)
	case 0x9F: // APC
		w.enterString(stateAPC, b)
	default:
		// Standalone C1 control (IND, NEL, RI, SS2, SS3, SPA, ...): drop.
	}
}

// handleESC processes the byte following a lone ESC.
func (w *Writer) handleESC(p []byte, i int) int {
	b := p[i]
	w.seq = append(w.seq, b)

	switch {
	case b == 0x1B:
		// Another ESC restarts the sequence; retain only the new ESC.
		w.seq = w.seq[:1]
		return i + 1
	case b == 0x18 || b == 0x1A: // CAN or SUB aborts the sequence.
		w.resetSequence()
		return i + 1
	case b == 0x5B: // '[' CSI
		w.state = stateCSI
		w.csiDrop = false
		return i + 1
	case b == 0x5D: // ']' OSC
		w.enterString(stateOSC, w.seq...)
		return i + 1
	case b == 0x50: // 'P' DCS
		w.enterString(stateDCS, w.seq...)
		return i + 1
	case b == 0x5F: // '_' APC
		w.enterString(stateAPC, w.seq...)
		return i + 1
	case b == 0x5E: // '^' PM
		w.enterString(statePM, w.seq...)
		return i + 1
	case b >= 0x80 && b <= 0x9F:
		// A C1 control replaces the ESC-prefixed sequence entirely.
		w.resetSequence()
		w.handleC1(b)
		return i + 1
	case b < 0x20 || b == 0x7F:
		// Other control bytes do not terminate an ESC sequence; keep waiting
		// for its final byte.
		return i + 1
	case b >= 0x20 && b <= 0x2F:
		// Intermediate byte (e.g. the '(' in ESC ( B): keep buffering until
		// the final byte, and honor the sequence length limit.
		if len(w.seq) >= w.maxSeq {
			w.resetSequence()
			return i + 1
		}
		return i + 1
	default:
		// Final byte of an ESC sequence. Simple ESC sequences are not on the
		// allowlist, so drop the whole sequence.
		w.resetSequence()
		return i + 1
	}
}

// csiAllowed reports whether a complete CSI sequence is on the allowlist.
// Only SGR color sequences and basic cursor-movement sequences without a
// private/experimental parameter marker are forwarded.
func csiAllowed(seq []byte) bool {
	if len(seq) < 2 {
		return false
	}

	// Strip the introducer: either ESC '[' or the single 0x9B byte.
	body := seq
	if body[0] == 0x1B {
		body = body[2:]
	} else {
		body = body[1:]
	}
	if len(body) == 0 {
		return false
	}
	final := body[len(body)-1]

	// Private/experimental sequences such as CSI ? ... h (DEC private modes)
	// are never forwarded.
	if body[0] == '?' || body[0] == '>' || body[0] == '=' || body[0] == '<' {
		return false
	}

	switch final {
	case 'm': // SGR: colors and text attributes.
		return true
	case 'A', 'B', 'C', 'D', // cursor up/down/forward/back
		'E', 'F', // cursor next/previous line
		'G', 'H', 'd', // column / position / line
		'f',      // horizontal and vertical position
		'J', 'K', // erase display / line
		'`': // horizontal position absolute
		return true
	default:
		return false
	}
}

// enterString opens an OSC, DCS, APC or PM state. The prefix bytes are the
// introducer (ESC plus type byte, or the 8-bit C1 introducer alone).
func (w *Writer) enterString(kind state, prefix ...byte) {
	w.state = kind
	w.seqLen = len(prefix)
	w.stringOverlong = false
}

// handleString consumes bytes of an OSC, DCS, APC or PM sequence, which are
// always suppressed.
func (w *Writer) handleString(p []byte, i int) int {
	b := p[i]
	w.seqLen++

	switch {
	case b == 0x1B:
		// Possibly the start of an ESC-backslash string terminator.
		w.stringKind = w.state
		w.state = stateStringESC
		return i + 1
	case b == 0x07: // BEL terminates OSC (and is consumed for any string).
		w.finishString()
		return i + 1
	case b == 0x9C: // C1 ST terminates the string.
		w.finishString()
		return i + 1
	case b >= 0x80 && b <= 0x9F:
		// Other C1 controls are swallowed as string content.
		w.noteStringByte()
		return i + 1
	default:
		// Payload is never buffered or emitted; it is swallowed until the
		// terminator, and noteStringByte enforces the truncation threshold.
		w.noteStringByte()
		return i + 1
	}
}

// noteStringByte accounts for a string payload byte and flips the overlong
// marker once the sequence occupies at least maxSeq bytes.
func (w *Writer) noteStringByte() {
	if !w.stringOverlong && w.seqLen >= w.maxSeq {
		w.stringOverlong = true
	}
}

// handleStringESC resolves the byte after an ESC seen inside an OSC, DCS, APC
// or PM sequence.
func (w *Writer) handleStringESC(p []byte, i int) int {
	b := p[i]
	w.seqLen++

	if b == 0x5C { // backslash: ESC-backslash is ST, the string terminates.
		w.finishString()
		return i + 1
	}
	// Not a terminator: resume swallowing the string. The ESC was ordinary
	// content; another escape may still follow.
	w.state = w.stringKind
	if b == 0x1B {
		w.stringKind = w.state
		w.state = stateStringESC
	} else if b == 0x07 || b == 0x9C {
		w.finishString()
	}
	return i + 1
}

// finishString leaves any string state and returns to ordinary text. The
// overlong flag only affects what was suppressed; all strings are dropped.
func (w *Writer) finishString() {
	w.state = stateText
	w.seqLen = 0
	w.stringOverlong = false
	w.stringKind = stateText
}

// resetSequence abandons any in-progress sequence and returns to text state.
// Buffered bytes are discarded without being emitted.
func (w *Writer) resetSequence() {
	w.state = stateText
	w.seq = w.seq[:0]
	w.csiDrop = false
	w.seqLen = 0
	w.stringOverlong = false
	w.stringKind = stateText
}

// emit forwards already-accepted bytes to the underlying writer, retaining
// the first downstream error.
func (w *Writer) emit(b []byte) {
	if w.err != nil || len(b) == 0 {
		return
	}
	n, err := w.dst.Write(b)
	if err != nil {
		w.err = err
		return
	}
	if n != len(b) {
		w.err = io.ErrShortWrite
	}
}

// emitByte forwards p[i] and advances the index by one.
func (w *Writer) emitByte(p []byte, i int) int {
	w.emit(p[i : i+1])
	return i + 1
}

// emitBytes forwards p[lo:hi] and advances the index to hi.
func (w *Writer) emitBytes(p []byte, lo, hi int) int {
	w.emit(p[lo:hi])
	return hi
}

// handleCSI parses parameter, intermediate and final bytes of a CSI sequence.
func (w *Writer) handleCSI(p []byte, i int) int {
	b := p[i]

	switch {
	case b == 0x1B: // ESC restarts into a fresh sequence.
		w.seq = append(w.seq[:0], b)
		w.csiDrop = false
		w.state = stateESC
		return i + 1
	case b == 0x18 || b == 0x1A: // CAN or SUB cancels the sequence.
		w.resetSequence()
		return i + 1
	case b >= 0x80 && b <= 0x9F:
		// A C1 control terminates the CSI sequence without emitting it. The
		// C1 introducers (CSI/OSC/DCS/APC/PM) start a fresh sequence directly;
		// the remaining standalone C1 controls are dropped.
		w.state = stateText
		w.seq = w.seq[:0]
		w.csiDrop = false
		w.handleC1(b)
		return i + 1
	case b < 0x20 || b == 0x7F:
		// Embedded C0 controls do not terminate a CSI sequence.
		return i + 1
	case b >= 0x40 && b <= 0x7E:
		// Final byte: the sequence is complete.
		allowed := !w.csiDrop && len(w.seq) < w.maxSeq
		if allowed {
			w.seq = append(w.seq, b)
			allowed = csiAllowed(w.seq)
		}
		seq := w.seq
		w.resetSequence()
		if allowed {
			w.emit(seq)
		}
		return i + 1
	default:
		// Parameter (0x30-0x3F), private marker or intermediate (0x20-0x2F)
		// byte: retain until the final byte, unless the sequence is already
		// marked for truncation.
		if !w.csiDrop {
			w.seq = append(w.seq, b)
			if len(w.seq) >= w.maxSeq {
				// Truncation: stop retaining bytes so no fragment can leak,
				// keep consuming until the final byte.
				w.csiDrop = true
			}
		}
		return i + 1
	}
}
