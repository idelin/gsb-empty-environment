package netsec

import "testing"

// TestExhaustive8Compares enumerates every byte pair and confirms the
// bit-blasted signed comparisons match the interpreter.
func TestExhaustive8Compares(t *testing.T) {
	for _, op := range []Op{OpEq, OpNe, OpSlt, OpSle} {
		t.Run(op.String(), func(t *testing.T) {
			b := NewBuilder("cmp-"+op.String(), 2)
			cmp := b.Compare(3, op, b.Input(0), b.Input(1))
			errBlk, okBlk := b.NewBlock(), b.NewBlock()
			b.Branch(cmp, errBlk, okBlk)
			b.UseBlock(errBlk)
			b.Error(5)
			b.UseBlock(okBlk)
			g := b.Return(b.Input(0))

			f := NewBFactory()
			outs, _, err := symbolicExecute(g, f, "g")
			if err != nil {
				t.Fatal(err)
			}
			var trueGuard *BNode
			for _, o := range outs {
				if o.kind == OutcomeError {
					trueGuard = o.guard
				}
			}
			ev := newDAGEvaluator(f)
			for a := 0; a < 256; a++ {
				for c := 0; c < 256; c++ {
					in := []byte{byte(a), byte(c)}
					ev.assignInputs(in)
					out, err := Interpret(g, in)
					if err != nil {
						t.Fatal(err)
					}
					want := out.Kind == OutcomeError
					if ev.bool(trueGuard) != want {
						t.Fatalf("op=%s in=%v symbolic=%v want=%v",
							op, in, ev.bool(trueGuard), want)
					}
				}
			}
		})
	}
}

// TestExhaustive8Extensions validates truncation and sign/zero extension
// against the interpreter over all single-byte inputs.
func TestExhaustive8Extensions(t *testing.T) {
	build := func(mode string) *Graph {
		b := NewBuilder("ext-"+mode, 1)
		in := b.Input(0)
		switch mode {
		case "sext16-trunc":
			return b.Return(b.Trunc(2, b.SExt(1, in, 16), 8))
		case "zext16-trunc":
			return b.Return(b.Trunc(2, b.ZExt(1, in, 16), 8))
		case "sext32-trunc16":
			return b.Return(b.Trunc(3, b.SExt(1, in, 32), 16))
		default:
			panic("bad mode")
		}
	}
	base := NewBuilder("base", 1)
	baseG := base.Return(base.Input(0))

	for _, mode := range []string{"sext16-trunc", "zext16-trunc"} {
		g := build(mode)
		mm, err := EnumerateAll(baseG, g, 10)
		if err != nil {
			t.Fatal(err)
		}
		if len(mm) != 0 {
			t.Fatalf("mode=%s produced %d mismatches", mode, len(mm))
		}
	}

	// sext32 then truncate to 16 must equal the direct 16-bit sign extension.
	ref := NewBuilder("ref16", 1)
	refG := ref.Return(ref.SExt(1, ref.Input(0), 16))
	g := build("sext32-trunc16")
	mm, err := EnumerateAll(refG, g, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(mm) != 0 {
		t.Fatalf("sext32-trunc16 produced %d mismatches", len(mm))
	}
}
