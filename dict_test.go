package strdict

import (
	"errors"
	"fmt"
	"testing"
)

func TestInternEmptyString(t *testing.T) {
	d := NewDictionary(1)
	idx, err := d.Intern("")
	if err != nil {
		t.Fatal(err)
	}
	if idx != 0 {
		t.Fatalf("empty string index = %d, want 0", idx)
	}
	// Re-interning is idempotent.
	again, err := d.Intern("")
	if err != nil || again != idx {
		t.Fatalf("re-intern = %d, %v; want %d, nil", again, err, idx)
	}
	got, ok := d.Lookup(idx)
	if !ok || got != "" {
		t.Fatalf("Lookup(%d) = %q, %v; want \"\", true", idx, got, ok)
	}
	if _, ok := d.Index(""); !ok {
		t.Fatal("Index(\"\") not found")
	}
}

func TestFreezeImmutability(t *testing.T) {
	d := NewDictionary(1)
	if _, err := d.Intern("a"); err != nil {
		t.Fatal(err)
	}
	d.Freeze()
	if _, err := d.Intern("b"); !errors.Is(err, ErrFrozen) {
		t.Fatalf("Intern after Freeze = %v, want ErrFrozen", err)
	}
	if d.Len() != 1 {
		t.Fatalf("Len = %d, want 1", d.Len())
	}
}

func TestForcedHashCollision(t *testing.T) {
	// Every string hashes to the same bucket.
	d := NewDictionary(1, WithHashFunc(func(string) uint64 { return 42 }))
	want := make([]string, 0, 65)
	want = append(want, "")
	for i := 0; i < 64; i++ {
		want = append(want, fmt.Sprintf("s%02d", i))
	}
	for i, s := range want {
		idx, err := d.Intern(s)
		if err != nil {
			t.Fatal(err)
		}
		if int(idx) != i {
			t.Fatalf("Intern(%q) = %d, want %d", s, idx, i)
		}
	}
	for i, s := range want {
		idx, ok := d.Index(s)
		if !ok || int(idx) != i {
			t.Fatalf("Index(%q) = %d, %v; want %d, true", s, idx, ok, i)
		}
		got, ok := d.Lookup(uint32(i))
		if !ok || got != s {
			t.Fatalf("Lookup(%d) = %q, %v; want %q, true", i, got, ok, s)
		}
	}
	if _, ok := d.Index("absent"); ok {
		t.Fatal("Index(absent) found in colliding bucket")
	}
}

func TestDictionaryBytes(t *testing.T) {
	d := NewDictionary(1)
	_, _ = d.Intern("")
	_, _ = d.Intern("hello")
	_, _ = d.Intern("world!")
	if got, want := d.Bytes(), int64(11); got != want {
		t.Fatalf("Bytes = %d, want %d", got, want)
	}
}
