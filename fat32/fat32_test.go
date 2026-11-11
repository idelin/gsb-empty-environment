package fat32

import (
	"errors"
	"io"
	"testing"
)

// buildThreeClusterFile lays out a three-cluster chain (3 -> 4 -> 5) whose
// contents are distinct byte fills, plus a matching root directory entry.
func buildThreeClusterFile() (*fatImage, uint32, uint32) {
	img := newFATImage()
	img.setChain(3, 4, 5)
	img.writeCluster(3, 'A')
	img.writeCluster(4, 'B')
	img.writeCluster(5, 'C')
	size := uint32(3 * img.clusSize)
	img.addRootEntry("EVIDENCE.TXT", 3, size, false)
	return img, 3, size
}

func expectFills(t *testing.T, buf []byte, clusterSize int, fills []byte) {
	t.Helper()
	for i, b := range buf {
		if b != fills[i/clusterSize] {
			t.Fatalf("byte %d: want %q got %q", i, fills[i/clusterSize], b)
		}
	}
}

func TestNewVolumeRejectsGarbage(t *testing.T) {
	if _, err := NewVolume(newFaultDisk(make([]byte, bootSectorSize)).underlying); !errors.Is(err, ErrNotFAT32) {
		t.Fatalf("expected ErrNotFAT32, got %v", err)
	}

	img := newFATImage()
	img.data[510] = 0x00 // break signature
	if _, err := NewVolume(img.reader()); !errors.Is(err, ErrNotFAT32) {
		t.Fatalf("broken signature: expected ErrNotFAT32, got %v", err)
	}
}

func TestOpenRejectsOutOfRangeCluster(t *testing.T) {
	img := newFATImage()
	vol, err := NewVolume(img.reader())
	if err != nil {
		t.Fatalf("NewVolume: %v", err)
	}
	if _, err := vol.Open(999, 10); !errors.Is(err, ErrBadChain) {
		t.Fatalf("expected ErrBadChain, got %v", err)
	}
}

func TestReadAtFullAndSubrange(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	vol, err := NewVolume(img.reader())
	if err != nil {
		t.Fatalf("NewVolume: %v", err)
	}
	f, err := vol.Open(start, size)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	full := make([]byte, size)
	if n, err := f.ReadAt(full, 0); err != nil || n != int(size) {
		t.Fatalf("full read: n=%d err=%v", n, err)
	}
	expectFills(t, full, img.clusSize, []byte{'A', 'B', 'C'})

	// Unaligned window spanning the A/B and B/C boundaries.
	window := make([]byte, img.clusSize+40)
	off := int64(img.clusSize - 20)
	if n, err := f.ReadAt(window, off); err != nil || n != len(window) {
		t.Fatalf("window read: n=%d err=%v", n, err)
	}
	// Window covers file [492,1044): 20 A-bytes, then B-bytes; the C region
	// starts at index 532.
	if window[0] != 'A' || window[20] != 'B' || window[532] != 'C' {
		t.Fatalf("window contents wrong: %q ... %q ... %q",
			window[0], window[20], window[532])
	}
}

func TestReadAtEOFBehavior(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	vol, _ := NewVolume(img.reader())
	f, _ := vol.Open(start, size)

	// Zero bytes at EOF => io.EOF.
	if _, err := f.ReadAt(make([]byte, 4), int64(size)); !errors.Is(err, io.EOF) {
		t.Fatalf("read at EOF: expected io.EOF, got %v", err)
	}

	// One byte before EOF: exactly one byte plus io.EOF.
	tail := make([]byte, 4)
	n, err := f.ReadAt(tail, int64(size)-1)
	if n != 1 || !errors.Is(err, io.EOF) || tail[0] != 'C' {
		t.Fatalf("tail read: n=%d err=%v byte=%q", n, err, tail[0])
	}

	// Negative offset is a chain error rather than a panic.
	if _, err := f.ReadAt(make([]byte, 1), -1); err == nil {
		t.Fatal("expected error for negative offset")
	}
}

func TestRootLookupAndOpenRoot(t *testing.T) {
	img, _, size := buildThreeClusterFile()
	vol, err := NewVolume(img.reader())
	if err != nil {
		t.Fatalf("NewVolume: %v", err)
	}

	entry, err := vol.RootEntry("evidence.txt") // case-insensitive
	if err != nil {
		t.Fatalf("RootEntry: %v", err)
	}
	if entry.Size != size || entry.Cluster != 3 || entry.IsDirectory {
		t.Fatalf("unexpected entry: %+v", entry)
	}

	f, err := vol.OpenRoot("EVIDENCE.TXT")
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	buf := make([]byte, 8)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "AAAAAAAA" {
		t.Fatalf("OpenRoot read: err=%v data=%q", err, buf)
	}

	if _, err := vol.RootEntry("MISSING.TXT"); err == nil {
		t.Fatal("expected not-found error")
	}
}
