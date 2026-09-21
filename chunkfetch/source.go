package chunkfetch

import (
	"context"
	"crypto/sha256"
	"errors"
	"strconv"
)

// VersionMismatchError reports that the object currently served by the source
// is no longer the version the caller asked about. Old verified progress must
// be discarded in that case.
type VersionMismatchError struct {
	Wanted string
	Got    string
}

func (e *VersionMismatchError) Error() string {
	return "chunkfetch: source version changed: wanted " + e.Wanted + ", got " + e.Got
}

// Manifest describes one immutable version of a chunked object.
type Manifest struct {
	Version     string   // opaque source version identifier
	Size        int64    // total object size in bytes
	ChunkSize   int      // maximum bytes per chunk
	WholeSHA256 []byte   // digest of the complete object
	ChunkSHA256 [][]byte // one digest per chunk
}

// ChunkCount returns the number of chunks the object is split into.
func (m *Manifest) ChunkCount() int {
	if m == nil || m.ChunkSize <= 0 || m.Size < 0 {
		return 0
	}
	return int((m.Size + int64(m.ChunkSize) - 1) / int64(m.ChunkSize))
}

// ChunkRange returns [off, off+length) for chunk i.
func (m *Manifest) ChunkRange(i int) (off, length int64) {
	off = int64(i) * int64(m.ChunkSize)
	length = int64(m.ChunkSize)
	if rest := m.Size - off; rest < length {
		length = rest
	}
	return off, length
}

// Validate checks the manifest for internal consistency.
func (m *Manifest) Validate() error {
	if m == nil {
		return errors.New("chunkfetch: nil manifest")
	}
	if m.Version == "" {
		return errors.New("chunkfetch: manifest has empty version")
	}
	if m.ChunkSize <= 0 {
		return errors.New("chunkfetch: manifest has non-positive chunk size")
	}
	if m.Size < 0 {
		return errors.New("chunkfetch: manifest has negative size")
	}
	if len(m.WholeSHA256) != sha256.Size {
		return errors.New("chunkfetch: manifest whole digest must be SHA-256")
	}
	if len(m.ChunkSHA256) != m.ChunkCount() {
		return errors.New("chunkfetch: manifest chunk digest count mismatch")
	}
	for i, c := range m.ChunkSHA256 {
		if len(c) != sha256.Size {
			return errors.New("chunkfetch: manifest chunk " + strconv.Itoa(i) + " digest must be SHA-256")
		}
	}
	return nil
}

// Source is an injectable random-access data source. Implementations serve one
// object version at a time; ReadAt must fail with *VersionMismatchError once
// the object behind the source changes.
type Source interface {
	Manifest(ctx context.Context) (*Manifest, error)
	// ReadAt returns one wire frame containing the object's bytes in
	// [off, off+length). The returned slice may itself be incomplete
	// (missing data) or malformed (bad data); the fetcher classifies it via
	// the frame protocol and retries.
	ReadAt(ctx context.Context, version string, off, length int64) ([]byte, error)
}

// BuildManifest hashes data and produces its manifest for the given version.
func BuildManifest(version string, data []byte, chunkSize int) *Manifest {
	whole := sha256.Sum256(data)
	count := int((int64(len(data)) + int64(chunkSize) - 1) / int64(chunkSize))
	chunks := make([][]byte, count)
	for i := 0; i < count; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		sum := sha256.Sum256(data[start:end])
		chunks[i] = sum[:]
	}
	return &Manifest{
		Version:     version,
		Size:        int64(len(data)),
		ChunkSize:   chunkSize,
		WholeSHA256: whole[:],
		ChunkSHA256: chunks,
	}
}
