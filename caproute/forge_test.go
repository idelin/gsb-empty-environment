package caproute

import (
	"bytes"
	"context"
	"testing"
)

// sequenceReader serves 32 bytes of each value in order, so consecutive
// minted handles are deterministic and distinct inside this test only.
type sequenceReader struct {
	vals []byte
	pos  int
}

func (r *sequenceReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.vals[r.pos/32%len(r.vals)]
		r.pos++
	}
	return len(p), nil
}

func TestHandleUnforgeableAndUnique(t *testing.T) {
	audit := &MemoryAudit{}
	host := NewHost(WithAudit(audit), withRand(&sequenceReader{vals: []byte{0x42, 0x07}}))
	if err := host.RegisterInstance("alpha"); err != nil {
		t.Fatal(err)
	}
	res := newTrackResource(false)
	if err := host.RegisterResource(TypeFile, res, "doc"); err != nil {
		t.Fatal(err)
	}

	h1 := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	h2 := mint(t, host, "alpha", TypeFile, "doc", []Operation{OpFileRead})
	want1 := bytes.Repeat([]byte{0x42}, 32)
	want2 := bytes.Repeat([]byte{0x07}, 32)
	if !bytes.Equal(h1, want1) || !bytes.Equal(h2, want2) {
		t.Fatalf("deterministic handles mismatch: %x %x", h1, h2)
	}
	if bytes.Equal(h1, h2) {
		t.Fatal("two capabilities must not share a handle value")
	}

	// Knowing the exact bytes of h1 does not help: presenting the raw bytes
	// through a freshly built slice still resolves to the real capability,
	// while flipping one byte turns it into an invalid handle.
	if _, err := host.Entry(context.Background(), "alpha", append([]byte(nil), h1...), OpFileRead, nil); err != nil {
		t.Fatalf("identical bytes should resolve: %v", err)
	}
	forged := append([]byte(nil), h1...)
	forged[0] ^= 0xff
	_, err := host.Entry(context.Background(), "alpha", forged, OpFileRead, nil)
	if got := denialReason(t, err); got != ReasonInvalidHandle {
		t.Fatalf("forged one-bit-flip handle: got reason %s", got)
	}
}
