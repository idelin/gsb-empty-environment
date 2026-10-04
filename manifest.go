package evidence

import "time"

// Kind classifies an evidence file.
type Kind string

const (
	KindTrace   Kind = "trace"
	KindLog     Kind = "log"
	KindMetrics Kind = "metrics"
)

// FileEntry describes one evidence file pinned by a manifest version.
type FileEntry struct {
	Name   string
	SHA256 string // lowercase hex digest of the file content
	Size   int64  // exact byte size
	Kind   Kind
	Start  time.Time // declared inclusive lower bound of record timestamps
	End    time.Time // declared inclusive upper bound of record timestamps
}

// Manifest is a versioned inventory of an evidence package.
// A manifest is immutable once accepted; a new version never reuses
// chunks that arrived under an older version.
type Manifest struct {
	Version string
	Files   []FileEntry
}
