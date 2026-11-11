package netsec

import (
	"errors"
	"sync"
)

// MaxPacketNumber is the largest full packet number. The number space is
// 62 bits wide, matching the bound the truncation scheme is designed for.
const MaxPacketNumber = uint64(1<<62) - 1

// Truncated packet number widths supported by the wire format. The width is
// measured in bytes, exactly as the transport host negotiates it.
const (
	Width1 byte = 1
	Width2 byte = 2
	Width3 byte = 3
	Width4 byte = 4
)

// WirePhaseBit is the position of the key phase bit in the first header byte.
// The remaining seven bits carry the leading bits of the truncated number.
const WirePhaseBit byte = 0x80

// Sentinel errors returned by Receive. All state transitions are skipped when
// one of these is returned.
var (
	// ErrShortDatagram is returned when the datagram cannot hold a header of
	// the negotiated truncation width.
	ErrShortDatagram = errors.New("netsec: datagram shorter than negotiated header")
	// ErrBadWidth is returned when the configured truncation width is invalid.
	ErrBadWidth = errors.New("netsec: truncation width must be 1, 2, 3 or 4 bytes")
	// ErrBadReplayWindow is returned when the replay window option is zero.
	ErrBadReplayWindow = errors.New("netsec: replay window size must be positive")
	// ErrUnknownPhase is returned when the datagram announces a phase that is
	// neither the current generation nor the retained previous generation.
	ErrUnknownPhase = errors.New("netsec: packet references unknown key phase")
	// ErrBeforeSwitch is returned for a previous-generation packet whose number
	// does not fall in the single truncation window before the switch boundary.
	// It covers both pre-boundary violations and two-wrap mixing attempts.
	ErrBeforeSwitch = errors.New("netsec: previous-generation packet outside switch boundary window")
	// ErrReplay is returned for duplicate packet numbers, including duplicates
	// arriving concurrently while the first copy is still being authenticated.
	ErrReplay = errors.New("netsec: replayed packet number")
	// ErrNumberSpaceExhausted is returned when decoding exceeds MaxPacketNumber.
	ErrNumberSpaceExhausted = errors.New("netsec: packet number space exhausted")
)

// Authenticator verifies a datagram's keyed integrity tag. The transport host
// supplies the implementation; keys never leave the host. Authenticate must be
// safe for concurrent use.
type Authenticator interface {
	// Authenticate validates payload under the key identified by generation.
	// A nil error means the tag is valid. The receiver only advances its key
	// phase and replay window after Authenticate returns nil.
	Authenticate(generation uint64, payload []byte) error
}

// AuthenticatorFunc adapts a plain function to the Authenticator interface.
type AuthenticatorFunc func(generation uint64, payload []byte) error

// Authenticate calls the wrapped function.
func (f AuthenticatorFunc) Authenticate(generation uint64, payload []byte) error {
	return f(generation, payload)
}

// Option configures a Receiver.
type Option func(*Receiver) error

// WithReplayWindow sets the number of distinct packet numbers covered by the
// sliding replay window. The default is 256.
func WithReplayWindow(size int) Option {
	return func(r *Receiver) error {
		if size <= 0 {
			return ErrBadReplayWindow
		}
		r.replaySize = size
		return nil
	}
}

// Receiver reassembles truncated packet numbers, gates them by key generation
// and feeds them through the host Authenticator. A Receiver is safe for
// concurrent use by multiple transport goroutines.
type Receiver struct {
	mu sync.Mutex

	auth  Authenticator
	width byte

	// generation is the currently accepted key generation (zero based). The
	// wire phase bit is generation mod 2.
	generation uint64
	curKey     Key
	pendingKey Key
	prevKey    Key

	// started reports whether any packet has been authenticated yet.
	started bool
	// highest is the largest authenticated full packet number.
	highest uint64
	// switchBoundary is the full number of the first packet authenticated
	// under the current generation.
	switchBoundary uint64

	replaySize int
	// bitmap holds acceptance bits for the window ending at highest. Bit zero
	// corresponds to highest, bit one to highest-1, and so on.
	bitmap []uint64
}

// New constructs a Receiver. width is the negotiated truncation width in bytes
// and initialKey authenticates generation zero.
func New(width byte, initialKey Key, auth Authenticator, opts ...Option) (*Receiver, error) {
	if width < Width1 || width > Width4 {
		return nil, ErrBadWidth
	}
	if auth == nil {
		return nil, errors.New("netsec: authenticator must not be nil")
	}
	r := &Receiver{
		auth:       auth,
		width:      width,
		curKey:     cloneKey(initialKey),
		replaySize: 256,
	}
	for _, opt := range opts {
		if err := opt(r); err != nil {
			return nil, err
		}
	}
	r.bitmap = make([]uint64, (r.replaySize+63)/64)
	return r, nil
}

// RotateKey provisions the key for the next generation. It does not change the
// active generation itself: the phase advances only when the first packet
// carrying the new phase bit authenticates successfully. Rotating again before
// the switch completes replaces the pending key.
func (r *Receiver) RotateKey(next Key) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pendingKey = cloneKey(next)
}

// RetirePrevious drops the retained previous-generation key. Afterwards only
// the current generation is accepted until the next successful switch.
func (r *Receiver) RetirePrevious() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prevKey = nil
}

// Generation reports the currently accepted key generation.
func (r *Receiver) Generation() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.generation
}

// HighestPacketNumber reports the largest authenticated full packet number.
// Before the first accepted packet it returns false.
func (r *Receiver) HighestPacketNumber() (uint64, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.highest, r.started
}

// Packet describes an accepted datagram.
type Packet struct {
	// Number is the reassembled full packet number.
	Number uint64
	// Generation is the key generation under which the packet authenticated.
	Generation uint64
	// Phase is the key phase bit carried on the wire.
	Phase byte
	// Payload is the authenticated payload (the datagram without its header).
	Payload []byte
}

// Receive processes one datagram. It parses the key phase bit and truncated
// packet number, reassembles the full number against the highest authenticated
// number plus one, enforces generation and replay boundaries, and only then
// invokes the authenticator. No state changes occur unless authentication
// succeeds.
func (r *Receiver) Receive(datagram []byte) (Packet, error) {
	headerLen := int(r.width) + 1
	if len(datagram) < headerLen {
		return Packet{}, ErrShortDatagram
	}

	phase, truncated := ParseHeader(datagram[:headerLen])
	payload := datagram[headerLen:]
	// Normalize the wire bit (0 or WirePhaseBit) to the generation parity (0/1).
	phase >>= 7

	r.mu.Lock()
	defer r.mu.Unlock()

	// Resolve the generation the phase bit refers to. Only the current
	// generation and the immediately retained previous one are reachable.
	generation, key, isPrevious, err := r.resolveGeneration(phase)
	if err != nil {
		return Packet{}, err
	}

	// Reassemble the full number against highest+1, breaking distance ties
	// toward the smaller candidate.
	expected := r.expectedNumber()
	number, err := decodePacketNumber(truncated, r.width, expected)
	if err != nil {
		return Packet{}, err
	}

	// Previous-generation keys may only authenticate numbers inside the single
	// truncation window ending at the switch boundary. This rejects packets
	// from before the boundary and blocks two-wrap mixing under the same
	// phase bit.
	if isPrevious {
		if err := r.checkPreviousWindow(number); err != nil {
			return Packet{}, err
		}
	}

	// Replay check runs before the authenticator so duplicate or concurrent
	// duplicate packets never consume verification resources.
	if r.started && r.isReplay(number) {
		return Packet{}, ErrReplay
	}

	if err := r.auth.Authenticate(generation, payload); err != nil {
		// A failed tag must not move the phase, window or highest number.
		return Packet{}, err
	}

	r.commit(generation, number, key)
	return Packet{
		Number:     number,
		Generation: generation,
		Phase:      phase,
		Payload:    payload,
	}, nil
}

// resolveGeneration maps a wire phase bit to a generation and its key.
func (r *Receiver) resolveGeneration(phase byte) (generation uint64, key Key, isPrevious bool, err error) {
	// phase is normalized to 0 or 1 by Receive.
	if phase == byte(r.generation&1) {
		return r.generation, r.curKey, false, nil
	}

	// The opposite phase bit means either a forward switch (when the next key
	// has been provisioned) or, only after at least one completed switch, the
	// immediately retained previous generation.
	if r.pendingKey != nil {
		return r.generation + 1, r.pendingKey, false, nil
	}
	if r.generation > 0 && r.prevKey != nil {
		return r.generation - 1, r.prevKey, true, nil
	}
	return 0, nil, false, ErrUnknownPhase
}

// expectedNumber returns highest+1, the reference point for decoding.
func (r *Receiver) expectedNumber() uint64 {
	if !r.started {
		return 0
	}
	return r.highest + 1
}

// checkPreviousWindow enforces that a previous-generation number lies in the
// truncation window immediately before the switch boundary.
func (r *Receiver) checkPreviousWindow(number uint64) error {
	window := uint64(1) << (r.width * 8)
	boundary := r.switchBoundary
	// The previous generation ends strictly before the boundary, and no more
	// than one truncation window may separate it from the boundary.
	if number >= boundary || boundary-number > window {
		return ErrBeforeSwitch
	}
	return nil
}

// isReplay reports whether number has already been authenticated. The caller
// must hold r.mu.
func (r *Receiver) isReplay(number uint64) bool {
	if number > r.highest {
		return false
	}
	diff := r.highest - number
	if diff >= uint64(r.replaySize) {
		// Outside the replay window: treated as an ancient replay rather than
		// a new packet.
		return true
	}
	return r.bitmap[diff/64]&(uint64(1)<<(diff%64)) != 0
}

// commit applies the state changes of an authenticated packet. The caller must
// hold r.mu.
func (r *Receiver) commit(generation, number uint64, key Key) {
	if !r.started {
		r.started = true
		r.highest = number
		r.bitmap[0] = 1
		if generation == r.generation+1 {
			r.advanceGeneration(generation, key, number)
		}
		return
	}

	if generation == r.generation+1 {
		// First authenticated packet of a new generation defines the boundary.
		r.advanceGeneration(generation, key, number)
	}

	switch {
	case number > r.highest:
		shift := number - r.highest
		r.slideWindow(shift)
		r.highest = number
		r.bitmap[0] |= 1
	case number == r.highest:
		// Defensive: isReplay already rejects this path.
		r.bitmap[0] |= 1
	default:
		diff := r.highest - number
		r.bitmap[diff/64] |= uint64(1) << (diff % 64)
	}
}

// advanceGeneration moves the current generation forward, retaining the old
// current key as the short-lived previous generation.
func (r *Receiver) advanceGeneration(generation uint64, key Key, boundary uint64) {
	r.prevKey = r.curKey
	r.curKey = key
	r.pendingKey = nil
	r.generation = generation
	r.switchBoundary = boundary
}

// slideWindow shifts the replay bitmap right by shift bits, dropping entries
// that fall out of the window. The caller must hold r.mu.
func (r *Receiver) slideWindow(shift uint64) {
	if shift == 0 {
		return
	}
	words := uint64(len(r.bitmap))
	wordShift := shift / 64
	bitShift := shift % 64
	if wordShift >= words {
		for i := range r.bitmap {
			r.bitmap[i] = 0
		}
		return
	}
	for i := uint64(0); i < words; i++ {
		var next uint64
		src := i + wordShift
		if src < words {
			next = r.bitmap[src] >> bitShift
			if bitShift != 0 && src+1 < words {
				next |= r.bitmap[src+1] << (64 - bitShift)
			}
		}
		r.bitmap[i] = next
	}
}

// decodePacketNumber reassembles a truncated packet number. expected is the
// highest authenticated number plus one (zero when nothing has been
// authenticated yet). Among the legal candidates the one with the smallest
// distance to expected wins; on a distance tie the smaller candidate wins.
func decodePacketNumber(truncated uint64, width byte, expected uint64) (uint64, error) {
	window := uint64(1) << (width * 8)
	mask := window - 1

	// Candidate aligned with expected's window, plus its two neighbours. This
	// spans every legal candidate that can be closest to expected.
	base := expected &^ mask
	best := uint64(0)
	bestSet := false
	for _, candidate := range []uint64{
		subWrapping(base, window) + truncated,
		base + truncated,
		base + window + truncated,
	} {
		if candidate > MaxPacketNumber {
			continue
		}
		if !bestSet {
			best, bestSet = candidate, true
			continue
		}
		best = closerOrSmaller(best, candidate, expected)
	}
	if !bestSet {
		return 0, ErrNumberSpaceExhausted
	}
	return best, nil
}

// subWrapping computes base-window without underflowing at zero.
func subWrapping(base, window uint64) uint64 {
	if base < window {
		return 0
	}
	return base - window
}

// closerOrSmaller picks the candidate with the smaller distance to expected;
// when distances are equal it picks the smaller candidate.
func closerOrSmaller(a, b, expected uint64) uint64 {
	da := distance(a, expected)
	db := distance(b, expected)
	switch {
	case db < da:
		return b
	case da < db:
		return a
	default:
		if b < a {
			return b
		}
		return a
	}
}

// distance returns the absolute gap between two packet numbers. Both values
// are bounded by MaxPacketNumber so the subtraction never overflows.
func distance(a, b uint64) uint64 {
	if a > b {
		return a - b
	}
	return b - a
}

func cloneKey(k Key) Key {
	if k == nil {
		return nil
	}
	out := make(Key, len(k))
	copy(out, k)
	return out
}
