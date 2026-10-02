// Demo: compressed bitmap with automatic container selection, fast set
// algebra, crash-safe persistence and lossy-network transfer.
package main

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"time"

	"roaring"
)

func printStats(name string, b *roaring.Bitmap) {
	s := b.Stats()
	fmt.Printf("%-28s cardinality=%d containers=%d (array=%d bitmap=%d run=%d) bytes=%d\n",
		name, s.Cardinality, s.Containers, s.Array, s.Bitmap, s.Run, s.Bytes)
}

func main() {
	fmt.Println("=== 1. Container auto-selection ===")
	sparse := roaring.New()
	rng := rand.New(rand.NewSource(42))
	for i := 0; i < 1000000; i++ {
		sparse.Add(uint64(rng.Intn(1 << 32)))
	}
	dense := roaring.New()
	dense.AddRange(10000000, 20000000) // 10M contiguous IDs
	mixed := roaring.New()
	for i := 0; i < 20000; i++ {
		mixed.Add(uint64(rng.Intn(65536))) // dense random -> bitmap container
	}
	printStats("sparse (1M random IDs)", sparse)
	printStats("dense range [1e7,2e7)", dense)
	printStats("dense random (20k/64k)", mixed)

	fmt.Println("\n=== 2. Set operations ===")
	start := time.Now()
	inter := sparse.And(dense)
	fmt.Printf("AND  sparse∩dense : %-12v cardinality=%d\n", time.Since(start), inter.Cardinality())
	start = time.Now()
	union := sparse.Or(dense)
	fmt.Printf("OR   sparse∪dense : %-12v cardinality=%d\n", time.Since(start), union.Cardinality())
	start = time.Now()
	x := sparse.Xor(dense)
	fmt.Printf("XOR  sparse△dense : %-12v cardinality=%d\n", time.Since(start), x.Cardinality())
	start = time.Now()
	batch := roaring.AndBatch(sparse, dense, union)
	fmt.Printf("AndBatch(3)       : %-12v cardinality=%d\n", time.Since(start), batch.Cardinality())

	fmt.Println("\n=== 3. 10^10-scale simulation (RLE containers) ===")
	A := roaring.New()
	A.AddRange(0, 5000000000)
	B := roaring.New()
	B.AddRange(2500000000, 7500000000)
	printStats("A = [0, 5e9)", A)
	printStats("B = [2.5e9, 7.5e9)", B)
	start = time.Now()
	ab := A.And(B)
	fmt.Printf("AND  A∩B: %-12v cardinality=%d (%.1f µs per 1e8 IDs)\n",
		time.Since(start), ab.Cardinality(), float64(time.Since(start).Microseconds())/50.0)
	start = time.Now()
	au := A.Or(B)
	fmt.Printf("OR   A∪B: %-12v cardinality=%d\n", time.Since(start), au.Cardinality())

	fmt.Println("\n=== 4. Fault injection & crash recovery ===")
	dir, err := os.MkdirTemp("", "roaring-demo")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)
	fi := roaring.NewFaultInjector()
	st := roaring.NewStorage(filepath.Join(dir, "bitmap.dat"), fi)

	if err := st.Save(sparse); err != nil {
		panic(err)
	}
	fmt.Printf("committed sparse bitmap (%d IDs)\n", sparse.Cardinality())

	// Disk full: save fails, state rolls back to the committed image.
	fi.DiskFullAfter = 128
	err = st.Save(dense)
	fi.Reset()
	back, _ := st.Load()
	fmt.Printf("[disk full]   save err=%v | rolled back to %d IDs (== sparse: %v)\n",
		err, back.Cardinality(), back.Equal(sparse))

	// Sudden power loss mid-commit: partial temp file is never committed.
	fi.CrashAfterBytes = 512
	err = st.Save(dense)
	fi.Reset()
	back, _ = st.Load()
	fmt.Printf("[power loss]  save err=%v | recovered %d IDs (== sparse: %v)\n",
		err, back.Cardinality(), back.Equal(sparse))

	// Torn write / bit-rot on the main file: self-heal from backup.
	fi.CorruptMainOnCommit = true
	if err := st.Save(dense); err != nil { // commits dense, then corrupts main
		panic(err)
	}
	fi.Reset()
	back, err = st.Load()
	fmt.Printf("[bit rot]     self-healed=%v | restored %d IDs (== sparse: %v)\n",
		err == nil, back.Cardinality(), back.Equal(sparse))

	fmt.Println("\n=== 5. Lossy-network transfer (30% packet loss) ===")
	fi.PacketLossRate = 0.30
	link := roaring.NewLossyLink(fi)
	s, r := link.Ends()
	var wg sync.WaitGroup
	wg.Add(2)
	var got *roaring.Bitmap
	var serr, rerr error
	go func() { defer wg.Done(); got, rerr = roaring.ReceiveBitmap(r) }()
	go func() { defer wg.Done(); serr = roaring.SendBitmap(sparse, s) }()
	wg.Wait()
	fmt.Printf("send err=%v recv err=%v | received %d IDs, identical=%v\n",
		serr, rerr, got.Cardinality(), got.Equal(sparse))
}
