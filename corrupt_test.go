package bptree

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCorruptDataPageDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "corrupt.db")
	db, err := Open(path, 4)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Update(func(tx *Tx) error {
		for i := int64(0); i < 60; i++ {
			if err := tx.Put(i, val("data")); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := db.CheckIntegrity(); err != nil {
		t.Fatalf("CheckIntegrity on healthy file: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Corrupt one byte inside the used region of the root page.
	root := db.Root()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	off := int64(root)*PageSize + 33
	b := []byte{0}
	if _, err := f.ReadAt(b, off); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xFF
	if _, err := f.WriteAt(b, off); err != nil {
		t.Fatal(err)
	}
	f.Close()

	db2, err := Open(path, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer db2.Close()
	err = db2.CheckIntegrity()
	if err == nil {
		t.Fatalf("CheckIntegrity did not detect corruption")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("err = %v, want ErrCorrupt", err)
	}
	t.Logf("corruption detected: %v", err)
}

func TestCorruptMetaPageDetected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "meta.db")
	db, err := Open(path, 4)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := db.Update(func(tx *Tx) error { return tx.Put(1, val("x")) }); err != nil {
		t.Fatalf("Update: %v", err)
	}
	db.Close()

	// Corrupt the meta page checksum region.
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte{0xAA, 0xBB}, 20); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if _, err := Open(path, 0); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open err = %v, want ErrCorrupt", err)
	}
}
