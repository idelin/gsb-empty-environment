package shrink

import (
	"bytes"
	"testing"
)

func TestShrinkToMarkerLine(t *testing.T) {
	// Interesting iff the input contains the line "BUG".
	interesting := func(in []byte) bool {
		return bytes.Contains(in, []byte("BUG"))
	}
	input := []byte("header\nfiller1\nBUG\nfiller2\nfiller3\ntrailer\n")
	got := Shrink(input, interesting)
	if !interesting(got) {
		t.Fatalf("shrunk input no longer interesting: %q", got)
	}
	if len(got) >= len(input) {
		t.Fatalf("no reduction: %d -> %d", len(input), len(got))
	}
	if string(got) != "BUG" && string(got) != "BUG\n" {
		t.Fatalf("expected minimal marker, got %q", got)
	}
}

func TestShrinkByteLevel(t *testing.T) {
	// No newlines: interesting iff input contains "xy".
	interesting := func(in []byte) bool {
		return bytes.Contains(in, []byte("xy"))
	}
	got := Shrink([]byte("aaaaxya aaa"), interesting)
	if string(got) != "xy" {
		t.Fatalf("got %q, want %q", got, "xy")
	}
}

func TestShrinkNotInteresting(t *testing.T) {
	input := []byte("nothing here")
	got := Shrink(input, func([]byte) bool { return false })
	if !bytes.Equal(got, input) {
		t.Fatalf("expected input unchanged, got %q", got)
	}
}
