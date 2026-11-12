package dsc

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInjectionIntoArmorAndSignature(t *testing.T) {
	good := buildDSC(sampleFiles())

	t.Run("injected line before armor header empty line", func(t *testing.T) {
		tampered := strings.Replace(string(good), "Hash: SHA256\n\n",
			"Hash: SHA256\nEvil: yes\n\n", 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if err == nil {
			t.Fatal("expected armor injection to be rejected")
		}
	})

	t.Run("injected field inside signature armor headers", func(t *testing.T) {
		tampered := strings.Replace(string(good), sigBegin+"\n\n",
			sigBegin+"\nEvil-Header: evil\n\n", 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v, want ErrMalformed", err)
		}
	})

	t.Run("standard comment armor header accepted", func(t *testing.T) {
		tampered := strings.Replace(string(good), sigBegin+"\n\n",
			sigBegin+"\nComment: harmless\n\n", 1)
		if _, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{}); err != nil {
			t.Fatalf("standard armor header should be accepted: %v", err)
		}
	})

	t.Run("injected text inside signature base64 body", func(t *testing.T) {
		tampered := strings.Replace(string(good),
			"iQEzBAABCAAdFiEEdummyBase64SignatureBodyForTesting00=",
			"iQEzBAABCAAdFiEEdummyBase64SignatureBodyForTesting00=\nevil line here", 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("got %v, want ErrMalformed", err)
		}
	})
}

func TestContextPropagation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	v := VerifierFunc(func(got context.Context, _ []byte) error {
		return got.Err()
	})
	_, err := Parse(ctx, buildDSC(sampleFiles()), v)
	if !errors.Is(err, ErrSignature) || !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want ErrSignature wrapping context.Canceled", err)
	}
}

func TestSymlinkReferentRejected(t *testing.T) {
	files := sampleFiles()
	dir := t.TempDir()
	writeFixture(t, dir, files)

	target := filepath.Join(dir, files[0].name)
	link := filepath.Join(dir, files[1].name)
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	d := mustParse(t, buildDSC(files))
	if err := d.VerifyFiles(dir); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("got %v, want ErrChecksumMismatch for symlink", err)
	}
}

func TestMissingRequiredFields(t *testing.T) {
	files := sampleFiles()

	t.Run("no Files field", func(t *testing.T) {
		data := buildDSC(files)
		tampered := stripField(data, "Files:")
		if _, err := Parse(context.Background(), tampered, &fakeVerifier{}); !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})

	t.Run("no Checksums-Sha256 field", func(t *testing.T) {
		data := buildDSC(files)
		tampered := stripField(data, "Checksums-Sha256:")
		if _, err := Parse(context.Background(), tampered, &fakeVerifier{}); !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})

	t.Run("invalid hex digest", func(t *testing.T) {
		data := buildDSC(files)
		tampered := strings.Replace(string(data), sha256hex(files[0].data),
			strings.Repeat("z", sha256HexLen), 1)
		if _, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{}); !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})
}

// stripField removes a "Header:" line and its folded continuation lines from
// the signed cleartext.
func stripField(data []byte, header string) []byte {
	lines := strings.Split(string(data), "\n")
	out := lines[:0]
	skipping := false
	for _, line := range lines {
		if strings.HasPrefix(line, header) {
			skipping = true
			continue
		}
		if skipping && strings.HasPrefix(line, " ") {
			continue
		}
		skipping = false
		out = append(out, line)
	}
	return []byte(strings.Join(out, "\n"))
}

func TestFieldNameCaseInsensitive(t *testing.T) {
	files := sampleFiles()
	data := []byte(strings.Replace(string(buildDSC(files)), "Source: example",
		"Source: example", 1))
	d := mustParse(t, data)
	if got := d.Value("source"); got != "example" {
		t.Fatalf("case-insensitive lookup = %q", got)
	}
	if got := d.Value("version"); got != "1.0-1" {
		t.Fatalf("version lookup = %q", got)
	}
}
