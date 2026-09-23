package freq

import (
	"math"
	"testing"
)

func TestDeterministicEntropyZero(t *testing.T) {
	m := NewModel(256)
	for i := 0; i < 1000; i++ {
		m.Add('a')
	}
	if got := m.Entropy(); got != 0 {
		t.Fatalf("deterministic sequence entropy = %v, want 0", got)
	}
}

func TestRetractMissingRejected(t *testing.T) {
	m := NewModel(256)
	if err := m.Retract('x'); err != ErrNotPresent {
		t.Fatalf("retract of never-added symbol: err = %v, want ErrNotPresent", err)
	}
	m.Add('x')
	if err := m.Retract('x'); err != nil {
		t.Fatalf("retract of present symbol: %v", err)
	}
	if err := m.Retract('x'); err != ErrNotPresent {
		t.Fatalf("double retract: err = %v, want ErrNotPresent", err)
	}
	if m.Total() != 0 {
		t.Fatalf("total after full retraction = %d, want 0", m.Total())
	}
}

func TestConservation(t *testing.T) {
	m := NewModel(256)
	const adds = 500
	for i := 0; i < adds; i++ {
		m.Add(byte(i % 7))
	}
	retracted := 0
	for i := 0; i < 100; i++ {
		if err := m.Retract(byte(i % 7)); err != nil {
			t.Fatalf("retract: %v", err)
		}
		retracted++
	}
	if got, want := m.Total(), uint64(adds-retracted); got != want {
		t.Fatalf("total = %d, want %d (adds-retracts)", got, want)
	}
	var sum uint64
	for _, s := range m.Symbols() {
		sum += m.Count(s)
	}
	if sum != m.Total() {
		t.Fatalf("sum of counts = %d, total = %d: not conserved", sum, m.Total())
	}
}

// TestIncrementalMatchesRecompute feeds a random add/retract script and
// checks the surviving multiset against a full recompute from scratch.
func TestIncrementalMatchesRecompute(t *testing.T) {
	rng := uint64(12345)
	next := func(n uint64) uint64 {
		rng = rng*6364136223846793005 + 1442695040888963407
		return (rng >> 33) % n
	}
	m := NewModel(256)
	var live []byte
	for step := 0; step < 5000; step++ {
		if len(live) == 0 || next(3) != 0 {
			s := byte(next(11))
			m.Add(s)
			live = append(live, s)
		} else {
			i := int(next(uint64(len(live))))
			if err := m.Retract(live[i]); err != nil {
				t.Fatalf("step %d: retract of live sample failed: %v", step, err)
			}
			live = append(live[:i], live[i+1:]...)
		}
	}
	// Full recompute from the surviving records.
	re := NewModel(256)
	for _, s := range live {
		re.Add(s)
	}
	if m.Total() != re.Total() || m.Total() != uint64(len(live)) {
		t.Fatalf("totals differ: incremental %d, recompute %d, records %d",
			m.Total(), re.Total(), len(live))
	}
	for s := 0; s < 256; s++ {
		if m.Count(byte(s)) != re.Count(byte(s)) {
			t.Fatalf("count[%d]: incremental %d != recompute %d", s, m.Count(byte(s)), re.Count(byte(s)))
		}
	}
	if m.Entropy() != re.Entropy() {
		t.Fatalf("entropy: incremental %v != recompute %v", m.Entropy(), re.Entropy())
	}
}

func TestNoLog0AndSmoothedEscape(t *testing.T) {
	m := NewModel(4)
	m.Add(0)
	m.Add(0)
	m.Add(1)
	// Symbols 2 and 3 have zero frequency: entropy must be finite and
	// equal to the sum over nonzero symbols only.
	want := -(2.0/3.0)*math.Log2(2.0/3.0) - (1.0/3.0)*math.Log2(1.0/3.0)
	if got := m.Entropy(); math.Abs(got-want) > 1e-12 {
		t.Fatalf("entropy = %v, want %v", got, want)
	}
	// Explicit escape: zero-frequency symbols still get probability > 0.
	if p := m.Prob(2); p <= 0 {
		t.Fatalf("smoothed prob of unseen symbol = %v, want > 0", p)
	}
	sum := 0.0
	for s := 0; s < 4; s++ {
		sum += m.Prob(byte(s))
	}
	if math.Abs(sum-1) > 1e-12 {
		t.Fatalf("smoothed probabilities sum to %v, want 1", sum)
	}
}

func TestEntropyIsEstimateNotByteLength(t *testing.T) {
	// Entropy is a float estimate; it is not a byte count. A
	// deterministic stream has entropy exactly 0 yet obviously cannot
	// be stored in 0 bytes (the codec proves the real length).
	m := NewModel(256)
	for i := 0; i < 64; i++ {
		m.Add('z')
	}
	if m.Entropy() != 0 {
		t.Fatalf("entropy = %v, want 0", m.Entropy())
	}
	if m.CodeLength() <= 0 {
		t.Fatalf("smoothed code length estimate = %v, want > 0 (smoothing costs bits)", m.CodeLength())
	}
}
