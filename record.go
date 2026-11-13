package netsec

import (
	"encoding/binary"
	"fmt"
)

const (
	// RecordVersion is the only record format understood by this package.
	RecordVersion uint8 = 1

	// CommitmentSize is the truncated HMAC-SHA256 commitment length.
	CommitmentSize = 16

	// MaxKeyIDLen bounds key identifiers. Record encodings store this as a
	// one-byte length.
	MaxKeyIDLen = 255

	// MaxNonceLen bounds nonces independently of the injected AEAD's nonce
	// size. Serviceable AEADs use much smaller nonces.
	MaxNonceLen = 64

	// MaxCiphertextLen bounds the encoded ciphertext.
	MaxCiphertextLen = 64 << 20

	// MaxAADLen bounds additional authenticated data supplied at seal/open
	// time. AAD is committed but is not stored in the record.
	MaxAADLen = 1 << 20

	recordVersionOffset       = 0
	recordKeyIDLenOffset      = 1
	recordNonceLenOffset      = 2
	recordCiphertextLenOffset = 3
	recordCommitOffset        = 7
	recordHeaderSize          = recordCommitOffset + CommitmentSize

	// MaxRecordLen is the largest canonical record this package accepts.
	MaxRecordLen = recordHeaderSize + MaxKeyIDLen + MaxNonceLen + MaxCiphertextLen
)

// Record is the persisted form of an authenticated, key-committed envelope.
//
// AAD is deliberately not stored. It must be supplied to Envelope.Open and is
// included in both the commitment input and the AEAD authentication.
type Record struct {
	Version    uint8
	KeyID      string
	Nonce      []byte
	Commit     [CommitmentSize]byte
	Ciphertext []byte
}

// Clone returns a deep copy of the record.
func (record *Record) Clone() *Record {
	if record == nil {
		return nil
	}
	clone := *record
	clone.Nonce = cloneBytes(record.Nonce)
	clone.Ciphertext = cloneBytes(record.Ciphertext)
	return &clone
}

// MarshalBinary returns the canonical record encoding:
//
//	version                1 byte
//	key ID length          1 byte
//	nonce length           1 byte
//	ciphertext length      4 bytes, big endian
//	commitment            16 bytes
//	key ID                 key ID length bytes
//	nonce                  nonce length bytes
//	ciphertext             ciphertext length bytes
func (record Record) MarshalBinary() ([]byte, error) {
	if err := validateRecord(&record); err != nil {
		return nil, err
	}

	keyID := []byte(record.KeyID)
	encoded := make([]byte, recordHeaderSize+len(keyID)+len(record.Nonce)+len(record.Ciphertext))
	encoded[recordVersionOffset] = record.Version
	encoded[recordKeyIDLenOffset] = byte(len(keyID))
	encoded[recordNonceLenOffset] = byte(len(record.Nonce))
	binary.BigEndian.PutUint32(encoded[recordCiphertextLenOffset:recordCommitOffset], uint32(len(record.Ciphertext)))
	copy(encoded[recordCommitOffset:recordHeaderSize], record.Commit[:])

	offset := recordHeaderSize
	offset += copy(encoded[offset:], keyID)
	offset += copy(encoded[offset:], record.Nonce)
	copy(encoded[offset:], record.Ciphertext)
	return encoded, nil
}

// UnmarshalBinary parses a canonical record encoding. It rejects unknown
// versions, zero-length key IDs or nonces, oversized fields, truncated data,
// and trailing data.
func (record *Record) UnmarshalBinary(data []byte) error {
	parsed, err := ParseRecord(data)
	if err != nil {
		return err
	}
	*record = *parsed
	return nil
}

// MarshalRecord is the function form of Record.MarshalBinary.
func MarshalRecord(record *Record) ([]byte, error) {
	if record == nil {
		return nil, fmt.Errorf("%w: nil record", ErrMalformedRecord)
	}
	return record.MarshalBinary()
}

// ParseRecord parses a canonical record encoding into a private copy.
func ParseRecord(data []byte) (*Record, error) {
	if len(data) > MaxRecordLen {
		return nil, fmt.Errorf("%w: %d bytes exceeds %d", ErrMalformedRecord, len(data), MaxRecordLen)
	}
	if len(data) < recordHeaderSize {
		return nil, fmt.Errorf("%w: truncated header", ErrMalformedRecord)
	}
	if data[recordVersionOffset] != RecordVersion {
		return nil, fmt.Errorf("%w: unsupported version %d", ErrMalformedRecord, data[recordVersionOffset])
	}

	keyIDLen := int(data[recordKeyIDLenOffset])
	nonceLen := int(data[recordNonceLenOffset])
	ciphertextLen := uint64(binary.BigEndian.Uint32(data[recordCiphertextLenOffset:recordCommitOffset]))

	if keyIDLen == 0 {
		return nil, fmt.Errorf("%w: empty key identifier", ErrMalformedRecord)
	}
	if nonceLen == 0 || nonceLen > MaxNonceLen {
		return nil, fmt.Errorf("%w: invalid nonce length %d", ErrMalformedRecord, nonceLen)
	}
	if ciphertextLen > MaxCiphertextLen {
		return nil, fmt.Errorf("%w: ciphertext length %d exceeds %d", ErrMalformedRecord, ciphertextLen, MaxCiphertextLen)
	}

	totalLen := uint64(recordHeaderSize) + uint64(keyIDLen) + uint64(nonceLen) + ciphertextLen
	if totalLen != uint64(len(data)) {
		return nil, fmt.Errorf("%w: encoded length %d, actual %d", ErrMalformedRecord, totalLen, len(data))
	}

	record := &Record{Version: data[recordVersionOffset]}
	copy(record.Commit[:], data[recordCommitOffset:recordHeaderSize])

	offset := recordHeaderSize
	record.KeyID = string(data[offset : offset+keyIDLen])
	offset += keyIDLen
	record.Nonce = cloneBytes(data[offset : offset+nonceLen])
	offset += nonceLen
	record.Ciphertext = cloneBytes(data[offset:])

	if err := validateRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

func validateRecord(record *Record) error {
	if record == nil {
		return fmt.Errorf("%w: nil record", ErrMalformedRecord)
	}
	if record.Version != RecordVersion {
		return fmt.Errorf("%w: unsupported version %d", ErrMalformedRecord, record.Version)
	}
	if err := validateKeyID(record.KeyID); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedRecord, err)
	}
	if len(record.Nonce) == 0 || len(record.Nonce) > MaxNonceLen {
		return fmt.Errorf("%w: invalid nonce length %d", ErrMalformedRecord, len(record.Nonce))
	}
	if len(record.Ciphertext) > MaxCiphertextLen {
		return fmt.Errorf("%w: ciphertext length %d exceeds %d", ErrMalformedRecord, len(record.Ciphertext), MaxCiphertextLen)
	}
	return nil
}

func cloneBytes(data []byte) []byte {
	if data == nil {
		return nil
	}
	clone := make([]byte, len(data))
	copy(clone, data)
	return clone
}
