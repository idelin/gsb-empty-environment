package netsec

import (
	"errors"
	"testing"
)

const (
	validSigOffset = int64(4032)
	validSigSize   = int64(64)
	validCDOffset  = int64(4096)
	validCDSize    = int64(128)
	validEOCDOff   = int64(4224)
	validEOCDSize  = int64(22)
)

func validV2Package() Package {
	return Package{
		Header:                 []byte("PK\x03\x04\x14\x00"),
		FirstLocalHeaderOffset: 0,
		CentralDirectory:       Range{Offset: validCDOffset, Size: validCDSize},
		EOCD:                   Range{Offset: validEOCDOff, Size: validEOCDSize},
		SignatureBlock: SignatureBlock{
			Present: true,
			Offset:  validSigOffset,
			Size:    validSigSize,
			IDs:     []uint32{V2SignatureID},
		},
		MinSDK:      24,
		V1Signature: true,
	}
}

func fullCoverageVerifier(ranges DigestRanges) bool {
	return ranges.Contents == Range{Offset: 0, Size: validSigOffset} &&
		ranges.CentralDirectory == Range{Offset: validCDOffset, Size: validCDSize} &&
		ranges.EndRecord == Range{Offset: validEOCDOff, Size: validEOCDSize}
}

func TestAdmitRejectsDEXPrefixedPolymorphicPackage(t *testing.T) {
	pkg := validV2Package()
	pkg.Header = []byte("dex\n035\x00")

	if err := Admit(pkg, fullCoverageVerifier); !errors.Is(err, ErrPolymorphic) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrPolymorphic)
	}
}

func TestAdmitRejectsNonZeroFirstLocalHeader(t *testing.T) {
	pkg := validV2Package()
	pkg.FirstLocalHeaderOffset = 8

	if err := Admit(pkg, fullCoverageVerifier); !errors.Is(err, ErrFirstLocalHeader) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrFirstLocalHeader)
	}
}

func TestAdmitRejectsV1OnlyForAPI24(t *testing.T) {
	pkg := validV2Package()
	pkg.SignatureBlock = SignatureBlock{}
	pkg.V1Signature = true

	if err := Admit(pkg, nil); !errors.Is(err, ErrV1Only) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrV1Only)
	}
}

func TestAdmitAcceptsV1OnlyBelowAPI24(t *testing.T) {
	pkg := validV2Package()
	pkg.SignatureBlock = SignatureBlock{}
	pkg.MinSDK = 23
	pkg.V1Signature = true

	if err := Admit(pkg, nil); err != nil {
		t.Fatalf("Admit() error = %v, want nil", err)
	}
}

func TestAdmitRejectsV2DigestMissingCentralDirectory(t *testing.T) {
	pkg := validV2Package()
	pkg.CentralDirectory.Size = 0

	// The callback accepts everything, so the rejection must come from the
	// admission check itself detecting the missing range.
	if err := Admit(pkg, func(DigestRanges) bool { return true }); !errors.Is(err, ErrDigestCoverage) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrDigestCoverage)
	}
}

func TestAdmitRejectsVerifierThatRejectsCoverage(t *testing.T) {
	pkg := validV2Package()

	if err := Admit(pkg, func(DigestRanges) bool { return false }); !errors.Is(err, ErrDigestCoverage) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrDigestCoverage)
	}
}

func TestAdmitAcceptsValidV2Package(t *testing.T) {
	pkg := validV2Package()
	var got DigestRanges
	calls := 0
	err := Admit(pkg, func(ranges DigestRanges) bool {
		calls++
		got = ranges
		return fullCoverageVerifier(ranges)
	})
	if err != nil {
		t.Fatalf("Admit() error = %v, want nil", err)
	}
	if calls != 1 {
		t.Fatalf("verifier calls = %d, want 1", calls)
	}
	if got.Contents != (Range{Offset: 0, Size: validSigOffset}) {
		t.Fatalf("contents range = %+v", got.Contents)
	}
	if got.CentralDirectory != (Range{Offset: validCDOffset, Size: validCDSize}) {
		t.Fatalf("central directory range = %+v", got.CentralDirectory)
	}
	if got.EndRecord != (Range{Offset: validEOCDOff, Size: validEOCDSize}) {
		t.Fatalf("end record range = %+v", got.EndRecord)
	}
}

func TestAdmitRejectsMissingV2Verifier(t *testing.T) {
	pkg := validV2Package()

	if err := Admit(pkg, nil); !errors.Is(err, ErrDigestCoverage) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrDigestCoverage)
	}
}

func TestAdmitRejectsSigningBlockNotAdjacentToCentralDirectory(t *testing.T) {
	pkg := validV2Package()
	pkg.SignatureBlock.Size = validSigSize - 1

	if err := Admit(pkg, fullCoverageVerifier); !errors.Is(err, ErrSignatureBlockPlacement) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrSignatureBlockPlacement)
	}
}

func TestAdmitRejectsMissingSignatureAboveAPI24(t *testing.T) {
	pkg := validV2Package()
	pkg.SignatureBlock = SignatureBlock{}
	pkg.V1Signature = false

	if err := Admit(pkg, nil); !errors.Is(err, ErrV2Required) {
		t.Fatalf("Admit() error = %v, want %v", err, ErrV2Required)
	}
}

func TestAdmitAcceptsV3ForAPI24(t *testing.T) {
	pkg := validV2Package()
	pkg.SignatureBlock.IDs = []uint32{V3SignatureID}

	if err := Admit(pkg, nil); err != nil {
		t.Fatalf("Admit() error = %v, want nil", err)
	}
}

func TestSignatureIDs(t *testing.T) {
	ids := []uint32{V3SignatureID, V2SignatureID}
	if !HasV2SignatureID(ids) {
		t.Fatal("HasV2SignatureID() = false, want true")
	}
	if !HasV3SignatureID(ids) {
		t.Fatal("HasV3SignatureID() = false, want true")
	}
	if HasV2SignatureID([]uint32{V3SignatureID}) {
		t.Fatal("HasV2SignatureID() = true for a v3-only list")
	}
}

func TestIsDEXHeader(t *testing.T) {
	if !IsDEXHeader([]byte("dex\n035\x00")) {
		t.Fatal("IsDEXHeader() = false, want true")
	}
	if IsDEXHeader([]byte("PK\x03\x04")) {
		t.Fatal("IsDEXHeader() = true, want false")
	}
}
