// Package diffcmp compares two byte streams (or two remote byte sources)
// and reports maximal contiguous difference intervals. Tails caused by
// unequal lengths are reported separately as missing data, and hard read
// errors are distinguished from EOF: confirmed differences are kept but
// the overall result is marked incomplete.
package diffcmp

import (
	"io"
)

// Interval is a half-open byte range [Start, End) that differs between
// the two inputs. Intervals are maximal: two differing bytes separated
// only by differing bytes are merged into one interval.
type Interval struct {
	Start int64
	End   int64
}

// MissingTail describes the tail of the longer input that has no
// counterpart in the shorter input. Side is 0 when A is the shorter
// stream and 1 when B is. End is -1 when the length of the longer
// stream could not be determined because reading it failed.
type MissingTail struct {
	Side  int
	Start int64
	End   int64
}

// StreamResult is the outcome of CompareStreams.
type StreamResult struct {
	Diffs    []Interval
	Missing  []MissingTail
	Complete bool  // false when a hard read error prevented a full comparison
	Err      error // the first hard read error encountered, if any
}

const defaultChunkSize = 32 * 1024

// CompareStreams reads a and b chunk by chunk and reports where their
// contents differ. Short reads (a Read returning fewer bytes than
// requested with a nil error) are transparently assembled. A read error
// other than io.EOF stops the comparison: differences confirmed so far
// are kept, but the result is marked incomplete.
func CompareStreams(a, b io.Reader, chunkSize int) StreamResult {
	if chunkSize <= 0 {
		chunkSize = defaultChunkSize
	}
	bufA := make([]byte, chunkSize)
	bufB := make([]byte, chunkSize)
	var res StreamResult
	var offset int64
	open := int64(-1)
	closeOpen := func(end int64) {
		if open >= 0 {
			res.Diffs = append(res.Diffs, Interval{Start: open, End: end})
			open = -1
		}
	}
	for {
		nA, eA := readFull(a, bufA)
		nB, eB := readFull(b, bufB)
		n := min(nA, nB)
		for i := 0; i < n; i++ {
			if bufA[i] != bufB[i] {
				if open < 0 {
					open = offset + int64(i)
				}
			} else if open >= 0 {
				closeOpen(offset + int64(i))
			}
		}
		offset += int64(n)
		hardA := eA != nil && eA != io.EOF
		hardB := eB != nil && eB != io.EOF
		termA := eA != nil
		termB := eB != nil
		if !termA && !termB {
			continue
		}
		// At least one side terminated (EOF or hard error). The compared
		// region ends here; any open difference run is confirmed.
		closeOpen(offset)
		switch {
		case nA == nB:
			// Both sides terminated at the same offset.
			if hardA {
				res.Err = eA
			} else if hardB {
				res.Err = eB
			} else {
				res.Complete = true
			}
			return res
		case nA < nB:
			// A ended first; B has a tail that A is missing.
			if hardA {
				res.Err = eA
				return res
			}
			tail := MissingTail{Side: 0, Start: offset}
			res.finishTail(&tail, b, int64(nB-n), eB, bufB)
			res.Missing = append(res.Missing, tail)
			return res
		default:
			// B ended first; A has a tail that B is missing.
			if hardB {
				res.Err = eB
				return res
			}
			tail := MissingTail{Side: 1, Start: offset}
			res.finishTail(&tail, a, int64(nA-n), eA, bufA)
			res.Missing = append(res.Missing, tail)
			return res
		}
	}
}

// finishTail determines the end of the longer stream. buffered is the
// number of bytes of the longer stream already read beyond the compared
// region, and e is the error its last read returned (nil, io.EOF, or a
// hard error).
func (res *StreamResult) finishTail(tail *MissingTail, longer io.Reader, buffered int64, e error, scratch []byte) {
	known := tail.Start + buffered
	switch {
	case e == io.EOF:
		tail.End = known
		res.Complete = true
	case e != nil:
		tail.End = -1
		res.Err = e
	default:
		rest, err := drain(longer, scratch)
		if err != nil {
			tail.End = -1
			res.Err = err
		} else {
			tail.End = known + rest
			res.Complete = true
		}
	}
}

// readFull reads until buf is full, the stream ends, or a hard error
// occurs. Short reads are assembled. It returns io.EOF only when the
// stream ended before buf was filled.
func readFull(r io.Reader, buf []byte) (int, error) {
	total := 0
	zero := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if err != nil {
			if err == io.EOF && total == len(buf) {
				return total, nil
			}
			return total, err
		}
		if n == 0 {
			zero++
			if zero > 100 {
				return total, io.ErrNoProgress
			}
		} else {
			zero = 0
		}
	}
	return total, nil
}

// drain reads r to EOF and returns the number of bytes read. A hard
// error is returned alongside the partial count.
func drain(r io.Reader, scratch []byte) (int64, error) {
	var total int64
	for {
		n, err := readFull(r, scratch)
		total += int64(n)
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return total, err
		}
	}
}
