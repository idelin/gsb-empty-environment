package diffcmp

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"sort"
)

// ErrVersionChanged is returned by CompareRemote when either source's
// entity version changed during the comparison. Every conclusion reached
// during the aborted pass is discarded; the returned checkpoint is the
// state that was validated at entry and may be passed back to
// CompareRemote to start a fresh pass under the new versions.
var ErrVersionChanged = errors.New("diffcmp: entity version changed during comparison")

// Segment is one piece of a remote entity. Segments returned by a single
// FetchRange call may arrive out of order and may cover less than the
// requested range (short read). Version identifies the entity version
// the bytes were read from; it must be non-empty.
type Segment struct {
	Offset  int64
	Data    []byte
	Version string
}

// RemoteSource is a versioned remote byte source. FetchRange returns
// segments covering part or all of [offset, offset+length); an empty
// result with a nil error signals EOF at offset. Version reports the
// current entity version.
type RemoteSource interface {
	FetchRange(ctx context.Context, offset, length int64) ([]Segment, error)
	Version(ctx context.Context) (string, error)
}

// ConfirmedDiff is a difference interval confirmed under a specific pair
// of entity versions, together with the SHA-256 digests of each side's
// bytes inside the interval.
type ConfirmedDiff struct {
	Interval
	VersionA string
	VersionB string
	DigestA  [sha256.Size]byte
	DigestB  [sha256.Size]byte
}

// OpenRun is a difference run that was still open (not yet closed by a
// matching byte) when the checkpoint was taken. The digests cover each
// side's bytes in [Start, Checkpoint.Offset).
type OpenRun struct {
	Start   int64
	DigestA [sha256.Size]byte
	DigestB [sha256.Size]byte
}

// Checkpoint is resumable comparison state. When Done is true the
// comparison finished and Diffs/Tail hold the final result.
type Checkpoint struct {
	Offset   int64  // next offset to compare
	VersionA string // entity versions the confirmed state was built from
	VersionB string
	Diffs    []ConfirmedDiff // confirmed difference intervals, in order
	Open     *OpenRun        // unconfirmed run crossing Offset, if any
	Tail     *MissingTail    // set when Done
	Done     bool
}

func (c *Checkpoint) clone() *Checkpoint {
	out := *c
	out.Diffs = append([]ConfirmedDiff(nil), c.Diffs...)
	if c.Open != nil {
		o := *c.Open
		out.Open = &o
	}
	if c.Tail != nil {
		t := *c.Tail
		out.Tail = &t
	}
	return &out
}

// RemoteStats reports what a CompareRemote call did.
type RemoteStats struct {
	ReusedDiffs int // confirmed intervals carried through resume validation
}

// CompareRemote compares two remote sources chunk by chunk. Every read
// carries the entity version; if either source changes version during
// the pass, the pass's conclusions are discarded and ErrVersionChanged
// is returned together with the checkpoint validated at entry.
//
// When resume is non-nil, confirmed intervals are reused only if both
// sources still report the recorded versions and the re-fetched bytes
// still match the recorded digests; the first interval that fails
// validation truncates the checkpoint back to its start. Transient
// errors return the current checkpoint so the caller can resume later.
func CompareRemote(ctx context.Context, a, b RemoteSource, chunkSize int64, resume *Checkpoint) (Checkpoint, RemoteStats, error) {
	var stats RemoteStats
	if chunkSize <= 0 {
		chunkSize = 1 << 20
	}
	if resume != nil && resume.Done {
		return *resume.clone(), stats, nil
	}
	verA, err := a.Version(ctx)
	if err != nil {
		return Checkpoint{}, stats, err
	}
	verB, err := b.Version(ctx)
	if err != nil {
		return Checkpoint{}, stats, err
	}

	wc := &Checkpoint{VersionA: verA, VersionB: verB}
	if resume != nil && resume.VersionA == verA && resume.VersionB == verB {
		wc = resume.clone()
		keep := 0
		for keep < len(wc.Diffs) {
			ok, err := validateDiff(ctx, a, b, wc.Diffs[keep])
			if err != nil {
				return *wc, stats, err
			}
			if !ok {
				break
			}
			keep++
		}
		stats.ReusedDiffs = keep
		if keep < len(wc.Diffs) {
			wc.Offset = wc.Diffs[keep].Start
			wc.Diffs = wc.Diffs[:keep]
			wc.Open = nil
			wc.Tail = nil
		}
	}

	// Rebuild the unconfirmed open run, re-validating its bytes.
	openStart := int64(-1)
	var hashA, hashB hash.Hash
	if wc.Open != nil {
		o := wc.Open
		fa, err := fetchRange(ctx, a, o.Start, wc.Offset-o.Start)
		if err != nil {
			return *wc, stats, err
		}
		fb, err := fetchRange(ctx, b, o.Start, wc.Offset-o.Start)
		if err != nil {
			return *wc, stats, err
		}
		if fa.eof || fb.eof || fa.vbreak || fb.vbreak ||
			fa.version != wc.VersionA || fb.version != wc.VersionB ||
			sha256.Sum256(fa.data) != o.DigestA || sha256.Sum256(fb.data) != o.DigestB {
			wc.Offset = o.Start
			wc.Open = nil
		} else {
			openStart = o.Start
			hashA = sha256.New()
			hashA.Write(fa.data)
			hashB = sha256.New()
			hashB.Write(fb.data)
		}
	}
	entry := wc.clone()

	snapshotOpen := func() {
		if openStart >= 0 {
			wc.Open = &OpenRun{Start: openStart}
			copy(wc.Open.DigestA[:], hashA.Sum(nil))
			copy(wc.Open.DigestB[:], hashB.Sum(nil))
		} else {
			wc.Open = nil
		}
	}
	closeRun := func(end int64) {
		if openStart < 0 {
			return
		}
		d := ConfirmedDiff{
			Interval: Interval{Start: openStart, End: end},
			VersionA: wc.VersionA,
			VersionB: wc.VersionB,
		}
		copy(d.DigestA[:], hashA.Sum(nil))
		copy(d.DigestB[:], hashB.Sum(nil))
		wc.Diffs = append(wc.Diffs, d)
		openStart = -1
	}

	for {
		if err := ctx.Err(); err != nil {
			snapshotOpen()
			return *wc, stats, err
		}
		fa, err := fetchRange(ctx, a, wc.Offset, chunkSize)
		if err != nil {
			snapshotOpen()
			return *wc, stats, err
		}
		fb, err := fetchRange(ctx, b, wc.Offset, chunkSize)
		if err != nil {
			snapshotOpen()
			return *wc, stats, err
		}
		va, vb := fa.version, fb.version
		if va == "" && fa.eof {
			va = verA
		}
		if vb == "" && fb.eof {
			vb = verB
		}
		if fa.vbreak || fb.vbreak || va != wc.VersionA || vb != wc.VersionB {
			return *entry, stats, ErrVersionChanged
		}
		n := min(len(fa.data), len(fb.data))
		base := wc.Offset
		for i := 0; i < n; i++ {
			if fa.data[i] != fb.data[i] {
				if openStart < 0 {
					openStart = base + int64(i)
					hashA = sha256.New()
					hashB = sha256.New()
				}
				hashA.Write(fa.data[i : i+1])
				hashB.Write(fb.data[i : i+1])
			} else if openStart >= 0 {
				closeRun(base + int64(i))
			}
		}
		wc.Offset += int64(n)
		if !fa.eof && !fb.eof {
			snapshotOpen()
			continue
		}
		closeRun(wc.Offset)
		snapshotOpen()
		if len(fa.data) == len(fb.data) {
			wc.Done = true
			return *wc, stats, nil
		}
		// One side is shorter; the other's tail is reported missing.
		tailStart := wc.Offset
		side := 0
		longer := b
		longerData := fb.data
		longerVer := wc.VersionB
		longerEOF := fb.eof
		if len(fa.data) > len(fb.data) {
			side = 1
			longer = a
			longerData = fa.data
			longerVer = wc.VersionA
			longerEOF = fa.eof
		}
		end := tailStart + int64(len(longerData)-n)
		for !longerEOF {
			d, err := fetchRange(ctx, longer, end, chunkSize)
			if err != nil {
				// Rewind so a resume re-derives the tail.
				wc.Offset = tailStart
				return *wc, stats, err
			}
			dv := d.version
			if dv == "" && d.eof {
				dv = longerVer
			}
			if d.vbreak || dv != longerVer {
				return *entry, stats, ErrVersionChanged
			}
			end += int64(len(d.data))
			if d.eof {
				break
			}
		}
		wc.Tail = &MissingTail{Side: side, Start: tailStart, End: end}
		wc.Done = true
		return *wc, stats, nil
	}
}

// validateDiff re-fetches a confirmed interval from both sources and
// checks that versions and digests still match the recorded ones.
func validateDiff(ctx context.Context, a, b RemoteSource, d ConfirmedDiff) (bool, error) {
	fa, err := fetchRange(ctx, a, d.Start, d.End-d.Start)
	if err != nil {
		return false, err
	}
	fb, err := fetchRange(ctx, b, d.Start, d.End-d.Start)
	if err != nil {
		return false, err
	}
	if fa.eof || fb.eof || fa.vbreak || fb.vbreak {
		return false, nil
	}
	if fa.version != d.VersionA || fb.version != d.VersionB {
		return false, nil
	}
	if sha256.Sum256(fa.data) != d.DigestA || sha256.Sum256(fb.data) != d.DigestB {
		return false, nil
	}
	return true, nil
}

type fetchOut struct {
	data    []byte
	version string
	eof     bool // source ended before length bytes were covered
	vbreak  bool // a differing entity version was encountered after data
}

// fetchRange assembles [offset, offset+length) from possibly out-of-order,
// possibly short segment responses. All returned bytes share one entity
// version; if a differing version shows up after a stable prefix, the
// prefix is returned with vbreak set.
func fetchRange(ctx context.Context, src RemoteSource, offset, length int64) (fetchOut, error) {
	if length <= 0 {
		return fetchOut{}, nil
	}
	limit := offset + length
	var segs []Segment
	stall := 0
	for {
		end, version, vbreak := walkCoverage(segs, offset)
		if vbreak {
			return fetchOut{data: assemble(segs, offset, end), version: version, vbreak: true}, nil
		}
		if end >= limit {
			return fetchOut{data: assemble(segs, offset, limit), version: version}, nil
		}
		if stall > 16 {
			return fetchOut{}, fmt.Errorf("diffcmp: fetch at offset %d made no progress", offset)
		}
		if err := ctx.Err(); err != nil {
			return fetchOut{}, err
		}
		batch, err := src.FetchRange(ctx, end, limit-end)
		if err != nil {
			return fetchOut{}, err
		}
		if len(batch) == 0 {
			return fetchOut{data: assemble(segs, offset, end), version: version, eof: true}, nil
		}
		segs = append(segs, batch...)
		sort.SliceStable(segs, func(i, j int) bool { return segs[i].Offset < segs[j].Offset })
		if end2, _, _ := walkCoverage(segs, offset); end2 > end {
			stall = 0
		} else {
			stall++
		}
	}
}

// walkCoverage returns the end of the contiguous covered run starting at
// offset, the version of that run, and whether a differing version was
// met at the run's end.
func walkCoverage(segs []Segment, offset int64) (end int64, version string, vbreak bool) {
	cur := offset
	hasVersion := false
	for {
		idx := -1
		for i := range segs {
			s := &segs[i]
			if s.Offset <= cur && cur < s.Offset+int64(len(s.Data)) {
				if idx == -1 || s.Offset < segs[idx].Offset {
					idx = i
				}
			}
		}
		if idx == -1 {
			return cur, version, false
		}
		s := segs[idx]
		if !hasVersion {
			version, hasVersion = s.Version, true
		} else if s.Version != version {
			return cur, version, true
		}
		cur = s.Offset + int64(len(s.Data))
	}
}

// assemble copies [start, end) out of segs using the same segment
// selection rule as walkCoverage.
func assemble(segs []Segment, start, end int64) []byte {
	out := make([]byte, 0, end-start)
	cur := start
	for cur < end {
		idx := -1
		for i := range segs {
			s := &segs[i]
			if s.Offset <= cur && cur < s.Offset+int64(len(s.Data)) {
				if idx == -1 || s.Offset < segs[idx].Offset {
					idx = i
				}
			}
		}
		if idx == -1 {
			break
		}
		s := segs[idx]
		from := cur - s.Offset
		to := min(int64(len(s.Data)), from+(end-cur))
		out = append(out, s.Data[from:to]...)
		cur += to - from
	}
	return out
}
