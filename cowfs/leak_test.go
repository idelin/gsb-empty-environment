package cowfs

import (
	"math/rand"
	"testing"
)

// TestFullReclamationReturnsToZero builds a heavily churned history, removes
// every snapshot in a randomized order and then every live file, and asserts
// the allocator returns to zero live blocks and zero allocated bytes.
func TestFullReclamationReturnsToZero(t *testing.T) {
	e := NewEngine(WithARCCapacity(20))
	rng := rand.New(rand.NewSource(7))

	for round := 0; round < 8; round++ {
		for f := 0; f < 5; f++ {
			file := "file-" + string(rune('a'+f))
			switch rng.Intn(3) {
			case 0:
				_ = e.DeleteFile(file)
			default:
				if err := e.Write(file, mkBlock(1+rng.Intn(30), byte(round+f))); err != nil {
					t.Fatal(err)
				}
			}
		}
		if _, err := e.Snapshot("s" + string(rune('0'+round))); err != nil {
			t.Fatal(err)
		}
	}

	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0)
	for _, info := range e.ListSnapshots() {
		names = append(names, info.Name)
	}
	rng.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })

	for _, name := range names {
		info := infoByName(e.ListSnapshots(), name)
		freed, err := e.DeleteSnapshot(name)
		if err != nil {
			t.Fatal(err)
		}
		if freed != info.UniqueUsed {
			t.Fatalf("%s freed %d != unique %d", name, freed, info.UniqueUsed)
		}
		if err := e.Validate(); err != nil {
			t.Fatal(err)
		}
	}

	for _, file := range e.HeadFiles() {
		if err := e.DeleteFile(file); err != nil {
			t.Fatal(err)
		}
	}

	if e.Store().LiveBlocks() != 0 || e.Store().AllocatedBytes() != 0 {
		t.Fatalf("physical leak: live=%d bytes=%d",
			e.Store().LiveBlocks(), e.Store().AllocatedBytes())
	}
	if e.Store().FreeListLen() == 0 {
		t.Fatal("expected freed ids available for reuse")
	}
	if err := e.Validate(); err != nil {
		t.Fatal(err)
	}
}
