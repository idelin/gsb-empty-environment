package netsec

import (
	"bytes"
	"fmt"
)

// Budget is the shared resource pool for a receive operation. The same pool
// is charged for both retained compressed input and produced output, so a
// peer cannot amplify small fragments into a large message for free.
//
// Charge returns false once the pool is exhausted. Charges must be
// non-negative; a zero charge never fails a non-exhausted pool.
type Budget interface {
	Charge(n int) bool
}

// FixedBudget is a simple byte-counting Budget.
type FixedBudget struct {
	remaining int
}

// NewFixedBudget creates a budget that can absorb at most limit total
// charged bytes. A non-positive limit means nothing can be charged.
func NewFixedBudget(limit int) *FixedBudget {
	return &FixedBudget{remaining: limit}
}

func (b *FixedBudget) Charge(n int) bool {
	if n > b.remaining {
		b.remaining = 0
		return false
	}
	b.remaining -= n
	return true
}

// Remaining reports how many bytes can still be charged.
func (b *FixedBudget) Remaining() int {
	return b.remaining
}

// ReceiverConfig configures Receiver.
type ReceiverConfig struct {
	// Decompressor inflates compressed messages and owns the dictionary.
	// Required for any frame that declares compression; nil disables
	// per-message compression and rejects frames with RSV1 set.
	Decompressor Decompressor
	// ContextTakeover reports the negotiated dictionary-retain rule.
	// true reuses the dictionary across messages; false clears it after
	// every completed or failed message.
	ContextTakeover bool
	// Budget, when non-nil, is shared between retained input and output.
	Budget Budget
}

// Receiver is the per-connection receive component driven by the message
// host. It is not safe for concurrent use; the host serializes frame
// delivery, which matches the ordered nature of a WebSocket connection.
type Receiver struct {
	decompressor Decompressor
	takeover     bool
	budget       Budget

	inProgress bool
	opcode     byte
	compressed bool
	buf        []byte
}

// NewReceiver builds a receiver from the negotiated extension parameters.
func NewReceiver(cfg ReceiverConfig) *Receiver {
	return &Receiver{
		decompressor: cfg.Decompressor,
		takeover:     cfg.ContextTakeover,
		budget:       cfg.Budget,
	}
}

// HandleFrame processes one frame.
//
// Control frames are returned immediately and never touch the compression
// buffer or the dictionary, so control frames interleaved between fragments
// do not perturb the message being assembled. Data frames are accumulated
// until the FIN frame completes the message.
//
// On a message-level error (framing violation, budget exhaustion, bad
// trailer, inflation failure) the whole in-progress message is discarded
// and the fragment state machine returns to its initial state.
func (r *Receiver) HandleFrame(f Frame) (Event, error) {
	if isControl(f.Opcode) {
		return r.handleControl(f)
	}
	return r.handleData(f)
}

func (r *Receiver) handleControl(f Frame) (Event, error) {
	if !f.Fin {
		return Event{}, fmt.Errorf("%w: control frames must not be fragmented", ErrProtocol)
	}
	if f.RSV1 {
		return Event{}, fmt.Errorf("%w: control frame", ErrRSV1)
	}
	switch f.Opcode {
	case OpClose, OpPing, OpPong:
	default:
		return Event{}, fmt.Errorf("%w: unknown control opcode 0x%x", ErrProtocol, f.Opcode)
	}
	if len(f.Payload) > 125 {
		return Event{}, fmt.Errorf("%w: control payload exceeds 125 bytes", ErrProtocol)
	}
	// Deliberately do not touch r.buf or r.decompressor: control frames do
	// not belong to the message stream and must never enter the dictionary.
	return Event{Kind: EventControl, Frame: &f}, nil
}

func (r *Receiver) handleData(f Frame) (Event, error) {
	switch f.Opcode {
	case OpText, OpBinary:
		if r.inProgress {
			return r.discard(fmt.Errorf("%w: new data frame before previous message finished", ErrProtocol))
		}
		r.startMessage(f)
	case OpContinuation:
		if !r.inProgress {
			return r.discard(fmt.Errorf("%w: continuation frame without a started message", ErrProtocol))
		}
		if f.RSV1 {
			// Only the first data frame of a message may declare compression.
			return r.discard(fmt.Errorf("%w: continuation frame", ErrRSV1))
		}
	default:
		return r.discard(fmt.Errorf("%w: unknown data opcode 0x%x", ErrProtocol, f.Opcode))
	}

	if err := r.charge(len(f.Payload)); err != nil {
		return r.discard(err)
	}
	r.buf = append(r.buf, f.Payload...)

	if !f.Fin {
		return Event{Kind: EventNone}, nil
	}
	return r.finalize()
}

func (r *Receiver) startMessage(f Frame) {
	r.inProgress = true
	r.opcode = f.Opcode
	r.compressed = f.RSV1
	r.buf = r.buf[:0]
}

func (r *Receiver) charge(n int) error {
	if n == 0 || r.budget == nil {
		return nil
	}
	if !r.budget.Charge(n) {
		return fmt.Errorf("%w: charged %d bytes", ErrBudget, n)
	}
	return nil
}

func (r *Receiver) finalize() (Event, error) {
	if !r.compressed {
		// The payload bytes were already charged as retained input while
		// fragments arrived, so an uncompressed message is billed once.
		payload := append([]byte(nil), r.buf...)
		msg := &Message{Opcode: r.opcode, Payload: payload}
		r.resetAssembly()
		return Event{Kind: EventMessage, Message: msg}, nil
	}

	if r.decompressor == nil {
		return r.discard(fmt.Errorf("%w: compression negotiated but no decompressor installed", ErrRSV1))
	}
	if hasIllegalTrailer(r.buf) {
		r.resetDictionaryAfterFailure()
		return r.discard(ErrIllegalTrailer)
	}

	withTail := make([]byte, 0, len(r.buf)+len(rfc7692Tail))
	withTail = append(withTail, r.buf...)
	withTail = append(withTail, rfc7692Tail[:]...)

	out, err := r.decompressor.Decompress(withTail)
	if err != nil {
		r.resetDictionaryAfterFailure()
		return r.discard(fmt.Errorf("%w: %v", ErrDecompression, err))
	}
	if err := r.charge(len(out)); err != nil {
		// The inflation already happened; only non-takeover mode rewinds
		// the dictionary, mirroring what the peer's per-message reset does.
		r.resetDictionaryIfNoTakeover()
		return r.discard(err)
	}

	r.resetDictionaryIfNoTakeover()
	msg := &Message{Opcode: r.opcode, Payload: out}
	r.resetAssembly()
	return Event{Kind: EventMessage, Message: msg}, nil
}

func hasIllegalTrailer(p []byte) bool {
	return bytes.HasSuffix(p, rfc7692Tail[:])
}

func (r *Receiver) resetDictionaryAfterFailure() {
	if r.decompressor != nil {
		r.decompressor.Reset()
	}
}

func (r *Receiver) resetDictionaryIfNoTakeover() {
	if !r.takeover && r.decompressor != nil {
		r.decompressor.Reset()
	}
}

func (r *Receiver) resetAssembly() {
	r.inProgress = false
	r.compressed = false
	r.opcode = 0
	r.buf = r.buf[:0]
}

// discard aborts the in-progress message and returns the given error.
func (r *Receiver) discard(err error) (Event, error) {
	r.resetAssembly()
	return Event{}, err
}

// Close releases the underlying decompressor.
func (r *Receiver) Close() error {
	if r.decompressor != nil {
		return r.decompressor.Close()
	}
	return nil
}
