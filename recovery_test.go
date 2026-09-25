package btree_test

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"btree"
)

func copyDBFile(t *testing.T, srcDir, dstDir string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(srcDir, btree.FileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dstDir, btree.FileName), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// buildGolden creates a reference database with 300 keys in 128-byte pages.
func buildGolden(t *testing.T) (dir string, md *model) {
	t.Helper()
	dir = tempDir(t)
	md = newModel()
	db, err := btree.Open(dir, &btree.Options{PageSize: 128})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 300; i++ {
		k := fmt.Sprintf("k-%05d", i)
		v := fmt.Sprintf("v-%d", i)
		if err := db.Insert([]byte(k), []byte(v)); err != nil {
			t.Fatal(err)
		}
		md.put(k, v)
	}
	if err := db.Check(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dir, md
}

// TestCrashRecoveryMatrix injects a crash at every stage of the commit
// protocol (and at several page offsets inside the data flush). After
// reopening, the database must contain either the complete old state or the
// complete new state — never anything in between — and pass all structural
// checks. Runs both for a fresh insert and for a duplicate-key overwrite.
func TestCrashRecoveryMatrix(t *testing.T) {
	golden, md := buildGolden(t)

	type scenario struct {
		name string
		fp   btree.FaultPoint
		page int // FPAfterDataPage only: crash on this page-write index
	}
	var scenarios []scenario
	for k := 0; k < 8; k++ {
		scenarios = append(scenarios, scenario{fmt.Sprintf("dataPage#%d", k), btree.FPAfterDataPage, k})
	}
	scenarios = append(scenarios,
		scenario{"dataWrite", btree.FPAfterDataWrite, -1},
		scenario{"dataSync", btree.FPAfterDataSync, -1},
		scenario{"superWrite", btree.FPAfterSuperWrite, -1},
		scenario{"superSync", btree.FPAfterSuperSync, -1},
	)

	for _, overwrite := range []bool{false, true} {
		for _, sc := range scenarios {
			name := fmt.Sprintf("overwrite=%v/%s", overwrite, sc.name)
			t.Run(name, func(t *testing.T) {
				dir := tempDir(t)
				copyDBFile(t, golden, dir)

				pageCount := 0
				db, err := btree.Open(dir, &btree.Options{FaultHook: func(fp btree.FaultPoint) bool {
					if fp != sc.fp {
						return false
					}
					if sc.fp == btree.FPAfterDataPage {
						if pageCount == sc.page {
							return true
						}
						pageCount++
						return false
					}
					return true
				}})
				if err != nil {
					t.Fatal(err)
				}

				key := "k-00150a"
				val := "brand-new"
				if overwrite {
					key = "k-00150"
					val = "overwritten"
				}
				insErr := db.Insert([]byte(key), []byte(val))
				if insErr == nil {
					// Hook never fired (page index beyond this commit's
					// write count): the insert committed cleanly.
					db.Close()
					return
				}
				if insErr != btree.ErrCrashed {
					t.Fatalf("want ErrCrashed, got %v", insErr)
				}
				// Dead handle must reject everything.
				if err := db.Insert([]byte("x"), []byte("y")); err != btree.ErrCrashed {
					t.Fatalf("dead handle accepted insert: %v", err)
				}
				if _, _, err := db.Get([]byte("k-00000")); err != btree.ErrCrashed {
					t.Fatalf("dead handle accepted get: %v", err)
				}
				db.Close()

				// Reopen and recover.
				db2, err := btree.Open(dir, nil)
				if err != nil {
					t.Fatalf("recovery failed: %v", err)
				}
				defer db2.Close()
				if err := db2.Check(); err != nil {
					t.Fatalf("recovered tree inconsistent: %v", err)
				}

				oldWant := md.sorted()
				newMD := newModel()
				for k, v := range md.m {
					newMD.m[k] = v
				}
				newMD.put(key, val)
				newWant := newMD.sorted()

				got := scanAll(t, mustSnapshot(t, db2))
				isOld := equalKVs(got, oldWant)
				isNew := equalKVs(got, newWant)
				if isOld == isNew {
					t.Fatalf("recovered state is neither the complete old nor the complete new state (isOld=%v isNew=%v, got %d entries)", isOld, isNew, len(got))
				}
				t.Logf("recovered to %s state", map[bool]string{true: "old", false: "new"}[isOld])

				// The recovered database must accept further commits.
				if err := db2.Insert([]byte("k-after-recovery"), []byte("1")); err != nil {
					t.Fatalf("insert after recovery: %v", err)
				}
				if err := db2.Check(); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
}

// TestLeafWrittenParentNotPublished pins down the two sides of the atomic
// commit boundary: data durable but commit record not published recovers to
// the OLD state; commit record published recovers to the NEW state.
func TestLeafWrittenParentNotPublished(t *testing.T) {
	t.Run("data-synced-but-superblock-not-published", func(t *testing.T) {
		dir := tempDir(t)
		md := newModel()
		armed := false
		db, err := btree.Open(dir, &btree.Options{
			PageSize: 128,
			FaultHook: func(fp btree.FaultPoint) bool {
				return armed && fp == btree.FPAfterDataSync
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 200; i++ {
			k := fmt.Sprintf("k-%04d", i)
			if err := db.Insert([]byte(k), []byte("v")); err != nil {
				t.Fatal(err)
			}
			md.put(k, "v")
		}
		genBefore := db.Gen()
		armed = true
		// At this point in the commit the new leaf (and its COW parent
		// chain) is written and fsynced, but the superblock is not.
		if err := db.Insert([]byte("k-99999"), []byte("new")); err != btree.ErrCrashed {
			t.Fatalf("want ErrCrashed, got %v", err)
		}
		db.Close()

		db2, err := btree.Open(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer db2.Close()
		if db2.Gen() != genBefore {
			t.Fatalf("generation advanced without a published commit: %d != %d", db2.Gen(), genBefore)
		}
		if got := scanAll(t, mustSnapshot(t, db2)); !equalKVs(got, md.sorted()) {
			t.Fatal("expected the complete OLD state")
		}
		if err := db2.Check(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("superblock-published", func(t *testing.T) {
		dir := tempDir(t)
		md := newModel()
		armed := false
		db, err := btree.Open(dir, &btree.Options{
			PageSize: 128,
			FaultHook: func(fp btree.FaultPoint) bool {
				return armed && fp == btree.FPAfterSuperSync
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 200; i++ {
			k := fmt.Sprintf("k-%04d", i)
			if err := db.Insert([]byte(k), []byte("v")); err != nil {
				t.Fatal(err)
			}
			md.put(k, "v")
		}
		armed = true
		if err := db.Insert([]byte("k-99999"), []byte("new")); err != btree.ErrCrashed {
			t.Fatalf("want ErrCrashed, got %v", err)
		}
		md.put("k-99999", "new")
		db.Close()

		db2, err := btree.Open(dir, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer db2.Close()
		if got := scanAll(t, mustSnapshot(t, db2)); !equalKVs(got, md.sorted()) {
			t.Fatal("expected the complete NEW state")
		}
		if err := db2.Check(); err != nil {
			t.Fatal(err)
		}
	})
}

// TestTornSuperblockWrite simulates a power loss in the middle of the
// superblock slot write: the slot is half garbage, its CRC fails, and
// recovery must fall back to the previous generation in the other slot.
func TestTornSuperblockWrite(t *testing.T) {
	dir := tempDir(t)
	md := newModel()
	armed := false
	db, err := btree.Open(dir, &btree.Options{
		PageSize: 128,
		FaultHook: func(fp btree.FaultPoint) bool {
			return armed && fp == btree.FPAfterSuperWrite
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		k := fmt.Sprintf("k-%04d", i)
		if err := db.Insert([]byte(k), []byte("v")); err != nil {
			t.Fatal(err)
		}
		md.put(k, "v")
	}
	gen := db.Gen()
	armed = true
	if err := db.Insert([]byte("k-99999"), []byte("new")); err != btree.ErrCrashed {
		t.Fatalf("want ErrCrashed, got %v", err)
	}
	db.Close()

	// Corrupt the just-written slot as a torn write would.
	slot := (gen + 1) % 2
	f, err := os.OpenFile(filepath.Join(dir, btree.FileName), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, 24), int64(slot)*4096+8); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db2, err := btree.Open(dir, nil)
	if err != nil {
		t.Fatalf("recovery from torn superblock failed: %v", err)
	}
	defer db2.Close()
	if db2.Gen() != gen {
		t.Fatalf("torn write was adopted: gen %d != %d", db2.Gen(), gen)
	}
	if got := scanAll(t, mustSnapshot(t, db2)); !equalKVs(got, md.sorted()) {
		t.Fatal("expected the complete OLD state after torn superblock write")
	}
	if err := db2.Check(); err != nil {
		t.Fatal(err)
	}
	// And it keeps accepting commits afterwards.
	if err := db2.Insert([]byte("k-99999"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := db2.Check(); err != nil {
		t.Fatal(err)
	}
}
