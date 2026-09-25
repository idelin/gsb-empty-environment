package kvimport

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// checkpoint is the durable commit record. Offset/Digest describe the
// confirmed input prefix; DataSize/RejectsSize describe how much of the
// staging files belongs to confirmed batches. Checksum detects torn or
// corrupted checkpoint files.
type checkpoint struct {
	Schema      int    `json:"schema"`
	Offset      int64  `json:"offset"`
	Digest      string `json:"digest"`
	DataSize    int64  `json:"dataSize"`
	RejectsSize int64  `json:"rejectsSize"`
	Checksum    string `json:"sum"`
}

// checksum hashes the canonical encoding of the checkpoint with an empty
// Checksum field.
func (c *checkpoint) checksum() string {
	tmp := *c
	tmp.Checksum = ""
	b, err := json.Marshal(&tmp)
	if err != nil {
		panic(err) // all fields are marshalable
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func parseCheckpoint(b []byte) (*checkpoint, error) {
	var cp checkpoint
	if err := json.Unmarshal(b, &cp); err != nil {
		return nil, fmt.Errorf("kvimport: checkpoint unreadable: %w", err)
	}
	if cp.Schema != 1 {
		return nil, fmt.Errorf("kvimport: unsupported checkpoint schema %d", cp.Schema)
	}
	if got := cp.checksum(); got != cp.Checksum {
		return nil, fmt.Errorf("kvimport: checkpoint corrupt: checksum mismatch (want %s, got %s)", cp.Checksum, got)
	}
	return &cp, nil
}
