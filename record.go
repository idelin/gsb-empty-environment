package netsec

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
)

// ContentTypeTLS12CID is the DTLS 1.2 tls12_cid content type defined by RFC 9146.
const ContentTypeTLS12CID uint8 = 25

// TypeTLS12CID is an alias for ContentTypeTLS12CID.
const TypeTLS12CID = ContentTypeTLS12CID

const (
	cidRecordHeaderLen = 1 + 2 + 2 + 6 + 2
	maxRecordLen       = 1 << 14
)

var (
	ErrNotCID          = errors.New("netsec: record is not tls12_cid")
	ErrMalformedRecord = errors.New("netsec: malformed tls12_cid record")
	ErrCIDLength       = errors.New("netsec: CID length does not match negotiated value")
	ErrSessionNotFound = errors.New("netsec: no session for CID")
	ErrAuthentication  = errors.New("netsec: record authentication failed")
	ErrNoAuthenticator = errors.New("netsec: no record authenticator configured")
	ErrAmplification   = errors.New("netsec: path amplification limit exceeded")
	ErrNoTransport     = errors.New("netsec: no transport configured")
	ErrWrongPath       = errors.New("netsec: address is not the pending path")
	ErrBadPathResponse = errors.New("netsec: invalid path validation response")

	// Aliases for callers that use alternative names.
	ErrReplay             = errors.New("netsec: replayed record")
	ErrUnknownSession     = ErrSessionNotFound
	ErrCIDMismatch        = ErrCIDLength
	ErrAuth               = ErrAuthentication
	ErrAmplificationLimit = ErrAmplification
)

// Record is a parsed DTLS 1.2 tls12_cid record.
//
// The RFC 9146 wire format does not carry the CID length: it is negotiated
// during the handshake. CIDLength therefore records the length used to parse
// (or the length declared by an in-memory Record), not an extra wire byte.
type Record struct {
	Type       uint8
	Version    uint16
	Epoch      uint16
	Sequence   uint64
	CIDLength  uint8
	CID        []byte
	Ciphertext []byte
	Length     uint16

	// ContentType, ConnectionID, SequenceNumber, and Encrypted are accepted
	// aliases for the corresponding canonical fields above.
	ContentType    uint8
	ConnectionID   []byte
	SequenceNumber uint64
	Encrypted      []byte

	// RawLength is the number of wire bytes occupied by this record. It is
	// used to account for bytes received from a candidate path.
	RawLength int
	Source    net.Addr
}

// ParseCIDRecord parses exactly one RFC 9146 tls12_cid record. cidLen is the
// CID length negotiated for the session.
func ParseCIDRecord(raw []byte, cidLen int) (*Record, error) {
	if len(raw) < 1 || raw[0] != ContentTypeTLS12CID {
		return nil, ErrNotCID
	}
	if cidLen < 0 || cidLen > 255 {
		return nil, fmt.Errorf("%w: invalid CID length %d", ErrMalformedRecord, cidLen)
	}

	headerLen := cidRecordHeaderLen + cidLen
	if len(raw) < headerLen {
		return nil, ErrMalformedRecord
	}

	rec := &Record{
		Type:      raw[0],
		Version:   binary.BigEndian.Uint16(raw[1:3]),
		Epoch:     binary.BigEndian.Uint16(raw[3:5]),
		Sequence:  readUint48(raw[5:11]),
		CIDLength: uint8(cidLen),
		Length:    binary.BigEndian.Uint16(raw[11+cidLen : headerLen]),
		RawLength: len(raw),
	}
	if rec.Type != ContentTypeTLS12CID {
		return nil, ErrNotCID
	}
	if int(rec.Length) > maxRecordLen || headerLen+int(rec.Length) != len(raw) {
		return nil, ErrMalformedRecord
	}

	rec.CID = append([]byte(nil), raw[11:11+cidLen]...)
	rec.Ciphertext = append([]byte(nil), raw[headerLen:headerLen+int(rec.Length)]...)
	rec.normalize()
	return rec, nil
}

// ParseRecord is an alias for ParseCIDRecord.
func ParseRecord(raw []byte, cidLen int) (*Record, error) {
	return ParseCIDRecord(raw, cidLen)
}

// RecordNumber returns the 64-bit DTLS record number formed by the epoch and
// the 48-bit sequence number.
func (rec *Record) RecordNumber() uint64 {
	rec.normalize()
	return uint64(rec.Epoch)<<48 | (rec.Sequence & ((1 << 48) - 1))
}

// WireSize returns the size of the RFC 9146 record represented by rec.
func (rec *Record) WireSize() int {
	rec.normalize()
	if rec.RawLength > 0 {
		return rec.RawLength
	}
	return cidRecordHeaderLen + len(rec.CID) + len(rec.Ciphertext)
}

// Marshal serializes rec using the RFC 9146 record layout.
func (rec *Record) Marshal() ([]byte, error) {
	rec.normalize()
	if rec.Type != ContentTypeTLS12CID {
		return nil, ErrNotCID
	}
	cidLen := rec.declaredCIDLength()
	if cidLen != len(rec.CID) || cidLen > 255 {
		return nil, ErrCIDLength
	}
	if len(rec.Ciphertext) > maxRecordLen {
		return nil, ErrMalformedRecord
	}

	out := make([]byte, cidRecordHeaderLen+len(rec.CID)+len(rec.Ciphertext))
	rec.Length = uint16(len(rec.Ciphertext))
	out[0] = ContentTypeTLS12CID
	binary.BigEndian.PutUint16(out[1:3], rec.Version)
	binary.BigEndian.PutUint16(out[3:5], rec.Epoch)
	writeUint48(out[5:11], rec.Sequence)
	copy(out[11:11+len(rec.CID)], rec.CID)
	binary.BigEndian.PutUint16(out[11+len(rec.CID):], uint16(len(rec.Ciphertext)))
	copy(out[cidRecordHeaderLen+len(rec.CID):], rec.Ciphertext)
	return out, nil
}

// AdditionalData returns the RFC 9146 AEAD additional data for a tls12_cid
// record. innerPlaintextLen is the length of the serialized
// DTLSInnerPlaintext (that is, ciphertext length minus the AEAD tag length).
func AdditionalData(rec *Record, innerPlaintextLen int) []byte {
	cidLen := rec.declaredCIDLength()
	if cidLen != len(rec.CID) {
		cidLen = len(rec.CID)
	}

	out := make([]byte, 0, 23+cidLen)
	out = append(out, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	out = append(out, ContentTypeTLS12CID)
	out = append(out, uint8(cidLen))
	out = append(out, ContentTypeTLS12CID)

	out = append(out, byte(rec.Version>>8), byte(rec.Version))
	out = append(out, byte(rec.Epoch>>8), byte(rec.Epoch))
	var sequence [6]byte
	writeUint48(sequence[:], rec.Sequence)
	out = append(out, sequence[:]...)
	out = append(out, rec.CID...)
	out = append(out, byte(innerPlaintextLen>>8), byte(innerPlaintextLen))
	return out
}

func (rec *Record) declaredCIDLength() int {
	if rec.CIDLength != 0 {
		return int(rec.CIDLength)
	}
	return len(rec.CID)
}

func (rec *Record) normalize() {
	if rec.Type == 0 && rec.ContentType != 0 {
		rec.Type = rec.ContentType
	}
	if rec.CID == nil && rec.ConnectionID != nil {
		rec.CID = rec.ConnectionID
	}
	if rec.Ciphertext == nil && rec.Encrypted != nil {
		rec.Ciphertext = rec.Encrypted
	}
	if rec.Sequence == 0 && rec.SequenceNumber != 0 {
		rec.Sequence = rec.SequenceNumber
	}
}

func (s *Session) openRecord(rec *Record) ([]byte, error) {
	if s.AEAD == nil {
		return nil, ErrNoAuthenticator
	}
	nonce := s.recordNonce(rec)
	aad := s.additionalData(rec)
	plaintext, err := s.AEAD.Open(nil, nonce, rec.Ciphertext, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthentication, err)
	}
	return plaintext, nil
}

func (s *Session) recordNonce(rec *Record) []byte {
	if s.NonceFunc != nil {
		return s.NonceFunc(rec)
	}

	size := s.AEAD.NonceSize()
	if size <= 0 {
		size = 12
	}
	nonce := make([]byte, size)
	if len(s.NoncePrefix) >= size {
		copy(nonce, s.NoncePrefix[:size])
		return nonce
	}
	copy(nonce, s.NoncePrefix)

	var explicit [8]byte
	binary.BigEndian.PutUint16(explicit[0:2], rec.Epoch)
	writeUint48(explicit[2:8], rec.Sequence)
	copy(nonce[size-8:], explicit[:])
	return nonce
}

func (s *Session) additionalData(rec *Record) []byte {
	if s.AdditionalData != nil {
		return s.AdditionalData(rec)
	}
	innerLen := len(rec.Ciphertext) - s.AEAD.Overhead()
	if innerLen < 0 {
		innerLen = 0
	}
	return AdditionalData(rec, innerLen)
}

// SealRecord encrypts plaintext as a tls12_cid record for this session. It is
// primarily useful for callers and tests that need to produce RFC 9146
// records with the session's configured AEAD.
func (s *Session) SealRecord(rec *Record, plaintext []byte) ([]byte, error) {
	if s.AEAD == nil {
		return nil, ErrNoAuthenticator
	}
	if rec.Type == 0 {
		rec.Type = ContentTypeTLS12CID
	}
	rec.CIDLength = uint8(len(rec.CID))
	rec.CID = append([]byte(nil), s.CID...)

	nonce := s.recordNonce(rec)
	var aad []byte
	if s.AdditionalData != nil {
		aad = s.AdditionalData(rec)
	} else {
		aad = AdditionalData(rec, len(plaintext))
	}
	ciphertext := s.AEAD.Seal(nil, nonce, plaintext, aad)
	rec.Ciphertext = ciphertext
	rec.Length = uint16(len(ciphertext))
	return rec.Marshal()
}

// Open decrypts rec and returns its plaintext.
func (s *Session) Open(rec *Record) ([]byte, error) {
	return s.openRecord(rec)
}

func readUint48(b []byte) uint64 {
	return uint64(b[0])<<40 | uint64(b[1])<<32 | uint64(b[2])<<24 |
		uint64(b[3])<<16 | uint64(b[4])<<8 | uint64(b[5])
}

func writeUint48(b []byte, v uint64) {
	b[0] = byte(v >> 40)
	b[1] = byte(v >> 32)
	b[2] = byte(v >> 24)
	b[3] = byte(v >> 16)
	b[4] = byte(v >> 8)
	b[5] = byte(v)
}
