package snaprecv

import (
	"bytes"
	"crypto/sha256"
	"math/rand"
	"os"
	"testing"
)

// model is a byte-level reference implementation used to differentially
// check Receiver chunk by chunk.
type model struct {
	sizes    map[uint64]int
	contents map[uint64][]byte

	installed uint64
	visible   []byte // content installed by the last completed version
	staged    uint64
	total     uint64
	known     []bool // verified byte map of the staged version
	stagedBuf []byte // verified bytes of the staged version
}

func newModel() *model {
	return &model{sizes: map[uint64]int{}, contents: map[uint64][]byte{}}
}

// sizeOf derives each version's size deterministically; never a multiple
// of the 64-byte window cap, so every version has a short tail chunk.
func (m *model) sizeOf(v uint64) int {
	if s, ok := m.sizes[v]; ok {
		return s
	}
	s := 100 + int(v%7)*37 + int(v*v)%13
	m.sizes[v] = s
	return s
}

func (m *model) contentOf(v uint64) []byte {
	if c, ok := m.contents[v]; ok {
		return c
	}
	c := content(v, m.sizeOf(v))
	m.contents[v] = c
	return c
}

// doomed reports whether the staged version can never complete because
// some verified byte disagrees with the true content (sender corruption).
func (m *model) doomed() bool {
	if m.staged == 0 || m.known == nil {
		return false
	}
	want := m.contentOf(m.staged)
	for off, k := range m.known {
		if k && m.stagedBuf[off] != want[off] {
			return true
		}
	}
	return false
}

func (m *model) reset(v uint64) {
	m.staged = v
	m.total = 0
	m.known = nil
	m.stagedBuf = nil
}

// apply mirrors Receiver.HandleChunk's decision order and returns the
// error the receiver is expected to produce.
func (m *model) apply(c Chunk) error {
	if c.Hash != sha256.Sum256(c.Data) {
		return ErrHashMismatch
	}
	if c.Version <= m.installed || (m.staged != 0 && c.Version < m.staged) {
		return ErrStaleVersion
	}
	if c.Version > m.staged {
		m.reset(c.Version)
	}
	if len(c.Data) == 0 || c.TotalSize == 0 ||
		c.Offset > c.TotalSize || uint64(len(c.Data)) > c.TotalSize-c.Offset {
		return ErrInvalidChunk
	}
	if m.total != 0 && c.TotalSize != m.total {
		return ErrConflict
	}
	m.total = c.TotalSize
	if m.known == nil {
		m.known = make([]bool, m.total)
		m.stagedBuf = make([]byte, m.total)
	}
	for i := range c.Data {
		off := c.Offset + uint64(i)
		if m.known[off] && m.stagedBuf[off] != c.Data[i] {
			return ErrConflict
		}
	}
	for i := range c.Data {
		off := c.Offset + uint64(i)
		m.known[off] = true
		m.stagedBuf[off] = c.Data[i]
	}
	full := true
	for _, k := range m.known {
		if !k {
			full = false
			break
		}
	}
	if full {
		m.installed = c.Version
		m.visible = append([]byte(nil), m.stagedBuf...)
		m.staged = 0
		m.total = 0
		m.known = nil
		m.stagedBuf = nil
	}
	return nil
}

func (m *model) knownCount() (n uint64) {
	for _, k := range m.known {
		if k {
			n++
		}
	}
	return
}

func verifiedCount(r *Receiver) (n uint64) {
	for _, iv := range r.intervals {
		n += iv.Len
	}
	return
}

// TestChunkLevelModel drives the receiver with a randomized op stream —
// valid/duplicate/conflicting/corrupt/out-of-bounds/stale chunks plus
// crashes — and checks the receiver against the model after every op.
func TestChunkLevelModel(t *testing.T) {
	dir := tempDir(t)
	defer os.RemoveAll(dir)

	rng := rand.New(rand.NewSource(20261006))
	m := newModel()
	r, err := Open(Options{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}

	check := func(op int, what string) {
		t.Helper()
		if r.installed != m.installed || r.staged != m.staged {
			t.Fatalf("op %d (%s): receiver installed/staged=%d/%d, model=%d/%d",
				op, what, r.installed, r.staged, m.installed, m.staged)
		}
		if got, want := verifiedCount(r), m.knownCount(); got != want {
			t.Fatalf("op %d (%s): verified bytes=%d, model=%d", op, what, got, want)
		}
		b, v, err := r.Read()
		if m.installed == 0 {
			if err != ErrNoSnapshot {
				t.Fatalf("op %d (%s): read err=%v, want ErrNoSnapshot", op, what, err)
			}
			return
		}
		if err != nil || v != m.installed || !bytes.Equal(b, m.visible) {
			t.Fatalf("op %d (%s): visible v=%d err=%v, want v=%d", op, what, v, err, m.installed)
		}
	}

	pickVersion := func() uint64 {
		// Sometimes abandon a doomed (poisoned) version, but usually let
		// it finish: a lying sender's snapshot still installs.
		if m.doomed() && rng.Intn(10) < 3 {
			return m.staged + 1
		}
		if rng.Intn(10) < 9 {
			if m.staged != 0 {
				return m.staged
			}
			return m.installed + 1
		}
		return 1 + uint64(rng.Intn(int(m.installed)+2))
	}

	window := func(v uint64) (off uint64, data []byte) {
		size := m.sizeOf(v)
		o := rng.Intn(size)
		maxLen := size - o
		if maxLen > 64 {
			maxLen = 64
		}
		n := 1 + rng.Intn(maxLen)
		if maxLen > 16 {
			// Bias toward larger windows so coverage converges.
			n = 16 + rng.Intn(maxLen-15)
		}
		return uint64(o), m.contentOf(v)[o : o+n]
	}

	var history []Chunk // accepted chunks, for retransmission ops

	const ops = 6000
	installs := 0
	for i := 0; i < ops; i++ {
		kind := rng.Intn(100)
		var c Chunk
		var what string
		crash := false

		switch {
		case kind < 56: // valid chunk, random window (may overlap)
			v := pickVersion()
			off, data := window(v)
			c = NewChunk(v, off, uint64(m.sizeOf(v)), data)
			what = "valid"

		case kind < 60: // conflicting bytes over verified data -> ErrConflict
			if m.staged == 0 || m.knownCount() == 0 {
				continue
			}
			v := m.staged
			size := m.sizeOf(v)
			var o int
			for {
				o = rng.Intn(size)
				if m.known[o] {
					break
				}
			}
			n := 1 + rng.Intn(8)
			if o+n > size {
				n = size - o
			}
			cp := append([]byte(nil), m.contentOf(v)[o:o+n]...)
			cp[0] ^= 0xff // position o is verified: guaranteed conflict
			c = NewChunk(v, uint64(o), uint64(size), cp)
			what = "conflict"

		case kind < 62: // wrong bytes over unverified data (poisons staging)
			v := pickVersion()
			off, data := window(v)
			cp := append([]byte(nil), data...)
			cp[rng.Intn(len(cp))] ^= 0xff
			c = NewChunk(v, off, uint64(m.sizeOf(v)), cp)
			what = "poison"

		case kind < 67: // corrupt hash
			v := pickVersion()
			off, data := window(v)
			c = NewChunk(v, off, uint64(m.sizeOf(v)), data)
			c.Hash[0] ^= 0xff
			what = "badhash"

		case kind < 72: // out-of-bounds tail (offset+len > total)
			v := m.staged
			if v == 0 {
				v = m.installed + 1
			}
			size := uint64(m.sizeOf(v))
			off := size - uint64(rng.Intn(int(size)/2+1))
			n := size - off + 1 + uint64(rng.Intn(4))
			c = NewChunk(v, off, size, make([]byte, n))
			what = "overflow"

		case kind < 76: // lying total size for the staged version
			if m.staged == 0 || m.total == 0 {
				continue
			}
			v := m.staged
			off, data := window(v)
			c = NewChunk(v, off, m.total+1+uint64(rng.Intn(50)), data)
			what = "totallie"

		case kind < 82: // stale version chunk
			if m.installed == 0 {
				continue
			}
			v := 1 + uint64(rng.Intn(int(m.installed)))
			off, data := window(v)
			c = NewChunk(v, off, uint64(m.sizeOf(v)), data)
			what = "stale"

		case kind < 87: // retransmission of a previously accepted chunk
			if len(history) == 0 {
				continue
			}
			c = history[rng.Intn(len(history))]
			what = "dup"

		case kind < 93: // crash and recover
			r.Close()
			r, err = Open(Options{Dir: dir})
			if err != nil {
				t.Fatalf("op %d: reopen: %v", i, err)
			}
			crash = true
			what = "crash"

		default: // empty chunk
			v := pickVersion()
			c = NewChunk(v, 0, uint64(m.sizeOf(v)), nil)
			what = "empty"
		}

		if crash {
			check(i, what)
			continue
		}

		before := m.installed
		wantErr := m.apply(c)
		if gotErr := r.HandleChunk(c); gotErr != wantErr {
			t.Fatalf("op %d (%s): receiver err=%v, model=%v, chunk=%+v",
				i, what, gotErr, wantErr, c)
		}
		if wantErr == nil {
			history = append(history, c)
			if m.installed != before {
				installs++
			}
		}
		check(i, what)
	}

	if installs < 2 {
		t.Fatalf("model run installed %d snapshots; adjust op mix", installs)
	}
	t.Logf("ops=%d installs=%d final installed=%d", ops, installs, m.installed)
}
