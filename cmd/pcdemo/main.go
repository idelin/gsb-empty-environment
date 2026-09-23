// Command pcdemo demonstrates deterministic code-length construction,
// canonical codes, the wire format, and chunked streaming decoding.
package main

import (
	"bytes"
	"fmt"
	"sort"
	"strings"

	"prefixcode"
)

func codeString(c prefixcode.Code) string {
	var b strings.Builder
	for i := int(c.Len) - 1; i >= 0; i-- {
		b.WriteByte('0' + byte((c.Bits>>uint(i))&1))
	}
	return b.String()
}

func main() {
	message := []byte(strings.Repeat("the quick brown fox jumps over the lazy dog. ", 8) +
		strings.Repeat("eee", 30))
	const maxLen = 12

	var freqs [256]uint64
	for _, b := range message {
		freqs[b]++
	}
	lengths, err := prefixcode.BuildLengths(freqs[:], maxLen)
	if err != nil {
		panic(err)
	}
	codes, err := prefixcode.CanonicalCodes(lengths)
	if err != nil {
		panic(err)
	}

	fmt.Printf("message: %d bytes, maxLen=%d\n\n", len(message), maxLen)
	fmt.Println("sym  char  freq  len  code")
	var syms []int
	for s, l := range lengths {
		if l > 0 {
			syms = append(syms, s)
		}
	}
	sort.Ints(syms)
	for _, s := range syms {
		ch := fmt.Sprintf("%q", rune(s))
		if s == ' ' {
			ch = "'_'"
		}
		fmt.Printf("%3d  %-4s  %4d  %3d  %s\n", s, ch, freqs[s], lengths[s], codeString(codes[s]))
	}

	kraft := prefixcode.Kraft(lengths)
	kf, _ := kraft.Float64()
	cost := prefixcode.Cost(freqs[:], lengths)
	ent := prefixcode.Entropy(freqs[:])
	fmt.Printf("\nKraft sum: %s (%.6f)\n", kraft.RatString(), kf)
	fmt.Printf("cost: %d bits = %.4f bits/symbol (entropy %.4f, raw 8)\n",
		cost, float64(cost)/float64(len(message)), ent)

	data, err := prefixcode.Compress(message, maxLen)
	if err != nil {
		panic(err)
	}
	fmt.Printf("\nwire format: %d bytes total (header %d + payload %d)\n",
		len(data), 284, len(data)-284)

	// Streaming decode: feed the payload one byte at a time.
	out, err := prefixcode.Decompress(bytes.NewReader(data), int64(len(message)))
	if err != nil {
		panic(err)
	}
	fmt.Printf("round-trip via Decompress: %v\n", bytes.Equal(out, message))

	// Low-level chunked decode of the raw payload, byte by byte.
	tbl, _ := prefixcode.NewTable(lengths)
	dec := prefixcode.NewDecoder(tbl,
		prefixcode.WithSymbolCount(int64(len(message))),
		prefixcode.WithQuota(int64(len(message))))
	payload := data[284:]
	var got []byte
	buf := make([]byte, 3) // tiny output buffer on purpose
	for i := 0; i < len(payload); {
		c, n, err := dec.Decode(payload[i:i+1], buf, false)
		got = append(got, buf[:n]...)
		i += c
		if err == prefixcode.ErrShortBuffer {
			continue // retry unconsumed input with a fresh output buffer
		}
		if err != nil {
			panic(err)
		}
	}
	for {
		_, n, err := dec.Decode(nil, buf, true)
		got = append(got, buf[:n]...)
		if err != nil && err != prefixcode.ErrShortBuffer {
			panic(err)
		}
		if n == 0 && err == nil {
			break
		}
	}
	fmt.Printf("round-trip byte-by-byte + 3-byte output buffer: %v\n", bytes.Equal(got, message))

	// Truncation must fail, never succeed silently.
	short := data[:len(data)-1]
	if _, err := prefixcode.Decompress(bytes.NewReader(short), -1); err != nil {
		fmt.Printf("truncated tail rejected: %v\n", err)
	} else {
		fmt.Println("BUG: truncated input accepted")
	}

	// Quota is enforced.
	if _, err := prefixcode.Decompress(bytes.NewReader(data), int64(len(message)-1)); err != nil {
		fmt.Printf("quota enforced: %v\n", err)
	}
}
