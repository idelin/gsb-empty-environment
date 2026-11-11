package fslog

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"strings"
)

// IssueKind classifies a verification finding.
type IssueKind string

const (
	// IssueTampered means a frame's MAC does not match its contents: the
	// record was modified in place.
	IssueTampered IssueKind = "tampered"
	// IssueChainBreak means a frame is not the expected successor in the
	// chain: records before it were deleted or it was reordered/inserted.
	IssueChainBreak IssueKind = "chain-break"
	// IssueTruncated means the log ends cleanly at a frame boundary but
	// the final epoch has no epoch-end marker. Per the write protocol
	// (frames are atomic, Close always emits an end marker) this indicates
	// malicious truncation, not a crash.
	IssueTruncated IssueKind = "truncated"
	// IssueTornTail means the log ends with a partial frame: a crash
	// interrupted a single in-flight write. This is benign; the complete
	// prefix is still verifiable.
	IssueTornTail IssueKind = "torn-tail"
)

// Issue pinpoints one integrity problem in the log.
type Issue struct {
	Kind   IssueKind
	Offset int64  // byte offset of the offending frame (or of the torn tail)
	Epoch  uint64 // epoch the problem was detected in
	Seq    int64  // sequence number involved, -1 if not applicable
	Detail string
}

func (i Issue) String() string {
	loc := fmt.Sprintf("offset=%d epoch=%d", i.Offset, i.Epoch)
	if i.Seq >= 0 {
		loc += fmt.Sprintf(" seq=%d", i.Seq)
	}
	return fmt.Sprintf("%s [%s]: %s", i.Kind, loc, i.Detail)
}

// Report summarises a Verify run.
type Report struct {
	// Records is the number of well-formed, cryptographically valid audit
	// records verified before the first problem (or in total when OK).
	Records int
	// Epochs is the number of fully closed and verified epochs.
	Epochs int
	// Issues lists every problem found, in log order.
	Issues []Issue
}

// OK reports whether the log verified cleanly end to end.
func (r *Report) OK() bool { return len(r.Issues) == 0 }

func (r *Report) String() string {
	if r.OK() {
		return fmt.Sprintf("OK: %d records across %d closed epochs verified", r.Records, r.Epochs)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "FAILED: %d issue(s); %d records verified before/around problems\n", len(r.Issues), r.Records)
	for _, i := range r.Issues {
		b.WriteString("  ")
		b.WriteString(i.String())
		b.WriteByte('\n')
	}
	return b.String()
}

// Verify recomputes the key schedule from initialKey and checks every frame
// of the log read from r. It never trusts in-log counters: keys, chain
// values, sequence numbers and record counts are all derived independently
// and compared against the frames.
//
// Verification stops at the first cryptographic break (the rest of the log
// cannot be trusted past an unknown chain state), but a torn tail or a
// truncation at the end is always reported.
func Verify(r io.Reader, initialKey []byte) (*Report, error) {
	if len(initialKey) != KeySize {
		return nil, fmt.Errorf("fslog: initial key must be %d bytes, got %d", KeySize, len(initialKey))
	}
	frames, tornOffset, tornBytes, err := readFrames(r)
	if err != nil {
		return nil, err
	}

	rep := &Report{}
	key := append([]byte(nil), initialKey...)
	defer erase(key)

	var (
		chain     []byte
		epoch     uint64
		expectSeq uint64
		count     uint64
		epochUsed bool // current epoch contains frames but no end marker yet
	)
	beginEpoch := func(prevEndMAC []byte) {
		mac := hmac.New(sha256.New, key)
		mac.Write(labelInit)
		var num [8]byte
		binary.BigEndian.PutUint64(num[:], epoch)
		mac.Write(num[:])
		mac.Write(prevEndMAC)
		chain = mac.Sum(nil)
		expectSeq = 0
		count = 0
		epochUsed = false
	}
	beginEpoch(nil)

loop:
	for _, f := range frames {
		if f.epoch != epoch {
			rep.Issues = append(rep.Issues, Issue{
				Kind: IssueChainBreak, Offset: f.offset, Epoch: epoch, Seq: int64(expectSeq),
				Detail: fmt.Sprintf("frame claims epoch %d, expected %d: an epoch boundary was deleted or frames were reordered", f.epoch, epoch),
			})
			break
		}
		switch f.typ {
		case recTypeRecord:
			mac := hmac.New(sha256.New, key)
			mac.Write(labelRecord)
			var num [8]byte
			binary.BigEndian.PutUint64(num[:], epoch)
			mac.Write(num[:])
			binary.BigEndian.PutUint64(num[:], f.seq)
			mac.Write(num[:])
			mac.Write(chain)
			mac.Write(f.data)
			expected := mac.Sum(nil)
			if !hmac.Equal(expected, f.tag) {
				kind := IssueTampered
				detail := "MAC mismatch: record contents were modified"
				if f.seq != expectSeq {
					kind = IssueChainBreak
					detail = fmt.Sprintf("MAC mismatch and sequence jump (got %d, expected %d): records were deleted or reordered", f.seq, expectSeq)
				}
				rep.Issues = append(rep.Issues, Issue{
					Kind: kind, Offset: f.offset, Epoch: epoch, Seq: int64(f.seq), Detail: detail,
				})
				break loop
			}
			if f.seq != expectSeq {
				// MAC valid but unexpected seq: genuine frames reordered or
				// replayed by someone who held the (now erased) epoch key.
				rep.Issues = append(rep.Issues, Issue{
					Kind: IssueChainBreak, Offset: f.offset, Epoch: epoch, Seq: int64(f.seq),
					Detail: fmt.Sprintf("valid MAC but sequence %d, expected %d: frames were reordered or replayed", f.seq, expectSeq),
				})
				break loop
			}
			chain = f.tag
			expectSeq++
			count++
			rep.Records++
			epochUsed = true
		case recTypeEpochEnd:
			if len(f.data) != 8 {
				rep.Issues = append(rep.Issues, Issue{
					Kind: IssueTampered, Offset: f.offset, Epoch: epoch, Seq: -1,
					Detail: "malformed epoch-end marker",
				})
				break loop
			}
			mac := hmac.New(sha256.New, key)
			mac.Write(labelEnd)
			var num [8]byte
			binary.BigEndian.PutUint64(num[:], epoch)
			mac.Write(num[:])
			mac.Write(f.data)
			mac.Write(chain)
			expected := mac.Sum(nil)
			if !hmac.Equal(expected, f.tag) {
				rep.Issues = append(rep.Issues, Issue{
					Kind: IssueChainBreak, Offset: f.offset, Epoch: epoch, Seq: -1,
					Detail: "epoch-end marker MAC mismatch: records at the end of this epoch were deleted or altered",
				})
				break loop
			}
			want := binary.BigEndian.Uint64(f.data)
			if want != count {
				rep.Issues = append(rep.Issues, Issue{
					Kind: IssueChainBreak, Offset: f.offset, Epoch: epoch, Seq: -1,
					Detail: fmt.Sprintf("epoch-end marker counts %d records but %d were verified: records were deleted", want, count),
				})
				break loop
			}
			// Evolve the key one-way, exactly as the writer did.
			next := sha256.Sum256(append(append([]byte(nil), labelEvolve...), key...))
			erase(key)
			key = next[:]
			epoch++
			rep.Epochs++
			beginEpoch(f.tag)
		default:
			rep.Issues = append(rep.Issues, Issue{
				Kind: IssueTampered, Offset: f.offset, Epoch: epoch, Seq: -1,
				Detail: fmt.Sprintf("unknown frame type 0x%02x", f.typ),
			})
			break loop
		}
	}

	// Tail analysis: distinguish a crash from an attack.
	if tornBytes > 0 {
		rep.Issues = append(rep.Issues, Issue{
			Kind: IssueTornTail, Offset: tornOffset, Epoch: epoch, Seq: -1,
			Detail: fmt.Sprintf("%d trailing byte(s) form an incomplete frame: crash mid-write; the %d verified records up to offset %d are intact", tornBytes, rep.Records, tornOffset),
		})
	} else if epochUsed && len(rep.Issues) == 0 {
		rep.Issues = append(rep.Issues, Issue{
			Kind: IssueTruncated, Offset: endOffset(frames), Epoch: epoch, Seq: int64(expectSeq),
			Detail: fmt.Sprintf("log ends at a clean frame boundary but epoch %d has no epoch-end marker: trailing records/marker were maliciously deleted", epoch),
		})
	}
	return rep, nil
}

func endOffset(frames []frame) int64 {
	if len(frames) == 0 {
		return 0
	}
	last := frames[len(frames)-1]
	return last.offset + last.rawLen
}

// equalFrames is a small helper used by tests.
func equalFrames(a, b *frame) bool {
	return a.epoch == b.epoch && a.seq == b.seq && a.typ == b.typ &&
		bytes.Equal(a.data, b.data) && bytes.Equal(a.tag, b.tag)
}
