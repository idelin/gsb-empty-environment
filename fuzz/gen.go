// Package fuzz generates structured valid KVS1 samples, applies
// single-point mutations, runs correctness checks, and shrinks
// failing inputs.
package fuzz

import (
	"math/rand"

	"kvsfmt/format"
)

// keyAlphabet contains only bytes legal in a key.
const keyAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789._-:+"

// Generate builds one random valid document. The generator maintains
// the format's constraint dependencies: entry count always matches the
// number of entries, keys use only legal bytes, lengths stay within
// bounds, and the checksum is computed last over the final bytes.
func Generate(rng *rand.Rand) ([]format.Entry, []byte) {
	n := rng.Intn(7)
	entries := make([]format.Entry, 0, n)
	for i := 0; i < n; i++ {
		klen := 1 + rng.Intn(12)
		key := make([]byte, klen)
		for j := range key {
			key[j] = keyAlphabet[rng.Intn(len(keyAlphabet))]
		}
		val := make([]byte, rng.Intn(33))
		rng.Read(val)
		entries = append(entries, format.Entry{Key: string(key), Value: val})
	}
	data, err := format.Encode(entries)
	if err != nil {
		panic("generator produced unencodable entries: " + err.Error())
	}
	return entries, data
}
