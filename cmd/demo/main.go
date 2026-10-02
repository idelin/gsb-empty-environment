// Command demo exercises the wire codec on a hand-built nested protobuf
// stream and then runs a multi-node arbitration race.
package main

import (
	"fmt"
	"sync"

	"example.com/wirearb/arbiter"
	"example.com/wirearb/wire"
)

func main() {
	demoWire()
	demoArbiter()
}

func demoWire() {
	fmt.Println("== protobuf wire-format decode ==")

	// Hand-build a 3-level nested message:
	//   Outer{ id=1 varint, inner=2 msg, scores=3 packed varint }
	//   Inner{ delta=1 zigzag, core=2 msg }
	//   Core{ stamp=1 fixed64, label=2 string, coeffs=3 packed fixed32 }
	var core []byte
	core = wire.AppendTag(core, 1, wire.TypeFixed64)
	core = wire.AppendFixed64(core, 0x0123456789abcdef)
	core = wire.AppendTag(core, 2, wire.TypeBytes)
	core = wire.AppendBytes(core, []byte("deep-core"))
	core = wire.AppendTag(core, 3, wire.TypeBytes)
	core = wire.AppendBytes(core, wire.AppendPackedFixed32(nil, 7, 8, 9))

	var inner []byte
	inner = wire.AppendTag(inner, 1, wire.TypeVarint)
	inner = wire.AppendZigZag(inner, -123456)
	inner = wire.AppendTag(inner, 2, wire.TypeBytes)
	inner = wire.AppendBytes(inner, core)

	var outer []byte
	outer = wire.AppendTag(outer, 1, wire.TypeVarint)
	outer = wire.AppendVarint(outer, 4242)
	outer = wire.AppendTag(outer, 2, wire.TypeBytes)
	outer = wire.AppendBytes(outer, inner)
	outer = wire.AppendTag(outer, 3, wire.TypeBytes)
	outer = wire.AppendBytes(outer, wire.AppendPackedVarints(nil, 3, 1, 4, 1, 5, 9))

	fmt.Printf("encoded %d bytes: % x\n", len(outer), outer)

	// Decode schema-driven, the way generated protobuf code would.
	fields, err := wire.ParseMessage(outer)
	must(err)
	fmt.Printf("outer.id     = %d\n", fields[0].Varint)
	scores, err := wire.DecodePackedVarints(fields[2].Bytes)
	must(err)
	fmt.Printf("outer.scores = %v (packed)\n", scores)

	innerF, err := wire.ParseMessage(fields[1].Bytes)
	must(err)
	fmt.Printf("inner.delta  = %d (zigzag)\n", innerF[0].Svarint())

	coreF, err := wire.ParseMessage(innerF[1].Bytes)
	must(err)
	coeffs, err := wire.DecodePackedFixed32(coreF[2].Bytes)
	must(err)
	fmt.Printf("core.stamp   = %#x\n", coreF[0].Fixed64)
	fmt.Printf("core.label   = %q\n", coreF[1].Bytes)
	fmt.Printf("core.coeffs  = %v (packed)\n", coeffs)

	// Truncation safety: cut the stream mid-field.
	truncated := outer[:len(outer)-3]
	if _, err := wire.ParseMessage(truncated); err != nil {
		fmt.Printf("truncated stream safely rejected: %v\n", err)
	}
	fmt.Println()
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func demoArbiter() {
	fmt.Println("== multi-node conflict arbitration ==")
	a := arbiter.New()
	const nodes, proposals = 8, 1000
	var wg sync.WaitGroup
	for n := 0; n < nodes; n++ {
		wg.Add(1)
		go func(node int) {
			defer wg.Done()
			for i := 0; i < proposals; i++ {
				e := a.Propose(node)
				_ = a.Commit(e, node, fmt.Sprintf("node-%d/round-%d", node, i))
			}
		}(n)
	}
	wg.Wait()

	st := a.Stats()
	log := a.Log()
	fmt.Printf("%d nodes x %d proposals: %d committed, %d stale-token rejections, %d wrong-node rejections\n",
		nodes, proposals, st.Commits, st.StaleWins, st.WrongNode)
	fmt.Printf("final epoch: %d, log length: %d\n", a.Epoch(), len(log))
	prev := uint64(0)
	linearizable := true
	for _, e := range log {
		if e.Epoch <= prev {
			linearizable = false
			break
		}
		prev = e.Epoch
	}
	fmt.Printf("log is a strict total order (linearizable): %v\n", linearizable)
	if len(log) > 0 {
		fmt.Printf("last committed entry: %+v\n", log[len(log)-1])
	}
}
