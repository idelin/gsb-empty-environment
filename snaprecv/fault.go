package snaprecv

// CutPoint identifies a crash-injection point on a persistence boundary.
// Tests install a Hook to simulate power loss between two durable writes.
type CutPoint int

const (
	CutChunkBeforeRename    CutPoint = iota // chunk temp file written+fsynced, before atomic rename into pool
	CutManifestBeforeRename                 // manifest temp written+fsynced, before atomic rename
	CutSnapshotBeforeRename                 // assembled snapshot fsynced, before rename to immutable name
	CutPointerBeforeRename                  // pointer temp written+fsynced, before atomic rename
	CutPointerAfterRename                   // pointer swapped, before directory fsync
	numCutPoints
)

func (c CutPoint) String() string {
	switch c {
	case CutChunkBeforeRename:
		return "chunk-before-rename"
	case CutManifestBeforeRename:
		return "manifest-before-rename"
	case CutSnapshotBeforeRename:
		return "snapshot-before-rename"
	case CutPointerBeforeRename:
		return "pointer-before-rename"
	case CutPointerAfterRename:
		return "pointer-after-rename"
	}
	return "unknown"
}

// Hook is invoked at every CutPoint. Returning a non-nil error aborts the
// operation as if the process crashed at that instant: no further writes
// happen, and the Store must be discarded and re-opened with Open.
type Hook func(CutPoint) error

// CutPoints lists all cut points, for exhaustive fault-injection tests.
func CutPoints() []CutPoint {
	cps := make([]CutPoint, 0, int(numCutPoints))
	for i := 0; i < int(numCutPoints); i++ {
		cps = append(cps, CutPoint(i))
	}
	return cps
}
