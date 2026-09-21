package framechain

import (
	"bytes"
	"errors"
	"fmt"
)

// Chain-related errors.
var (
	ErrChainBreak   = errors.New("framechain: broken link")
	ErrSeqGap       = errors.New("framechain: sequence gap")
	ErrDuplicate    = errors.New("framechain: conflicting duplicate frame")
	ErrCheckpoint   = errors.New("framechain: checkpoint mismatch")
	ErrTruncatedMid = errors.New("framechain: truncated frame in the middle of stream")
)

// Checkpoint pins a verified position in the chain: the digest of the frame
// with sequence number Seq. A suffix is accepted only if its first frame
// links exactly to this checkpoint.
type Checkpoint struct {
	Seq    uint64
	Digest [DigestSize]byte
}

// CheckpointOf derives a checkpoint from a parsed frame.
func CheckpointOf(f *Frame) Checkpoint {
	return Checkpoint{Seq: f.Seq, Digest: f.DigestOf()}
}

// Chain is an append-only verifier/collector for one frame sequence.
type Chain struct {
	frames  []*Frame
	last    Checkpoint
	started bool
	seen    map[uint64][DigestSize]byte // seq -> digest, for duplicate detection
}

// NewChain starts a chain from genesis (no frames yet).
func NewChain() *Chain {
	return &Chain{last: Checkpoint{Digest: GenesisPrev}, seen: make(map[uint64][DigestSize]byte)}
}

// NewChainFrom starts a chain that continues after cp.
func NewChainFrom(cp Checkpoint) *Chain {
	return &Chain{last: cp, started: true, seen: make(map[uint64][DigestSize]byte)}
}

// Add validates and appends f.
//
//   - prev-digest must equal the digest of the previous frame, so swapping
//     two frames breaks the chain even if both CRCs are valid;
//   - a byte-identical re-send of an already-seen frame is a duplicate and
//     is ignored (reported as dup=true, not appended);
//   - a frame reusing a seen seq with different content is ErrDuplicate;
//   - any other seq/prev mismatch is ErrChainBreak / ErrSeqGap.
func (c *Chain) Add(f *Frame) (dup bool, err error) {
	d := f.DigestOf()
	if prev, ok := c.seen[f.Seq]; ok {
		if prev == d {
			return true, nil // 重复帧：不当新帧
		}
		return false, fmt.Errorf("%w: seq %d", ErrDuplicate, f.Seq)
	}
	var wantSeq uint64
	if c.started {
		wantSeq = c.last.Seq + 1
	} else {
		wantSeq = GenesisSeq
	}
	if f.Seq != wantSeq {
		return false, fmt.Errorf("%w: got seq %d, want %d", ErrSeqGap, f.Seq, wantSeq)
	}
	if f.Prev != c.last.Digest {
		return false, fmt.Errorf("%w: seq %d prev digest mismatch", ErrChainBreak, f.Seq)
	}
	c.frames = append(c.frames, f)
	c.seen[f.Seq] = d
	c.last = Checkpoint{Seq: f.Seq, Digest: d}
	c.started = true
	return false, nil
}

// Frames returns the accepted frames.
func (c *Chain) Frames() []*Frame { return c.frames }

// Head returns the checkpoint of the latest accepted frame.
func (c *Chain) Head() Checkpoint { return c.last }

// FindBreak returns the index of the first frame that fails linkage
// (sequence gap or prev-digest mismatch), starting from genesis.
// It returns -1 when the whole slice chains cleanly. CRCs are assumed to
// have been validated at parse time.
func FindBreak(frames []*Frame) int {
	prev := GenesisPrev
	for i, f := range frames {
		wantSeq := uint64(i)
		if f.Seq != wantSeq || f.Prev != prev {
			return i
		}
		prev = f.DigestOf()
	}
	return -1
}

// VerifySuffix checks that frames is a valid continuation of cp: the first
// frame must have Seq == cp.Seq+1 and Prev == cp.Digest, and the rest must
// chain. A wrong checkpoint can never accept a suffix.
func VerifySuffix(cp Checkpoint, frames []*Frame) error {
	if len(frames) == 0 {
		return nil
	}
	prev := cp.Digest
	for i, f := range frames {
		if f.Seq != cp.Seq+1+uint64(i) {
			return fmt.Errorf("%w: suffix frame %d has seq %d, want %d", ErrSeqGap, i, f.Seq, cp.Seq+1+uint64(i))
		}
		if !bytes.Equal(f.Prev[:], prev[:]) {
			if i == 0 {
				return fmt.Errorf("%w: first suffix frame does not link to checkpoint seq %d", ErrCheckpoint, cp.Seq)
			}
			return fmt.Errorf("%w: suffix frame %d (seq %d)", ErrChainBreak, i, f.Seq)
		}
		prev = f.DigestOf()
	}
	return nil
}
