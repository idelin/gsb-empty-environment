package nsec3

import (
	"errors"
	"testing"
)

func TestWireName(t *testing.T) {
	cases := []struct {
		in   string
		want []byte
	}{
		{".", []byte{0}},
		{"example.", []byte{7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0}},
		{"a.example", []byte{1, 'a', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0}},
		{"A.EXAMPLE.", []byte{1, 'a', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0}},
		{"*.example.", []byte{1, '*', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 0}},
	}
	for _, c := range cases {
		got, err := WireName(c.in)
		if err != nil {
			t.Fatalf("WireName(%q): %v", c.in, err)
		}
		if string(got) != string(c.want) {
			t.Errorf("WireName(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestWireNameRejects(t *testing.T) {
	for _, in := range []string{"", "bad..name.", ".bad", "a..b."} {
		if _, err := WireName(in); !errors.Is(err, ErrInvalidName) {
			t.Errorf("WireName(%q) = %v, want ErrInvalidName", in, err)
		}
	}
}

func TestLowerName(t *testing.T) {
	got, err := LowerName("Host.Example")
	if err != nil {
		t.Fatal(err)
	}
	if got != "host.example." {
		t.Fatalf("LowerName = %q", got)
	}
	root, err := LowerName(".")
	if err != nil || root != "." {
		t.Fatalf("root LowerName = %q, %v", root, err)
	}
}

func TestNameHelpers(t *testing.T) {
	anc, err := ancestors("a.b.example.")
	if err != nil {
		t.Fatal(err)
	}
	wantAnc := []string{"b.example.", "example."}
	if len(anc) != len(wantAnc) {
		t.Fatalf("ancestors = %v", anc)
	}
	for i := range anc {
		if anc[i] != wantAnc[i] {
			t.Fatalf("ancestor %d = %q, want %q", i, anc[i], wantAnc[i])
		}
	}
	nc, err := nextCloserName("a.b.example.", "example.")
	if err != nil || nc != "b.example." {
		t.Fatalf("nextCloserName = %q, %v", nc, err)
	}
	wild, err := wildcardName("example.")
	if err != nil || wild != "*.example." {
		t.Fatalf("wildcardName = %q, %v", wild, err)
	}
	rootWild, err := wildcardName(".")
	if err != nil || rootWild != "*." {
		t.Fatalf("root wildcard = %q, %v", rootWild, err)
	}
}

func TestRecordCoversHashGuards(t *testing.T) {
	r := &Record{OwnerHash: hb(1, 2), NextHash: hb(3, 4)}
	if RecordCoversHash(r, hb(1)) {
		t.Error("length mismatch must not cover")
	}
	if RecordCoversHash(nil, hb(1, 2)) {
		t.Error("nil record must not cover")
	}
}

func TestVerifierParamsAndSaltRejection(t *testing.T) {
	recs := buildRing(t, []ringRecord{
		{"example.", []uint16{TypeSOA}, false},
		{"a.example.", []uint16{TypeA}, false},
	})
	v := newVerifier(t, recs)
	if v.Params().Algorithm != HashSHA1 || v.Params().Iterations != testIter {
		t.Fatal("verifier parameters not exposed correctly")
	}
	// Over-long salt.
	bad := &Record{
		Algorithm: HashSHA1, Iterations: 0,
		Salt:      make([]byte, maxSaltLen+1),
		OwnerHash: hb(1), NextHash: hb(2),
		Types: map[uint16]bool{},
	}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("long salt: %v", err)
	}
	// Missing next hash / nil type map.
	bad = &Record{Algorithm: HashSHA1, OwnerHash: hb(1), Types: map[uint16]bool{}}
	if err := bad.Validate(); !errors.Is(err, ErrInvalidRecord) {
		t.Fatalf("missing next hash: %v", err)
	}
}
