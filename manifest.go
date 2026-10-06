package csvq

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// QuarantineEntry binds a quarantined row to its source range.
type QuarantineEntry struct {
	Index  int
	Offset int64
	Length int64
	Digest string // sha256 of the raw quarantined bytes
	Reason string
}

// ChunkDigest binds an enumerated chunk to the raw bytes it spans.
type ChunkDigest struct {
	Index       int
	FirstRecord int
	LastRecord  int
	Digest      string // sha256 of the concatenated raw record bytes
}

// Manifest is a recovery manifest binding a source digest, a schema
// version, per-chunk digests, and the quarantine list.
type Manifest struct {
	SourceDigest    string
	SchemaVersion   int
	RecordCount     int
	QuarantineCount int
	Quarantine      []QuarantineEntry
	Chunks          []ChunkDigest
}

func digest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// chunkDigest hashes the raw source bytes of every record in the chunk.
func chunkDigest(src []byte, f *File, c Chunk) string {
	h := sha256.New()
	for i := c.FirstRecord; i < c.LastRecord; i++ {
		r := f.Records[i]
		h.Write(src[r.Offset : r.Offset+r.Length])
	}
	return hex.EncodeToString(h.Sum(nil))
}

// BuildManifest builds a recovery manifest for src from its parsed file
// and the store produced by importing it, chunking records in blocks of
// chunkSize.
func BuildManifest(src []byte, f *File, s *Store, chunkSize int) Manifest {
	m := Manifest{
		SourceDigest:    digest(src),
		SchemaVersion:   s.SchemaVersion(),
		RecordCount:     len(f.Records),
		QuarantineCount: len(f.Quarantine),
	}
	for _, q := range f.Quarantine {
		m.Quarantine = append(m.Quarantine, QuarantineEntry{
			Index:  q.Index,
			Offset: q.Offset,
			Length: q.Length,
			Digest: digest([]byte(q.Raw)),
			Reason: q.Reason,
		})
	}
	for _, c := range Chunks(f, chunkSize) {
		m.Chunks = append(m.Chunks, ChunkDigest{
			Index:       c.Index,
			FirstRecord: c.FirstRecord,
			LastRecord:  c.LastRecord,
			Digest:      chunkDigest(src, f, c),
		})
	}
	return m
}

// Verify checks the manifest against src by re-parsing it and running a
// full re-import: source digest, record and quarantine counts, every
// quarantined row's range and bytes, every chunk's raw-byte digest, and
// the final schema version must all match.
func (m Manifest) Verify(src []byte, chunkSize int) error {
	if got := digest(src); got != m.SourceDigest {
		return fmt.Errorf("source digest mismatch: got %s, want %s", got, m.SourceDigest)
	}
	f := Parse(src)
	if len(f.Records) != m.RecordCount {
		return fmt.Errorf("record count mismatch: got %d, want %d", len(f.Records), m.RecordCount)
	}
	if len(f.Quarantine) != m.QuarantineCount {
		return fmt.Errorf("quarantine count mismatch: got %d, want %d", len(f.Quarantine), m.QuarantineCount)
	}
	for i, q := range f.Quarantine {
		want := m.Quarantine[i]
		if q.Index != want.Index || q.Offset != want.Offset || q.Length != want.Length {
			return fmt.Errorf("quarantine %d range mismatch: got (%d,%d,%d), want (%d,%d,%d)",
				i, q.Index, q.Offset, q.Length, want.Index, want.Offset, want.Length)
		}
		if got := digest([]byte(q.Raw)); got != want.Digest {
			return fmt.Errorf("quarantine %d digest mismatch", i)
		}
	}
	chunks := Chunks(f, chunkSize)
	if len(chunks) != len(m.Chunks) {
		return fmt.Errorf("chunk count mismatch: got %d, want %d", len(chunks), len(m.Chunks))
	}
	for i, c := range chunks {
		want := m.Chunks[i]
		if c.FirstRecord != want.FirstRecord || c.LastRecord != want.LastRecord {
			return fmt.Errorf("chunk %d range mismatch", i)
		}
		if got := chunkDigest(src, f, c); got != want.Digest {
			return fmt.Errorf("chunk %d digest mismatch", i)
		}
	}
	// Full re-import: replay every chunk as an atomic batch and compare
	// the resulting schema version and row count.
	store := NewStore(f.Width())
	imp := NewImporter(store, f.Width())
	for _, c := range chunks {
		if err := imp.ImportBatch(f.Records[c.FirstRecord:c.LastRecord]); err != nil {
			return fmt.Errorf("re-import chunk %d: %v", c.Index, err)
		}
	}
	if got := store.SchemaVersion(); got != m.SchemaVersion {
		return fmt.Errorf("schema version mismatch: got %d, want %d", got, m.SchemaVersion)
	}
	if got := store.RowCount(); got != m.RecordCount {
		return fmt.Errorf("re-imported row count mismatch: got %d, want %d", got, m.RecordCount)
	}
	return nil
}
