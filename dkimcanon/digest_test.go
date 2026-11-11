package dkimcanon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
)

func sha256Hex(t *testing.T, data []byte) string {
	t.Helper()
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// TestDigestIndependentOfChunking verifies the core relay requirement: the
// body hash is identical for every chunking of the same body, including
// boundaries that split CRLFs and WSP runs.
func TestDigestIndependentOfChunking(t *testing.T) {
	bodies := [][]byte{
		nil,
		[]byte(""),
		[]byte("\r\n"),
		[]byte("a\r\nb\r\n\r\n"),
		[]byte("  hi \t there \r\n\r\n\r\n"),
		[]byte("no terminating newline"),
		append(bytes.Repeat([]byte("Z"), 3000), []byte("\r\n")...),
	}

	for _, algo := range []Algorithm{Simple, Relaxed} {
		for _, body := range bodies {
			canon, err := Canonicalize(algo, body)
			if err != nil {
				t.Fatal(err)
			}
			want := sha256Hex(t, canon)

			for _, size := range chunkSizes {
				h := sha256.New()
				d := NewDigester(algo, h)
				if err := writeChunks(d, body, size); err != nil {
					t.Fatalf("algo=%v size=%d: %v", algo, size, err)
				}
				got, err := d.Sum()
				if err != nil {
					t.Fatalf("algo=%v size=%d: %v", algo, size, err)
				}
				if hex.EncodeToString(got) != want {
					t.Fatalf("algo=%v size=%d digest drift for body=%q", algo, size, body)
				}
			}

			// The one-shot helper must agree too.
			oneShot, err := BodyHash(algo, sha256.New, body)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(oneShot) != want {
				t.Fatalf("BodyHash mismatch for body=%q", body)
			}
		}
	}
}

// TestKnownBodyHashes pins the hashes of small canonicalized bodies. The
// inputs are first canonicalized, so these match the bh= computation rule of
// RFC 6376 section 3.7 (hash of the canonicalized body).
func TestKnownBodyHashes(t *testing.T) {
	cases := []struct {
		name string
		algo Algorithm
		body []byte
		bh   string
	}{
		{"empty simple", Simple, nil, sha256Hex(t, []byte("\r\n"))},
		{"empty relaxed", Relaxed, nil, sha256Hex(t, []byte{})},
		{"simple trailing blanks", Simple, []byte("Hello: Foo\r\n\r\n"),
			sha256Hex(t, []byte("Hello: Foo\r\n"))},
		{"relaxed whitespace", Relaxed, []byte("Hello: Foo \t\r\n\r\n"),
			sha256Hex(t, []byte("Hello: Foo\r\n"))},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := BodyHash(tc.algo, sha256.New, tc.body)
			if err != nil {
				t.Fatal(err)
			}
			if hex.EncodeToString(got) != tc.bh {
				t.Fatalf("got %s want %s", hex.EncodeToString(got), tc.bh)
			}

			// Verify accepts the same digest and rejects a tampered one.
			h := sha256.New()
			d := NewDigester(tc.algo, h)
			if _, err := d.Write(tc.body); err != nil {
				t.Fatal(err)
			}
			wantSum, _ := hex.DecodeString(tc.bh)
			if err := d.Verify(wantSum); err != nil {
				t.Fatalf("Verify rejected a valid digest: %v", err)
			}

			h2 := sha256.New()
			d2 := NewDigester(tc.algo, h2)
			if _, err := d2.Write(tc.body); err != nil {
				t.Fatal(err)
			}
			bad := append(append([]byte{}, wantSum...), 0)
			bad[0] ^= 0xFF
			if err := d2.Verify(bad); !errors.Is(err, ErrDigestMismatch) {
				t.Fatalf("want ErrDigestMismatch, got %v", err)
			}
		})
	}
}

// TestRelayWriteCallback wires the canonicalizer into a write callback that
// forwards canonical bytes while hashing them, mirroring a streaming relay.
func TestRelayWriteCallback(t *testing.T) {
	body := []byte("Subject: test\r\n\r\nbody line\r\n\r\n")

	var forwarded bytes.Buffer
	h := sha256.New()

	// The callback receives only canonical bytes; the hash sees the same
	// stream via the multi-writer digester.
	callback := WriteFunc(func(p []byte) error {
		_, err := forwarded.Write(p)
		return err
	})
	d := NewDigesterWithSink(Relaxed, h, callback)

	// Arbitrary cut points, including mid-CRLF.
	pos := 0
	for pos < len(body) {
		end := pos + 3
		if end > len(body) {
			end = len(body)
		}
		if _, err := d.Write(body[pos:end]); err != nil {
			t.Fatal(err)
		}
		pos = end
	}

	wantCanon, err := Canonicalize(Relaxed, body)
	if err != nil {
		t.Fatal(err)
	}

	sum, err := d.Sum()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(forwarded.Bytes(), wantCanon) {
		t.Fatalf("forwarded stream is not canonical:\n got %q\nwant %q",
			forwarded.Bytes(), wantCanon)
	}
	wantSum := sha256.New()
	wantSum.Write(wantCanon)
	if !bytes.Equal(sum, wantSum.Sum(nil)) {
		t.Fatal("callback stream and hash diverged")
	}
}

// TestIllegalNewlineIsNeverDeliveredVerified proves an illegal newline stops
// the pipeline: the forwarding callback never sees a verifiable stream, the
// digester keeps the error, and Verify fails rather than returning success.
func TestIllegalNewlineIsNeverDeliveredVerified(t *testing.T) {
	illegalBodies := [][]byte{
		[]byte("good line\r\nbad\nline\r\n"),
		[]byte("bare\rcr\r\n"),
		[]byte("trailing dangling\r"),
	}

	for _, body := range illegalBodies {
		var delivered bytes.Buffer
		h := sha256.New()
		d := NewDigesterWithSink(Simple, h, &delivered)

		var writeErr error
		for i := 1; i <= len(body); i++ {
			if _, err := d.Write(body[i-1 : i]); err != nil {
				writeErr = err
				break
			}
		}
		closeErr := d.Close()
		if !errors.Is(writeErr, ErrInvalidNewline) && !errors.Is(closeErr, ErrInvalidNewline) {
			t.Fatalf("body=%q: want ErrInvalidNewline, got write=%v close=%v",
				body, writeErr, closeErr)
		}

		// Verify must never succeed for an un-canonicalizable body.
		if err := d.Verify(h.Sum(nil)); !errors.Is(err, ErrInvalidNewline) {
			t.Fatalf("body=%q: Verify returned %v, want ErrInvalidNewline", body, err)
		}

		// A relay gating delivery on verification must reject the message.
		if err := d.Verify(h.Sum(nil)); err == nil {
			t.Fatalf("body=%q would have been delivered as verified", body)
		}

		// Further writes remain blocked by the sticky error.
		if _, err := d.Write([]byte("more\r\n")); !errors.Is(err, ErrInvalidNewline) {
			t.Fatalf("write after failure returned %v, want ErrInvalidNewline", err)
		}
	}
}

func TestDigestWithBodyLengthLimit(t *testing.T) {
	body := []byte("alpha\r\nbeta\r\ngamma\r\n")

	full, err := Canonicalize(Simple, body)
	if err != nil {
		t.Fatal(err)
	}
	const limit int64 = 9
	if int64(len(full)) <= limit {
		t.Fatalf("test setup: full canonical body (%d) must exceed limit %d", len(full), limit)
	}

	for _, size := range chunkSizes {
		h := sha256.New()
		d := NewDigester(Simple, h, WithBodyLength(limit))
		if err := writeChunks(d, body, size); err != nil {
			t.Fatal(err)
		}
		sum, err := d.Sum()
		if err != nil {
			t.Fatal(err)
		}
		if !d.Truncated() || d.Emitted() != limit {
			t.Fatalf("size=%d: Truncated=%v Emitted=%d", size, d.Truncated(), d.Emitted())
		}
		want := sha256.New()
		want.Write(full[:limit])
		if !bytes.Equal(sum, want.Sum(nil)) {
			t.Fatalf("size=%d: limited digest mismatch", size)
		}
	}
}
