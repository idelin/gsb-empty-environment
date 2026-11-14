package netsec

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"fmt"
	"time"
)

// FormatVersion is committed inside the AAD.  It is deliberately independent
// of the key epoch so old handles cannot be reinterpreted after a format
// change.
const FormatVersion uint8 = 1

var handleMagic = [4]byte{'N', 'F', 'S', 'H'}

const (
	nonceSize       = 12
	tagSize         = 16
	serverTagSize   = 16
	maxKeyIDLength  = 64
	maxHandleLength = 64 << 10
	payloadVersion  = uint8(1)
)

// CodecConfig configures handle sealing and opening.
type CodecConfig struct {
	// ServerInstance is a stable, deployment-unique instance name.  It is
	// authenticated in the AAD, so a handle from another server cannot be
	// replayed even when both servers share key material.
	ServerInstance string
	// KeyRing supplies the active and retired keys.
	KeyRing *KeyRing
	// Now is optional and defaults to time.Now.  It is used for key grace
	// decisions and is injectable for deterministic deployments/tests.
	Now func() time.Time
}

// Codec seals and opens opaque file handles.
type Codec struct {
	serverInstance string
	serverTag      [16]byte
	keys           *KeyRing
	now            func() time.Time
}

// NewCodec creates a handle codec.
func NewCodec(cfg CodecConfig) (*Codec, error) {
	if cfg.ServerInstance == "" {
		return nil, fmt.Errorf("netsec: server instance must not be empty")
	}
	if cfg.KeyRing == nil {
		return nil, fmt.Errorf("netsec: key ring must not be nil")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Codec{
		serverInstance: cfg.ServerInstance,
		serverTag:      serverTag(cfg.ServerInstance),
		keys:           cfg.KeyRing,
		now:            now,
	}, nil
}

// ServerInstance returns the AAD-bound instance name.
func (c *Codec) ServerInstance() string {
	return c.serverInstance
}

// KeyRing returns the key ring used by the codec.
func (c *Codec) KeyRing() *KeyRing {
	return c.keys
}

// Seal encrypts claims with the active key.
func (c *Codec) Seal(claims Claims) ([]byte, error) {
	keyID, material, err := c.keys.sealMaterial()
	if err != nil {
		return nil, err
	}
	return c.sealWithMaterial(keyID, material, claims)
}

// Encode is an alias for Seal.
func (c *Codec) Encode(claims Claims) ([]byte, error) {
	return c.Seal(claims)
}

// SealWithKey seals with a named key.  This exists for migration/rewrap tools.
// Opening still enforces the retired-key generation cut-off, so sealing a new
// object under an old key does not make it usable.
func (c *Codec) SealWithKey(keyID string, claims Claims) ([]byte, error) {
	material, err := c.keys.material(keyID)
	if err != nil {
		return nil, err
	}
	return c.sealWithMaterial(keyID, material, claims)
}

func (c *Codec) sealWithMaterial(keyID string, material [32]byte, claims Claims) ([]byte, error) {
	if keyID == "" || len(keyID) > maxKeyIDLength {
		return nil, ErrMalformed
	}
	if claims.ObjectID == "" || len(claims.ObjectID) > 0xffff {
		return nil, deny(StatusInvalid, ErrInvalidArgument, "object ID must be 1..65535 bytes")
	}
	if !claims.ObjectType.valid() {
		return nil, ErrUnsupportedObjectType
	}
	payload := encodePayload(claims)
	if len(payload) > maxHandleLength {
		return nil, deny(StatusInvalid, ErrInvalidArgument, "handle payload too large")
	}
	block, err := aes.NewCipher(material[:])
	if err != nil {
		return nil, fmt.Errorf("netsec: create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: create GCM: %w", err)
	}
	var nonce [nonceSize]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, fmt.Errorf("netsec: generate nonce: %w", err)
	}
	tag := c.serverTag
	header := make([]byte, 0, 6+len(keyID)+serverTagSize+nonceSize+len(payload)+tagSize)
	header = append(header, handleMagic[:]...)
	header = append(header, FormatVersion, byte(len(keyID)))
	header = append(header, keyID...)
	header = append(header, tag[:]...)
	header = append(header, nonce[:]...)
	aad := c.associatedData(keyID, tag)
	ciphertext := aead.Seal(nil, nonce[:], payload, aad)
	return append(header, ciphertext...), nil
}

func (c *Codec) associatedData(keyID string, tag [16]byte) []byte {
	aad := make([]byte, 0, 4+1+1+len(keyID)+serverTagSize+len(c.serverInstance))
	aad = append(aad, handleMagic[:]...)
	aad = append(aad, FormatVersion, byte(len(keyID)))
	aad = append(aad, keyID...)
	aad = append(aad, tag[:]...)
	aad = append(aad, c.serverInstance...)
	return aad
}

// Capability is an authenticated, opaque handle.  It deliberately exposes no
// export UUID, object ID, generation, type, or policy metadata.  Only Manager
// can authorize an operation on it.
type Capability struct {
	raw       []byte
	claims    Claims
	keyID     string
	serverTag [16]byte
}

// Raw returns a copy of the encrypted handle bytes.
func (c *Capability) Raw() []byte {
	if c == nil {
		return nil
	}
	return append([]byte(nil), c.raw...)
}

// String prevents accidental metadata logging with %v.
func (c *Capability) String() string {
	if c == nil {
		return "<nil>"
	}
	return "netsec.Capability(opaque)"
}

// Open authenticates and decrypts a handle.  It returns only an opaque
// Capability.  Key grace rules are applied here and again on every Manager
// access decision.
func (c *Codec) Open(raw []byte) (*Capability, error) {
	return c.OpenAt(raw, c.now())
}

// Decode is an alias for Open.
func (c *Codec) Decode(raw []byte) (*Capability, error) {
	return c.Open(raw)
}

// OpenAt is Open with an explicit clock.
func (c *Codec) OpenAt(raw []byte, now time.Time) (*Capability, error) {
	if len(raw) < 6+serverTagSize+nonceSize+tagSize || len(raw) > maxHandleLength {
		return nil, ErrMalformed
	}
	if subtle.ConstantTimeCompare(raw[0:4], handleMagic[:]) != 1 {
		return nil, ErrMalformed
	}
	version := raw[4]
	if version != FormatVersion {
		return nil, ErrUnsupportedVersion
	}
	keyLen := int(raw[5])
	if keyLen == 0 || keyLen > maxKeyIDLength {
		return nil, ErrMalformed
	}
	off := 6
	if len(raw) < off+keyLen+serverTagSize+nonceSize+tagSize {
		return nil, ErrMalformed
	}
	keyID := string(raw[off : off+keyLen])
	off += keyLen
	var tag [16]byte
	copy(tag[:], raw[off:off+serverTagSize])
	off += serverTagSize
	if subtle.ConstantTimeCompare(tag[:], c.serverTag[:]) != 1 {
		return nil, ErrWrongServer
	}
	var nonce [nonceSize]byte
	copy(nonce[:], raw[off:off+nonceSize])
	off += nonceSize
	ciphertext := raw[off:]
	if len(ciphertext) < tagSize {
		return nil, ErrHandleTampered
	}
	material, err := c.keys.material(keyID)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(material[:])
	if err != nil {
		return nil, fmt.Errorf("netsec: create AES cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("netsec: create GCM: %w", err)
	}
	payload, err := aead.Open(nil, nonce[:], ciphertext, c.associatedData(keyID, tag))
	if err != nil {
		return nil, ErrHandleTampered
	}
	claims, err := decodePayload(payload)
	if err != nil {
		return nil, ErrMalformed
	}
	if err := c.keys.authorize(keyID, claims.ObjectGeneration, now); err != nil {
		return nil, err
	}
	return &Capability{
		raw:       append([]byte(nil), raw...),
		claims:    claims,
		keyID:     keyID,
		serverTag: tag,
	}, nil
}

func encodePayload(claims Claims) []byte {
	n := 1 + 16 + 8 + 2 + len(claims.ObjectID) + 8 + 1 + 32
	b := make([]byte, n)
	off := 0
	b[off] = payloadVersion
	off++
	copy(b[off:off+16], claims.ExportID[:])
	off += 16
	binary.BigEndian.PutUint64(b[off:off+8], claims.ExportGeneration)
	off += 8
	binary.BigEndian.PutUint16(b[off:off+2], uint16(len(claims.ObjectID)))
	off += 2
	copy(b[off:off+len(claims.ObjectID)], claims.ObjectID)
	off += len(claims.ObjectID)
	binary.BigEndian.PutUint64(b[off:off+8], claims.ObjectGeneration)
	off += 8
	b[off] = byte(claims.ObjectType)
	off++
	copy(b[off:off+32], claims.PolicyDigest[:])
	return b
}

func decodePayload(b []byte) (Claims, error) {
	var claims Claims
	if len(b) < 1+16+8+2+8+1+32 {
		return claims, ErrMalformed
	}
	off := 0
	if b[off] != payloadVersion {
		return claims, ErrMalformed
	}
	off++
	copy(claims.ExportID[:], b[off:off+16])
	off += 16
	claims.ExportGeneration = binary.BigEndian.Uint64(b[off : off+8])
	off += 8
	idLen := int(binary.BigEndian.Uint16(b[off : off+2]))
	off += 2
	if idLen == 0 || len(b) < off+idLen+8+1+32 {
		return claims, ErrMalformed
	}
	claims.ObjectID = StableObjectID(string(b[off : off+idLen]))
	off += idLen
	claims.ObjectGeneration = binary.BigEndian.Uint64(b[off : off+8])
	off += 8
	claims.ObjectType = ObjectType(b[off])
	off++
	if !claims.ObjectType.valid() {
		return claims, ErrMalformed
	}
	copy(claims.PolicyDigest[:], b[off:off+32])
	off += 32
	if off != len(b) {
		return claims, ErrMalformed
	}
	return claims, nil
}
