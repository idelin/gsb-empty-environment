package main

import (
	"os"
	"testing"
)

func BenchmarkBig18(b *testing.B) {
	data, err := os.ReadFile("examples/hard18.json")
	if err != nil {
		b.Skip(err)
	}
	in, err := ParseInput(data)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Solve(in); err != nil {
			b.Fatal(err)
		}
	}
}
