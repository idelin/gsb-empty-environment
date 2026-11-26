package apkverify

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"math"
)

const schemeV31MinSDK = 33

// Coverage is the exact APK Signature Scheme digest input: section 1, the
// complete central directory, and the EOCD normalized to point its central
// directory offset at the APK Signing Block.
type Coverage struct {
	Section1         []byte
	CentralDirectory []byte
	EOCD             []byte
}

// WriteTo writes the covered bytes in signature-scheme order.
func (c Coverage) WriteTo(w io.Writer) (int64, error) {
	var total int64
	for _, segment := range [][]byte{c.Section1, c.CentralDirectory, c.EOCD} {
		n, err := writeAll(w, segment)
		total += int64(n)
		if err != nil {
			return total, err
		}
	}
	return total, nil
}

// Bytes returns a fresh concatenation of all covered segments.
func (c Coverage) Bytes() []byte {
	size := len(c.Section1) + len(c.CentralDirectory) + len(c.EOCD)
	result := make([]byte, 0, size)
	result = append(result, c.Section1...)
	result = append(result, c.CentralDirectory...)
	result = append(result, c.EOCD...)
	return result
}

// SHA256 returns the SHA-256 digest of the covered bytes.
func (c Coverage) SHA256() [32]byte {
	hash := sha256.New()
	_, _ = c.WriteTo(hash)
	var sum [32]byte
	copy(sum[:], hash.Sum(nil))
	return sum
}

// VerificationRequest is passed to the caller-supplied signature verifier.
// The verifier must validate the signatures and content digests of every
// recognized scheme block against Coverage and reject any inconsistency.
type VerificationRequest struct {
	Archive      *Archive
	SigningBlock *SigningBlock
	Coverage     Coverage
}

// SignatureVerifier performs cryptographic signature verification. Returning
// a non-nil error rejects the APK.
type SignatureVerifier func(context.Context, VerificationRequest) error

// Options controls admission policy.
type Options struct {
	// Verifier is mandatory whenever a v2, v3, or v3.1 block is present.
	Verifier SignatureVerifier

	// MinSDKVersion is the application's minSdkVersion. When it is at least
	// API 24, a structurally valid v2 or newer block is mandatory.
	MinSDKVersion *int32

	// AllowV1Only permits an APK with no v2 or newer block only when
	// MinSDKVersion is known and below API 24.
	AllowV1Only bool
}

// Report describes an admitted APK.
type Report struct {
	Archive           *Archive
	Schemes           []Scheme
	SignatureVerified bool
	MinSDKVersion     *int32
}

// Validate parses and admits APK bytes. The verifier is invoked with the one
// and only digest coverage computed from the parsed byte slice.
func Validate(data []byte, options Options) (*Report, error) {
	return ValidateContext(context.Background(), data, options)
}

// ValidateContext is Validate with caller-controlled cancellation.
func ValidateContext(ctx context.Context, data []byte, options Options) (*Report, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if options.MinSDKVersion != nil && *options.MinSDKVersion < 0 {
		return nil, fmt.Errorf("%w: negative minSdkVersion %d", ErrSignaturePolicy, *options.MinSDKVersion)
	}

	archive, err := Parse(data)
	if err != nil {
		return nil, err
	}
	report := &Report{
		Archive:       archive,
		MinSDKVersion: options.MinSDKVersion,
	}
	if archive.SigningBlock != nil {
		for _, block := range archive.SigningBlock.Schemes {
			report.Schemes = append(report.Schemes, block.Scheme)
		}
	}

	hasV2Plus := archive.SigningBlock != nil && archive.SigningBlock.HasV2OrNewer()
	if !hasV2Plus {
		if options.MinSDKVersion != nil && *options.MinSDKVersion >= 24 {
			return nil, fmt.Errorf("%w: minSdkVersion %d requires a v2 or newer block",
				ErrV1Only, *options.MinSDKVersion)
		}
		if !options.AllowV1Only || options.MinSDKVersion == nil || *options.MinSDKVersion >= 24 {
			return nil, ErrV1Only
		}
		return report, nil
	}

	if err := validateRotationPolicy(archive.SigningBlock); err != nil {
		return nil, err
	}
	if options.Verifier == nil {
		return nil, ErrVerifierRequired
	}

	coverage := Coverage{
		Section1:         archive.Section1(),
		CentralDirectory: archive.CentralDirectory,
		EOCD:             archive.NormalizedEOCD(),
	}
	request := VerificationRequest{
		Archive:      archive,
		SigningBlock: archive.SigningBlock,
		Coverage:     coverage,
	}
	if err := options.Verifier(ctx, request); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrSignatureVerification, err)
	}
	report.SignatureVerified = true
	return report, nil
}

func validateRotationPolicy(block *SigningBlock) error {
	if block == nil {
		return nil
	}
	v3, hasV3 := block.Scheme(SchemeV3)
	v31, hasV31 := block.Scheme(SchemeV31)
	if !hasV31 {
		if hasV3 {
			for _, signer := range v3.Signers {
				if signer.HasSDKRange && signer.MaxSDKVersion < schemeV31MinSDK {
					return fmt.Errorf("%w: v3 block stops at API %d without a v3.1 fallback",
						ErrSignaturePolicy, signer.MaxSDKVersion)
				}
			}
		}
		return nil
	}
	if !hasV3 {
		return fmt.Errorf("%w: v3.1 block is present without a v3 block", ErrSignaturePolicy)
	}
	for _, signer := range v31.Signers {
		if !signer.HasSDKRange || signer.MinSDKVersion < schemeV31MinSDK {
			return fmt.Errorf("%w: v3.1 signer targets API %d before API %d",
				ErrSignaturePolicy, signer.MinSDKVersion, schemeV31MinSDK)
		}
		if signer.MaxSDKVersion == 0 {
			return fmt.Errorf("%w: v3.1 signer has an empty SDK range", ErrSignaturePolicy)
		}
	}
	return nil
}

func writeAll(w io.Writer, data []byte) (int, error) {
	written := 0
	for len(data) > 0 {
		n, err := w.Write(data)
		if n < 0 || n > len(data) {
			return written, io.ErrShortWrite
		}
		written += n
		data = data[n:]
		if err != nil {
			return written, err
		}
		if n == 0 {
			return written, io.ErrShortWrite
		}
	}
	return written, nil
}

// MaxSDKVersion is the largest value representable in an APK v3 SDK range.
const MaxSDKVersion = uint32(math.MaxUint32)
