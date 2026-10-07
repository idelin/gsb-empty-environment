// Package snaprecv receives versioned snapshots that arrive as out-of-order
// chunks, stages them until fully validated, and installs them atomically.
package snaprecv

import "errors"

var (
	// ErrStaleVersion is returned when a chunk or manifest targets a
	// version that is not newer than the installed snapshot.
	ErrStaleVersion = errors.New("snaprecv: stale version")
	// ErrChunkHashMismatch is returned when chunk bytes do not match the
	// hash declared in the manifest.
	ErrChunkHashMismatch = errors.New("snaprecv: chunk hash mismatch")
	// ErrChunkLength is returned when a chunk has the wrong length for
	// its offset (including the tail chunk).
	ErrChunkLength = errors.New("snaprecv: chunk length mismatch")
	// ErrUnknownVersion is returned when no manifest was offered for a
	// version.
	ErrUnknownVersion = errors.New("snaprecv: unknown version")
	// ErrMetaConflict is returned when a manifest conflicts with one
	// already staged for the same version.
	ErrMetaConflict = errors.New("snaprecv: conflicting manifest")
	// ErrIncomplete is returned when validation or installation is
	// attempted before every chunk was validated.
	ErrIncomplete = errors.New("snaprecv: snapshot incomplete")
	// ErrNotInstalled is returned when no snapshot has been installed.
	ErrNotInstalled = errors.New("snaprecv: nothing installed")
)

// Meta describes one snapshot version. Chunks[i] is the hex SHA-256 of
// chunk i; Hash is the hex SHA-256 of the whole snapshot.
type Meta struct {
	Version   uint64   `json:"version"`
	Size      int64    `json:"size"`
	ChunkSize int64    `json:"chunkSize"`
	Hash      string   `json:"hash"`
	Chunks    []string `json:"chunks"`
}

// NumChunks is the number of chunks implied by Size/ChunkSize.
func (m Meta) NumChunks() int {
	if m.Size == 0 {
		return 0
	}
	return int((m.Size + m.ChunkSize - 1) / m.ChunkSize)
}

// ChunkLen is the expected byte length of chunk i; the tail chunk is
// shorter when Size is not a multiple of ChunkSize.
func (m Meta) ChunkLen(i int) int64 {
	last := int64(m.NumChunks()) - 1
	if int64(i) == last {
		return m.Size - last*m.ChunkSize
	}
	return m.ChunkSize
}

func (m Meta) valid() error {
	switch {
	case m.ChunkSize <= 0:
		return errors.New("snaprecv: non-positive chunk size")
	case m.Size < 0:
		return errors.New("snaprecv: negative size")
	case len(m.Chunks) != m.NumChunks():
		return errors.New("snaprecv: chunk hash count mismatch")
	}
	return nil
}

func (m Meta) equal(o Meta) bool {
	if m.Version != o.Version || m.Size != o.Size || m.ChunkSize != o.ChunkSize || m.Hash != o.Hash || len(m.Chunks) != len(o.Chunks) {
		return false
	}
	for i := range m.Chunks {
		if m.Chunks[i] != o.Chunks[i] {
			return false
		}
	}
	return true
}
