package canonkey

import (
	"bytes"
	"testing"
)

func TestStoreInsertAndDedup(t *testing.T) {
	s := NewStore(ConflictReject)
	k := EncodeKey(String("user"), Int(42))
	if o, err := s.Add(k, []byte("v1")); err != nil || o != Inserted {
		t.Fatalf("first add: got %v, %v; want inserted, nil", o, err)
	}
	for i := 0; i < 3; i++ {
		if o, err := s.Add(k, []byte("v1")); err != nil || o != Duplicate {
			t.Fatalf("dup add %d: got %v, %v; want duplicate, nil", i, o, err)
		}
	}
	if s.Len() != 1 {
		t.Fatalf("Len = %d, want 1 after dedup", s.Len())
	}
	got, ok := s.Get(k)
	if !ok || string(got) != "v1" {
		t.Fatalf("Get = %q, %v; want v1, true", got, ok)
	}
}

func TestStoreConflictRejectKeepsOriginal(t *testing.T) {
	s := NewStore(ConflictReject)
	k := EncodeKey(String("k"))
	if _, err := s.Add(k, []byte("original")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := s.Add(k, []byte("impostor")); err != ErrConflict {
		t.Fatalf("conflicting add: got err %v, want ErrConflict", err)
	}
	got, _ := s.Get(k)
	if string(got) != "original" {
		t.Fatalf("stored content silently changed to %q", got)
	}
	if s.Len() != 1 || s.IsolatedLen() != 0 {
		t.Fatalf("Len=%d IsolatedLen=%d, want 1/0", s.Len(), s.IsolatedLen())
	}
}

func TestStoreConflictIsolateQuarantines(t *testing.T) {
	s := NewStore(ConflictIsolate)
	k := EncodeKey(String("k"), Null())
	if _, err := s.Add(k, []byte("original")); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if o, err := s.Add(k, []byte("alt-1")); err != nil || o != Isolated {
		t.Fatalf("conflict 1: got %v, %v; want isolated, nil", o, err)
	}
	if o, err := s.Add(k, []byte("alt-2")); err != nil || o != Isolated {
		t.Fatalf("conflict 2: got %v, %v; want isolated, nil", o, err)
	}
	got, _ := s.Get(k)
	if string(got) != "original" {
		t.Fatalf("stored content silently changed to %q", got)
	}
	iso := s.Isolated(k)
	if len(iso) != 2 || string(iso[0]) != "alt-1" || string(iso[1]) != "alt-2" {
		t.Fatalf("isolated = %q, want [alt-1 alt-2]", iso)
	}
	if o, err := s.Add(k, []byte("alt-1")); err != nil || o != Isolated {
		t.Fatalf("re-isolated add: got %v, %v; want isolated, nil", o, err)
	}
	if s.IsolatedLen() != 3 {
		t.Fatalf("IsolatedLen = %d, want 3", s.IsolatedLen())
	}
}

func TestStoreDistinctKeysIndependent(t *testing.T) {
	s := NewStore(ConflictReject)
	k1 := EncodeKey(String("a"), Null())
	k2 := EncodeKey(String("a"), String(""))
	if _, err := s.Add(k1, []byte("one")); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Add(k2, []byte("two")); err != nil {
		t.Fatalf("null-vs-empty keys must not conflict: %v", err)
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}

func TestStoreCopiesContent(t *testing.T) {
	s := NewStore(ConflictReject)
	k := EncodeKey(Int(1))
	content := []byte("mutable")
	if _, err := s.Add(k, content); err != nil {
		t.Fatal(err)
	}
	content[0] = 'X'
	got, _ := s.Get(k)
	if !bytes.Equal(got, []byte("mutable")) {
		t.Fatalf("stored content was aliased and mutated: %q", got)
	}
	got[0] = 'Y'
	again, _ := s.Get(k)
	if !bytes.Equal(again, []byte("mutable")) {
		t.Fatalf("Get leaked internal storage: %q", again)
	}
}
