package sframe

import (
	"bytes"
	"strings"
	"testing"
)

const (
	secretPassword = "s3cret-PA55word"
	secretToken    = "tok-live-9f8e7d6c"
)

func redactor() *Redactor {
	return &Redactor{
		Paths:  []string{"user.credentials.password", "user.credentials.token"},
		Strict: true,
	}
}

func TestRedactNestedAndReparse(t *testing.T) {
	orig := testFrame(7)
	out, err := redactor().Redact(orig)
	if err != nil {
		t.Fatalf("Redact: %v", err)
	}

	// Frame still parses: recomputed length prefix and CRC are consistent.
	hdr, payload, err := SplitFrame(out)
	if err != nil {
		t.Fatalf("redacted frame does not parse: %v", err)
	}
	// Timing and ordering metadata preserved for replayability.
	if hdr.Seq != 7 || hdr.Timestamp != 1_758_000_000_000_000_007 {
		t.Fatalf("header changed: %+v", hdr)
	}

	fields, err := ParsePayload(payload)
	if err != nil {
		t.Fatalf("redacted payload does not parse: %v", err)
	}
	dump := Dump(fields)
	for _, secret := range []string{secretPassword, secretToken} {
		if strings.Contains(dump, secret) || bytes.Contains(out, []byte(secret)) {
			t.Fatalf("secret still present after redaction")
		}
	}
	// Non-sensitive data survives, including the unknown extension field.
	if !bytes.Contains(out, []byte("alice")) || !bytes.Contains(out, []byte("ext-payload-bytes")) {
		t.Fatal("non-sensitive fields were not preserved")
	}
	// Replacement has a different length than the originals, proving length
	// recomputation works.
	if len(out) == len(orig) {
		t.Fatal("expected frame size to change after redaction")
	}
}

func TestRedactLengthChangeBothDirections(t *testing.T) {
	// Short secret: replacement grows the frame.
	short := MarshalObject([]*Field{{Name: "pw", Type: TypeString, Raw: []byte("ab")}})
	f1 := EncodeFrame(Header{Version: Version1, Seq: 1}, short)
	out1, err := (&Redactor{Paths: []string{"pw"}, Strict: true}).Redact(f1)
	if err != nil {
		t.Fatal(err)
	}
	if len(out1) <= len(f1) {
		t.Fatal("expected frame to grow")
	}
	if _, _, err := SplitFrame(out1); err != nil {
		t.Fatalf("grown frame invalid: %v", err)
	}

	// Long secret: replacement shrinks the frame.
	long := MarshalObject([]*Field{{Name: "pw", Type: TypeString, Raw: []byte(strings.Repeat("z", 200))}})
	f2 := EncodeFrame(Header{Version: Version1, Seq: 2}, long)
	out2, err := (&Redactor{Paths: []string{"pw"}, Strict: true}).Redact(f2)
	if err != nil {
		t.Fatal(err)
	}
	if len(out2) >= len(f2) {
		t.Fatal("expected frame to shrink")
	}
	if _, _, err := SplitFrame(out2); err != nil {
		t.Fatalf("shrunk frame invalid: %v", err)
	}
}

func TestStrictMissingPathRejectsOutput(t *testing.T) {
	r := &Redactor{Paths: []string{"user.credentials.nonexistent"}, Strict: true}
	out, err := r.Redact(testFrame(1))
	if err == nil {
		t.Fatal("expected strict-mode error")
	}
	if out != nil {
		t.Fatal("strict mode must not emit output on missing path")
	}
	if !strings.Contains(err.Error(), "user.credentials.nonexistent") {
		t.Fatalf("error should name the missing path: %v", err)
	}
}

func TestNonStrictMissingPathPassesThrough(t *testing.T) {
	r := &Redactor{Paths: []string{"nope"}, Strict: false}
	out, err := r.Redact(testFrame(1))
	if err != nil {
		t.Fatalf("non-strict should not fail: %v", err)
	}
	if _, payload, err := SplitFrame(out); err != nil {
		t.Fatalf("output invalid: %v", err)
	} else if !bytes.Contains(payload, []byte(secretPassword)) {
		t.Fatal("non-strict pass-through should leave other fields intact")
	}
}

func TestUnknownExtensionCannotLeakSecret(t *testing.T) {
	// An unknown extension field happens to embed the secret bytes. The
	// leak guard must reject the frame rather than forward them.
	payload := MarshalObject([]*Field{
		{Name: "pw", Type: TypeString, Raw: []byte(secretPassword)},
		{Name: "x-ext-blob", Type: TypeBytes, Raw: []byte("prefix-" + secretPassword + "-suffix")},
	})
	frame := EncodeFrame(Header{Version: Version1, Seq: 1}, payload)
	r := &Redactor{Paths: []string{"pw"}, Strict: true}
	out, err := r.Redact(frame)
	if err == nil {
		t.Fatal("expected leak-guard rejection")
	}
	if out != nil {
		t.Fatal("no output may be emitted when the leak guard trips")
	}
}

func TestErrorsNeverContainSecrets(t *testing.T) {
	// Corrupt a frame carrying secrets; the error must not echo them.
	frame := testFrame(1)
	frame[len(frame)-1] ^= 0xff
	_, err := redactor().Redact(frame)
	if err == nil {
		t.Fatal("expected error")
	}
	for _, secret := range []string{secretPassword, secretToken} {
		if strings.Contains(err.Error(), secret) {
			t.Fatalf("error leaks secret: %v", err)
		}
	}

	// Same for TLV-level corruption.
	bad := testFrame(1)
	bad[HeaderSize+1] ^= 0xff // scramble first field name byte
	if _, err := redactor().Redact(bad); err != nil {
		for _, secret := range []string{secretPassword, secretToken} {
			if strings.Contains(err.Error(), secret) {
				t.Fatalf("error leaks secret: %v", err)
			}
		}
	}
}

func TestRedactedStreamReplaysInOrder(t *testing.T) {
	// Redact a 3-frame stream and confirm sequence/timestamps are untouched
	// and frames decode in the original order.
	p := NewParser(1 << 20)
	r := redactor()
	var stream []byte
	for i := uint64(1); i <= 3; i++ {
		stream = append(stream, testFrame(i)...)
	}
	frames, err := p.Feed(stream)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	for _, f := range frames {
		rf, err := r.Redact(f)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, rf...)
	}
	check := NewParser(1 << 20)
	got, err := check.Feed(out)
	if err != nil {
		t.Fatalf("redacted stream does not parse: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3", len(got))
	}
	for i, f := range got {
		hdr, _, err := SplitFrame(f)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Seq != uint64(i+1) || hdr.Timestamp != 1_758_000_000_000_000_000+int64(i+1) {
			t.Fatalf("frame %d: header not preserved: %+v", i, hdr)
		}
	}
}
