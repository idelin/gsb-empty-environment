package fat32

import (
	"bytes"
	"encoding/binary"
	"io"
	"sync"
)

// testGeometry describes the tiny synthetic FAT32 image used by the tests.
// 512-byte sectors, one sector per cluster keep every case hand-checkable.
const (
	testSectorSize = 512
	testSpc        = 1
	testReserved   = 32
	testNumFATs    = 2
	testFATSecs    = 1
	testRootClus   = 2
	testClusters   = 16 // data cluster numbers 2..17
)

// fatImage is a scratch FAT32 volume assembled sector by sector.
type fatImage struct {
	data     []byte
	fat0Off  int
	fat1Off  int
	dataOff  int
	clusSize int
}

func newFATImage() *fatImage {
	totalSectors := testReserved + testNumFATs*testFATSecs + testClusters*testSpc
	img := make([]byte, totalSectors*testSectorSize)

	// Boot parameter block.
	img[0] = 0xEB
	img[2] = 0x90
	copy(img[3:11], "MSDOS5.0")
	binary.LittleEndian.PutUint16(img[11:13], testSectorSize)
	img[13] = testSpc
	binary.LittleEndian.PutUint16(img[14:16], testReserved)
	img[16] = testNumFATs
	binary.LittleEndian.PutUint16(img[17:19], 0) // root entries: 0 => FAT32
	binary.LittleEndian.PutUint16(img[19:21], 0)
	img[21] = 0xF8 // media descriptor: fixed disk
	binary.LittleEndian.PutUint16(img[22:24], 0)
	binary.LittleEndian.PutUint16(img[24:26], 32)
	binary.LittleEndian.PutUint16(img[26:28], 63)
	binary.LittleEndian.PutUint32(img[28:32], 0)                    // hidden sectors
	binary.LittleEndian.PutUint32(img[32:36], uint32(totalSectors)) // 32-bit total sectors
	binary.LittleEndian.PutUint32(img[36:40], testFATSecs)
	binary.LittleEndian.PutUint32(img[40:44], 0)
	binary.LittleEndian.PutUint32(img[44:48], testRootClus)
	img[510] = 0x55
	img[511] = 0xAA

	fat0 := testReserved * testSectorSize
	fat1 := fat0 + testFATSecs*testSectorSize
	data := fat0 + testNumFATs*testFATSecs*testSectorSize

	fi := &fatImage{
		data:     img,
		fat0Off:  fat0,
		fat1Off:  fat1,
		dataOff:  data,
		clusSize: testSectorSize * testSpc,
	}

	// Mandatory FAT id words.
	fi.setFATEntry(0, 0, 0x0FFFFFF8)
	fi.setFATEntry(0, 1, eocDefault)
	fi.setFATEntry(1, 0, 0x0FFFFFF8)
	fi.setFATEntry(1, 1, eocDefault)
	// Root directory occupies a single cluster ending immediately.
	fi.setFATEntry(0, testRootClus, eocDefault)
	fi.setFATEntry(1, testRootClus, eocDefault)
	return fi
}

func (fi *fatImage) setFATEntry(fat, cluster uint32, value uint32) {
	off := fi.fat0Off
	if fat == 1 {
		off = fi.fat1Off
	}
	binary.LittleEndian.PutUint32(fi.data[off+int(cluster)*4:], value)
}

// setChain wires a cluster chain identically in both FAT copies.
func (fi *fatImage) setChain(clusters ...uint32) {
	for i := 0; i < len(clusters)-1; i++ {
		fi.setFATEntry(0, clusters[i], clusters[i+1])
		fi.setFATEntry(1, clusters[i], clusters[i+1])
	}
	fi.setFATEntry(0, clusters[len(clusters)-1], eocDefault)
	fi.setFATEntry(1, clusters[len(clusters)-1], eocDefault)
}

func (fi *fatImage) clusterOffset(cluster uint32) int {
	return fi.dataOff + int(cluster-firstDataCluster)*fi.clusSize
}

// writeCluster fills one data cluster with a recognizable per-cluster pattern.
func (fi *fatImage) writeCluster(cluster uint32, fill byte) {
	off := fi.clusterOffset(cluster)
	for i := 0; i < fi.clusSize; i++ {
		fi.data[off+i] = fill
	}
}

// addRootEntry plants a plain 8.3 directory entry in the root cluster.
func (fi *fatImage) addRootEntry(name string, cluster, size uint32, dir bool) {
	raw := make([]byte, dirEntrySize)
	fields := bytes.SplitN([]byte(name), []byte("."), 2)
	base := make([]byte, 8)
	for i := range base {
		base[i] = ' '
	}
	copy(base, fields[0])
	ext := make([]byte, 3)
	for i := range ext {
		ext[i] = ' '
	}
	if len(fields) == 2 {
		copy(ext, fields[1])
	}
	copy(raw[0:8], base)
	copy(raw[8:11], ext)
	if dir {
		raw[11] = attrDirectory
	}
	binary.LittleEndian.PutUint16(raw[20:22], uint16(cluster>>16))
	binary.LittleEndian.PutUint16(raw[26:28], uint16(cluster))
	binary.LittleEndian.PutUint32(raw[28:32], size)

	off := fi.clusterOffset(testRootClus)
	for ; off+dirEntrySize <= len(fi.data); off += dirEntrySize {
		if fi.data[off] == 0 {
			copy(fi.data[off:off+dirEntrySize], raw)
			return
		}
	}
	panic("root directory cluster full")
}

func (fi *fatImage) reader() *bytes.Reader { return bytes.NewReader(fi.data) }

// faultDisk wraps the image and models faulty storage. Every ReadAt is forced
// to return at most half of the requested bytes (and never more than 64),
// exercising the caller's short-read handling. Call counters and the most
// recent absolute offset are guarded for concurrent use.
type faultDisk struct {
	underlying *bytes.Reader

	mu          sync.Mutex
	calls       int
	lastOffsets []int64
}

func newFaultDisk(data []byte) *faultDisk {
	return &faultDisk{underlying: bytes.NewReader(data)}
}

func (d *faultDisk) ReadAt(p []byte, off int64) (int, error) {
	d.mu.Lock()
	d.calls++
	d.lastOffsets = append(d.lastOffsets, off)
	d.mu.Unlock()

	chunk := len(p)
	if max := len(p) / 2; chunk > max && max > 0 {
		chunk = max
	}
	if chunk > 64 {
		chunk = 64
	}
	if chunk < 1 {
		chunk = 1
	}
	// Cap the chunk at whatever physically remains. bytes.Reader would attach
	// io.EOF to such a boundary read, but a block device surfaces a true EOF
	// only when the caller asks past the end; a partial count with nil is the
	// fault-disk signal the production code must stitch together.
	remaining := d.underlying.Size() - off
	if int64(chunk) > remaining {
		chunk = int(remaining)
	}
	if chunk <= 0 {
		return 0, io.EOF
	}
	n, _ := d.underlying.ReadAt(p[:chunk], off)
	return n, nil
}

func (d *faultDisk) snapshot() (int, []int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	offsets := append([]int64(nil), d.lastOffsets...)
	return d.calls, offsets
}

// distinctOffsets reports how many different absolute offsets were read;
// ReaderAt implementations must never move a shared cursor.
func (d *faultDisk) distinctOffsets() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	seen := make(map[int64]struct{})
	for _, off := range d.lastOffsets {
		seen[off] = struct{}{}
	}
	return len(seen)
}
