package dsc

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeVerifier accepts every well-formed block unless fail is set, and
// records the exact bytes it was asked to verify.
type fakeVerifier struct {
	fail  bool
	got   []byte
	calls int
	err   error
}

func (f *fakeVerifier) VerifyClearsigned(_ context.Context, message []byte) error {
	f.calls++
	f.got = append([]byte(nil), message...)
	if f.fail {
		if f.err == nil {
			f.err = errors.New("bad signature")
		}
		return f.err
	}
	return nil
}

type testFile struct {
	name string
	data []byte
}

func md5Sum(b []byte) []byte {
	sum := md5.Sum(b)
	return sum[:]
}

func sha1Sum(b []byte) []byte {
	sum := sha1.Sum(b)
	return sum[:]
}

func md5hex(b []byte) string { return hex.EncodeToString(md5Sum(b)) }

func sha1hex(b []byte) string { return hex.EncodeToString(sha1Sum(b)) }

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func itoa(n int) string { return strconv.Itoa(n) }

func bytesReplace(s, old, new []byte) []byte { return bytes.ReplaceAll(s, old, new) }

// buildDSC assembles a syntactically valid cleartext-signed .dsc over the
// given files. The signature body is dummy base64; only the structural
// parser and the injected verifier care about it.
func buildDSC(files []testFile, extraFields ...string) []byte {
	var filesLines, sha256Lines, sha1Lines []string
	for _, f := range files {
		filesLines = append(filesLines, " "+md5hex(f.data)+" "+itoa(len(f.data))+" "+f.name)
		sha256Lines = append(sha256Lines, " "+sha256hex(f.data)+" "+itoa(len(f.data))+" "+f.name)
		sha1Lines = append(sha1Lines, " "+sha1hex(f.data)+" "+itoa(len(f.data))+" "+f.name)
	}

	var clear strings.Builder
	clear.WriteString("Format: 3.0 (quilt)\n")
	clear.WriteString("Source: example\n")
	clear.WriteString("Version: 1.0-1\n")
	for _, field := range extraFields {
		clear.WriteString(field)
		if !strings.HasSuffix(field, "\n") {
			clear.WriteByte('\n')
		}
	}
	clear.WriteString("Files:\n")
	clear.WriteString(strings.Join(filesLines, "\n") + "\n")
	clear.WriteString("Checksums-Sha256:\n")
	clear.WriteString(strings.Join(sha256Lines, "\n") + "\n")
	clear.WriteString("Checksums-Sha1:\n")
	clear.WriteString(strings.Join(sha1Lines, "\n") + "\n")

	var b strings.Builder
	b.WriteString(beginMarker + "\n")
	b.WriteString("Hash: SHA256\n\n")
	b.WriteString(clear.String())
	b.WriteString("\n" + sigBegin + "\n\n")
	b.WriteString("iQEzBAABCAAdFiEEdummyBase64SignatureBodyForTesting00=\n")
	b.WriteString(sigEnd + "\n")
	return []byte(b.String())
}

func sampleFiles() []testFile {
	return []testFile{
		{name: "example_1.0.orig.tar.gz", data: []byte("upstream tarball contents")},
		{name: "example_1.0-1.debian.tar.xz", data: []byte("debian packaging contents")},
	}
}

func mustParse(t *testing.T, data []byte) *DSC {
	t.Helper()
	d, err := Parse(context.Background(), data, &fakeVerifier{})
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	return d
}

func writeFixture(t *testing.T, dir string, files []testFile) {
	t.Helper()
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(dir, f.name), f.data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// injectFieldBody replaces a "Name:\n ..." field block inside the signed
// cleartext with a hand-built replacement block.
func injectFieldBody(data []byte, header, replacement string) string {
	s := string(data)
	start := strings.Index(s, header)
	if start < 0 {
		panic("header not found: " + header)
	}
	rest := s[start:]
	lines := strings.Split(rest, "\n")
	end := 1
	for end < len(lines) && strings.HasPrefix(lines[end], " ") {
		end++
	}
	return s[:start] + replacement + strings.Join(lines[end:], "\n")
}
