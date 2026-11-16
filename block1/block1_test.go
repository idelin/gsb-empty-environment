package block1

import (
	"bytes"
	"fmt"
	"testing"
)

const (
	peerA = "peer-a"
	path  = "/upload"
)

func block(peer, resource string, num uint32, more bool, szx uint, payload []byte) Message {
	return Message{
		Peer:          peer,
		Resource:      resource,
		Num:           num,
		More:          more,
		SZX:           szx,
		Size1:         -1,
		ContentFormat: -1,
		Payload:       payload,
	}
}

func TestReassembleInOrder(t *testing.T) {
	r := New()
	var want []byte
	for num := uint32(0); num < 4; num++ {
		payload := bytes.Repeat([]byte{byte('a' + num)}, 16)
		want = append(want, payload...)
		more := num < 3
		res := r.Handle(block(peerA, path, num, more, 0, payload))
		if more {
			if res.Code != CodeContinue || res.Complete {
				t.Fatalf("block %d: got %v complete=%v, want 2.31", num, res.Code, res.Complete)
			}
			continue
		}
		if res.Code != CodeChanged || !res.Complete {
			t.Fatalf("final block: got %v complete=%v, want 2.04 complete", res.Code, res.Complete)
		}
		if !bytes.Equal(res.Payload, want) {
			t.Fatalf("reassembled payload mismatch: got %q want %q", res.Payload, want)
		}
	}
	if got := r.peerPending[peerA]; got != 0 {
		t.Fatalf("peer slot not released, pending=%d", got)
	}
}

func TestHugeNumGapRejected(t *testing.T) {
	r := New()
	if res := r.Handle(block(peerA, path, 0, true, 0, bytes.Repeat([]byte{1}, 16))); res.Code != CodeContinue {
		t.Fatalf("first block: got %v, want 2.31", res.Code)
	}
	// Attacker jumps to a huge NUM hoping the server allocates
	// num*blockSize bytes.
	res := r.Handle(block(peerA, path, 1<<20-1, true, 0, bytes.Repeat([]byte{2}, 16)))
	if res.Code != CodeRequestEntityIncomplete {
		t.Fatalf("gap: got %v, want 4.08", res.Code)
	}
	// State survives the gap: the correct next block is still accepted.
	if res := r.Handle(block(peerA, path, 1, false, 0, []byte("tail"))); res.Code != CodeChanged {
		t.Fatalf("after gap: got %v, want 2.04", res.Code)
	}
}

func TestOversizedSize1Rejected(t *testing.T) {
	r := New()
	m := block(peerA, path, 0, true, 6, bytes.Repeat([]byte{1}, 1024))
	m.Size1 = MaxPayloadSize + 1
	if res := r.Handle(m); res.Code != CodeRequestEntityTooLarge {
		t.Fatalf("got %v, want 4.13", res.Code)
	}
	if got := len(r.transfers); got != 0 {
		t.Fatalf("state allocated despite 4.13, transfers=%d", got)
	}
	// Exactly at the limit is accepted.
	m.Size1 = MaxPayloadSize
	if res := r.Handle(m); res.Code != CodeContinue {
		t.Fatalf("at-limit Size1: got %v, want 2.31", res.Code)
	}
}

func TestAccumulatedOverflowRejected(t *testing.T) {
	r := New()
	// No Size1 declared; 65 blocks of 1024 bytes exceed 64 KiB.
	for num := uint32(0); num < 65; num++ {
		res := r.Handle(block(peerA, path, num, true, 6, bytes.Repeat([]byte{1}, 1024)))
		if num < 64 && res.Code != CodeContinue {
			t.Fatalf("block %d: got %v, want 2.31", num, res.Code)
		}
		if num == 64 && res.Code != CodeRequestEntityTooLarge {
			t.Fatalf("block %d: got %v, want 4.13", num, res.Code)
		}
	}
	if got := len(r.transfers); got != 0 {
		t.Fatalf("overflowing transfer not dropped, transfers=%d", got)
	}
}

func TestFifthTransferRejected(t *testing.T) {
	r := New()
	payload := bytes.Repeat([]byte{1}, 16)
	for i := 0; i < MaxTransfersPerPeer; i++ {
		resource := fmt.Sprintf("/upload/%d", i)
		if res := r.Handle(block(peerA, resource, 0, true, 0, payload)); res.Code != CodeContinue {
			t.Fatalf("transfer %d: got %v, want 2.31", i, res.Code)
		}
	}
	res := r.Handle(block(peerA, "/upload/5", 0, true, 0, payload))
	if res.Code != CodeTooManyRequests {
		t.Fatalf("5th transfer: got %v, want 4.29", res.Code)
	}
	// A different peer is unaffected.
	if res := r.Handle(block("peer-b", "/upload/5", 0, true, 0, payload)); res.Code != CodeContinue {
		t.Fatalf("other peer: got %v, want 2.31", res.Code)
	}
	// Completing one transfer frees a slot.
	if res := r.Handle(block(peerA, "/upload/0", 1, false, 0, []byte("end"))); res.Code != CodeChanged {
		t.Fatalf("complete transfer: got %v, want 2.04", res.Code)
	}
	if res := r.Handle(block(peerA, "/upload/5", 0, true, 0, payload)); res.Code != CodeContinue {
		t.Fatalf("after free: got %v, want 2.31", res.Code)
	}
}

func TestETagChangeDropsTransfer(t *testing.T) {
	r := New()
	m0 := block(peerA, path, 0, true, 0, bytes.Repeat([]byte{1}, 16))
	m0.ETag = []byte("v1")
	if res := r.Handle(m0); res.Code != CodeContinue {
		t.Fatalf("first block: got %v, want 2.31", res.Code)
	}
	// Mid-transfer content swap: same NUM sequence, different ETag.
	m1 := block(peerA, path, 1, true, 0, bytes.Repeat([]byte{2}, 16))
	m1.ETag = []byte("v2")
	if res := r.Handle(m1); res.Code != CodeRequestEntityIncomplete {
		t.Fatalf("etag change: got %v, want 4.08", res.Code)
	}
	// The whole transfer is gone: further blocks find no state.
	m2 := block(peerA, path, 2, false, 0, []byte("x"))
	m2.ETag = []byte("v1")
	if res := r.Handle(m2); res.Code != CodeRequestEntityIncomplete {
		t.Fatalf("after drop: got %v, want 4.08", res.Code)
	}
	// The peer slot was released, so a fresh transfer may restart.
	if res := r.Handle(m0); res.Code != CodeContinue {
		t.Fatalf("restart: got %v, want 2.31", res.Code)
	}
}

func TestContentFormatChangeDropsTransfer(t *testing.T) {
	r := New()
	m0 := block(peerA, path, 0, true, 0, bytes.Repeat([]byte{1}, 16))
	m0.ContentFormat = 0
	if res := r.Handle(m0); res.Code != CodeContinue {
		t.Fatalf("first block: got %v, want 2.31", res.Code)
	}
	m1 := block(peerA, path, 1, false, 0, []byte("x"))
	m1.ContentFormat = 40
	if res := r.Handle(m1); res.Code != CodeRequestEntityIncomplete {
		t.Fatalf("content-format change: got %v, want 4.08", res.Code)
	}
	if got := len(r.transfers); got != 0 {
		t.Fatalf("transfer not dropped, transfers=%d", got)
	}
}

func TestSZXChangeRejected(t *testing.T) {
	r := New()
	if res := r.Handle(block(peerA, path, 0, true, 0, bytes.Repeat([]byte{1}, 16))); res.Code != CodeContinue {
		t.Fatalf("first block: got %v, want 2.31", res.Code)
	}
	if res := r.Handle(block(peerA, path, 1, true, 4, bytes.Repeat([]byte{2}, 256))); res.Code != CodeBadRequest {
		t.Fatalf("szx change: got %v, want 4.00", res.Code)
	}
}

func TestInvalidSZXRejected(t *testing.T) {
	r := New()
	if res := r.Handle(block(peerA, path, 0, true, 7, nil)); res.Code != CodeBadRequest {
		t.Fatalf("szx=7: got %v, want 4.00", res.Code)
	}
}

func TestNonFinalShortBlockRejected(t *testing.T) {
	r := New()
	res := r.Handle(block(peerA, path, 0, true, 4, []byte("short")))
	if res.Code != CodeBadRequest {
		t.Fatalf("got %v, want 4.00", res.Code)
	}
}
