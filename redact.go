package netsec

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Record is one entry of the original audit log.
type Record struct {
	// Seq is the original log sequence number.  NewLog requires positive,
	// strictly increasing sequence numbers; duplicates are rejected.
	Seq     uint64 `json:"seq"`
	Payload []byte `json:"payload"`
}

type logEntry struct {
	sequence uint64
	payload  []byte
	digest   []byte
	leaf     []byte
}

// Log is the original, unredacted audit log.  Its Root commits to the log ID,
// the record count and the ordered record commitments.
type Log struct {
	ID    string
	Count uint64
	Root  []byte

	entries []logEntry
	tree    *merkleTree
}

// NewLog builds a log from records whose sequence numbers are positive and
// strictly increasing.  The root binds both the log ID and the total count.
func NewLog(logID string, records []Record) (*Log, error) {
	if logID == "" {
		return nil, errors.New("log ID must not be empty")
	}

	entries := make([]logEntry, len(records))
	leaves := make([][]byte, len(records))
	for i, record := range records {
		if record.Seq == 0 {
			return nil, fmt.Errorf("record %d has sequence number 0", i)
		}
		if i > 0 && record.Seq <= records[i-1].Seq {
			return nil, fmt.Errorf(
				"record %d sequence %d is not greater than predecessor %d",
				i,
				record.Seq,
				records[i-1].Seq,
			)
		}

		digest := payloadDigest(record.Payload)
		leaf := leafCommitment(logID, uint64(i), record.Seq, digest)
		entries[i] = logEntry{
			sequence: record.Seq,
			payload:  cloneBytes(record.Payload),
			digest:   digest,
			leaf:     leaf,
		}
		leaves[i] = leaf
	}

	tree := buildMerkleTree(leaves)
	count := uint64(len(entries))
	root := logRoot(logID, count, tree.root())

	return &Log{
		ID:      logID,
		Count:   count,
		Root:    root,
		entries: entries,
		tree:    tree,
	}, nil
}

// NewLogFromPayloads assigns sequence numbers 1..N and builds a log.
func NewLogFromPayloads(logID string, payloads [][]byte) (*Log, error) {
	records := make([]Record, len(payloads))
	for i, payload := range payloads {
		records[i] = Record{Seq: uint64(i + 1), Payload: payload}
	}
	return NewLog(logID, records)
}

// Anchor is the published trust anchor a verifier must obtain out of band.
type Anchor struct {
	LogID string
	Root  []byte
}

// Anchor returns this log's published trust anchor.
func (log *Log) Anchor() Anchor {
	if log == nil {
		return Anchor{}
	}
	return Anchor{LogID: log.ID, Root: cloneBytes(log.Root)}
}

// Commitments returns copies of the ordered leaf commitments.  These are safe
// to publish and are the values shown for redacted entries.
func (log *Log) Commitments() [][]byte {
	if log == nil {
		return nil
	}
	commitments := make([][]byte, len(log.entries))
	for i := range log.entries {
		commitments[i] = cloneBytes(log.entries[i].leaf)
	}
	return commitments
}

// PathStep is one sibling on a Merkle membership path.  The side is derived
// from the entry position when the path is verified.
type PathStep struct {
	Sibling []byte `json:"sibling"`
}

// ExportEntry is one position of a redacted export.  A redacted entry contains
// only structural metadata and its leaf commitment.  A public entry also
// contains the original sequence number, payload, payload digest and Merkle
// membership path.
type ExportEntry struct {
	Index      int        `json:"index"`
	Redacted   bool       `json:"redacted"`
	Commitment []byte     `json:"commitment"`
	Seq        uint64     `json:"seq,omitempty"`
	Payload    []byte     `json:"payload,omitempty"`
	Digest     []byte     `json:"digest,omitempty"`
	Path       []PathStep `json:"path,omitempty"`
}

// Export is a redacted audit log.  Count and Root are the values bound by the
// proof; Verify still requires an independent trust anchor.
type Export struct {
	LogID   string        `json:"log_id"`
	Count   uint64        `json:"count"`
	Root    []byte        `json:"root"`
	Entries []ExportEntry `json:"entries"`
}

// Export reveals exactly the positions listed in reveal.  Every other record
// is represented only by its commitment.  Duplicate or out-of-range positions
// are rejected.
func (log *Log) Export(reveal []int) (*Export, error) {
	if log == nil {
		return nil, errors.New("nil log")
	}

	revealed := make(map[int]bool, len(reveal))
	for _, index := range reveal {
		if index < 0 || index >= len(log.entries) {
			return nil, fmt.Errorf(
				"reveal position %d outside log of %d records",
				index,
				len(log.entries),
			)
		}
		if revealed[index] {
			return nil, fmt.Errorf("reveal position %d listed more than once", index)
		}
		revealed[index] = true
	}

	export := &Export{
		LogID:   log.ID,
		Count:   log.Count,
		Root:    cloneBytes(log.Root),
		Entries: make([]ExportEntry, len(log.entries)),
	}
	for i, entry := range log.entries {
		exported := ExportEntry{
			Index:      i,
			Redacted:   !revealed[i],
			Commitment: cloneBytes(entry.leaf),
		}
		if revealed[i] {
			exported.Seq = entry.sequence
			exported.Payload = cloneBytes(entry.payload)
			exported.Digest = cloneBytes(entry.digest)
			exported.Path = clonePath(log.tree.membershipPath(i))
		}
		export.Entries[i] = exported
	}
	return export, nil
}

// ExportAll reveals every record.
func (log *Log) ExportAll() (*Export, error) {
	reveal := make([]int, len(log.entries))
	for i := range reveal {
		reveal[i] = i
	}
	return log.Export(reveal)
}

// ExportNone redacts every record.
func (log *Log) ExportNone() (*Export, error) {
	return log.Export(nil)
}

func clonePath(path []PathStep) []PathStep {
	if path == nil {
		return nil
	}
	result := make([]PathStep, len(path))
	for i, step := range path {
		result[i] = PathStep{Sibling: cloneBytes(step.Sibling)}
	}
	return result
}

// VerifiedRecord is the result of verification at one position.  A redacted
// record has no payload; its commitment is still present.
type VerifiedRecord struct {
	Index      int
	Redacted   bool
	Seq        uint64
	Payload    []byte
	Digest     []byte
	Commitment []byte
}

// VerifiedLog is an export that has been checked against a trust anchor.
type VerifiedLog struct {
	LogID   string
	Count   uint64
	Root    []byte
	Records []VerifiedRecord
}

// Verify checks export against an independently obtained anchor.  It rejects:
//
//   - an export with a different log ID or root;
//   - missing, extra or out-of-order positions;
//   - redacted entries that leak payload, sequence, digest or path data;
//   - public entries whose payload, digest, commitment, sequence or Merkle
//     path is inconsistent;
//   - duplicate sequence numbers among public entries;
//   - any change to the final root, including a changed record count.
func Verify(anchor Anchor, export *Export) (*VerifiedLog, error) {
	if export == nil {
		return nil, errors.New("nil export")
	}
	if anchor.LogID == "" {
		return nil, errors.New("anchor log ID must not be empty")
	}
	if len(anchor.Root) != sha256.Size {
		return nil, fmt.Errorf("anchor root has length %d, want %d", len(anchor.Root), sha256.Size)
	}
	if export.LogID != anchor.LogID {
		return nil, fmt.Errorf("export log ID %q does not match anchor %q", export.LogID, anchor.LogID)
	}
	if len(export.Root) != sha256.Size {
		return nil, fmt.Errorf("export root has length %d, want %d", len(export.Root), sha256.Size)
	}
	if !bytes.Equal(export.Root, anchor.Root) {
		return nil, errors.New("export root does not match trust anchor")
	}
	if uint64(len(export.Entries)) != export.Count {
		return nil, fmt.Errorf(
			"export has %d entries, root commits to %d records",
			len(export.Entries),
			export.Count,
		)
	}

	// Count is a uint64 in the wire format.  Slices are indexed with int, so
	// reject values that cannot be represented on this platform.
	maxInt := uint64(^uint(0) >> 1)
	if export.Count > maxInt {
		return nil, fmt.Errorf("record count %d is too large", export.Count)
	}

	leaves := make([][]byte, len(export.Entries))
	pathRoots := make(map[int][]byte)
	seenSequences := make(map[uint64]int, len(export.Entries))
	verified := make([]VerifiedRecord, len(export.Entries))

	for i := range export.Entries {
		entry := &export.Entries[i]
		if entry.Index != i {
			return nil, fmt.Errorf("entry at slot %d declares position %d", i, entry.Index)
		}
		if len(entry.Commitment) != sha256.Size {
			return nil, fmt.Errorf(
				"entry %d commitment has length %d, want %d",
				i,
				len(entry.Commitment),
				sha256.Size,
			)
		}

		verified[i] = VerifiedRecord{
			Index:      i,
			Redacted:   entry.Redacted,
			Commitment: cloneBytes(entry.Commitment),
		}

		if entry.Redacted {
			if entry.Seq != 0 || len(entry.Payload) != 0 || len(entry.Digest) != 0 || len(entry.Path) != 0 {
				return nil, fmt.Errorf("redacted entry %d contains public data", i)
			}
			leaves[i] = cloneBytes(entry.Commitment)
			continue
		}

		if entry.Seq == 0 {
			return nil, fmt.Errorf("public entry %d has sequence number 0", i)
		}
		if earlier, exists := seenSequences[entry.Seq]; exists {
			return nil, fmt.Errorf(
				"duplicate sequence number %d at positions %d and %d",
				entry.Seq,
				earlier,
				i,
			)
		}
		seenSequences[entry.Seq] = i

		if len(entry.Digest) != sha256.Size {
			return nil, fmt.Errorf(
				"public entry %d digest has length %d, want %d",
				i,
				len(entry.Digest),
				sha256.Size,
			)
		}
		digest := payloadDigest(entry.Payload)
		if !bytes.Equal(digest, entry.Digest) {
			return nil, fmt.Errorf("public entry %d payload does not match its digest", i)
		}

		expectedLeaf := leafCommitment(export.LogID, uint64(i), entry.Seq, entry.Digest)
		if !bytes.Equal(expectedLeaf, entry.Commitment) {
			return nil, fmt.Errorf("public entry %d does not match its commitment", i)
		}

		pathRoot, err := rootFromPath(expectedLeaf, i, export.Count, entry.Path)
		if err != nil {
			return nil, fmt.Errorf("public entry %d: %w", i, err)
		}
		pathRoots[i] = pathRoot
		leaves[i] = cloneBytes(entry.Commitment)

		verified[i].Seq = entry.Seq
		verified[i].Payload = cloneBytes(entry.Payload)
		verified[i].Digest = cloneBytes(entry.Digest)
	}

	tree := buildMerkleTree(leaves)
	merkleRoot := tree.root()
	for index, pathRoot := range pathRoots {
		if !bytes.Equal(pathRoot, merkleRoot) {
			return nil, fmt.Errorf("public entry %d membership path does not match log", index)
		}
	}

	recomputed := logRoot(export.LogID, export.Count, merkleRoot)
	if !bytes.Equal(recomputed, anchor.Root) {
		return nil, errors.New("recomputed root does not match trust anchor")
	}
	if !bytes.Equal(recomputed, export.Root) {
		return nil, errors.New("recomputed root does not match export root")
	}

	return &VerifiedLog{
		LogID:   export.LogID,
		Count:   export.Count,
		Root:    cloneBytes(export.Root),
		Records: verified,
	}, nil
}

// MarshalExport serializes an export.  UnmarshalExport should be used by the
// host so that unknown fields and trailing JSON are rejected.
func MarshalExport(export *Export) ([]byte, error) {
	if export == nil {
		return nil, errors.New("nil export")
	}
	return json.Marshal(export)
}

// UnmarshalExport decodes an export and rejects unknown fields and trailing
// data.  The result is still untrusted until passed to Verify.
func UnmarshalExport(data []byte) (*Export, error) {
	var export Export
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&export); err != nil {
		return nil, fmt.Errorf("decode export: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, errors.New("export contains trailing JSON")
		}
		return nil, fmt.Errorf("decode trailing export data: %w", err)
	}
	return &export, nil
}

// VerifyJSON decodes and verifies an export against anchor.
func VerifyJSON(anchor Anchor, data []byte) (*VerifiedLog, error) {
	export, err := UnmarshalExport(data)
	if err != nil {
		return nil, err
	}
	return Verify(anchor, export)
}
