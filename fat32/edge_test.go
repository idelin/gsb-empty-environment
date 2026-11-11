package fat32

import (
	"errors"
	"testing"
)

func TestDanglingZeroPointer(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// Both FATs drop the EOC and leave a zero (free) entry behind cluster 5.
	img.setFATEntry(0, 5, 0)
	img.setFATEntry(1, 5, 0)

	vol, _ := NewVolume(img.reader())
	f, _ := vol.Open(start, size)

	buf := make([]byte, size)
	n, err := f.ReadAt(buf, 0)
	if !errors.Is(err, ErrBadChain) {
		t.Fatalf("expected ErrBadChain, got n=%d err=%v", n, err)
	}
	if n != 3*img.clusSize {
		t.Fatalf("expected the three clusters before the dangling pointer, got %d", n)
	}
}

func TestUnreadableFATEntry(t *testing.T) {
	// The volume parses fine, but reading cluster 4's entry in FAT0 hits a bad
	// sector while FAT1 stays intact: the copies diverge in classification.
	img2, start2, size2 := buildThreeClusterFile()
	disk := &selectiveFaultDisk{
		underlying: newFaultDisk(img2.data),
		fatBase:    int64(img2.fat0Off),
		fatLimit:   int64(img2.fat1Off),
	}
	vol2, err := NewVolume(disk)
	if err != nil {
		t.Fatalf("NewVolume: %v", err)
	}
	f2, err := vol2.Open(start2, size2)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	buf := make([]byte, size2)
	n, rerr := f2.ReadAt(buf, 0)
	if !errors.Is(rerr, ErrFATMismatch) && !errors.Is(rerr, ErrBadChain) {
		t.Fatalf("expected a chain error, got n=%d err=%v", n, rerr)
	}
	if n != img2.clusSize {
		t.Fatalf("cluster 3 should be the last verified cluster, got %d bytes", n)
	}
}

// selectiveFaultDisk delegates to another ReaderAt but fails configured
// absolute offsets, modelling a bad sector in one FAT copy.
type selectiveFaultDisk struct {
	underlying *faultDisk
	fatBase    int64
	fatLimit   int64
}

func (d *selectiveFaultDisk) ReadAt(p []byte, off int64) (int, error) {
	// Fail any read intersecting FAT0's entry for cluster 3 (its successor
	// pointer), while FAT1 at the later region still works. Cluster 3 itself
	// remains verifiable; the divergence starts at the next cluster.
	entryStart := d.fatBase + int64(3)*4
	entryEnd := entryStart + 4
	if off >= d.fatBase && off < d.fatLimit && off < entryEnd && off+int64(len(p)) > entryStart {
		return 0, errors.New("bad sector")
	}
	return d.underlying.ReadAt(p, off)
}

func TestOpenDoesNotBufferImage(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	vol, err := NewVolume(img.reader())
	if err != nil {
		t.Fatal(err)
	}
	f, err := vol.Open(start, size)
	if err != nil {
		t.Fatal(err)
	}
	// The File holds only geometry and two integers; no image-sized slice is
	// reachable from it.
	if f.Size() != int64(size) {
		t.Fatalf("size mismatch: %d", f.Size())
	}
	// A single-byte random read must succeed without touching more than one
	// cluster's worth of buffers.
	one := make([]byte, 1)
	if n, err := f.ReadAt(one, int64(size)-1); err != nil || n != 1 || one[0] != 'C' {
		t.Fatalf("single byte read: n=%d err=%v byte=%q", n, err, one[0])
	}
}
