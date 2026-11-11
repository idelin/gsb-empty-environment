package symbolcache

import (
	"bytes"
	"errors"
	"io"
	"testing"
)

func validateBytes(t *testing.T, pdb []byte, key Key) error {
	t.Helper()
	return validatePDB(bytes.NewReader(pdb), int64(len(pdb)), key)
}

func le32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

func TestValidatePDBValid(t *testing.T) {
	pdb := buildPDB(testGUID, 7, 512, nil)
	if err := validateBytes(t, pdb, testKey(testGUID, 7)); err != nil {
		t.Fatalf("valid PDB rejected: %v", err)
	}
	// Larger block size exercises multi-block streams.
	big := buildPDB(testGUID, 1, 4096, nil)
	if err := validateBytes(t, big, testKey(testGUID, 1)); err != nil {
		t.Fatalf("valid 4K-block PDB rejected: %v", err)
	}
}

func TestValidatePDBCorruptions(t *testing.T) {
	key := testKey(testGUID, 7)

	t.Run("too short", func(t *testing.T) {
		if err := validatePDB(bytes.NewReader([]byte("short")), 5, key); !errors.Is(err, ErrNotPDB) {
			t.Fatalf("err = %v, want ErrNotPDB", err)
		}
	})

	t.Run("bad magic", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { b[0] = '?' })
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrNotPDB) {
			t.Fatalf("err = %v, want ErrNotPDB", err)
		}
	})

	t.Run("bad block size", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { put32(b, offBlockSize, 1000) })
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
	})

	t.Run("block count mismatch", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { put32(b, offNumBlocks, 99) })
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
	})

	t.Run("block map out of range", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { put32(b, offBlockMapAddr, 999) })
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
	})

	t.Run("truncated directory", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { put32(b, offNumDirectoryBytes, 1000) })
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
	})

	t.Run("smashed directory", func(t *testing.T) {
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) { b[512] = 0xff })
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
	})

	t.Run("stream overlaps metadata", func(t *testing.T) {
		// Redirect the info stream block (the final directory u32) at the
		// super-block (block 0), which is reserved metadata.
		pdb := buildPDB(testGUID, 7, 512, func(b []byte) {
			dirBytes := int(le32(b[offNumDirectoryBytes:]))
			put32(b, 512+dirBytes-4, 0)
		})
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrCorruptPDB) {
			t.Fatalf("err = %v, want ErrCorruptPDB", err)
		}
	})

	t.Run("identity mismatch", func(t *testing.T) {
		pdb := buildPDB(testGUID, 8, 512, nil)
		if err := validateBytes(t, pdb, key); !errors.Is(err, ErrMismatch) {
			t.Fatalf("err = %v, want ErrMismatch", err)
		}
	})
}

// failingReaderAt always errors to ensure read failures propagate cleanly.
type failingReaderAt struct{}

func (failingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	return 0, io.ErrUnexpectedEOF
}

func TestValidatePDBReadError(t *testing.T) {
	err := validatePDB(failingReaderAt{}, 1024, testKey(testGUID, 7))
	if err == nil {
		t.Fatal("expected error from failing ReaderAt")
	}
}
