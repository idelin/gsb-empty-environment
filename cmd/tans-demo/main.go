// Command tans-demo compresses a highly skewed sample with tANS, compares
// the result against the theoretical entropy bound and a classic range
// coder, and measures encode/decode throughput of both.
package main

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"time"

	"tans"
)

func zipfSample(rng *rand.Rand, n, alphabet int) []byte {
	cum := make([]float64, alphabet)
	sum := 0.0
	for i := 0; i < alphabet; i++ {
		sum += 1 / float64(i+1)
		cum[i] = sum
	}
	out := make([]byte, n)
	for i := range out {
		v := rng.Float64() * sum
		s := 0
		for s < alphabet-1 && cum[s] < v {
			s++
		}
		out[i] = byte(s)
	}
	return out
}

func entropyBits(counts []uint64) float64 {
	var total uint64
	for _, c := range counts {
		total += c
	}
	h := 0.0
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / float64(total)
		h -= p * math.Log2(p)
	}
	return h * float64(total)
}

func mbps(n int, d time.Duration) float64 {
	return float64(n) / d.Seconds() / (1 << 20)
}

func main() {
	const n = 16 << 20 // 16 MiB
	rng := rand.New(rand.NewSource(2026))
	data := zipfSample(rng, n, 64)
	counts := tans.CountSymbols(data)
	bound := entropyBits(counts)

	table := tans.BuildTable(counts, 0)
	tblBytes, _ := table.MarshalBinary()

	start := time.Now()
	compressed := table.Encode(nil, data)
	encDur := time.Since(start)

	start = time.Now()
	restored, err := table.Decode(nil, compressed)
	decDur := time.Since(start)
	if err != nil {
		panic(err)
	}
	if !bytes.Equal(restored, data) {
		panic("tANS round trip failed")
	}

	fmt.Printf("input:                %d bytes (highly skewed Zipf sample, 64 symbols)\n", len(data))
	fmt.Printf("entropy bound:        %d bytes (%.3f bits/symbol)\n", int(bound/8), bound/float64(n))
	fmt.Printf("tANS table:           %d bytes (tableLog=%d, %d states)\n",
		len(tblBytes), table.TableLog(), 1<<table.TableLog())
	fmt.Printf("tANS compressed:      %d bytes payload, %d bytes with table (%.3f%% of entropy bound)\n",
		len(compressed), len(compressed)+len(tblBytes),
		100*float64(len(compressed)+len(tblBytes))*8/bound)
	fmt.Printf("tANS encode:          %.0f MiB/s\n", mbps(len(data), encDur))
	fmt.Printf("tANS decode:          %.0f MiB/s\n", mbps(len(data), decDur))

	rc := tans.NewRangeBaseline(table)
	start = time.Now()
	rcCompressed := rc.Encode(data)
	rcEncDur := time.Since(start)
	start = time.Now()
	rcRestored := rc.Decode(rcCompressed, len(data))
	rcDecDur := time.Since(start)
	if !bytes.Equal(rcRestored, data) {
		panic("range coder round trip failed")
	}

	fmt.Printf("range coder payload:  %d bytes\n", len(rcCompressed))
	fmt.Printf("range coder encode:   %.0f MiB/s\n", mbps(len(data), rcEncDur))
	fmt.Printf("range coder decode:   %.0f MiB/s\n", mbps(len(data), rcDecDur))
	fmt.Printf("tANS speedup:         %.1fx encode, %.1fx decode vs arithmetic coding\n",
		float64(rcEncDur)/float64(encDur), float64(rcDecDur)/float64(decDur))
}
