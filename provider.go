package netsec

import (
	"crypto/rand"
	"fmt"
	"io"
	"sync"
)

// TripleProvider is an offline Beaver-triple source.
type TripleProvider interface {
	Triple(id string) (Triple, error)
}

// HonestProvider generates triples that satisfy c = a*b.
type HonestProvider struct {
	field Field
	rng   io.Reader
	mu    sync.Mutex
}

// NewHonestProvider constructs an honest fixture for the given prime.
func NewHonestProvider(p uint32, rng io.Reader) (*HonestProvider, error) {
	f, err := NewField(p)
	if err != nil {
		return nil, err
	}
	if rng == nil {
		rng = rand.Reader
	}
	return &HonestProvider{field: f, rng: rng}, nil
}

// Triple generates one additive-shared honest Beaver triple.
func (p *HonestProvider) Triple(id string) (Triple, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if id == "" {
		return Triple{}, fmt.Errorf("%w: empty ID", ErrTripleID)
	}
	a, err := p.field.Random(p.rng)
	if err != nil {
		return Triple{}, err
	}
	b, err := p.field.Random(p.rng)
	if err != nil {
		return Triple{}, err
	}
	c := p.field.Mul(a, b)
	aShares, err := p.field.SplitValue(a, p.rng)
	if err != nil {
		return Triple{}, err
	}
	bShares, err := p.field.SplitValue(b, p.rng)
	if err != nil {
		return Triple{}, err
	}
	cShares, err := p.field.SplitValue(c, p.rng)
	if err != nil {
		return Triple{}, err
	}
	t := Triple{ID: id, A: aShares, B: bShares, C: cShares}
	if err := p.field.ValidateTriple(t); err != nil {
		return Triple{}, err
	}
	return t, nil
}

// MaliciousProvider applies a caller-supplied mutation to honest triples. The
// fixture makes no attempt to hide the mutation from the sacrifice check.
type MaliciousProvider struct {
	Base   TripleProvider
	Mutate func(*Triple)
}

// NewMaliciousProvider wraps a provider. If mutate is nil the provider is
// effectively honest.
func NewMaliciousProvider(base TripleProvider, mutate func(*Triple)) *MaliciousProvider {
	return &MaliciousProvider{Base: base, Mutate: mutate}
}

// Triple obtains a base triple and applies the malicious mutation.
func (p *MaliciousProvider) Triple(id string) (Triple, error) {
	if p == nil || p.Base == nil {
		return Triple{}, fmt.Errorf("%w: nil base provider", ErrInvalidTriple)
	}
	t, err := p.Base.Triple(id)
	if err != nil {
		return Triple{}, err
	}
	if p.Mutate != nil {
		p.Mutate(&t)
	}
	return t, nil
}

// CorruptC returns a mutation that adds delta mod p to party's c share while
// leaving a and b correct. The malformed triple still has c != a*b in general.
func CorruptC(f Field, party int, delta uint32) func(*Triple) {
	return func(t *Triple) {
		if party < 0 || party > 2 {
			return
		}
		t.C[party] = f.Add(t.C[party], delta)
	}
}

// CorruptShare returns a mutation that changes one component share. component
// is 'a', 'b', or 'c'; party is 0..2.
func CorruptShare(f Field, component byte, party int, delta uint32) func(*Triple) {
	return func(t *Triple) {
		if party < 0 || party > 2 {
			return
		}
		switch component {
		case 'a', 'A':
			t.A[party] = f.Add(t.A[party], delta)
		case 'b', 'B':
			t.B[party] = f.Add(t.B[party], delta)
		case 'c', 'C':
			t.C[party] = f.Add(t.C[party], delta)
		}
	}
}

// NewTriplePair obtains two distinct-ID triples from a provider.
func NewTriplePair(provider TripleProvider, candidateID, checkID string) (Triple, Triple, error) {
	if candidateID == checkID {
		return Triple{}, Triple{}, fmt.Errorf("%w: IDs equal", ErrTripleID)
	}
	candidate, err := provider.Triple(candidateID)
	if err != nil {
		return Triple{}, Triple{}, err
	}
	check, err := provider.Triple(checkID)
	if err != nil {
		return Triple{}, Triple{}, err
	}
	return candidate, check, nil
}
