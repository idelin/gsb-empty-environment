package safezip

import (
	"io"
	"strings"
	"testing"
)

func readMember(t *testing.T, a *Archive, name string) []byte {
	t.Helper()
	m, ok := a.Lookup(name)
	if !ok {
		t.Fatalf("member %q missing", name)
	}
	r, err := a.OpenMember(m)
	if err != nil {
		t.Fatalf("open member: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read member: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("close member: %v", err)
	}
	return data
}

func TestOpenAndFetchBasic(t *testing.T) {
	payload := strings.Repeat("hello-safe-zip-", 200)
	raw := buildZip(false,
		fixtureMember{name: "a.txt", data: []byte(payload)},
		fixtureMember{name: "dir/", dir: true},
		fixtureMember{name: "b.bin", data: []byte("second")},
	)
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got := a.Names(); len(got) != 2 || got[0] != "a.txt" || got[1] != "b.bin" {
		t.Fatalf("unexpected names: %v", got)
	}
	if got := readMember(t, a, "a.txt"); string(got) != payload {
		t.Fatalf("payload mismatch: %q", got)
	}
	if got := readMember(t, a, "b.bin"); string(got) != "second" {
		t.Fatalf("payload mismatch: %q", got)
	}

	cache := NewMemCache()
	m, _ := a.Lookup("a.txt")
	n, err := a.CacheMember(m, cache)
	if err != nil || n != int64(len(payload)) {
		t.Fatalf("cache member: n=%d err=%v", n, err)
	}
	got, ok := cache.Get(a.cacheKey(m))
	if !ok || string(got) != payload {
		t.Fatalf("cache content mismatch")
	}
}

func TestDataDescriptors(t *testing.T) {
	payload := strings.Repeat("descriptor-", 128)
	raw := buildZip(true,
		fixtureMember{name: "x.txt", data: []byte(payload)},
		fixtureMember{name: "y.txt", data: []byte("tiny")},
	)
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	for _, name := range []string{"x.txt", "y.txt"} {
		m, ok := a.Lookup(name)
		if !ok || !m.DataDescriptor {
			t.Fatalf("member %s missing descriptor flag: %+v", name, m)
		}
	}
	if got := readMember(t, a, "x.txt"); string(got) != payload {
		t.Fatalf("descriptor payload mismatch")
	}
	if got := readMember(t, a, "y.txt"); string(got) != "tiny" {
		t.Fatalf("descriptor payload mismatch")
	}
}

func TestZip64Archive(t *testing.T) {
	payload := strings.Repeat("zip64-", 300)
	raw := buildRawZip64([]rawZip64Member{
		{name: "big.txt", data: []byte(payload)},
		{name: "empty-dir/", dir: true},
		{name: "small.txt", data: []byte("z")},
	})
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatalf("open zip64: %v", err)
	}
	if got := a.Names(); len(got) != 2 {
		t.Fatalf("unexpected names: %v", got)
	}
	m, ok := a.Lookup("big.txt")
	if !ok || !m.zip64 || !m.DataDescriptor {
		t.Fatalf("big member not flagged zip64/descriptor: %+v", m)
	}
	if got := readMember(t, a, "big.txt"); string(got) != payload {
		t.Fatalf("zip64 payload mismatch")
	}
	if got := readMember(t, a, "small.txt"); string(got) != "z" {
		t.Fatalf("zip64 payload mismatch")
	}
}

func TestIncompleteReadRejected(t *testing.T) {
	raw := buildZip(false, fixtureMember{name: "a.txt", data: []byte(strings.Repeat("x", 500))})
	a, err := Open(BytesStore{B: raw}, testConfig())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	m, _ := a.Lookup("a.txt")
	r, err := a.OpenMember(m)
	if err != nil {
		t.Fatal(err)
	}
	small := make([]byte, 10)
	if _, err := io.ReadFull(r, small); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); !errorIs(err, ErrIncomplete) {
		t.Fatalf("expected ErrIncomplete, got %v", err)
	}
}
