package versionedkv

import "testing"

func TestCompareVersionUpdate(t *testing.T) {
	s := New()
	if !s.CompareAndSwap(IfVersion(s.VersionOf("k")), Put("k", []byte("v1"))) {
		t.Fatal("create-if-absent should succeed")
	}
	v1 := s.VersionOf("k")
	if !v1.Exists {
		t.Fatal("key should exist after put")
	}
	if !s.CompareAndSwap(IfVersion(v1), Put("k", []byte("v2"))) {
		t.Fatal("CAS with current version should succeed")
	}
	if got, _ := s.Get("k"); string(got) != "v2" {
		t.Fatalf("got %q, want v2", got)
	}
	if s.CompareAndSwap(IfVersion(v1), Put("k", []byte("v3"))) {
		t.Fatal("CAS with stale version must fail")
	}
	if got, _ := s.Get("k"); string(got) != "v2" {
		t.Fatalf("failed CAS must not write, got %q", got)
	}
}

func TestBatchAllConditionsMustPass(t *testing.T) {
	s := New()
	s.CompareAndSwap(IfVersion(s.VersionOf("a")), Put("a", []byte("1")))
	s.CompareAndSwap(IfVersion(s.VersionOf("b")), Put("b", []byte("1")))
	va, vb := s.VersionOf("a"), s.VersionOf("b")

	// One stale condition among three: whole batch rejected, nothing written.
	ok := s.Batch(
		[]Condition{IfVersion(va), IfVersion(vb), IfVersion(s.VersionOf("c"))},
		[]Write{Put("a", []byte("2")), Put("b", []byte("2")), Put("c", []byte("2"))},
	)
	if !ok {
		t.Fatal("batch with all conditions current should succeed")
	}
	for _, k := range []string{"a", "b", "c"} {
		if got, _ := s.Get(k); string(got) != "2" {
			t.Fatalf("key %s = %q, want 2", k, got)
		}
	}

	va2, vb2 := s.VersionOf("a"), s.VersionOf("b")
	ok = s.Batch(
		[]Condition{IfVersion(va2), IfVersion(vb), IfVersion(vb2)}, // vb is stale
		[]Write{Put("a", []byte("3")), Put("b", []byte("3"))},
	)
	if ok {
		t.Fatal("batch with one stale condition must fail")
	}
	if got, _ := s.Get("a"); string(got) != "2" {
		t.Fatalf("rejected batch must not write, a = %q", got)
	}
	if s.VersionOf("a") != va2 || s.VersionOf("b") != vb2 {
		t.Fatal("rejected batch must not change any version")
	}
}

func TestMissingVersionIsIndependent(t *testing.T) {
	s := New()
	ma, mb := s.VersionOf("a"), s.VersionOf("b")
	if ma.Exists || mb.Exists {
		t.Fatal("missing keys must report Exists=false")
	}
	if ma.Key != "a" || mb.Key != "b" {
		t.Fatal("missing version must be bound to its own key")
	}
	// A condition built from a's missing version guards key a, not key b.
	if !s.CompareAndSwap(IfVersion(ma), Put("a", []byte("1"))) {
		t.Fatal("create-if-absent on a should succeed")
	}
	if s.CompareAndSwap(IfVersion(ma), Put("a", []byte("2"))) {
		t.Fatal("a's missing version must be invalidated by the create")
	}
	if !s.CompareAndSwap(IfVersion(mb), Put("b", []byte("1"))) {
		t.Fatal("b's missing version must be unaffected by writes to a")
	}
}

func TestDeleteRecreateNoABA(t *testing.T) {
	s := New()
	m0 := s.VersionOf("k") // missing version before anything
	s.CompareAndSwap(IfVersion(m0), Put("k", []byte("v1")))
	v1 := s.VersionOf("k")

	s.CompareAndSwap(IfVersion(v1), Delete("k"))
	m1 := s.VersionOf("k")
	if m1.Exists {
		t.Fatal("key should be missing after delete")
	}
	if m1 == m0 {
		t.Fatal("missing version after delete must differ from the original missing version")
	}

	s.CompareAndSwap(IfVersion(m1), Put("k", []byte("v2")))
	v2 := s.VersionOf("k")
	if v2 == v1 {
		t.Fatal("recreated key must not reuse the old version")
	}
	if s.CompareAndSwap(IfVersion(v1), Put("k", []byte("v3"))) {
		t.Fatal("pre-delete condition must never hold again after delete+recreate")
	}
	if s.CompareAndSwap(IfVersion(m0), Put("other", []byte("x"))) {
		t.Fatal("original missing condition must never hold again")
	}
	if got, _ := s.Get("k"); string(got) != "v2" {
		t.Fatalf("k = %q, want v2", got)
	}
}

func TestEmptyBatch(t *testing.T) {
	s := New()
	s.CompareAndSwap(IfVersion(s.VersionOf("k")), Put("k", []byte("v")))
	before := s.VersionOf("k")
	if !s.Batch(nil, nil) {
		t.Fatal("empty batch should succeed")
	}
	if !s.Batch([]Condition{}, []Write{}) {
		t.Fatal("empty batch (non-nil slices) should succeed")
	}
	if s.VersionOf("k") != before {
		t.Fatal("empty batch must not change any version")
	}
	// Conditions-only batch is a consistent read barrier.
	if !s.Batch([]Condition{IfVersion(before)}, nil) {
		t.Fatal("condition-only batch with current version should succeed")
	}
	if s.Batch([]Condition{IfVersion(s.VersionOf("missing"))}, nil) != true {
		t.Fatal("condition-only batch on missing key should succeed")
	}
}

func TestSameKeyConflictingConditions(t *testing.T) {
	s := New()
	s.CompareAndSwap(IfVersion(s.VersionOf("k")), Put("k", []byte("v1")))
	v1 := s.VersionOf("k")
	s.CompareAndSwap(IfVersion(v1), Put("k", []byte("v2")))
	v2 := s.VersionOf("k")

	// Two conditions on the same key with different versions cannot both hold.
	if s.Batch([]Condition{IfVersion(v1), IfVersion(v2)}, []Write{Put("k", []byte("v3"))}) {
		t.Fatal("conflicting same-key conditions must fail")
	}
	if got, _ := s.Get("k"); string(got) != "v2" {
		t.Fatalf("k = %q, want v2", got)
	}
	// Duplicate identical conditions are harmless.
	if !s.Batch([]Condition{IfVersion(v2), IfVersion(v2)}, []Write{Put("k", []byte("v3"))}) {
		t.Fatal("duplicate identical conditions should succeed")
	}
}

func TestGetMissing(t *testing.T) {
	s := New()
	val, v := s.Get("nope")
	if val != nil || v.Exists {
		t.Fatal("missing key should return nil value and Exists=false version")
	}
}
