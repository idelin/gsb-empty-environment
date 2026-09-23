package mtf

import (
	"math/rand"
	"reflect"
	"testing"
)

func mustAlphabet(t *testing.T, syms ...rune) *Alphabet {
	t.Helper()
	a, err := NewAlphabet(syms)
	if err != nil {
		t.Fatalf("NewAlphabet: %v", err)
	}
	return a
}

func TestAlphabetRejectsDuplicates(t *testing.T) {
	if _, err := NewAlphabet([]rune{'a', 'b', 'a'}); err != ErrDuplicateSymbol {
		t.Fatalf("NewAlphabet with duplicate: got %v, want ErrDuplicateSymbol", err)
	}
	a := mustAlphabet(t, 'a', 'b')
	if _, err := a.Extend('c', 'a'); err != ErrDuplicateSymbol {
		t.Fatalf("Extend duplicating existing: got %v, want ErrDuplicateSymbol", err)
	}
	if _, err := a.Extend('x', 'x'); err != ErrDuplicateSymbol {
		t.Fatalf("Extend duplicating itself: got %v, want ErrDuplicateSymbol", err)
	}
	b, err := a.Extend('z')
	if err != nil {
		t.Fatalf("Extend: %v", err)
	}
	if b.Version() != a.Version()+1 || b.Len() != 3 {
		t.Fatalf("versioned extension: version %d len %d", b.Version(), b.Len())
	}
	if a.Len() != 2 {
		t.Fatalf("extension mutated the original alphabet")
	}
}

func TestRepeatedSymbolEncodesZero(t *testing.T) {
	m := NewModel(mustAlphabet(t, 'a', 'b', 'c'))
	for _, want := range []int{1, 0, 0, 0} {
		got, err := m.Encode('b')
		if err != nil {
			t.Fatalf("Encode: %v", err)
		}
		if got != want {
			t.Fatalf("Encode('b') = %d, want %d", got, want)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	alpha := mustAlphabet(t, 'a', 'b', 'r', 'c', 'd')
	enc := NewEncoder(alpha, PolicyReject)
	dec := NewDecoder(alpha)
	input := []rune("abracadabra")
	var toks []Token
	for _, s := range input {
		ts, err := enc.Encode(s)
		if err != nil {
			t.Fatalf("Encode(%q): %v", s, err)
		}
		toks = append(toks, ts...)
	}
	out, err := dec.DecodeStream(toks)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if !reflect.DeepEqual(out, input) {
		t.Fatalf("round trip: got %q, want %q", string(out), string(input))
	}
}

// TestModelMatchesReference replays random operation sequences against the
// Fenwick model and the naive list reference, comparing every emitted value
// and the full MTF order at arbitrary breakpoints, including after
// checkpoint/restore at random points. The op count is high enough to force
// repeated internal compaction of the model.
func TestModelMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alpha := mustAlphabet(t, []rune("abcdefghij")...)
	m := NewModel(alpha)
	ref := NewRefModel(alpha)

	type snap struct {
		cp   Checkpoint
		list []rune
	}
	var snaps []snap

	step := func(i int) {
		sym := rune('a' + rng.Intn(10))
		if rng.Intn(2) == 0 {
			gotM, errM := m.Encode(sym)
			gotR, errR := ref.Encode(sym)
			if errM != nil || errR != nil || gotM != gotR {
				t.Fatalf("step %d Encode(%q): model=(%d,%v) ref=(%d,%v)", i, sym, gotM, errM, gotR, errR)
			}
		} else {
			idx := rng.Intn(alpha.Len())
			gotM, errM := m.Decode(idx)
			gotR, errR := ref.Decode(idx)
			if errM != nil || errR != nil || gotM != gotR {
				t.Fatalf("step %d Decode(%d): model=(%q,%v) ref=(%q,%v)", i, idx, gotM, errM, gotR, errR)
			}
		}
		if rng.Intn(50) == 0 {
			snaps = append(snaps, snap{m.Checkpoint(), ref.Snapshot()})
		}
		if rng.Intn(97) == 0 && len(snaps) > 0 {
			s := snaps[rng.Intn(len(snaps))]
			m.Restore(s.cp)
			ref.Restore(s.list)
		}
		if rng.Intn(20) == 0 {
			if !reflect.DeepEqual(m.Order(), ref.Order()) {
				t.Fatalf("step %d: order mismatch\nmodel: %q\nref:   %q", i, string(m.Order()), string(ref.Order()))
			}
		}
	}
	for i := 0; i < 20000; i++ {
		step(i)
	}
	if !reflect.DeepEqual(m.Order(), ref.Order()) {
		t.Fatalf("final order mismatch\nmodel: %q\nref:   %q", string(m.Order()), string(ref.Order()))
	}
}

// TestBreakpointRecovery verifies that restoring any breakpoint and
// replaying the suffix reproduces the continuous run exactly.
func TestBreakpointRecovery(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	alpha := mustAlphabet(t, []rune("abcdefgh")...)
	const n = 500
	ops := make([]int, n)
	for i := range ops {
		ops[i] = rng.Intn(alpha.Len())
	}
	// Continuous reference run, recording states at every breakpoint.
	cont := NewModel(alpha)
	cps := make([]Checkpoint, n+1)
	cps[0] = cont.Checkpoint()
	want := make([]rune, n)
	for i, idx := range ops {
		s, err := cont.Decode(idx)
		if err != nil {
			t.Fatalf("continuous Decode(%d): %v", idx, err)
		}
		want[i] = s
		cps[i+1] = cont.Checkpoint()
	}
	// Recover from several arbitrary breakpoints and replay the suffix.
	for _, bp := range []int{0, 1, 7, 123, 250, 499} {
		m := NewModel(alpha)
		m.Restore(cps[bp])
		for i := bp; i < n; i++ {
			s, err := m.Decode(ops[i])
			if err != nil {
				t.Fatalf("bp=%d step=%d Decode(%d): %v", bp, i, ops[i], err)
			}
			if s != want[i] {
				t.Fatalf("bp=%d step=%d: got %q, want %q", bp, i, s, want[i])
			}
		}
		if !reflect.DeepEqual(m.Order(), cont.Order()) {
			t.Fatalf("bp=%d: recovered state diverges from continuous run", bp)
		}
	}
}

func TestIllegalIndexKeepsState(t *testing.T) {
	m := NewModel(mustAlphabet(t, 'a', 'b', 'c'))
	if _, err := m.Encode('b'); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	before := m.Order()
	for _, bad := range []int{-1, 3, 100} {
		if _, err := m.Decode(bad); err != ErrIndexOutOfRange {
			t.Fatalf("Decode(%d): got %v, want ErrIndexOutOfRange", bad, err)
		}
		if !reflect.DeepEqual(m.Order(), before) {
			t.Fatalf("Decode(%d) mutated state: %q -> %q", bad, string(before), string(m.Order()))
		}
	}
}

func TestReset(t *testing.T) {
	alpha := mustAlphabet(t, 'x', 'y', 'z')
	m := NewModel(alpha)
	for _, s := range []rune{'z', 'z', 'y', 'x', 'z'} {
		if _, err := m.Encode(s); err != nil {
			t.Fatalf("Encode: %v", err)
		}
	}
	m.Reset()
	if got, want := m.Order(), alpha.Symbols(); !reflect.DeepEqual(got, want) {
		t.Fatalf("after Reset: got %q, want %q", string(got), string(want))
	}
	idx, err := m.Encode('x')
	if err != nil || idx != 0 {
		t.Fatalf("after Reset Encode('x') = (%d,%v), want (0,nil)", idx, err)
	}
}

func TestEscapeUpdateProtocol(t *testing.T) {
	alpha := mustAlphabet(t, 'a', 'b')
	enc := NewEncoder(alpha, PolicyEscapeUpdate)
	dec := NewDecoder(alpha)
	input := []rune("abzcabz") // 'z' and 'c' are unknown at first use
	var toks []Token
	for _, s := range input {
		ts, err := enc.Encode(s)
		if err != nil {
			t.Fatalf("Encode(%q): %v", s, err)
		}
		toks = append(toks, ts...)
	}
	out, err := dec.DecodeStream(toks)
	if err != nil {
		t.Fatalf("DecodeStream: %v", err)
	}
	if !reflect.DeepEqual(out, input) {
		t.Fatalf("escape round trip: got %q, want %q", string(out), string(input))
	}
	// Both sides extended their alphabets identically and in-band.
	if enc.Model().Alphabet().Version() != dec.Model().Alphabet().Version() {
		t.Fatalf("alphabet versions diverged: enc=%d dec=%d",
			enc.Model().Alphabet().Version(), dec.Model().Alphabet().Version())
	}
	if !reflect.DeepEqual(enc.Model().Order(), dec.Model().Order()) {
		t.Fatalf("models diverged after escape protocol")
	}
	// Second occurrence of a previously unknown symbol encodes as a plain
	// index, not another escape.
	literals := 0
	for _, tok := range toks {
		if tok.Literal {
			literals++
		}
	}
	if literals != 2 {
		t.Fatalf("got %d literal tokens, want exactly 2 (one per new symbol)", literals)
	}
}

func TestPolicyRejectLeavesState(t *testing.T) {
	enc := NewEncoder(mustAlphabet(t, 'a'), PolicyReject)
	before := enc.Model().Order()
	if _, err := enc.Encode('q'); err != ErrUnknownSymbol {
		t.Fatalf("Encode unknown: got %v, want ErrUnknownSymbol", err)
	}
	if !reflect.DeepEqual(enc.Model().Order(), before) {
		t.Fatalf("rejected symbol mutated the model")
	}
}

func TestBlockRoundTrip(t *testing.T) {
	alpha := mustAlphabet(t, 'h', 'e', 'l', 'o')
	enc := NewEncoder(alpha, PolicyEscapeUpdate)
	input := []rune("hello world") // space, w, r, d unknown
	var toks []Token
	for _, s := range input {
		ts, err := enc.Encode(s)
		if err != nil {
			t.Fatalf("Encode(%q): %v", s, err)
		}
		toks = append(toks, ts...)
	}
	block, err := EncodeBlock(alpha.Version(), toks)
	if err != nil {
		t.Fatalf("EncodeBlock: %v", err)
	}
	dec := NewDecoder(alpha)
	out, err := dec.DecodeBlock(block, 1<<20)
	if err != nil {
		t.Fatalf("DecodeBlock: %v", err)
	}
	if !reflect.DeepEqual(out, input) {
		t.Fatalf("block round trip: got %q, want %q", string(out), string(input))
	}
}

func TestBlockTampering(t *testing.T) {
	alpha := mustAlphabet(t, 'a', 'b')
	enc := NewEncoder(alpha, PolicyReject)
	var toks []Token
	for _, s := range []rune("abba") {
		ts, _ := enc.Encode(s)
		toks = append(toks, ts...)
	}
	block, err := EncodeBlock(0, toks)
	if err != nil {
		t.Fatalf("EncodeBlock: %v", err)
	}
	cases := map[string]func([]byte) []byte{
		"magic":    func(b []byte) []byte { b[0] = 'X'; return b },
		"version":  func(b []byte) []byte { b[4] = 99; return b },
		"payload":  func(b []byte) []byte { b[len(b)-5] ^= 0xff; return b },
		"checksum": func(b []byte) []byte { b[len(b)-1] ^= 0xff; return b },
		"truncate": func(b []byte) []byte { return b[:len(b)-6] },
	}
	for name, corrupt := range cases {
		bad := corrupt(append([]byte(nil), block...))
		if _, err := NewDecoder(alpha).DecodeBlock(bad, 1<<20); err == nil {
			t.Fatalf("%s corruption went undetected", name)
		}
	}
}

func TestDecodeOutputQuota(t *testing.T) {
	alpha := mustAlphabet(t, 'a', 'b')
	enc := NewEncoder(alpha, PolicyReject)
	var toks []Token
	for _, s := range []rune("ababab") {
		ts, _ := enc.Encode(s)
		toks = append(toks, ts...)
	}
	block, err := EncodeBlock(0, toks)
	if err != nil {
		t.Fatalf("EncodeBlock: %v", err)
	}
	if _, err := NewDecoder(alpha).DecodeBlock(block, 3); err != ErrOutputTooLarge {
		t.Fatalf("quota: got %v, want ErrOutputTooLarge", err)
	}
	if out, err := NewDecoder(alpha).DecodeBlock(block, 6); err != nil || len(out) != 6 {
		t.Fatalf("exact quota: got (%v,%v), want 6 symbols", len(out), err)
	}
}

// TestSlotCapacityBounded ensures the logarithmic structure compacts instead
// of growing slots without bound under heavy move-to-front traffic.
func TestSlotCapacityBounded(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	const n = 64
	syms := make([]rune, n)
	for i := range syms {
		syms[i] = rune(0x4e00 + i)
	}
	m := NewModel(mustAlphabet(t, syms...))
	for i := 0; i < 100000; i++ {
		if _, err := m.Decode(rng.Intn(n)); err != nil {
			t.Fatalf("Decode: %v", err)
		}
	}
	if cap := len(m.symOf); cap > 2*n+16 {
		t.Fatalf("slot capacity %d exceeds bound %d", cap, 2*n+16)
	}
}
