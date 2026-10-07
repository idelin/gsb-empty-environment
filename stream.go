package paircmp

import (
	"errors"
	"io"
)

// ErrNoProgress is returned when a reader keeps returning (0, nil).
var ErrNoProgress = errors.New("paircmp: reader made no progress")

// StreamResult is the outcome of CompareStreams.
type StreamResult struct {
	// Diffs are the maximal contiguous difference intervals confirmed
	// so far. They remain valid even when Complete is false.
	Diffs []Interval
	// Missing is set when one stream ended and the other had a tail.
	// It is never set when the comparison is incomplete.
	Missing *TailMissing
	// Complete is false when a read error (distinct from EOF) stopped
	// the comparison early.
	Complete bool
	// Compared is the number of aligned bytes actually compared.
	Compared int64
	// Err is the first non-EOF read error encountered, if any.
	Err error
}

// CompareStreams reads left and right in lockstep, tolerating short
// reads, and reports where their bytes differ. A read error (anything
// other than io.EOF) stops the comparison: differences confirmed so
// far are kept, but the result is marked incomplete.
func CompareStreams(left, right io.Reader, bufSize int) StreamResult {
	if bufSize <= 0 {
		bufSize = 32 * 1024
	}
	var res StreamResult
	lbuf := make([]byte, bufSize)
	rbuf := make([]byte, bufSize)
	var off int64
	runStart, runEnd := int64(-1), int64(-1)
	closeRun := func() {
		if runStart >= 0 {
			res.Diffs = append(res.Diffs, Interval{Start: runStart, End: runEnd})
			runStart = -1
		}
	}
	compare := func(n int) {
		for i := 0; i < n; i++ {
			if lbuf[i] != rbuf[i] {
				if runStart < 0 {
					runStart = off + int64(i)
				}
				runEnd = off + int64(i) + 1
			} else {
				closeRun()
			}
		}
		off += int64(n)
		res.Compared = off
	}
	for {
		ln, lerr := fill(left, lbuf)
		rn, rerr := fill(right, rbuf)
		n := min(ln, rn)
		if n > 0 {
			compare(n)
		}
		if isFatal(lerr) || isFatal(rerr) {
			closeRun()
			res.Err = firstFatal(lerr, rerr)
			return res
		}
		leof := lerr == io.EOF
		reof := rerr == io.EOF
		switch {
		case leof && reof:
			closeRun()
			if ln > rn {
				res.Missing = &TailMissing{Side: 1, Start: off, End: off + int64(ln-rn)}
			} else if rn > ln {
				res.Missing = &TailMissing{Side: 0, Start: off, End: off + int64(rn-ln)}
			}
			res.Complete = true
			return res
		case leof:
			tail := int64(rn - n)
			for {
				m, err := fill(right, rbuf)
				tail += int64(m)
				if err == io.EOF {
					break
				}
				if err != nil {
					closeRun()
					res.Err = err
					return res
				}
			}
			closeRun()
			res.Missing = &TailMissing{Side: 0, Start: off, End: off + tail}
			res.Complete = true
			return res
		case reof:
			tail := int64(ln - n)
			for {
				m, err := fill(left, lbuf)
				tail += int64(m)
				if err == io.EOF {
					break
				}
				if err != nil {
					closeRun()
					res.Err = err
					return res
				}
			}
			closeRun()
			res.Missing = &TailMissing{Side: 1, Start: off, End: off + tail}
			res.Complete = true
			return res
		}
	}
}

// fill reads until buf is full, EOF, or a real error. Short reads are
// retried; the returned error is io.EOF only on clean termination.
func fill(r io.Reader, buf []byte) (int, error) {
	total := 0
	zeroReads := 0
	for total < len(buf) {
		n, err := r.Read(buf[total:])
		total += n
		if n == 0 {
			zeroReads++
		} else {
			zeroReads = 0
		}
		if err != nil {
			return total, err
		}
		if zeroReads > 100 {
			return total, ErrNoProgress
		}
	}
	return total, nil
}

func isFatal(err error) bool {
	return err != nil && err != io.EOF
}

func firstFatal(a, b error) error {
	if isFatal(a) {
		return a
	}
	return b
}
