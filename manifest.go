package csvq

import (
	"crypto/sha256"
	"fmt"
	"reflect"
)

// QuarantineEntry binds a quarantined row to its source span digest.
type QuarantineEntry struct {
	Batch     int
	Seq       int
	Offset    int64
	EndOffset int64
	Digest    [32]byte // sha256 of the raw source span
	Reason    string
}

// Manifest is a recovery manifest. It binds the source digest and the
// full schema version history to every committed batch and quarantined
// row, so a full re-import of the same source must reproduce it exactly.
type Manifest struct {
	SourceDigest  [32]byte // sha256 of the whole source
	SourceLen     int64
	Columns       []string // positional header names
	SchemaHistory []Schema
	Batches       []BatchMeta
	Quarantine    []QuarantineEntry
	Records       int
}

// BuildManifest builds the recovery manifest for a store over src.
func BuildManifest(src []byte, s *Store) Manifest {
	m := Manifest{
		SourceDigest:  sha256.Sum256(src),
		SourceLen:     int64(len(src)),
		Columns:       s.Columns(),
		SchemaHistory: s.Schemas(),
		Batches:       s.Batches(),
		Records:       s.Rows(),
	}
	for _, q := range s.Quarantine() {
		m.Quarantine = append(m.Quarantine, QuarantineEntry{
			Batch:     q.Batch,
			Seq:       q.Seq,
			Offset:    q.Offset,
			EndOffset: q.EndOffset,
			Digest:    sha256.Sum256(q.Raw),
			Reason:    q.Reason,
		})
	}
	return m
}

// VerifyRecovery verifies a manifest by full import: it checks the
// source digest, re-parses the whole source, replays the manifest's
// batches over the parsed records, and requires the rebuilt manifest to
// equal the given one.
func VerifyRecovery(src []byte, m Manifest) error {
	if int64(len(src)) != m.SourceLen {
		return fmt.Errorf("csvq: source length %d does not match manifest %d", len(src), m.SourceLen)
	}
	if sha256.Sum256(src) != m.SourceDigest {
		return fmt.Errorf("csvq: source digest does not match manifest")
	}
	t, err := Parse(src)
	if err != nil {
		return fmt.Errorf("csvq: recovery parse failed: %v", err)
	}
	s := NewStore()
	for _, bm := range m.Batches {
		var recs []Record
		var qs []QuarantinedRow
		for _, it := range t.Items {
			if it.Seq < bm.SeqLo || it.Seq >= bm.SeqHi {
				continue
			}
			if it.Record != nil {
				recs = append(recs, *it.Record)
			} else {
				qs = append(qs, *it.Quarantine)
			}
		}
		if _, err := s.ImportBatch(t.Header, recs, qs); err != nil {
			return fmt.Errorf("csvq: recovery batch %d failed: %v", bm.ID, err)
		}
	}
	got := BuildManifest(src, s)
	return compareManifest(m, got)
}

func compareManifest(want, got Manifest) error {
	if want.SourceDigest != got.SourceDigest || want.SourceLen != got.SourceLen {
		return fmt.Errorf("csvq: recovery source binding mismatch")
	}
	if !reflect.DeepEqual(want.Columns, got.Columns) {
		return fmt.Errorf("csvq: recovery columns mismatch: want %v, got %v", want.Columns, got.Columns)
	}
	if !reflect.DeepEqual(want.SchemaHistory, got.SchemaHistory) {
		return fmt.Errorf("csvq: recovery schema history mismatch: want %+v, got %+v", want.SchemaHistory, got.SchemaHistory)
	}
	if !reflect.DeepEqual(want.Batches, got.Batches) {
		return fmt.Errorf("csvq: recovery batches mismatch: want %+v, got %+v", want.Batches, got.Batches)
	}
	if !reflect.DeepEqual(want.Quarantine, got.Quarantine) {
		return fmt.Errorf("csvq: recovery quarantine mismatch: want %+v, got %+v", want.Quarantine, got.Quarantine)
	}
	if want.Records != got.Records {
		return fmt.Errorf("csvq: recovery record count mismatch: want %d, got %d", want.Records, got.Records)
	}
	return nil
}
