// Command bnbdemo demonstrates the bnb branch-and-bound knapsack solver:
// it runs an instance uninterrupted, then again with repeated
// checkpoint/restore cycles (including simulated crashes), and finally
// verifies the optimality claim against independent exhaustive
// enumeration. It exits non-zero if any run disagrees.
package main

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"

	"bnb"
)

func main() {
	seed := flag.Int64("seed", 1, "random seed")
	n := flag.Int("n", 10, "number of items")
	maxBound := flag.Int64("max-bound", 3, "maximum per-item bound")
	chunk := flag.Int64("chunk", 40, "node budget per solve chunk")
	crashEvery := flag.Int64("crash-every", 7, "expansions between mid-run checkpoints in the crash simulation")
	exhaustiveLimit := flag.Int64("exhaustive-limit", 3000000, "skip exhaustive verification above this search-space size")
	flag.Parse()

	rng := rand.New(rand.NewSource(*seed))
	p := bnb.RandomProblem(rng, *n, 25, 60, *maxBound, 2, 5)
	fmt.Printf("problem: n=%d capacity=%d values=%v\n", p.NumItems(), p.Capacity, p.Values)
	fmt.Printf("         weights=%v bounds=%v\n", p.Weights, p.Bounds)

	ok := true
	fail := func(format string, args ...interface{}) {
		ok = false
		fmt.Printf("FAIL: "+format+"\n", args...)
	}

	// 1. Uninterrupted reference run.
	ref, err := bnb.NewSolver(p)
	must(err)
	refRes, err := ref.Solve(bnb.Options{Budget: -1})
	must(err)
	fmt.Printf("\n[1] uninterrupted:        %s\n", refRes)
	if refRes.Status != bnb.StatusOptimal {
		fail("uninterrupted run did not prove optimality")
	}

	// 1b. Budget-exhausted run must return valid lower/upper bounds.
	limited, err := bnb.NewSolver(p)
	must(err)
	limRes, err := limited.Solve(bnb.Options{Budget: 2})
	must(err)
	fmt.Printf("    budget=2 only:        %s\n", limRes)
	if limRes.Status == bnb.StatusBudgetExhausted {
		if limRes.LowerBound > refRes.LowerBound || limRes.UpperBound < refRes.UpperBound {
			fail("budget bounds [%d,%d] do not bracket optimum %d",
				limRes.LowerBound, limRes.UpperBound, refRes.LowerBound)
		}
	}

	dir, err := tempDir()
	must(err)
	defer os.RemoveAll(dir)

	// 2. Chunked run with a checkpoint file round-trip after every chunk.
	s2, err := bnb.NewSolver(p)
	must(err)
	cpFile := filepath.Join(dir, "checkpoint.json")
	resumes := 0
	var res2 bnb.Result
	for {
		res2, err = s2.Solve(bnb.Options{Budget: *chunk})
		must(err)
		if res2.Status == bnb.StatusOptimal {
			break
		}
		must(s2.SaveCheckpointFile(cpFile))
		s2, err = bnb.LoadCheckpointFile(cpFile)
		must(err)
		resumes++
	}
	fmt.Printf("[2] checkpointed (%d resumes): %s\n", resumes, res2)
	compare("checkpointed", res2, refRes, &ok)

	// 3. Crash simulation: mid-run checkpoints; after each budget chunk the
	//    in-memory solver is discarded and the last checkpoint restored.
	s3, err := bnb.NewSolver(p)
	must(err)
	must(s3.SaveCheckpointFile(cpFile))
	crashes := 0
	var res3 bnb.Result
	for {
		res3, err = s3.Solve(bnb.Options{
			Budget:          *chunk,
			CheckpointEvery: *crashEvery,
			Hook: func(s *bnb.Solver) error {
				return s.SaveCheckpointFile(cpFile)
			},
		})
		must(err)
		if res3.Status == bnb.StatusOptimal {
			break
		}
		s3, err = bnb.LoadCheckpointFile(cpFile)
		must(err)
		crashes++
	}
	fmt.Printf("[3] crash-resumed (%d crashes): %s\n", crashes, res3)
	compare("crash-resumed", res3, refRes, &ok)

	// 4. Independent exhaustive verification of the optimality claim.
	if space := bnb.SearchSpace(p); space <= *exhaustiveLimit {
		best, sol := bnb.Exhaustive(p)
		fmt.Printf("[4] exhaustive (%d assignments): optimum=%d sol=%v\n", space, best, sol)
		if best != refRes.LowerBound || refRes.UpperBound != best {
			fail("solver claims [%d,%d] but exhaustive optimum is %d",
				refRes.LowerBound, refRes.UpperBound, best)
		}
	} else {
		fmt.Printf("[4] exhaustive: skipped (search space %d > limit %d)\n", space, *exhaustiveLimit)
	}

	if !ok {
		fmt.Println("\nRESULT: FAIL")
		os.Exit(1)
	}
	fmt.Println("\nRESULT: PASS — uninterrupted, checkpointed and crash-resumed runs agree; optimum verified")
}

func compare(name string, got, want bnb.Result, ok *bool) {
	if got.Status != want.Status ||
		got.LowerBound != want.LowerBound ||
		got.UpperBound != want.UpperBound ||
		got.NodesExplored != want.NodesExplored ||
		!reflect.DeepEqual(got.Solution, want.Solution) {
		*ok = false
		fmt.Printf("FAIL: %s run disagrees:\n got:  %s sol=%v\n want: %s sol=%v\n",
			name, got, got.Solution, want, want.Solution)
	}
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(2)
	}
}

func tempDir() (string, error) {
	dir := filepath.Join(os.TempDir(), fmt.Sprintf("bnbdemo-%d", os.Getpid()))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}
