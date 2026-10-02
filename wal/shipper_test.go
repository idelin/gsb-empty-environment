package wal

import (
	"os"
	"path/filepath"
	"testing"
)

func TestShipWithPacketLoss(t *testing.T) {
	dir, err := os.MkdirTemp("", "shiptest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	src, err := Open(filepath.Join(dir, "primary.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	const n = 50
	for i := 0; i < n; i++ {
		src.Append(&Record{Type: TypeUpdate, TxnID: uint64(i%3 + 1), PageID: uint32(i % 5),
			Offset: uint32(i), NewData: []byte{byte(i)}})
	}
	if err := src.Flush(); err != nil {
		t.Fatal(err)
	}

	dst, err := Open(filepath.Join(dir, "replica.log"), nil)
	if err != nil {
		t.Fatal(err)
	}
	recv := NewReceiver(dst)
	link := NewLossyLink(40) // 40% of frames dropped
	ship := &Shipper{Source: src, Link: link, MaxRetries: 100}
	if err := ship.Sync(recv); err != nil {
		t.Fatalf("sync over lossy link: %v", err)
	}
	if link.Dropped == 0 {
		t.Fatal("expected the lossy link to actually drop frames")
	}
	if err := dst.Flush(); err != nil {
		t.Fatal(err)
	}

	got := dst.Records()
	if len(got) != n {
		t.Fatalf("replica has %d records, want %d", len(got), n)
	}
	for i, rec := range got {
		if rec.LSN != uint64(i+1) {
			t.Fatalf("replica record %d has LSN %d, want %d", i, rec.LSN, i+1)
		}
	}

	// Retransmission must be idempotent: re-syncing changes nothing.
	if err := ship.Sync(recv); err != nil {
		t.Fatal(err)
	}
	if got := len(dst.Records()); got != n {
		t.Fatalf("after re-sync replica has %d records, want %d (dedup by LSN)", got, n)
	}
	src.Close()
	dst.Close()
}

func TestShipperGivesUpWhenLinkIsDown(t *testing.T) {
	dir, err := os.MkdirTemp("", "shiptest")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)

	src, _ := Open(filepath.Join(dir, "primary.log"), nil)
	src.Append(&Record{Type: TypeBegin, TxnID: 1})
	src.Flush()
	dst, _ := Open(filepath.Join(dir, "replica.log"), nil)
	recv := NewReceiver(dst)

	ship := &Shipper{Source: src, Link: NewLossyLink(100), MaxRetries: 3}
	if err := ship.Sync(recv); err != ErrPacketLoss {
		t.Fatalf("want ErrPacketLoss, got %v", err)
	}
	if got := len(dst.Records()); got != 0 {
		t.Fatalf("nothing may be delivered on a dead link, got %d", got)
	}
	src.Close()
	dst.Close()
}
