package name

import (
	"reflect"
	"testing"
)

func labelsEqual(got [][]byte, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if string(got[i]) != want[i] {
			return false
		}
	}
	return true
}

func nameLabel(parts ...string) []byte {
	var b []byte
	for _, p := range parts {
		b = append(b, byte(len(p)))
		b = append(b, p...)
	}
	return append(b, 0)
}

func ptrTo(offset int) []byte {
	return []byte{0xC0 | byte(offset>>8), byte(offset)}
}

func assertValid(t *testing.T, d *Decoder, start int, wantLabels []string, wantNext int) {
	t.Helper()
	labels, _, next, err := d.Resolve(start)
	if err != nil {
		t.Fatalf("Resolve(%d): unexpected error: %v", start, err)
	}
	if !labelsEqual(labels, wantLabels...) {
		t.Fatalf("Resolve(%d) labels = %q, want %q", start, labels, wantLabels)
	}
	if next != wantNext {
		t.Fatalf("Resolve(%d) next = %d, want %d", start, next, wantNext)
	}
}

func assertInvalid(t *testing.T, d *Decoder, start int) {
	t.Helper()
	if _, _, _, err := d.Resolve(start); err == nil {
		t.Fatalf("Resolve(%d): expected error, got nil", start)
	}
}

func TestRootAndSimpleName(t *testing.T) {
	d := New([]byte{0x00})
	assertValid(t, d, 0, []string{}, 1)

	d2 := New(nameLabel("foo", "com"))
	assertValid(t, d2, 0, []string{"foo", "com"}, 9)
}

func TestPointerNextOffsetIsNotPointerTarget(t *testing.T) {
	// Offset 0 holds the full name; offset 9 is a two-byte pointer back to it.
	packet := append(nameLabel("foo", "com"), ptrTo(0)...)
	d := New(packet)
	assertValid(t, d, 9, []string{"foo", "com"}, 11)
}

func TestMultiHopPointers(t *testing.T) {
	// Layout:
	//   0: foo <ptr 6>   (6 bytes)
	//   6: bar <ptr 12>  (6 bytes)
	//  12: com 0         (5 bytes)
	packet2 := []byte{}
	packet2 = append(packet2, 3, 'f', 'o', 'o')
	packet2 = append(packet2, ptrTo(6)...)
	packet2 = append(packet2, 3, 'b', 'a', 'r')
	packet2 = append(packet2, ptrTo(12)...)
	packet2 = append(packet2, nameLabel("com")...)

	d := New(packet2)
	assertValid(t, d, 0, []string{"foo", "bar", "com"}, 6)
	assertValid(t, d, 4, []string{"bar", "com"}, 6)
}

func TestForwardReference(t *testing.T) {
	// A pointer at offset 0 points ahead to a name appearing later.
	packet := append(ptrTo(2), nameLabel("later", "name")...)
	d := New(packet)
	assertValid(t, d, 0, []string{"later", "name"}, 2)
}

func TestReservedPrefixes(t *testing.T) {
	for _, b := range []byte{0x40, 0x41, 0x7F, 0x80, 0xBF} {
		packet := []byte{b, 0x00, 0x00}
		assertInvalid(t, New(packet), 0)
	}
}

func TestPointerLoops(t *testing.T) {
	// Self loop.
	assertInvalid(t, New(ptrTo(0)), 0)

	// Two-node cycle: 0 -> 2 -> 0.
	two := append(ptrTo(2), ptrTo(0)...)
	assertInvalid(t, New(two), 0)
	assertInvalid(t, New(two), 2)

	// Three-node cycle with a normal label on the way.
	three := []byte{1, 'a'}
	three = append(three, ptrTo(4)...) // offset 2 -> 4
	three = append(three, 1, 'b')
	three = append(three, ptrTo(0)...) // offset 6 -> 0
	assertInvalid(t, New(three), 0)
	assertInvalid(t, New(three), 4)
}

func TestOutOfBounds(t *testing.T) {
	assertInvalid(t, New([]byte{}), 0)
	assertInvalid(t, New(nameLabel("x")), 5)

	// Truncated two-byte pointer.
	assertInvalid(t, New([]byte{0xC0}), 0)

	// Pointer target beyond packet.
	assertInvalid(t, New(ptrTo(99)), 0)

	// Truncated label body.
	assertInvalid(t, New([]byte{4, 'a', 'b'}), 0)

	// Label body runs into the end without a root.
	assertInvalid(t, New([]byte{2, 'a', 'b'}), 0)
}

func TestExpandedNameLengthLimit(t *testing.T) {
	// Exactly 255 bytes on the wire: 63+1 three times, 61+1 once, root.
	var packet []byte
	for _, n := range []int{63, 63, 63, 61} {
		packet = append(packet, byte(n))
		for i := 0; i < n; i++ {
			packet = append(packet, 'a')
		}
	}
	packet = append(packet, 0)
	if len(packet) != 255 {
		t.Fatalf("setup: packet length = %d, want 255", len(packet))
	}
	d := New(packet)
	labels, wireLen, _, err := d.Resolve(0)
	if err != nil {
		t.Fatalf("255-byte name rejected: %v", err)
	}
	if wireLen != 255 || len(labels) != 4 {
		t.Fatalf("255-byte name: wireLen=%d labels=%d", wireLen, len(labels))
	}

	// 256 bytes: same shape but with one extra label byte.
	bad := []byte{}
	for _, n := range []int{63, 63, 63, 62} {
		bad = append(bad, byte(n))
		for i := 0; i < n; i++ {
			bad = append(bad, 'a')
		}
	}
	bad = append(bad, 0)
	if len(bad) != 256 {
		t.Fatalf("setup: bad packet length = %d, want 256", len(bad))
	}
	assertInvalid(t, New(bad), 0)
}

func TestSharedSuffixMemoized(t *testing.T) {
	// Layout:
	//   0: foo <ptr 12>      (6 bytes)
	//   6: bar <ptr 12>      (6 bytes)
	//  12: com 0             (5 bytes, shared suffix)
	packet := []byte{}
	packet = append(packet, 3, 'f', 'o', 'o')
	packet = append(packet, ptrTo(12)...)
	packet = append(packet, 3, 'b', 'a', 'r')
	packet = append(packet, ptrTo(12)...)
	packet = append(packet, nameLabel("com")...)

	d := New(packet)
	assertValid(t, d, 0, []string{"foo", "com"}, 6)
	assertValid(t, d, 6, []string{"bar", "com"}, 12)
	assertValid(t, d, 12, []string{"com"}, 17)

	// Every packet byte is read at most a small constant number of times:
	// the shared suffix must not be re-walked per starting offset.
	if d.Reads() > len(packet)*2+8 {
		t.Fatalf("reads = %d for %d-byte packet; suffix appears re-walked", d.Reads(), len(packet))
	}

	// Repeated queries use the memoized entries without extra walks.
	assertValid(t, d, 0, []string{"foo", "com"}, 6)
	if d.Reads() > len(packet)*2+8 {
		t.Fatalf("reads = %d after cached lookup", d.Reads())
	}
}

func TestLongSharedChainNotRepeated(t *testing.T) {
	// One long label chain (still within the 255-byte name limit), and one
	// pointer start per chain position. All starts share that chain, so a
	// correct decoder must not re-walk it once per start.
	n := 125
	var packet []byte
	for i := 0; i < n; i++ {
		packet = append(packet, 1, 'a')
	}
	packet = append(packet, 0)
	chainLen := len(packet)
	if chainLen != 2*n+1 {
		t.Fatalf("setup: chain length = %d", chainLen)
	}

	starts := []int{}
	for i := 0; i < n; i++ {
		starts = append(starts, len(packet))
		packet = append(packet, ptrTo(i*2)...)
	}

	d := New(packet)
	for _, start := range starts {
		if _, _, _, err := d.Resolve(start); err != nil {
			t.Fatalf("Resolve(%d): %v", start, err)
		}
	}
	// Naive re-walking would touch O(n^2); memoization keeps it near O(n).
	if d.Reads() > len(packet)*2+4 {
		t.Fatalf("reads = %d, packet = %d; chain re-walked per start", d.Reads(), len(packet))
	}
}

func TestMemoizedInvalidSuffixInvalidatesUsers(t *testing.T) {
	// Offset 3 holds a label whose suffix loops; offset 0 points at it.
	packet := []byte{1, 'a'}
	packet = append(packet, ptrTo(5)...) // offset 2 -> 5
	packet = append(packet, 1, 'b')
	packet = append(packet, ptrTo(5)...) // offset 6 self loop
	d := New(packet)
	assertInvalid(t, d, 6)
	assertInvalid(t, d, 0)

	// Stored state remains error on repeated queries.
	if !reflect.DeepEqual(d.memo[6].state, stError) {
		t.Fatalf("memo[6].state = %d, want stError", d.memo[6].state)
	}
}
