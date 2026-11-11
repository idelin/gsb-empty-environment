package fat32

import (
	"errors"
	"fmt"
	"io"
)

// File is a read-only, random-access view of one file's cluster chain.
//
// It implements io.ReaderAt: reads never mutate any shared state, so a single
// File may be used by many goroutines concurrently. Cluster data is fetched
// from the backing image on demand and only a single cluster is buffered at a
// time, so opening very large images costs O(1) memory.
type File struct {
	vol   *Volume
	start uint32
	size  uint32
}

// Open attaches a ReaderAt view to the chain beginning at startCluster.
// declaredSize bounds the logical file; pass zero when the size is unknown,
// in which case reads run until the chain ends or fails.
func (v *Volume) Open(startCluster uint32, declaredSize uint32) (*File, error) {
	if startCluster < firstDataCluster || startCluster >= firstDataCluster+v.totalClusters {
		return nil, fmt.Errorf("%w: start cluster %d is outside the data region",
			ErrBadChain, startCluster)
	}
	return &File{vol: v, start: startCluster, size: declaredSize}, nil
}

// Size reports the declared file length in bytes. It may be zero when the
// length was unknown at open time.
func (f *File) Size() int64 { return int64(f.size) }

// ReadAt reads len(p) bytes starting at logical offset off. It follows the
// io.ReaderAt contract: it never updates an internal cursor, and it returns
// io.EOF only when zero bytes are read at or past the logical end.
//
// On damaged media the call may return a short, verified prefix together with
// an error. Inspect the error with errors.Is:
//
//   - ErrFATMismatch: the FAT copies first disagree at ChainError.LogicalOffset;
//     bytes before that offset are identical under every copy.
//   - ErrBadChain: dangling pointer, bad/reserved marker, cycle or
//     out-of-range cluster at ChainError.LogicalOffset (consistent on all
//     copies).
//   - ErrTruncatedVolume: the image physically ends inside a cluster (partial
//     last cluster); the returned bytes are all that remain on disk.
func (f *File) ReadAt(p []byte, off int64) (int, error) {
	if off < 0 {
		return 0, &ChainError{LogicalOffset: off, Reason: errors.New("negative offset")}
	}
	if len(p) == 0 {
		return 0, nil
	}
	if f.size > 0 && off >= int64(f.size) {
		return 0, io.EOF
	}

	chain, chainErr := f.resolveChain()
	if len(chain) == 0 && chainErr != nil {
		return 0, chainErr
	}

	clusterBytes := f.vol.clusterBytes
	index := uint64(off) / uint64(clusterBytes)
	skip := int(uint64(off) % uint64(clusterBytes))

	buf := make([]byte, clusterBytes)
	written := 0

	for written < len(p) {
		if index >= uint64(len(chain)) {
			// Verified chain/prefix exhausted. A clean end-of-chain surfaces
			// as io.EOF; a failure (divergence, cycle, bad pointer) is handed
			// back so reads staying within the agreed prefix still succeed.
			return written, chainErr
		}

		available, err := f.vol.readCluster(chain[index], buf)
		if err != nil && !errors.Is(err, io.EOF) {
			return written, &ChainError{
				LogicalOffset: off + int64(written),
				Cluster:       chain[index],
				Reason:        err,
			}
		}
		// io.EOF with a short count means the image physically ends inside
		// this cluster (a partial last cluster). A short count with a nil
		// error is just a faulty-but-complete device read, already retried
		// inside readCluster.
		partial := errors.Is(err, io.EOF) && available < int(clusterBytes)

		take := available - skip
		if take <= 0 {
			// Physical truncation landed inside this cluster before the
			// requested sub-offset.
			return written, &ChainError{
				LogicalOffset: off + int64(written),
				Cluster:       chain[index],
				Reason:        ErrTruncatedVolume,
			}
		}
		if take > len(p)-written {
			take = len(p) - written
		}
		if f.size > 0 {
			logicalRemaining := int64(f.size) - (off + int64(written))
			if logicalRemaining <= 0 {
				break
			}
			if int64(take) > logicalRemaining {
				take = int(logicalRemaining)
			}
		}

		copy(p[written:written+take], buf[skip:skip+take])
		written += take
		skip = 0
		index++

		if partial && written < len(p) {
			// Partial last cluster: everything physically present has been
			// returned; further bytes simply do not exist in the image.
			return written, &ChainError{
				LogicalOffset: off + int64(written),
				Cluster:       chain[index-1],
				Reason:        ErrTruncatedVolume,
			}
		}
	}

	// The requested buffer is full. Surface a chain failure only when it
	// starts within or before the read range; a divergence sitting beyond the
	// requested bytes must not poison reads inside the verified prefix.
	if chainErr != nil && !errors.Is(chainErr, io.EOF) {
		if ce, ok := chainErr.(*ChainError); ok && ce.LogicalOffset > off+int64(written) {
			return written, nil
		}
		return written, chainErr
	}
	return written, nil
}

// resolveChain walks every FAT copy in lockstep from the start cluster until
// the chain ends or fails. The returned slice contains only clusters on which
// all FAT copies agree. The walk is independent of any read offset, which is
// what makes concurrent ReadAt calls share no mutable position.
//
// Two failure modes are kept apart:
//   - the copies point at different clusters (or only one of them ends or
//     fails): the first ambiguous offset is reported with ErrFATMismatch;
//   - all copies fail at the same point (identically corrupted FATs, a cycle
//     present in every copy, ...): the same offset is reported with
//     ErrBadChain and no ambiguity is claimed.
//
// A clean end of chain is reported with io.EOF, still accompanied by every
// cluster that was verified before it.
func (f *File) resolveChain() ([]uint32, error) {
	nfat := int(f.vol.numFATs)
	current := make([]uint32, nfat)
	seen := make(map[uint32]struct{})
	for i := range current {
		current[i] = f.start
	}

	agreed := make([]uint32, 0, 8)

	for {
		// Re-validate the current cluster on every copy.
		classes := make([]entryClass, nfat)
		nexts := make([]uint32, nfat)
		for i := 0; i < nfat; i++ {
			classes[i], nexts[i] = f.classify(i, current[i])
		}

		cluster := current[0]
		for i := 1; i < nfat; i++ {
			if current[i] != cluster {
				return agreed, f.ambiguityError(agreed, current, classes)
			}
		}

		// Mixed classifications or differing successors mean the current
		// cluster's outgoing pointer cannot be trusted. The cluster itself is
		// still part of the agreed prefix; the ambiguity starts at the next
		// cluster position, so accept it before reporting.
		for i := 1; i < nfat; i++ {
			if classes[i] != classes[0] ||
				(classes[i] == classNext && nexts[i] != nexts[0]) {
				agreed = append(agreed, cluster)
				return agreed, f.ambiguityError(agreed, nexts, classes)
			}
		}

		switch classes[0] {
		case classEOC:
			// The terminal cluster holds real file data; its FAT entry simply
			// has no successor. Accept it before ending the walk.
			agreed = append(agreed, cluster)
			return agreed, io.EOF
		case classNext:
			agreed = append(agreed, cluster)
			if _, dup := seen[nexts[0]]; dup {
				return agreed, &ChainError{
					LogicalOffset: int64(len(agreed)) * f.vol.clusterBytes,
					Cluster:       nexts[0],
					Reason:        fmt.Errorf("%w: %s", ErrBadChain, classCycle),
				}
			}
			seen[cluster] = struct{}{}
			for i := range current {
				current[i] = nexts[i]
			}
		default:
			// A corrupt successor pointer (bad/reserved/out-of-range/dangling
			// cluster) makes the current cluster the last readable one.
			agreed = append(agreed, cluster)
			return agreed, &ChainError{
				LogicalOffset: int64(len(agreed)) * f.vol.clusterBytes,
				Cluster:       cluster,
				Reason:        fmt.Errorf("%w: %s", ErrBadChain, classes[0]),
			}
		}
	}
}

// ambiguityError builds the ErrFATMismatch ChainError for the first cluster
// position on which the FAT copies no longer agree. Cluster names the
// divergent successor when one exists, otherwise the last agreed cluster.
func (f *File) ambiguityError(agreed []uint32, current []uint32, classes []entryClass) error {
	cluster := f.start
	if len(agreed) > 0 {
		cluster = agreed[len(agreed)-1]
	}
	// Prefer the concrete divergent successor so the caller knows which
	// cluster number the FAT copies fight over.
	for i, class := range classes {
		if class == classNext {
			cluster = current[i]
			break
		}
	}
	return &ChainError{
		LogicalOffset: int64(len(agreed)) * f.vol.clusterBytes,
		Cluster:       cluster,
		Reason:        ErrFATMismatch,
	}
}

type entryClass int

const (
	classNext entryClass = iota
	classEOC
	classBad
	classDangling
	classReserved
	classOutOfRange
	classCycle
	classReadError
)

func (c entryClass) String() string {
	switch c {
	case classNext:
		return "chain continues"
	case classEOC:
		return "end of chain"
	case classBad:
		return "bad-cluster marker"
	case classDangling:
		return "dangling pointer to free cluster 0"
	case classReserved:
		return "reserved cluster number"
	case classOutOfRange:
		return "pointer outside data region"
	case classCycle:
		return "cluster chain contains a cycle"
	case classReadError:
		return "unreadable FAT entry"
	default:
		return "unknown chain state"
	}
}

// classify validates cluster in one FAT copy and reads its successor.
// Cycle detection is handled by the caller via the set of accepted clusters.
func (f *File) classify(fat int, cluster uint32) (entryClass, uint32) {
	if cluster < firstDataCluster {
		return classReserved, 0
	}
	if cluster >= firstDataCluster+f.vol.totalClusters {
		return classOutOfRange, 0
	}
	next, err := f.vol.readFATEntry(fat, cluster)
	if err != nil {
		return classReadError, 0
	}
	if next >= 0x0FFFFFF8 {
		return classEOC, 0
	}
	if next == badCluster {
		return classBad, 0
	}
	if next < firstDataCluster {
		// A zero entry is a dangling pointer (chain ends without an EOC
		// marker); other low numbers reference reserved FAT id clusters.
		if next == 0 {
			return classDangling, 0
		}
		return classReserved, 0
	}
	if next >= firstDataCluster+f.vol.totalClusters {
		return classOutOfRange, 0
	}
	return classNext, next
}
