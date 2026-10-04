// Command demo is an end-to-end showcase of the compressed bitmap and the
// deterministic simulation / fault-reproduction tool.
//
//	demo                      run the full showcase
//	demo -steps 1000000       run a 1M-step deterministic simulation
//	demo -log ops.log         record the op stream to a file
//	demo -replay ops.log      replay a recorded log, verify every checksum
//	demo -fault -log bad.log  record a log, then corrupt + replay it
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"math/rand"
	"os"
	"strings"
	"time"

	"compressedbitmap/bitmap"
	"compressedbitmap/sim"
)

func main() {
	seed := flag.Int64("seed", 42, "simulation seed")
	steps := flag.Uint64("steps", 1000000, "simulation steps")
	logFile := flag.String("log", "", "record op log to this file")
	replayFile := flag.String("replay", "", "replay and verify a recorded op log")
	fault := flag.Bool("fault", false, "inject a fault into the recorded log and reproduce it")
	flag.Parse()

	if *replayFile != "" {
		replay(*replayFile, *seed)
		return
	}

	showcaseContainers()
	showcaseScale()
	sum := runSimulation(*seed, *steps, *logFile)
	if *fault && *logFile != "" {
		injectAndReproduce(*logFile, *seed, sum)
	}
}

// showcaseContainers inserts sparse ids and dense ranges and shows how the
// engine picks Array / Bitmap / RLE containers automatically.
func showcaseContainers() {
	fmt.Println("== container auto-selection ==")

	sparse := bitmap.New()
	r := rand.New(rand.NewSource(1))
	for i := 0; i < 3000; i++ {
		sparse.Add(uint64(r.Int63n(1 << 40)))
	}
	printStats("sparse ids (3000 scattered)", sparse)

	dense := bitmap.New()
	dense.AddRange(1000000, 1100000)
	printStats("dense range [1e6, 1.1e6)", dense)

	hot := bitmap.New()
	for i := 0; i < 6000; i++ {
		hot.Add(uint64(i*7) % 60000) // >4096 scattered in one container
	}
	printStats("6000 scattered ids / container", hot)

	hot.Optimize()
	printStats("same after Optimize()", hot)
	fmt.Println()
}

func printStats(label string, b *bitmap.Bitmap) {
	s := b.Stats()
	fmt.Printf("  %-32s card=%-8d containers=%d (array=%d bitmap=%d run=%d) bytes=%d\n",
		label, s.Cardinality, s.Containers, s.ArrayContainers, s.BitmapContainers,
		s.RunContainers, s.SizeInBytes)
}

// showcaseScale intersects/unions two bitmaps holding ~5e9 ids each and
// times the operations. Dense ranges are RLE-compressed, so the working
// set stays tiny and ops complete in microseconds.
func showcaseScale() {
	fmt.Println("== 10^10-scale set algebra ==")
	a := bitmap.New()
	a.AddRange(0, 5000000000) // 5e9 ids
	b := bitmap.New()
	b.AddRange(2500000000, 7500000000)
	fmt.Printf("  A: card=%d bytes=%d   B: card=%d bytes=%d\n",
		a.Cardinality(), a.Stats().SizeInBytes, b.Cardinality(), b.Stats().SizeInBytes)

	timeOp := func(name string, f func() *bitmap.Bitmap) {
		f() // warm up
		start := time.Now()
		res := f()
		el := time.Since(start)
		fmt.Printf("  A %-3s B -> card=%-12d in %v (%d us)\n",
			name, res.Cardinality(), el, el.Nanoseconds()/1000)
	}
	timeOp("AND", func() *bitmap.Bitmap { return a.And(b) })
	timeOp("OR ", func() *bitmap.Bitmap { return a.Or(b) })
	timeOp("XOR", func() *bitmap.Bitmap { return a.Xor(b) })

	// segment bitmaps: sparse membership sets inside the same 10^10 space
	s1, s2 := bitmap.New(), bitmap.New()
	r := rand.New(rand.NewSource(5))
	for i := 0; i < 100000; i++ {
		s1.Add(uint64(r.Int63n(10000000000)))
		s2.Add(uint64(r.Int63n(10000000000)))
	}
	fmt.Printf("  sparse segments: |S1|=|S2|=1e5 ids in [0, 10^10)\n")
	timeOp("AND", func() *bitmap.Bitmap { return s1.And(s2) })
	timeOp("OR ", func() *bitmap.Bitmap { return s1.Or(s2) })
	fmt.Println()
}

// runSimulation executes a deterministic event-driven simulation.
func runSimulation(seed int64, steps uint64, logFile string) uint64 {
	fmt.Printf("== deterministic simulation: seed=%d steps=%d ==\n", seed, steps)
	var rec *sim.Recorder
	var hook func(uint64, sim.Op, uint64)
	if logFile != "" {
		f, err := os.Create(logFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer f.Close()
		rec = sim.NewRecorder(f)
		hook = rec.Hook()
		defer rec.Flush()
	}
	e := sim.NewEngine(seed, 8)
	start := time.Now()
	sum := e.Run(steps, hook)
	el := time.Since(start)
	fmt.Printf("  final checksum=%d  sim-now=%d  wall=%v (%.0f steps/s)\n",
		sum, e.Now, el, float64(steps)/el.Seconds())
	fmt.Printf("  bitmap[0] stats: %+v\n", e.Bitmaps[0].Stats())
	if logFile != "" {
		fmt.Printf("  op log written to %s (%d records)\n", logFile, len(rec.Records))
	}
	fmt.Println()
	return sum
}

// replay verifies a recorded op log step by step.
func replay(path string, seed int64) {
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	recs, err := sim.ParseLog(f)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("== replay: %s (%d records, seed=%d) ==\n", path, len(recs), seed)
	if fault := sim.Replay(seed, 8, recs); fault != nil {
		fmt.Println("  DIVERGENCE DETECTED:")
		fmt.Println(" ", fault)
		fmt.Println(sim.Reproduce(seed, 8, recs, fault))
		os.Exit(1)
	}
	fmt.Printf("  OK: all %d checksums match, log is consistent\n", len(recs))
}

func writeFile(path, content string) error {
	return ioutil.WriteFile(path, []byte(content), 0644)
}

// injectAndReproduce corrupts a recorded log and demonstrates the
// fault-reproduction workflow.
func injectAndReproduce(path string, seed int64, goodSum uint64) {
	fmt.Println("== fault injection & reproduction ==")
	f, err := os.Open(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	recs, err := sim.ParseLog(f)
	f.Close()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// corrupt an ADD_RANGE in the middle of the log
	victim := len(recs) / 2
	for i := victim; i < len(recs); i++ {
		if recs[i].Op.Kind == sim.OpAddRange && recs[i].Op.End < (1<<18) {
			victim = i
			break
		}
	}
	recs[victim].Op.End++
	fmt.Printf("  injected fault at record %d (step %d): ADD_RANGE end +1\n",
		victim, recs[victim].Step)
	fault := sim.Replay(seed, 8, recs)
	if fault == nil {
		fmt.Println("  ERROR: fault was not detected!")
		os.Exit(1)
	}
	fmt.Println("  detected:", fault)
	repro := sim.Reproduce(seed, 8, recs, fault)
	reproPath := "repro.log"
	if err := writeFile(reproPath, repro); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	lines := strings.Split(strings.TrimRight(repro, "\n"), "\n")
	fmt.Printf("  minimal repro prefix (%d ops) written to %s; head:\n", fault.Step, reproPath)
	for _, l := range lines[:5] {
		fmt.Println("   ", l)
	}
	fmt.Printf("  (untampered run checksum was %d)\n", goodSum)
}
