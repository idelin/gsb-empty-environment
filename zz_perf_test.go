package main

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

func TestPerf200k(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 8))
	var sb strings.Builder
	sb.WriteByte('[')
	const n = 200000
	for i := range n {
		if i > 0 {
			sb.WriteByte(',')
		}
		lo := rng.Int64N(1_000_000_000_000)
		span := rng.Int64N(1_000_000) + 1
		hi := lo + span
		gen := rng.Int64N(100)
		kind := "ack"
		if rng.IntN(2) == 0 {
			kind = "lease"
		}
		fmt.Fprintf(&sb, `["%s",%d,%d,%d]`, kind, lo, hi, gen)
	}
	sb.WriteByte(']')
	cps, err := Replay(strings.NewReader(sb.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(cps) != n {
		t.Fatalf("got %d checkpoints want %d", len(cps), n)
	}
}
