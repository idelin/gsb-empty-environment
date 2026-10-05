// Package window implements a fixed-capacity sliding window over a byte
// stream. It keeps the most recent W bytes and answers, at any moment,
// for the shortest complete period of the current window: the shortest
// byte string whose length divides the window length and whose repetition
// equals the whole window.
//
// Append and Undo are O(1). Period costs O(number of divisors of the
// window length) hash probes plus one O(n) verification of the answer,
// instead of re-comparing every candidate period byte by byte.
package window

// base is the multiplier of the polynomial rolling hash. Arithmetic is
// mod 2^64 via natural uint64 overflow.
const base uint64 = 1099511628211

// Window is a sliding window over the last W appended bytes.
type Window struct {
	w     int
	start int // stream index of the first byte in the window
	end   int // stream index one past the last byte (exclusive)

	ring []byte   // ring[start%cap .. end%cap): window bytes
	hash []uint64 // hash[k%cap] = rolling hash of stream[:k]
	pow  []uint64 // pow[i] = base^i

	hist []histEntry // one entry per Append, for Undo
}

type histEntry struct {
	evicted  bool
	evictedB byte   // byte dropped from the front, if any
	evictedH uint64 // hash[start] before eviction
}

// New returns a Window holding at most w bytes. It panics if w < 1.
func New(w int) *Window {
	if w < 1 {
		panic("window: capacity must be >= 1")
	}
	cap := w + 1 // one spare slot so a full window's indices stay distinct
	win := &Window{
		w:    w,
		ring: make([]byte, cap),
		hash: make([]uint64, cap),
		pow:  make([]uint64, w+1),
	}
	win.pow[0] = 1
	for i := 1; i <= w; i++ {
		win.pow[i] = win.pow[i-1] * base
	}
	return win
}

// Len returns the number of bytes currently in the window.
func (win *Window) Len() int { return win.end - win.start }

// Append adds b to the back of the window, evicting the oldest byte if
// the window is full.
func (win *Window) Append(b byte) {
	var e histEntry
	if win.Len() == win.w {
		s := win.start % len(win.ring)
		e.evicted = true
		e.evictedB = win.ring[s]
		e.evictedH = win.hash[s]
		win.start++
	}
	cap := len(win.ring)
	win.ring[win.end%cap] = b
	win.hash[(win.end+1)%cap] = win.hash[win.end%cap]*base + uint64(b)
	win.end++
	win.hist = append(win.hist, e)
}

// Undo reverts the most recent Append, restoring any byte it evicted.
// It reports whether there was an Append to undo.
func (win *Window) Undo() bool {
	n := len(win.hist)
	if n == 0 {
		return false
	}
	e := win.hist[n-1]
	win.hist = win.hist[:n-1]
	win.end--
	if e.evicted {
		win.start--
		s := win.start % len(win.ring)
		win.ring[s] = e.evictedB
		win.hash[s] = e.evictedH
	}
	return true
}

// Bytes returns a copy of the current window contents.
func (win *Window) Bytes() []byte {
	n := win.Len()
	out := make([]byte, n)
	cap := len(win.ring)
	for i := 0; i < n; i++ {
		out[i] = win.ring[(win.start+i)%cap]
	}
	return out
}

// hashOf returns the hash of stream[l:r], for start <= l <= r <= end.
func (win *Window) hashOf(l, r int) uint64 {
	cap := len(win.ring)
	return win.hash[r%cap] - win.hash[l%cap]*win.pow[r-l]
}

// hasPeriod reports whether the window consists of repeats of its first
// d bytes. A cheap hash probe filters candidates; a direct comparison
// confirms, so hash collisions can never yield a wrong answer.
func (win *Window) hasPeriod(d int) bool {
	n := win.Len()
	if d == n {
		return true
	}
	if win.hashOf(win.start, win.end-d) != win.hashOf(win.start+d, win.end) {
		return false
	}
	cap := len(win.ring)
	for i := 0; i < n-d; i++ {
		if win.ring[(win.start+i)%cap] != win.ring[(win.start+d+i)%cap] {
			return false
		}
	}
	return true
}

// Period returns the shortest byte string p and repeat count k such that
// the current window equals p repeated k times. The period length always
// divides the window length. It returns (nil, 0) for an empty window.
func (win *Window) Period() (period []byte, times int) {
	n := win.Len()
	if n == 0 {
		return nil, 0
	}
	for _, d := range divisors(n) {
		if win.hasPeriod(d) {
			period = win.Bytes()[:d]
			return period, n / d
		}
	}
	panic("window: unreachable; length n is always a period")
}

// divisors returns the positive divisors of n in ascending order.
func divisors(n int) []int {
	var lo, hi []int
	for i := 1; i*i <= n; i++ {
		if n%i == 0 {
			lo = append(lo, i)
			if i*i != n {
				hi = append(hi, n/i)
			}
		}
	}
	for j := len(hi) - 1; j >= 0; j-- {
		lo = append(lo, hi[j])
	}
	return lo
}
