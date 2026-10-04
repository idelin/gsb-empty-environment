// Package blockmatch implements content-defined block matching between an
// "old" and a "new" byte stream. A rolling Rabin fingerprint (weak hash)
// proposes candidate blocks from an index; every candidate is then confirmed
// with a strong digest (SHA-256) AND a byte-for-byte content comparison
// before it may be emitted as a match. A fingerprint collision therefore
// only ever yields a candidate, never a reusable match.
package blockmatch

// Rabin fingerprint modulo an irreducible polynomial of degree 53.
const polyDeg = 53
const poly uint64 = 0x3DA3358B4DC173

var modTab = buildModTab()

// buildModTab precomputes (v * x^polyDeg) mod poly for v in [0,256), used to
// reduce a shifted fingerprint back below the polynomial degree.
func buildModTab() [256]uint64 {
	var t [256]uint64
	for i := 0; i < 256; i++ {
		v := uint64(i) << polyDeg
		for j := 7; j >= 0; j-- {
			if v&(uint64(1)<<uint(polyDeg+j)) != 0 {
				v ^= poly << uint(j)
			}
		}
		t[i] = v
	}
	return t
}

func reduce(h uint64) uint64 {
	return (h & (uint64(1)<<polyDeg - 1)) ^ modTab[h>>polyDeg]
}

// buildOutTab precomputes the contribution b * x^(8*(size-1)) mod poly of the
// byte that is about to slide out of a window of the given size.
func buildOutTab(size int) [256]uint64 {
	var t [256]uint64
	shift := 8 * (size - 1)
	for b := 0; b < 256; b++ {
		v := uint64(b)
		for i := 0; i < shift; i++ {
			v <<= 1
			if v&(uint64(1)<<polyDeg) != 0 {
				v ^= poly
			}
		}
		t[b] = v
	}
	return t
}

// Roller is a rolling Rabin fingerprint over a sliding window of size bytes.
type Roller struct {
	size   int
	window []byte // circular buffer
	wpos   int    // position of the oldest byte (next to be overwritten)
	hash   uint64
	outTab [256]uint64
}

// NewRoller returns a Roller for the given window (block) size.
func NewRoller(size int) *Roller {
	if size <= 0 {
		panic("blockmatch: window size must be positive")
	}
	return &Roller{size: size, window: make([]byte, size), outTab: buildOutTab(size)}
}

// Roll slides the window by one byte and returns the new fingerprint.
func (r *Roller) Roll(b byte) uint64 {
	out := r.window[r.wpos]
	r.window[r.wpos] = b
	r.wpos++
	if r.wpos == r.size {
		r.wpos = 0
	}
	r.hash = reduce(((r.hash ^ r.outTab[out]) << 8) | uint64(b))
	return r.hash
}

// Sum returns the current fingerprint without advancing the window.
func (r *Roller) Sum() uint64 { return r.hash }

// Size returns the window size.
func (r *Roller) Size() int { return r.size }

// Reset clears the window and switches to the given block size, recomputing
// tables when the size actually changes.
func (r *Roller) Reset(size int) {
	if size <= 0 {
		panic("blockmatch: window size must be positive")
	}
	if size != r.size {
		r.size = size
		r.window = make([]byte, size)
		r.outTab = buildOutTab(size)
	} else {
		for i := range r.window {
			r.window[i] = 0
		}
	}
	r.wpos = 0
	r.hash = 0
}

// Bytes returns the window content in chronological order (oldest first).
func (r *Roller) Bytes() []byte {
	buf := make([]byte, r.size)
	for i := 0; i < r.size; i++ {
		buf[i] = r.window[(r.wpos+i)%r.size]
	}
	return buf
}

// RollerState is a serializable snapshot of a Roller for pause/resume.
type RollerState struct {
	Size   int
	Window []byte
	Wpos   int
	Hash   uint64
}

// Snapshot captures the Roller state.
func (r *Roller) Snapshot() RollerState {
	w := make([]byte, len(r.window))
	copy(w, r.window)
	return RollerState{Size: r.size, Window: w, Wpos: r.wpos, Hash: r.hash}
}

// RestoreRoller rebuilds a Roller from a snapshot.
func RestoreRoller(st RollerState) *Roller {
	r := &Roller{size: st.Size, window: make([]byte, st.Size), outTab: buildOutTab(st.Size)}
	copy(r.window, st.Window)
	r.wpos = st.Wpos
	r.hash = st.Hash
	return r
}

// Fingerprint computes the Rabin fingerprint of a single block.
func Fingerprint(data []byte) uint64 {
	r := NewRoller(len(data))
	for _, b := range data {
		r.Roll(b)
	}
	return r.Sum()
}
