package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"math/big"
	"sort"
)

const (
	// MinRosterSize is the smallest supported member set.
	MinRosterSize = 2
	// MaxRosterSize is the largest supported member set.
	MaxRosterSize = 32

	// ScalarSize is the encoded size of scalars modulo the P-256 group order.
	ScalarSize = 32
	// CompressedPointSize is the encoded size of a compressed P-256 point.
	CompressedPointSize = 33

	// DomainTagCoefficient domain-separates MuSig-style key coefficients.
	DomainTagCoefficient = "netsec/q0046/schnorr/v1/coefficient"
	// DomainTagCommitment domain-separates nonce commitments.
	DomainTagCommitment = "netsec/q0046/schnorr/v1/nonce-commitment"
	// DomainTagChallenge domain-separates the Fiat-Shamir challenge.
	DomainTagChallenge = "netsec/q0046/schnorr/v1/challenge"
	// DomainTagNonceRecord domain-separates persistence record identifiers.
	DomainTagNonceRecord = "netsec/q0046/schnorr/v1/nonce-record"
)

var (
	p256Curve = elliptic.P256()
	groupN    = p256Curve.Params().N
)

var (
	// ErrRosterSize is returned when a roster has fewer than 2 or more than 32
	// members.
	ErrRosterSize = errors.New("netsec: roster must contain between 2 and 32 members")
	// ErrInvalidPublicKey is returned for a public key that is not a valid,
	// finite P-256 point.
	ErrInvalidPublicKey = errors.New("netsec: invalid P-256 public key")
	// ErrDuplicatePublicKey is returned when a roster contains the same
	// compressed public key more than once.
	ErrDuplicatePublicKey = errors.New("netsec: duplicate public key in roster")
	// ErrZeroAggregatePublicKey is returned when the coefficient-weighted
	// roster public key is the point at infinity.
	ErrZeroAggregatePublicKey = errors.New("netsec: zero aggregate public key")
	// ErrZeroAggregateNonce is returned when the sum of nonce points is the
	// point at infinity.
	ErrZeroAggregateNonce = errors.New("netsec: zero aggregate nonce")
	// ErrInvalidPoint is returned for malformed or infinite point encodings.
	ErrInvalidPoint = errors.New("netsec: invalid point encoding")
	// ErrInvalidCommitment is returned for a malformed nonce commitment.
	ErrInvalidCommitment = errors.New("netsec: invalid nonce commitment")
	// ErrCommitmentMismatch is returned when a revealed nonce does not match
	// the commitment that was received earlier.
	ErrCommitmentMismatch = errors.New("netsec: nonce commitment mismatch")
	// ErrInvalidPartial is returned when a partial signature equation fails.
	ErrInvalidPartial = errors.New("netsec: invalid partial signature")
	// ErrInvalidSignature is returned when the final aggregate signature does
	// not verify.
	ErrInvalidSignature = errors.New("netsec: invalid aggregate signature")
	// ErrUnknownSigner is returned when a message names a public key that is
	// not in the session roster.
	ErrUnknownSigner = errors.New("netsec: signer is not in roster")
	// ErrSessionPhase is returned when an event is not valid in the current
	// protocol phase.
	ErrSessionPhase = errors.New("netsec: operation is not valid in the current session phase")
	// ErrSessionAborted is returned after a session has terminated.
	ErrSessionAborted = errors.New("netsec: session is aborted")
	// ErrSessionComplete is returned when an operation requires a non-final
	// session state but the signature is already complete.
	ErrSessionComplete = errors.New("netsec: session is already complete")
	// ErrConflictingSubmission is returned when a signer retries an event with
	// different data.
	ErrConflictingSubmission = errors.New("netsec: conflicting duplicate submission")
)

// point is an affine P-256 point. The point at infinity is represented as
// (0, 0), which is also the convention used by crypto/elliptic's affine API.
type point struct {
	x *big.Int
	y *big.Int
}

func infinity() point {
	return point{x: new(big.Int), y: new(big.Int)}
}

func (p point) isInfinity() bool {
	return p.x == nil || p.y == nil || (p.x.Sign() == 0 && p.y.Sign() == 0)
}

func (p point) equal(q point) bool {
	return p.x.Cmp(q.x) == 0 && p.y.Cmp(q.y) == 0
}

func (p point) compressed() []byte {
	if p.isInfinity() {
		return []byte{0}
	}
	return elliptic.MarshalCompressed(p256Curve, p.x, p.y)
}

func addPoints(a, b point) point {
	x, y := p256Curve.Add(a.x, a.y, b.x, b.y)
	return point{x: x, y: y}
}

func scalarMultPoint(p point, scalar *big.Int) point {
	if scalar.Sign() == 0 || p.isInfinity() {
		return infinity()
	}
	x, y := p256Curve.ScalarMult(p.x, p.y, scalar.Bytes())
	return point{x: x, y: y}
}

func scalarBaseMult(scalar *big.Int) point {
	if scalar.Sign() == 0 {
		return infinity()
	}
	x, y := p256Curve.ScalarBaseMult(scalar.Bytes())
	return point{x: x, y: y}
}

func parseCompressedPoint(data []byte) (point, error) {
	if len(data) != CompressedPointSize || (data[0] != 2 && data[0] != 3) {
		return point{}, ErrInvalidPoint
	}
	x, y := elliptic.UnmarshalCompressed(p256Curve, data)
	if x == nil || y == nil {
		return point{}, ErrInvalidPoint
	}
	p := point{x: x, y: y}
	if p.isInfinity() {
		return point{}, ErrInvalidPoint
	}
	return p, nil
}

func parseScalar(data []byte) (*big.Int, error) {
	if len(data) != ScalarSize {
		return nil, ErrInvalidPartial
	}
	v := new(big.Int).SetBytes(data)
	if v.Cmp(groupN) >= 0 {
		return nil, ErrInvalidPartial
	}
	return v, nil
}

func scalarBytes(v *big.Int) []byte {
	out := make([]byte, ScalarSize)
	v.FillBytes(out)
	return out
}

// writeField writes one length-prefixed hash field. The length is an unsigned
// 64-bit big-endian integer, as required by the protocol.
func writeField(h hash.Hash, field []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(field)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(field)
}

func digestFields(domain []byte, fields ...[]byte) []byte {
	h := sha256.New()
	writeField(h, domain)
	for _, field := range fields {
		writeField(h, field)
	}
	return h.Sum(nil)
}

// CompressedPublicKey returns the canonical 33-byte compressed encoding of a
// P-256 public key.
func CompressedPublicKey(pub *ecdsa.PublicKey) ([]byte, error) {
	if pub == nil || pub.X == nil || pub.Y == nil {
		return nil, ErrInvalidPublicKey
	}
	if !p256Curve.IsOnCurve(pub.X, pub.Y) {
		return nil, ErrInvalidPublicKey
	}
	return elliptic.MarshalCompressed(p256Curve, pub.X, pub.Y), nil
}

// ParsePublicKey parses a compressed P-256 public key.
func ParsePublicKey(data []byte) (*ecdsa.PublicKey, error) {
	p, err := parseCompressedPoint(data)
	if err != nil {
		return nil, ErrInvalidPublicKey
	}
	return &ecdsa.PublicKey{Curve: p256Curve, X: new(big.Int).Set(p.x), Y: new(big.Int).Set(p.y)}, nil
}

// Roster is a canonical, sorted member set and its derived MuSig coefficients.
// The zero value is not usable; construct a roster with NewRoster or
// ParseRoster.
type Roster struct {
	keys         []*ecdsa.PublicKey
	encodedKeys  [][]byte
	coefficients []*big.Int
	encoded      []byte
	aggregate    point
	index        map[string]int
}

type rosterEntry struct {
	key []byte
	pub *ecdsa.PublicKey
}

// NewRoster canonicalizes a member set. Keys are sorted by compressed
// encoding, duplicates are rejected, and the coefficient-weighted aggregate
// public key is computed immediately.
func NewRoster(keys []*ecdsa.PublicKey) (*Roster, error) {
	if len(keys) < MinRosterSize || len(keys) > MaxRosterSize {
		return nil, ErrRosterSize
	}

	entries := make([]rosterEntry, 0, len(keys))
	seen := make(map[string]struct{}, len(keys))
	for _, pub := range keys {
		encoded, err := CompressedPublicKey(pub)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[string(encoded)]; ok {
			return nil, ErrDuplicatePublicKey
		}
		seen[string(encoded)] = struct{}{}
		entries = append(entries, rosterEntry{
			key: encoded,
			pub: &ecdsa.PublicKey{Curve: p256Curve, X: new(big.Int).Set(pub.X), Y: new(big.Int).Set(pub.Y)},
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].key, entries[j].key) < 0
	})

	return makeRoster(entries)
}

// ParseRoster constructs a roster from compressed public keys.
func ParseRoster(compressedKeys [][]byte) (*Roster, error) {
	if len(compressedKeys) < MinRosterSize || len(compressedKeys) > MaxRosterSize {
		return nil, ErrRosterSize
	}

	entries := make([]rosterEntry, 0, len(compressedKeys))
	seen := make(map[string]struct{}, len(compressedKeys))
	for _, encoded := range compressedKeys {
		pub, err := ParsePublicKey(encoded)
		if err != nil {
			return nil, err
		}
		canonical, err := CompressedPublicKey(pub)
		if err != nil {
			return nil, err
		}
		if _, ok := seen[string(canonical)]; ok {
			return nil, ErrDuplicatePublicKey
		}
		seen[string(canonical)] = struct{}{}
		entries = append(entries, rosterEntry{key: canonical, pub: pub})
	}
	sort.Slice(entries, func(i, j int) bool {
		return bytes.Compare(entries[i].key, entries[j].key) < 0
	})

	return makeRoster(entries)
}

func makeRoster(entries []rosterEntry) (*Roster, error) {
	keys := make([]*ecdsa.PublicKey, len(entries))
	encodedKeys := make([][]byte, len(entries))
	index := make(map[string]int, len(entries))
	for i, entry := range entries {
		keys[i] = entry.pub
		encodedKeys[i] = append([]byte(nil), entry.key...)
		index[string(entry.key)] = i
	}

	encoded := encodeRoster(encodedKeys)
	coefficients := make([]*big.Int, len(encodedKeys))
	aggregate := infinity()
	for i, key := range encodedKeys {
		coefficient := deriveCoefficient(encoded, key)
		coefficients[i] = coefficient
		aggregate = addPoints(aggregate, scalarMultPoint(point{x: keys[i].X, y: keys[i].Y}, coefficient))
	}
	if aggregate.isInfinity() {
		return nil, ErrZeroAggregatePublicKey
	}

	return &Roster{
		keys:         keys,
		encodedKeys:  encodedKeys,
		coefficients: coefficients,
		encoded:      encoded,
		aggregate:    aggregate,
		index:        index,
	}, nil
}

func encodeRoster(keys [][]byte) []byte {
	var buf bytes.Buffer
	for _, key := range keys {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(key)))
		buf.Write(length[:])
		buf.Write(key)
	}
	return buf.Bytes()
}

// deriveCoefficient implements
//
//	SHA256(domain || roster || publicKey) mod N
//
// with a length-prefixed retry counter appended only if the first digest maps
// to zero.
func deriveCoefficient(roster, publicKey []byte) *big.Int {
	for counter := uint64(0); ; counter++ {
		h := sha256.New()
		writeField(h, []byte(DomainTagCoefficient))
		writeField(h, roster)
		writeField(h, publicKey)
		if counter > 0 {
			var encodedCounter [8]byte
			binary.BigEndian.PutUint64(encodedCounter[:], counter)
			writeField(h, encodedCounter[:])
		}
		coefficient := new(big.Int).SetBytes(h.Sum(nil))
		coefficient.Mod(coefficient, groupN)
		if coefficient.Sign() != 0 {
			return coefficient
		}
	}
}

// Size returns the number of roster members.
func (r *Roster) Size() int {
	if r == nil {
		return 0
	}
	return len(r.keys)
}

// Bytes returns the canonical full-roster encoding used as a hash field.
func (r *Roster) Bytes() []byte {
	if r == nil {
		return nil
	}
	return append([]byte(nil), r.encoded...)
}

// EncodedPublicKeys returns the sorted compressed member keys.
func (r *Roster) EncodedPublicKeys() [][]byte {
	if r == nil {
		return nil
	}
	out := make([][]byte, len(r.encodedKeys))
	for i, key := range r.encodedKeys {
		out[i] = append([]byte(nil), key...)
	}
	return out
}

// PublicKeys returns copies of the sorted member public keys.
func (r *Roster) PublicKeys() []*ecdsa.PublicKey {
	if r == nil {
		return nil
	}
	out := make([]*ecdsa.PublicKey, len(r.keys))
	for i, key := range r.keys {
		out[i] = &ecdsa.PublicKey{
			Curve: p256Curve,
			X:     new(big.Int).Set(key.X),
			Y:     new(big.Int).Set(key.Y),
		}
	}
	return out
}

// Contains reports whether the compressed public key belongs to the roster.
func (r *Roster) Contains(compressed []byte) bool {
	if r == nil {
		return false
	}
	_, ok := r.index[string(compressed)]
	return ok
}

// Coefficient returns the coefficient for a compressed roster public key.
func (r *Roster) Coefficient(compressed []byte) (*big.Int, bool) {
	if r == nil {
		return nil, false
	}
	index, ok := r.index[string(compressed)]
	if !ok {
		return nil, false
	}
	return new(big.Int).Set(r.coefficients[index]), true
}

// AggregatePublicKey returns a copy of P = sum(a_i * P_i).
func (r *Roster) AggregatePublicKey() *ecdsa.PublicKey {
	if r == nil || r.aggregate.isInfinity() {
		return nil
	}
	return &ecdsa.PublicKey{
		Curve: p256Curve,
		X:     new(big.Int).Set(r.aggregate.x),
		Y:     new(big.Int).Set(r.aggregate.y),
	}
}

// AggregatePublicKeyBytes returns the compressed encoding of P.
func (r *Roster) AggregatePublicKeyBytes() []byte {
	if r == nil || r.aggregate.isInfinity() {
		return nil
	}
	return r.aggregate.compressed()
}

// CommitmentHash computes SHA256(sessionID || signerPublicKey || R_i), with
// every hash field length-prefixed.
func CommitmentHash(sessionID, signerPublicKey, noncePoint []byte) []byte {
	return digestFields([]byte(DomainTagCommitment), sessionID, signerPublicKey, noncePoint)
}

// ComputeChallenge computes e = H(sessionID || roster || message || P || R)
// modulo the P-256 group order.
func ComputeChallenge(sessionID, message []byte, roster *Roster, aggregateR []byte) ([]byte, error) {
	if roster == nil || roster.aggregate.isInfinity() {
		return nil, ErrZeroAggregatePublicKey
	}
	rPoint, err := parseCompressedPoint(aggregateR)
	if err != nil {
		return nil, ErrInvalidPoint
	}
	if rPoint.isInfinity() {
		return nil, ErrZeroAggregateNonce
	}
	digest := digestFields(
		[]byte(DomainTagChallenge),
		sessionID,
		roster.encoded,
		message,
		roster.aggregate.compressed(),
		rPoint.compressed(),
	)
	challenge := new(big.Int).SetBytes(digest)
	challenge.Mod(challenge, groupN)
	return scalarBytes(challenge), nil
}

// Signature is the final Schnorr signature (R, e, z).
type Signature struct {
	R []byte
	E []byte
	Z []byte
}

// Clone returns a deep copy of the signature.
func (s *Signature) Clone() *Signature {
	if s == nil {
		return nil
	}
	return &Signature{
		R: append([]byte(nil), s.R...),
		E: append([]byte(nil), s.E...),
		Z: append([]byte(nil), s.Z...),
	}
}

// VerifySignature verifies zG = R + eP and recomputes the challenge binding.
func VerifySignature(sig *Signature, sessionID, message []byte, roster *Roster) error {
	if sig == nil {
		return ErrInvalidSignature
	}
	if roster == nil || roster.aggregate.isInfinity() {
		return ErrZeroAggregatePublicKey
	}
	rPoint, err := parseCompressedPoint(sig.R)
	if err != nil {
		return ErrInvalidSignature
	}
	e, err := parseScalar(sig.E)
	if err != nil {
		return ErrInvalidSignature
	}
	z, err := parseScalar(sig.Z)
	if err != nil {
		return ErrInvalidSignature
	}
	expectedChallenge, err := ComputeChallenge(sessionID, message, roster, sig.R)
	if err != nil || !bytes.Equal(expectedChallenge, sig.E) {
		return ErrInvalidSignature
	}

	left := scalarBaseMult(z)
	right := addPoints(rPoint, scalarMultPoint(roster.aggregate, e))
	if !left.equal(right) {
		return ErrInvalidSignature
	}
	return nil
}

func validatePrivateKey(privateKey *ecdsa.PrivateKey) error {
	if privateKey == nil || privateKey.D == nil || privateKey.D.Sign() <= 0 || privateKey.D.Cmp(groupN) >= 0 {
		return fmt.Errorf("netsec: invalid P-256 private scalar")
	}
	if privateKey.X == nil || privateKey.Y == nil || !p256Curve.IsOnCurve(privateKey.X, privateKey.Y) {
		return ErrInvalidPublicKey
	}
	derived := scalarBaseMult(privateKey.D)
	if derived.isInfinity() || derived.x.Cmp(privateKey.X) != 0 || derived.y.Cmp(privateKey.Y) != 0 {
		return fmt.Errorf("netsec: private key does not match its public key")
	}
	return nil
}
