package window

import (
	"bytes"
	"math/rand"
	"testing"
)

// naivePeriod is the reference: enumerate every divisor of n and compare
// candidate periods byte by byte.
func naivePeriod(s []byte) ([]byte, int) {
	n := len(s)
	if n == 0 {
		return nil, 0
	}
	for d := 1; d <= n; d++ {
		if n%d != 0 {
			continue
		}
		ok := true
		for i := d; i < n; i++ {
			if s[i] != s[i-d] {
				ok = false
				break
			}
		}
		if ok {
			return s[:d], n / d
		}
	}
	return s, 1
}

// model tracks the ground truth: the full stream plus the window size.
// The window always holds the last min(w, len(stream)) bytes, and Undo
// truncates the stream (restoring any byte that had slid out).
type model struct {
	w      int
	stream []byte
}

func (m *model) append(b byte) { m.stream = append(m.stream, b) }
func (m *model) undo()         { m.stream = m.stream[:len(m.stream)-1] }
func (m *model) window() []byte {
	s := m.stream
	if len(s) > m.w {
		s = s[len(s)-m.w:]
	}
	return s
}

func checkPeriod(t *testing.T, win *Window, m *model) {
	t.Helper()
	want := m.window()
	p, k := win.Period()
	ep, ek := naivePeriod(want)
	if !bytes.Equal(p, ep) || k != ek {
		t.Fatalf("window %q: got period %q x%d, want %q x%d", want, p, k, ep, ek)
	}
	if got := win.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("window contents %q, want %q", got, want)
	}
	if win.Len() != len(want) {
		t.Fatalf("Len() = %d, want %d", win.Len(), len(want))
	}
}

func TestSameByteLongString(t *testing.T) {
	const w = 4096
	win := New(w)
	m := &model{w: w}
	for i := 0; i < 2*w; i++ { // exceed capacity to exercise sliding
		win.Append('a')
		m.append('a')
		p, k := win.Period()
		if len(p) != 1 || p[0] != 'a' || k != win.Len() {
			t.Fatalf("len=%d: got %q x%d", win.Len(), p, k)
		}
	}
}

func TestPrimeLength(t *testing.T) {
	primes := []int{2, 3, 5, 7, 89, 97, 389, 397, 997}
	rng := rand.New(rand.NewSource(1))
	for _, w := range primes {
		win := New(w)
		m := &model{w: w}
		for i := 0; i < w; i++ {
			b := byte(rng.Intn(256))
			win.Append(b)
			m.append(b)
		}
		// Random content is almost surely aperiodic: only divisors 1 and w,
		// and period 1 would require all bytes equal.
		p, k := win.Period()
		if k != 1 || !bytes.Equal(p, m.window()) {
			t.Fatalf("prime w=%d: got %q x%d, want whole window x1", w, p, k)
		}
		// All-equal bytes of prime length must collapse to period 1.
		win2 := New(w)
		for i := 0; i < w; i++ {
			win2.Append('z')
		}
		if p, k := win2.Period(); len(p) != 1 || k != w {
			t.Fatalf("prime w=%d all-equal: got %q x%d", w, p, k)
		}
	}
}

func TestWindowSlidesPastPeriodBreakpoint(t *testing.T) {
	// Window holds exactly two copies of "abc"; appending anything else
	// breaks the period, and sliding further must restore order.
	win := New(6)
	m := &model{w: 6}
	feed := func(s string) {
		for i := 0; i < len(s); i++ {
			win.Append(s[i])
			m.append(s[i])
			checkPeriod(t, win, m)
		}
	}
	feed("abcabc")
	if p, k := win.Period(); string(p) != "abc" || k != 2 {
		t.Fatalf("got %q x%d, want abc x2", p, k)
	}
	feed("x")     // breakpoint enters: window "bcabcx"
	feed("abcab") // slides across the breakpoint byte by byte
	feed("cccccc")
	if p, k := win.Period(); string(p) != "c" || k != 6 {
		t.Fatalf("got %q x%d, want c x6", p, k)
	}
}

func TestUndo(t *testing.T) {
	win := New(4)
	m := &model{w: 4}
	push := func(b byte) {
		win.Append(b)
		m.append(b)
	}
	pop := func() {
		if !win.Undo() {
			t.Fatal("Undo returned false with pending appends")
		}
		m.undo()
	}
	for _, b := range []byte("ababab") { // fills and slides the window
		push(b)
	}
	checkPeriod(t, win, m) // "abab"
	pop()
	checkPeriod(t, win, m) // "baba": undo restores the evicted front byte
	pop()
	checkPeriod(t, win, m) // "bab"
	push('a')
	push('b')
	push('a') // evicts again; undo must restore the evicted front byte
	checkPeriod(t, win, m)
	pop()
	checkPeriod(t, win, m)
	pop()
	checkPeriod(t, win, m)
	// Drain everything.
	for win.Len() > 0 {
		pop()
		checkPeriod(t, win, m)
	}
	if win.Undo() {
		t.Fatal("Undo on empty history returned true")
	}
	if p, k := win.Period(); p != nil || k != 0 {
		t.Fatalf("empty window: got %q x%d", p, k)
	}
}

// TestRandomCrossCheck drives random append/undo mixes against the naive
// reference after every single step, across many capacities.
func TestRandomCrossCheck(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 200; trial++ {
		w := 1 + rng.Intn(48)
		win := New(w)
		m := &model{w: w}
		steps := 200 + rng.Intn(300)
		for s := 0; s < steps; s++ {
			if win.Len() > 0 && rng.Intn(4) == 0 {
				if !win.Undo() {
					t.Fatal("Undo failed")
				}
				m.undo()
			} else {
				var b byte
				if rng.Intn(3) == 0 {
					b = "abc"[rng.Intn(3)] // small alphabet -> frequent periods
				} else {
					b = byte(rng.Intn(256))
				}
				win.Append(b)
				m.append(b)
			}
			checkPeriod(t, win, m)
		}
	}
}

func TestStructuredPatterns(t *testing.T) {
	cases := []struct {
		w      int
		stream string
	}{
		{6, "ababab"},
		{9, "abcabcabc"},
		{12, "abcdabcdabcd"},
		{8, "aaaabaaa"},
		{10, "xyxyxyxyxy"},
		{1, "q"},
		{16, "abbaabbaabbaabba"},
	}
	for _, c := range cases {
		win := New(c.w)
		m := &model{w: c.w}
		for i := 0; i < len(c.stream); i++ {
			win.Append(c.stream[i])
			m.append(c.stream[i])
			checkPeriod(t, win, m)
		}
	}
}
