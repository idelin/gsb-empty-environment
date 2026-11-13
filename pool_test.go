package netsec

import (
	"bytes"
	"errors"
	"testing"
)

func TestPoolScrubsOnFinalRelease(t *testing.T) {
	p, err := NewPool(2)
	if err != nil {
		t.Fatal(err)
	}
	h, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Fill(h, []byte("tenant-secret")); err != nil {
		t.Fatal(err)
	}
	if err := p.Pin(h); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(h); err != nil {
		t.Fatal(err)
	}
	info, err := p.SlotInfo(h.Slot)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != SlotReady || info.RefCount != 1 {
		t.Fatalf("after first release: %+v", info)
	}
	if err := p.Release(h); err != nil {
		t.Fatal(err)
	}
	info, err = p.SlotInfo(h.Slot)
	if err != nil {
		t.Fatal(err)
	}
	if info.State != SlotFree || info.RefCount != 0 || !info.AllZero {
		t.Fatalf("slot was not scrubbed before free: %+v", info)
	}
}

func TestPoolGenerationRejectsLateFill(t *testing.T) {
	p, err := NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	old, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Fill(old, []byte("first-secret")); err != nil {
		t.Fatal(err)
	}
	if err := p.Release(old); err != nil {
		t.Fatal(err)
	}

	reused, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if reused.Slot != old.Slot {
		t.Fatalf("expected slot reuse, got old=%+v new=%+v", old, reused)
	}
	if reused.Generation == old.Generation {
		t.Fatalf("slot generation did not advance: %+v", reused)
	}
	if err := p.Fill(reused, []byte("new-entry")); err != nil {
		t.Fatal(err)
	}
	if err := p.Fill(old, []byte("late-secret")); !errors.Is(err, ErrStaleBlock) {
		t.Fatalf("late fill error = %v, want ErrStaleBlock", err)
	}
	got, err := p.Read(reused)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("new-entry")) {
		t.Fatalf("new entry was overwritten: %q", got)
	}
}

func TestPoolReadyBlockCannotBeRefilled(t *testing.T) {
	p, err := NewPool(1)
	if err != nil {
		t.Fatal(err)
	}
	h, err := p.Allocate()
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Fill(h, []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	if err := p.Fill(h, []byte("mutated")); !errors.Is(err, ErrBlockNotFilling) {
		t.Fatalf("refill error = %v, want ErrBlockNotFilling", err)
	}
	got, err := p.Read(h)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, []byte("sealed")) {
		t.Fatalf("ready block changed: %q", got)
	}
}
