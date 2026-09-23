package mtf

import (
	"bytes"
	"math/rand"
	"testing"
)

// refMTF is an independent, deliberately naive list-based MTF used as the
// reference oracle. It moves elements by linearly shifting the slice.
type refMTF struct {
	order []byte
}

func newRefMTF(order []byte) *refMTF {
	return &refMTF{order: append([]byte(nil), order...)}
}

func (r *refMTF) encode(s byte) (int, bool) {
	for i, v := range r.order {
		if v == s {
			copy(r.order[1:i+1], r.order[:i])
			r.order[0] = s
			return i, true
		}
	}
	return -1, false
}

func (r *refMTF) decode(i int) (byte, bool) {
	if i < 0 || i >= len(r.order) {
		return 0, false
	}
	s := r.order[i]
	copy(r.order[1:i+1], r.order[:i])
	r.order[0] = s
	return s, true
}

// TestPropertyAgainstReference drives the treap model and the reference
// list through identical random operation sequences, including resets and
// checkpoint/restore at arbitrary breakpoints, and compares the full state
// after every single operation.
func TestPropertyAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(20260923))
	for trial := 0; trial < 60; trial++ {
		n := 1 + rng.Intn(80)
		pool := rng.Perm(256)[:n]
		symbols := make([]byte, n)
		for i, v := range pool {
			symbols[i] = byte(v)
		}
		alpha, err := NewAlphabet(symbols)
		if err != nil {
			t.Fatal(err)
		}
		m := NewModel(alpha)
		ref := newRefMTF(symbols)

		for step := 0; step < 400; step++ {
			switch rng.Intn(7) {
			case 0, 1, 2, 3: // encode a known symbol
				s := symbols[rng.Intn(n)]
				idx, err := m.EncodeIndex(s)
				ridx, ok := ref.encode(s)
				if err != nil || !ok || idx != ridx {
					t.Fatalf("trial %d step %d: encode(%#02x) = %d,%v; ref = %d,%v",
						trial, step, s, idx, err, ridx, ok)
				}
			case 4: // decode an index, sometimes out of range
				i := rng.Intn(n+2) - 1
				got, err := m.DecodeIndex(i)
				want, ok := ref.decode(i)
				if (err == nil) != ok {
					t.Fatalf("trial %d step %d: decode(%d) err=%v; ref ok=%v", trial, step, i, err, ok)
				}
				if ok && got != want {
					t.Fatalf("trial %d step %d: decode(%d) = %#02x; ref = %#02x", trial, step, i, got, want)
				}
			case 5: // reset both models
				m.Reset()
				ref = newRefMTF(alpha.Symbols())
			case 6: // checkpoint and continue from the restored copy
				cp := m.Checkpoint()
				m2, err := Restore(alpha, cp)
				if err != nil {
					t.Fatalf("trial %d step %d: Restore: %v", trial, step, err)
				}
				m = m2
			}
			if got, want := m.Order(), ref.order; !bytes.Equal(got, want) {
				t.Fatalf("trial %d step %d: order diverged\ngot  %q\nwant %q", trial, step, got, want)
			}
		}
	}
}

// TestRestoreMatchesContinuous verifies that a model restored from a
// checkpoint produces exactly the same results as uninterrupted processing.
func TestRestoreMatchesContinuous(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	symbols := []byte("abcdefghij")
	input := make([]byte, 2000)
	for i := range input {
		input[i] = symbols[rng.Intn(len(symbols))]
	}

	continuous := NewModel(mustAlphabet(t, string(symbols)))
	restored := NewModel(mustAlphabet(t, string(symbols)))
	breakpoint := 1000

	for i, s := range input {
		if i == breakpoint {
			cp := restored.Checkpoint()
			m2, err := Restore(mustAlphabet(t, string(symbols)), cp)
			if err != nil {
				t.Fatal(err)
			}
			restored = m2
		}
		i1, err1 := continuous.EncodeIndex(s)
		i2, err2 := restored.EncodeIndex(s)
		if err1 != nil || err2 != nil || i1 != i2 {
			t.Fatalf("step %d: continuous=%d,%v restored=%d,%v", i, i1, err1, i2, err2)
		}
	}
	if !bytes.Equal(continuous.Order(), restored.Order()) {
		t.Fatal("final states diverged")
	}
}

// TestBlockCheckpointBinding simulates block-based transfer: the encoder
// checkpoints at every block boundary, and the decoder restores from those
// checkpoints instead of ever seeing the encoder's live model. Escapes
// extend the alphabet mid-stream, and both sides must stay consistent.
func TestBlockCheckpointBinding(t *testing.T) {
	rng := rand.New(rand.NewSource(99))
	base := []byte("abcdef")
	// Input mixes known and unknown symbols so escapes fire.
	pool := []byte("abcdefXYZQ")
	input := make([]byte, 500)
	for i := range input {
		input[i] = pool[rng.Intn(len(pool))]
	}

	encAlpha, _ := NewAlphabet(base)
	encModel := NewModel(encAlpha)
	decAlpha, _ := NewAlphabet(base)

	const blockSize = 37
	for start := 0; start < len(input); start += blockSize {
		end := start + blockSize
		if end > len(input) {
			end = len(input)
		}
		// Block boundary: checkpoint binds MTF state + alphabet version.
		cp := encModel.Checkpoint()
		enc := NewBlockEncoder(encModel, EscapeUnknown)
		for _, s := range input[start:end] {
			if err := enc.Write(s); err != nil {
				t.Fatal(err)
			}
		}
		decModel, err := Restore(decAlpha, cp)
		if err != nil {
			t.Fatalf("Restore at block %d: %v", start, err)
		}
		out, err := NewBlockDecoder(decModel).Decode(enc.Bytes(), blockSize)
		if err != nil {
			t.Fatalf("Decode at block %d: %v", start, err)
		}
		if !bytes.Equal(out, input[start:end]) {
			t.Fatalf("block %d: got %q want %q", start, out, input[start:end])
		}
		if !bytes.Equal(encModel.Order(), decModel.Order()) {
			t.Fatalf("block %d: encoder/decoder states diverged", start)
		}
		if encModel.Alphabet().Version() != decModel.Alphabet().Version() {
			t.Fatalf("block %d: alphabet versions diverged", start)
		}
	}
}

// TestEscapePropertyRoundTrip cross-checks the escape stream against the
// reference list over random inputs with a tiny alphabet.
func TestEscapePropertyRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(4242))
	for trial := 0; trial < 40; trial++ {
		base := []byte{'a', 'b', 'c'}
		encModel := NewModel(mustAlphabet(t, string(base)))
		enc := NewBlockEncoder(encModel, EscapeUnknown)
		ref := newRefMTF(base)

		input := make([]byte, 300)
		for i := range input {
			input[i] = byte('a' + rng.Intn(8)) // 'a'..'h', alphabet only has a-c
		}
		for _, s := range input {
			if err := enc.Write(s); err != nil {
				t.Fatal(err)
			}
			if _, ok := ref.encode(s); !ok {
				ref.order = append(ref.order, s)
				ridx, _ := ref.encode(s)
				_ = ridx
			}
		}
		if got := encModel.Order(); !bytes.Equal(got, ref.order) {
			t.Fatalf("trial %d: encoder order %q != ref %q", trial, got, ref.order)
		}
		out, err := NewBlockDecoder(NewModel(mustAlphabet(t, string(base)))).Decode(enc.Bytes(), len(input))
		if err != nil {
			t.Fatalf("trial %d: Decode: %v", trial, err)
		}
		if !bytes.Equal(out, input) {
			t.Fatalf("trial %d: round trip mismatch", trial)
		}
	}
}
