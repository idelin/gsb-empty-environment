package snapcatchup

import "hash/crc32"

// SnapshotMeta identifies one snapshot generation. Generation increases every
// time the leader takes a new snapshot, so a receiver can tell a fresh
// transfer apart from leftover chunks of an older, now useless snapshot.
type SnapshotMeta struct {
	Generation uint64 `json:"generation"`
	LastIndex  uint64 `json:"last_index"`
	LastTerm   uint64 `json:"last_term"`
	NumChunks  int    `json:"num_chunks"`
	Checksum   uint32 `json:"checksum"` // crc32 of the whole snapshot payload
}

// Chunk is one piece of a snapshot. Checksum covers only Data.
type Chunk struct {
	Meta     SnapshotMeta
	Seq      int
	Data     []byte
	Checksum uint32
}

func checksum(data []byte) uint32 { return crc32.ChecksumIEEE(data) }

// valid reports whether the chunk payload matches its attached checksum.
func (c Chunk) valid() bool { return checksum(c.Data) == c.Checksum }
