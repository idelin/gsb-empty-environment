package tans

import (
	"math/rand"
	"testing"
)

var (
	benchData  []byte
	benchTable *Table
	benchModel *rangeModel
	benched    []byte
)

func setupBench() {
	if benchData != nil {
		return
	}
	rng := rand.New(rand.NewSource(99))
	benchData = zipfSample(rng, 4<<20, 64) // 4 MiB, highly skewed
	benchTable = BuildTable(CountSymbols(benchData), 0)
	benchModel = newRangeModel(benchTable.norm)
	benched = benchTable.Encode(nil, benchData)
}

func BenchmarkTANSEncode(b *testing.B) {
	setupBench()
	b.SetBytes(int64(len(benchData)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchTable.Encode(nil, benchData)
	}
}

func BenchmarkTANSDecode(b *testing.B) {
	setupBench()
	b.SetBytes(int64(len(benchData)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := benchTable.Decode(nil, benched); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRangeEncode(b *testing.B) {
	setupBench()
	b.SetBytes(int64(len(benchData)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rangeEncode(benchModel, benchData)
	}
}

func BenchmarkRangeDecode(b *testing.B) {
	setupBench()
	enc := rangeEncode(benchModel, benchData)
	n := len(benchData)
	b.SetBytes(int64(n))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		rangeDecode(benchModel, enc, n)
	}
}
