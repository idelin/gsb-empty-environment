package zonekey

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestPersistedV1Keys(t *testing.T) {
	for name, want := range map[string]string{".": "ff", "A.Example.": "6578616d706c65006100ff", "example": "6578616d706c6500ff", "AB": "616200ff"} {
		key, err := EncodeV1(name)
		if err != nil || hex.EncodeToString(key) != want {
			t.Fatalf("%q: %x %v, want %s", name, key, err, want)
		}
	}
	apex, _ := EncodeV1("example")
	child, _ := EncodeV1("a.example")
	if bytes.Compare(apex, child) <= 0 {
		t.Fatal("v1 apex sorts after the descendant")
	}
}
func TestRejectInvalidName(t *testing.T) {
	for _, n := range []string{"", "a..b", "-a.example", "x-.example", "*.example", "é.example"} {
		if _, err := EncodeV1(n); err == nil {
			t.Fatalf("accepted %q", n)
		}
	}
}
