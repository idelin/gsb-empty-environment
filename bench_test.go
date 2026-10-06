package testselect

import (
	"fmt"
	"testing"
)

func BenchmarkSelect20(b *testing.B) {
	var tests []Test
	for i := 0; i < 20; i++ {
		tests = append(tests, Test{
			ID:     fmt.Sprintf("t%02d", i),
			Cost:   int64(i*7%13 + 1),
			Faults: []string{fmt.Sprintf("f%d", i%8), fmt.Sprintf("f%d", (i*3+1)%8)},
		})
	}
	var faults []string
	for i := 0; i < 8; i++ {
		faults = append(faults, fmt.Sprintf("f%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Select(tests, faults); err != nil {
			b.Fatal(err)
		}
	}
}
