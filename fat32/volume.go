package fat32

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// EOC (end of cluster chain) marker and related FAT32 entry constants.
const (
	eocDefault  uint32 = 0x0FFFFFFF
	badCluster  uint32 = 0x0FFFFFF7
	clusterMask uint32 = 0x0FFFFFFF

	// cluster 0 and 1 hold the FAT id / reserved marker, so data clusters
	// always start at number 2.
	firstDataCluster uint32 = 2

	// bootSectorSize is the fixed size of the FAT32 boot sector.
	bootSectorSize = 512
)

// Sentinel errors. Callers should use errors.Is to match them; they may be
// wrapped together with a ChainError describing the exact failure point.
var (
	// ErrNotFAT32 is returned when the supplied image does not carry a
	// recognizable FAT32 boot sector.
	ErrNotFAT32 = errors.New("fat32: not a FAT32 volume")

	// ErrTruncatedVolume is returned when the image ends before the region
	// described by the boot sector (for example a partial last cluster).
	ErrTruncatedVolume = errors.New("fat32: volume image is truncated")

	// ErrBadChain is returned when a cluster chain cannot be followed:
	// dangling pointers, reserved/bad markers, cycles or out-of-range
	// clusters. The wrapped ChainError carries the precise offset.
	ErrBadChain = errors.New("fat32: corrupt cluster chain")

	// ErrFATMismatch is returned when the two FAT copies describe different
	// chains. The wrapped ChainError reports the first ambiguous offset and
	// the data that both copies agree on up to that point.
	ErrFATMismatch = errors.New("fat32: FAT copies disagree")
)

// ChainError describes the point at which a cluster chain became unusable.
//
// LogicalOffset is the byte offset within the file where the problem starts;
// every byte before that offset could be verified against both FAT copies.
// Cluster is the data cluster number that caused the failure (0 when the
// failure is a physical truncation rather than a chain problem).
type ChainError struct {
	LogicalOffset int64
	Cluster       uint32
	Reason        error
}

func (e *ChainError) Error() string {
	return fmt.Sprintf("fat32: chain failure at file offset %d (cluster %d): %v",
		e.LogicalOffset, e.Cluster, e.Reason)
}

func (e *ChainError) Unwrap() error { return e.Reason }

// Volume is a read-only view over a FAT32 image. The backing storage is never
// fully loaded into memory: every read goes through the image's ReaderAt.
//
// A Volume is safe for concurrent use as long as the underlying ReaderAt is.
type Volume struct {
	image io.ReaderAt

	bytesPerSector uint16
	sectorsPerClus uint8
	reservedSecs   uint16
	numFATs        uint8
	rootCluster    uint32
	totalSectors   uint32

	sectorsPerFAT uint32
	fatOffset     int64
	dataOffset    int64
	clusterBytes  int64
	totalClusters uint32
}

// NewVolume parses the FAT32 boot sector accessible through image. Only the
// first sector is read eagerly; everything else is resolved on demand.
func NewVolume(image io.ReaderAt) (*Volume, error) {
	boot := make([]byte, bootSectorSize)
	if err := readFullAt(image, boot, 0); err != nil {
		return nil, fmt.Errorf("fat32: reading boot sector: %w", err)
	}

	if !looksLikeFAT32BootSector(boot) {
		return nil, ErrNotFAT32
	}

	v := &Volume{
		image:          image,
		bytesPerSector: binary.LittleEndian.Uint16(boot[11:13]),
		sectorsPerClus: boot[13],
		reservedSecs:   binary.LittleEndian.Uint16(boot[14:16]),
		numFATs:        boot[16],
		sectorsPerFAT:  binary.LittleEndian.Uint32(boot[36:40]),
		rootCluster:    binary.LittleEndian.Uint32(boot[44:48]),
	}
	v.totalSectors = binary.LittleEndian.Uint32(boot[32:36])
	if v.totalSectors == 0 {
		v.totalSectors = uint32(binary.LittleEndian.Uint16(boot[19:21]))
	}

	switch {
	case v.bytesPerSector == 0 || v.bytesPerSector&(v.bytesPerSector-1) != 0:
		return nil, fmt.Errorf("%w: invalid bytes-per-sector %d", ErrNotFAT32, v.bytesPerSector)
	case v.sectorsPerClus == 0:
		return nil, fmt.Errorf("%w: zero sectors per cluster", ErrNotFAT32)
	case v.numFATs < 1:
		return nil, fmt.Errorf("%w: volume declares no FAT", ErrNotFAT32)
	case v.sectorsPerFAT == 0:
		return nil, fmt.Errorf("%w: zero sectors per FAT", ErrNotFAT32)
	}

	sectorBytes := int64(v.bytesPerSector)
	v.clusterBytes = int64(v.sectorsPerClus) * sectorBytes
	v.fatOffset = int64(v.reservedSecs) * sectorBytes
	v.dataOffset = v.fatOffset + int64(v.numFATs)*int64(v.sectorsPerFAT)*sectorBytes

	// Number of data clusters derives from the geometry rather than from the
	// (unreliable on damaged media) FSInfo sector.
	dataSectors := int64(v.totalSectors) -
		(int64(v.reservedSecs) + int64(v.numFATs)*int64(v.sectorsPerFAT))
	if dataSectors < 0 {
		return nil, fmt.Errorf("%w: metadata extends past volume", ErrNotFAT32)
	}
	v.totalClusters = uint32(dataSectors / int64(v.sectorsPerClus))

	return v, nil
}

// readFullAt fills buf entirely from image at absolute offset off. The
// io.ReaderAt contract permits implementations to return short counts without
// EOF (notably fault-injecting or network-backed storage), so this retries
// until the buffer is full or a real error/EOF stops progress.
func readFullAt(image io.ReaderAt, buf []byte, off int64) error {
	for len(buf) > 0 {
		n, err := image.ReadAt(buf, off)
		buf = buf[n:]
		off += int64(n)
		if err == nil {
			continue
		}
		// A short final read may arrive together with io.EOF; only treat EOF
		// as terminal when no bytes remain to be filled.
		if errors.Is(err, io.EOF) && len(buf) == 0 {
			return nil
		}
		if n == 0 {
			return err
		}
	}
	return nil
}

// looksLikeFAT32BootSector performs the signature and jump-instruction checks
// mandated by the FAT32 specification and confirms that the FAT16 root-entry
// count is zero (a distinguishing FAT32 trait).
func looksLikeFAT32BootSector(b []byte) bool {
	if len(b) < bootSectorSize || b[510] != 0x55 || b[511] != 0xAA {
		return false
	}
	jmp := b[0]
	if !((jmp == 0xEB && b[2] == 0x90) || jmp == 0xE9) {
		return false
	}
	if binary.LittleEndian.Uint16(b[17:19]) != 0 {
		return false // FAT12/FAT16 carry a root directory entry count here
	}
	return true
}

// ClusterSize reports the number of bytes in one data cluster.
func (v *Volume) ClusterSize() int { return int(v.clusterBytes) }

// clusterOffset translates a data cluster number to an absolute image offset.
func (v *Volume) clusterOffset(cluster uint32) int64 {
	return v.dataOffset + int64(cluster-firstDataCluster)*v.clusterBytes
}

// readFATEntry reads one 32-bit FAT entry, low 28 bits only, from the given
// FAT copy (0-based).
func (v *Volume) readFATEntry(fat int, cluster uint32) (uint32, error) {
	off := v.fatOffset +
		int64(fat)*int64(v.sectorsPerFAT)*int64(v.bytesPerSector) +
		int64(cluster)*4
	buf := make([]byte, 4)
	if err := readFullAt(v.image, buf, off); err != nil {
		return 0, fmt.Errorf("reading FAT%d entry for cluster %d: %w", fat, cluster, err)
	}
	return binary.LittleEndian.Uint32(buf) & clusterMask, nil
}

// readCluster loads one data cluster into dst, which must be at least
// ClusterSize bytes long, retrying across short reads as required by the
// ReaderAt contract. On a physically truncated image it returns the bytes that
// still exist together with io.EOF (a partial last cluster).
func (v *Volume) readCluster(cluster uint32, dst []byte) (int, error) {
	if cluster < firstDataCluster || cluster >= firstDataCluster+v.totalClusters {
		return 0, fmt.Errorf("%w: cluster %d is outside the data region", ErrBadChain, cluster)
	}
	off := v.clusterOffset(cluster)
	buf := dst[:v.clusterBytes]
	total := 0
	for len(buf) > 0 {
		n, err := v.image.ReadAt(buf, off)
		total += n
		if n > 0 {
			buf = buf[n:]
			off += int64(n)
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			// Reached the physical end of the image inside this cluster.
			return total, io.EOF
		}
		if n == 0 {
			return total, fmt.Errorf("reading cluster %d: %w", cluster, err)
		}
	}
	return total, nil
}
