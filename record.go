package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
)

const (
	// ContentTypeHandshake is the TLS 1.3 inner content type for handshake
	// messages.
	ContentTypeHandshake byte = 0x18

	// KeyUpdateMessageType is handshake message type 24 (KeyUpdate).
	KeyUpdateMessageType byte = 24

	recordOuterContentType byte   = 0x17
	recordLegacyVersion    uint16 = 0x0303
	maxRecordPlaintext            = 1 << 14
	maxRecordCiphertext           = (1 << 14) + 256
)

// CipherSuite identifies the parts of a TLS 1.3 cipher suite that the key
// update state machine needs.
type CipherSuite struct {
	Name    string
	Hash    func() hash.Hash
	KeyLen  int
	IVLen   int
	NewAEAD func(key []byte) (cipher.AEAD, error)
}

// TLS13AES128GCM is TLS_AES_128_GCM_SHA256.
var TLS13AES128GCM = CipherSuite{
	Name:    "TLS_AES_128_GCM_SHA256",
	Hash:    sha256.New,
	KeyLen:  16,
	IVLen:   12,
	NewAEAD: newAESGCM,
}

// TLS13AES256GCM is TLS_AES_256_GCM_SHA384.
var TLS13AES256GCM = CipherSuite{
	Name:    "TLS_AES_256_GCM_SHA384",
	Hash:    sha512.New384,
	KeyLen:  32,
	IVLen:   12,
	NewAEAD: newAESGCM,
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nonceFor(iv []byte, seq uint64) []byte {
	nonce := make([]byte, len(iv))
	copy(nonce, iv)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], seq)
	for i := range encoded {
		nonce[len(nonce)-8+i] ^= encoded[i]
	}
	return nonce
}

func recordHeader(ciphertextLen int) []byte {
	header := make([]byte, 5)
	header[0] = recordOuterContentType
	binary.BigEndian.PutUint16(header[1:3], recordLegacyVersion)
	binary.BigEndian.PutUint16(header[3:5], uint16(ciphertextLen))
	return header
}

func (s *KeyUpdateState) sealRecordLocked(contentType byte, plaintext []byte) ([]byte, error) {
	if s.writeSeq == ^uint64(0) {
		return nil, ErrSequenceExhausted
	}
	if contentType == 0 {
		return nil, ErrMalformedRecord
	}
	if len(plaintext)+1 > maxRecordPlaintext {
		return nil, ErrRecordTooLarge
	}
	inner := make([]byte, 0, len(plaintext)+1)
	inner = append(inner, plaintext...)
	inner = append(inner, contentType)

	aad := recordHeader(len(inner) + s.writeAEAD.Overhead())
	ciphertext := s.writeAEAD.Seal(nil, nonceFor(s.writeIV, s.writeSeq), inner, aad)
	record := make([]byte, 0, len(aad)+len(ciphertext))
	record = append(record, aad...)
	record = append(record, ciphertext...)
	s.writeSeq++
	return record, nil
}

func (s *KeyUpdateState) openRecordLocked(record []byte) (byte, []byte, error) {
	if len(record) < 5 {
		return 0, nil, ErrMalformedRecord
	}
	if record[0] != recordOuterContentType {
		return 0, nil, ErrMalformedRecord
	}
	if binary.BigEndian.Uint16(record[1:3]) != recordLegacyVersion {
		return 0, nil, ErrMalformedRecord
	}
	ciphertextLen := int(binary.BigEndian.Uint16(record[3:5]))
	if ciphertextLen > maxRecordCiphertext || len(record) != 5+ciphertextLen {
		return 0, nil, ErrMalformedRecord
	}
	if s.readSeq == ^uint64(0) {
		return 0, nil, ErrSequenceExhausted
	}

	aad := record[:5]
	inner, err := s.readAEAD.Open(nil, nonceFor(s.readIV, s.readSeq), record[5:], aad)
	if err != nil {
		// A failed authentication consumes neither a sequence number nor a
		// read key generation. The caller must treat this as a fatal alert in
		// a real TLS connection.
		return 0, nil, ErrAuthentication
	}

	// The record is authentic, so from this point on it is safe to consume the
	// sequence number, even if the inner plaintext is later rejected.
	s.readSeq++

	lastNonzero := len(inner) - 1
	for lastNonzero >= 0 && inner[lastNonzero] == 0 {
		lastNonzero--
	}
	if lastNonzero < 0 {
		return 0, nil, ErrMalformedRecord
	}
	return inner[lastNonzero], append([]byte(nil), inner[:lastNonzero]...), nil
}

func validateSuite(suite CipherSuite) (CipherSuite, error) {
	if suite.Hash == nil {
		suite = TLS13AES128GCM
	}
	if suite.NewAEAD == nil {
		return CipherSuite{}, fmt.Errorf("netsec: cipher suite %q has no AEAD constructor", suite.Name)
	}
	if suite.KeyLen <= 0 {
		return CipherSuite{}, fmt.Errorf("netsec: cipher suite %q has invalid key length %d", suite.Name, suite.KeyLen)
	}
	if suite.IVLen < 8 {
		return CipherSuite{}, fmt.Errorf("netsec: cipher suite %q has invalid IV length %d", suite.Name, suite.IVLen)
	}
	if suite.Hash().Size() <= 0 {
		return CipherSuite{}, errors.New("netsec: cipher suite hash has zero output size")
	}
	return suite, nil
}
