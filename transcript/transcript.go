// Package transcript builds collision-resistant canonical transcripts for
// authentication protocol messages.
//
// A transcript is an ordered sequence of field frames, each carrying the
// message phase, a unique field tag, a field type, an explicit length and
// the field payload. Because every boundary is encoded in the framing,
// two different field sequences can never share one byte encoding, which
// removes the ambiguity of concatenating bare variable-length fields.
//
// Integers use a unique shortest encoding: unsigned values use minimal
// unsigned LEB128 and signed values use ZigZag LEB128. Unknown optional
// fields are accepted as already-encoded frames via WriteRawFrame and
// remain part of the digest without requiring knowledge of their schema.
//
// A Builder rejects duplicate tags and attempts to move back to an earlier
// phase. Large fields may be streamed in chunks, a build may be cancelled
// at any point, and a signable digest is only available after Close.
package transcript

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
)

// Sentinel errors. Wrapped errors from failed calls can be tested with
// errors.Is.
var (
	// ErrCancelled is returned by every operation on a cancelled builder.
	ErrCancelled = errors.New("transcript: builder cancelled")
	// ErrClosed is returned when a mutating call reaches a closed builder.
	ErrClosed = errors.New("transcript: builder already closed")
	// ErrOpenField is returned when an operation requires no open field
	// while one is still streaming.
	ErrOpenField = errors.New("transcript: field still open")
	// ErrDuplicateTag is returned when a tag is used more than once.
	ErrDuplicateTag = errors.New("transcript: duplicate field tag")
	// ErrPhaseRollback is returned when a call targets an earlier phase.
	ErrPhaseRollback = errors.New("transcript: phase cannot move backwards")
	// ErrNotClosed is returned when Digest is requested before Close.
	ErrNotClosed = errors.New("transcript: digest unavailable until close")
	// ErrFieldTooLarge is returned when a field exceeds MaxFieldLen.
	ErrFieldTooLarge = errors.New("transcript: field too large")
	// ErrBadTag is returned for the reserved zero tag.
	ErrBadTag = errors.New("transcript: field tag must be greater than zero")
)

// Digest is the signable output of a closed transcript.
type Digest struct {
	// Sum is the SHA-256 digest of the canonical transcript.
	Sum [sha256.Size]byte
	// Fields is the number of field frames covered by Sum.
	Fields int
	// Phases is the highest phase reached, including phase zero.
	Phases uint64
	// EncodedLen is the total length of the canonical byte stream.
	EncodedLen int
}

// Builder incrementally constructs one transcript. It is safe for
// concurrent use; the internal lock serializes frame emission.
type Builder struct {
	mu       stateMu
	h        hash.Hash
	phase    uint64
	fields   int
	encoded  int
	tags     map[uint64]struct{}
	open     *FieldWriter
	closed   bool
	canceled bool
}

// stateMu is a channel-based mutex so a zero-value Builder is not
// accidentally usable without New.
type stateMu struct {
	ch chan struct{}
}

func newStateMu() *stateMu {
	m := &stateMu{ch: make(chan struct{}, 1)}
	m.ch <- struct{}{}
	return m
}

func (m *stateMu) lock()   { <-m.ch }
func (m *stateMu) unlock() { m.ch <- struct{}{} }

// New returns a builder starting at phase zero.
func New() *Builder {
	return &Builder{
		mu:   *newStateMu(),
		h:    sha256.New(),
		tags: make(map[uint64]struct{}),
	}
}

// Phase returns the current message phase.
func (b *Builder) Phase() uint64 {
	b.mu.lock()
	defer b.mu.unlock()
	return b.phase
}

// FieldCount returns the number of completed field frames.
func (b *Builder) FieldCount() int {
	b.mu.lock()
	defer b.mu.unlock()
	return b.fields
}

// HasTag reports whether tag has already been used.
func (b *Builder) HasTag(tag uint64) bool {
	b.mu.lock()
	defer b.mu.unlock()
	_, ok := b.tags[tag]
	return ok
}

// ready runs the state checks shared by every mutating call. All
// validation happens before any state change, so rejected calls leave the
// transcript untouched and can be retried by the caller.
func (b *Builder) ready(phase uint64) error {
	if b.canceled {
		return ErrCancelled
	}
	if b.closed {
		return ErrClosed
	}
	if b.open != nil {
		return ErrOpenField
	}
	if phase < b.phase {
		return fmt.Errorf("%w: %d -> %d", ErrPhaseRollback, b.phase, phase)
	}
	return nil
}

// checkTag validates a tag against the reserved range and prior usage.
func (b *Builder) checkTag(tag uint64) error {
	if tag <= endTag {
		return ErrBadTag
	}
	if _, ok := b.tags[tag]; ok {
		return fmt.Errorf("%w: tag %d", ErrDuplicateTag, tag)
	}
	return nil
}

// emit hashes one fully formed frame and records its bookkeeping.
func (b *Builder) emit(f frame, raw []byte) {
	b.h.Write(raw)
	b.encoded += len(raw)
	b.fields++
	b.tags[f.tag] = struct{}{}
}

// BeginPhase advances the transcript to phase. Moving forward is allowed;
// returning to an earlier phase is rejected without changing state.
func (b *Builder) BeginPhase(phase uint64) error {
	b.mu.lock()
	defer b.mu.unlock()
	if err := b.ready(phase); err != nil {
		return err
	}
	b.phase = phase
	return nil
}

// WriteBytes adds one length-prefixed byte field in the current phase.
// An empty payload is a valid, explicitly framed field.
func (b *Builder) WriteBytes(tag uint64, payload []byte) error {
	b.mu.lock()
	defer b.mu.unlock()
	if err := b.ready(b.phase); err != nil {
		return err
	}
	if err := b.checkTag(tag); err != nil {
		return err
	}
	if uint64(len(payload)) > MaxFieldLen {
		return fmt.Errorf("%w: %d bytes", ErrFieldTooLarge, len(payload))
	}
	raw := appendFrame(nil, b.phase, tag, typeBytes, payload)
	b.emit(frame{phase: b.phase, tag: tag, typ: typeBytes, payload: payload}, raw)
	return nil
}

// WriteUint adds an unsigned integer using its unique minimal varint
// encoding. There is exactly one representation for each value, including
// zero, so encoders cannot create distinct frames with equal values.
func (b *Builder) WriteUint(tag, value uint64) error {
	b.mu.lock()
	defer b.mu.unlock()
	if err := b.ready(b.phase); err != nil {
		return err
	}
	if err := b.checkTag(tag); err != nil {
		return err
	}
	payload := appendUvarint(nil, value)
	raw := appendFrame(nil, b.phase, tag, typeUint, payload)
	b.emit(frame{phase: b.phase, tag: tag, typ: typeUint}, raw)
	return nil
}

// WriteInt adds a signed integer using minimal ZigZag varint encoding.
func (b *Builder) WriteInt(tag uint64, value int64) error {
	b.mu.lock()
	defer b.mu.unlock()
	if err := b.ready(b.phase); err != nil {
		return err
	}
	if err := b.checkTag(tag); err != nil {
		return err
	}
	payload := appendUvarint(nil, encodeZigZag(value))
	raw := appendFrame(nil, b.phase, tag, typeInt, payload)
	b.emit(frame{phase: b.phase, tag: tag, typ: typeInt}, raw)
	return nil
}

// BeginField starts a byte field that can be filled with multiple Write
// calls. The declared length is encoded in the header up front and must
// be matched exactly by FieldWriter.Close. The tag is reserved
// immediately, so a concurrent attempt to reuse it fails.
func (b *Builder) BeginField(tag uint64, length int) (*FieldWriter, error) {
	b.mu.lock()
	defer b.mu.unlock()
	if err := b.ready(b.phase); err != nil {
		return nil, err
	}
	if err := b.checkTag(tag); err != nil {
		return nil, err
	}
	if length < 0 || uint64(length) > MaxFieldLen {
		return nil, fmt.Errorf("%w: declared %d bytes", ErrFieldTooLarge, length)
	}

	header := appendFrame(nil, b.phase, tag, typeBytes, make([]byte, length))
	headerLen := len(header) - length

	fw := &FieldWriter{
		builder: b,
		tag:     tag,
		total:   length,
	}
	b.open = fw
	// Reserve the tag for the whole lifetime of the open field so a
	// duplicate attempt is reported as ErrDuplicateTag and HasTag stays
	// truthful while the field is streaming.
	b.tags[tag] = struct{}{}
	// Hash the header now; payload chunks are hashed as they arrive.
	b.h.Write(header[:headerLen])
	b.encoded += headerLen
	return fw, nil
}

// WriteRawFrame incorporates a frame produced by another implementation,
// for example an unknown optional field whose schema this host does not
// understand. The frame is fully validated and parsed, then hashed
// verbatim, so unknown fields still contribute to the digest. Its phase
// must match the current phase and its tag must not already be present.
func (b *Builder) WriteRawFrame(raw []byte) error {
	f, err := parseFrame(raw)
	if err != nil {
		return err
	}

	b.mu.lock()
	defer b.mu.unlock()
	if err := b.ready(f.phase); err != nil {
		return err
	}
	if f.phase != b.phase {
		return fmt.Errorf("%w: frame phase %d, current phase %d",
			ErrPhaseRollback, f.phase, b.phase)
	}
	if err := b.checkTag(f.tag); err != nil {
		return err
	}
	b.emit(f, raw)
	return nil
}

// Close finalizes the transcript by appending a domain-separated end
// frame. After Close, Digest returns the signable summary and mutating
// calls fail with ErrClosed.
func (b *Builder) Close() (Digest, error) {
	b.mu.lock()
	defer b.mu.unlock()
	if b.canceled {
		return Digest{}, ErrCancelled
	}
	if b.closed {
		return Digest{}, ErrClosed
	}
	if b.open != nil {
		return Digest{}, ErrOpenField
	}

	// Tag zero is reserved for the end frame, whose payload records the
	// field count. No application frame can collide with it.
	end := appendFrame(nil, b.phase, endTag, typeUint,
		appendUvarint(nil, uint64(b.fields)))
	b.h.Write(end)
	b.encoded += len(end)
	b.closed = true

	var sum [sha256.Size]byte
	copy(sum[:], b.h.Sum(nil))
	return Digest{
		Sum:        sum,
		Fields:     b.fields,
		Phases:     b.phase,
		EncodedLen: b.encoded,
	}, nil
}

// Digest returns the signable summary. It fails with ErrNotClosed until
// Close has succeeded, guaranteeing that no partial transcript can be
// signed.
func (b *Builder) Digest() (Digest, error) {
	b.mu.lock()
	defer b.mu.unlock()
	if b.canceled {
		return Digest{}, ErrCancelled
	}
	if !b.closed {
		return Digest{}, ErrNotClosed
	}
	var sum [sha256.Size]byte
	copy(sum[:], b.h.Sum(nil))
	return Digest{
		Sum:        sum,
		Fields:     b.fields,
		Phases:     b.phase,
		EncodedLen: b.encoded,
	}, nil
}

// Cancel aborts the transcript at any point, including while a field is
// open. The internal hash state is reset and every later call fails with
// ErrCancelled; no digest can ever be exported from a cancelled builder.
func (b *Builder) Cancel() {
	b.mu.lock()
	defer b.mu.unlock()
	if b.canceled {
		return
	}
	b.canceled = true
	b.closed = false
	b.open = nil
	b.h.Reset()
	b.tags = make(map[uint64]struct{})
}

// FieldWriter streams one length-declared field. It implements io.Writer.
type FieldWriter struct {
	builder *Builder
	tag     uint64
	written int
	total   int
}

// Tag returns the field tag this writer was opened for.
func (fw *FieldWriter) Tag() uint64 { return fw.tag }

// Write appends a chunk of the field payload. A write that would exceed
// the declared length is rejected in full without consuming any bytes, so
// the builder remains usable and the caller can retry or cancel.
func (fw *FieldWriter) Write(p []byte) (int, error) {
	b := fw.builder
	b.mu.lock()
	defer b.mu.unlock()
	if b.canceled {
		return 0, ErrCancelled
	}
	if b.closed {
		return 0, ErrClosed
	}
	if b.open != fw {
		return 0, errors.New("transcript: field is no longer open")
	}
	if len(p) > fw.total-fw.written {
		return 0, fmt.Errorf("%w: chunk %d bytes, %d remaining",
			ErrFieldTooLarge, len(p), fw.total-fw.written)
	}
	b.h.Write(p)
	b.encoded += len(p)
	fw.written += len(p)
	return len(p), nil
}

// Close completes the field. It fails unless exactly the declared number
// of payload bytes has been written.
func (fw *FieldWriter) Close() error {
	b := fw.builder
	b.mu.lock()
	defer b.mu.unlock()
	if b.canceled {
		return ErrCancelled
	}
	if b.closed {
		return ErrClosed
	}
	if b.open != fw {
		return errors.New("transcript: field already closed")
	}
	if fw.written != fw.total {
		return fmt.Errorf("transcript: incomplete field: wrote %d of %d bytes",
			fw.written, fw.total)
	}
	b.open = nil
	b.fields++
	b.tags[fw.tag] = struct{}{}
	return nil
}
