package apkverify

import (
	"encoding/binary"
	"errors"
	"testing"
)

const (
	testLocalEnd  = int64(1000) // local file headers + entries section
	testSigEnd    = int64(1200) // end of APK Signing Block / start of central dir
	testEOCDStart = int64(1500)
	testFileSize  = int64(1522) // EOCD fixed 22 bytes, zero-length comment
	testSigSize   = uint64(testSigEnd - testLocalEnd - 8)
)

func zipHead() [8]byte {
	var head [8]byte
	binary.LittleEndian.PutUint32(head[:4], ZIPLocalFileHeaderMagic)
	return head
}

func dexHead() [8]byte {
	var head [8]byte
	copy(head[:], DexMagic+"035\x00")
	return head
}

func garbageHead() [8]byte {
	var head [8]byte
	head[0] = 0xde
	head[1] = 0xad
	head[2] = 0xbe
	head[3] = 0xef
	return head
}

// validV2Input returns a fully admissible v2-signed package description.
func validV2Input(verify DigestVerifier) Input {
	return Input{
		Head8:                  zipHead(),
		FirstLocalHeaderOffset: 0,
		CentralDirOffset:       testSigEnd,
		EOCDOffset:             testEOCDStart,
		EOCDCommentLength:      0,
		FileSize:               testFileSize,
		Zip64:                  false,
		HasSigBlock:            true,
		SigBlockOffset:         testLocalEnd,
		SigBlockSizeBefore:     testSigSize,
		SigBlockSizeAfter:      testSigSize,
		SignatureIDs:           []uint32{V2SignatureID},
		MinSDKVersion:          MinSDKWithV2,
		VerifyDigest:           verify,
	}
}

// recordingVerifier stores the segments it receives and accepts them.
func recordingVerifier(got *[]DigestSegment) DigestVerifier {
	return func(segments []DigestSegment) error {
		*got = append((*got)[:0:0], segments...)
		return nil
	}
}

// missingCentralDirVerifier models a signature whose digest does not cover
// the central directory range.
func missingCentralDirVerifier() DigestVerifier {
	return func(segments []DigestSegment) error {
		for _, seg := range segments {
			if seg.Start == testSigEnd && seg.End == testEOCDStart {
				return errors.New("digest does not cover the central directory")
			}
		}
		return errors.New("central directory segment missing from digest")
	}
}

func TestValidV2Accepted(t *testing.T) {
	var got []DigestSegment
	in := validV2Input(recordingVerifier(&got))
	if err := Admit(in); err != nil {
		t.Fatalf("Admit() unexpected error: %v", err)
	}
	want := []DigestSegment{
		{Start: 0, End: testLocalEnd},
		{Start: testSigEnd, End: testEOCDStart},
		{Start: testEOCDStart, End: testFileSize},
	}
	if len(got) != len(want) {
		t.Fatalf("digest segments = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("segment %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestValidV3Accepted(t *testing.T) {
	var got []DigestSegment
	in := validV2Input(recordingVerifier(&got))
	in.SignatureIDs = []uint32{V3SignatureID}
	in.MinSDKVersion = 28
	if err := Admit(in); err != nil {
		t.Fatalf("Admit() v3 package rejected: %v", err)
	}
}

func TestDexPrefixRejected(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	in.Head8 = dexHead()
	err := Admit(in)
	if !errors.Is(err, ErrDexPrefix) {
		t.Fatalf("Admit() error = %v, want ErrDexPrefix", err)
	}
}

func TestNonZipHeadWithValidEOCDRejectedAsPolyglot(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	in.Head8 = garbageHead()
	err := Admit(in)
	if !errors.Is(err, ErrPolymorphic) {
		t.Fatalf("Admit() error = %v, want ErrPolymorphic", err)
	}
}

func TestLocalHeaderNotAtZeroRejected(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	in.FirstLocalHeaderOffset = 64
	err := Admit(in)
	if !errors.Is(err, ErrLocalHeaderNotAtZero) {
		t.Fatalf("Admit() error = %v, want ErrLocalHeaderNotAtZero", err)
	}
}

func TestV1OnlyAtSDK24Rejected(t *testing.T) {
	in := validV2Input(nil)
	in.SignatureIDs = nil
	in.HasSigBlock = false
	err := Admit(in)
	if !errors.Is(err, ErrMissingV2Signature) {
		t.Fatalf("Admit() error = %v, want ErrMissingV2Signature", err)
	}
}

func TestV1OnlyBelowSDK24Accepted(t *testing.T) {
	in := validV2Input(nil)
	in.SignatureIDs = nil
	in.HasSigBlock = false
	in.MinSDKVersion = MinSDKWithV2 - 1
	if err := Admit(in); err != nil {
		t.Fatalf("Admit() v1 package below API 24 rejected: %v", err)
	}
}

func TestSigBlockSizeMismatchRejected(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	in.SigBlockSizeAfter = in.SigBlockSizeBefore - 1
	err := Admit(in)
	if !errors.Is(err, ErrSigBlockSizeMismatch) {
		t.Fatalf("Admit() error = %v, want ErrSigBlockSizeMismatch", err)
	}
}

func TestSigBlockNotFollowedByCentralDirRejected(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	// Both size fields stay consistent, but they describe a block that ends
	// 16 bytes before the central directory starts.
	in.SigBlockSizeBefore = testSigSize - 16
	in.SigBlockSizeAfter = testSigSize - 16
	err := Admit(in)
	if !errors.Is(err, ErrSigBlockLayout) {
		t.Fatalf("Admit() error = %v, want ErrSigBlockLayout", err)
	}
}

func TestDigestMissingCentralDirRejected(t *testing.T) {
	in := validV2Input(missingCentralDirVerifier())
	err := Admit(in)
	if !errors.Is(err, ErrDigestRange) {
		t.Fatalf("Admit() error = %v, want ErrDigestRange", err)
	}
}

func TestZip64Rejected(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	in.Zip64 = true
	err := Admit(in)
	if !errors.Is(err, ErrZip64) {
		t.Fatalf("Admit() error = %v, want ErrZip64", err)
	}
}

func TestForgedEOCDCommentOverflowRejected(t *testing.T) {
	in := validV2Input(recordingVerifier(new([]DigestSegment)))
	in.EOCDCommentLength = 64 // EOCD would end past the physical file end
	err := Admit(in)
	if !errors.Is(err, ErrEOCDOverflow) {
		t.Fatalf("Admit() error = %v, want ErrEOCDOverflow", err)
	}
}

func TestNilDigestVerifierWithV2Rejected(t *testing.T) {
	in := validV2Input(nil)
	err := Admit(in)
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Admit() error = %v, want ErrInvalidInput", err)
	}
}

func TestConstants(t *testing.T) {
	if V2SignatureID != 0x7109871a {
		t.Errorf("V2SignatureID = %#x", V2SignatureID)
	}
	if V3SignatureID != 0xf05368c0 {
		t.Errorf("V3SignatureID = %#x", V3SignatureID)
	}
	if SigBlockMagic != "APK Sig Block 42" {
		t.Errorf("SigBlockMagic = %q", SigBlockMagic)
	}
}
