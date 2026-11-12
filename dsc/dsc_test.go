package dsc

import (
	"context"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestParseHappyPath(t *testing.T) {
	files := sampleFiles()
	data := buildDSC(files)
	v := &fakeVerifier{}

	d, err := Parse(context.Background(), data, v)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if v.calls != 1 {
		t.Fatalf("verifier called %d times, want 1", v.calls)
	}
	if string(v.got) != string(data) {
		t.Fatal("verifier did not receive the exact armor block")
	}
	if d.Value("Source") != "example" {
		t.Fatalf("Source = %q", d.Value("Source"))
	}
	if len(d.Files) != len(files) {
		t.Fatalf("got %d entries, want %d", len(d.Files), len(files))
	}
	for i, want := range files {
		got := d.Files[i]
		if got.Name != want.name || got.Size != int64(len(want.data)) {
			t.Fatalf("entry %d = %+v", i, got)
		}
		if hex.EncodeToString(got.SHA256) != sha256hex(want.data) {
			t.Fatalf("sha256 mismatch for %s", want.name)
		}
		if hex.EncodeToString(got.MD5) != md5hex(want.data) {
			t.Fatalf("md5 mismatch for %s", want.name)
		}
		if hex.EncodeToString(got.SHA1) != sha1hex(want.data) {
			t.Fatalf("sha1 mismatch for %s", want.name)
		}
	}
	if !strings.HasPrefix(string(d.SignedContent()), "Format:") {
		t.Fatalf("unexpected signed content: %q", d.SignedContent())
	}
	if string(d.Raw()) != string(data) {
		t.Fatal("Raw() changed the input")
	}
}

func TestParseTrailingNewlineAccepted(t *testing.T) {
	data := append(buildDSC(sampleFiles()), '\n')
	if _, err := Parse(context.Background(), data, &fakeVerifier{}); err != nil {
		t.Fatalf("single trailing newline should be accepted: %v", err)
	}
}

func TestOutsideBlockRejected(t *testing.T) {
	good := buildDSC(sampleFiles())
	cases := map[string][]byte{
		"text before block":     append([]byte("Files:\n deadbeef 1 x\n"), good...),
		"text after footer":     append(append([]byte(nil), good...), []byte("Files:\n "+strings.Repeat("a", 32)+" 999 evil.tar.gz\n")...),
		"two trailing newlines": append(append([]byte(nil), good...), '\n', '\n'),
		"garbage after block":   append(append([]byte(nil), good...), 'x'),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(context.Background(), data, &fakeVerifier{})
			if !errors.Is(err, ErrOutsideBlock) {
				t.Fatalf("got %v, want ErrOutsideBlock", err)
			}
		})
	}
}

func TestInjectedFieldAfterFooterIsNotParsed(t *testing.T) {
	files := sampleFiles()
	good := buildDSC(files)
	injected := append(append([]byte(nil), good...),
		[]byte("Files:\n "+strings.Repeat("a", 32)+" 999 evil.tar.gz\n")...)

	_, err := Parse(context.Background(), injected, &fakeVerifier{})
	if !errors.Is(err, ErrOutsideBlock) {
		t.Fatalf("injected trailing field: got %v, want ErrOutsideBlock", err)
	}

	d := mustParse(t, good)
	for _, entry := range d.Files {
		if strings.Contains(entry.Name, "evil") {
			t.Fatal("block-external field leaked into parsed entries")
		}
	}
}

func TestSignatureFailureWrapsErrSignature(t *testing.T) {
	v := &fakeVerifier{fail: true}
	_, err := Parse(context.Background(), buildDSC(sampleFiles()), v)
	if !errors.Is(err, ErrSignature) {
		t.Fatalf("got %v, want ErrSignature", err)
	}
}

func TestDashUnescaping(t *testing.T) {
	raw := buildDSC(sampleFiles(), "- Dash-Line: - leading dash content")
	d := mustParse(t, raw)
	if got := d.Value("Dash-Line"); got != "- leading dash content" {
		t.Fatalf("dash escape not reversed: %q", got)
	}
	if !strings.Contains(string(d.SignedContent()), "Dash-Line: - leading dash content") {
		t.Fatalf("signed content should hold the unescaped line, got %q", d.SignedContent())
	}

	bad := bytesReplace(raw, []byte("- Dash-Line:"), []byte("-Dash-Line:"))
	if _, err := Parse(context.Background(), bad, &fakeVerifier{}); !errors.Is(err, ErrMalformed) {
		t.Fatalf("bare-dash line: got %v, want ErrMalformed", err)
	}
}

func TestMalformedArmor(t *testing.T) {
	good := buildDSC(sampleFiles())
	cases := map[string][]byte{
		"no header":           []byte("Format: 3.0\n"),
		"no footer":           bytesReplace(good, []byte(sigEnd), []byte("-----END PGP FOO-----")),
		"bad base64 body":     bytesReplace(good, []byte("iQEzBAABCAAd"), []byte("not base64!!!")),
		"crlf":                bytesReplaceAll(good, []byte("\n"), []byte("\r\n")),
		"no empty armor line": bytesReplace(good, []byte("Hash: SHA256\n\n"), []byte("Hash: SHA256\n")),
		"no signature header": bytesReplace(good, []byte(sigBegin), []byte("-----BEGIN PGP FOO-----")),
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Parse(context.Background(), data, &fakeVerifier{})
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, ErrMalformed) && !errors.Is(err, ErrOutsideBlock) {
				t.Fatalf("got %v, want ErrMalformed or ErrOutsideBlock", err)
			}
		})
	}
}

func bytesReplaceAll(s, old, new []byte) []byte {
	return bytesReplace(s, old, new)
}

func TestVerifyFilesHappyPath(t *testing.T) {
	files := sampleFiles()
	dir := t.TempDir()
	writeFixture(t, dir, files)

	d := mustParse(t, buildDSC(files))
	if err := d.VerifyFiles(dir); err != nil {
		t.Fatalf("VerifyFiles: %v", err)
	}
}
