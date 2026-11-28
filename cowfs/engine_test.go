package cowfs

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"
)

func mkBlock(n int, tag byte) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = tag
	}
	return b
}

func infoByName(infos []SnapshotInfo, name string) SnapshotInfo {
	for _, info := range infos {
		if info.Name == name {
			return info
		}
	}
	panic("missing snapshot " + name)
}

// buildCrossOverwriteEngine constructs a four-snapshot history where two files
// are repeatedly overwritten and deleted across transaction groups.
func buildCrossOverwriteEngine(t *testing.T) *Engine {
	t.Helper()
	e := NewEngine(WithARCCapacity(8))

	mustWrite := func(file string, size int, tag byte) {
		t.Helper()
		if err := e.Write(file, mkBlock(size, tag)); err != nil {
			t.Fatalf("write %s: %v", file, err)
		}
	}
	mustSnap := func(name string) {
		t.Helper()
		if _, err := e.Snapshot(name); err != nil {
			t.Fatalf("snapshot %s: %v", name, err)
		}
	}

	// S1: a=10, b=20
	mustWrite("a", 10, 1)
	mustWrite("b", 20, 2)
	mustSnap("S1")

	// S2: overwrite a; old a(10) moves onto S2's deadlist.
	mustWrite("a", 30, 3)
	mustSnap("S2")

	// S3: overwrite b, delete a. a(30) and b(20) die between S2 and S3.
	mustWrite("b", 40, 4)
	if err := e.DeleteFile("a"); err != nil {
		t.Fatal(err)
	}
	mustSnap("S3")

	// S4: overwrite b twice; b(40) dies, the transient version is born after
	// S3 and is reclaimed immediately when displaced inside the same open txg.
	mustWrite("b", 50, 5)
	mustWrite("b", 60, 6)
	mustSnap("S4")

	if err := e.Validate(); err != nil {
		t.Fatalf("validate after build: %v", err)
	}
	return e
}

func TestCrossOverwriteReferencedAndUnique(t *testing.T) {
	e := buildCrossOverwriteEngine(t)
	infos := e.ListSnapshots()

	wantReferenced := map[string]int{
		"S1": 30, // a10 + b20
		"S2": 50, // a30 + b20
		"S3": 40, // b40
		"S4": 60, // b60
	}
	wantUnique := map[string]int{
		"S1": 10, // a10 retained solely by S1 (on S2's deadlist)
		"S2": 30, // a30 retained solely by S2 (on S3's deadlist)
		"S3": 40, // b40 retained solely by S3 (on S4's deadlist)
		"S4": 0,  // b60 is shared with the live head
	}

	for _, info := range infos {
		if info.Referenced != wantReferenced[info.Name] {
			t.Errorf("%s referenced = %d, want %d",
				info.Name, info.Referenced, wantReferenced[info.Name])
		}
		if info.UniqueUsed != wantUnique[info.Name] {
			t.Errorf("%s unique_used = %d, want %d",
				info.Name, info.UniqueUsed, wantUnique[info.Name])
		}
	}

	// S2 must retain the a10 block that died between S1 and S2.
	s2 := infoByName(infos, "S2")
	if s2.DeadlistBlocks != 1 || s2.DeadlistBytes != 10 {
		t.Fatalf("S2 deadlist = (%d blocks, %d bytes), want (1, 10)",
			s2.DeadlistBlocks, s2.DeadlistBytes)
	}

	// S3 must retain a30 and b20, both dead between S2 and S3.
	s3 := infoByName(infos, "S3")
	if s3.DeadlistBlocks != 2 || s3.DeadlistBytes != 50 {
		t.Fatalf("S3 deadlist = (%d blocks, %d bytes), want (2, 50)",
			s3.DeadlistBlocks, s3.DeadlistBytes)
	}
}

// TestUniqueUsedWithExclusiveBlocks builds a history where snapshots own blocks
// that no other snapshot and the head can still observe, then verifies
// unique_used and the actual freed bytes on deletion.
func TestUniqueUsedWithExclusiveBlocks(t *testing.T) {
	e := NewEngine(WithARCCapacity(8))

	mustWrite := func(file string, size int, tag byte) {
		t.Helper()
		if err := e.Write(file, mkBlock(size, tag)); err != nil {
			t.Fatal(err)
		}
	}
	mustSnap := func(name string) {
		t.Helper()
		if _, err := e.Snapshot(name); err != nil {
			t.Fatal(err)
		}
	}

	// S1 holds f=100; then f is overwritten so the 100-byte block is unique to S1.
	mustWrite("f", 100, 1)
	mustSnap("S1")
	mustWrite("f", 200, 2)

	// S2 holds f=200; overwrite again, leaving 200 unique to S2.
	mustSnap("S2")
	mustWrite("f", 300, 3)

	// S3 holds f=300, which the head still references, so it is not unique.
	mustSnap("S3")

	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	got := map[string]int{}
	for _, info := range e.ListSnapshots() {
		got[info.Name] = info.UniqueUsed
	}
	want := map[string]int{"S1": 100, "S2": 200, "S3": 0}
	for name, unique := range want {
		if got[name] != unique {
			t.Fatalf("%s unique_used = %d, want %d", name, got[name], unique)
		}
	}

	// Deleting S1 must free exactly its 100 exclusive bytes.
	before := e.Store().AllocatedBytes()
	freed, err := e.DeleteSnapshot("S1")
	if err != nil {
		t.Fatal(err)
	}
	if freed != 100 || e.Store().AllocatedBytes() != before-100 {
		t.Fatalf("S1 freed=%d allocated=%d", freed, e.Store().AllocatedBytes())
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	if infoByName(e.ListSnapshots(), "S2").UniqueUsed != 200 {
		t.Fatalf("S2 unique_used after S1 deletion = %d, want 200",
			infoByName(e.ListSnapshots(), "S2").UniqueUsed)
	}
}

func TestDeleteFreedBytesEqualUniqueUsed(t *testing.T) {
	names := []string{"S1", "S2", "S3", "S4"}

	for _, target := range names {
		t.Run(target, func(t *testing.T) {
			e := buildCrossOverwriteEngine(t)
			before := e.Store().AllocatedBytes()
			info := infoByName(e.ListSnapshots(), target)

			freed, err := e.DeleteSnapshot(target)
			if err != nil {
				t.Fatalf("delete: %v", err)
			}
			if freed != info.UniqueUsed {
				t.Fatalf("freed %d != reported unique_used %d",
					freed, info.UniqueUsed)
			}
			if got := e.Store().AllocatedBytes(); got != before-freed {
				t.Fatalf("allocated after delete = %d, want %d",
					got, before-freed)
			}
			if err := e.Validate(); err != nil {
				t.Fatalf("validate after delete: %v", err)
			}
		})
	}
}

func TestSnapshotContentIsImmutable(t *testing.T) {
	e := buildCrossOverwriteEngine(t)

	cases := []struct {
		snapshot string
		file     string
		want     byte
		size     int
	}{
		{"S1", "a", 1, 10},
		{"S2", "a", 3, 30},
		{"S3", "b", 4, 40},
		{"S4", "b", 6, 60},
	}
	for _, tc := range cases {
		data, err := e.ReadAtSnapshot(tc.snapshot, tc.file)
		if err != nil {
			t.Fatalf("%s/%s: %v", tc.snapshot, tc.file, err)
		}
		if len(data) != tc.size || !bytes.Equal(data, mkBlock(tc.size, tc.want)) {
			t.Fatalf("%s/%s content mismatch, size=%d", tc.snapshot, tc.file, len(data))
		}
	}

	if _, err := e.ReadAtSnapshot("S3", "a"); err == nil {
		t.Fatal("expected deleted file a to be absent in S3")
	}
}

// TestArbitraryDeletionOrders exhaustively deletes five snapshots in every
// permutation. Each deletion must free exactly its reported unique_used, every
// intermediate state must validate, and after all deletions no physical block
// may leak.
func TestArbitraryDeletionOrders(t *testing.T) {
	const snapshotCount = 5

	build := func(t *testing.T) *Engine {
		t.Helper()
		e := NewEngine(WithARCCapacity(16))
		rng := rand.New(rand.NewSource(42))
		for i := 0; i < snapshotCount; i++ {
			for f := 0; f < 4; f++ {
				file := fmt.Sprintf("f%d", f)
				if rng.Intn(5) == 0 {
					if err := e.DeleteFile(file); err != nil {
						// Absent file is fine; recreate on next round.
					}
					continue
				}
				size := 1 + rng.Intn(20)
				if err := e.Write(file, mkBlock(size, byte(i+f))); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Snapshot(fmt.Sprintf("S%d", i)); err != nil {
				t.Fatal(err)
			}
		}
		// Post-snapshot churn exercises the active head deadlist.
		for f := 0; f < 6; f++ {
			if err := e.Write(fmt.Sprintf("extra%d", f), mkBlock(3+f, 9)); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
		return e
	}

	var permute func([]int, int)
	permutations := 0
	permute = func(order []int, offset int) {
		if offset == len(order) {
			permutations++
			e := build(t)
			beforeAll := e.Store().AllocatedBytes()
			totalFreed := 0
			for _, idx := range order {
				name := fmt.Sprintf("S%d", idx)
				info := infoByName(e.ListSnapshots(), name)
				freed, err := e.DeleteSnapshot(name)
				if err != nil {
					t.Fatalf("order %v delete %s: %v", order, name, err)
				}
				if freed != info.UniqueUsed {
					t.Fatalf("order %v: %s freed %d != unique %d",
						order, name, freed, info.UniqueUsed)
				}
				totalFreed += freed
				if err := e.Validate(); err != nil {
					t.Fatalf("order %v validate: %v", order, err)
				}
			}

			remaining := e.Store().AllocatedBytes()
			if remaining != beforeAll-totalFreed {
				t.Fatalf("order %v: accounting drift %d vs %d",
					order, remaining, beforeAll-totalFreed)
			}
			// Remaining blocks belong to the live head; deleting all live files
			// must return the allocator to zero with no snapshot-held blocks.
			for _, file := range e.HeadFiles() {
				_ = e.DeleteFile(file)
			}
			if e.Store().AllocatedBytes() != 0 || e.Store().LiveBlocks() != 0 {
				t.Fatalf("order %v: physical leak after full cleanup: bytes=%d blocks=%d",
					order, e.Store().AllocatedBytes(), e.Store().LiveBlocks())
			}
			if err := e.Validate(); err != nil {
				t.Fatalf("order %v final validate: %v", order, err)
			}
			return
		}
		for i := offset; i < len(order); i++ {
			order[offset], order[i] = order[i], order[offset]
			permute(order, offset+1)
			order[offset], order[i] = order[i], order[offset]
		}
	}

	permute([]int{0, 1, 2, 3, 4}, 0)
	if permutations != 120 {
		t.Fatalf("expected 120 permutations, ran %d", permutations)
	}
}

func TestFirstMiddleLastDeletion(t *testing.T) {
	positions := []string{"S1", "S2", "S3", "S4"}
	for _, target := range positions {
		t.Run(target, func(t *testing.T) {
			e := buildCrossOverwriteEngine(t)
			info := infoByName(e.ListSnapshots(), target)
			freed, err := e.DeleteSnapshot(target)
			if err != nil {
				t.Fatal(err)
			}
			if freed != info.UniqueUsed {
				t.Fatalf("%s freed %d != unique %d", target, freed, info.UniqueUsed)
			}
			if _, err := e.DeleteSnapshot(target); err == nil {
				t.Fatalf("deleting %s twice must fail", target)
			}
			if err := e.Validate(); err != nil {
				t.Fatal(err)
			}

			// Remaining snapshots still return their original immutable data.
			for _, remaining := range e.ListSnapshots() {
				if _, err := e.ReadAtSnapshot(remaining.Name, "b"); err != nil {
					t.Fatalf("snapshot %s unreadable after deletion: %v",
						remaining.Name, err)
				}
			}
		})
	}
}

func TestImmediateFreeWithoutSnapshots(t *testing.T) {
	e := NewEngine()
	if err := e.Write("x", mkBlock(12, 1)); err != nil {
		t.Fatal(err)
	}
	if e.Store().AllocatedBytes() != 12 {
		t.Fatalf("allocated = %d", e.Store().AllocatedBytes())
	}
	// No snapshot exists, so overwrites and deletes reclaim blocks immediately.
	if err := e.Write("x", mkBlock(7, 2)); err != nil {
		t.Fatal(err)
	}
	if e.Store().AllocatedBytes() != 7 {
		t.Fatalf("allocated = %d, want 7", e.Store().AllocatedBytes())
	}
	if err := e.DeleteFile("x"); err != nil {
		t.Fatal(err)
	}
	if e.Store().AllocatedBytes() != 0 {
		t.Fatalf("allocated = %d, want zero leak", e.Store().AllocatedBytes())
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
