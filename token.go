package netsec

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"errors"
)

// Canonical wire markers keep the different structures unambiguous even when
// embedded into one another.
const (
	markerBox    byte = 'B'
	markerParent byte = 'P'
	markerToken  byte = 'T'
)

// maxDecodedDimensions bounds allocations while parsing untrusted tokens.
const maxDecodedDimensions = 1 << 20

var (
	// ErrInvalidToken is returned when a token is malformed or its HMAC tag
	// does not authenticate it.
	ErrInvalidToken = errors.New("netsec: invalid token")

	// ErrWrongParent is returned when a token's parent authorization digest
	// does not match the authorization presented for verification.
	ErrWrongParent = errors.New("netsec: token bound to a different parent authorization")

	// ErrRegionOutside is returned when a token's region is not contained in
	// the presented parent authorization's accessible area.
	ErrRegionOutside = errors.New("netsec: token region outside parent accessible area")
)

// ParentAuth is the wide authorization owned by the scheduling host.
// Accessible is the overall half-open box; Denied is the union of half-open
// boxes subtracted before sharding. The digest is order-independent for the
// denied list.
type ParentAuth struct {
	Accessible Box
	Denied     []Box
}

// Digest returns the canonical SHA-256 digest of the parent authorization.
// It is deterministic: the same geometry always hashes to the same digest,
// and denied boxes are sorted so listing order does not matter.
func (p ParentAuth) Digest() ([32]byte, error) {
	raw, err := p.canonical()
	if err != nil {
		return [32]byte{}, err
	}
	return sha256.Sum256(raw), nil
}

func (p ParentAuth) canonical() ([]byte, error) {
	if err := p.Accessible.validate(); err != nil {
		return nil, err
	}
	dim := p.Accessible.Dim()

	denied := make([]Box, len(p.Denied))
	copy(denied, p.Denied)
	for i := range denied {
		if err := denied[i].validate(); err != nil {
			return nil, err
		}
		if denied[i].Dim() != dim {
			return nil, errors.New("netsec: denied box dimension mismatch")
		}
	}
	sortBoxes(denied)

	var buf bytes.Buffer
	buf.WriteByte(markerParent)
	writeUvarint(&buf, uint64(dim))
	writeBox(&buf, p.Accessible)
	writeUvarint(&buf, uint64(len(denied)))
	for _, d := range denied {
		writeBox(&buf, d)
	}
	return buf.Bytes(), nil
}

// Token is a work shard credential. It binds a specific Region to the digest
// of its parent authorization and carries an HMAC tag over both.
//
// Tokens are opaque to callers: mint one with Mint, persist or transport it
// with MarshalBinary/MarshalText, and validate it with Verify/VerifyFor.
type Token struct {
	parentDigest [32]byte
	region       Box
	tag          []byte
}

// Region returns the half-open box this token authorizes. The returned box
// is a copy of the internal bounds.
func (t Token) Region() Box {
	return Box{
		Lo: append([]int64(nil), t.region.Lo...),
		Hi: append([]int64(nil), t.region.Hi...),
	}
}

// ParentDigest returns the SHA-256 digest of the parent authorization the
// token was minted against.
func (t Token) ParentDigest() [32]byte {
	return t.parentDigest
}

// Mint issues a token binding region to parent, authenticated with key.
// region must be valid and non-empty; key must not be empty.
func Mint(key []byte, parent ParentAuth, region Box) (Token, error) {
	if len(key) == 0 {
		return Token{}, errors.New("netsec: empty HMAC key")
	}
	digest, err := parent.Digest()
	if err != nil {
		return Token{}, err
	}
	if err := region.validate(); err != nil {
		return Token{}, err
	}
	if region.Empty() {
		return Token{}, errors.New("netsec: cannot mint token for empty region")
	}
	if region.Dim() != parent.Accessible.Dim() {
		return Token{}, errors.New("netsec: region dimension mismatch")
	}

	body := tokenBody(digest, region)
	mac := hmac.New(sha256.New, key)
	mac.Write(body)

	token := Token{
		parentDigest: digest,
		region:       cloneBox(region),
	}
	token.tag = mac.Sum(nil)
	return token, nil
}

// MintShards runs the full pipeline: subtract denied from parent, split the
// accessible area into shards of at most maxVolume points, and mint one
// token per shard. Retrying a returned token reuses the exact same region
// and therefore cannot widen authorization scope.
func MintShards(key []byte, parent ParentAuth, maxVolume int64) ([]Token, error) {
	accessible, err := Subtract(parent.Accessible, parent.Denied...)
	if err != nil {
		return nil, err
	}
	shards, err := Split(accessible, maxVolume)
	if err != nil {
		return nil, err
	}
	tokens := make([]Token, 0, len(shards))
	for _, shard := range shards {
		token, err := Mint(key, parent, shard.Region)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	return tokens, nil
}

func tokenBody(digest [32]byte, region Box) []byte {
	var buf bytes.Buffer
	buf.WriteByte(markerToken)
	buf.Write(digest[:])
	writeBox(&buf, region)
	return buf.Bytes()
}

// Verify authenticates the token with key and checks that it was minted for
// parent. It does not check containment of the region in the parent; use
// VerifyFor when presenting a concrete region to act on.
func (t Token) Verify(key []byte, parent ParentAuth) error {
	if len(key) == 0 {
		return errors.New("netsec: empty HMAC key")
	}
	digest, err := parent.Digest()
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare(t.parentDigest[:], digest[:]) != 1 {
		return ErrWrongParent
	}
	if len(t.tag) != sha256.Size {
		return ErrInvalidToken
	}
	mac := hmac.New(sha256.New, key)
	mac.Write(tokenBody(t.parentDigest, t.region))
	if !hmac.Equal(t.tag, mac.Sum(nil)) {
		return ErrInvalidToken
	}
	return nil
}

// VerifyFor authenticates the token and additionally proves that region is
// fully inside the shard carried by the token. A host that only acts after
// VerifyFor succeeds can never be pushed outside the original shard scope by
// a retried or replayed request.
func (t Token) VerifyFor(key []byte, parent ParentAuth, region Box) error {
	if err := t.Verify(key, parent); err != nil {
		return err
	}
	if !boxContains(t.region, region) {
		return ErrRegionOutside
	}
	return nil
}

// MarshalBinary serializes the token into its authenticated binary form:
// the canonical token body followed by the 32-byte HMAC tag.
func (t Token) MarshalBinary() ([]byte, error) {
	if len(t.tag) != sha256.Size {
		return nil, ErrInvalidToken
	}
	body := tokenBody(t.parentDigest, t.region)
	out := make([]byte, 0, len(body)+len(t.tag))
	out = append(out, body...)
	out = append(out, t.tag...)
	return out, nil
}

// UnmarshalBinary parses a token produced by MarshalBinary. The HMAC tag is
// checked later by Verify/VerifyFor; parsing only validates the structure.
func (t *Token) UnmarshalBinary(data []byte) error {
	if len(data) < sha256.Size+1 {
		return ErrInvalidToken
	}
	split := len(data) - sha256.Size
	body, tag := data[:split], data[split:]

	r := bytes.NewReader(body)
	header, err := r.ReadByte()
	if err != nil || header != markerToken {
		return ErrInvalidToken
	}

	var digest [32]byte
	if n, err := r.Read(digest[:]); err != nil || n != sha256.Size {
		return ErrInvalidToken
	}
	region, err := readBox(r)
	if err != nil {
		return ErrInvalidToken
	}
	if r.Len() != 0 {
		return ErrInvalidToken
	}

	t.parentDigest = digest
	t.region = region
	t.tag = append([]byte(nil), tag...)
	return nil
}

// MarshalText returns the standard base64 encoding of the binary token,
// suitable for headers, URLs and log-safe transport.
func (t Token) MarshalText() ([]byte, error) {
	raw, err := t.MarshalBinary()
	if err != nil {
		return nil, err
	}
	return []byte(base64.StdEncoding.EncodeToString(raw)), nil
}

// UnmarshalText parses a base64 token produced by MarshalText.
func (t *Token) UnmarshalText(text []byte) error {
	raw, err := base64.StdEncoding.DecodeString(string(text))
	if err != nil {
		return ErrInvalidToken
	}
	return t.UnmarshalBinary(raw)
}

// Intersect returns the overlap of a and b. When the boxes live in
// different dimensions the result is the zero Box and false.
func Intersect(a, b Box) (Box, bool) {
	if a.Dim() != b.Dim() || a.Dim() == 0 {
		return Box{}, false
	}
	dim := a.Dim()
	out := Box{Lo: make([]int64, dim), Hi: make([]int64, dim)}
	for i := 0; i < dim; i++ {
		lo := a.Lo[i]
		if b.Lo[i] > lo {
			lo = b.Lo[i]
		}
		hi := a.Hi[i]
		if b.Hi[i] < hi {
			hi = b.Hi[i]
		}
		if lo >= hi {
			return Box{}, false
		}
		out.Lo[i] = lo
		out.Hi[i] = hi
	}
	return out, true
}

func boxContains(outer, inner Box) bool {
	if outer.Dim() != inner.Dim() || outer.Dim() == 0 {
		return false
	}
	for i := range outer.Lo {
		if inner.Lo[i] < outer.Lo[i] || inner.Hi[i] > outer.Hi[i] {
			return false
		}
	}
	return true
}

func cloneBox(b Box) Box {
	return Box{
		Lo: append([]int64(nil), b.Lo...),
		Hi: append([]int64(nil), b.Hi...),
	}
}

func writeBox(buf *bytes.Buffer, b Box) {
	buf.WriteByte(markerBox)
	writeUvarint(buf, uint64(len(b.Lo)))
	for i := range b.Lo {
		// Write through explicit indexing: writing the slices directly would
		// include spare capacity of the backing arrays and make the encoding
		// depend on how the box was constructed rather than only on its bounds.
		writeVarint(buf, b.Lo[i])
		writeVarint(buf, b.Hi[i])
	}
}

func readBox(r *bytes.Reader) (Box, error) {
	header, err := r.ReadByte()
	if err != nil || header != markerBox {
		return Box{}, ErrInvalidToken
	}
	dimU, err := binary.ReadUvarint(r)
	if err != nil || dimU == 0 || dimU > maxDecodedDimensions {
		return Box{}, ErrInvalidToken
	}
	dim := int(dimU)
	b := Box{Lo: make([]int64, dim), Hi: make([]int64, dim)}
	for i := 0; i < dim; i++ {
		lo, err := binary.ReadVarint(r)
		if err != nil {
			return Box{}, err
		}
		hi, err := binary.ReadVarint(r)
		if err != nil {
			return Box{}, err
		}
		if lo > hi {
			return Box{}, ErrInvalidToken
		}
		b.Lo[i] = lo
		b.Hi[i] = hi
	}
	return b, nil
}

func writeUvarint(buf *bytes.Buffer, v uint64) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(scratch[:], v)
	buf.Write(scratch[:n])
}

func writeVarint(buf *bytes.Buffer, v int64) {
	var scratch [binary.MaxVarintLen64]byte
	n := binary.PutVarint(scratch[:], v)
	buf.Write(scratch[:n])
}
