package netsec

import (
	"math/rand"
	"testing"
)

// TestExhaustive8Arithmetic enumerates all 8-bit pairs for add/sub/mul and
// confirms the symbolic overflow/result logic matches the interpreter on
// every input. This is the mandated small-graph complete enumeration.
func TestExhaustive8Arithmetic(t *testing.T) {
	ops := []Op{OpAdd, OpSub, OpMul}
	for _, op := range ops {
		t.Run(op.String(), func(t *testing.T) {
			b := NewBuilder("exh-"+op.String(), 2)
			x := b.Input(0)
			y := b.Input(1)
			r := b.Binary(3, op, x, y)
			ovf := b.Overflow(4, r)
			errBlk := b.NewBlock()
			okBlk := b.NewBlock()
			b.Branch(ovf, errBlk, okBlk)
			b.UseBlock(errBlk)
			b.Error(7)
			b.UseBlock(okBlk)
			g := b.Return(r)

			f := NewBFactory()
			outs, trunc, err := symbolicExecute(g, f, "g")
			if err != nil {
				t.Fatal(err)
			}
			if trunc {
				t.Fatal("path enumeration truncated on a two-block graph")
			}

			eval := newDAGEvaluator(f)
			var ovfGuard *BNode
			for _, o := range outs {
				if o.kind == OutcomeError {
					ovfGuard = o.guard
				}
			}
			if ovfGuard == nil {
				t.Fatal("missing error outcome")
			}

			checked := 0
			for a := 0; a < 256; a++ {
				for c := 0; c < 256; c++ {
					in := []byte{byte(a), byte(c)}
					eval.assignInputs(in)
					symErr := eval.bool(ovfGuard)

					out, err := Interpret(g, in)
					if err != nil {
						t.Fatal(err)
					}
					wantErr := out.Kind == OutcomeError
					if symErr != wantErr {
						t.Fatalf("op=%s in=%v symbolic error=%v want %v",
							op, in, symErr, wantErr)
					}
					checked++
				}
			}
			if checked != 65536 {
				t.Fatalf("checked %d inputs, want 65536", checked)
			}
		})
	}
}

// dagEvaluator evaluates boolean DAG nodes under a concrete input assignment.
// It mirrors the brute-force SAT fallback and is independent of the
// interpreter's integer arithmetic.
type dagEvaluator struct {
	factory *BFactory
	slot    []int32
	val     []bool
	assign  map[*BNode]bool
	stamp   int32
	eval    func(n *BNode) bool
}

func newDAGEvaluator(f *BFactory) *dagEvaluator {
	maxID := 0
	for _, n := range f.Nodes() {
		if n.id > maxID {
			maxID = n.id
		}
	}
	d := &dagEvaluator{
		factory: f,
		slot:    make([]int32, maxID+1),
		val:     make([]bool, maxID+1),
		assign:  map[*BNode]bool{},
	}
	d.eval = func(n *BNode) bool {
		if n.op == BConst {
			return n.imm
		}
		if d.slot[n.id] == d.stamp {
			return d.val[n.id]
		}
		var r bool
		switch n.op {
		case BVar:
			r = d.assign[n]
		case BNot:
			r = !d.eval(n.x)
		case BAnd:
			r = d.eval(n.x) && d.eval(n.y)
		case BOr:
			r = d.eval(n.x) || d.eval(n.y)
		}
		d.slot[n.id] = d.stamp
		d.val[n.id] = r
		return r
	}
	return d
}

func (d *dagEvaluator) assignInputs(in []byte) {
	d.stamp++
	for i, bb := range in {
		for bit := 0; bit < 8; bit++ {
			d.assign[d.factory.Var("in"+itoa(i)+":"+itoa(bit))] = bb&(1<<bit) != 0
		}
	}
}

func (d *dagEvaluator) bool(n *BNode) bool { return d.eval(n) }

// assignModel fixes primary variables from a SAT model and stamps a fresh
// evaluation generation.
func (d *dagEvaluator) assignModel(model map[string]bool) {
	d.stamp++
	for _, n := range d.factory.Nodes() {
		if n.op == BVar {
			d.assign[n] = model[n.name]
		}
	}
}

// TestExhaustive8CrossCheck runs full enumeration through both the
// interpreter (EnumerateAll) and the symbolic Verify on a corpus of graph
// pairs, requiring the two methods to agree.
func TestExhaustive8CrossCheck(t *testing.T) {
	pairs := buildExhaustiveCorpus(t)
	for _, pc := range pairs {
		name := pc.name
		t.Run(name, func(t *testing.T) {
			mm, err := EnumerateAll(pc.oldG, pc.newG, 1<<16)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Verify(pc.oldG, pc.newG, Config{Budget: 2_000_000})
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case len(mm) == 0 && res.Status != StatusEquivalent:
				t.Fatalf("enumeration found no mismatch but Verify=%v reason=%q",
					res.Status, res.Reason)
			case len(mm) > 0 && res.Status != StatusMismatch:
				t.Fatalf("enumeration found %d mismatches but Verify=%v",
					len(mm), res.Status)
			case res.Status == StatusMismatch:
				if !witnessInMismatches(res.Witness, mm) {
					t.Fatalf("symbolic witness %v not reproduced by enumeration",
						res.Witness.Inputs)
				}
			}
		})
	}
}

func witnessInMismatches(w *Witness, mm []Mismatch) bool {
	if w == nil {
		return false
	}
	for _, m := range mm {
		if bytesEqual(m.Inputs, w.Inputs) {
			return true
		}
	}
	return false
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

type pairCase struct {
	name       string
	oldG, newG *Graph
}

// buildExhaustiveCorpus covers add/sub/mul, truncation/extension and branches,
// including both equivalent rewrites and injected discrepancies.
func buildExhaustiveCorpus(t *testing.T) []pairCase {
	t.Helper()
	var cases []pairCase

	// 1. Straight-line equivalent 8-bit arithmetic.
	for _, op := range []Op{OpAdd, OpSub, OpMul} {
		bo := NewBuilder("old-"+op.String(), 2)
		oldG := bo.Return(bo.Binary(3, op, bo.Input(0), bo.Input(1)))
		bn := NewBuilder("new-"+op.String(), 2)
		newG := bn.Return(bn.Binary(3, op, bn.Input(0), bn.Input(1)))
		cases = append(cases, pairCase{"equiv-" + op.String(), oldG, newG})
	}

	// 2. Widening to 16 bits with a correct reconstructed overflow predicate.
	cases = append(cases, pairCase{"mul8-widen-equiv", buildMul8Old(t), buildMul8New(t, false, false)})
	// 3. Wrong error node.
	cases = append(cases, pairCase{"mul8-wrong-node", buildMul8Old(t), buildMul8New(t, true, false)})
	// 4. Dropped overflow check.
	cases = append(cases, pairCase{"mul8-dropped", buildMul8Old(t), buildMul8New(t, false, true)})

	// 5. Truncation/extension equivalence: sext then trunc is identity on 8-bit.
	bo := NewBuilder("sext-trunc-old", 1)
	oldG := bo.Return(bo.Input(0))
	bn := NewBuilder("sext-trunc-new", 1)
	newG := bn.Return(bn.Trunc(4, bn.SExt(3, bn.Input(0), 16), 8))
	cases = append(cases, pairCase{"sext-trunc-equiv", oldG, newG})

	// 6. Injected constant discrepancy on the normal path.
	bo = NewBuilder("const-old", 1)
	oldG = bo.Return(bo.Input(0))
	bn = NewBuilder("const-new", 1)
	newG = bn.Return(bn.Const(9, 8, 1))
	cases = append(cases, pairCase{"const-mismatch", oldG, newG})

	// 7. Branch on signed comparison, both sides equivalent.
	bo = NewBuilder("cmp-old", 2)
	lt := bo.Compare(3, OpSlt, bo.Input(0), bo.Input(1))
	eBlk, oBlk := bo.NewBlock(), bo.NewBlock()
	bo.Branch(lt, eBlk, oBlk)
	bo.UseBlock(eBlk)
	bo.Error(11)
	bo.UseBlock(oBlk)
	oldG = bo.Return(bo.Input(0))
	bn = NewBuilder("cmp-new", 2)
	ge := bn.Compare(3, OpSle, bn.Input(1), bn.Input(0))
	eBlk2, oBlk2 := bn.NewBlock(), bn.NewBlock()
	bn.Branch(bn.Not(4, ge), eBlk2, oBlk2)
	bn.UseBlock(eBlk2)
	bn.Error(11)
	bn.UseBlock(oBlk2)
	newG = bn.Return(bn.Input(0))
	cases = append(cases, pairCase{"cmp-equiv", oldG, newG})

	return cases
}

// TestExhaustiveFuzzCorpus adds randomized small graph pairs and checks that
// enumeration and symbolic search never contradict each other.
func TestExhaustiveFuzzCorpus(t *testing.T) {
	rng := rand.New(rand.NewSource(20261112))
	for iter := 0; iter < 12; iter++ {
		oldG, newG, wantMismatch := randomSmallPair(t, rng)
		mm, err := EnumerateAll(oldG, newG, 1<<16)
		if err != nil {
			t.Fatal(err)
		}
		res, err := Verify(oldG, newG, Config{Budget: 2_000_000})
		if err != nil {
			t.Fatal(err)
		}
		if wantMismatch && len(mm) == 0 {
			t.Fatalf("iter %d: expected injected mismatch, enumeration found none", iter)
		}
		if len(mm) == 0 && res.Status != StatusEquivalent {
			t.Fatalf("iter %d: no enumeration mismatch but Verify=%v", iter, res.Status)
		}
		if len(mm) > 0 && res.Status != StatusMismatch {
			t.Fatalf("iter %d: enumeration mismatches=%d but Verify=%v",
				iter, len(mm), res.Status)
		}
	}
}

// randomSmallPair builds two tiny straight-line 8-bit graphs. With probability
// half the new graph gets an injected discrepancy (flipped op or constant).
func randomSmallPair(t *testing.T, rng *rand.Rand) (oldG, newG *Graph, wantMismatch bool) {
	t.Helper()
	op := []Op{OpAdd, OpSub, OpMul}[rng.Intn(3)]
	build := func(name string, inject bool) *Graph {
		b := NewBuilder(name, 2)
		x, y := b.Input(0), b.Input(1)
		chosen := op
		if inject {
			switch rng.Intn(2) {
			case 0:
				chosen = []Op{OpAdd, OpSub, OpMul}[(int(op)+1+rng.Intn(2))%3]
			case 1:
				y = b.Const(20, 8, uint64(1+rng.Intn(5)))
			}
		}
		return b.Return(b.Binary(3, chosen, x, y))
	}
	wantMismatch = rng.Intn(2) == 0
	oldG = build("fz-old", false)
	newG = build("fz-new", wantMismatch)
	return oldG, newG, wantMismatch
}
