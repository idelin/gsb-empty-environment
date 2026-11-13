package netsec

import (
	"crypto/sha256"
	"errors"
)

// Row is one 64-byte database row: 32-byte record followed by a 32-byte tag.
type Row [RowSize]byte

// Database is an immutable, published snapshot. It contains no HMAC key.
type Database struct {
	registry Registry
	rows     []Row
}

// Publish is the trusted-publisher step. It validates N records, computes
// tag_i = HMAC-SHA256(key, big-endian i || record_i), builds row_i =
// record_i || tag_i, and computes the Merkle root over those rows.
func Publish(records [][]byte, hmacKey []byte, snapshotID string) (*Database, error) {
	if len(hmacKey) == 0 {
		return nil, errors.New("pir: empty HMAC key")
	}
	if snapshotID == "" {
		return nil, errors.New("pir: empty snapshot ID")
	}
	if len(records) < 1 || len(records) > MaxRecords {
		return nil, errors.New("pir: database must contain between 1 and 4096 records")
	}

	rows := make([]Row, len(records))
	for i, record := range records {
		if len(record) != RecordSize {
			return nil, errors.New("pir: every record must be 32 bytes")
		}
		tag := computeTag(hmacKey, uint32(i), record)
		copy(rows[i][:RecordSize], record)
		copy(rows[i][RecordSize:], tag[:])
	}

	return &Database{
		registry: Registry{
			SnapshotID: snapshotID,
			MerkleRoot: ComputeMerkleRoot(rows),
			N:          len(rows),
		},
		rows: rows,
	}, nil
}

// NewDatabase is an alias for Publish.
func NewDatabase(records [][]byte, hmacKey []byte, snapshotID string) (*Database, error) {
	return Publish(records, hmacKey, snapshotID)
}

// Registry returns a copy of the public registration data.
func (db *Database) Registry() Registry {
	return db.registry
}

// SnapshotID returns the published snapshot identifier.
func (db *Database) SnapshotID() string {
	return db.registry.SnapshotID
}

// MerkleRoot returns the published root over record||tag rows.
func (db *Database) MerkleRoot() [32]byte {
	return db.registry.MerkleRoot
}

// N returns the number of records.
func (db *Database) N() int {
	return db.registry.N
}

// Row returns a copy of row i.
func (db *Database) Row(i int) (Row, bool) {
	if i < 0 || i >= len(db.rows) {
		return Row{}, false
	}
	return db.rows[i], true
}

// ComputeMerkleRoot hashes rows as leaves and combines pairs upward. Leaves
// are SHA-256(0x00 || row), internal nodes are SHA-256(0x01 || left || right),
// and an unpaired node is duplicated for the next level.
func ComputeMerkleRoot(rows []Row) [32]byte {
	if len(rows) == 0 {
		return [32]byte{}
	}

	level := make([][32]byte, len(rows))
	for i, row := range rows {
		h := sha256.New()
		_, _ = h.Write([]byte{0x00})
		_, _ = h.Write(row[:])
		copy(level[i][:], h.Sum(nil))
	}

	for len(level) > 1 {
		nextLen := (len(level) + 1) / 2
		next := make([][32]byte, nextLen)
		for i := 0; i < len(level); i += 2 {
			right := level[i]
			if i+1 < len(level) {
				right = level[i+1]
			}
			h := sha256.New()
			_, _ = h.Write([]byte{0x01})
			_, _ = h.Write(level[i][:])
			_, _ = h.Write(right[:])
			copy(next[i/2][:], h.Sum(nil))
		}
		level = next
	}
	return level[0]
}
