// Command tsblock-report compresses synthetic metric streams with the tsblock
// codec and prints compression-ratio statistics.
package main

import (
	"fmt"
	"math"
	"math/rand"

	"example.org/tsblock"
)

type scenario struct {
	name    string
	samples []tsblock.Sample
}

func main() {
	rng := rand.New(rand.NewSource(20261111))

	scenarios := []scenario{
		{"constant-clock-and-value", constantStream(20000)},
		{"realistic-cpu", realisticCPU(20000, rng)},
		{"random-floats", randomFloats(20000, rng)},
		{"random-bits", randomBits(20000, rng)},
	}

	fmt.Printf("%-24s %8s %12s %12s %8s %8s\n",
		"scenario", "samples", "raw bytes", "compressed", "ratio", "x smaller")
	for _, sc := range scenarios {
		report(sc.name, sc.samples)
	}
}

func report(name string, samples []tsblock.Sample) {
	enc := tsblock.NewEncoder(tsblock.DefaultMaxBlockSize)
	for _, s := range samples {
		if err := enc.Append(s); err != nil {
			panic(err)
		}
	}
	frames, err := enc.Seal()
	if err != nil {
		panic(err)
	}

	var compressed int
	for _, f := range frames {
		compressed += len(f)
	}

	// Verify bit-for-bit round trip before reporting any numbers.
	var decoded int
	for _, frame := range frames {
		dec, err := tsblock.NewDecoder(frame)
		if err != nil {
			panic(err)
		}
		for dec.Next() {
			want := samples[decoded]
			if dec.Time() != want.T ||
				math.Float64bits(dec.Value()) != math.Float64bits(want.V) {
				panic(fmt.Sprintf("round-trip mismatch at sample %d", decoded))
			}
			decoded++
		}
		if err := dec.Err(); err != nil {
			panic(err)
		}
	}
	if decoded != len(samples) {
		panic(fmt.Sprintf("decoded %d of %d samples", decoded, len(samples)))
	}

	raw := len(samples) * 16
	ratio := float64(compressed) / float64(raw)
	fmt.Printf("%-24s %8d %12d %12d %8.4f %7.1fx\n",
		name, len(samples), raw, compressed, ratio, 1/ratio)
}

func constantStream(n int) []tsblock.Sample {
	out := make([]tsblock.Sample, n)
	for i := range out {
		out[i] = tsblock.Sample{T: uint64(1_000_000 + i*100), V: 42.5}
	}
	return out
}

func realisticCPU(n int, rng *rand.Rand) []tsblock.Sample {
	out := make([]tsblock.Sample, n)
	ts := uint64(1_700_000_000_000)
	level := 35.0
	for i := range out {
		ts += 100 + uint64(rng.Intn(3)-1)
		switch {
		case i%2000 == 0:
			level = 80 + rng.Float64()*15
		case i%250 == 0:
			level += rng.NormFloat64() * 4
		default:
			level += rng.NormFloat64() * 0.02
		}
		if level < 2 {
			level = 2
		}
		if level > 98 {
			level = 98
		}
		v := level + math.Sin(float64(i)/40)*0.5
		if i%777 == 0 {
			v = math.Copysign(0, -1)
		}
		if i%991 == 0 {
			v = math.NaN()
		}
		out[i] = tsblock.Sample{T: ts, V: v}
	}
	return out
}

func randomFloats(n int, rng *rand.Rand) []tsblock.Sample {
	out := make([]tsblock.Sample, n)
	ts := uint64(1_700_000_000_000)
	for i := range out {
		ts += 95 + uint64(rng.Intn(11))
		out[i] = tsblock.Sample{T: ts, V: rng.NormFloat64() * 100}
	}
	return out
}

func randomBits(n int, rng *rand.Rand) []tsblock.Sample {
	out := make([]tsblock.Sample, n)
	ts := uint64(1_000_000)
	for i := range out {
		ts += uint64(1 + rng.Intn(1000))
		out[i] = tsblock.Sample{T: ts, V: math.Float64frombits(rng.Uint64())}
	}
	return out
}
