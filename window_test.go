package windowperiod

import (
	"bytes"
	"math/rand"
	"testing"
)

// naivePeriod is the reference implementation: it enumerates the factors
// of W in ascending order and verifies each candidate by direct byte
// comparison. Slow but obviously correct.
func naivePeriod(s []byte, w int) (string, int, bool) {
	if len(s) < w {
		return "", 0, false
	}
	s = s[len(s)-w:]
	for p := 1; p <= w; p++ {
		if w%p != 0 {
			continue
		}
		ok := true
		for i := p; i < w; i++ {
			if s[i] != s[i-p] {
				ok = false
				break
			}
		}
		if ok {
			return string(s[:p]), w / p, true
		}
	}
	panic("unreachable")
}

// naiveModel mirrors the stream semantics with plain slices.
type naiveModel struct {
	w     int
	hist  []byte
	start int
	evict []bool
}

func (m *naiveModel) append(b byte) {
	m.hist = append(m.hist, b)
	if len(m.hist)-m.start > m.w {
		m.start++
		m.evict = append(m.evict, true)
	} else {
		m.evict = append(m.evict, false)
	}
}

func (m *naiveModel) undo() bool {
	if len(m.evict) == 0 {
		return false
	}
	ev := m.evict[len(m.evict)-1]
	m.evict = m.evict[:len(m.evict)-1]
	m.hist = m.hist[:len(m.hist)-1]
	if ev {
		m.start--
	}
	return true
}

func (m *naiveModel) window() []byte { return m.hist[m.start:] }

// check compares the library against the naive reference for the current
// model state.
func check(t *testing.T, win *Window, m *naiveModel) {
	t.Helper()
	if got := win.Len(); got != len(m.window()) {
		t.Fatalf("Len() = %d, want %d", got, len(m.window()))
	}
	if got := win.Bytes(); !bytes.Equal(got, m.window()) {
		t.Fatalf("Bytes() = %q, want %q", got, m.window())
	}
	wantP, wantR, wantOK := naivePeriod(m.window(), m.w)
	gotP, gotR, gotOK := win.Period()
	if gotOK != wantOK {
		t.Fatalf("Period ok = %v, want %v (window %q)", gotOK, wantOK, m.window())
	}
	if !wantOK {
		return
	}
	if string(gotP) != wantP || gotR != wantR {
		t.Fatalf("Period() = (%q, %d), want (%q, %d); window %q",
			gotP, gotR, wantP, wantR, m.window())
	}
}

func TestSameByteLongString(t *testing.T) {
	const w = 512
	win := New(w)
	m := &naiveModel{w: w}
	for i := 0; i < 5000; i++ {
		win.Append('x')
		m.append('x')
	}
	check(t, win, m)
	p, r, ok := win.Period()
	if !ok || string(p) != "x" || r != w {
		t.Fatalf("Period() = (%q, %d, %v), want (\"x\", %d, true)", p, r, ok, w)
	}
	// Undo a batch of appends; the window must stay all 'x'.
	for i := 0; i < 100; i++ {
		if !win.Undo() {
			t.Fatal("Undo() = false, want true")
		}
		m.undo()
	}
	check(t, win, m)
}

func TestPrimeWindowLength(t *testing.T) {
	const w = 13 // prime: only divisors are 1 and 13

	// All identical bytes -> period length 1.
	win := New(w)
	m := &naiveModel{w: w}
	for i := 0; i < 40; i++ {
		win.Append('q')
		m.append('q')
	}
	check(t, win, m)
	if p, r, _ := win.Period(); string(p) != "q" || r != w {
		t.Fatalf("Period() = (%q, %d), want (\"q\", %d)", p, r, w)
	}

	// Aperiodic block of length 13 -> period length 13, one repeat.
	block := []byte("abcdefghijklm")
	win2 := New(w)
	m2 := &naiveModel{w: w}
	for i := 0; i < 26; i++ { // two full blocks: window ends exactly on "abcdefghijklm"
		b := block[i%len(block)]
		win2.Append(b)
		m2.append(b)
	}
	check(t, win2, m2)
	if p, r, _ := win2.Period(); string(p) != string(block) || r != 1 {
		t.Fatalf("Period() = (%q, %d), want (%q, 1)", p, r, block)
	}
}

func TestWindowSlidesPastPeriodBreakpoint(t *testing.T) {
	const w = 12
	win := New(w)
	m := &naiveModel{w: w}

	// Phase 1: "abcd" repeated -> period 4.
	stream := []byte(bytes.Repeat([]byte("abcd"), 8))
	// Phase 2: breakpoint, constant run -> eventually period 1.
	stream = append(stream, bytes.Repeat([]byte("e"), 20)...)
	// Phase 3: "xy" repeated -> eventually period 2.
	stream = append(stream, bytes.Repeat([]byte("xy"), 10)...)

	type snapshot struct {
		when  int
		wantP string
		wantR int
	}
	snaps := []snapshot{
		{16, "abcd", 3},        // window = "abcdabcdabcd"
		{44, "e", 12},          // window all 'e'
		{len(stream), "xy", 6}, // window = "xyxyxyxyxyxy"
	}
	for i, b := range stream {
		win.Append(b)
		m.append(b)
		check(t, win, m)
		for _, s := range snaps {
			if i+1 == s.when {
				p, r, ok := win.Period()
				if !ok || string(p) != s.wantP || r != s.wantR {
					t.Fatalf("after %d appends: Period() = (%q, %d, %v), want (%q, %d, true)",
						i+1, p, r, ok, s.wantP, s.wantR)
				}
			}
		}
	}
}

func TestUndo(t *testing.T) {
	const w = 8
	win := New(w)
	m := &naiveModel{w: w}
	stream := []byte("abracadabra123456789")
	for _, b := range stream {
		win.Append(b)
		m.append(b)
		check(t, win, m)
	}
	// Undo everything, checking every intermediate state.
	for i := 0; i < len(stream); i++ {
		if !win.Undo() {
			t.Fatalf("Undo() #%d = false, want true", i)
		}
		m.undo()
		check(t, win, m)
	}
	if win.Undo() {
		t.Fatal("Undo() on empty history = true, want false")
	}
	// Re-append a different suffix after undoing; state must follow the
	// new stream, not the undone one.
	for _, b := range []byte("ZZZZZZZZZZ") {
		win.Append(b)
		m.append(b)
		check(t, win, m)
	}
	if p, r, ok := win.Period(); !ok || string(p) != "Z" || r != w {
		t.Fatalf("Period() = (%q, %d, %v), want (\"Z\", %d, true)", p, r, ok, w)
	}
}

func TestIncompleteWindow(t *testing.T) {
	win := New(4)
	if _, _, ok := win.Period(); ok {
		t.Fatal("Period() on empty window: ok = true, want false")
	}
	win.Append('a')
	win.Append('a')
	if _, _, ok := win.Period(); ok {
		t.Fatal("Period() on half-full window: ok = true, want false")
	}
	win.Append('a')
	win.Append('a')
	if p, r, ok := win.Period(); !ok || string(p) != "a" || r != 4 {
		t.Fatalf("Period() = (%q, %d, %v), want (\"a\", 4, true)", p, r, ok)
	}
	// Undo below the fill level must make the query fail again.
	win.Undo()
	if _, _, ok := win.Period(); ok {
		t.Fatal("Period() after undo below fill level: ok = true, want false")
	}
}

func TestRandomVsNaive(t *testing.T) {
	widths := []int{1, 2, 3, 4, 6, 12, 13, 16, 36, 97, 120}
	rng := rand.New(rand.NewSource(42))
	for _, w := range widths {
		win := New(w)
		m := &naiveModel{w: w}
		for step := 0; step < 3000; step++ {
			// Bias towards appends so the window stays mostly full;
			// small alphabet makes real periods likely.
			if rng.Intn(4) == 0 {
				if win.Undo() != m.undo() {
					t.Fatalf("w=%d step=%d: Undo mismatch", w, step)
				}
			} else {
				b := byte('a' + rng.Intn(3))
				win.Append(b)
				m.append(b)
			}
			check(t, win, m)
		}
	}
}
