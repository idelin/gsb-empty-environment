package bitmap

import (
	"math/rand"
	"testing"
)

// reference is a naive map-based model used to validate the bitmap.
type reference map[uint32]struct{}

func (r reference) add(id uint32)    { r[id] = struct{}{} }
func (r reference) remove(id uint32) { delete(r, id) }
func (r reference) contains(id uint32) bool {
	_, ok := r[id]
	return ok
}

func randomReference(rng *rand.Rand, space uint32, n int) reference {
	ref := reference{}
	for len(ref) < n {
		ref.add(uint32(rng.Intn(int(space))))
	}
	return ref
}

func bitmapFromReference(ref reference) *Bitmap {
	b := New()
	for id := range ref {
		b.Add(id)
	}
	return b
}

// TestAgainstReferenceModel hammers a single bitmap with random mixed
// operations (sparse adds, dense range adds, removes, lookups) and checks
// every observable state against a naive map.
func TestAgainstReferenceModel(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	b := New()
	ref := reference{}
	const space = 1 << 22
	for step := 0; step < 4000; step++ {
		switch rng.Intn(5) {
		case 0, 1: // sparse add
			id := uint32(rng.Intn(space))
			if got, want := b.Add(id), !ref.contains(id); got != want {
				t.Fatalf("step %d: Add(%d) = %v, want %v", step, id, got, want)
			}
			ref.add(id)
		case 2: // dense range add
			lo := uint64(rng.Intn(space - 1))
			hi := lo + uint64(rng.Intn(1+rng.Intn(150000)))
			b.AddRange(lo, hi)
			for k := lo; k < hi; k++ {
				ref.add(uint32(k))
			}
		case 3: // remove
			id := uint32(rng.Intn(space))
			if got, want := b.Remove(id), ref.contains(id); got != want {
				t.Fatalf("step %d: Remove(%d) = %v, want %v", step, id, got, want)
			}
			ref.remove(id)
		case 4: // point query
			id := uint32(rng.Intn(space))
			if got, want := b.Contains(id), ref.contains(id); got != want {
				t.Fatalf("step %d: Contains(%d) = %v, want %v", step, id, got, want)
			}
		}
	}
	if got, want := b.Cardinality(), uint64(len(ref)); got != want {
		t.Fatalf("cardinality = %d, want %d", got, want)
	}
	for id := range ref {
		if !b.Contains(id) {
			t.Fatalf("final check: missing %d", id)
		}
	}
}

// TestBatchOpsAgainstReference validates And/Or/Xor/AndMany/OrMany against
// set operations on the naive model, exercising every container pairing
// (array/bitmap/run operands are produced by the mixed loads).
func TestBatchOpsAgainstReference(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	const space = 1 << 20

	build := func() (*Bitmap, reference) {
		b := New()
		ref := reference{}
		// Sparse ids -> array containers.
		for i := 0; i < 3000; i++ {
			id := uint32(rng.Intn(space))
			b.Add(id)
			ref.add(id)
		}
		// Dense ranges -> run containers.
		for i := 0; i < 4; i++ {
			lo := uint64(rng.Intn(space - 70000))
			hi := lo + uint64(30000+rng.Intn(40000))
			b.AddRange(lo, hi)
			for k := lo; k < hi; k++ {
				ref.add(uint32(k))
			}
		}
		// Heavy random fill in one region -> bitmap containers.
		base := uint32(rng.Intn(space-65536)) &^ 0xFFFF
		for i := 0; i < 20000; i++ {
			id := base + uint32(rng.Intn(65536))
			b.Add(id)
			ref.add(id)
		}
		return b, ref
	}

	a, ra := build()
	b, rb := build()
	c, rc := build()

	andRef := reference{}
	for id := range ra {
		if rb.contains(id) {
			andRef.add(id)
		}
	}
	orRef := reference{}
	for id := range ra {
		orRef.add(id)
	}
	for id := range rb {
		orRef.add(id)
	}
	xorRef := reference{}
	for id := range orRef {
		if ra.contains(id) != rb.contains(id) {
			xorRef.add(id)
		}
	}
	andManyRef := reference{}
	for id := range ra {
		if rb.contains(id) && rc.contains(id) {
			andManyRef.add(id)
		}
	}
	orManyRef := reference{}
	for id := range ra {
		orManyRef.add(id)
	}
	for id := range rb {
		orManyRef.add(id)
	}
	for id := range rc {
		orManyRef.add(id)
	}

	check := func(name string, got *Bitmap, want reference) {
		t.Helper()
		if got.Cardinality() != uint64(len(want)) {
			t.Fatalf("%s: cardinality = %d, want %d", name, got.Cardinality(), len(want))
		}
		for id := range want {
			if !got.Contains(id) {
				t.Fatalf("%s: missing %d", name, id)
			}
		}
	}

	check("And", And(a, b), andRef)
	check("Or", Or(a, b), orRef)
	check("Xor", Xor(a, b), xorRef)
	check("AndMany", AndMany(a, b, c), andManyRef)
	check("OrMany", OrMany(a, b, c), orManyRef)

	// Identity / algebraic laws.
	check("And(a,a)=a", And(a, a), ra)
	check("Or(a,a)=a", Or(a, a), ra)
	check("Xor(a,a)=empty", Xor(a, a), reference{})
	check("And(a,empty)=empty", And(a, New()), reference{})
	check("Or(a,empty)=a", Or(a, New()), ra)
	check("Xor(a,empty)=a", Xor(a, New()), ra)
}

// TestBatchOpsDoNotMutateOperands ensures batch results share no memory
// with their operands.
func TestBatchOpsDoNotMutateOperands(t *testing.T) {
	a := New()
	b := New()
	a.AddRange(0, 100000)
	b.AddRange(50000, 150000)
	ca, cb := a.Cardinality(), b.Cardinality()
	_ = And(a, b)
	_ = Or(a, b)
	_ = Xor(a, b)
	if a.Cardinality() != ca || b.Cardinality() != cb {
		t.Fatal("batch op mutated an operand")
	}
	// Mutating a result must not affect operands.
	res := Or(a, b)
	res.Add(1 << 20)
	res.Remove(0)
	if a.Cardinality() != ca || b.Cardinality() != cb {
		t.Fatal("result shares memory with operands")
	}
}
