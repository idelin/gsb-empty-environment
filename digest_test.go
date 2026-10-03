package main

import "testing"

func TestDigestIncremental(t *testing.T) {
	n := NewNode("x")
	n.Upsert(Member{ID: "m1", State: Alive, Version: 1})
	n.Upsert(Member{ID: "m2", State: Alive, Version: 1})
	n.Upsert(Member{ID: "m3", State: Suspect, Version: 3})
	d1 := n.Digest()

	// 从零重算，验证增量维护的摘要与全量计算一致
	fresh := NewNode("y")
	for _, m := range n.Members() {
		fresh.Upsert(m)
	}
	if fresh.Digest() != d1 {
		t.Fatalf("rebuilt digest mismatch: %08x != %08x", fresh.Digest(), d1)
	}

	// 版本更新（状态变更）应改变摘要
	n.Upsert(Member{ID: "m2", State: Dead, Version: 2})
	if n.Digest() == d1 {
		t.Fatal("digest should change after state update")
	}

	// 旧版本写入不应生效
	if n.Upsert(Member{ID: "m2", State: Alive, Version: 1}) {
		t.Fatal("stale version should be rejected")
	}
	m, _ := n.members["m2"]
	if m.State != Dead || m.Version != 2 {
		t.Fatalf("stale write corrupted member: %+v", m)
	}
}

func TestDigestMismatchDetection(t *testing.T) {
	a, b := NewNode("a"), NewNode("b")
	for _, id := range []string{"n1", "n2", "n3"} {
		a.Upsert(Member{ID: id, State: Alive, Version: 1})
		b.Upsert(Member{ID: id, State: Alive, Version: 1})
	}
	if a.Digest() != b.Digest() {
		t.Fatal("identical views must have identical digests")
	}
	b.Upsert(Member{ID: "n4", State: Alive, Version: 1})
	if a.Digest() == b.Digest() {
		t.Fatal("diverged views must produce different digests")
	}
}

func TestBucketIsolation(t *testing.T) {
	a, b := NewNode("a"), NewNode("b")
	a.Upsert(Member{ID: "x1", State: Alive, Version: 1})
	b.Upsert(Member{ID: "x1", State: Alive, Version: 1})
	b.Upsert(Member{ID: "x2", State: Alive, Version: 1})

	da, db := a.BucketDigests(), b.BucketDigests()
	diff := 0
	for i := 0; i < NumBuckets; i++ {
		if da[i] != db[i] {
			diff++
		}
	}
	if diff != 1 {
		t.Fatalf("expected exactly 1 differing bucket, got %d", diff)
	}
	if diffBucket := bucketOf("x2"); da[diffBucket] == db[diffBucket] {
		t.Fatal("bucket containing x2 should differ")
	}
}
