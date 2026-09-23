package twopc

import (
	"errors"
	"testing"
)

func TestWALRoundTrip(t *testing.T) {
	r := Rec{Kind: RecPrepared, Txn: "T", Coordinator: "C", Keys: []string{"x", "y"}, Values: []string{"1", "2"}}
	b, err := r.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, n, err := DecodeRecord(b)
	if err != nil || n != len(b) {
		t.Fatalf("decode: err=%v n=%d len=%d", err, n, len(b))
	}
	if got.Kind != RecPrepared || got.Txn != "T" || len(got.Keys) != 2 || got.Keys[1] != "y" {
		t.Fatalf("bad decoded record: %+v", got)
	}
}

func TestWALDetectsTornAndCorruptTail(t *testing.T) {
	r := Rec{Kind: RecCommit, Txn: "T"}
	b, _ := r.Encode()

	trunc := b[:len(b)-3]
	if _, _, err := DecodeRecord(trunc); !errors.Is(err, ErrTruncatedTail) {
		t.Fatalf("truncated tail: want ErrTruncatedTail, got %v", err)
	}

	corrupt := append([]byte(nil), b...)
	corrupt[len(corrupt)-1] ^= 0xFF
	if _, _, err := DecodeRecord(corrupt); !errors.Is(err, ErrTruncatedTail) {
		t.Fatalf("corrupt CRC: want ErrTruncatedTail, got %v", err)
	}

	// Intact prefix survives alongside the bad tail.
	good, _ := Rec{Kind: RecAbort, Txn: "ok"}.Encode()
	recs, err := DecodeAll([][]byte{good, trunc})
	if !errors.Is(err, ErrTruncatedTail) {
		t.Fatalf("DecodeAll: want torn-tail error, got %v", err)
	}
	if len(recs) != 1 || recs[0].Txn != "ok" {
		t.Fatalf("DecodeAll prefix = %+v", recs)
	}
}
