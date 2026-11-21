package repair_test

import (
	"math/rand"
	"testing"

	"prescreen.local/ps-8303db8549/repair"
)

func BenchmarkScheduleFullSize(b *testing.B) {
	rng := rand.New(rand.NewSource(7))
	dim := 12
	shards := make([]repair.Shard, 24)
	rackFee := map[int]int64{}
	for i := range shards {
		row := make([]byte, dim)
		for j := range row {
			row[j] = byte(rng.Intn(256))
		}
		rack := i % 4
		rackFee[rack] = int64(5 + rack)
		shards[i] = repair.Shard{
			Row:       row,
			ReadPrice: int64(1 + rng.Intn(4)),
			Rack:      rack,
		}
	}
	targets := [][]byte{
		{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1},
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repair.Schedule(dim, shards, rackFee, targets); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScheduleZeroPricesWorstCase(b *testing.B) {
	dim := 12
	shards := make([]repair.Shard, 24)
	for i := range shards {
		row := make([]byte, dim)
		if i < dim {
			row[i] = 1
		} else {
			row[i%dim] = 1
			row[(i+1)%dim] = 2
		}
		shards[i] = repair.Shard{Row: row, ReadPrice: 0, Rack: 0}
	}
	targets := [][]byte{{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := repair.Schedule(dim, shards, map[int]int64{0: 0}, targets); err != nil {
			b.Fatal(err)
		}
	}
}
