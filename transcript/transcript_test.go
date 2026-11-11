package transcript

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"testing"
)

// manualTranscript renders the exact canonical bytes of a sequence of
// frames plus the closing end frame, independently of Builder.
func manualTranscript(frames []frame, endPhase uint64) []byte {
	var out []byte
	for _, f := range frames {
		out = appendFrame(out, f.phase, f.tag, f.typ, f.payload)
	}
	out = appendFrame(out, endPhase, endTag, typeUint,
		appendUvarint(nil, uint64(len(frames))))
	return out
}

func digestOfFrames(frames []frame) [sha256.Size]byte {
	return sha256.Sum256(manualTranscript(frames, 0))
}

// TestCollisionPairs constructs the exact ambiguity families that break
// bare concatenation encodings and proves each pair yields a distinct
// canonical digest.
func TestCollisionPairs(t *testing.T) {
	cases := []struct {
		name string
		a, b []frame
	}{
		{
			name: "boundary shift",
			a: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte{1, 2}},
				{phase: 0, tag: 2, typ: typeBytes, payload: []byte{3}},
			},
			b: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte{1}},
				{phase: 0, tag: 2, typ: typeBytes, payload: []byte{2, 3}},
			},
		},
		{
			name: "empty field boundary",
			a: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: nil},
				{phase: 0, tag: 2, typ: typeBytes, payload: []byte("abc")},
			},
			b: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte("a")},
				{phase: 0, tag: 2, typ: typeBytes, payload: []byte("bc")},
			},
		},
		{
			name: "type confusion bytes vs uint",
			a: []frame{
				{phase: 0, tag: 1, typ: typeUint, payload: appendUvarint(nil, 16)},
			},
			b: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte{16}},
			},
		},
		{
			name: "same fields different phase",
			a: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte("v")},
			},
			b: []frame{
				{phase: 1, tag: 1, typ: typeBytes, payload: []byte("v")},
			},
		},
		{
			name: "one field vs two fields same bytes",
			a: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte("xy")},
			},
			b: []frame{
				{phase: 0, tag: 1, typ: typeBytes, payload: []byte("x")},
				{phase: 0, tag: 2, typ: typeBytes, payload: []byte("y")},
			},
		},
	}

	sums := map[[sha256.Size]byte]string{}
	for _, tc := range cases {
		sa := digestOfFrames(tc.a)
		sb := digestOfFrames(tc.b)
		if sa == sb {
			t.Fatalf("%s: distinct structures share digest %x", tc.name, sa)
		}
		if prior, ok := sums[sa]; ok {
			t.Fatalf("%s/a collides with %s", tc.name, prior)
		}
		sums[sa] = tc.name + "/a"
		if prior, ok := sums[sb]; ok {
			t.Fatalf("%s/b collides with %s", tc.name, prior)
		}
		sums[sb] = tc.name + "/b"
	}
}

// TestBuilderMatchesManualEncoding verifies Builder hashes exactly the
// documented canonical byte stream, including the end frame.
func TestBuilderMatchesManualEncoding(t *testing.T) {
	b := New()
	if err := b.WriteBytes(7, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := b.BeginPhase(2); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteUint(3, 128); err != nil {
		t.Fatal(err)
	}
	if err := b.WriteInt(4, -2); err != nil {
		t.Fatal(err)
	}
	d, err := b.Close()
	if err != nil {
		t.Fatal(err)
	}
	frames := []frame{
		{phase: 0, tag: 7, typ: typeBytes, payload: []byte("hello")},
		{phase: 2, tag: 3, typ: typeUint, payload: appendUvarint(nil, 128)},
		{phase: 2, tag: 4, typ: typeInt, payload: appendUvarint(nil, encodeZigZag(-2))},
	}
	want := manualTranscript(frames, 2)
	if got := sha256.Sum256(want); got != d.Sum {
		t.Fatalf("digest mismatch\n got %x\nwant %x", got, d.Sum)
	}
	if d.EncodedLen != len(want) || d.Fields != 3 || d.Phases != 2 {
		t.Fatalf("digest metadata mismatch: %+v len want %d", d, len(want))
	}
}

// TestAllChunkPositions streams one field using every possible cut
// position (including zero-byte writes) and checks the digest is
// identical to the one-shot encoding.
func TestAllChunkPositions(t *testing.T) {
	payload := []byte("the quick brown fox jumps over 0123456789")
	oneShot := New()
	if err := oneShot.WriteBytes(1, payload); err != nil {
		t.Fatal(err)
	}
	want, err := oneShot.Close()
	if err != nil {
		t.Fatal(err)
	}

	for cut := 0; cut <= len(payload); cut++ {
		t.Run(fmt.Sprintf("cut=%d", cut), func(t *testing.T) {
			b := New()
			fw, err := b.BeginField(1, len(payload))
			if err != nil {
				t.Fatal(err)
			}
			if n, err := fw.Write(payload[:cut]); err != nil || n != cut {
				t.Fatalf("first chunk: n=%d err=%v", n, err)
			}
			if n, err := fw.Write(nil); err != nil || n != 0 {
				t.Fatalf("empty chunk: n=%d err=%v", n, err)
			}
			if n, err := fw.Write(payload[cut:]); err != nil || n != len(payload)-cut {
				t.Fatalf("second chunk: n=%d err=%v", n, err)
			}
			if err := fw.Close(); err != nil {
				t.Fatal(err)
			}
			got, err := b.Close()
			if err != nil {
				t.Fatal(err)
			}
			if got.Sum != want.Sum {
				t.Fatalf("cut %d: chunked digest %x != one-shot %x", cut, got.Sum, want.Sum)
			}
			if got.EncodedLen != want.EncodedLen {
				t.Fatalf("cut %d: encoded length %d != %d", cut, got.EncodedLen, want.EncodedLen)
			}
		})
	}
}

// TestChunkFailuresAtEveryPosition checks that oversize writes and
// premature close fail at every position without mutating builder state.
func TestChunkFailuresAtEveryPosition(t *testing.T) {
	const size = 24
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i)
	}

	for cut := 0; cut <= size; cut++ {
		t.Run(fmt.Sprintf("cut=%d", cut), func(t *testing.T) {
			b := New()
			fw, err := b.BeginField(1, size)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fw.Write(payload[:cut]); err != nil {
				t.Fatal(err)
			}

			remaining := size - cut
			oversize := make([]byte, remaining+1)
			if _, err := fw.Write(oversize); !errors.Is(err, ErrFieldTooLarge) {
				t.Fatalf("expected ErrFieldTooLarge, got %v", err)
			}

			if cut < size {
				if err := fw.Close(); err == nil {
					t.Fatal("premature close succeeded")
				}
				if b.FieldCount() != 0 {
					t.Fatal("failed close counted a field")
				}
				if _, err := fw.Write(payload[cut:]); err != nil {
					t.Fatal(err)
				}
			}
			if err := fw.Close(); err != nil {
				t.Fatal(err)
			}
			d, err := b.Close()
			if err != nil {
				t.Fatal(err)
			}
			if d.Fields != 1 {
				t.Fatalf("fields = %d, want 1", d.Fields)
			}
		})
	}
}
