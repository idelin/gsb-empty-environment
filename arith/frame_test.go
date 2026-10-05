package arith

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/rand"
	"testing"
)

func uv(v uint64) []byte {
	var b [binary.MaxVarintLen64]byte
	n := binary.PutUvarint(b[:], v)
	return b[:n]
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

// sampleFrame returns a valid small frame for mutation tests.
func sampleFrame(t *testing.T) []byte {
	t.Helper()
	labels := []string{"x", "y", "x", "x", "y", "x", "y", "y"}
	bits := []byte{0, 1, 1, 0, 1, 0, 0, 1}
	frame, err := EncodeFrame(labels, bits)
	if err != nil {
		t.Fatal(err)
	}
	return frame
}

func expectErr(t *testing.T, frame []byte, want error) {
	t.Helper()
	_, _, err := DecodeFrame(frame)
	if !errors.Is(err, want) {
		t.Fatalf("DecodeFrame: got err %v, want %v", err, want)
	}
}

func TestRejectBadMagic(t *testing.T) {
	frame := sampleFrame(t)
	frame[0] ^= 0xFF
	expectErr(t, frame, ErrBadMagic)
}

func TestRejectTruncation(t *testing.T) {
	frame := sampleFrame(t)
	for _, cut := range []int{0, 2, 5, len(frame) / 2, len(frame) - 5, len(frame) - 1} {
		expectErr(t, frame[:cut], ErrTruncated)
	}
}

func TestRejectTrailingGarbage(t *testing.T) {
	frame := append(sampleFrame(t), 0x00)
	expectErr(t, frame, ErrTrailing)
}

func TestRejectChecksumMismatch(t *testing.T) {
	frame := sampleFrame(t)
	// Flip a bit inside the payload (before the 4-byte CRC trailer).
	frame[len(frame)-6] ^= 0x40
	expectErr(t, frame, ErrChecksum)
}

func TestRejectLimits(t *testing.T) {
	// Bit count over the limit.
	expectErr(t, cat(magic, uv(MaxBits+1)), ErrLimit)
	// Context count over the limit.
	expectErr(t, cat(magic, uv(1), uv(MaxContexts+1)), ErrLimit)
	// Payload length over the sanity bound for n=1 (K==1: no label seq).
	expectErr(t, cat(magic, uv(1), uv(1), uv(1), []byte("a"), uv(1000)), ErrLimit)
}

func TestRejectBadContexts(t *testing.T) {
	// Bits present but zero contexts.
	expectErr(t, cat(magic, uv(1), uv(0)), ErrContext)
	// Contexts present but zero bits.
	expectErr(t, cat(magic, uv(0), uv(1), uv(1), []byte("a")), ErrContext)
	// Empty label.
	expectErr(t, cat(magic, uv(1), uv(1), uv(0)), ErrContext)
	// Oversized label.
	expectErr(t, cat(magic, uv(1), uv(1), uv(MaxLabelLen+1)), ErrContext)
	// Duplicate labels.
	dup := cat(magic, uv(2), uv(2), uv(1), []byte("a"), uv(1), []byte("a"))
	expectErr(t, dup, ErrContext)
	// Context index out of range (K=2 so the label sequence is stored).
	expectErr(t, cat(magic, uv(1), uv(2), uv(1), []byte("a"), uv(1), []byte("b"), uv(2)), ErrContext)
}

func TestRejectTruncatedPayload(t *testing.T) {
	// Header claims 10 payload bytes, only 3 + CRC follow (K==1: no label seq).
	frame := cat(magic, uv(1), uv(1), uv(1), []byte("a"), uv(10), []byte{1, 2, 3})
	expectErr(t, frame, ErrTruncated)
}

func TestEncoderInputValidation(t *testing.T) {
	e := NewEncoder()
	if err := e.EncodeBit("ok", 2); !errors.Is(err, ErrBit) {
		t.Fatalf("bit=2: got %v", err)
	}
	if err := e.EncodeBit("", 0); !errors.Is(err, ErrContext) {
		t.Fatalf("empty label: got %v", err)
	}
	long := string(bytes.Repeat([]byte("z"), MaxLabelLen+1))
	if err := e.EncodeBit(long, 0); !errors.Is(err, ErrContext) {
		t.Fatalf("long label: got %v", err)
	}
	if _, err := EncodeFrame([]string{"a"}, nil); !errors.Is(err, ErrState) {
		t.Fatalf("length mismatch: got %v", err)
	}
	if err := e.EncodeBit("ok", 1); err != nil {
		t.Fatal(err)
	}
	e.Finish()
	if err := e.EncodeBit("ok", 0); !errors.Is(err, ErrState) {
		t.Fatalf("encode after finish: got %v", err)
	}
	if _, err := e.Snapshot(); !errors.Is(err, ErrState) {
		t.Fatalf("snapshot after finish: got %v", err)
	}
}

func TestPauseResumeByteIdentical(t *testing.T) {
	rng := rand.New(rand.NewSource(1234))
	n := 20000
	ctxNames := []string{"c0", "c1", "c2"}
	labels := make([]string, n)
	bits := make([]byte, n)
	for i := range bits {
		labels[i] = ctxNames[rng.Intn(len(ctxNames))]
		bits[i] = byte(rng.Intn(2))
	}
	oneShot, err := EncodeFrame(labels, bits)
	if err != nil {
		t.Fatal(err)
	}

	// Split at a non-byte-aligned point so the partial output byte,
	// pending carry bits and interval registers all cross the boundary.
	split := 8371
	e := NewEncoder()
	for i := 0; i < split; i++ {
		if err := e.EncodeBit(labels[i], bits[i]); err != nil {
			t.Fatal(err)
		}
	}
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := snap.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	restored, err := UnmarshalEncoderState(blob)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := Resume(restored)
	if err != nil {
		t.Fatal(err)
	}
	for i := split; i < n; i++ {
		if err := e2.EncodeBit(labels[i], bits[i]); err != nil {
			t.Fatal(err)
		}
	}
	resumed := e2.Finish()
	if !bytes.Equal(oneShot, resumed) {
		t.Fatalf("pause/resume mismatch: one-shot %d bytes, resumed %d bytes", len(oneShot), len(resumed))
	}
	t.Logf("pause/resume: %d-byte frame identical to one-shot", len(oneShot))
}

func TestResumeRejectsCorruptState(t *testing.T) {
	e := NewEncoder()
	if err := e.Encode("a", []byte{0, 1, 1}); err != nil {
		t.Fatal(err)
	}
	snap, err := e.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	blob, err := snap.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnmarshalEncoderState(blob[:len(blob)-3]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("truncated state: got %v", err)
	}
	bad := append([]byte(nil), blob...)
	bad[0] = 'X'
	if _, err := UnmarshalEncoderState(bad); !errors.Is(err, ErrState) {
		t.Fatalf("bad magic: got %v", err)
	}
	if _, err := UnmarshalEncoderState(append(blob, 0)); !errors.Is(err, ErrTrailing) {
		t.Fatalf("trailing: got %v", err)
	}
	// Structurally invalid snapshot: unknown context index.
	snap.Seq[0] = 99
	if _, err := Resume(snap); !errors.Is(err, ErrState) {
		t.Fatalf("bad index: got %v", err)
	}
}
