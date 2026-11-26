package tls13

import (
	"crypto"
	"crypto/aes"
	"crypto/cipher"
	_ "crypto/sha256"
	_ "crypto/sha512"
	"hash"
)

// CipherSuite describes the two TLS 1.3 AEAD suites implemented by this
// package.
type CipherSuite struct {
	id     uint16
	name   string
	hash   crypto.Hash
	keyLen int
	ivLen  int
	tagLen int
}

// TLS cipher suite identifiers.
const (
	TLSACipherSuiteAES128GCMSHA256 uint16 = 0x1301
	TLSACipherSuiteAES256GCMSHA384 uint16 = 0x1302
)

var (
	TLS_AES_128_GCM_SHA256 = CipherSuite{
		id:     TLSACipherSuiteAES128GCMSHA256,
		name:   "TLS_AES_128_GCM_SHA256",
		hash:   crypto.SHA256,
		keyLen: 16,
		ivLen:  12,
		tagLen: 16,
	}
	TLS_AES_256_GCM_SHA384 = CipherSuite{
		id:     TLSACipherSuiteAES256GCMSHA384,
		name:   "TLS_AES_256_GCM_SHA384",
		hash:   crypto.SHA384,
		keyLen: 32,
		ivLen:  12,
		tagLen: 16,
	}
)

func (c CipherSuite) ID() uint16         { return c.id }
func (c CipherSuite) Name() string       { return c.name }
func (c CipherSuite) KeyLen() int        { return c.keyLen }
func (c CipherSuite) IVLen() int         { return c.ivLen }
func (c CipherSuite) TagLen() int        { return c.tagLen }
func (c CipherSuite) HashSize() int      { return c.hash.Size() }
func (c CipherSuite) NewHash() hash.Hash { return c.hash.New() }
func (c CipherSuite) hashFunc() func() hash.Hash {
	h := c.hash
	return h.New
}

func (c CipherSuite) newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

// CipherSuiteByID returns one of the supported cipher suites.
func CipherSuiteByID(id uint16) (CipherSuite, bool) {
	switch id {
	case TLS_AES_128_GCM_SHA256.id:
		return TLS_AES_128_GCM_SHA256, true
	case TLS_AES_256_GCM_SHA384.id:
		return TLS_AES_256_GCM_SHA384, true
	default:
		return CipherSuite{}, false
	}
}
