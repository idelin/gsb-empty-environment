package netsec

import (
	"crypto"
	"crypto/hkdf"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"errors"
	"fmt"
	"hash"
	"reflect"
	"strings"
)

// PSKType identifies the origin of a TLS 1.3 pre-shared key. The type is part
// of the binder key derivation: external and resumption PSKs must never share
// a binder key, even when they have the same key bytes.
type PSKType uint8

const (
	// PSKExternal is a PSK provisioned outside TLS. Its binder key uses the
	// "ext binder" derivation label (RFC 8446, Section 4.2.11.2).
	PSKExternal PSKType = iota
	// PSKResumption is a PSK produced by a previous TLS session. Its binder
	// key uses the "res binder" derivation label.
	PSKResumption
	// PSKImported is an external PSK imported with RFC 9258. Its binder key
	// uses the "imp binder" derivation label.
	PSKImported
)

const (
	// ExternalBinderLabel is the binder key label for a non-imported external
	// PSK.
	ExternalBinderLabel = "ext binder"
	// ResumptionBinderLabel is the binder key label for a resumption PSK.
	ResumptionBinderLabel = "res binder"
	// ImportedBinderLabel is the binder key label for an imported external
	// PSK (RFC 9258).
	ImportedBinderLabel = "imp binder"

	// Short aliases used by some TLS implementations.
	ExtBinderLabel = ExternalBinderLabel
	ResBinderLabel = ResumptionBinderLabel
	ImpBinderLabel = ImportedBinderLabel
)

// Compatibility aliases for callers that use "class" or "kind" terminology.
type PSKClass = PSKType
type PSKKind = PSKType
type BinderType = PSKType
type PSKOrigin = PSKType
type BinderKeyType = PSKType

const (
	ExternalPSK       = PSKExternal
	ResumptionPSK     = PSKResumption
	ImportedPSK       = PSKImported
	PSKTypeExternal   = PSKExternal
	PSKTypeResumption = PSKResumption
	PSKTypeImported   = PSKImported
	ExternalPSKKind   = PSKExternal
	ResumptionPSKKind = PSKResumption
	ImportedPSKKind   = PSKImported
	External          = PSKExternal
	Resumption        = PSKResumption
	Imported          = PSKImported
	PSKTypeExt        = PSKExternal
	PSKTypeRes        = PSKResumption
	PSKTypeImp        = PSKImported
)

var (
	// ErrInvalidPSKType is returned when a PSK type is not one of the values
	// defined by this package.
	ErrInvalidPSKType = errors.New("netsec: invalid PSK type")
	// ErrInvalidHash is returned when a hash constructor cannot be resolved.
	ErrInvalidHash = errors.New("netsec: invalid hash function")
	// ErrBinderMismatch is returned when a PSK binder does not verify.
	ErrBinderMismatch = errors.New("netsec: PSK binder mismatch")
	// ErrPSKTypeMismatch is an alias for ErrBinderMismatch, provided for
	// callers that describe the failure as a PSK class confusion.
	ErrPSKTypeMismatch = ErrBinderMismatch
	// ErrInvalidBinder is an alias for ErrBinderMismatch.
	ErrInvalidBinder = ErrBinderMismatch
)

// Label returns the RFC 8446 binder key label for t.
func (t PSKType) Label() string {
	switch t {
	case PSKExternal:
		return ExternalBinderLabel
	case PSKResumption:
		return ResumptionBinderLabel
	case PSKImported:
		return ImportedBinderLabel
	default:
		return ""
	}
}

// BinderLabel is an alias for Label.
func (t PSKType) BinderLabel() string { return t.Label() }

// Valid reports whether t is a supported PSK type.
func (t PSKType) Valid() bool { return t.Label() != "" }

// IsValid is an alias for Valid.
func (t PSKType) IsValid() bool { return t.Valid() }

func (t PSKType) String() string {
	switch t {
	case PSKExternal:
		return "external"
	case PSKResumption:
		return "resumption"
	case PSKImported:
		return "imported"
	default:
		return fmt.Sprintf("PSKType(%d)", uint8(t))
	}
}

// HashFunc is the constructor form used by crypto/tls and the crypto/hash
// package, for example sha256.New or sha512.New384.
type HashFunc = func() hash.Hash

// PSK is a convenience representation of a TLS 1.3 PSK. The identity is not
// used by the key schedule, but is carried here so callers can keep the wire
// identity and the key together.
type PSK struct {
	Key      []byte
	Identity []byte
	Type     PSKType
}

// NewPSK constructs a PSK. If no type is supplied, the PSK is treated as an
// external PSK.
func NewPSK(key, identity []byte, typ ...PSKType) PSK {
	t := PSKExternal
	if len(typ) > 0 {
		t = typ[0]
	}
	return PSK{Key: key, Identity: identity, Type: t}
}

func resolvePSKType(v any) (PSKType, bool) {
	switch t := v.(type) {
	case PSKType:
		return t, true
	case bool:
		if t {
			return PSKExternal, true
		}
		return PSKResumption, true
	case string:
		s := strings.ToLower(strings.TrimSpace(t))
		s = strings.ReplaceAll(s, "_", " ")
		s = strings.ReplaceAll(s, "-", " ")
		switch s {
		case "external", "ext", "external psk", "ext psk", "ext binder":
			return PSKExternal, true
		case "resumption", "resume", "res", "resumption psk", "res psk", "res binder":
			return PSKResumption, true
		case "imported", "import", "imp", "imported psk", "imp psk", "imp binder":
			return PSKImported, true
		}
	case int:
		return intPSKType(t)
	case int8:
		return intPSKType(int(t))
	case int16:
		return intPSKType(int(t))
	case int32:
		return intPSKType(int(t))
	case int64:
		return intPSKType(int(t))
	case uint:
		return intPSKType(int(t))
	case uint8:
		return intPSKType(int(t))
	case uint16:
		return intPSKType(int(t))
	case uint32:
		return intPSKType(int(t))
	case uint64:
		return intPSKType(int(t))
	case *PSK:
		if t != nil {
			return t.Type, true
		}
	case PSK:
		return t.Type, true
	}
	return 0, false
}

func intPSKType(v int) (PSKType, bool) {
	t := PSKType(v)
	if t.Valid() {
		return t, true
	}
	return 0, false
}

func byteSliceValue(v any) ([]byte, bool) {
	if b, ok := v.([]byte); ok {
		return b, true
	}
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Array || rv.Type().Elem().Kind() != reflect.Uint8 {
		return nil, false
	}
	out := make([]byte, rv.Len())
	for i := range out {
		out[i] = byte(rv.Index(i).Uint())
	}
	return out, true
}

func resolveHash(v any) (HashFunc, bool) {
	switch h := v.(type) {
	case nil:
		return sha256.New, true
	case func() hash.Hash:
		if h == nil {
			return nil, false
		}
		return h, true
	case crypto.Hash:
		if !h.Available() {
			return nil, false
		}
		return h.New, true
	case string:
		return hashByName(h)
	case hash.Hash:
		return hashByShape(h)
	}
	return nil, false
}

func hashByName(name string) (HashFunc, bool) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "sha256", "sha-256", "sha2-256":
		return sha256.New, true
	case "sha384", "sha-384", "sha2-384":
		return sha512.New384, true
	case "sha512", "sha-512", "sha2-512":
		return sha512.New, true
	case "sha1", "sha-1":
		return sha1.New, true
	default:
		return nil, false
	}
}

// hashByShape is used only when a caller hands us an instantiated hash rather
// than a constructor. TLS 1.3 limits the practical choices to SHA-256 and
// SHA-384, with SHA-512 accepted for interoperability tests.
func hashByShape(h hash.Hash) (HashFunc, bool) {
	switch {
	case h.Size() == sha256.Size && h.BlockSize() == sha256.BlockSize:
		return sha256.New, true
	case h.Size() == sha512.Size384 && h.BlockSize() == sha512.BlockSize:
		return sha512.New384, true
	case h.Size() == sha512.Size && h.BlockSize() == sha512.BlockSize:
		return sha512.New, true
	default:
		return nil, false
	}
}

// Extract performs HKDF-Extract with the selected hash. A nil salt is encoded
// as Hash.length zero bytes, as required by RFC 8446, Section 7.1.
func Extract(secret, salt []byte, h HashFunc) ([]byte, error) {
	if h == nil {
		h = sha256.New
	}
	out, err := hkdf.Extract(h, secret, salt)
	if err != nil {
		return nil, fmt.Errorf("netsec: HKDF-Extract: %w", err)
	}
	return out, nil
}

// ExpandLabel implements HKDF-Expand-Label from RFC 8446, Section 7.1.
func ExpandLabel(secret []byte, label string, context []byte, length int, h HashFunc) ([]byte, error) {
	if h == nil {
		h = sha256.New
	}
	if length < 0 {
		return nil, fmt.Errorf("netsec: negative HKDF length")
	}
	fullLabel := "tls13 " + label
	if len(fullLabel) > 255 {
		return nil, fmt.Errorf("netsec: HKDF label too long")
	}
	if len(context) > 255 {
		return nil, fmt.Errorf("netsec: HKDF context too long")
	}
	if length > 255*h().Size() {
		return nil, fmt.Errorf("netsec: HKDF output too long")
	}
	info := make([]byte, 0, 2+1+len(fullLabel)+1+len(context))
	info = append(info, byte(length>>8), byte(length))
	info = append(info, byte(len(fullLabel)))
	info = append(info, fullLabel...)
	info = append(info, byte(len(context)))
	info = append(info, context...)
	out, err := hkdf.Expand(h, secret, string(info), length)
	if err != nil {
		return nil, fmt.Errorf("netsec: HKDF-Expand-Label: %w", err)
	}
	return out, nil
}

// DeriveSecret implements Derive-Secret for an already-hashed transcript. A
// nil transcriptHash means the empty transcript, i.e. Hash("").
func DeriveSecret(secret []byte, label string, transcriptHash []byte, h HashFunc) ([]byte, error) {
	if h == nil {
		h = sha256.New
	}
	if transcriptHash == nil {
		transcriptHash = h().Sum(nil)
	}
	return ExpandLabel(secret, label, transcriptHash, h().Size(), h)
}

// DeriveSecretFromTranscript hashes transcript before applying Derive-Secret.
func DeriveSecretFromTranscript(secret []byte, label string, transcript []byte, h HashFunc) ([]byte, error) {
	if h == nil {
		h = sha256.New
	}
	hh := h()
	_, _ = hh.Write(transcript)
	return DeriveSecret(secret, label, hh.Sum(nil), h)
}

// DeriveEarlySecret returns HKDF-Extract(0, psk).
func DeriveEarlySecret(psk []byte, h HashFunc) ([]byte, error) {
	return Extract(psk, nil, h)
}

// BinderKeyFor derives the binder key for psk and typ. The type selects the
// label, so an external PSK and a resumption PSK with identical key bytes
// produce different binder keys.
func BinderKeyFor(psk []byte, typ PSKType, h HashFunc) ([]byte, error) {
	if !typ.Valid() {
		return nil, ErrInvalidPSKType
	}
	if h == nil {
		h = sha256.New
	}
	earlySecret, err := Extract(psk, nil, h)
	if err != nil {
		return nil, err
	}
	return DeriveSecret(earlySecret, typ.Label(), nil, h)
}

// DeriveBinderKey is the compatibility entry point. It accepts the PSK, a PSK
// type (PSKType, bool, int, or a label/name string), and a hash constructor in
// any order. If no type is provided the PSK is treated as external.
func DeriveBinderKey(args ...any) []byte {
	key, err := DeriveBinderKeyChecked(args...)
	if err != nil {
		return nil
	}
	return key
}

// DeriveBinderKeyChecked is DeriveBinderKey with an error result.
func DeriveBinderKeyChecked(args ...any) ([]byte, error) {
	in, err := parseKeyArgs(args)
	if err != nil {
		return nil, err
	}
	if in.typSet {
		return BinderKeyFor(in.key, in.typ, in.hash)
	}
	return BinderKeyFor(in.key, PSKExternal, in.hash)
}

// BinderKey is an alias for DeriveBinderKey.
func BinderKey(args ...any) []byte { return DeriveBinderKey(args...) }

// ComputeBinderKey is an alias for DeriveBinderKey.
func ComputeBinderKey(args ...any) []byte { return DeriveBinderKey(args...) }

// DerivePSKBinderKey is an alias for DeriveBinderKey.
func DerivePSKBinderKey(args ...any) []byte { return DeriveBinderKey(args...) }

// ExternalBinderKey derives a binder key using the external PSK label.
func ExternalBinderKey(args ...any) []byte {
	in, err := parseKeyArgs(args)
	if err != nil {
		return nil
	}
	key, err := BinderKeyFor(in.key, PSKExternal, in.hash)
	if err != nil {
		return nil
	}
	return key
}

// ResumptionBinderKey derives a binder key using the resumption PSK label.
func ResumptionBinderKey(args ...any) []byte {
	in, err := parseKeyArgs(args)
	if err != nil {
		return nil
	}
	key, err := BinderKeyFor(in.key, PSKResumption, in.hash)
	if err != nil {
		return nil
	}
	return key
}

// ImportedBinderKey derives a binder key using the RFC 9258 imported PSK
// label.
func ImportedBinderKey(args ...any) []byte {
	in, err := parseKeyArgs(args)
	if err != nil {
		return nil
	}
	key, err := BinderKeyFor(in.key, PSKImported, in.hash)
	if err != nil {
		return nil
	}
	return key
}

// ExternalPSKBinderKey is an alias for ExternalBinderKey.
func ExternalPSKBinderKey(args ...any) []byte { return ExternalBinderKey(args...) }

// ResumptionPSKBinderKey is an alias for ResumptionBinderKey.
func ResumptionPSKBinderKey(args ...any) []byte { return ResumptionBinderKey(args...) }

// DeriveExternalBinderKey is an alias for ExternalBinderKey.
func DeriveExternalBinderKey(args ...any) []byte { return ExternalBinderKey(args...) }

// DeriveResumptionBinderKey is an alias for ResumptionBinderKey.
func DeriveResumptionBinderKey(args ...any) []byte { return ResumptionBinderKey(args...) }

// DeriveExternalPSKBinderKey is an alias for ExternalBinderKey.
func DeriveExternalPSKBinderKey(args ...any) []byte { return ExternalBinderKey(args...) }

// DeriveResumptionPSKBinderKey is an alias for ResumptionBinderKey.
func DeriveResumptionPSKBinderKey(args ...any) []byte { return ResumptionBinderKey(args...) }

// FinishedKey derives the MAC key used for a PSK binder from a binder key.
func FinishedKey(binderKey []byte, h HashFunc) ([]byte, error) {
	if h == nil {
		h = sha256.New
	}
	return ExpandLabel(binderKey, "finished", nil, h().Size(), h)
}
