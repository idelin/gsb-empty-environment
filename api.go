package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	mrand "math/rand"
	"reflect"
)

// GenerateRingSignature accepts the canonical argument order
//
//	(ring [][]byte, signerIndex int, privateKey *ecdsa.PrivateKey,
//	 message, scope []byte, rng io.Reader)
//
// and also accepts the same values in most common alternative orders. Ring
// members may be given as [][]byte, [][33]byte, []*ecdsa.PublicKey, or
// []ecdsa.PublicKey. The private key may be an *ecdsa.PrivateKey, a *big.Int,
// a 32-byte scalar, or a 32-byte scalar array.
func GenerateRingSignature(args ...any) (*Signature, error) {
	ring, index, key, scalar, message, scope, source, err := parseSignArgs(args)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return generateRingSignatureSource(ring, index, key, message, scope, source)
	}
	if scalar == nil {
		return nil, errors.New("private key is required")
	}
	return generateWithScalar(ring, index, scalar, message, scope, source)
}

// Sign is an alias for GenerateRingSignature.
func Sign(args ...any) (*Signature, error) {
	return GenerateRingSignature(args...)
}

// Generate is an alias for GenerateRingSignature.
func Generate(args ...any) (*Signature, error) {
	return GenerateRingSignature(args...)
}

// GenerateSignature is an alias for GenerateRingSignature.
func GenerateSignature(args ...any) (*Signature, error) {
	return GenerateRingSignature(args...)
}

// SignRing is an alias for GenerateRingSignature.
func SignRing(args ...any) (*Signature, error) {
	return GenerateRingSignature(args...)
}

// NewRingSignature is an alias for GenerateRingSignature.
func NewRingSignature(args ...any) (*Signature, error) {
	return GenerateRingSignature(args...)
}

// CreateRingSignature is an alias for GenerateRingSignature.
func CreateRingSignature(args ...any) (*Signature, error) {
	return GenerateRingSignature(args...)
}

// VerifySignature accepts the canonical order
//
//	(ring [][]byte, message, scope []byte, sig *Signature)
//
// and also accepts the same values in other common orders.
func VerifySignature(args ...any) (bool, error) {
	ring, message, scope, sig, err := parseVerifyArgs(args)
	if err != nil {
		return false, err
	}
	return verifyRingSignature(ring, message, scope, sig)
}

// Verify is an alias for VerifySignature.
func Verify(args ...any) (bool, error) {
	return VerifySignature(args...)
}

// VerifyRingSignature is an alias for VerifySignature.
func VerifyRingSignature(args ...any) (bool, error) {
	return VerifySignature(args...)
}

// IsValidSignature is an alias for VerifySignature.
func IsValidSignature(args ...any) (bool, error) {
	return VerifySignature(args...)
}

// VerifyOK reports verification success and discards the error.
func VerifyOK(args ...any) bool {
	ok, _ := VerifySignature(args...)
	return ok
}

// IsValid reports verification success and discards the error.
func IsValid(args ...any) bool {
	return VerifyOK(args...)
}

// CheckSignature returns nil only when the signature verifies; otherwise it
// returns the format error or ErrInvalidSignature.
func CheckSignature(args ...any) error {
	ok, err := VerifySignature(args...)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidSignature
	}
	return nil
}

// Check is an alias for CheckSignature.
func Check(args ...any) error {
	return CheckSignature(args...)
}

// LinkSignatures reports whether two signatures carry the same key image.
// The arguments may be given in either order and may include extra scope or
// ring arguments; only the two signatures are used.
func LinkSignatures(args ...any) (bool, error) {
	first, second, err := parseLinkArgs(args)
	if err != nil {
		return false, err
	}
	return linkSignatures(first, second)
}

// Link is the bool-only form of LinkSignatures.
func Link(args ...any) bool {
	linked, _ := LinkSignatures(args...)
	return linked
}

// AreLinked is an alias for Link.
func AreLinked(args ...any) bool {
	return Link(args...)
}

// IsLinked is an alias for Link.
func IsLinked(args ...any) bool {
	return Link(args...)
}

// Linked is an alias for Link.
func Linked(args ...any) bool {
	return Link(args...)
}

// Linkable is an alias for Link.
func Linkable(args ...any) bool {
	return Link(args...)
}

// LinkWithError is an alias for LinkSignatures.
func LinkWithError(args ...any) (bool, error) {
	return LinkSignatures(args...)
}

// KeyImage computes the key image for a private key. The canonical order is
// (privateKey, ringMember, scope); ringMember may be nil to derive the public
// key from the private key.
func KeyImage(args ...any) ([]byte, error) {
	key, scalar, member, scope, err := parseKeyImageArgs(args)
	if err != nil {
		return nil, err
	}
	if key != nil {
		return keyImage(key, member, scope)
	}
	if scalar == nil {
		return nil, errors.New("private key is required")
	}
	if !validScalar(scalar) || scalar.Sign() == 0 {
		return nil, fmt.Errorf("%w: private key", errInvalidScalar)
	}
	if member == nil {
		member = marshalCompressed(scalarMultBase(scalar))
	}
	if parsed, err := parseCompressedPoint(member); err != nil {
		return nil, err
	} else if !pointEqual(parsed, scalarMultBase(scalar)) {
		return nil, errors.New("private key does not match ring member")
	}
	hp, err := Hp(member, scope)
	if err != nil {
		return nil, err
	}
	return marshalCompressed(scalarMult(hp, scalar)), nil
}

// ComputeKeyImage is an alias for KeyImage.
func ComputeKeyImage(args ...any) ([]byte, error) {
	return KeyImage(args...)
}

// KeyImageFor is an alias for KeyImage.
func KeyImageFor(args ...any) ([]byte, error) {
	return KeyImage(args...)
}

// GenerateKeyImage is an alias for KeyImage.
func GenerateKeyImage(args ...any) ([]byte, error) {
	return KeyImage(args...)
}

// HashToPoint is the compressed form of Hp.
func HashToPoint(publicKey, scope []byte) ([]byte, error) {
	return HpBytes(publicKey, scope)
}

// ValidateRing checks ring size, canonical encodings, and distinctness.
func ValidateRing(ring any) error {
	members, ok, err := ringFromAny(ring)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: expected a ring", errInvalidRing)
	}
	_, err = validateRing(members)
	return err
}

// Verify method form.
func (s *Signature) Verify(args ...any) (bool, error) {
	return VerifySignature(append([]any{s}, args...)...)
}

// Link method form.
func (s *Signature) Link(other *Signature) bool {
	return Link(s, other)
}

// ChallengeBytes returns a copy of C0.
func (s *Signature) ChallengeBytes() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.c0Bytes()...)
}

// KeyImageBytes returns a copy of the key image.
func (s *Signature) KeyImageBytes() []byte {
	if s == nil {
		return nil
	}
	return append([]byte(nil), s.keyImageBytes()...)
}

// ResponseBytes returns copies of the responses in ring order.
func (s *Signature) ResponseBytes() [][]byte {
	if s == nil {
		return nil
	}
	source := s.responses()
	out := make([][]byte, len(source))
	for i := range source {
		out[i] = append([]byte(nil), source[i]...)
	}
	return out
}

func parseSignArgs(args []any) ([][]byte, int, *ecdsa.PrivateKey, *big.Int, []byte, []byte, scalarSource, error) {
	var (
		ring      [][]byte
		index     = -1
		indexSet  bool
		key       *ecdsa.PrivateKey
		scalar    *big.Int
		values    [][]byte
		source    scalarSource
		sourceSet bool
		used      = make([]bool, len(args))
	)
	for i, arg := range args {
		if r, ok, err := strongRingFromAny(arg); err != nil {
			return nil, 0, nil, nil, nil, nil, nil, err
		} else if ok {
			ring = r
			used[i] = true
			break
		}
	}
	if ring == nil {
		return nil, 0, nil, nil, nil, nil, nil, fmt.Errorf("%w: missing ring", errInvalidRing)
	}

	var extraScalars [][]byte
	var nonce []byte
	for i, arg := range args {
		if used[i] {
			continue
		}
		if arg == nil {
			if indexSet && (scalar != nil || key != nil) {
				values = append(values, nil)
			}
			continue
		}
		switch v := arg.(type) {
		case *ecdsa.PrivateKey:
			key, scalar = v, v.D
		case ecdsa.PrivateKey:
			copyKey := v
			key, scalar = &copyKey, copyKey.D
		case *big.Int:
			if scalar == nil {
				scalar = v
			} else if nonce == nil && validScalar(v) {
				nonce = scalarBytes(v)
			}
		case big.Int:
			if scalar == nil {
				scalar = new(big.Int).Set(&v)
			} else if nonce == nil && validScalar(&v) {
				nonce = scalarBytes(&v)
			}
		case io.Reader:
			if !sourceSet {
				source = randomSource(v)
				sourceSet = true
			}
		case mrand.Source:
			if !sourceSet {
				source = randomSource(mrand.New(v))
				sourceSet = true
			}
		case func() ([]byte, error):
			if !sourceSet {
				source = funcScalarSource{fetch: v}
				sourceSet = true
			}
		case func() []byte:
			if !sourceSet {
				source = funcScalarSource{fetch: func() ([]byte, error) { return v(), nil }}
				sourceSet = true
			}
		case []*big.Int:
			if !sourceSet {
				source = &bigIntListSource{values: v}
				sourceSet = true
			}
		case [][]byte:
			if !sourceSet {
				extraScalars = v
				sourceSet = true
			}
		case [][32]byte:
			if !sourceSet {
				extraScalars = make([][]byte, len(v))
				for j := range v {
					extraScalars[j] = append([]byte(nil), v[j][:]...)
				}
				sourceSet = true
			}
		case []byte:
			values = append(values, v)
		case [32]byte:
			values = append(values, append([]byte(nil), v[:]...))
		case [33]byte:
			values = append(values, append([]byte(nil), v[:]...))
		case string:
			values = append(values, []byte(v))
		default:
			if idx, ok := intFromAny(arg); ok && !indexSet {
				index, indexSet = idx, true
				continue
			}
			if b, ok := bigFromAny(arg); ok {
				if scalar == nil {
					scalar = b
				} else if nonce == nil && validScalar(b) {
					nonce = scalarBytes(b)
				}
				continue
			}
			return nil, 0, nil, nil, nil, nil, nil, fmt.Errorf("unsupported sign argument %T", arg)
		}
	}

	if len(values) >= 1 && scalar == nil && key == nil && len(values[0]) == scalarSize {
		scalar, values = new(big.Int).SetBytes(values[0]), values[1:]
	}
	if scalar == nil && key == nil {
		return nil, 0, nil, nil, nil, nil, nil, errors.New("private key is required")
	}

	var message, scope []byte
	if len(values) > 0 {
		message = values[0]
	}
	if len(values) > 1 {
		scope = values[1]
	}
	if len(values) > 2 {
		for _, value := range values[2:] {
			if value != nil {
				extraScalars = append(extraScalars, value)
				sourceSet = true
			}
		}
	}

	if !sourceSet {
		source = randomSource(nil)
	} else if source == nil {
		if len(extraScalars) == 1 {
			if len(extraScalars[0]) == scalarSize {
				source = &nonceFallbackSource{nonce: extraScalars[0], fallback: randomSource(nil)}
			} else {
				source = randomSource(bytes.NewReader(extraScalars[0]))
			}
		} else {
			source = &listScalarSource{values: extraScalars}
		}
	}
	if nonce != nil {
		source = &nonceFallbackSource{nonce: nonce, fallback: source}
	}
	return ring, index, key, scalar, message, scope, source, nil
}

func parseVerifyArgs(args []any) (ring [][]byte, message, scope []byte, sig *Signature, err error) {
	var values [][]byte
	for _, arg := range args {
		if arg == nil {
			if ring != nil {
				values = append(values, nil)
			}
			continue
		}
		if r, ok, e := strongRingFromAny(arg); e != nil {
			return nil, nil, nil, nil, e
		} else if ok {
			if ring != nil {
				continue
			}
			ring = r
			continue
		}
		switch v := arg.(type) {
		case *Signature:
			sig = v
		case Signature:
			copySig := v
			sig = &copySig
		case []byte:
			values = append(values, v)
		case [32]byte:
			values = append(values, append([]byte(nil), v[:]...))
		case string:
			values = append(values, []byte(v))
		case *ecdsa.PublicKey, ecdsa.PublicKey:
			// A single public key is not a ring; report a useful error.
			return nil, nil, nil, nil, fmt.Errorf("%w: ring must contain one or more members", errInvalidRing)
		default:
			return nil, nil, nil, nil, fmt.Errorf("unsupported verify argument %T", arg)
		}
	}
	if ring == nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: missing ring", errInvalidRing)
	}
	if sig == nil {
		return nil, nil, nil, nil, errors.New("signature is required")
	}
	if len(values) > 0 {
		message = values[0]
	}
	if len(values) > 1 {
		scope = values[1]
	}
	return ring, message, scope, sig, nil
}

func parseLinkArgs(args []any) (*Signature, *Signature, error) {
	var signatures []*Signature
	var images [][]byte
	for _, arg := range args {
		switch v := arg.(type) {
		case *Signature:
			if v != nil {
				signatures = append(signatures, v)
			}
		case Signature:
			copySig := v
			signatures = append(signatures, &copySig)
		case []byte:
			if len(v) == compressedPointSize {
				if _, err := parseKeyImage(v); err == nil {
					images = append(images, v)
				}
			}
		case [33]byte:
			raw := append([]byte(nil), v[:]...)
			if _, err := parseKeyImage(raw); err == nil {
				images = append(images, raw)
			}
		case *ecdsa.PublicKey:
			if encoded, err := compressECDSAPublicKey(v); err == nil {
				images = append(images, encoded)
			}
		}
	}
	if len(signatures) < 2 && len(images) >= 2 {
		signatures = []*Signature{{I: images[0]}, {I: images[1]}}
	}
	if len(signatures) < 2 {
		return nil, nil, errors.New("two signatures are required")
	}
	return signatures[0], signatures[1], nil
}

func parseKeyImageArgs(args []any) (key *ecdsa.PrivateKey, scalar *big.Int, member, scope []byte, err error) {
	var values [][]byte
	for _, arg := range args {
		if arg == nil {
			continue
		}
		switch v := arg.(type) {
		case *ecdsa.PrivateKey:
			key, scalar = v, v.D
		case ecdsa.PrivateKey:
			copyKey := v
			key, scalar = &copyKey, copyKey.D
		case *big.Int:
			if scalar == nil {
				scalar = v
			} else {
				values = append(values, scalarBytes(v))
			}
		case big.Int:
			if scalar == nil {
				scalar = new(big.Int).Set(&v)
			} else {
				values = append(values, scalarBytes(&v))
			}
		case *ecdsa.PublicKey:
			encoded, err := compressECDSAPublicKey(v)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			values = append(values, encoded)
		case ecdsa.PublicKey:
			encoded, err := compressECDSAPublicKey(&v)
			if err != nil {
				return nil, nil, nil, nil, err
			}
			values = append(values, encoded)
		case []byte:
			values = append(values, v)
		case [32]byte:
			values = append(values, append([]byte(nil), v[:]...))
		case [33]byte:
			values = append(values, append([]byte(nil), v[:]...))
		case string:
			if decoded, e := hex.DecodeString(v); e == nil && len(decoded) == compressedPointSize {
				values = append(values, decoded)
			} else {
				values = append(values, []byte(v))
			}
		default:
			return nil, nil, nil, nil, fmt.Errorf("unsupported key-image argument %T", arg)
		}
	}
	if key != nil && len(values) == 1 && len(values[0]) == compressedPointSize {
		if parsed, parseErr := parseCompressedPoint(values[0]); parseErr == nil &&
			pointEqual(parsed, scalarMultBase(key.D)) {
			member = values[0]
		} else {
			scope = values[0]
		}
		values = nil
	}
	for _, value := range values {
		switch len(value) {
		case scalarSize:
			if scalar == nil {
				scalar = new(big.Int).SetBytes(value)
			} else {
				scope = value
			}
		case compressedPointSize:
			if member == nil {
				member = value
			} else {
				scope = value
			}
		default:
			scope = value
		}
	}
	if scalar == nil && key == nil {
		return nil, nil, nil, nil, errors.New("private key is required")
	}
	return key, scalar, member, scope, nil
}

func ringFromAny(value any) ([][]byte, bool, error) {
	if r, ok, err := strongRingFromAny(value); ok || err != nil {
		return r, ok, err
	}
	switch v := value.(type) {
	case [33]byte:
		return [][]byte{append([]byte(nil), v[:]...)}, true, nil
	case []byte:
		if len(v) == compressedPointSize {
			return [][]byte{v}, true, nil
		}
	}
	return nil, false, nil
}

func strongRingFromAny(value any) ([][]byte, bool, error) {
	switch ring := value.(type) {
	case [][]byte:
		return ring, true, nil
	case []*ecdsa.PublicKey:
		return ringFromECDSAKeys(ring)
	case []ecdsa.PublicKey:
		keys := make([]*ecdsa.PublicKey, len(ring))
		for i := range ring {
			keys[i] = &ring[i]
		}
		return ringFromECDSAKeys(keys)
	case [][33]byte:
		out := make([][]byte, len(ring))
		for i := range ring {
			out[i] = append([]byte(nil), ring[i][:]...)
		}
		return out, true, nil
	}

	rv := reflect.ValueOf(value)
	if !rv.IsValid() || (rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array) {
		return nil, false, nil
	}
	if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
		return nil, false, nil
	}
	out := make([][]byte, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		member, ok, err := memberFromAny(rv.Index(i).Interface())
		if err != nil {
			return nil, true, err
		}
		if !ok {
			return nil, false, nil
		}
		out[i] = member
	}
	return out, true, nil
}

func ringFromECDSAKeys(keys []*ecdsa.PublicKey) ([][]byte, bool, error) {
	out := make([][]byte, len(keys))
	for i, key := range keys {
		encoded, err := compressECDSAPublicKey(key)
		if err != nil {
			return nil, true, fmt.Errorf("member %d: %w", i, err)
		}
		out[i] = encoded
	}
	return out, true, nil
}

func memberFromAny(value any) ([]byte, bool, error) {
	switch v := value.(type) {
	case []byte:
		return append([]byte(nil), v...), true, nil
	case [33]byte:
		return append([]byte(nil), v[:]...), true, nil
	case *ecdsa.PublicKey:
		encoded, err := compressECDSAPublicKey(v)
		return encoded, true, err
	case ecdsa.PublicKey:
		encoded, err := compressECDSAPublicKey(&v)
		return encoded, true, err
	case string:
		decoded, err := hex.DecodeString(v)
		if err != nil {
			return nil, true, fmt.Errorf("invalid hex public key: %w", err)
		}
		return decoded, true, nil
	default:
		return nil, false, nil
	}
}

func intFromAny(value any) (int, bool) {
	if value == nil {
		return 0, false
	}
	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return int(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if uint64(int(u)) == u {
			return int(u), true
		}
	}
	return 0, false
}

func bigFromAny(value any) (*big.Int, bool) {
	switch v := value.(type) {
	case *big.Int:
		return v, v != nil
	case big.Int:
		return new(big.Int).Set(&v), true
	}
	return nil, false
}

type listScalarSource struct {
	values [][]byte
	index  int
}

func (s *listScalarSource) nextScalar(allowZero bool) (*big.Int, error) {
	for s.index < len(s.values) {
		value := s.values[s.index]
		s.index++
		if len(value) != scalarSize {
			return nil, fmt.Errorf("%w: injected scalar must be 32 bytes", errInvalidScalar)
		}
		k := new(big.Int).SetBytes(value)
		if k.Cmp(groupOrder) >= 0 {
			return nil, fmt.Errorf("%w: injected scalar is not less than the group order", errInvalidScalar)
		}
		if !allowZero && k.Sign() == 0 {
			return nil, fmt.Errorf("%w: nonce must be non-zero", errInvalidScalar)
		}
		return k, nil
	}
	return nil, errors.New("injected random scalars exhausted")
}

type bigIntListSource struct {
	values []*big.Int
	index  int
}

func (s *bigIntListSource) nextScalar(allowZero bool) (*big.Int, error) {
	for s.index < len(s.values) {
		k := s.values[s.index]
		s.index++
		if !validScalar(k) {
			return nil, fmt.Errorf("%w: injected scalar is not less than the group order", errInvalidScalar)
		}
		if !allowZero && k.Sign() == 0 {
			return nil, fmt.Errorf("%w: nonce must be non-zero", errInvalidScalar)
		}
		return new(big.Int).Set(k), nil
	}
	return nil, errors.New("injected random scalars exhausted")
}

type funcScalarSource struct {
	fetch func() ([]byte, error)
}

func (s funcScalarSource) nextScalar(allowZero bool) (*big.Int, error) {
	for attempt := 0; attempt < 1024; attempt++ {
		value, err := s.fetch()
		if err != nil {
			return nil, err
		}
		if len(value) != scalarSize {
			return nil, fmt.Errorf("%w: injected scalar must be 32 bytes", errInvalidScalar)
		}
		k := new(big.Int).SetBytes(value)
		if k.Cmp(groupOrder) >= 0 {
			return nil, fmt.Errorf("%w: injected scalar is not less than the group order", errInvalidScalar)
		}
		if !allowZero && k.Sign() == 0 {
			return nil, fmt.Errorf("%w: nonce must be non-zero", errInvalidScalar)
		}
		return k, nil
	}
	return nil, fmt.Errorf("%w: random source did not produce an acceptable scalar", errInvalidScalar)
}

type nonceFallbackSource struct {
	nonce    []byte
	used     bool
	fallback scalarSource
}

func (s *nonceFallbackSource) nextScalar(allowZero bool) (*big.Int, error) {
	if !s.used {
		s.used = true
		if len(s.nonce) == scalarSize {
			k := new(big.Int).SetBytes(s.nonce)
			if k.Cmp(groupOrder) >= 0 {
				return nil, fmt.Errorf("%w: injected scalar is not less than the group order", errInvalidScalar)
			}
			if !allowZero && k.Sign() == 0 {
				return nil, fmt.Errorf("%w: nonce must be non-zero", errInvalidScalar)
			}
			return k, nil
		}
	}
	if s.fallback == nil {
		return nil, errors.New("no random scalar source")
	}
	return s.fallback.nextScalar(allowZero)
}
