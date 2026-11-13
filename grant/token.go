package grant

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"time"
)

// tokenWireV1 is the format tag for the token encoding.
const tokenWireV1 byte = 1

// Token is an opaque grant bearer token. All structural fields are
// authenticated by MAC; the MAC is recomputed and compared in constant time.
type Token struct {
	GrantID    string
	Generation uint64
	TokenID    []byte
	Expiry     time.Time
	Digest     [sha256.Size]byte
	MAC        [sha256.Size]byte
}

func macMessage(t *Token) []byte {
	b := make([]byte, 0, 128)
	b = append(b, 'T') // domain separator for "grant token v1"
	b = writeString(b, t.GrantID)
	b = writeUint64(b, t.Generation)
	b = writeUint64(b, uint64(t.Expiry.UnixNano()))
	b = append(b, t.Digest[:]...)
	b = writeUint64(b, uint64(len(t.TokenID)))
	b = append(b, t.TokenID...)
	return b
}

func computeMAC(serviceKey []byte, t *Token) [sha256.Size]byte {
	h := hmac.New(sha256.New, serviceKey)
	h.Write(macMessage(t))
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// MintToken creates a random token bound to the given grant digest.
func MintToken(serviceKey []byte, g *Grant, ttl time.Duration, now time.Time) (*Token, error) {
	if len(serviceKey) < sha256.Size {
		return nil, ErrInvalidRequest
	}
	if ttl <= 0 {
		return nil, ErrInvalidRequest
	}
	id := make([]byte, 16)
	if _, err := rand.Read(id); err != nil {
		return nil, err
	}
	t := &Token{
		GrantID:    g.GrantID,
		Generation: g.Generation,
		TokenID:    id,
		Expiry:     now.Add(ttl),
		Digest:     g.Digest(),
	}
	t.MAC = computeMAC(serviceKey, t)
	return t, nil
}

// Marshal returns the URL-safe base64 token string.
func (t *Token) Marshal() string {
	b := make([]byte, 0, 160)
	b = append(b, tokenWireV1)
	b = writeString(b, t.GrantID)
	b = writeUint64(b, t.Generation)
	b = writeUint64(b, uint64(t.Expiry.UnixNano()))
	b = append(b, t.Digest[:]...)
	b = writeUint64(b, uint64(len(t.TokenID)))
	b = append(b, t.TokenID...)
	b = append(b, t.MAC[:]...)
	return base64.RawURLEncoding.EncodeToString(b)
}

// ParseToken decodes and cryptographically verifies a token. Expiry is
// checked against now; a token that fails MAC verification can never be
// trusted regardless of how it is stored or looked up.
func ParseToken(serviceKey []byte, encoded string, now time.Time) (*Token, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(raw) < 1 || raw[0] != tokenWireV1 {
		return nil, deny(ErrTokenInvalid, "grant: malformed token")
	}
	t := &Token{}
	r := newReader(raw[1:])
	t.GrantID = r.readString()
	t.Generation = r.readUint64()
	expiryNanos := r.readUint64()
	r.readBytes(t.Digest[:])
	t.TokenID = r.readVarBytes()
	r.readBytes(t.MAC[:])
	if r.err != nil || r.remaining() != 0 {
		return nil, deny(ErrTokenInvalid, "grant: truncated token")
	}
	t.Expiry = time.Unix(0, int64(expiryNanos))

	expected := computeMAC(serviceKey, t)
	if subtle.ConstantTimeCompare(expected[:], t.MAC[:]) != 1 {
		return nil, deny(ErrTokenInvalid, "grant: token mac mismatch")
	}
	if !now.Before(t.Expiry) {
		return nil, deny(ErrTokenExpired, "grant: token expired at %s", t.Expiry.Format(time.RFC3339Nano))
	}
	return t, nil
}

type byteReader struct {
	b   []byte
	pos int
	err error
}

func newReader(b []byte) *byteReader { return &byteReader{b: b} }

func (r *byteReader) remaining() int { return len(r.b) - r.pos }

func (r *byteReader) readBytes(dst []byte) {
	if r.err != nil || r.remaining() < len(dst) {
		r.err = ErrTokenInvalid
		return
	}
	copy(dst, r.b[r.pos:r.pos+len(dst)])
	r.pos += len(dst)
}

func (r *byteReader) readUint64() uint64 {
	var buf [8]byte
	r.readBytes(buf[:])
	if r.err != nil {
		return 0
	}
	return binary.BigEndian.Uint64(buf[:])
}

func (r *byteReader) readUint32() uint32 {
	var buf [4]byte
	r.readBytes(buf[:])
	if r.err != nil {
		return 0
	}
	return binary.BigEndian.Uint32(buf[:])
}

func (r *byteReader) readVarBytes() []byte {
	n := r.readUint64()
	if r.err != nil || n > uint64(r.remaining()) {
		r.err = ErrTokenInvalid
		return nil
	}
	out := make([]byte, n)
	r.readBytes(out)
	return out
}

func (r *byteReader) readString() string {
	n := r.readUint32()
	if r.err != nil || n > uint32(r.remaining()) {
		r.err = ErrTokenInvalid
		return ""
	}
	out := make([]byte, n)
	r.readBytes(out)
	if r.err != nil {
		return ""
	}
	return string(out)
}
