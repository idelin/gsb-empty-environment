package netsec

import "testing"

func TestReplayWindowSlidesAndRejectsOldRecords(t *testing.T) {
	window := NewReplayWindow(4)
	if window.Size() != 4 {
		t.Fatalf("Size() = %d, want 4", window.Size())
	}

	for _, sequence := range []uint64{0, 1, 2, 3} {
		if !window.Accept(sequence) {
			t.Fatalf("Accept(%d) = false, want true", sequence)
		}
	}
	for _, sequence := range []uint64{0, 1, 2, 3} {
		if window.Accept(sequence) {
			t.Fatalf("Accept(replay %d) = true, want false", sequence)
		}
	}
	if !window.Accept(5) {
		t.Fatal("Accept(5) = false, want true")
	}
	if window.Accept(2) {
		t.Fatal("Accept(2, still inside window but seen) = true, want false")
	}
	if window.Accept(1) {
		t.Fatal("Accept(1, now outside window) = true, want false")
	}
	if !window.Accept(4) {
		t.Fatal("Accept(4, unseen inside window) = false, want true")
	}
	if window.Accept(4) {
		t.Fatal("Accept(4, replay) = true, want false")
	}

	highest, initialized := window.Highest()
	if highest != 5 || !initialized {
		t.Fatalf("Highest() = %d, %v; want 5, true", highest, initialized)
	}
}

func TestReplayWindowCheckDoesNotCommit(t *testing.T) {
	window := NewReplayWindow(8)
	if !window.Check(1) {
		t.Fatal("Check(1) = false, want true")
	}
	if !window.Accept(1) {
		t.Fatal("Accept(1) after Check = false, want true")
	}
	if window.Check(1) {
		t.Fatal("Check(1) after Accept = true, want false")
	}
}

func TestReplayWindowResetAndDefaultSize(t *testing.T) {
	window := NewReplayWindow(0)
	if window.Size() != DefaultReplayWindowSize {
		t.Fatalf("Size(0) = %d, want %d", window.Size(), DefaultReplayWindowSize)
	}
	if !window.Accept(1_000_000) {
		t.Fatal("Accept(1000000) = false, want true")
	}
	window.Reset()
	if _, initialized := window.Highest(); initialized {
		t.Fatal("window is initialized after Reset")
	}
	if !window.Accept(1) {
		t.Fatal("Accept(1) after Reset = false, want true")
	}
	if !window.Accept(0) {
		t.Fatal("Accept(0) after Reset = false, want true")
	}
}

func TestReplayWindowLargeSlide(t *testing.T) {
	window := NewReplayWindow(128)
	if !window.Accept(0) {
		t.Fatal("Accept(0) = false")
	}
	if !window.Accept(129) {
		t.Fatal("Accept(129) = false")
	}
	if window.Accept(0) {
		t.Fatal("Accept(0) after 129-step slide = true, want false")
	}
	if !window.Accept(128) {
		t.Fatal("Accept(128, immediately below max) = false, want true")
	}
}
