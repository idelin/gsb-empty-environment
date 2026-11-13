package netsec

import (
	"bytes"
	"errors"
)

const (
	// DexMagic is the first four bytes of a Dalvik executable.
	DexMagic = "dex\n"

	// APKSignatureBlockMagic is the magic that terminates an APK signing block.
	APKSignatureBlockMagic = "APK Sig Block 42"

	// V2SignatureID identifies an APK Signature Scheme v2 block.
	V2SignatureID uint32 = 0x7109871a

	// V3SignatureID identifies an APK Signature Scheme v3 block.
	V3SignatureID uint32 = 0xf05368c0

	// MinSDKRequiringV2 is the first Android API level for which a v1-only
	// signature is not sufficient for admission.
	MinSDKRequiringV2 = 24

	// MinEOCDSize is the size of a ZIP end-of-central-directory record
	// without a comment.
	MinEOCDSize int64 = 22

	maxOffset int64 = int64(^uint64(0) >> 1)
)

var (
	// ErrPolymorphic reports a file that is both a DEX file and a ZIP archive.
	ErrPolymorphic = errors.New("netsec: package is a DEX/ZIP polymorphic file")

	// ErrFirstLocalHeader reports a ZIP whose first local file header does not
	// begin at offset zero.
	ErrFirstLocalHeader = errors.New("netsec: first local file header is not at offset 0")

	// ErrV2Required reports a package targeting API 24 or later without a v2
	// or v3 signature.
	ErrV2Required = errors.New("netsec: API 24 or later requires a v2 or v3 signature")

	// ErrV1Only reports a v1-only package targeting API 24 or later.
	ErrV1Only = errors.New("netsec: v1-only signature is not accepted for API 24 or later")

	// ErrNoSignature reports a package with no usable signature.
	ErrNoSignature = errors.New("netsec: package has no signature")

	// ErrSignatureBlockPlacement reports a signing block that is not
	// immediately followed by the central directory.
	ErrSignatureBlockPlacement = errors.New("netsec: signing block is not immediately followed by the central directory")

	// ErrDigestCoverage reports a v2 signature whose digest does not cover the
	// contents, central directory and end record.
	ErrDigestCoverage = errors.New("netsec: v2 digest does not cover all required ranges")

	// ErrInvalidMetadata reports mutually inconsistent package metadata.
	ErrInvalidMetadata = errors.New("netsec: invalid package metadata")
)

// Range is a half-open byte interval [Offset, Offset+Size).
type Range struct {
	Offset int64
	Size   int64
}

// End returns the exclusive end offset of r.
func (r Range) End() int64 {
	return r.Offset + r.Size
}

// Empty reports whether r contains no bytes.
func (r Range) Empty() bool {
	return r.Size <= 0
}

// SignatureBlock describes an APK signing block and the signature scheme IDs
// found in it. Offset and Size cover the complete block, including its size
// fields and trailing magic.
type SignatureBlock struct {
	Present bool
	Offset  int64
	Size    int64
	V2      bool
	V3      bool
	IDs     []uint32
}

// End returns the exclusive end offset of the signing block.
func (s SignatureBlock) End() int64 {
	return s.Offset + s.Size
}

// HasV2 reports whether the block contains a v2 signature. The ID list is
// authoritative even when the V2 convenience field was not populated.
func (s SignatureBlock) HasV2() bool {
	return s.V2 || hasSignatureID(s.IDs, V2SignatureID)
}

// HasV3 reports whether the block contains a v3 signature. The ID list is
// authoritative even when the V3 convenience field was not populated.
func (s SignatureBlock) HasV3() bool {
	return s.V3 || hasSignatureID(s.IDs, V3SignatureID)
}

// HasV2OrV3 reports whether the block contains either modern signature scheme.
func (s SignatureBlock) HasV2OrV3() bool {
	return s.HasV2() || s.HasV3()
}

// Package contains the package metadata needed for admission. Callers that
// already parsed the ZIP structures can populate it directly, which keeps this
// package independent of a particular ZIP or APK parser.
type Package struct {
	// Header is the first eight bytes of the package file. Fewer bytes are
	// accepted so callers can probe short files.
	Header []byte

	// FirstLocalHeaderOffset is the absolute file offset of the first local
	// file header.
	FirstLocalHeaderOffset int64

	// CentralDirectory is the central directory range.
	CentralDirectory Range

	// EOCD is the end-of-central-directory range. A zero Size means the
	// standard 22-byte record without a comment.
	EOCD Range

	// SignatureBlock describes the APK signing block, if any.
	SignatureBlock SignatureBlock

	// MinSDK is the package's minimum supported Android API level.
	MinSDK int

	// V1Signature reports a valid APK Signature Scheme v1 signature.
	V1Signature bool
}

// DigestRanges are the three byte ranges that an APK Signature Scheme v2
// digest must cover.
type DigestRanges struct {
	Contents         Range
	CentralDirectory Range
	EndRecord        Range
}

// V2DigestVerifier confirms that the v2 digest covers all three ranges.
// It must return true only when every non-empty range is authenticated.
type V2DigestVerifier func(DigestRanges) bool

// Admit checks package against the distribution gateway's admission policy.
// verifyV2 is required when a v2 signature is present. A nil verifier is
// rejected in that case because digest coverage cannot otherwise be
// established.
func Admit(pkg Package, verifyV2 V2DigestVerifier) error {
	if err := validateMetadata(pkg); err != nil {
		return err
	}

	if IsDEXHeader(pkg.Header) && pkg.hasValidEOCD() {
		return ErrPolymorphic
	}

	if pkg.FirstLocalHeaderOffset != 0 {
		return ErrFirstLocalHeader
	}

	block := pkg.SignatureBlock
	if block.Present {
		if block.Size <= 0 || block.End() != pkg.CentralDirectory.Offset {
			return ErrSignatureBlockPlacement
		}
	} else if block.V2 || block.V3 || len(block.IDs) != 0 {
		return ErrInvalidMetadata
	}

	hasV2 := block.Present && block.HasV2()
	hasV3 := block.Present && block.HasV3()
	switch {
	case pkg.MinSDK >= MinSDKRequiringV2 && !hasV2 && !hasV3:
		if pkg.V1Signature {
			return ErrV1Only
		}
		return ErrV2Required
	case pkg.MinSDK < MinSDKRequiringV2 && !pkg.V1Signature && !hasV2 && !hasV3:
		return ErrNoSignature
	}

	if hasV2 {
		if verifyV2 == nil {
			return ErrDigestCoverage
		}

		ranges := DigestRanges{
			Contents:         Range{Offset: 0, Size: block.Offset},
			CentralDirectory: pkg.CentralDirectory,
			EndRecord:        Range{Offset: pkg.EOCD.Offset, Size: eocdCoverageSize(pkg.EOCD)},
		}
		if ranges.Contents.Empty() ||
			ranges.CentralDirectory.Empty() ||
			ranges.EndRecord.Empty() {
			return ErrDigestCoverage
		}
		if !verifyV2(ranges) {
			return ErrDigestCoverage
		}
	}

	return nil
}

// Validate is an alias for Admit.
func Validate(pkg Package, verifyV2 V2DigestVerifier) error {
	return Admit(pkg, verifyV2)
}

// Check is an alias for Admit.
func Check(pkg Package, verifyV2 V2DigestVerifier) error {
	return Admit(pkg, verifyV2)
}

// IsDEXHeader reports whether header begins with the DEX magic.
func IsDEXHeader(header []byte) bool {
	return bytes.HasPrefix(header, []byte(DexMagic))
}

// HasV2SignatureID reports whether ids contains the v2 block ID.
func HasV2SignatureID(ids []uint32) bool {
	return hasSignatureID(ids, V2SignatureID)
}

// HasV3SignatureID reports whether ids contains the v3 block ID.
func HasV3SignatureID(ids []uint32) bool {
	return hasSignatureID(ids, V3SignatureID)
}

func hasSignatureID(ids []uint32, want uint32) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func (pkg Package) hasValidEOCD() bool {
	return pkg.EOCD.Offset >= 0 &&
		pkg.EOCD.Offset >= pkg.CentralDirectory.Offset &&
		eocdCoverageSize(pkg.EOCD) >= MinEOCDSize
}

func eocdCoverageSize(eocd Range) int64 {
	if eocd.Size == 0 {
		return MinEOCDSize
	}
	return eocd.Size
}

func validateMetadata(pkg Package) error {
	if pkg.MinSDK < 0 ||
		pkg.FirstLocalHeaderOffset < 0 ||
		!validRange(pkg.CentralDirectory) ||
		!validRange(pkg.EOCD) ||
		!validRange(Range{Offset: pkg.SignatureBlock.Offset, Size: pkg.SignatureBlock.Size}) {
		return ErrInvalidMetadata
	}
	if pkg.EOCD.Size != 0 && pkg.EOCD.Size < MinEOCDSize {
		return ErrInvalidMetadata
	}
	if !pkg.hasValidEOCD() {
		return ErrInvalidMetadata
	}
	if pkg.EOCD.Offset > maxOffset-eocdCoverageSize(pkg.EOCD) {
		return ErrInvalidMetadata
	}
	if pkg.CentralDirectory.End() > pkg.EOCD.Offset {
		return ErrInvalidMetadata
	}
	return nil
}

func validRange(r Range) bool {
	return r.Offset >= 0 && r.Size >= 0 && r.Offset <= maxOffset-r.Size
}
