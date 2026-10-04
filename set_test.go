package idset

import (
	"math/rand"
	"testing"
)

func TestBoundary65535_65536(t *testing.T) {
	s := New()
	for _, x := range []uint32{65534, 65535, 65536, 65537} {
		s.Add(x)
	}
	if len(s.keys) != 2 || s.keys[0] != 0 || s.keys[1] != 1 {
		t.Fatalf("blocks: got %v, want [0 1]", s.keys)
	}
	for _, x := range []uint32{65534, 65535, 65536, 65537} {
		if !s.Contains(x) {
			t.Fatalf("missing %d", x)
		}
	}
	if s.Contains(65533) || s.Contains(65538) {
		t.Fatal("unexpected member")
	}
	if got := s.Rank(65535); got != 2 {
		t.Fatalf("rank(65535) = %d, want 2", got)
	}
	if got := s.Rank(65536); got != 3 {
		t.Fatalf("rank(65536) = %d, want 3", got)
	}
	if got := s.Rank(1<<32 - 1); got != 4 {
		t.Fatalf("rank(max) = %d, want 4", got)
	}
	for k, want := range []uint32{65534, 65535, 65536, 65537} {
		got, err := s.Select(k)
		if err != nil || got != want {
			t.Fatalf("select(%d) = (%d, %v), want %d", k, got, err, want)
		}
	}
	s.Remove(65535)
	if s.Contains(65535) || s.Size() != 3 {
		t.Fatal("remove across boundary failed")
	}
	if got := s.Rank(65535); got != 1 {
		t.Fatalf("rank(65535) after remove = %d, want 1", got)
	}
}

func TestFullBlock(t *testing.T) {
	s := New()
	for i := 0; i < 65536; i++ {
		s.Add(uint32(i))
	}
	if s.Size() != 65536 || len(s.keys) != 1 {
		t.Fatalf("size=%d blocks=%d", s.Size(), len(s.keys))
	}
	s.Optimize()
	if s.cons[0].typ != cRun || len(s.cons[0].runs) != 1 {
		t.Fatalf("full block canonical type = %v, want single run", s.cons[0].typ)
	}
	if got := s.Rank(65535); got != 65536 {
		t.Fatalf("rank = %d, want 65536", got)
	}
	if v, err := s.Select(65535); err != nil || v != 65535 {
		t.Fatalf("select(65535) = (%d, %v)", v, err)
	}
	// Full block at the top of the domain.
	top := New()
	for i := 0; i < 65536; i++ {
		top.Add(0xffff0000 | uint32(i))
	}
	top.Optimize()
	if top.cons[0].typ != cRun {
		t.Fatalf("top full block type = %v", top.cons[0].typ)
	}
	if v, err := top.Select(65535); err != nil || v != 0xffffffff {
		t.Fatalf("select = (%x, %v), want ffffffff", v, err)
	}
}

func TestEmptyBlock(t *testing.T) {
	s := New()
	for i := 0; i < 100; i++ {
		s.Add(5<<16 | uint32(i))
	}
	if len(s.keys) != 1 {
		t.Fatal("expected one block")
	}
	for i := 0; i < 100; i++ {
		s.Remove(5<<16 | uint32(i))
	}
	if len(s.keys) != 0 || s.Size() != 0 {
		t.Fatalf("empty block not dropped: blocks=%v size=%d", s.keys, s.Size())
	}
	if got := len(s.Encode()); got != 4 {
		t.Fatalf("empty encoding = %d bytes, want 4", got)
	}
	if _, err := s.Select(0); err != ErrSelectRange {
		t.Fatalf("select on empty = %v, want ErrSelectRange", err)
	}
}

func TestRunSplitMerge(t *testing.T) {
	s := New()
	for i := 1000; i < 2000; i++ {
		s.Add(uint32(i))
	}
	s.Optimize()
	if s.cons[0].typ != cRun || len(s.cons[0].runs) != 1 {
		t.Fatalf("want single run, got %+v", s.cons[0].runs)
	}
	// Split the run by removing a middle element.
	s.Remove(1500)
	if s.cons[0].typ != cRun || len(s.cons[0].runs) != 2 {
		t.Fatalf("run did not split: %+v", s.cons[0].runs)
	}
	if s.cons[0].runs[0] != (ival{1000, 1499}) || s.cons[0].runs[1] != (ival{1501, 1999}) {
		t.Fatalf("split runs = %+v", s.cons[0].runs)
	}
	// Merge back by re-adding the gap element.
	s.Add(1500)
	if len(s.cons[0].runs) != 1 || s.cons[0].runs[0] != (ival{1000, 1999}) {
		t.Fatalf("run did not merge: %+v", s.cons[0].runs)
	}
	// Shrink from both ends.
	s.Remove(1000)
	s.Remove(1999)
	if s.cons[0].runs[0] != (ival{1001, 1998}) {
		t.Fatalf("shrunk run = %+v", s.cons[0].runs[0])
	}
	// Remove the last element of a singleton run.
	s2 := &Set{keys: []uint16{7}, size: 2,
		cons: []*container{runContainer(ival{0, 0}, ival{10, 10})}}
	s2.Remove(7 << 16)
	if len(s2.cons[0].runs) != 1 || s2.cons[0].runs[0] != (ival{10, 10}) {
		t.Fatalf("runs after singleton removal: %+v", s2.cons[0].runs)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	s := New()
	rng := rand.New(rand.NewSource(7))
	ref := refSet{}
	for i := 0; i < 5000; i++ {
		x := uint32(rng.Intn(200000))
		s.Add(x)
		ref.add(x)
	}
	snap := s.Snapshot()
	snapRef := refSet{}
	for x := range ref {
		snapRef.add(x)
	}
	// Heavy mutation of the original, including block removal.
	for i := 0; i < 200000; i += 3 {
		s.Remove(uint32(i))
	}
	for i := 0; i < 3000; i++ {
		s.Add(rng.Uint32())
	}
	s.Optimize()
	checkEqual(t, snap, snapRef, 0, 65535, 65536, 199999, 1<<32-1)
	if snap.Size() == s.Size() {
		t.Fatal("sizes unexpectedly equal after mutation")
	}
}

func TestSelectErrors(t *testing.T) {
	s := New()
	s.Add(1)
	s.Add(70000)
	if _, err := s.Select(-1); err != ErrSelectRange {
		t.Fatalf("select(-1) = %v", err)
	}
	if _, err := s.Select(2); err != ErrSelectRange {
		t.Fatalf("select(2) = %v", err)
	}
	if _, err := s.Select(1 << 30); err != ErrSelectRange {
		t.Fatalf("select(huge) = %v", err)
	}
}

func TestAddRemoveFuzz(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	s := New()
	ref := refSet{}
	for i := 0; i < 40000; i++ {
		var x uint32
		if rng.Float64() < 0.8 {
			x = uint32(rng.Intn(3 << 16))
		} else {
			x = rng.Uint32()
		}
		if rng.Float64() < 0.6 {
			s.Add(x)
			ref.add(x)
		} else {
			s.Remove(x)
			ref.remove(x)
		}
	}
	probes := []uint32{0, 1, 65534, 65535, 65536, 1<<16 + 65535, 2 << 16, 3<<16 - 1, 1 << 31, 1<<32 - 1}
	for i := 0; i < 200; i++ {
		probes = append(probes, rng.Uint32())
	}
	checkEqual(t, s, ref, probes...)
	// Round 2: mutate after Optimize to exercise bitmap/run mutation paths.
	s.Optimize()
	for i := 0; i < 20000; i++ {
		x := uint32(rng.Intn(3 << 16))
		if rng.Float64() < 0.5 {
			s.Add(x)
			ref.add(x)
		} else {
			s.Remove(x)
			ref.remove(x)
		}
	}
	checkEqual(t, s, ref, probes...)
}
