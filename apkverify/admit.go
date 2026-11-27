// Package apkverify implements admission checks for Android install
// packages (APK) on a distribution gateway.
//
// It defends against the Janus-style polyglot attack: a DEX file is
// prepended to a structurally valid ZIP/APK so that v1 (JAR) signature
// verification, which only covers archive entries, still passes while the
// platform loads the attacker's DEX code. The checks here validate the
// bytes outside the ZIP entry stream as well: the file head, local header
// placement, the End Of Central Directory (EOCD) record, the APK Signing
// Block layout and the v2/v3 digest coverage ranges.
package apkverify

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
)

// Well-known magic numbers and signing block identifiers.
const (
	// DexMagic is the leading magic of a Dalvik/ART executable.
	DexMagic = "dex\n"

	// ZIPLocalFileHeaderMagic is "PK\x03\x04" interpreted little-endian.
	ZIPLocalFileHeaderMagic uint32 = 0x04034b50

	// EOCDMagic is "PK\x05\x06" interpreted little-endian.
	EOCDMagic uint32 = 0x06054b50

	// SigBlockMagic is the 16-byte trailer of the APK Signing Block.
	SigBlockMagic = "APK Sig Block 42"

	// V2SignatureID is the APK Signature Scheme v2 block pair ID.
	V2SignatureID uint32 = 0x7109871a

	// V3SignatureID is the APK Signature Scheme v3 block pair ID.
	V3SignatureID uint32 = 0xf05368c0

	// MinSDKWithV2 is the first Android API level (7.0) on which v2-or-newer
	// signatures are mandatory; below it legacy v1 (JAR) signing is accepted.
	MinSDKWithV2 = 24

	// eocdFixedSize is the fixed-size portion of the EOCD record, before
	// the variable-length comment.
	eocdFixedSize = 22
)

// Sentinel errors returned by Admit. Use errors.Is to match them.
var (
	// ErrDexPrefix marks a file beginning with the DEX magic, i.e. a DEX
	// payload prepended to the archive.
	ErrDexPrefix = errors.New("apkverify: file starts with DEX magic (polyglot DEX+ZIP attack)")

	// ErrPolymorphic marks a file whose first bytes are not a ZIP local
	// file header even though a valid EOCD record exists.
	ErrPolymorphic = errors.New("apkverify: non-ZIP file head with valid EOCD (polyglot archive)")

	// ErrZip64 marks archives using Zip64 structures, which are rejected.
	ErrZip64 = errors.New("apkverify: Zip64 archives are not accepted")

	// ErrEOCDOverflow marks an EOCD whose declared comment length runs past
	// the physical end of file, indicating a forged EOCD record.
	ErrEOCDOverflow = errors.New("apkverify: EOCD comment length overruns the file end (forged EOCD)")

	// ErrLocalHeaderNotAtZero marks archives whose first local file header
	// does not sit at offset 0.
	ErrLocalHeaderNotAtZero = errors.New("apkverify: first ZIP local file header is not at offset 0")

	// ErrMissingV2Signature marks packages without v2/v3 signatures that
	// target API level 24 or newer.
	ErrMissingV2Signature = errors.New("apkverify: target API >= 24 requires APK Signature Scheme v2 or newer")

	// ErrSigBlockLayout marks an APK Signing Block that is not directly
	// followed by the central directory.
	ErrSigBlockLayout = errors.New("apkverify: APK signing block is not directly followed by the central directory")

	// ErrSigBlockSizeMismatch marks an APK Signing Block whose leading and
	// trailing uint64 size fields disagree.
	ErrSigBlockSizeMismatch = errors.New("apkverify: APK signing block leading and trailing size fields differ")

	// ErrDigestRange wraps a failure reported by the caller-supplied
	// DigestVerifier, including digest ranges that miss one of the three
	// required byte ranges.
	ErrDigestRange = errors.New("apkverify: v2/v3 digest range verification failed")

	// ErrInvalidInput marks internally inconsistent admission metadata.
	ErrInvalidInput = errors.New("apkverify: invalid admission input")
)

// DigestSegment is one contiguous byte range covered by the v2/v3 content
// digest. End is exclusive.
type DigestSegment struct {
	Start int64
	End   int64
}

// DigestVerifier is implemented by the gateway caller. It receives exactly
// the three ranges the v2/v3 content digest must cover, in order:
//
//  1. the local-file-header section from offset 0 up to (not including) the
//     APK Signing Block;
//  2. the central directory;
//  3. the EOCD record up to the physical end of file.
//
// The implementation must confirm that the digest embedded in every offered
// v2/v3 signature is computed over precisely these ranges, and that the
// signing block's two size fields describe the same block. Returning an
// error (for example when any range is missing from the digest) rejects the
// package.
type DigestVerifier func(segments []DigestSegment) error

// Input carries the package metadata parsed by the gateway's ZIP/APK parser.
// All offsets are absolute byte offsets into the candidate package file.
type Input struct {
	// Head8 is the first eight bytes of the package file.
	Head8 [8]byte

	// FirstLocalHeaderOffset is the offset of the first ZIP local file
	// header ("PK\x03\x04") located by the parser.
	FirstLocalHeaderOffset int64

	// CentralDirOffset is the start offset of the central directory.
	CentralDirOffset int64

	// EOCDOffset is the start offset of the End Of Central Directory record.
	EOCDOffset int64

	// EOCDCommentLength is the comment length declared inside the EOCD.
	EOCDCommentLength uint16

	// FileSize is the total size of the package file in bytes.
	FileSize int64

	// Zip64 reports whether the parser detected Zip64 usage (a Zip64 EOCD
	// locator or 0xffffffff sentinel fields). Such archives are rejected.
	Zip64 bool

	// HasSigBlock reports that an APK Signing Block ending in the 16-byte
	// magic SigBlockMagic was found immediately before the central directory.
	HasSigBlock bool

	// SigBlockOffset is the offset of the block's leading uint64 size field.
	SigBlockOffset int64

	// SigBlockSizeBefore is the leading uint64 size field (block size minus
	// this field itself).
	SigBlockSizeBefore uint64

	// SigBlockSizeAfter is the uint64 size field located immediately before
	// the block magic. It must equal SigBlockSizeBefore.
	SigBlockSizeAfter uint64

	// SignatureIDs lists the key-value pair IDs found in the signing block,
	// such as V2SignatureID and V3SignatureID.
	SignatureIDs []uint32

	// MinSDKVersion is the minimum Android API level the package targets.
	MinSDKVersion int

	// VerifyDigest is invoked when the package carries a v2 and/or v3
	// signature. It must be non-nil in that case.
	VerifyDigest DigestVerifier
}

// Admit decides whether a candidate install package may enter the gateway.
// A nil return means the package passes every admission rule; otherwise the
// returned error wraps one of the package's sentinel errors.
func Admit(in Input) error {
	if err := validateOffsets(in); err != nil {
		return err
	}
	if err := validateEOCD(in); err != nil {
		return err
	}
	if err := detectPolyglot(in); err != nil {
		return err
	}
	if in.FirstLocalHeaderOffset != 0 {
		return fmt.Errorf("%w: offset=%d", ErrLocalHeaderNotAtZero, in.FirstLocalHeaderOffset)
	}

	if !hasModernSignature(in.SignatureIDs) {
		// No v2/v3 signature: only legacy v1 (JAR) signing can apply, which
		// is accepted solely below API 24. On >= 24 the package is rejected.
		if in.MinSDKVersion >= MinSDKWithV2 {
			return ErrMissingV2Signature
		}
		return nil
	}

	if !in.HasSigBlock {
		return fmt.Errorf("%w: v2/v3 signature IDs present without an APK signing block", ErrInvalidInput)
	}
	if err := validateSigBlock(in); err != nil {
		return err
	}

	// The v2/v3 digest is computed over three disjoint ranges separated by
	// the signing block, which is itself excluded from the digest.
	segments := []DigestSegment{
		{Start: 0, End: in.SigBlockOffset},
		{Start: in.CentralDirOffset, End: in.EOCDOffset},
		{Start: in.EOCDOffset, End: in.FileSize},
	}
	for _, seg := range segments {
		if seg.Start < 0 || seg.End <= seg.Start {
			return fmt.Errorf("%w: empty or negative digest segment %+v", ErrInvalidInput, seg)
		}
	}

	if in.VerifyDigest == nil {
		return fmt.Errorf("%w: missing digest verifier callback for v2/v3 signature", ErrInvalidInput)
	}
	if err := in.VerifyDigest(segments); err != nil {
		return fmt.Errorf("%w: %v", ErrDigestRange, err)
	}
	return nil
}

// hasModernSignature reports whether ids contain a v2 or v3 signature pair.
func hasModernSignature(ids []uint32) bool {
	for _, id := range ids {
		if id == V2SignatureID || id == V3SignatureID {
			return true
		}
	}
	return false
}

// validateOffsets checks basic offset ordering before structural rules run.
func validateOffsets(in Input) error {
	if in.FileSize <= 0 ||
		in.FirstLocalHeaderOffset < 0 ||
		in.CentralDirOffset < 0 ||
		in.EOCDOffset < 0 ||
		in.SigBlockOffset < 0 {
		return fmt.Errorf("%w: offsets must be non-negative and file size positive", ErrInvalidInput)
	}
	if in.CentralDirOffset >= in.EOCDOffset || in.EOCDOffset >= in.FileSize {
		return fmt.Errorf("%w: inconsistent central directory / EOCD offsets", ErrInvalidInput)
	}
	return nil
}

// validateEOCD rejects Zip64 archives and EOCD records whose declared comment
// length does not end exactly at the physical end of file.
func validateEOCD(in Input) error {
	if in.Zip64 {
		return ErrZip64
	}
	// The EOCD consists of 22 fixed bytes followed by the comment. A forged
	// comment length that overruns EOF means this is not a real, terminating
	// EOCD record (an attacker may hide another record behind it).
	eocdEnd := in.EOCDOffset + eocdFixedSize + int64(in.EOCDCommentLength)
	if eocdEnd != in.FileSize {
		return fmt.Errorf("%w: EOCD end=%d file size=%d", ErrEOCDOverflow, eocdEnd, in.FileSize)
	}
	return nil
}

// detectPolyglot rejects DEX-prefixed files and any file whose head is not a
// ZIP local file header while a valid EOCD exists. EOCD validity must have
// been established by validateEOCD before calling this function.
func detectPolyglot(in Input) error {
	if bytes.HasPrefix(in.Head8[:], []byte(DexMagic)) {
		return ErrDexPrefix
	}
	if binary.LittleEndian.Uint32(in.Head8[:4]) != ZIPLocalFileHeaderMagic {
		return ErrPolymorphic
	}
	return nil
}

// validateSigBlock enforces the signing block layout: equal leading and
// trailing size fields and a central directory starting at the first byte
// after the block. The block occupies [SigBlockOffset, SigBlockOffset+size+8)
// because the leading size field excludes itself from its own value.
func validateSigBlock(in Input) error {
	if in.SigBlockOffset == 0 || in.SigBlockSizeBefore == 0 {
		return fmt.Errorf("%w: signing block offset or size is zero", ErrInvalidInput)
	}
	if in.SigBlockSizeBefore != in.SigBlockSizeAfter {
		return fmt.Errorf("%w: leading=%d trailing=%d",
			ErrSigBlockSizeMismatch, in.SigBlockSizeBefore, in.SigBlockSizeAfter)
	}
	if in.SigBlockSizeBefore > math.MaxInt64 {
		return fmt.Errorf("%w: signing block size overflows int64", ErrInvalidInput)
	}
	blockEnd := in.SigBlockOffset + int64(in.SigBlockSizeBefore) + 8
	if blockEnd != in.CentralDirOffset {
		return fmt.Errorf("%w: signing block end=%d central directory offset=%d",
			ErrSigBlockLayout, blockEnd, in.CentralDirOffset)
	}
	return nil
}
