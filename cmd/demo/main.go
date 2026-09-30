package main

import (
	"fmt"

	"roaring"
)

func printStats(label string, s *roaring.Set) {
	st := s.Stats()
	fmt.Printf("%-28s card=%-7d buckets=%d (array=%d bitmap=%d run=%d) bytes=%d (array=%d bitmap=%d run=%d) conversions=%d\n",
		label, st.Cardinality, st.Buckets,
		st.ArrayBuckets, st.BitmapBuckets, st.RunBuckets,
		st.TotalBytes, st.ArrayBytes, st.BitmapBytes, st.RunBytes,
		st.Conversions)
}

func main() {
	cfg := roaring.DefaultConfig()
	fmt.Printf("config: ArrayMax=%d BitmapMin=%d HysteresisBytes=%d (bitmap fixed at 8192 B)\n\n",
		cfg.ArrayMax, cfg.BitmapMin, cfg.HysteresisBytes)

	// Bucket 0: consecutive IDs -> run encoding.
	// Bucket 1: sparse scattered IDs -> array encoding.
	// Bucket 2: dense scattered IDs -> bitmap encoding.
	a := roaring.New(cfg)
	for i := uint32(0); i < 10000; i++ {
		a.Add(i) // 0..9999 consecutive
	}
	for i := 0; i < 300; i++ {
		a.Add(65536 + uint32(i*200)) // sparse in bucket 1
	}
	for i := uint32(0); i < 6000; i++ {
		a.Add(131072 + (i*7)%65536) // dense in bucket 2
	}
	printStats("set A", a)

	b := roaring.New(cfg)
	for i := uint32(5000); i < 15000; i++ {
		b.Add(i) // overlaps A's run in bucket 0
	}
	for i := uint32(0); i < 5000; i++ {
		b.Add(131072 + (i*11)%65536) // dense in bucket 2
	}
	printStats("set B", b)

	fmt.Println()
	printStats("A Or B", a.Or(b))
	printStats("A And B", a.And(b))
	printStats("A AndNot B", a.AndNot(b))
	printStats("A Xor B", a.Xor(b))

	fmt.Println()
	or := a.Or(b)
	fmt.Printf("sample: A.And(B) contains 7000=%v 20000=%v; Or card=%d And card=%d\n",
		a.And(b).Contains(7000), a.And(b).Contains(20000),
		or.Cardinality(), a.And(b).Cardinality())

	// Hysteresis demo: delete back down through the bitmap threshold.
	c := roaring.New(cfg)
	for i := uint32(0); i < 4097; i++ {
		c.Add((i * 17) % 65536)
	}
	printStats("C at card 4097", c)
	c.Remove(0) // 4096: inside hysteresis band, stays bitmap
	printStats("C after removing 1", c)
	for i := uint32(0); i < 600; i++ { // drain below BitmapMin -> array
		v := (i * 17) % 65536
		if c.Contains(v) {
			c.Remove(v)
		}
	}
	printStats("C drained below BitmapMin", c)
}
