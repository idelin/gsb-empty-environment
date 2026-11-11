package conntrack

import (
	"math/rand"
	"testing"
	"time"
)

// TestDifferentialStress runs a larger, differently-seeded differential
// campaign than TestDifferentialAgainstReference to widen coverage.
func TestDifferentialStress(t *testing.T) {
	for trial := 0; trial < 400; trial++ {
		rng := rand.New(rand.NewSource(int64(0xABCD0000 + trial)))
		gen := newGenerator(rng, trial%2 == 0)
		pkts := gen.trace(rng)
		got, want := NewTable(), newRefTable()
		for i, p := range pkts {
			v1 := got.ProcessAt(p, time.Unix(int64(i), 0))
			v2 := want.process(p)
			if v1 != v2 {
				t.Fatalf("trial=%d pkt=%d flags=%s seq=%d ack=%d: %s vs %s",
					trial, i, flagName(p.Flags), p.Seq, p.Ack, v1, v2)
			}
		}
	}
}
