package dsc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChecksumDisagreement(t *testing.T) {
	files := sampleFiles()

	t.Run("sha256 digest differs from recorded file hash", func(t *testing.T) {
		data := buildDSC(files)
		tampered := strings.Replace(string(data), sha256hex(files[0].data),
			strings.Repeat("f", sha256HexLen), 1)
		d, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if err != nil {
			t.Fatalf("malformed record should still parse: %v", err)
		}
		dir := t.TempDir()
		writeFixture(t, dir, files)
		if err := d.VerifyFiles(dir); !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("got %v, want ErrChecksumMismatch against actual file", err)
		}
	})

	t.Run("md5 and sha256 both recorded for the same real file", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, files)
		d := mustParse(t, buildDSC(files))
		if err := d.VerifyFiles(dir); err != nil {
			t.Fatalf("VerifyFiles: %v", err)
		}
	})

	t.Run("sha256 size disagrees with Files size", func(t *testing.T) {
		data := buildDSC(files)
		good := " " + sha256hex(files[0].data) + " " + itoa(len(files[0].data)) + " "
		bad := " " + sha256hex(files[0].data) + " " + itoa(len(files[0].data)+10) + " "
		tampered := strings.Replace(string(data), good, bad, 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("got %v, want ErrChecksumMismatch", err)
		}
	})

	t.Run("actual file content hash mismatch", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, files)
		if err := os.WriteFile(filepath.Join(dir, files[0].name),
			[]byte("tampered contents on disk"), 0o600); err != nil {
			t.Fatal(err)
		}
		d := mustParse(t, buildDSC(files))
		if err := d.VerifyFiles(dir); !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("got %v, want ErrChecksumMismatch", err)
		}
	})

	t.Run("actual file size mismatch", func(t *testing.T) {
		dir := t.TempDir()
		writeFixture(t, dir, files)
		path := filepath.Join(dir, files[1].name)
		if err := os.WriteFile(path, append(files[1].data, 'x'), 0o600); err != nil {
			t.Fatal(err)
		}
		d := mustParse(t, buildDSC(files))
		if err := d.VerifyFiles(dir); !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("got %v, want ErrChecksumMismatch", err)
		}
	})

	t.Run("missing referenced file", func(t *testing.T) {
		dir := t.TempDir()
		d := mustParse(t, buildDSC(files))
		if err := d.VerifyFiles(dir); !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("got %v, want ErrChecksumMismatch", err)
		}
	})
}

func TestDuplicateFileNameRejected(t *testing.T) {
	files := sampleFiles()
	f := files[0]

	t.Run("duplicate in Files", func(t *testing.T) {
		dup := "Files:\n" +
			" " + md5hex(f.data) + " " + itoa(len(f.data)) + " " + f.name + "\n" +
			" " + md5hex(f.data) + " " + itoa(len(f.data)) + " " + f.name + "\n"
		tampered := injectFieldBody(buildDSC(files), "Files:", dup)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})

	t.Run("duplicate in Checksums-Sha256", func(t *testing.T) {
		dup := "Checksums-Sha256:\n" +
			" " + sha256hex(f.data) + " " + itoa(len(f.data)) + " " + f.name + "\n" +
			" " + sha256hex(f.data) + " " + itoa(len(f.data)) + " " + f.name + "\n"
		tampered := injectFieldBody(buildDSC(files), "Checksums-Sha256:", dup)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})
}

func TestInvalidFileNames(t *testing.T) {
	for _, bad := range []string{"sub/dir/x.tar.gz", "../escape.tar", ".hidden.tar", "."} {
		files := []testFile{{name: bad, data: []byte("x")}}
		_, err := Parse(context.Background(), buildDSC(files), &fakeVerifier{})
		if !errors.Is(err, ErrField) {
			t.Fatalf("name %q: got %v, want ErrField", bad, err)
		}
	}
}

func TestChecksumCoverageGaps(t *testing.T) {
	files := sampleFiles()

	t.Run("sha256 names unknown file", func(t *testing.T) {
		data := buildDSC(files)
		extra := " " + strings.Repeat("a", sha256HexLen) + " 1 ghost.tar.gz\n"
		tampered := strings.Replace(string(data), "Checksums-Sha256:\n",
			"Checksums-Sha256:\n"+extra, 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})

	t.Run("missing sha256 for one file", func(t *testing.T) {
		data := buildDSC(files)
		line := " " + sha256hex(files[1].data) + " " + itoa(len(files[1].data)) +
			" " + files[1].name + "\n"
		tampered := strings.Replace(string(data), line, "", 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrChecksumMismatch) {
			t.Fatalf("got %v, want ErrChecksumMismatch", err)
		}
	})

	t.Run("duplicate Files field", func(t *testing.T) {
		data := buildDSC(files)
		block := "Files:\n " + md5hex(files[0].data) + " " +
			itoa(len(files[0].data)) + " " + files[0].name + "\n"
		tampered := strings.Replace(string(data), block, block+block, 1)
		_, err := Parse(context.Background(), []byte(tampered), &fakeVerifier{})
		if !errors.Is(err, ErrField) {
			t.Fatalf("got %v, want ErrField", err)
		}
	})
}

func buildSources(dscName string, dscData []byte, files []testFile) []byte {
	var b strings.Builder
	b.WriteString("Package: example\n")
	b.WriteString("Directory: pool/main/e/example\n")
	b.WriteString("Checksums-Sha256:\n")
	for _, f := range files {
		b.WriteString(" " + sha256hex(f.data) + " " + itoa(len(f.data)) + " " + f.name + "\n")
	}
	sum := sha256.Sum256(dscData)
	b.WriteString(" " + hex.EncodeToString(sum[:]) + " " + itoa(len(dscData)) + " " + dscName + "\n")
	return []byte(b.String())
}

func TestVerifyChainHappyPath(t *testing.T) {
	files := sampleFiles()
	dir := t.TempDir()
	writeFixture(t, dir, files)

	dscName := "example_1.0-1.dsc"
	data := buildDSC(files)
	if err := os.WriteFile(filepath.Join(dir, dscName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	sources := buildSources(dscName, data, files)

	d := mustParse(t, data)
	if err := d.VerifyChain(dir, sources, dscName); err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
}

func TestVerifyInSourcesFailures(t *testing.T) {
	files := sampleFiles()
	data := buildDSC(files)
	dscName := "example_1.0-1.dsc"

	t.Run("digest not published", func(t *testing.T) {
		sources := buildSources(dscName, append([]byte(nil), data...), files)
		// Replace the recorded .dsc digest with a different one.
		sum := sha256.Sum256(append(data, 'x'))
		want := sha256.Sum256(data)
		sources = []byte(strings.Replace(string(sources), hex.EncodeToString(want[:]),
			hex.EncodeToString(sum[:]), 1))
		d := mustParse(t, data)
		if err := d.VerifyInSources(sources, dscName); !errors.Is(err, ErrSources) {
			t.Fatalf("got %v, want ErrSources", err)
		}
	})

	t.Run("digest published under different name", func(t *testing.T) {
		sources := buildSources("other_1.0-1.dsc", data, files)
		d := mustParse(t, data)
		if err := d.VerifyInSources(sources, dscName); !errors.Is(err, ErrSources) {
			t.Fatalf("got %v, want ErrSources", err)
		}
	})

	t.Run("empty index", func(t *testing.T) {
		d := mustParse(t, data)
		if err := d.VerifyInSources(nil, dscName); !errors.Is(err, ErrSources) {
			t.Fatalf("got %v, want ErrSources", err)
		}
	})

	t.Run("invalid dsc name", func(t *testing.T) {
		d := mustParse(t, data)
		if err := d.VerifyInSources(nil, "../x.dsc"); !errors.Is(err, ErrSources) {
			t.Fatalf("got %v, want ErrSources", err)
		}
	})
}

func TestVerifierFuncAdapter(t *testing.T) {
	called := false
	v := VerifierFunc(func(_ context.Context, message []byte) error {
		called = true
		if len(message) == 0 {
			t.Fatal("empty message")
		}
		return nil
	})
	if _, err := Parse(context.Background(), buildDSC(sampleFiles()), v); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !called {
		t.Fatal("VerifierFunc was not called")
	}
}
