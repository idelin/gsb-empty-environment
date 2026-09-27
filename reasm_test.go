package mtusim

import (
	"bytes"
	"testing"
)

func makeBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func TestReassembleEqualsOriginal(t *testing.T) {
	data := makeBytes(300)
	p := FragParams{HeaderLen: 20, MTU: 128, Align: 8}
	frags, err := p.Fragmentize(9, 1, len(data))
	if err != nil {
		t.Fatalf("Fragmentize: %v", err)
	}
	r := NewReassembler(9, 1)
	// Deliver out of order.
	for _, i := range []int{2, 0, 1} {
		f := frags[i]
		if err := r.Add(f, data[f.Offset:f.Offset+f.Len]); err != nil {
			t.Fatalf("Add frag %d: %v", i, err)
		}
	}
	if !r.Complete {
		t.Fatal("not complete after all fragments")
	}
	if got := r.Bytes(); !bytes.Equal(got, data) {
		t.Errorf("reassembled bytes differ from original")
	}
}

func TestDuplicateFragmentCounted(t *testing.T) {
	data := makeBytes(300)
	p := FragParams{HeaderLen: 20, MTU: 128, Align: 8}
	frags, _ := p.Fragmentize(9, 1, len(data))
	r := NewReassembler(9, 1)
	f := frags[0]
	payload := data[f.Offset : f.Offset+f.Len]
	if err := r.Add(f, payload); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Add(f, payload); err != nil {
		t.Fatalf("duplicate Add should be absorbed, got %v", err)
	}
	if r.Duplicates != 1 {
		t.Errorf("Duplicates=%d, want 1", r.Duplicates)
	}
}

func TestOverlapConflictDetected(t *testing.T) {
	r := NewReassembler(9, 1)
	a := Fragment{PacketID: 9, Epoch: 1, Offset: 0, Len: 16, Header: 20}
	if err := r.Add(a, makeBytes(16)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	// Overlaps [8,24) with [0,16) but disagrees on bytes 8..15.
	b := Fragment{PacketID: 9, Epoch: 1, Offset: 8, Len: 16, Header: 20}
	evil := make([]byte, 16)
	for i := range evil {
		evil[i] = 0xff
	}
	if err := r.Add(b, evil); KindOf(err) != ErrKindOverlapConflict {
		t.Errorf("got %v, want ErrKindOverlapConflict", err)
	}
}

func TestOverlapConsistentMerges(t *testing.T) {
	data := makeBytes(24)
	r := NewReassembler(9, 1)
	a := Fragment{PacketID: 9, Epoch: 1, Offset: 0, Len: 16, Header: 20}
	b := Fragment{PacketID: 9, Epoch: 1, Offset: 8, Len: 16, Last: true, Header: 20}
	if err := r.Add(a, data[0:16]); err != nil {
		t.Fatalf("Add a: %v", err)
	}
	if err := r.Add(b, data[8:24]); err != nil {
		t.Fatalf("Add b: %v", err)
	}
	if !r.Complete {
		t.Fatal("expected completion after consistent overlap")
	}
	if got := r.Bytes(); !bytes.Equal(got, data) {
		t.Errorf("reassembled bytes differ from original")
	}
}

func TestEpochMixingRejected(t *testing.T) {
	r := NewReassembler(9, 1)
	f0 := Fragment{PacketID: 9, Epoch: 1, Offset: 0, Len: 8, Header: 20}
	if err := r.Add(f0, makeBytes(8)); err != nil {
		t.Fatalf("Add: %v", err)
	}
	f1 := Fragment{PacketID: 9, Epoch: 2, Offset: 8, Len: 8, Last: true, Header: 20}
	if err := r.Add(f1, makeBytes(8)); KindOf(err) != ErrKindEpochMismatch {
		t.Errorf("got %v, want ErrKindEpochMismatch", err)
	}
}

func TestPacketMismatchRejected(t *testing.T) {
	r := NewReassembler(9, 1)
	f := Fragment{PacketID: 10, Epoch: 1, Offset: 0, Len: 8, Last: true, Header: 20}
	if err := r.Add(f, makeBytes(8)); KindOf(err) != ErrKindPacketMismatch {
		t.Errorf("got %v, want ErrKindPacketMismatch", err)
	}
}

func TestPayloadLengthMismatchRejected(t *testing.T) {
	r := NewReassembler(9, 1)
	f := Fragment{PacketID: 9, Epoch: 1, Offset: 0, Len: 8, Last: true, Header: 20}
	if err := r.Add(f, makeBytes(7)); KindOf(err) != ErrKindMalformed {
		t.Errorf("got %v, want ErrKindMalformed", err)
	}
}
