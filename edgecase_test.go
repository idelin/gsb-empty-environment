package netsec

import (
	"archive/tar"
	"bytes"
	"crypto/sha512"
	"encoding/asn1"
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"
)

func buildOuterTar(t *testing.T, entries map[string][]byte, typeflag byte) []byte {
	t.Helper()
	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	for name, content := range entries {
		flag := byte(tar.TypeReg)
		if typeflag != 0 {
			flag = typeflag
		}
		hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content)), Typeflag: flag}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(tw, bytes.NewReader(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func validMembers(t *testing.T) map[string][]byte {
	t.Helper()
	chain := mustChain(t, "edge")
	gem := newGemFixture().build(buildConfig{signer: chain})
	members, err := ReadMembers(bytes.NewReader(gem), 0)
	if err != nil {
		t.Fatal(err)
	}
	return members
}

func TestReadMembersMalformedInputs(t *testing.T) {
	t.Run("not a tar", func(t *testing.T) {
		if _, err := ReadMembers(strings.NewReader("definitely not tar data"), 0); !errors.Is(err, ErrMalformedArchive) {
			t.Fatalf("expected ErrMalformedArchive, got %v", err)
		}
	})

	t.Run("path traversal rejected", func(t *testing.T) {
		archive := buildOuterTar(t, map[string][]byte{"subdir/../../evil": []byte("x")}, 0)
		if _, err := ReadMembers(bytes.NewReader(archive), 0); !errors.Is(err, ErrMalformedArchive) {
			t.Fatalf("traversal must fail, got %v", err)
		}
	})

	t.Run("absolute path normalized but unknown member", func(t *testing.T) {
		archive := buildOuterTar(t, map[string][]byte{"/etc/passwd": []byte("x")}, 0)
		if _, err := ReadMembers(bytes.NewReader(archive), 0); !errors.Is(err, ErrUnknownMember) {
			t.Fatalf("absolute path must be normalized then rejected as unknown, got %v", err)
		}
	})

	t.Run("non-regular entry rejected", func(t *testing.T) {
		archive := buildOuterTar(t, map[string][]byte{"metadata.gz": nil}, tar.TypeSymlink)
		if _, err := ReadMembers(bytes.NewReader(archive), 0); !errors.Is(err, ErrMalformedArchive) {
			t.Fatalf("symlink must fail, got %v", err)
		}
	})

	t.Run("missing required member", func(t *testing.T) {
		archive := buildOuterTar(t, map[string][]byte{"metadata.gz": gzipBytes([]byte("x"))}, 0)
		if _, err := ReadMembers(bytes.NewReader(archive), 0); !errors.Is(err, ErrMissingMember) {
			t.Fatalf("missing member must fail, got %v", err)
		}
	})

	t.Run("oversized member rejected", func(t *testing.T) {
		archive := buildOuterTar(t, map[string][]byte{"metadata.gz": make([]byte, 16)}, 0)
		if _, err := ReadMembers(bytes.NewReader(archive), 8); !errors.Is(err, ErrMalformedArchive) {
			t.Fatalf("oversized member must fail, got %v", err)
		}
	})
}

func TestVerifyChecksumsMalformed(t *testing.T) {
	base := validMembers(t)

	t.Run("invalid gzip", func(t *testing.T) {
		members := cloneMembers(base)
		members[MemberChecksums] = []byte("not gzip")
		if err := VerifyChecksums(members); !errors.Is(err, ErrMalformedChecksums) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing sha512 section", func(t *testing.T) {
		members := cloneMembers(base)
		members[MemberChecksums] = gzipBytes([]byte("---\nSHA1:\n  metadata.gz: abc\n"))
		if err := VerifyChecksums(members); !errors.Is(err, ErrMalformedChecksums) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("missing data entry", func(t *testing.T) {
		members := cloneMembers(base)
		members[MemberChecksums] = gzipBytes([]byte("---\nSHA512:\n  metadata.gz: " + strings.Repeat("a", 128) + "\n"))
		if err := VerifyChecksums(members); !errors.Is(err, ErrMalformedChecksums) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("bad hex digest", func(t *testing.T) {
		members := cloneMembers(base)
		members[MemberChecksums] = gzipBytes([]byte("---\nSHA512:\n  metadata.gz: zz\n  data.tar.gz: " + strings.Repeat("b", 128) + "\n"))
		if err := VerifyChecksums(members); !errors.Is(err, ErrMalformedChecksums) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("unparseable line and comments", func(t *testing.T) {
		members := cloneMembers(base)
		members[MemberChecksums] = gzipBytes([]byte("# a comment\nplain value without colon\n"))
		if err := VerifyChecksums(members); !errors.Is(err, ErrMalformedChecksums) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("quoted values and trailing comments accepted", func(t *testing.T) {
		metaSum := sha512Hex(base[MemberMetadata])
		dataSum := sha512Hex(base[MemberData])
		doc := "--- # leading document marker\n" +
			"SHA512:\n" +
			"  'metadata.gz': \"" + metaSum + "\" # trailing comment\n" +
			"  \"data.tar.gz\": '" + dataSum + "'\n"
		members := cloneMembers(base)
		members[MemberChecksums] = gzipBytes([]byte(doc))
		if err := VerifyChecksums(members); err != nil {
			t.Fatalf("quoted YAML should parse: %v", err)
		}
	})
}

func TestVerifyMalformedSignatures(t *testing.T) {
	chain := mustChain(t, "malformed-sig")

	t.Run("garbage sig bytes", func(t *testing.T) {
		members := validMembers(t)
		members[MemberMetadata+".sig"] = []byte("not der")
		if _, err := verifyPKCS7Detached(members[MemberMetadata+".sig"], members[MemberMetadata]); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("got %v", err)
		}
	})

	t.Run("empty signed data payload", func(t *testing.T) {
		emptyCI := pkcs7ContentInfo{ContentType: oidPKCS7SignedData}
		der, err := asn1.Marshal(emptyCI)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyPKCS7Detached(der, []byte("content")); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("empty signedData must fail, got %v", err)
		}
	})

	t.Run("trailing data after signature", func(t *testing.T) {
		sig, err := chain.signDetached([]byte("content"), rsaSHA256Algo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyPKCS7Detached(append(sig, 0x00), []byte("content")); !errors.Is(err, ErrMalformedSignature) {
			t.Fatalf("trailing bytes must fail, got %v", err)
		}
	})

	t.Run("message digest attribute mismatch", func(t *testing.T) {
		sig, err := chain.signDetached([]byte("content"), rsaSHA256Algo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyPKCS7Detached(sig, []byte("different content")); !errors.Is(err, ErrInvalidSignature) {
			t.Fatalf("mismatched content must fail, got %v", err)
		}
	})

	t.Run("rsa sha1 digest rejected", func(t *testing.T) {
		sig, err := chain.signDetached([]byte("content"), rsaSHA1Algo)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := verifyPKCS7Detached(sig, []byte("content")); !errors.Is(err, ErrUnsupportedAlgorithm) {
			t.Fatalf("sha1 must be rejected, got %v", err)
		}
	})

	t.Run("tampered signature bytes rejected", func(t *testing.T) {
		sig, err := chain.signDetached([]byte("content"), rsaSHA256Algo)
		if err != nil {
			t.Fatal(err)
		}
		sig[len(sig)-5] ^= 0xFF
		if _, err := verifyPKCS7Detached(sig, []byte("content")); err == nil {
			t.Fatal("corrupted signature must not verify")
		}
	})
}

func cloneMembers(in map[string][]byte) map[string][]byte {
	out := make(map[string][]byte, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func sha512Hex(data []byte) string {
	h := sha512.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
