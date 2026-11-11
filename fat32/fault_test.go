package fat32

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
)

func TestCrossClusterShortReadsOnFaultDisk(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	disk := newFaultDisk(img.data)
	vol, err := NewVolume(disk)
	if err != nil {
		t.Fatalf("NewVolume: %v", err)
	}
	f, err := vol.Open(start, size)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Request spans all three clusters; the disk hands back <=64 bytes per
	// ReadAt, so many physical short reads must be stitched together.
	buf := make([]byte, size)
	n, err := f.ReadAt(buf, 0)
	if err != nil || n != int(size) {
		t.Fatalf("fault-disk read: n=%d err=%v", n, err)
	}
	expectFills(t, buf, img.clusSize, []byte{'A', 'B', 'C'})

	calls, _ := disk.snapshot()
	if calls < 3*img.clusSize/64 {
		t.Fatalf("expected many short reads, got %d calls", calls)
	}
}

func TestConcurrentReadsDoNotMoveSharedPosition(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	disk := newFaultDisk(img.data)
	vol, _ := NewVolume(disk)
	f, _ := vol.Open(start, size)

	const goroutines = 16
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// Each goroutine owns a distinct logical region and re-reads it
			// many times; a shared cursor would interleave the fills and
			// make these reads non-deterministic.
			off := int64(g * (int(size) / goroutines))
			length := int(size) / goroutines
			want := make([]byte, length)
			if _, err := f.ReadAt(want, off); err != nil {
				errs <- err
				return
			}
			for repeat := 0; repeat < 20; repeat++ {
				got := make([]byte, length)
				if n, err := f.ReadAt(got, off); err != nil || n != length {
					errs <- fmt.Errorf("repeat %d: n=%d err=%w", repeat, n, err)
					return
				}
				if string(got) != string(want) {
					errs <- errors.New("non-deterministic read: shared position changed")
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}

	// Every physical read must use an absolute offset; relative reads would
	// collapse onto a small set of offsets instead of the expected spread.
	if got := disk.distinctOffsets(); got < 3 {
		t.Fatalf("expected reads at many absolute offsets, saw %d", got)
	}
}

func TestFATMismatchReturnsAgreedPrefix(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// FAT0 keeps 3 -> 4 -> 5; FAT1 diverges at cluster 4, claiming 3 -> 6.
	img.setFATEntry(1, 3, 6)
	img.setFATEntry(1, 6, eocDefault)
	img.writeCluster(6, 'D')

	vol, _ := NewVolume(img.reader())
	f, _ := vol.Open(start, size)

	// Cluster 3 (fill A) is agreed; the first ambiguity is the logical offset
	// of cluster 4, i.e. one cluster length into the file.
	buf := make([]byte, 2*img.clusSize)
	n, err := f.ReadAt(buf, 0)
	if !errors.Is(err, ErrFATMismatch) {
		t.Fatalf("expected ErrFATMismatch, got n=%d err=%v", n, err)
	}
	if n != img.clusSize {
		t.Fatalf("agreed prefix should be one cluster, got %d", n)
	}
	for i := 0; i < n; i++ {
		if buf[i] != 'A' {
			t.Fatalf("agreed prefix corrupted at %d: %q", i, buf[i])
		}
	}
	var chainErr *ChainError
	if !errors.As(err, &chainErr) {
		t.Fatalf("error does not wrap ChainError: %v", err)
	}
	if chainErr.LogicalOffset != int64(img.clusSize) || chainErr.Cluster != 4 {
		t.Fatalf("unexpected ambiguity point: %+v", chainErr)
	}

	// Reading inside the agreed prefix still succeeds without error.
	clean := make([]byte, 16)
	if _, err := f.ReadAt(clean, 10); err != nil {
		t.Fatalf("read within agreed prefix failed: %v", err)
	}
}

func TestFATMismatchWhereOneFATEndsEarly(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// FAT1 marks cluster 3 as EOC while FAT0 continues the chain.
	img.setFATEntry(1, 3, eocDefault)

	vol, _ := NewVolume(img.reader())
	f, _ := vol.Open(start, size)

	buf := make([]byte, img.clusSize+10)
	n, err := f.ReadAt(buf, 0)
	if !errors.Is(err, ErrFATMismatch) {
		t.Fatalf("expected ErrFATMismatch, got %v", err)
	}
	if n != img.clusSize {
		t.Fatalf("expected one agreed cluster, got %d", n)
	}
}

func TestCorruptChainCycle(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// Both FATs agree on a cycle 3 -> 4 -> 5 -> 4.
	img.setFATEntry(0, 5, 4)
	img.setFATEntry(1, 5, 4)

	vol, _ := NewVolume(img.reader())
	f, _ := vol.Open(start, size)

	buf := make([]byte, size)
	n, err := f.ReadAt(buf, 0)
	if !errors.Is(err, ErrBadChain) {
		t.Fatalf("expected ErrBadChain, got n=%d err=%v", n, err)
	}
	if n != 3*img.clusSize {
		t.Fatalf("expected all three clusters before the cycle, got %d", n)
	}
	expectFills(t, buf[:n], img.clusSize, []byte{'A', 'B', 'C'})

	var chainErr *ChainError
	if !errors.As(err, &chainErr) {
		t.Fatalf("no ChainError: %v", err)
	}
	if chainErr.Cluster != 4 || chainErr.LogicalOffset != int64(3*img.clusSize) {
		t.Fatalf("cycle should re-enter cluster 4 after 3 clusters: %+v", chainErr)
	}
}

func TestCorruptChainBadAndDanglingMarkers(t *testing.T) {
	cases := []struct {
		name  string
		entry uint32
	}{
		{"bad cluster marker", badCluster},
		{"reserved low cluster", 1},
		{"out-of-range cluster", firstDataCluster + testClusters + 10},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			img, start, size := buildThreeClusterFile()
			img.setFATEntry(0, 5, tc.entry)
			img.setFATEntry(1, 5, tc.entry)

			vol, _ := NewVolume(img.reader())
			f, _ := vol.Open(start, size)

			buf := make([]byte, size)
			n, err := f.ReadAt(buf, 0)
			if !errors.Is(err, ErrBadChain) {
				t.Fatalf("expected ErrBadChain, got n=%d err=%v", n, err)
			}
			if n != 3*img.clusSize {
				t.Fatalf("expected three good clusters, got %d", n)
			}
			var chainErr *ChainError
			if !errors.As(err, &chainErr) || chainErr.LogicalOffset != int64(3*img.clusSize) {
				t.Fatalf("bad failure point: %v", err)
			}
		})
	}
}

func TestPartialLastCluster(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// Physically truncate the image inside the last cluster (cluster 5).
	cut := img.clusterOffset(5) + 73
	truncated := img.data[:cut]

	vol, err := NewVolume(newFaultDisk(truncated).underlying)
	if err != nil {
		t.Fatalf("NewVolume on truncated image: %v", err)
	}
	f, err := vol.Open(start, size)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	buf := make([]byte, size)
	n, err := f.ReadAt(buf, 0)
	if !errors.Is(err, ErrTruncatedVolume) {
		t.Fatalf("expected ErrTruncatedVolume, got n=%d err=%v", n, err)
	}
	want := 2*img.clusSize + 73
	if n != want {
		t.Fatalf("expected %d salvageable bytes, got %d", want, n)
	}
	expectFills(t, buf[:2*img.clusSize], img.clusSize, []byte{'A', 'B'})
	for i := 2 * img.clusSize; i < want; i++ {
		if buf[i] != 'C' {
			t.Fatalf("partial cluster byte %d: want C got %q", i, buf[i])
		}
	}
}

func TestUnknownSizeRunsToChainEnd(t *testing.T) {
	img, start, _ := buildThreeClusterFile()
	vol, _ := NewVolume(img.reader())

	// Size zero means "unknown": the chain itself defines the readable range.
	f, err := vol.Open(start, 0)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	buf := make([]byte, 4*img.clusSize)
	n, err := f.ReadAt(buf, 0)
	if n != 3*img.clusSize || !errors.Is(err, io.EOF) {
		t.Fatalf("unknown-size read: n=%d err=%v", n, err)
	}
	expectFills(t, buf[:n], img.clusSize, []byte{'A', 'B', 'C'})
}

func TestPartialLastClusterWithSubOffset(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// Keep only 40 bytes of the final cluster; the read starts 20 bytes into
	// it, so exactly 20 salvageable bytes come back.
	cut := img.clusterOffset(5) + 40
	vol, _ := NewVolume(newFaultDisk(img.data[:cut]).underlying)
	f, _ := vol.Open(start, size)

	// Logical offset of cluster 5 (the third cluster) plus a 20-byte skip.
	readOff := int64(2*img.clusSize + 20)
	buf := make([]byte, 100)
	n, err := f.ReadAt(buf, readOff)
	if !errors.Is(err, ErrTruncatedVolume) {
		t.Fatalf("expected ErrTruncatedVolume, got n=%d err=%v", n, err)
	}
	if n != 20 {
		t.Fatalf("expected 20 bytes past the sub-offset, got %d", n)
	}
	for i := 0; i < n; i++ {
		if buf[i] != 'C' {
			t.Fatalf("byte %d: want C got %q", i, buf[i])
		}
	}
}

func TestAgreedPrefixReadAtOffset(t *testing.T) {
	img, start, size := buildThreeClusterFile()
	// FAT1 diverges after cluster 3 (claims 3 -> 6).
	img.setFATEntry(1, 3, 6)
	img.setFATEntry(1, 6, eocDefault)

	vol, _ := NewVolume(img.reader())
	f, _ := vol.Open(start, size)

	// A read fully inside cluster 3 but starting at a non-zero offset must
	// succeed even though the chain diverges one cluster later.
	buf := make([]byte, 32)
	n, err := f.ReadAt(buf, 100)
	if err != nil || n != 32 {
		t.Fatalf("agreed-prefix read: n=%d err=%v", n, err)
	}
	for i := range buf {
		if buf[i] != 'A' {
			t.Fatalf("byte %d: want A got %q", i, buf[i])
		}
	}

	// A read crossing into the ambiguous cluster returns the verified prefix
	// and the mismatch error.
	cross := make([]byte, 2*img.clusSize)
	n, err = f.ReadAt(cross, int64(img.clusSize)-8)
	if !errors.Is(err, ErrFATMismatch) {
		t.Fatalf("expected ErrFATMismatch, got n=%d err=%v", n, err)
	}
	if n != 8 {
		t.Fatalf("expected only the 8 bytes still inside cluster 3, got %d", n)
	}
}
