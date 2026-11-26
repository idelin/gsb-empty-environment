package tls13sm

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"crypto/sha512"
	"hash"
)

// CipherSuite describes an AEAD-based TLS 1.3 cipher suite together with the
// hash function used by its key schedule. Only AEAD suites whose primitives
// are available in the Go standard library are implemented here.
type CipherSuite struct {
	ID      uint16
	Name    string
	NewHash func() hash.Hash
	HashLen int
	KeyLen  int
	IVLen   int
}

var (
	// SuiteAES128GCM is TLS_AES_128_GCM_SHA256 (RFC 8446 B.4).
	SuiteAES128GCM = &CipherSuite{
		ID:      0x1301,
		Name:    "TLS_AES_128_GCM_SHA256",
		NewHash: sha256.New,
		HashLen: 32,
		KeyLen:  16,
		IVLen:   12,
	}

	// SuiteAES256GCM is TLS_AES_256_GCM_SHA384.
	SuiteAES256GCM = &CipherSuite{
		ID:      0x1302,
		Name:    "TLS_AES_256_GCM_SHA384",
		NewHash: sha512.New,
		HashLen: 48,
		KeyLen:  32,
		IVLen:   12,
	}
)

// SupportedCipherSuites returns the suite IDs offered/accepted by default.
func SupportedCipherSuites() []*CipherSuite {
	return []*CipherSuite{SuiteAES128GCM, SuiteAES256GCM}
}

func cipherSuiteByID(id uint16) (*CipherSuite, error) {
	for _, suite := range SupportedCipherSuites() {
		if suite.ID == id {
			return suite, nil
		}
	}
	return nil, errProto("negotiated cipher suite 0x%04x is not supported", id)
}

// newAEAD constructs the AES-GCM AEAD for a traffic key.
func (s *CipherSuite) newAEAD(key []byte) (cipher.AEAD, error) {
	if len(key) != s.KeyLen {
		return nil, errProto("traffic key length %d does not match suite %s", len(key), s.Name)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead, nil
}

// Epoch labels the traffic protection generation of a record.
type Epoch uint8

const (
	EpochPlaintext Epoch = 0
	EpochEarlyData Epoch = 1
	EpochHandshake Epoch = 2
	EpochAppData   Epoch = 3
)

// Record is one TLS record. Protected records carry the outer content type
// application_data; the true inner content type is recovered after AEAD
// decryption. Epoch lets the state machine reject ciphertext presented under
// the wrong protection generation.
type Record struct {
	Epoch       Epoch
	ContentType uint8
	Version     uint16
	Payload     []byte
}

// recordCipher is one direction of TLS 1.3 record protection. It owns the
// per-direction sequence number and implements the nonce construction and
// additional data rules of RFC 8446 section 5.2 and 5.3.
type recordCipher struct {
	aead     cipher.AEAD
	iv       []byte
	sequence uint64
}

func newRecordCipher(suite *CipherSuite, key, iv []byte) (*recordCipher, error) {
	if len(iv) != suite.IVLen {
		return nil, errProto("traffic IV length %d does not match suite %s", len(iv), suite.Name)
	}
	aead, err := suite.newAEAD(key)
	if err != nil {
		return nil, err
	}
	return &recordCipher{aead: aead, iv: append([]byte(nil), iv...)}, nil
}

// nonce XORs the 64-bit record sequence number into the rightmost eight bytes
// of the static traffic IV (RFC 8446 5.2).
func (c *recordCipher) nonce(sequence uint64) []byte {
	nonce := append([]byte(nil), c.iv...)
	for i := 0; i < 8; i++ {
		nonce[len(nonce)-1-i] ^= byte(sequence >> (8 * i))
	}
	return nonce
}

// recordHeaderAAD returns the five-byte record header used as additional data.
// Protected records always use the outer content type application_data and the
// legacy version 0x0303.
func recordHeaderAAD(length int) []byte {
	return []byte{
		ContentTypeApplicationData,
		byte(LegacyVersionTLS12 >> 8),
		byte(LegacyVersionTLS12 & 0xff),
		byte(length >> 8),
		byte(length),
	}
}

// seal encrypts an inner plaintext that already ends with the real content
// type byte and returns the protected record payload.
func (c *recordCipher) seal(innerPlaintext []byte) []byte {
	nonce := c.nonce(c.sequence)
	aad := recordHeaderAAD(len(innerPlaintext) + c.aead.Overhead())
	sealed := c.aead.Seal(nil, nonce, innerPlaintext, aad)
	c.sequence++
	return sealed
}

// open authenticates and decrypts a protected record payload and returns the
// inner plaintext. A tampered header or body yields ErrDecryptFailed.
func (c *recordCipher) open(payload []byte) ([]byte, error) {
	nonce := c.nonce(c.sequence)
	aad := recordHeaderAAD(len(payload))
	plaintext, err := c.aead.Open(nil, nonce, payload, aad)
	if err != nil {
		return nil, ErrDecryptFailed
	}
	c.sequence++
	return plaintext, nil
}

// sealRecord builds a protected record for an inner content type and body.
func (c *recordCipher) sealRecord(innerType uint8, body []byte) Record {
	inner := make([]byte, 0, len(body)+1)
	inner = append(inner, body...)
	inner = appendU8(inner, innerType)
	return Record{
		ContentType: ContentTypeApplicationData,
		Version:     LegacyVersionTLS12,
		Payload:     c.seal(inner),
	}
}

// openRecord decrypts a protected record and splits the trailing real content
// type from its body.
func (c *recordCipher) openRecord(record Record) (innerType uint8, body []byte, err error) {
	if record.ContentType != ContentTypeApplicationData {
		return 0, nil, errIllegal("protected record must use outer content type application_data")
	}
	plaintext, err := c.open(record.Payload)
	if err != nil {
		return 0, nil, err
	}
	if len(plaintext) == 0 {
		return 0, nil, errIllegal("protected record missing inner content type")
	}
	return plaintext[len(plaintext)-1], plaintext[:len(plaintext)-1], nil
}
