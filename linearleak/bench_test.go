package linearleak

import "testing"

func BenchmarkAnalyze20Bits(b *testing.B) {
	n := 20
	eqs := make([]Equation, 6)
	for i := range eqs {
		row := make([]int64, n)
		for j := range row {
			row[j] = int64((j + i) % 5)
		}
		eqs[i] = Equation{Coeff: row, RHS: int64(20 + i*3)}
	}
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		rep, err := Analyze(n, eqs, nil, Budget{})
		if err != nil {
			b.Fatal(err)
		}
		if rep.Status == StatusUnknown {
			b.Fatalf("default budget exhausted: %+v", rep)
		}
	}
}
