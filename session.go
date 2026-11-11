package netsec

import (
	"errors"
	"fmt"
	"io"
	"sync"
)

// RekeyPhase describes the rotation state of one direction.
type RekeyPhase uint8

const (
	// RekeyStable means the direction uses a single active key generation.
	RekeyStable RekeyPhase = iota

	// RekeyArmed is a send-side state: a new key is provisioned and the
	// key-switch marker has been queued, but not yet fully written.
	RekeyArmed

	// RekeyPending is a receive-side state: a new key is provisioned and the
	// direction waits for the authenticated key-switch marker.
	RekeyPending
)

// defaultLateWindow bounds how many deprecated-generation application records
// the receive direction still accepts after committing to the new key.
const defaultLateWindow = 16

// sendDirection holds the sending-side key generations. The two directions of
// a session never share this state.
type sendDirection struct {
	phase RekeyPhase

	gen    uint64
	active Cipher

	// Next sequence number for records currently being enqueued. It belongs
	// to the generation of the last enqueued record, so a queued marker and
	// the records around it each bind to one determined generation.
	sequence uint64

	newGen uint64
	newKey Cipher
}

// recvDirection holds the receiving-side key generations, including the
// deprecated generation kept for legitimately late in-flight records.
type recvDirection struct {
	phase RekeyPhase

	gen     uint64
	active  Cipher
	nextSeq uint64

	newGen uint64
	newKey Cipher

	// Deprecated previous generation. It can only consume records, never
	// become the active key again.
	oldGen        uint64
	oldKey        Cipher
	oldNextSeq    uint64
	lateRemaining int
}

// outboundRecord is one frame in the send queue. Generation, key and sequence
// are frozen at enqueue time, so partial writes never cause a record to be
// re-sealed under another key version.
type outboundRecord struct {
	typ       RecordType
	gen       uint64
	key       Cipher
	sequence  uint64
	plaintext []byte
	frame     []byte
}

// Session is the bidirectional key-rotation state machine driven by a Go
// session host. Send and receive directions are independent: provisioning or
// committing a receive-side rotation never touches the send key, and vice
// versa.
type Session struct {
	transport io.ReadWriter
	maxLate   int

	sendMu sync.Mutex
	send   sendDirection
	queue  []*outboundRecord

	// The record currently being written and the offset inside its sealed
	// frame. A short write resumes the same bytes; the record is never sealed
	// twice.
	inFlight *outboundRecord
	writeOff int

	recvMu sync.Mutex
	recv   recvDirection

	closed bool
}

// Option configures a Session at construction time.
type Option func(*Session)

// WithMaxLateOldRecords overrides the deprecated-generation late-message
// window on the receive direction.
func WithMaxLateOldRecords(n int) Option {
	return func(s *Session) {
		if n >= 0 {
			s.maxLate = n
		}
	}
}

// NewSession creates a state machine over transport. sendKey and recvKey are
// the generation-zero ciphers for the two independent directions.
func NewSession(transport io.ReadWriter, sendKey, recvKey Cipher, opts ...Option) *Session {
	s := &Session{
		transport: transport,
		maxLate:   defaultLateWindow,
		send: sendDirection{
			phase:  RekeyStable,
			gen:    0,
			active: sendKey,
		},
		recv: recvDirection{
			phase:  RekeyStable,
			gen:    0,
			active: recvKey,
		},
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// InstallSendRekey provisions the next sending key. The active send key is not
// replaced until SwitchSend has queued an authenticated marker and that marker
// has been fully written to the transport.
func (s *Session) InstallSendRekey(generation uint64, key Cipher) error {
	if key == nil {
		return errors.New("netsec: send rekey key is nil")
	}
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.send.phase == RekeyArmed {
		return ErrRekeyPending
	}
	if generation != s.send.gen+1 {
		return fmt.Errorf("netsec: send rekey generation %d does not follow active generation %d",
			generation, s.send.gen)
	}
	s.send.newGen = generation
	s.send.newKey = key
	return nil
}

// InstallReceiveRekey provisions the next receiving key. The active receive
// key is replaced only when the peer's key-switch marker has been
// authenticated under key.
func (s *Session) InstallReceiveRekey(generation uint64, key Cipher) error {
	if key == nil {
		return errors.New("netsec: receive rekey key is nil")
	}
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.recv.phase == RekeyPending {
		return ErrRekeyPending
	}
	if generation != s.recv.gen+1 {
		return fmt.Errorf("netsec: receive rekey generation %d does not follow active generation %d",
			generation, s.recv.gen)
	}
	s.recv.newGen = generation
	s.recv.newKey = key
	s.recv.phase = RekeyPending
	return nil
}

// SwitchSend arms and flushes the send-side key-switch marker. Records already
// queued keep the old key; the marker and every later record use the new key.
// The send direction commits only once the sealed marker has been entirely
// written, and a sealing failure leaves the direction on the old key so the
// host can retry.
func (s *Session) SwitchSend() error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.closed {
		return ErrClosed
	}
	if s.send.phase == RekeyArmed {
		return ErrRekeyPending
	}
	if s.send.newKey == nil {
		return ErrNoNewKey
	}

	// The marker is the first record of the new generation and therefore
	// carries sequence zero.
	marker := &outboundRecord{
		typ:      RecordKeySwitch,
		gen:      s.send.newGen,
		key:      s.send.newKey,
		sequence: 0,
	}
	s.send.sequence = 1
	s.send.phase = RekeyArmed
	s.queue = append(s.queue, marker)

	return s.pumpLocked()
}

// SendMessage enqueues one application message, preserving its boundary and
// its position relative to control markers. The key generation used is the
// one active at enqueue time and is frozen on the record.
func (s *Session) SendMessage(message []byte) error {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	if s.closed {
		return ErrClosed
	}

	record := &outboundRecord{
		typ:       RecordApplication,
		gen:       s.send.gen,
		key:       s.send.active,
		sequence:  s.send.sequence,
		plaintext: append([]byte(nil), message...),
	}
	s.send.sequence++
	s.queue = append(s.queue, record)

	return s.pumpLocked()
}

// pumpLocked writes as many queued frames as the transport accepts. Callers
// hold sendMu. Short writes resume the same sealed frame rather than sealing
// again, which guarantees one record maps to exactly one key version.
func (s *Session) pumpLocked() error {
	for {
		if s.inFlight == nil {
			if len(s.queue) == 0 {
				return nil
			}
			s.inFlight = s.queue[0]
			s.queue = s.queue[1:]
			s.writeOff = 0
			if err := s.sealInFlightLocked(); err != nil {
				return err
			}
		}

		frame := s.inFlight.frame
		n, err := s.transport.Write(frame[s.writeOff:])
		if n > 0 {
			s.writeOff += n
		}
		if s.writeOff == len(frame) {
			finished := s.inFlight
			s.inFlight = nil
			s.writeOff = 0
			if finished.typ == RecordKeySwitch {
				s.commitSendLocked(finished)
			}
		}
		if err != nil {
			s.markClosedLocked()
			return err
		}
		if n == 0 {
			s.markClosedLocked()
			return io.ErrShortWrite
		}
	}
}

// sealInFlightLocked seals the current record once. Its header binds type,
// generation, sequence and length, and is also the authenticated associated
// data passed to the cipher.
func (s *Session) sealInFlightLocked() error {
	record := s.inFlight
	header := headerFor(record)
	if int(header.length) > MaxFrameSize {
		s.inFlight = nil
		s.writeOff = 0
		return ErrMessageTooLarge
	}
	ciphertext, err := record.key.Seal(record.plaintext, header.encodeBytes())
	if err != nil {
		s.inFlight = nil
		s.writeOff = 0
		if record.typ == RecordKeySwitch {
			// Abort before touching the wire: stay on the old key and let
			// the host retry the rotation.
			s.send.phase = RekeyStable
			s.send.sequence = s.activeSendSequenceLocked()
		}
		return err
	}
	if len(ciphertext) != int(header.length) || len(ciphertext) > MaxFrameSize {
		s.inFlight = nil
		s.writeOff = 0
		if record.typ == RecordKeySwitch {
			s.send.phase = RekeyStable
			s.send.sequence = s.activeSendSequenceLocked()
		}
		return ErrFrame
	}

	frame := make([]byte, frameHeaderSize+len(ciphertext))
	header.encode(frame[:frameHeaderSize])
	copy(frame[frameHeaderSize:], ciphertext)
	record.frame = frame
	return nil
}

// activeSendSequenceLocked reconstructs the next sequence number for the
// still-active old generation after a failed marker seal.
func (s *Session) activeSendSequenceLocked() uint64 {
	// All records still queued ahead of the removed marker already carry
	// their frozen sequences; the next enqueued old-generation record takes
	// the first unused value.
	next := uint64(0)
	for _, queued := range s.queue {
		if queued.gen == s.send.gen && queued.sequence >= next {
			next = queued.sequence + 1
		}
	}
	return next
}

// commitSendLocked switches the active send key after the marker has been
// fully written. The old key is dropped immediately: every later record is
// already bound to the new generation.
func (s *Session) commitSendLocked(marker *outboundRecord) {
	s.send.active = marker.key
	s.send.gen = marker.gen
	s.send.phase = RekeyStable
	s.send.newKey = nil
	s.send.newGen = 0
	// s.send.sequence already counts new-generation records from one.
}

// ReceiveMessage returns the next authenticated application message, blocking
// until it arrives. Key-switch markers are consumed internally. Each record is
// opened under exactly one generation: the active key, or the deprecated key
// for an in-window late record.
func (s *Session) ReceiveMessage() ([]byte, error) {
	for {
		record, err := s.readRecord()
		if err != nil {
			return nil, err
		}
		if record.header.typ == RecordKeySwitch {
			if err := s.handleMarker(record); err != nil {
				return nil, err
			}
			continue
		}
		plaintext, err := s.openApplication(record)
		if err != nil {
			return nil, err
		}
		return plaintext, nil
	}
}

type wireRecord struct {
	header     frameHeader
	headerRaw  [frameHeaderSize]byte
	ciphertext []byte
}

// readRecord decodes exactly one length-prefixed frame and preserves the raw
// header because it is the authenticated associated data.
func (s *Session) readRecord() (*wireRecord, error) {
	s.recvMu.Lock()
	closed := s.closed
	s.recvMu.Unlock()
	if closed {
		return nil, ErrClosed
	}

	var raw [frameHeaderSize]byte
	if _, err := io.ReadFull(s.transport, raw[:]); err != nil {
		s.markClosedOnReadError()
		return nil, err
	}
	header, err := decodeHeader(raw[:])
	if err != nil {
		s.markClosedOnReadError()
		return nil, err
	}
	ciphertext := make([]byte, header.length)
	if header.length > 0 {
		if _, err := io.ReadFull(s.transport, ciphertext); err != nil {
			s.markClosedOnReadError()
			return nil, err
		}
	}
	return &wireRecord{header: header, headerRaw: raw, ciphertext: ciphertext}, nil
}

// handleMarker authenticates the key-switch control frame under the
// provisioned new receive key. The direction commits only on success; any
// failure leaves every key generation untouched, so a deprecated key can never
// be resurrected.
func (s *Session) handleMarker(record *wireRecord) error {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	if s.closed {
		return ErrClosed
	}
	header := record.header

	fail := func() error {
		return fmt.Errorf("%w: key-switch marker for generation %d",
			ErrAuthenticationFailed, header.generation)
	}
	if s.recv.phase != RekeyPending ||
		s.recv.newKey == nil ||
		header.generation != s.recv.newGen ||
		header.sequence != 0 {
		return fail()
	}
	if _, err := s.recv.newKey.Open(record.ciphertext, record.headerRaw[:]); err != nil {
		return fail()
	}

	// Commit: the previous active generation becomes the deprecated one and
	// remains available only for late in-flight application records.
	s.recv.oldGen = s.recv.gen
	s.recv.oldKey = s.recv.active
	s.recv.oldNextSeq = s.recv.nextSeq
	s.recv.lateRemaining = s.maxLate

	s.recv.active = s.recv.newKey
	s.recv.gen = s.recv.newGen
	s.recv.nextSeq = 1
	s.recv.phase = RekeyStable
	s.recv.newKey = nil
	s.recv.newGen = 0
	return nil
}

// openApplication authenticates one application record under its single
// determined key generation and enforces strict per-generation sequencing.
func (s *Session) openApplication(record *wireRecord) ([]byte, error) {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	if s.closed {
		return nil, ErrClosed
	}
	header := record.header

	switch {
	case header.generation == s.recv.gen:
		if header.sequence < s.recv.nextSeq {
			return nil, fmt.Errorf("%w: generation %d sequence %d",
				ErrReplay, header.generation, header.sequence)
		}
		if header.sequence > s.recv.nextSeq {
			return nil, fmt.Errorf("%w: expected sequence %d, got %d",
				ErrSequenceGap, s.recv.nextSeq, header.sequence)
		}
		plaintext, err := s.recv.active.Open(record.ciphertext, record.headerRaw[:])
		if err != nil {
			return nil, fmt.Errorf("%w: generation %d", ErrAuthenticationFailed, header.generation)
		}
		s.recv.nextSeq++
		return plaintext, nil

	case s.recv.oldKey != nil && header.generation == s.recv.oldGen:
		// A late record from the deprecated generation. It may never promote
		// the old key back to active; it is only opened and accounted for.
		if s.recv.lateRemaining == 0 {
			return nil, fmt.Errorf("%w: generation %d sequence %d",
				ErrLateWindowExceeded, header.generation, header.sequence)
		}
		if header.sequence < s.recv.oldNextSeq {
			return nil, fmt.Errorf("%w: deprecated generation %d sequence %d",
				ErrReplay, header.generation, header.sequence)
		}
		if header.sequence > s.recv.oldNextSeq {
			return nil, fmt.Errorf("%w: deprecated generation expected %d, got %d",
				ErrSequenceGap, s.recv.oldNextSeq, header.sequence)
		}
		plaintext, err := s.recv.oldKey.Open(record.ciphertext, record.headerRaw[:])
		if err != nil {
			return nil, fmt.Errorf("%w: deprecated generation %d",
				ErrAuthenticationFailed, header.generation)
		}
		s.recv.oldNextSeq++
		s.recv.lateRemaining--
		return plaintext, nil

	default:
		return nil, fmt.Errorf("%w: generation %d (active %d)",
			ErrUnknownGeneration, header.generation, s.recv.gen)
	}
}

// Close marks the session unusable. It does not close the underlying
// transport, which remains owned by the host.
func (s *Session) Close() error {
	s.sendMu.Lock()
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	defer s.sendMu.Unlock()
	s.closed = true
	return nil
}

// SendPhase reports the current phase of the send direction.
func (s *Session) SendPhase() RekeyPhase {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.send.phase
}

// SendGeneration reports the active sending key generation.
func (s *Session) SendGeneration() uint64 {
	s.sendMu.Lock()
	defer s.sendMu.Unlock()
	return s.send.gen
}

// ReceivePhase reports the current phase of the receive direction.
func (s *Session) ReceivePhase() RekeyPhase {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	return s.recv.phase
}

// ReceiveGeneration reports the active receiving key generation.
func (s *Session) ReceiveGeneration() uint64 {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	return s.recv.gen
}

// DeprecatedReceiveGeneration reports the generation kept for late messages,
// or false when no deprecated generation exists.
func (s *Session) DeprecatedReceiveGeneration() (uint64, bool) {
	s.recvMu.Lock()
	defer s.recvMu.Unlock()
	if s.recv.oldKey == nil {
		return 0, false
	}
	return s.recv.oldGen, true
}

// markClosedLocked is used by send-side failures.
func (s *Session) markClosedLocked() {
	s.closed = true
}

// markClosedOnReadError is used by receive-side transport failures.
func (s *Session) markClosedOnReadError() {
	s.recvMu.Lock()
	s.closed = true
	s.recvMu.Unlock()
}
