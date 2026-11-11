package canon

import (
	"errors"
	"strings"
	"testing"
)

func TestAppendEncodeReusesBuffer(t *testing.T) {
	prefix := []byte{0xaa, 0xbb}
	out, err := AppendEncode(prefix, int64(5))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.ToLower(stringsToHex(out)); got != "aabb05" {
		t.Fatalf("AppendEncode = %s, want aabb05", got)
	}
}

func stringsToHex(b []byte) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 0, len(b)*2)
	for _, x := range b {
		out = append(out, digits[x>>4], digits[x&0x0f])
	}
	return string(out)
}

func TestMapGet(t *testing.T) {
	m := NewMap(
		MapEntry{Key: "name", Value: "device-1"},
		MapEntry{Key: int64(7), Value: []byte{1}},
	)
	if v, ok := m.Get("name"); !ok || v != "device-1" {
		t.Fatalf("Get(name) = %v, %v", v, ok)
	}
	if v, ok := m.Get(int64(7)); !ok {
		t.Fatalf("Get(7) missing: %v", v)
	}
	if _, ok := m.Get("missing"); ok {
		t.Fatal("Get(missing) unexpectedly found")
	}
	if _, ok := m.Get(true); ok {
		t.Fatal("Get with invalid key unexpectedly found")
	}
	if m.Len() != 2 || m.Entry(0).Key != "name" {
		t.Fatal("Len/Entry accessors broken")
	}
}

func TestReflectPaths(t *testing.T) {
	// Named byte-slice and fixed-size byte array encode as byte strings.
	type rawBytes []byte
	named := rawBytes{1, 2, 3}
	got, err := Encode(named)
	if err != nil {
		t.Fatal(err)
	}
	if stringsToHex(got) != "43010203" {
		t.Fatalf("named []byte = %x", got)
	}

	fixed := [3]byte{4, 5, 6}
	got, err = Encode(fixed)
	if err != nil {
		t.Fatal(err)
	}
	if stringsToHex(got) != "43040506" {
		t.Fatalf("byte array = %x", got)
	}

	// Non-byte slices and native maps go through reflection too.
	ints := []int{1, 2, 3}
	got, err = Encode(ints)
	if err != nil {
		t.Fatal(err)
	}
	if stringsToHex(got) != "83010203" {
		t.Fatalf("[]int = %x", got)
	}

	native := map[int]string{1: "one", 2: "two"}
	first, err := Encode(native)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := Encode(native)
	if !bytesEqual(first, again) {
		t.Fatal("native map encoding unstable")
	}
	if _, err := Decode(first); err != nil {
		t.Fatalf("native map output not decodable: %v", err)
	}

	fixedInts := [3]int{1, 2, 3}
	got, err = Encode(fixedInts)
	if err != nil {
		t.Fatal(err)
	}
	if stringsToHex(got) != "83010203" {
		t.Fatalf("fixed int array = %x", got)
	}

	if _, err := Encode(make(chan int)); err == nil {
		t.Fatal("channel value must be rejected")
	}
}

func TestErrorWrappersAndMessages(t *testing.T) {
	var budget error = &BudgetError{Resource: "items", Limit: 7}
	if !errors.Is(budget, ErrBudgetExceeded) {
		t.Fatal("BudgetError must wrap ErrBudgetExceeded")
	}
	if !strings.Contains(budget.Error(), "items") {
		t.Fatalf("budget message = %q", budget.Error())
	}

	unsupported := &UnsupportedTypeError{}
	if !strings.Contains(unsupported.Error(), "unsupported") {
		t.Fatalf("empty type message = %q", unsupported.Error())
	}

	_, err := Decode([]byte{0x41})
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("err = %v", err)
	}
}

func TestDepthLimit(t *testing.T) {
	deep := []any{int64(0)}
	for i := 0; i < 300; i++ {
		deep = []any{deep}
	}
	raw, err := Encode(deep)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeWithLimits(raw, DefaultLimits()); !errors.Is(err, ErrBudgetExceeded) {
		t.Fatalf("deep nesting err = %v, want budget", err)
	}
}

func TestIntegerOverflowNegative(t *testing.T) {
	// Negative integer whose magnitude cannot fit in int64.
	raw := mustHex(t, "3b8000000000000000")
	if _, err := Decode(raw); !errors.Is(err, ErrIntegerOverflow) {
		t.Fatalf("err = %v, want ErrIntegerOverflow", err)
	}
}

func TestTruncatedHeads(t *testing.T) {
	for _, in := range []string{"18", "1901", "1a010203", "1b01020304050607"} {
		if _, err := Decode(mustHex(t, in)); !errors.Is(err, ErrTruncated) {
			t.Fatalf("Decode(%s) err = %v, want ErrTruncated", in, err)
		}
	}
}
