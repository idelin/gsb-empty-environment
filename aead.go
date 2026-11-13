package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/bits"
)

const (
	// Version2 is the v2 SEIPD packet version.
	Version2 byte = 2

	// PacketTagSEIPD is the OpenPGP packet tag for SEIPD.
	PacketTagSEIPD byte = 18

	// SaltSize is the size of the per-message HKDF salt.
	SaltSize = 32

	// NonceSize is the AEAD nonce size used by v2 SEIPD.
	NonceSize = 12

	// TagSize is the authentication tag size used by v2 SEIPD.
	TagSize = 16

	// DefaultChunkSize is the default plaintext chunk size (1 MiB).
	DefaultChunkSize = 1 << 20

	// FinalSequence is the sequence marker used by the final chunk.
	FinalSequence uint64 = ^uint64(0)

	// aeadPacketTag is the new-format packet tag octet (0xC0 | 18).
	aeadPacketTag byte = 0xD2
)

// CipherFunction identifies an OpenPGP symmetric cipher.
type CipherFunction uint8

const (
	AES128 CipherFunction = 7
	AES192 CipherFunction = 8
	AES256 CipherFunction = 9
)

// KeySize returns the key size in bytes for the cipher.
func (c CipherFunction) KeySize() int {
	switch c {
	case AES128:
		return 16
	case AES192:
		return 24
	case AES256:
		return 32
	default:
		return 0
	}
}

func (c CipherFunction) String() string {
	switch c {
	case AES128:
		return "AES-128"
	case AES192:
		return "AES-192"
	case AES256:
		return "AES-256"
	default:
		return fmt.Sprintf("cipher(%d)", uint8(c))
	}
}

// AEADMode identifies an OpenPGP AEAD mode.
type AEADMode uint8

const (
	AEADModeEAX AEADMode = 1
	AEADModeOCB AEADMode = 2
	AEADModeGCM AEADMode = 3
)

func (m AEADMode) String() string {
	switch m {
	case AEADModeEAX:
		return "EAX"
	case AEADModeOCB:
		return "OCB"
	case AEADModeGCM:
		return "GCM"
	default:
		return fmt.Sprintf("aead(%d)", uint8(m))
	}
}

// SequencePolicy controls how chunk sequence numbers are validated.
type SequencePolicy int

const (
	// AllowSequenceReuse reproduces the reported nonce-reuse behavior: a
	// non-final chunk may repeat the preceding chunk's sequence number.
	// Sequence jumps, backwards moves, and duplicate final markers are still
	// rejected.
	AllowSequenceReuse SequencePolicy = iota

	// RequireSequential requires 0, 1, 2, ... followed by exactly one final
	// marker, and rejects repeated sequence numbers and truncation.
	RequireSequential
)

const (
	PolicyVulnerable = AllowSequenceReuse
	PolicyStrict     = RequireSequential
)

// Config configures a chunked AEAD cipher.
type Config struct {
	// Cipher selects the symmetric cipher. If zero, it is inferred from the
	// session-key length.
	Cipher CipherFunction

	// Mode selects the AEAD mode. If zero, GCM is used.
	Mode AEADMode

	// ChunkSize is the maximum plaintext size of one chunk in bytes. It must
	// be a power of two and at least 64. If zero, DefaultChunkSize is used.
	ChunkSize int

	// Salt is the 32-byte HKDF salt. If empty, a random salt is generated.
	Salt []byte

	// IV optionally overrides the 12-byte nonce prefix derived from the
	// session key and salt. It is intended for deterministic test vectors.
	IV []byte

	// Policy selects sequence validation behavior. The zero value allows
	// non-final sequence reuse. Strict is a convenience override.
	Policy SequencePolicy

	// Strict is a convenience alias for Policy: RequireSequential.
	Strict bool
}

// Cipher is a chunked AEAD engine for v2 SEIPD data.
type Cipher struct {
	SessionKey []byte
	DataKey    []byte
	IV         []byte
	Salt       []byte
	Cipher     CipherFunction
	Mode       AEADMode
	ChunkSize  int
	Policy     SequencePolicy

	aead cipher.AEAD
}

// AEAD is an alias for Cipher.
type AEAD = Cipher

// NewCipher derives the data-encryption key and nonce prefix and returns a
// chunked AEAD engine. At most one Config may be supplied.
func NewCipher(sessionKey []byte, configs ...Config) (*Cipher, error) {
	if len(configs) > 1 {
		return nil, fmt.Errorf("netsec: expected at most one Config")
	}
	var cfg Config
	if len(configs) == 1 {
		cfg = configs[0]
	}
	return newCipher(sessionKey, cfg)
}

// NewAEAD is an alias for NewCipher.
func NewAEAD(sessionKey []byte, configs ...Config) (*Cipher, error) {
	return NewCipher(sessionKey, configs...)
}

// NewSEIPD is an alias for NewCipher.
func NewSEIPD(sessionKey []byte, configs ...Config) (*Cipher, error) {
	return NewCipher(sessionKey, configs...)
}

// NewCipherWithIV constructs a cipher with a caller-supplied nonce prefix
// and a zero salt. It is useful when the session key has already been
// expanded and the caller wants the nonce progression to start at iv.
func NewCipherWithIV(sessionKey, iv []byte, chunkSize int) (*Cipher, error) {
	return NewCipher(sessionKey, Config{
		ChunkSize: chunkSize,
		Salt:      make([]byte, SaltSize),
		IV:        iv,
	})
}

func newCipher(sessionKey []byte, cfg Config) (*Cipher, error) {
	if len(sessionKey) == 0 {
		return nil, ErrInvalidKey
	}

	cipherFn := cfg.Cipher
	if cipherFn == 0 {
		switch len(sessionKey) {
		case 16:
			cipherFn = AES128
		case 24:
			cipherFn = AES192
		case 32:
			cipherFn = AES256
		default:
			return nil, fmt.Errorf("%w: length %d", ErrInvalidKey, len(sessionKey))
		}
	}
	keySize := cipherFn.KeySize()
	if keySize == 0 {
		return nil, fmt.Errorf("%w: %d", ErrUnsupportedCipher, uint8(cipherFn))
	}
	if len(sessionKey) != keySize {
		return nil, fmt.Errorf("%w: %s needs %d bytes, got %d", ErrInvalidKey, cipherFn, keySize, len(sessionKey))
	}

	mode := cfg.Mode
	if mode == 0 {
		mode = AEADModeGCM
	}
	if mode != AEADModeGCM {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedMode, mode)
	}

	chunkSize := cfg.ChunkSize
	if chunkSize == 0 {
		chunkSize = DefaultChunkSize
	}
	if _, err := chunkExponent(chunkSize); err != nil {
		return nil, err
	}

	salt := append([]byte(nil), cfg.Salt...)
	if len(salt) == 0 {
		salt = make([]byte, SaltSize)
		if _, err := rand.Read(salt); err != nil {
			return nil, fmt.Errorf("netsec: generating salt: %w", err)
		}
	}
	if len(salt) != SaltSize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidSalt, len(salt), SaltSize)
	}

	derived, err := hkdf.Key(
		sha256.New,
		sessionKey,
		salt,
		hkdfInfo(cipherFn, mode),
		keySize+NonceSize,
	)
	if err != nil {
		return nil, fmt.Errorf("netsec: deriving key: %w", err)
	}
	dataKey := append([]byte(nil), derived[:keySize]...)
	iv := append([]byte(nil), derived[keySize:]...)

	if cfg.IV != nil {
		if len(cfg.IV) != NonceSize {
			return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidNonce, len(cfg.IV), NonceSize)
		}
		iv = append([]byte(nil), cfg.IV...)
	}

	block, err := aes.NewCipher(dataKey)
	if err != nil {
		return nil, fmt.Errorf("netsec: initializing cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: initializing GCM: %w", err)
	}
	if aead.NonceSize() != NonceSize || aead.Overhead() != TagSize {
		return nil, fmt.Errorf("netsec: unexpected GCM parameters")
	}

	policy := cfg.Policy
	if cfg.Strict {
		policy = RequireSequential
	}
	if policy != AllowSequenceReuse && policy != RequireSequential {
		return nil, fmt.Errorf("netsec: invalid sequence policy %d", policy)
	}

	return &Cipher{
		SessionKey: append([]byte(nil), sessionKey...),
		DataKey:    dataKey,
		IV:         iv,
		Salt:       salt,
		Cipher:     cipherFn,
		Mode:       mode,
		ChunkSize:  chunkSize,
		Policy:     policy,
		aead:       aead,
	}, nil
}

func hkdfInfo(cipherFn CipherFunction, mode AEADMode) string {
	info := append([]byte("OpenPGP 2022 AEAD"), 0)
	info = append(info, byte(cipherFn), byte(mode))
	return string(info)
}

// nonceFor returns the per-chunk nonce: the message IV with the chunk
// sequence number XORed into its low 64 bits.
func (c *Cipher) nonceFor(sequence uint64) []byte {
	nonce := append([]byte(nil), c.IV...)
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], sequence)
	for i := 0; i < 8; i++ {
		nonce[len(nonce)-8+i] ^= encoded[i]
	}
	return nonce
}

// associatedData returns the additional authenticated data for a chunk. It
// binds the packet version, algorithms, chunk size, and chunk sequence.
func (c *Cipher) associatedData(sequence uint64) []byte {
	exponent, _ := chunkExponent(c.ChunkSize)
	ad := make([]byte, 13)
	ad[0] = aeadPacketTag
	ad[1] = Version2
	ad[2] = byte(c.Cipher)
	ad[3] = byte(c.Mode)
	ad[4] = exponent
	binary.BigEndian.PutUint64(ad[5:], sequence)
	return ad
}

func chunkExponent(size int) (byte, error) {
	if size < 64 || size&(size-1) != 0 {
		return 0, fmt.Errorf("%w: %d is not a power of two >= 64", ErrInvalidChunkSize, size)
	}
	exponent := bits.TrailingZeros(uint(size)) - 6
	if exponent < 0 || exponent > 56 {
		return 0, fmt.Errorf("%w: %d", ErrInvalidChunkSize, size)
	}
	return byte(exponent), nil
}

func chunkSizeFromExponent(exponent byte) (int, error) {
	if exponent > 56 {
		return 0, fmt.Errorf("%w: exponent %d", ErrInvalidChunkSize, exponent)
	}
	return 1 << (uint(exponent) + 6), nil
}

// Nonce returns the nonce that is used for sequence.
func (c *Cipher) Nonce(sequence uint64) []byte {
	return c.nonceFor(sequence)
}

// AAD returns the additional authenticated data that is used for sequence.
func (c *Cipher) AAD(sequence uint64) []byte {
	return c.associatedData(sequence)
}
