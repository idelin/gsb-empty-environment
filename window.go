// Package windowperiod implements a fixed-size sliding window over a byte
// stream. The window retains the most recent W bytes and answers "shortest
// complete period" queries: the smallest period length p such that p divides
// W and the whole window consists of W/p repetitions of a length-p block.
//
// Cost model: Append and Undo are O(1). A Period query costs O(d(W)) where
// d(W) is the number of divisors of W: each candidate period is verified in
// O(1) by comparing double rolling hashes of two window slices, instead of
// re-comparing all bytes for every candidate on every step.
package windowperiod

// Bases for the double polynomial rolling hash. Arithmetic uses uint64
// natural overflow (mod 2^64); two independent bases keep the collision
// probability negligible.
const (
	base1 = 0x9e3779b97f4a7c15
	base2 = 0xbf58476d1ce4e5b9
)

// record describes one Append so it can be undone. If the append evicted
// the oldest byte of a full window, the evicted byte and its rolling-hash
// state are kept so Undo can restore them exactly.
type record struct {
	evicted bool
	b       byte
	h1, h2  uint64
}

// Window is a sliding window over an appended byte stream. The zero value
// is not usable; construct with New.
type Window struct {
	w   int // window length
	cap int // ring capacity, w+1 (one spare slot so evicted bytes stay readable)

	start int // absolute index of the first byte currently in the window
	end   int // absolute index one past the last appended byte

	buf []byte   // buf[i%cap]: byte appended at absolute position i
	f1  []uint64 // f1[i%cap]: rolling hash (base1) of bytes[0..i]
	f2  []uint64 // f2[i%cap]: rolling hash (base2) of bytes[0..i]

	pow1 []uint64 // pow1[k] = base1^k for k in [0, w]
	pow2 []uint64

	history  []record // one entry per Append, popped by Undo
	divisors []int    // divisors of w, ascending

	// baseH1/baseH2 hold F[start-1]: the rolling hash of everything before
	// the window, used as the base for hashes of ranges starting at start.
	// The ring slot of position start-1 cannot be trusted: it may have been
	// overwritten by a later append that was itself undone since.
	baseH1 uint64
	baseH2 uint64

	// Memoized result of the last Period call, valid while start/end
	// are unchanged.
	qValid   bool
	qStart   int
	qEnd     int
	qPeriod  []byte
	qRepeats int
}

// New returns a Window holding the most recent w bytes. w must be >= 1.
func New(w int) *Window {
	if w < 1 {
		panic("windowperiod: window length must be >= 1")
	}
	cap := w + 1
	win := &Window{
		w:    w,
		cap:  cap,
		buf:  make([]byte, cap),
		f1:   make([]uint64, cap),
		f2:   make([]uint64, cap),
		pow1: make([]uint64, w+1),
		pow2: make([]uint64, w+1),
	}
	win.pow1[0] = 1
	win.pow2[0] = 1
	for i := 1; i <= w; i++ {
		win.pow1[i] = win.pow1[i-1] * base1
		win.pow2[i] = win.pow2[i-1] * base2
	}
	for p := 1; p <= w; p++ {
		if w%p == 0 {
			win.divisors = append(win.divisors, p)
		}
	}
	return win
}

// W returns the configured window length.
func (win *Window) W() int { return win.w }

// Len returns the number of bytes currently in the window (<= W).
func (win *Window) Len() int { return win.end - win.start }

// Append adds b to the stream, evicting the oldest byte if the window is
// full. It is O(1).
func (win *Window) Append(b byte) {
	slot := win.end % win.cap
	win.buf[slot] = b
	var p1, p2 uint64
	if win.end > 0 {
		prev := (win.end - 1) % win.cap
		p1 = win.f1[prev]
		p2 = win.f2[prev]
	}
	win.f1[slot] = p1*base1 + uint64(b)
	win.f2[slot] = p2*base2 + uint64(b)
	win.end++

	var rec record
	if win.end-win.start > win.w {
		s := win.start % win.cap
		rec = record{evicted: true, b: win.buf[s], h1: win.f1[s], h2: win.f2[s]}
		// The evicted position becomes the new start-1.
		win.baseH1, win.baseH2 = win.f1[s], win.f2[s]
		win.start++
	}
	win.history = append(win.history, rec)
	win.qValid = false
}

// Undo reverts the most recent Append, restoring any byte it evicted. It
// reports whether there was an Append to undo. Undo is O(1) and may be
// applied repeatedly to undo multiple appends in LIFO order.
func (win *Window) Undo() bool {
	n := len(win.history)
	if n == 0 {
		return false
	}
	rec := win.history[n-1]
	win.history = win.history[:n-1]
	win.end--
	if rec.evicted {
		win.start--
		s := win.start % win.cap
		win.buf[s] = rec.b
		win.f1[s] = rec.h1
		win.f2[s] = rec.h2
		if win.start == 0 {
			win.baseH1, win.baseH2 = 0, 0
		} else {
			// The window is full again, so the new top record is the
			// append that evicted position start-1; its saved hash is
			// exactly F[start-1].
			top := win.history[len(win.history)-1]
			win.baseH1, win.baseH2 = top.h1, top.h2
		}
	}
	win.qValid = false
	return true
}

// Bytes returns a copy of the current window contents, oldest first.
func (win *Window) Bytes() []byte {
	n := win.Len()
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = win.buf[(win.start+i)%win.cap]
	}
	return out
}

// hash returns the double rolling hash of absolute positions [l, r).
// Callers must ensure start-1 <= l-1 and r-1 <= end-1 so both ring slots
// still hold live data.
func (win *Window) hash(l, r int) (uint64, uint64) {
	if l >= r {
		return 0, 0
	}
	hi1 := win.f1[(r-1)%win.cap]
	hi2 := win.f2[(r-1)%win.cap]
	var lo1, lo2 uint64
	if l == win.start {
		lo1, lo2 = win.baseH1, win.baseH2
	} else {
		lo1 = win.f1[(l-1)%win.cap]
		lo2 = win.f2[(l-1)%win.cap]
	}
	k := r - l
	return hi1 - lo1*win.pow1[k], hi2 - lo2*win.pow2[k]
}

// Period finds the shortest complete period of the current window: the
// smallest p dividing W such that the window equals its length-p prefix
// repeated W/p times. It returns the period bytes and the repeat count.
// ok is false while fewer than W bytes are in the window.
//
// Each candidate p is checked in O(1): the window is p-periodic iff its
// first W-p bytes equal its last W-p bytes, which the rolling hash
// verifies without scanning. Candidates are tried in ascending order, so
// the first match is the shortest period. p == W always matches, so a
// result always exists once the window is full.
func (win *Window) Period() (period []byte, repeats int, ok bool) {
	if win.Len() < win.w {
		return nil, 0, false
	}
	if win.qValid && win.qStart == win.start && win.qEnd == win.end {
		return append([]byte(nil), win.qPeriod...), win.qRepeats, true
	}
	for _, p := range win.divisors {
		a1, a2 := win.hash(win.start, win.start+win.w-p)
		b1, b2 := win.hash(win.start+p, win.start+win.w)
		if a1 == b1 && a2 == b2 {
			period = make([]byte, p)
			for i := 0; i < p; i++ {
				period[i] = win.buf[(win.start+i)%win.cap]
			}
			win.qValid = true
			win.qStart, win.qEnd = win.start, win.end
			win.qPeriod = append(win.qPeriod[:0], period...)
			win.qRepeats = win.w / p
			return period, win.qRepeats, true
		}
	}
	panic("windowperiod: unreachable, p == W always matches")
}
