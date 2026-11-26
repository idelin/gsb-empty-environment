package tls13

import (
	"crypto/cipher"
	"errors"
	"fmt"
)

// TLS record content types.
const (
	ContentTypeChangeCipherSpec byte = 20
	ContentTypeAlert            byte = 21
	ContentTypeHandshake        byte = 22
	ContentTypeApplicationData  byte = 23

	// VersionTLS12 is the legacy record version used in TLS 1.3 record
	// headers/AAD.
	VersionTLS12 uint16 = 0x0303

	// MaxCiphertextRecordSize is RFC 8446's 2^14 + 256 ceiling.
	MaxCiphertextRecordSize = 16384 + 256
)

var (
	ErrSequenceExhausted = errors.New("tls13: record sequence number exhausted")
	ErrRecordTooLarge    = errors.New("tls13: TLS record is too large")
	ErrInvalidRecord     = errors.New("tls13: invalid TLS record")
	ErrInvalidPadding    = errors.New("tls13: invalid TLS 1.3 inner padding")
	ErrDecryption        = errors.New("tls13: record decryption or authentication failed")
)

// Record is a decoded TLS record.
type Record struct {
	ContentType byte
	Version     uint16
	Payload     []byte
}

// RecordState protects one direction of a TLS 1.3 connection. A TLS endpoint
// owns separate RecordState values for sending and receiving.
type RecordState struct {
	suite         CipherSuite
	trafficSecret []byte
	keys          TrafficKeys
	aead          cipher.AEAD
	sequence      uint64
	keyEpoch      uint64
}

// NewRecordState derives key/IV from trafficSecret and starts sequence at 0.
func NewRecordState(suite CipherSuite, trafficSecret []byte) (*RecordState, error) {
	keys, err := DeriveTrafficKeys(suite, trafficSecret)
	if err != nil {
		return nil, err
	}
	aead, err := suite.newGCM(keys.Key)
	if err != nil {
		return nil, err
	}
	return &RecordState{
		suite:         suite,
		trafficSecret: cloneBytes(trafficSecret),
		keys:          keys,
		aead:          aead,
	}, nil
}

func mustNewRecordState(suite CipherSuite, secret []byte) *RecordState {
	state, err := NewRecordState(suite, secret)
	if err != nil {
		panic(err)
	}
	return state
}

func (s *RecordState) Suite() CipherSuite    { return s.suite }
func (s *RecordState) Sequence() uint64      { return s.sequence }
func (s *RecordState) KeyEpoch() uint64      { return s.keyEpoch }
func (s *RecordState) TrafficSecret() []byte { return cloneBytes(s.trafficSecret) }
func (s *RecordState) Keys() TrafficKeys {
	return TrafficKeys{Key: cloneBytes(s.keys.Key), IV: cloneBytes(s.keys.IV)}
}

// SetSequence overrides the 64-bit record sequence number. It is useful for
// interoperability and exhaustive boundary tests; ordinary callers should not
// use it.
func (s *RecordState) SetSequence(sequence uint64) { s.sequence = sequence }

// Nonce returns IV XOR zero_pad(sequence) at the requested sequence number.
func (s *RecordState) Nonce(sequence uint64) ([]byte, error) {
	if len(s.keys.IV) < 8 {
		return nil, fmt.Errorf("%w: IV is too short", ErrInvalidRecord)
	}
	nonce := cloneBytes(s.keys.IV)
	for i := 0; i < 8; i++ {
		nonce[len(nonce)-1-i] ^= byte(sequence >> (8 * i))
	}
	return nonce, nil
}

// CurrentNonce returns the nonce for the next protected record.
func (s *RecordState) CurrentNonce() ([]byte, error) { return s.Nonce(s.sequence) }

// Rekey derives "traffic upd", replaces key/IV, and independently resets this
// direction's sequence number to zero.
func (s *RecordState) Rekey() error {
	nextSecret, err := NextTrafficSecret(s.suite, s.trafficSecret)
	if err != nil {
		return err
	}
	nextKeys, err := DeriveTrafficKeys(s.suite, nextSecret)
	if err != nil {
		return err
	}
	aead, err := s.suite.newGCM(nextKeys.Key)
	if err != nil {
		return err
	}
	s.trafficSecret = nextSecret
	s.keys = nextKeys
	s.aead = aead
	s.sequence = 0
	s.keyEpoch++
	return nil
}

// Rekeyed returns a rekeyed copy without mutating s.
func (s *RecordState) Rekeyed() (*RecordState, error) {
	next := *s
	if err := next.Rekey(); err != nil {
		return nil, err
	}
	return &next, nil
}

// SealOptions controls TLSInnerPlaintext padding and outer record values.
type SealOptions struct {
	PaddingLength int
	OuterType     byte
	Version       uint16
}

// Seal encrypts one TLS inner plaintext and returns the complete outer record.
func (s *RecordState) Seal(contentType byte, plaintext []byte, options *SealOptions) ([]byte, error) {
	if s.sequence == ^uint64(0) {
		return nil, ErrSequenceExhausted
	}

	paddingLength := 0
	outerType := ContentTypeApplicationData
	version := VersionTLS12
	if options != nil {
		paddingLength = options.PaddingLength
		if options.OuterType != 0 {
			outerType = options.OuterType
		}
		if options.Version != 0 {
			version = options.Version
		}
	}
	if paddingLength < 0 {
		return nil, fmt.Errorf("%w: negative padding length", ErrInvalidPadding)
	}

	inner := make([]byte, 0, len(plaintext)+1+paddingLength)
	inner = append(inner, plaintext...)
	inner = append(inner, contentType)
	inner = append(inner, make([]byte, paddingLength)...)

	payloadLen := len(inner) + s.suite.TagLen()
	recordLen := 5 + payloadLen
	if payloadLen > MaxCiphertextRecordSize {
		return nil, ErrRecordTooLarge
	}

	record := make([]byte, recordLen)
	record[0] = outerType
	record[1] = byte(version >> 8)
	record[2] = byte(version)
	record[3] = byte(payloadLen >> 8)
	record[4] = byte(payloadLen)

	nonce, err := s.Nonce(s.sequence)
	if err != nil {
		return nil, err
	}
	sealed := s.aead.Seal(record[5:5], nonce, inner, record[:5])
	if len(sealed) != payloadLen {
		return nil, errors.New("tls13: unexpected AEAD output length")
	}

	s.sequence++
	return record, nil
}

// Open authenticates and decrypts one complete outer TLS record. It validates
// and removes TLS 1.3 inner content type and zero padding.
func (s *RecordState) Open(record []byte) (contentType byte, plaintext []byte, err error) {
	rec, err := parseOuterRecord(record)
	if err != nil {
		return 0, nil, err
	}
	if len(rec.Payload) < s.suite.TagLen()+1 {
		return 0, nil, fmt.Errorf("%w: short AEAD record", ErrInvalidRecord)
	}

	nonce, err := s.Nonce(s.sequence)
	if err != nil {
		return 0, nil, err
	}
	inner, err := s.aead.Open(nil, nonce, rec.Payload, record[:5])
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrDecryption, err)
	}

	plaintext, contentType, err = RemoveInnerPadding(inner)
	if err != nil {
		return 0, nil, err
	}

	s.sequence++
	return contentType, plaintext, nil
}

// OpenPayload opens an explicit outer header and AEAD payload. The header is
// used as additional data exactly as supplied.
func (s *RecordState) OpenPayload(header [5]byte, payload []byte) (contentType byte, plaintext []byte, err error) {
	if len(payload) < s.suite.TagLen()+1 {
		return 0, nil, fmt.Errorf("%w: short AEAD record", ErrInvalidRecord)
	}
	nonce, err := s.Nonce(s.sequence)
	if err != nil {
		return 0, nil, err
	}
	inner, err := s.aead.Open(nil, nonce, payload, header[:])
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %v", ErrDecryption, err)
	}
	plaintext, contentType, err = RemoveInnerPadding(inner)
	if err != nil {
		return 0, nil, err
	}
	s.sequence++
	return contentType, plaintext, nil
}

func parseOuterRecord(record []byte) (Record, error) {
	if len(record) < 5 {
		return Record{}, fmt.Errorf("%w: short record header", ErrInvalidRecord)
	}
	length := int(record[3])<<8 | int(record[4])
	if length != len(record)-5 {
		return Record{}, fmt.Errorf("%w: declared length %d, payload %d", ErrInvalidRecord, length, len(record)-5)
	}
	if length > MaxCiphertextRecordSize {
		return Record{}, ErrRecordTooLarge
	}
	return Record{
		ContentType: record[0],
		Version:     uint16(record[1])<<8 | uint16(record[2]),
		Payload:     record[5:],
	}, nil
}

// ParseRecordHeader validates a complete record's length and returns its
// header fields and payload slice.
func ParseRecordHeader(record []byte) (Record, error) {
	return parseOuterRecord(record)
}

// RemoveInnerPadding removes arbitrary-length trailing zero padding and then
// returns the preceding non-zero byte as the TLS 1.3 inner content type.
func RemoveInnerPadding(inner []byte) (plaintext []byte, contentType byte, err error) {
	end := len(inner)
	for end > 0 && inner[end-1] == 0 {
		end--
	}
	if end == 0 {
		return nil, 0, fmt.Errorf("%w: no content type", ErrInvalidPadding)
	}
	contentType = inner[end-1]
	if !KnownContentType(contentType) {
		return nil, 0, fmt.Errorf("%w: unknown content type %d", ErrInvalidPadding, contentType)
	}
	return inner[:end-1], contentType, nil
}

// KnownContentType reports whether b is a recognized TLS record type.
func KnownContentType(b byte) bool {
	switch b {
	case ContentTypeChangeCipherSpec, ContentTypeAlert, ContentTypeHandshake, ContentTypeApplicationData:
		return true
	default:
		return false
	}
}

// Direction identifies one half of a bidirectional connection.
type Direction uint8

const (
	Outbound Direction = iota
	Inbound
)

// RecordProtector owns independent inbound and outbound record states.
type RecordProtector struct {
	role     Role
	outbound *RecordState
	inbound  *RecordState
}

// Role determines which traffic secret is used for sending.
type Role uint8

const (
	RoleClient Role = iota
	RoleServer
)

// NewRecordProtector creates client/server directional states from the two
// handshake or application traffic secrets.
func NewRecordProtector(suite CipherSuite, clientTrafficSecret, serverTrafficSecret []byte, role Role) (*RecordProtector, error) {
	clientState, err := NewRecordState(suite, clientTrafficSecret)
	if err != nil {
		return nil, err
	}
	serverState, err := NewRecordState(suite, serverTrafficSecret)
	if err != nil {
		return nil, err
	}
	p := &RecordProtector{role: role}
	if role == RoleClient {
		p.outbound = clientState
		p.inbound = serverState
	} else {
		p.outbound = serverState
		p.inbound = clientState
	}
	return p, nil
}

func (p *RecordProtector) Role() Role { return p.role }

// Outbound returns the sending record state.
func (p *RecordProtector) Outbound() *RecordState { return p.outbound }

// Inbound returns the receiving record state.
func (p *RecordProtector) Inbound() *RecordState { return p.inbound }

// DirectionState returns the requested local direction.
func (p *RecordProtector) DirectionState(direction Direction) *RecordState {
	if direction == Outbound {
		return p.outbound
	}
	return p.inbound
}

// Seal encrypts with the outbound state.
func (p *RecordProtector) Seal(contentType byte, plaintext []byte, options *SealOptions) ([]byte, error) {
	return p.outbound.Seal(contentType, plaintext, options)
}

// Open decrypts with the inbound state.
func (p *RecordProtector) Open(record []byte) (byte, []byte, error) {
	return p.inbound.Open(record)
}

// UpdateKey rekeys exactly one direction and resets only that direction's
// sequence number.
func (p *RecordProtector) UpdateKey(direction Direction) error {
	return p.DirectionState(direction).Rekey()
}

// KeyUpdateRequest is the request_update byte in a KeyUpdate message.
type KeyUpdateRequest byte

const (
	KeyUpdateNotRequested KeyUpdateRequest = 0
	KeyUpdateRequested    KeyUpdateRequest = 1
)

// EncodeKeyUpdate returns a complete TLS handshake KeyUpdate message,
// including its 4-byte handshake header.
func EncodeKeyUpdate(request KeyUpdateRequest) ([]byte, error) {
	if request != KeyUpdateNotRequested && request != KeyUpdateRequested {
		return nil, fmt.Errorf("%w: request_update %d", ErrInvalidRecord, request)
	}
	return []byte{0x18, 0x00, 0x00, 0x01, byte(request)}, nil
}

// ParseKeyUpdate validates a complete handshake message or its body and
// returns request_update.
func ParseKeyUpdate(message []byte) (KeyUpdateRequest, error) {
	body := message
	if len(message) == 5 && message[0] == 0x18 && message[1] == 0 && message[2] == 0 && message[3] == 1 {
		body = message[4:]
	}
	if len(body) != 1 || (body[0] != 0 && body[0] != 1) {
		return 0, fmt.Errorf("%w: malformed KeyUpdate", ErrInvalidRecord)
	}
	return KeyUpdateRequest(body[0]), nil
}

// HandleInboundKeyUpdate rekeys the inbound direction after a KeyUpdate
// handshake message has been decrypted and verified under the old inbound
// keys. If the peer requested an update response, response is a KeyUpdate
// message to protect with the current outbound keys; the caller must send it
// and then call CommitOutboundKeyUpdate.
func (p *RecordProtector) HandleInboundKeyUpdate(message []byte) (response []byte, respond bool, err error) {
	request, err := ParseKeyUpdate(message)
	if err != nil {
		return nil, false, err
	}
	if err := p.inbound.Rekey(); err != nil {
		return nil, false, err
	}
	if request == KeyUpdateRequested {
		response, err = EncodeKeyUpdate(KeyUpdateNotRequested)
		if err != nil {
			return nil, false, err
		}
		return response, true, nil
	}
	return nil, false, nil
}

// CommitOutboundKeyUpdate rekeys outbound after the KeyUpdate handshake
// message has been sealed under the previous outbound keys.
func (p *RecordProtector) CommitOutboundKeyUpdate() error {
	return p.outbound.Rekey()
}

// InitiateKeyUpdate builds a requested or non-requested KeyUpdate message.
// The caller seals and sends it with current outbound keys, then calls
// CommitOutboundKeyUpdate.
func (p *RecordProtector) InitiateKeyUpdate(request KeyUpdateRequest) ([]byte, error) {
	return EncodeKeyUpdate(request)
}
