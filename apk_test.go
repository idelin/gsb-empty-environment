package apkverify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"testing"
)

func TestParseAndValidateV2Coverage(t *testing.T) {
	data := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	archive, err := Parse(data)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if archive.SigningBlock == nil {
		t.Fatal("SigningBlock = nil")
	}
	if archive.SigningBlock.Start <= 0 || archive.SigningBlock.Start >= archive.CentralDirectoryOffset {
		t.Fatalf("unexpected signing block start %d", archive.SigningBlock.Start)
	}
	v2, ok := archive.SigningBlock.Scheme(SchemeV2)
	if !ok || len(v2.Signers) != 1 {
		t.Fatal("v2 block was not parsed")
	}
	if len(archive.Entries) != 2 || archive.Entries[0].Name != "AndroidManifest.xml" ||
		archive.Entries[1].Name != "classes.dex" {
		t.Fatalf("entries = %#v", archive.Entries)
	}

	minSDK := int32(24)
	expectedEOCD := append([]byte(nil), archive.EOCD...)
	binary.LittleEndian.PutUint32(expectedEOCD[16:20], uint32(archive.SigningBlock.Start))
	expected := append(append(append([]byte(nil), data[:archive.SigningBlock.Start]...),
		archive.CentralDirectory...), expectedEOCD...)

	called := false
	verifier := func(_ context.Context, req VerificationRequest) error {
		called = true
		if !bytes.Equal(req.Coverage.Section1, data[:archive.SigningBlock.Start]) {
			t.Error("section 1 does not end at signing block start")
		}
		if !bytes.Equal(req.Coverage.CentralDirectory, archive.CentralDirectory) {
			t.Error("central directory coverage differs")
		}
		if got := binary.LittleEndian.Uint32(req.Coverage.EOCD[16:20]); got != uint32(archive.SigningBlock.Start) {
			t.Errorf("normalized EOCD cd offset = %d, want %d", got, archive.SigningBlock.Start)
		}
		if !bytes.Equal(req.Coverage.Bytes(), expected) {
			t.Error("combined coverage differs")
		}
		wantHash := sha256.Sum256(expected)
		if got := req.Coverage.SHA256(); got != wantHash {
			t.Error("coverage hash differs")
		}
		return nil
	}
	report, err := Validate(data, Options{Verifier: verifier, MinSDKVersion: &minSDK})
	if err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !called || !report.SignatureVerified {
		t.Fatal("signature verifier was not called")
	}
}

func TestRejectsPolyglotPrefixes(t *testing.T) {
	valid := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	prefixes := [][]byte{
		[]byte("dex\n"),
		{0x7f, 'E', 'L', 'F'},
		[]byte("MZ"),
		[]byte{0x00, 0x00, 0x00, 0x00},
	}
	for _, prefix := range prefixes {
		data := append(append([]byte(nil), prefix...), valid...)
		if _, err := Parse(data); !errors.Is(err, ErrNotAPK) {
			t.Errorf("Parse(prefix %q) error = %v, want ErrNotAPK", prefix, err)
		}
	}
}

func TestRejectsZIP64Markers(t *testing.T) {
	valid := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	archive, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	eocdOffset := int(archive.EOCDRecordOffset)

	t.Run("EOCD sentinel", func(t *testing.T) {
		data := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(data[eocdOffset+16:eocdOffset+20], maxUint32)
		if _, err := Parse(data); !errors.Is(err, ErrZIP64) {
			t.Fatalf("Parse() error = %v, want ErrZIP64", err)
		}
	})

	t.Run("ZIP64 extra field", func(t *testing.T) {
		entries := []testEntry{{
			name:  "AndroidManifest.xml",
			data:  []byte("manifest"),
			extra: []byte{0x01, 0x00, 0x00, 0x00},
		}}
		data := buildAPK(t, entries, []testScheme{validV2Scheme()}, "")
		if _, err := Parse(data); !errors.Is(err, ErrZIP64) {
			t.Fatalf("Parse() error = %v, want ErrZIP64", err)
		}
	})

	t.Run("ZIP64 locator", func(t *testing.T) {
		data := append([]byte(nil), valid...)
		insertAt := eocdOffset
		locator := make([]byte, 20)
		binary.LittleEndian.PutUint32(locator[0:4], zip64LocatorSignature)
		data = append(data[:insertAt], append(locator, data[insertAt:]...)...)
		if _, err := Parse(data); !errors.Is(err, ErrZIP64) {
			t.Fatalf("Parse() error = %v, want ErrZIP64", err)
		}
	})
}

func TestRejectsEOCDCommentAndTrailingBytes(t *testing.T) {
	valid := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	archive, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	eocdOffset := int(archive.EOCDRecordOffset)

	t.Run("comment length mismatch", func(t *testing.T) {
		data := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint16(data[eocdOffset+20:eocdOffset+22], 1)
		if _, err := Parse(data); !errors.Is(err, ErrEOCD) {
			t.Fatalf("Parse() error = %v, want ErrEOCD", err)
		}
	})

	t.Run("trailing byte", func(t *testing.T) {
		data := append(append([]byte(nil), valid...), 0x00)
		if _, err := Parse(data); !errors.Is(err, ErrEOCD) {
			t.Fatalf("Parse() error = %v, want ErrEOCD", err)
		}
	})

	t.Run("declared comment is accepted", func(t *testing.T) {
		data := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "declared-comment")
		if _, err := Parse(data); err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
	})
}

func TestRejectsDuplicateNames(t *testing.T) {
	entries := []testEntry{
		{name: "classes.dex", data: []byte("first")},
		{name: "classes.dex", data: []byte("second")},
	}
	data := buildAPK(t, entries, []testScheme{validV2Scheme()}, "")
	if _, err := Parse(data); !errors.Is(err, ErrDuplicateEntry) {
		t.Fatalf("Parse() error = %v, want ErrDuplicateEntry", err)
	}
}

func TestRejectsLFHAndCentralDirectoryMismatch(t *testing.T) {
	valid := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	archive, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	cdOffset := int(archive.CentralDirectoryOffset)

	tests := []struct {
		name   string
		mutate func([]byte)
	}{
		{
			name: "CRC32",
			mutate: func(data []byte) {
				data[cdOffset+16] ^= 0xff
			},
		},
		{
			name: "compressed size",
			mutate: func(data []byte) {
				data[cdOffset+20]++
			},
		},
		{
			name: "uncompressed size",
			mutate: func(data []byte) {
				data[cdOffset+24]++
			},
		},
		{
			name: "local offset",
			mutate: func(data []byte) {
				data[cdOffset+42]++
			},
		},
		{
			name: "name",
			mutate: func(data []byte) {
				data[cdOffset+centralDirHeaderSize] ^= 0xff
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data := append([]byte(nil), valid...)
			tt.mutate(data)
			if _, err := Parse(data); !errors.Is(err, ErrEntryMismatch) {
				t.Fatalf("Parse() error = %v, want ErrEntryMismatch", err)
			}
		})
	}
}

func TestRejectsEmbeddedShadowLocalHeader(t *testing.T) {
	visible := testEntry{name: "classes.dex", data: []byte("hidden-payload")}
	shadow := makeLocalRecord(testEntry{name: "classes.dex", data: []byte("shadow")})
	visible.data = append(visible.data, shadow...)

	local := makeLocalRecord(visible)
	central := [][]byte{
		makeCentralRecord(visible, 0),
		makeCentralRecord(testEntry{name: "classes.dex", data: []byte("shadow")}, 30+uint32(len(visible.name))+uint32(len(visible.data))-uint32(len(shadow))),
	}
	data := assembleAPK(local, central, buildSigningBlock(t, []testScheme{validV2Scheme()}), "")

	if _, err := Parse(data); err == nil {
		t.Fatal("Parse() accepted an embedded shadow local header")
	}
}

func TestSigningBlockSizeAndAlignment(t *testing.T) {
	valid := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	archive, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	start := int(archive.SigningBlock.Start)
	cdOffset := int(archive.CentralDirectoryOffset)

	t.Run("front and footer sizes differ", func(t *testing.T) {
		data := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint64(data[start:start+8], archive.SigningBlock.Size-1)
		if _, err := Parse(data); !errors.Is(err, ErrSigningBlock) {
			t.Fatalf("Parse() error = %v, want ErrSigningBlock", err)
		}
	})

	t.Run("pair sequence is not aligned", func(t *testing.T) {
		data := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint64(data[cdOffset-24:cdOffset-16], archive.SigningBlock.Size+1)
		binary.LittleEndian.PutUint64(data[start:start+8], archive.SigningBlock.Size+1)
		if _, err := Parse(data); !errors.Is(err, ErrSigningBlock) {
			t.Fatalf("Parse() error = %v, want ErrSigningBlock", err)
		}
	})
}

func TestMinSDKPolicy(t *testing.T) {
	unsigned := buildAPK(t, validEntries(), nil, "")
	minSDK24 := int32(24)
	minSDK23 := int32(23)
	verifier := func(context.Context, VerificationRequest) error { return nil }

	if _, err := Validate(unsigned, Options{Verifier: verifier, MinSDKVersion: &minSDK24}); !errors.Is(err, ErrV1Only) {
		t.Fatalf("Validate(minSdk=24) error = %v, want ErrV1Only", err)
	}
	if _, err := Validate(unsigned, Options{Verifier: verifier, MinSDKVersion: &minSDK23}); !errors.Is(err, ErrV1Only) {
		t.Fatalf("Validate(minSdk=23 without AllowV1Only) error = %v, want ErrV1Only", err)
	}
	report, err := Validate(unsigned, Options{
		Verifier:      verifier,
		MinSDKVersion: &minSDK23,
		AllowV1Only:   true,
	})
	if err != nil {
		t.Fatalf("Validate(minSdk=23, AllowV1Only) error = %v", err)
	}
	if report.SignatureVerified {
		t.Fatal("v1-only report marked signature verified")
	}
}

func TestV3RollbackAndLineagePolicy(t *testing.T) {
	minSDK := int32(24)
	verifier := func(context.Context, VerificationRequest) error { return nil }

	t.Run("v3 stops before v3.1 without fallback", func(t *testing.T) {
		data := buildAPK(t, validEntries(), []testScheme{{
			scheme: SchemeV3, minSDK: 28, maxSDK: 32, cert: []byte("v3-cert"),
		}}, "")
		if _, err := Validate(data, Options{Verifier: verifier, MinSDKVersion: &minSDK}); !errors.Is(err, ErrSignaturePolicy) {
			t.Fatalf("Validate() error = %v, want ErrSignaturePolicy", err)
		}
	})

	t.Run("v3.1 targets an older API", func(t *testing.T) {
		schemes := []testScheme{
			{scheme: SchemeV3, minSDK: 28, maxSDK: 32, cert: []byte("v3-cert")},
			{scheme: SchemeV31, minSDK: 28, maxSDK: mathMaxUint32, cert: []byte("v31-cert")},
		}
		data := buildAPK(t, validEntries(), schemes, "")
		if _, err := Validate(data, Options{Verifier: verifier, MinSDKVersion: &minSDK}); !errors.Is(err, ErrSignaturePolicy) {
			t.Fatalf("Validate() error = %v, want ErrSignaturePolicy", err)
		}
	})

	t.Run("v3.1 without v3", func(t *testing.T) {
		data := buildAPK(t, validEntries(), []testScheme{{
			scheme: SchemeV31, minSDK: 33, maxSDK: mathMaxUint32, cert: []byte("v31-cert"),
		}}, "")
		if _, err := Validate(data, Options{Verifier: verifier, MinSDKVersion: &minSDK}); !errors.Is(err, ErrSignaturePolicy) {
			t.Fatalf("Validate() error = %v, want ErrSignaturePolicy", err)
		}
	})

	t.Run("valid v3 and v3.1 lineage", func(t *testing.T) {
		data := buildAPK(t, validEntries(), validV3Schemes(), "")
		archive, err := Parse(data)
		if err != nil {
			t.Fatalf("Parse() error = %v", err)
		}
		v31, ok := archive.SigningBlock.Scheme(SchemeV31)
		if !ok || len(v31.Signers) != 1 {
			t.Fatal("v3.1 block missing")
		}
		lineage := v31.Signers[0].Lineage
		if lineage == nil || lineage.Nodes != 2 || len(lineage.Certificates) != 2 {
			t.Fatalf("lineage = %#v", lineage)
		}
		if _, err := Validate(data, Options{Verifier: verifier, MinSDKVersion: &minSDK}); err != nil {
			t.Fatalf("Validate() error = %v", err)
		}
	})
}

func TestVerifierFailureRejectsAPK(t *testing.T) {
	data := buildAPK(t, validEntries(), []testScheme{validV2Scheme()}, "")
	sentinel := errors.New("bad digest")
	_, err := Validate(data, Options{
		Verifier: func(context.Context, VerificationRequest) error {
			return sentinel
		},
	})
	if !errors.Is(err, ErrSignatureVerification) || !errors.Is(err, sentinel) {
		t.Fatalf("Validate() error = %v, want wrapped verification failure", err)
	}
}

const mathMaxUint32 = ^uint32(0)
