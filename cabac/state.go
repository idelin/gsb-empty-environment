// Package cabac implements a context-adaptive binary arithmetic coding
// (CABAC) kernel modelled after the entropy coder of modern video coding
// standards (H.264/AVC style): every syntax element owns an independent
// context whose probability state is indexed into a finite-state table and
// is nudged up/down by each encoded bin, so the model tracks local
// statistics of the symbol stream.
package cabac

// NumStates is the number of probability states per context, as in
// H.264/AVC where pStateIdx ranges over [0, 63].
const NumStates = 64

// probLPS16[s] is the probability of the least probable symbol (LPS) for
// state s, in 1/65536 fixed point: pLPS(s) = 0.5 * 0.95^s, clamped to a
// minimum of 0.01875 (the H.264 floor), i.e. ~1229/65536.
var probLPS16 [NumStates]uint32

// nextStateLPS mirrors the H.264 transIdxLPS table: after an LPS the state
// drops sharply (probability estimate moves back towards 0.5).
var nextStateLPS = [NumStates]uint8{
	0, 0, 1, 2, 2, 4, 4, 5, 6, 7, 8, 9, 9, 11, 11, 12,
	13, 13, 15, 15, 16, 16, 18, 18, 19, 19, 21, 21, 22, 22, 23, 24,
	24, 25, 26, 26, 27, 27, 28, 29, 29, 30, 30, 30, 31, 32, 32, 33,
	33, 33, 34, 34, 35, 35, 35, 36, 36, 36, 37, 37, 37, 38, 38, 63,
}

// nextStateMPS mirrors the H.264 transIdxMPS table: after an MPS the state
// climbs by one, saturating at the most confident states.
var nextStateMPS = [NumStates]uint8{
	1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16,
	17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32,
	33, 34, 35, 36, 37, 38, 39, 40, 41, 42, 43, 44, 45, 46, 47, 48,
	49, 50, 51, 52, 53, 54, 55, 56, 57, 58, 59, 60, 61, 62, 62, 63,
}

func init() {
	p := 0.5
	for s := 0; s < NumStates; s++ {
		if p < 0.01875 {
			p = 0.01875
		}
		probLPS16[s] = uint32(p*65536.0 + 0.5)
		p *= 0.95
	}
}

// Model is the probability model interface consumed by Encoder and
// Decoder. Implementations must behave deterministically and identically
// on both sides given the same bin sequence.
type Model interface {
	// ProbLPS returns the LPS probability in 1/65536 fixed point.
	ProbLPS() uint32
	// MPS returns the value of the most probable symbol (0 or 1).
	MPS() uint32
	// Update adapts the model after coding bin.
	Update(bin uint32)
}

// Context is an adaptive CABAC context: one probability state machine per
// syntax element (or per bin position of a syntax element).
type Context struct {
	state uint8 // probability state index in [0, NumStates)
	mps   uint8 // value of the most probable symbol, 0 or 1
}

// NewContext returns a context initialised to maximum uncertainty
// (pLPS = 0.5), matching the "unknown statistics" entry point.
func NewContext() *Context { return &Context{state: 0, mps: 0} }

// ProbLPS implements Model.
func (c *Context) ProbLPS() uint32 { return probLPS16[c.state] }

// MPS implements Model.
func (c *Context) MPS() uint32 { return uint32(c.mps) }

// Update implements Model, driving the state transition tables.
func (c *Context) Update(bin uint32) {
	if uint8(bin) == c.mps {
		c.state = nextStateMPS[c.state]
	} else {
		c.state = nextStateLPS[c.state]
		if c.state == 0 {
			// LPS observed at the maximally uncertain state: swap MPS.
			c.mps = 1 - c.mps
		}
	}
}

// State exposes the current probability state index (for inspection/tests).
func (c *Context) State() int { return int(c.state) }

// ContextSet is a pool of independent adaptive contexts, one per syntax
// element or bin position, addressed by a context index.
type ContextSet struct {
	ctxs []Context
}

// NewContextSet allocates n independent contexts.
func NewContextSet(n int) *ContextSet { return &ContextSet{ctxs: make([]Context, n)} }

// At returns the context for the given index.
func (s *ContextSet) At(i int) *Context { return &s.ctxs[i] }

// Len returns the number of contexts in the set.
func (s *ContextSet) Len() int { return len(s.ctxs) }

// StaticModel is a non-adaptive, context-free probability model with a
// fixed bin probability. It serves as the baseline the adaptive kernel is
// benchmarked against.
type StaticModel struct {
	probLPS uint32
	mps     uint32
}

// NewStaticModel builds a static model where bin value mps (0 or 1) has
// probability 1-p, and the other value has probability p (0 < p < 1).
func NewStaticModel(mps uint32, p float64) *StaticModel {
	if p <= 0 {
		p = 1.0 / 65536.0
	}
	if p >= 0.5 {
		p = 0.5
	}
	return &StaticModel{probLPS: uint32(p*65536.0 + 0.5), mps: mps}
}

// ProbLPS implements Model.
func (m *StaticModel) ProbLPS() uint32 { return m.probLPS }

// MPS implements Model.
func (m *StaticModel) MPS() uint32 { return m.mps }

// Update implements Model; a static model never adapts.
func (m *StaticModel) Update(bin uint32) {}
