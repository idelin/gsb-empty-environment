package netsec

import (
	"crypto/cipher"
	"errors"
	"fmt"
	"sync"
	"time"
)

const (
	// DefaultMaxKeyUpdates bounds how many authenticated KeyUpdate messages
	// are accepted per sliding window.
	DefaultMaxKeyUpdates = 8

	// DefaultKeyUpdateWindow is the default sliding window length.
	DefaultKeyUpdateWindow = time.Minute
)

var (
	// ErrClosed is returned after the state machine has been closed, either by
	// the caller or because a fatal protocol/budget violation occurred.
	ErrClosed = errors.New("netsec: key update state closed")
	// ErrAuthentication means AEAD verification failed. No read sequence
	// number, read key, or read generation is advanced in this case.
	ErrAuthentication = errors.New("netsec: record authentication failed")
	// ErrMalformedRecord means the outer TLS record framing is invalid.
	ErrMalformedRecord = errors.New("netsec: malformed TLS record")
	// ErrMalformedKeyUpdate means a KeyUpdate message was truncated, had an
	// invalid length/request value, or was otherwise not a complete message.
	ErrMalformedKeyUpdate = errors.New("netsec: malformed KeyUpdate message")
	// ErrKeyUpdateNotIsolated means KeyUpdate shared a record with another
	// handshake message or was coalesced with a second KeyUpdate.
	ErrKeyUpdateNotIsolated = errors.New("netsec: KeyUpdate must occupy exactly one record")
	// ErrUpdateBudgetExceeded means the peer exceeded the sliding-window
	// KeyUpdate budget. The state machine is closed permanently.
	ErrUpdateBudgetExceeded = errors.New("netsec: KeyUpdate budget exceeded")
	// ErrSequenceExhausted means a record sequence number reached 2^64-1
	// without a key update.
	ErrSequenceExhausted = errors.New("netsec: record sequence number exhausted")
	// ErrRecordTooLarge means the caller attempted to seal a record whose
	// plaintext exceeds the TLS 1.3 limit.
	ErrRecordTooLarge = errors.New("netsec: record plaintext too large")
)

// KeyUpdateRequest is the one-byte body of a TLS 1.3 KeyUpdate message.
type KeyUpdateRequest byte

const (
	// KeyUpdateNotRequested tells the peer that no reciprocal update is needed.
	KeyUpdateNotRequested KeyUpdateRequest = 0
	// KeyUpdateRequested asks the peer to send its own KeyUpdate.
	KeyUpdateRequested KeyUpdateRequest = 1
)

// Config controls a KeyUpdateState.
type Config struct {
	// Suite selects the AEAD and hash. The zero value means
	// TLS13AES128GCM.
	Suite CipherSuite

	// ReadSecret is the current peer-to-local traffic secret. WriteSecret is
	// the current local-to-peer traffic secret. Both are Hash.Length bytes.
	ReadSecret  []byte
	WriteSecret []byte

	// MaxUpdates is the maximum number of authenticated inbound KeyUpdate
	// messages accepted in one sliding UpdateWindow. Zero selects
	// DefaultMaxKeyUpdates.
	MaxUpdates int
	// UpdateWindow is the sliding budget window. Zero selects
	// DefaultKeyUpdateWindow.
	UpdateWindow time.Duration
	// Now supplies the clock used by the sliding window. Nil means time.Now.
	Now func() time.Time
	// OnClose, if non-nil, is called exactly once when the state machine
	// becomes permanently unusable. It is invoked without holding the state
	// lock, so it may close the underlying connection.
	OnClose func(error)
}

// RecordResult is the outcome of processing one encrypted TLS record.
type RecordResult struct {
	// ContentType is the decrypted inner content type.
	ContentType byte
	// Plaintext is the decrypted content without the inner content type and
	// without record padding.
	Plaintext []byte
	// Response is a complete encrypted KeyUpdate(update_not_requested) record
	// when the received KeyUpdate requested an update. Otherwise it is nil.
	Response []byte
}

// KeyUpdateState is a record-layer state machine for TLS 1.3 KeyUpdate
// handling. It owns traffic secrets, AEAD keys, record sequence numbers, and
// the sliding-window update budget.
type KeyUpdateState struct {
	mu sync.Mutex

	suite CipherSuite

	readSecret  []byte
	writeSecret []byte
	readAEAD    cipher.AEAD
	writeAEAD   cipher.AEAD
	readIV      []byte
	writeIV     []byte
	readSeq     uint64
	writeSeq    uint64
	readGen     uint64
	writeGen    uint64

	maxUpdates int
	window     time.Duration
	now        func() time.Time
	updates    []time.Time

	closed   bool
	closeErr error
	onClose  func(error)
}

// NewKeyUpdateState validates cfg and derives the initial AEAD keys.
func NewKeyUpdateState(cfg Config) (*KeyUpdateState, error) {
	suite, err := validateSuite(cfg.Suite)
	if err != nil {
		return nil, err
	}
	secretLen := suite.Hash().Size()
	if len(cfg.ReadSecret) != secretLen {
		return nil, fmt.Errorf("netsec: read traffic secret is %d bytes, want %d", len(cfg.ReadSecret), secretLen)
	}
	if len(cfg.WriteSecret) != secretLen {
		return nil, fmt.Errorf("netsec: write traffic secret is %d bytes, want %d", len(cfg.WriteSecret), secretLen)
	}
	if cfg.MaxUpdates == 0 {
		cfg.MaxUpdates = DefaultMaxKeyUpdates
	}
	if cfg.MaxUpdates < 0 {
		return nil, errors.New("netsec: MaxUpdates must be positive or zero")
	}
	if cfg.UpdateWindow == 0 {
		cfg.UpdateWindow = DefaultKeyUpdateWindow
	}
	if cfg.UpdateWindow < 0 {
		return nil, errors.New("netsec: UpdateWindow must be positive or zero")
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	s := &KeyUpdateState{
		suite:       suite,
		readSecret:  append([]byte(nil), cfg.ReadSecret...),
		writeSecret: append([]byte(nil), cfg.WriteSecret...),
		maxUpdates:  cfg.MaxUpdates,
		window:      cfg.UpdateWindow,
		now:         cfg.Now,
		onClose:     cfg.OnClose,
	}
	s.readAEAD, s.readIV, err = s.deriveAEAD(s.readSecret)
	if err != nil {
		return nil, err
	}
	s.writeAEAD, s.writeIV, err = s.deriveAEAD(s.writeSecret)
	if err != nil {
		return nil, err
	}
	return s, nil
}

// New is an alias for NewKeyUpdateState.
func New(cfg Config) (*KeyUpdateState, error) {
	return NewKeyUpdateState(cfg)
}

func (s *KeyUpdateState) deriveAEAD(secret []byte) (cipher.AEAD, []byte, error) {
	key, err := HKDFExpandLabel(s.suite.Hash, secret, "key", nil, s.suite.KeyLen)
	if err != nil {
		return nil, nil, err
	}
	iv, err := HKDFExpandLabel(s.suite.Hash, secret, "iv", nil, s.suite.IVLen)
	if err != nil {
		return nil, nil, err
	}
	aead, err := s.suite.NewAEAD(key)
	if err != nil {
		return nil, nil, err
	}
	if aead.NonceSize() != s.suite.IVLen {
		return nil, nil, fmt.Errorf(
			"netsec: cipher suite %q AEAD nonce size is %d, want %d",
			s.suite.Name, aead.NonceSize(), s.suite.IVLen,
		)
	}
	return aead, iv, nil
}

// ProcessRecord authenticates and decrypts one complete TLS 1.3 record, then
// applies KeyUpdate semantics if the record contains one. The returned
// Response is already sealed under the old write keys and is followed by a
// write-key update with a reset write sequence number.
func (s *KeyUpdateState) ProcessRecord(record []byte) (RecordResult, error) {
	s.mu.Lock()
	if s.closed {
		err := s.closeErr
		s.mu.Unlock()
		return RecordResult{}, err
	}

	contentType, plaintext, err := s.openRecordLocked(record)
	if err != nil {
		s.mu.Unlock()
		return RecordResult{}, err
	}

	result := RecordResult{
		ContentType: contentType,
		Plaintext:   plaintext,
	}
	var fatal error
	var notify bool

	if contentType == ContentTypeHandshake {
		request, isKeyUpdate, parseErr := findKeyUpdate(plaintext)
		switch {
		case parseErr != nil:
			fatal = parseErr
		case isKeyUpdate:
			if !s.allowUpdateLocked(s.now()) {
				fatal = ErrUpdateBudgetExceeded
			} else {
				if err := s.rotateReadLocked(); err != nil {
					fatal = err
				} else if request == KeyUpdateRequested {
					result.Response, err = s.sealRecordLocked(
						ContentTypeHandshake,
						marshalKeyUpdate(KeyUpdateNotRequested),
					)
					if err != nil {
						fatal = err
					} else if err := s.rotateWriteLocked(); err != nil {
						fatal = err
					}
				}
			}
		}
	}

	if fatal != nil {
		notify = s.closeLocked(fatal)
	}
	onClose := s.onClose
	closeErr := s.closeErr
	s.mu.Unlock()

	if notify && onClose != nil {
		onClose(closeErr)
	}
	if fatal != nil {
		return RecordResult{}, fatal
	}
	return result, nil
}

// HandleRecord is a convenience wrapper around ProcessRecord that returns
// only the KeyUpdate response record.
func (s *KeyUpdateState) HandleRecord(record []byte) ([]byte, error) {
	result, err := s.ProcessRecord(record)
	if err != nil {
		return nil, err
	}
	return result.Response, nil
}

// SendKeyUpdate seals a KeyUpdate record under the current write keys and
// then advances the write keys and resets the write sequence number to zero.
func (s *KeyUpdateState) SendKeyUpdate(request KeyUpdateRequest) ([]byte, error) {
	if request != KeyUpdateNotRequested && request != KeyUpdateRequested {
		return nil, fmt.Errorf("netsec: invalid KeyUpdate request value %d", request)
	}
	s.mu.Lock()
	var notify bool
	defer func() {
		onClose := s.onClose
		closeErr := s.closeErr
		s.mu.Unlock()
		if notify && onClose != nil {
			onClose(closeErr)
		}
	}()
	if s.closed {
		return nil, s.closeErr
	}
	record, err := s.sealRecordLocked(ContentTypeHandshake, marshalKeyUpdate(request))
	if err != nil {
		notify = s.closeLocked(err)
		return nil, err
	}
	if err := s.rotateWriteLocked(); err != nil {
		notify = s.closeLocked(err)
		return nil, err
	}
	return record, nil
}

// SealRecord protects one record with the current write keys and increments
// the write sequence number. It is useful for tests and for callers that need
// to send application data or non-KeyUpdate handshake messages through the
// same key epoch. Use SendKeyUpdate instead of sealing a KeyUpdate message
// with this method so that the write key epoch is advanced correctly.
func (s *KeyUpdateState) SealRecord(contentType byte, plaintext []byte) ([]byte, error) {
	s.mu.Lock()
	var notify bool
	defer func() {
		onClose := s.onClose
		closeErr := s.closeErr
		s.mu.Unlock()
		if notify && onClose != nil {
			onClose(closeErr)
		}
	}()
	if s.closed {
		return nil, s.closeErr
	}
	record, err := s.sealRecordLocked(contentType, plaintext)
	if err != nil {
		notify = s.closeLocked(err)
		return nil, err
	}
	return record, nil
}

func (s *KeyUpdateState) allowUpdateLocked(now time.Time) bool {
	cutoff := now.Add(-s.window)
	kept := s.updates[:0]
	for _, at := range s.updates {
		if !at.Before(cutoff) {
			kept = append(kept, at)
		}
	}
	s.updates = kept
	if len(s.updates) >= s.maxUpdates {
		return false
	}
	s.updates = append(s.updates, now)
	return true
}

func (s *KeyUpdateState) rotateReadLocked() error {
	next, err := NextTrafficSecret(s.suite.Hash, s.readSecret)
	if err != nil {
		return err
	}
	aead, iv, err := s.deriveAEAD(next)
	if err != nil {
		return err
	}
	s.readSecret = next
	s.readAEAD = aead
	s.readIV = iv
	s.readSeq = 0
	s.readGen++
	return nil
}

func (s *KeyUpdateState) rotateWriteLocked() error {
	next, err := NextTrafficSecret(s.suite.Hash, s.writeSecret)
	if err != nil {
		return err
	}
	aead, iv, err := s.deriveAEAD(next)
	if err != nil {
		return err
	}
	s.writeSecret = next
	s.writeAEAD = aead
	s.writeIV = iv
	s.writeSeq = 0
	s.writeGen++
	return nil
}

func (s *KeyUpdateState) closeLocked(err error) bool {
	if s.closed {
		return false
	}
	s.closed = true
	s.closeErr = err
	return true
}

// Close permanently closes the state machine. It is idempotent.
func (s *KeyUpdateState) Close() error {
	s.mu.Lock()
	notify := s.closeLocked(ErrClosed)
	onClose := s.onClose
	closeErr := s.closeErr
	s.mu.Unlock()
	if notify && onClose != nil {
		onClose(closeErr)
	}
	return nil
}

// Closed reports whether the state machine is permanently closed.
func (s *KeyUpdateState) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// Err returns the error that closed the state machine, or nil if it is open.
func (s *KeyUpdateState) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		return nil
	}
	return s.closeErr
}

// ReadGeneration returns the number of successful read-key updates.
func (s *KeyUpdateState) ReadGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readGen
}

// WriteGeneration returns the number of successful write-key updates.
func (s *KeyUpdateState) WriteGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeGen
}

// ReadSequence returns the next expected read record sequence number.
func (s *KeyUpdateState) ReadSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readSeq
}

// WriteSequence returns the next write record sequence number.
func (s *KeyUpdateState) WriteSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeSeq
}

// ReadSecret returns a copy of the current peer-to-local traffic secret.
func (s *KeyUpdateState) ReadSecret() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.readSecret...)
}

// WriteSecret returns a copy of the current local-to-peer traffic secret.
func (s *KeyUpdateState) WriteSecret() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.writeSecret...)
}

// MarshalKeyUpdate returns the complete five-byte KeyUpdate handshake
// message.
func MarshalKeyUpdate(request KeyUpdateRequest) ([]byte, error) {
	if request != KeyUpdateNotRequested && request != KeyUpdateRequested {
		return nil, fmt.Errorf("netsec: invalid KeyUpdate request value %d", request)
	}
	return []byte{KeyUpdateMessageType, 0, 0, 1, byte(request)}, nil
}

func marshalKeyUpdate(request KeyUpdateRequest) []byte {
	message, _ := MarshalKeyUpdate(request)
	return message
}

func findKeyUpdate(handshake []byte) (KeyUpdateRequest, bool, error) {
	if len(handshake) == 0 {
		return 0, false, nil
	}

	// Scan the messages in this record. A KeyUpdate must be the only
	// handshake message and must fit entirely in this record.
	messages := 0
	var request KeyUpdateRequest
	found := false
	for offset := 0; offset < len(handshake); {
		if len(handshake)-offset < 4 {
			if handshake[offset] == KeyUpdateMessageType {
				return 0, false, ErrMalformedKeyUpdate
			}
			// Some other handshake message is fragmented; leave it to the
			// caller's handshake reassembly layer.
			return 0, false, nil
		}
		msgType := handshake[offset]
		msgLen := int(handshake[offset+1])<<16 | int(handshake[offset+2])<<8 | int(handshake[offset+3])
		if msgLen > len(handshake)-offset-4 {
			if msgType == KeyUpdateMessageType {
				return 0, false, ErrMalformedKeyUpdate
			}
			return 0, false, nil
		}
		messages++
		if msgType == KeyUpdateMessageType {
			if msgLen != 1 {
				return 0, false, ErrMalformedKeyUpdate
			}
			if found || messages != 1 || offset+4+msgLen != len(handshake) {
				return 0, false, ErrKeyUpdateNotIsolated
			}
			value := handshake[offset+4]
			if value != byte(KeyUpdateNotRequested) && value != byte(KeyUpdateRequested) {
				return 0, false, ErrMalformedKeyUpdate
			}
			request = KeyUpdateRequest(value)
			found = true
		}
		offset += 4 + msgLen
	}
	return request, found, nil
}

// ParseKeyUpdate parses exactly one complete KeyUpdate handshake message.
func ParseKeyUpdate(message []byte) (KeyUpdateRequest, error) {
	if len(message) != 5 {
		return 0, ErrMalformedKeyUpdate
	}
	if message[0] != KeyUpdateMessageType {
		return 0, ErrMalformedKeyUpdate
	}
	if message[1] != 0 || message[2] != 0 || message[3] != 1 {
		return 0, ErrMalformedKeyUpdate
	}
	request := KeyUpdateRequest(message[4])
	if request != KeyUpdateNotRequested && request != KeyUpdateRequested {
		return 0, ErrMalformedKeyUpdate
	}
	return request, nil
}
