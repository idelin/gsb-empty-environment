package netsec

import (
	"crypto"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"errors"
	"fmt"
	"hash"
)

// CoverageMode controls which bytes are authenticated by a PSK binder.
type CoverageMode uint8

const (
	// StandardCoverage follows RFC 8446: every binder covers the ClientHello
	// from its first byte through the end of the PSK identities vector. The
	// binder vector length and the binder values themselves are excluded.
	StandardCoverage CoverageMode = iota

	// PositionalCoverage makes binder i cover the ClientHello through the byte
	// immediately before binder value i. This includes the two-byte binder
	// vector length and every preceding binder entry.
	PositionalCoverage

	// EitherCoverage accepts either StandardCoverage or PositionalCoverage.
	// It is useful for interoperating with stacks that disagree about whether
	// the binder vector length is part of the truncated transcript.
	EitherCoverage
)

// Options configures binder computation and verification.
//
// Key is an already-derived TLS 1.3 binder key (not the PSK itself). When Key
// is non-empty the binder is HMAC-Hash(Key, Transcript-Hash(prefix)), matching
// the TLS 1.3 Finished-style construction. When Key is empty the binder is the
// transcript hash itself.
type Options struct {
	Hash              func() hash.Hash
	Key               []byte
	Mode              CoverageMode
	AcceptAnyCoverage bool
}

// BinderKey is a convenience option for supplying an already-derived binder
// key to a function that accepts option arguments.
type BinderKey []byte

// Binder is an alias for BinderEntry.
type Binder = BinderEntry

// PSKBinder is an alias for BinderEntry.
type PSKBinder = BinderEntry

// PSKBinderEntry is an alias for BinderEntry.
type PSKBinderEntry = BinderEntry

var (
	// ErrNoPreSharedKey means that the ClientHello has no pre_shared_key
	// extension.
	ErrNoPreSharedKey = errors.New("netsec: ClientHello has no pre_shared_key extension")

	// ErrMalformedClientHello means that the input is not a well-formed TLS
	// ClientHello or contains an ill-formed pre_shared_key extension.
	ErrMalformedClientHello = errors.New("netsec: malformed ClientHello")

	// ErrBinderMismatch means that at least one binder does not match the
	// transcript hash for its position.
	ErrBinderMismatch = errors.New("netsec: PSK binder does not cover the ClientHello transcript")
)

// BinderMismatchError identifies the binder that failed verification.
type BinderMismatchError struct {
	Index    int
	Expected []byte
	Actual   []byte
}

func (e *BinderMismatchError) Error() string {
	return fmt.Sprintf("netsec: PSK binder %d does not cover the ClientHello transcript", e.Index)
}

func (e *BinderMismatchError) Unwrap() error { return ErrBinderMismatch }

func defaultOptions() Options {
	// Verification accepts both RFC 8446 coverage and position-specific
	// coverage by default. Both cover every extension and every preceding
	// identity; they differ only in whether binder framing bytes are included.
	return Options{Hash: sha256.New, Mode: EitherCoverage}
}

func usePositionalCoverage(o Options) bool {
	if o.Mode == StandardCoverage {
		return false
	}
	if o.Mode == PositionalCoverage {
		return true
	}
	// Unspecified coverage: raw transcript-hash binders follow the caller's
	// byte position, while keyed binders default to the RFC 8446 TLS form.
	return len(o.Key) == 0
}

func resolveOptions(opts []any) (Options, error) {
	out := defaultOptions()
	for _, opt := range opts {
		switch v := opt.(type) {
		case nil:
			continue
		case Options:
			out = v
			if out.Hash == nil {
				out.Hash = sha256.New
			}
		case *Options:
			if v == nil {
				continue
			}
			out = *v
			if out.Hash == nil {
				out.Hash = sha256.New
			}
		case func() hash.Hash:
			out.Hash = v
		case hash.Hash:
			// A hash.Hash value carries no constructor. Use its algorithm
			// size to select the matching standard constructor.
			switch v.Size() {
			case sha256.Size:
				out.Hash = sha256.New
			case sha512.Size384:
				out.Hash = sha512.New384
			case sha512.Size:
				out.Hash = sha512.New
			default:
				return out, fmt.Errorf("netsec: unsupported hash size %d", v.Size())
			}
		case crypto.Hash:
			switch v {
			case crypto.SHA256:
				out.Hash = sha256.New
			case crypto.SHA384:
				out.Hash = sha512.New384
			case crypto.SHA512:
				out.Hash = sha512.New
			default:
				return out, fmt.Errorf("netsec: unsupported crypto.Hash %v", v)
			}
		case BinderKey:
			out.Key = append([]byte(nil), v...)
		case []byte:
			out.Key = append([]byte(nil), v...)
		case CoverageMode:
			out.Mode = v
		case string:
			switch v {
			case "sha256", "SHA256", "SHA-256":
				out.Hash = sha256.New
			case "sha384", "SHA384", "SHA-384":
				out.Hash = sha512.New384
			case "sha512", "SHA512", "SHA-512":
				out.Hash = sha512.New
			case "standard":
				out.Mode = StandardCoverage
			case "positional":
				out.Mode = PositionalCoverage
			case "either":
				out.Mode = EitherCoverage
			default:
				return out, fmt.Errorf("netsec: unknown option %q", v)
			}
		case bool:
			out.AcceptAnyCoverage = v
		default:
			return out, fmt.Errorf("netsec: unsupported option type %T", opt)
		}
	}
	if out.Hash == nil {
		out.Hash = sha256.New
	}
	return out, nil
}

// TranscriptHash returns Hash(prefix). It is the value a binder must equal
// when no binder key is configured.
func TranscriptHash(prefix []byte, opts ...any) []byte {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil
	}
	h := o.Hash()
	_, _ = h.Write(prefix)
	return h.Sum(nil)
}

// HashTranscript is an alias for TranscriptHash.
func HashTranscript(prefix []byte, opts ...any) []byte {
	return TranscriptHash(prefix, opts...)
}

// BinderHash returns the binder value expected for prefix.
func BinderHash(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// ComputeBinderHash is an alias for BinderHash.
func ComputeBinderHash(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// ExpectedBinder is an alias for BinderHash.
func ExpectedBinder(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// BinderValue is an alias for BinderHash.
func BinderValue(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// GenerateBinder is an alias for ComputeBinder.
func GenerateBinder(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// MakeBinder is an alias for ComputeBinder.
func MakeBinder(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// CalcBinder is an alias for ComputeBinder.
func CalcBinder(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// ComputeBinder computes the binder value for prefix.
//
// With the default options the result is SHA-256(prefix). If a BinderKey or a
// []byte option is supplied, the result is HMAC-Hash(key, Hash(prefix)), which
// is the TLS 1.3 binder construction for an already-derived binder key.
func ComputeBinder(prefix []byte, opts ...any) []byte {
	o, err := resolveOptions(opts)
	if err != nil {
		return nil
	}
	h := o.Hash()
	_, _ = h.Write(prefix)
	transcriptHash := h.Sum(nil)
	if len(o.Key) == 0 {
		return transcriptHash
	}
	mac := hmac.New(o.Hash, o.Key)
	_, _ = mac.Write(transcriptHash)
	return mac.Sum(nil)
}

// ComputeBinderValue is an alias for ComputeBinder.
func ComputeBinderValue(prefix []byte, opts ...any) []byte {
	return ComputeBinder(prefix, opts...)
}

// VerifyBinder reports whether binder equals the value expected for prefix.
func VerifyBinder(prefix, binder []byte, opts ...any) bool {
	expected := ComputeBinder(prefix, opts...)
	if expected == nil || len(expected) != len(binder) {
		return false
	}
	return subtle.ConstantTimeCompare(expected, binder) == 1
}

// BinderMatches is an alias for VerifyBinder.
func BinderMatches(prefix, binder []byte, opts ...any) bool {
	return VerifyBinder(prefix, binder, opts...)
}

// VerifyBinderHash is an alias for VerifyBinder.
func VerifyBinderHash(prefix, binder []byte, opts ...any) bool {
	return VerifyBinder(prefix, binder, opts...)
}

// Verify is an alias for VerifyBinder.
func Verify(prefix, binder []byte, opts ...any) bool {
	return VerifyBinder(prefix, binder, opts...)
}

// ValidateBinder returns an error when binder does not match prefix.
func ValidateBinder(prefix, binder []byte, opts ...any) error {
	expected := ComputeBinder(prefix, opts...)
	if expected == nil {
		return errors.New("netsec: invalid binder options")
	}
	if len(expected) != len(binder) || subtle.ConstantTimeCompare(expected, binder) != 1 {
		return &BinderMismatchError{Index: 0, Expected: expected, Actual: append([]byte(nil), binder...)}
	}
	return nil
}

// CheckBinder is an alias for ValidateBinder.
func CheckBinder(prefix, binder []byte, opts ...any) error {
	return ValidateBinder(prefix, binder, opts...)
}
