// Package lfs implements the storage layer of a log-structured filesystem
// (LFS/F2FS style): append-only segmented logging, benefit-age segment
// cleaning with hot/cold separation, snapshot-pinned historical versions,
// dual-slot CRC-protected checkpoints and fsync-chain roll-forward recovery.
package lfs

import (
	"errors"
	"time"
)

// NodeID identifies a file/inode. ID 0 is reserved and rejected.
type NodeID uint64

// BlockIndex is the per-inode logical block index.
type BlockIndex uint32

// NATKey addresses one logical block: the (inode, logical block index) pair.
type NATKey struct {
	Ino    NodeID
	BlkIdx BlockIndex
}

// PhysAddr is a physical block location: segment id and slot inside it.
type PhysAddr struct {
	Seg  int
	Slot int
}

// Summary is the Segment Summary reverse pointer stored for every logged
// physical block: who owns it and which version of the logical block it is.
type Summary struct {
	Ino    NodeID
	BlkIdx BlockIndex
	Ver    uint64
	Hot    bool
}

// NodeKind discriminates logged node blocks.
type NodeKind uint8

const (
	// KindData is a regular data/node block carrying a logical block.
	KindData NodeKind = 1
	// KindFsync is an fsync commit marker terminating a durable node chain.
	KindFsync NodeKind = 2
)

// Node is the recoverable log record written into one physical block.
type Node struct {
	Kind   NodeKind
	Seq    uint64
	Ver    uint64
	Ino    NodeID
	BlkIdx BlockIndex
	Fsync  bool
	Hot    bool
	TimeNs int64
	Data   []byte
}

// NATEntry is one current node address table mapping.
type NATEntry struct {
	Addr PhysAddr
	Ver  uint64
	Hot  bool
}

// Clock returns the current time in nanoseconds. Injectable for deterministic
// segment-age testing.
type Clock func() int64

// WallClock is the default Clock, backed by the real wall clock.
func WallClock() int64 { return time.Now().UnixNano() }

// FakeClock is a manually controlled Clock for tests.
type FakeClock struct {
	NowNs int64
}

// Get satisfies Clock.
func (c *FakeClock) Get() int64 { return c.NowNs }

// Advance moves the fake clock forward by d.
func (c *FakeClock) Advance(d time.Duration) { c.NowNs += int64(d) }

// Sentinel errors returned by the package.
var (
	// ErrNoSpace means the device is full and cleaning cannot free anything.
	ErrNoSpace = errors.New("lfs: no free space and no reclaimable segment")
	// ErrNoCandidate means no non-active segment is cleanable right now.
	ErrNoCandidate = errors.New("lfs: no cleanable candidate segment")
	// ErrNoCheckpoint means neither CP slot holds a usable checkpoint.
	ErrNoCheckpoint = errors.New("lfs: no valid checkpoint found")
	// ErrCorruptCheckpoint means a CP slot failed head/tail CRC validation.
	ErrCorruptCheckpoint = errors.New("lfs: corrupt checkpoint slot")
	// ErrInvalidNode means a node block failed checksum/frame validation.
	ErrInvalidNode = errors.New("lfs: invalid node block")
	// ErrSnapshotExists is returned when creating a duplicate snapshot name.
	ErrSnapshotExists = errors.New("lfs: snapshot already exists")
	// ErrSnapshotNotFound is returned for an unknown snapshot name.
	ErrSnapshotNotFound = errors.New("lfs: snapshot not found")
	// ErrInvalidArgument is returned for caller-side validation failures.
	ErrInvalidArgument = errors.New("lfs: invalid argument")
)

// Recovery stop reasons reported by roll-forward.
const (
	// StopComplete: the chain ended exactly on an fsync commit marker.
	StopComplete = "complete"
	// StopNonFsyncTail: the chain ends with nodes never committed by fsync.
	StopNonFsyncTail = "non-fsync-tail"
	// StopChecksum: the next expected node block has a bad checksum/torn frame.
	StopChecksum = "checksum-error"
	// StopSequenceGap: the next expected node sequence number is missing.
	StopSequenceGap = "sequence-gap"
)
