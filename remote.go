package paircmp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"sort"
)

// Segment is a piece of a remote entity's content, tagged with the
// entity version it was read from. Segments answering one Fetch may
// arrive out of order and may not cover the whole requested range.
type Segment struct {
	Off     int64
	Data    []byte
	Version string
}

// Remote models a range-readable remote entity with versioning.
type Remote interface {
	// Fetch returns segments covering a prefix of [off, off+want),
	// possibly out of order and possibly short. An empty result means
	// the entity ends at or before off (EOF).
	Fetch(ctx context.Context, off, want int64) ([]Segment, error)
	// Head reports the current version and content digest of
	// [off, off+want) without transferring the data.
	Head(ctx context.Context, off, want int64) (version string, digest [32]byte, err error)
}

// ErrVersionChanged means the entity version changed while a single
// chunk was being assembled from segments.
var ErrVersionChanged = errors.New("paircmp: remote version changed mid-read")

// ErrTooManyInvalidations means CompareRemote gave up retrying.
var ErrTooManyInvalidations = errors.New("paircmp: too many version invalidations")

// ChunkRecord pins one compared chunk to a version and digest.
type ChunkRecord struct {
	Off, Len int64
	Version  string
	Digest   [32]byte
}

// Checkpoint captures confirmed progress so a later pass can resume.
// Only intervals backed by chunk records whose version AND digest
// still match both remotes are reused.
type Checkpoint struct {
	Offset int64
	Left   []ChunkRecord
	Right  []ChunkRecord
	Diffs  []Interval
}

// RemoteResult is the outcome of one RemoteComparator.Compare pass.
type RemoteResult struct {
	Diffs    []Interval
	Missing  *TailMissing
	Complete bool
	// Invalidated means a source changed version mid-comparison; the
	// conclusions in this result were discarded and Checkpoint holds
	// only the still-confirmable progress.
	Invalidated bool
	Err         error
	Checkpoint  Checkpoint
}

// RemoteComparator compares two Remote entities chunk by chunk.
type RemoteComparator struct {
	Left, Right Remote
	Chunk       int64
}

// Compare runs one pass. If resume is non-nil, its chunk records are
// revalidated with Head: only the longest prefix where BOTH sources
// still report the recorded version and digest is reused; comparison
// continues from there. If either source's version changes during the
// pass, the pass is invalidated and its conclusions discarded.
func (c *RemoteComparator) Compare(ctx context.Context, resume *Checkpoint) RemoteResult {
	var res RemoteResult
	chunk := c.Chunk
	if chunk <= 0 {
		chunk = 1 << 20
	}
	var (
		diffs           []Interval
		off             int64
		lv, rv          string
		haveLV, haveRV  bool
		runStart        = int64(-1)
		runEnd          int64
		ckOffset        int64
		ckLeft, ckRight []ChunkRecord
	)
	closeRun := func() {
		if runStart >= 0 {
			diffs = append(diffs, Interval{Start: runStart, End: runEnd})
			runStart = -1
		}
	}
	snapshot := func() Checkpoint {
		d := clipIntervals(diffs, ckOffset)
		if runStart >= 0 && runStart < ckOffset {
			d = append(d, Interval{Start: runStart, End: min(runEnd, ckOffset)})
		}
		return Checkpoint{Offset: ckOffset, Left: ckLeft, Right: ckRight, Diffs: d}
	}
	fail := func(err error) RemoteResult {
		closeRun()
		res.Err = err
		res.Diffs = diffs
		res.Checkpoint = snapshot()
		return res
	}
	invalidate := func() RemoteResult {
		res.Invalidated = true
		res.Checkpoint = snapshot()
		return res
	}

	if resume != nil && resume.Offset > 0 {
		k := min(validPrefix(ctx, c.Left, resume.Left), validPrefix(ctx, c.Right, resume.Right))
		for i := 0; i < k; i++ {
			ckLeft = append(ckLeft, resume.Left[i])
			ckRight = append(ckRight, resume.Right[i])
			ckOffset += resume.Left[i].Len
		}
		off = ckOffset
		diffs = clipIntervals(resume.Diffs, off)
		if k > 0 {
			lv, haveLV = ckLeft[k-1].Version, true
			rv, haveRV = ckRight[k-1].Version, true
			// Reopen a difference run that straddles the resume point.
			if len(diffs) > 0 && diffs[len(diffs)-1].End == off {
				runStart = diffs[len(diffs)-1].Start
				runEnd = off
				diffs = diffs[:len(diffs)-1]
			}
		}
	}

	for {
		ld, lver, err := fetchChunk(ctx, c.Left, off, chunk)
		if err != nil {
			if errors.Is(err, ErrVersionChanged) {
				return invalidate()
			}
			return fail(err)
		}
		rd, rver, err := fetchChunk(ctx, c.Right, off, chunk)
		if err != nil {
			if errors.Is(err, ErrVersionChanged) {
				return invalidate()
			}
			return fail(err)
		}
		if len(ld) == 0 && len(rd) == 0 {
			closeRun()
			res.Complete = true
			res.Diffs = diffs
			res.Checkpoint = snapshot()
			return res
		}
		if len(ld) > 0 {
			if !haveLV {
				lv, haveLV = lver, true
			} else if lver != lv {
				return invalidate()
			}
		}
		if len(rd) > 0 {
			if !haveRV {
				rv, haveRV = rver, true
			} else if rver != rv {
				return invalidate()
			}
		}
		n := min(len(ld), len(rd))
		for i := 0; i < n; i++ {
			if ld[i] != rd[i] {
				if runStart < 0 {
					runStart = off + int64(i)
				}
				runEnd = off + int64(i) + 1
			} else {
				closeRun()
			}
		}
		if n == int(chunk) && off == ckOffset {
			ckLeft = append(ckLeft, ChunkRecord{Off: off, Len: chunk, Version: lv, Digest: sha256.Sum256(ld)})
			ckRight = append(ckRight, ChunkRecord{Off: off, Len: chunk, Version: rv, Digest: sha256.Sum256(rd)})
			ckOffset += chunk
		}
		off += int64(n)
		if len(ld) == len(rd) {
			continue
		}
		// The shorter read hit EOF; the other side has a tail.
		if len(ld) < len(rd) {
			tail, err := c.drain(ctx, c.Right, off+int64(len(rd)-n), chunk, rv)
			tail += int64(len(rd) - n)
			if err != nil {
				if errors.Is(err, ErrVersionChanged) {
					return invalidate()
				}
				return fail(err)
			}
			closeRun()
			res.Missing = &TailMissing{Side: 0, Start: off, End: off + tail}
		} else {
			tail, err := c.drain(ctx, c.Left, off+int64(len(ld)-n), chunk, lv)
			tail += int64(len(ld) - n)
			if err != nil {
				if errors.Is(err, ErrVersionChanged) {
					return invalidate()
				}
				return fail(err)
			}
			closeRun()
			res.Missing = &TailMissing{Side: 1, Start: off, End: off + tail}
		}
		res.Complete = true
		res.Diffs = diffs
		res.Checkpoint = snapshot()
		return res
	}
}

// drain reads r from off until EOF, returning the number of bytes
// seen. A version change against wantVer aborts with ErrVersionChanged.
func (c *RemoteComparator) drain(ctx context.Context, r Remote, off, chunk int64, wantVer string) (int64, error) {
	var total int64
	for {
		d, v, err := fetchChunk(ctx, r, off+total, chunk)
		if err != nil {
			return 0, err
		}
		if len(d) == 0 {
			return total, nil
		}
		if v != wantVer {
			return 0, ErrVersionChanged
		}
		total += int64(len(d))
	}
}

// CompareRemote drives Compare until it completes, retrying after
// version invalidations and resuming from the last checkpoint.
func CompareRemote(ctx context.Context, left, right Remote, chunk int64) (RemoteResult, error) {
	cmp := &RemoteComparator{Left: left, Right: right, Chunk: chunk}
	var ck *Checkpoint
	for i := 0; i < 100; i++ {
		res := cmp.Compare(ctx, ck)
		if !res.Invalidated {
			return res, nil
		}
		ck = &res.Checkpoint
	}
	return RemoteResult{}, ErrTooManyInvalidations
}

// fetchChunk assembles exactly want bytes starting at off, unless the
// remote signals EOF first (empty Fetch result). Segments may arrive
// out of order and cover the range only partially; all segments of
// one chunk must carry the same entity version.
func fetchChunk(ctx context.Context, r Remote, off, want int64) ([]byte, string, error) {
	var buf []byte
	var version string
	verSet := false
	for int64(len(buf)) < want {
		segs, err := r.Fetch(ctx, off+int64(len(buf)), want-int64(len(buf)))
		if err != nil {
			return nil, "", err
		}
		if len(segs) == 0 {
			return buf, version, nil
		}
		sort.Slice(segs, func(i, j int) bool { return segs[i].Off < segs[j].Off })
		pos := off + int64(len(buf))
		before := len(buf)
		for _, s := range segs {
			if s.Off != pos {
				return nil, "", fmt.Errorf("paircmp: segment gap at %d, want %d", s.Off, pos)
			}
			if !verSet {
				version, verSet = s.Version, true
			} else if s.Version != version {
				return nil, "", ErrVersionChanged
			}
			buf = append(buf, s.Data...)
			pos += int64(len(s.Data))
			if int64(len(buf)) >= want {
				break
			}
		}
		if len(buf) == before {
			return nil, "", ErrNoProgress
		}
	}
	return buf, version, nil
}

// validPrefix returns how many leading records still match the
// remote's current version and digest.
func validPrefix(ctx context.Context, r Remote, recs []ChunkRecord) int {
	for i, rec := range recs {
		v, d, err := r.Head(ctx, rec.Off, rec.Len)
		if err != nil || v != rec.Version || d != rec.Digest {
			return i
		}
	}
	return len(recs)
}
