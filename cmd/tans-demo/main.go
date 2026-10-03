// Command tans-demo compresses a highly skewed sample with the tANS coder
// and the reference arithmetic coder, and prints compression ratio and
// throughput for both.
package main

import (
	"bytes"
	"fmt"
	"math"
	"math/rand"
	"time"

	"tans"
)

func skewedSample(rng *rand.Rand, n int) []byte {
	var weights [32]float64
	sum := 0.0
	for i := range weights {
		weights[i] = math.Pow(0.8, float64(i))
		sum += weights[i]
	}
	out := make([]byte, n)
	for i := range out {
		x := rng.Float64() * sum
		acc := 0.0
		s := 0
		for ; s < len(weights)-1; s++ {
			acc += weights[s]
			if x < acc {
				break
			}
		}
		out[i] = byte(s)
	}
	return out
}

func bestOf(n int, f func()) time.Duration {
	best := time.Duration(1<<63 - 1)
	for i := 0; i < n; i++ {
		start := time.Now()
		f()
		if d := time.Since(start); d < best {
			best = d
		}
	}
	return best
}

func mbps(n int, d time.Duration) float64 {
	return float64(n) / d.Seconds() / (1 << 20)
}

func main() {
	const n = 4 << 20 // 4 MiB
	rng := rand.New(rand.NewSource(42))
	data := skewedSample(rng, n)

	var freq [256]uint64
	for _, b := range data {
		freq[b]++
	}
	entropy := tans.EntropyBits(freq[:])

	fmt.Printf("input: %d bytes, order-0 entropy bound: %.0f bytes (%.3f bits/symbol)\n\n",
		n, entropy/8, entropy/float64(n))

	// tANS
	var compressed []byte
	encDur := bestOf(3, func() { compressed = tans.Compress(data, tans.DefaultTableLog) })
	var decoded []byte
	var err error
	decDur := bestOf(3, func() { decoded, err = tans.Decompress(compressed) })
	if err != nil || !bytes.Equal(decoded, data) {
		panic("tANS round trip failed")
	}

	// Reference arithmetic coding (same static order-0 model)
	var acPayload []byte
	acEncDur := bestOf(3, func() { acPayload = tans.ArithCompress(data, &freq) })
	var acDecoded []byte
	acDecDur := bestOf(3, func() { acDecoded = tans.ArithDecompress(acPayload, len(data), &freq) })
	if !bytes.Equal(acDecoded, data) {
		panic("arithmetic round trip failed")
	}

	fmt.Println("codec              size (bytes)  vs entropy   encode      decode")
	fmt.Printf("tANS (this)        %11d   %+6.2f%%   %7.1f MB/s  %7.1f MB/s\n",
		len(compressed), 100*(float64(len(compressed))*8-entropy)/entropy,
		mbps(n, encDur), mbps(n, decDur))
	fmt.Printf("arithmetic (ref)   %11d   %+6.2f%%   %7.1f MB/s  %7.1f MB/s\n",
		len(acPayload), 100*(float64(len(acPayload))*8-entropy)/entropy,
		mbps(n, acEncDur), mbps(n, acDecDur))
	fmt.Printf("\nspeedup tANS vs arithmetic: encode %.1fx, decode %.1fx\n",
		float64(acEncDur)/float64(encDur), float64(acDecDur)/float64(decDur))
}
