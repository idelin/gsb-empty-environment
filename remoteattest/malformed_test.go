package remoteattest

import (
	"bytes"
	"errors"
	"testing"
)

func TestAttestationDecoderValidation(t *testing.T) {
	b := validBundle(t)

	t.Run("truncated blob", func(t *testing.T) {
		if _, err := DecodeAttestation(b.attest[:10]); err != nil {
			if !errors.Is(err, ErrMalformedAttestation) {
				t.Fatalf("want ErrMalformedAttestation, got %v", err)
			}
		} else {
			t.Fatal("expected decode error")
		}
	})

	t.Run("trailing bytes", func(t *testing.T) {
		padded := append(bytes.Clone(b.attest), 0xAA)
		if _, err := DecodeAttestation(padded); !errors.Is(err, ErrMalformedAttestation) {
			t.Fatalf("want ErrMalformedAttestation, got %v", err)
		}
	})

	t.Run("bad magic", func(t *testing.T) {
		bad := bytes.Clone(b.attest)
		bad[0] = 0x00
		if _, err := DecodeAttestation(bad); !errors.Is(err, ErrBadMagic) {
			t.Fatalf("want ErrBadMagic, got %v", err)
		}
	})

	t.Run("wrong attestation type", func(t *testing.T) {
		bad := bytes.Clone(b.attest)
		bad[4] = 0x80
		bad[5] = 0x19 // not TPM_ST_ATTEST_QUOTE
		if _, err := DecodeAttestation(bad); !errors.Is(err, ErrNotQuote) {
			t.Fatalf("want ErrNotQuote, got %v", err)
		}
	})

	t.Run("round trip fields", func(t *testing.T) {
		att, err := DecodeAttestation(b.attest)
		if err != nil {
			t.Fatal(err)
		}
		if att.FirmwareVersion != 0x2024000000000000 {
			t.Fatalf("firmware round trip = %x", att.FirmwareVersion)
		}
		if att.ClockInfo.ResetCount != 1 || att.ClockInfo.Safe != 1 {
			t.Fatalf("clock info round trip = %+v", att.ClockInfo)
		}
		if len(att.QualifiedSigner) != compressedPointSize {
			t.Fatalf("qualifiedSigner size = %d", len(att.QualifiedSigner))
		}
	})
}

func TestSignatureDecoderValidation(t *testing.T) {
	b := validBundle(t)

	t.Run("truncated signature", func(t *testing.T) {
		if _, err := DecodeECDSASignature(b.sig[:10]); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("want ErrMalformedSignature, got %v", err)
		}
	})

	t.Run("wrong scheme", func(t *testing.T) {
		bad := bytes.Clone(b.sig)
		bad[0] = 0x00
		bad[1] = 0x14 // TPM2_ALG_RSA
		if _, err := DecodeECDSASignature(bad); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("want ErrMalformedSignature, got %v", err)
		}
	})

	t.Run("trailing signature bytes", func(t *testing.T) {
		bad := append(bytes.Clone(b.sig), 0x00)
		if _, err := DecodeECDSASignature(bad); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("want ErrMalformedSignature, got %v", err)
		}
	})
}

func TestIMALogParserValidation(t *testing.T) {
	b := validBundle(t)

	t.Run("truncated header", func(t *testing.T) {
		if _, err := ReplayIMALog(b.log[:3]); !errors.Is(err, ErrMalformedIMALog) {
			t.Fatalf("want ErrMalformedIMALog, got %v", err)
		}
	})

	t.Run("truncated template name", func(t *testing.T) {
		if _, err := ReplayIMALog(b.log[:4+20+3]); !errors.Is(err, ErrMalformedIMALog) {
			t.Fatalf("want ErrMalformedIMALog, got %v", err)
		}
	})

	t.Run("unsupported template", func(t *testing.T) {
		digest := fileDigest([]byte("x"))
		log := serializeEntries([]builtEntry{{
			pcr: 10, template: "ima-buf", fileDigest: digest, path: "/x",
		}})
		if _, err := ReplayIMALog(log); !errors.Is(err, ErrUnsupportedIMATemplate) {
			t.Fatalf("want ErrUnsupportedIMATemplate, got %v", err)
		}
	})

	t.Run("out of range PCR index", func(t *testing.T) {
		digest := fileDigest([]byte("x"))
		log := serializeEntries([]builtEntry{{
			pcr: 99, template: TemplateIMANG, fileDigest: digest, path: "/x",
		}})
		if _, err := ReplayIMALog(log); !errors.Is(err, ErrPCRIndexOutOfRange) {
			t.Fatalf("want ErrPCRIndexOutOfRange, got %v", err)
		}
	})

	t.Run("little endian PCR index decoded correctly", func(t *testing.T) {
		entries, err := ParseIMALog(b.log)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.PCRIndex != 10 {
				t.Fatalf("PCR index = %d, want 10 (LE decode)", e.PCRIndex)
			}
			if e.FileDigestAlg != "sha256" || len(e.FileDigest) != 32 {
				t.Fatalf("bad d-ng parse: alg=%q len=%d", e.FileDigestAlg, len(e.FileDigest))
			}
		}
	})

	t.Run("ima-sig signature round trip", func(t *testing.T) {
		entries, err := ParseIMALog(b.log)
		if err != nil {
			t.Fatal(err)
		}
		if entries[0].Signature != nil {
			t.Fatal("ima-ng entry must carry no signature")
		}
		if len(entries[1].Signature) == 0 || entries[1].TemplateName != TemplateIMASIG {
			t.Fatal("ima-sig signature not round-tripped")
		}
	})
}

func TestPCRSelectionBitmap(t *testing.T) {
	sel := PCRSelection{HashAlg: TPMAlgSHA256, Bitmap: []byte{0x01, 0x04}}
	got := sel.Indices()
	want := []uint32{0, 10}
	if len(got) != len(want) {
		t.Fatalf("indices = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] || !sel.Contains(want[i]) {
			t.Fatalf("bitmap decode wrong: %v", got)
		}
	}
	if sel.Contains(3) {
		t.Fatal("PCR 3 must not be selected")
	}
}
