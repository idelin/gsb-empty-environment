package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func parseImageFlag(s string) *Image {
	im, err := NewImage(strings.Split(s, ","))
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid image: %v\n", err)
		os.Exit(2)
	}
	return im
}

func printCanvas(rows [][]int) {
	for _, row := range rows {
		for _, v := range row {
			fmt.Printf("%d", v)
		}
		fmt.Println()
	}
}

func cmdSolve1(args []string) {
	fs := flag.NewFlagSet("solve1", flag.ExitOnError)
	k := fs.Int("k", 4, "maximum number of leaves")
	imgFlag := fs.String("img", "", "image rows, comma-separated, e.g. 0101,1100")
	verify := fs.Bool("verify", false, "verify optimum by enumerating all cut trees")
	fs.Parse(args)
	img := parseImageFlag(*imgFlag)
	full := rect{0, 0, img.W, img.H}

	sol := newSolver1(img).best(full, *k)
	tree, err := parseEnc(sol.enc, full)
	if err != nil {
		fmt.Fprintf(os.Stderr, "internal: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("error=%d leaves=%d (limit %d)\n", sol.err, sol.leaves, *k)
	fmt.Printf("encoding=%s\n", sol.enc)
	fmt.Print(formatTree(tree, img))
	fmt.Println("painted:")
	printCanvas(render(tree, img))

	if *verify {
		dp, bf, ok := verify1(img, *k)
		if !ok {
			fmt.Printf("VERIFY FAILED: DP=%+v enumeration=%+v\n", dp, bf)
			os.Exit(1)
		}
		fmt.Printf("verify: enumeration over all trees agrees (err=%d leaves=%d enc=%s)\n", bf.err, bf.leaves, bf.enc)
	}
}

func cmdSolve2(args []string) {
	fs := flag.NewFlagSet("solve2", flag.ExitOnError)
	k := fs.Int("k", 4, "maximum number of leaves (shared tree)")
	capA := fs.Int("capA", 1<<30, "per-frame error cap for frame A")
	capB := fs.Int("capB", 1<<30, "per-frame error cap for frame B")
	imgAFlag := fs.String("imgA", "", "frame A rows, comma-separated")
	imgBFlag := fs.String("imgB", "", "frame B rows, comma-separated")
	verify := fs.Bool("verify", false, "verify result by enumerating all cut trees")
	fs.Parse(args)
	a := parseImageFlag(*imgAFlag)
	b := parseImageFlag(*imgBFlag)
	if a.W != b.W || a.H != b.H {
		fmt.Fprintf(os.Stderr, "frames must have the same size (%dx%d vs %dx%d)\n", a.W, a.H, b.W, b.H)
		os.Exit(2)
	}
	full := rect{0, 0, a.W, a.H}
	front := newSolver2(a, b).front(full, *k)
	best, ok := pickBest2(front, *k, *capA, *capB)

	if !ok {
		fmt.Printf("INFEASIBLE: no shared cut tree with <= %d leaves meets caps (A<=%d, B<=%d)\n", *k, *capA, *capB)
		minA, minB := 1<<30, 1<<30
		for _, p := range front {
			if p.errA < minA {
				minA = p.errA
			}
			if p.errB < minB {
				minB = p.errB
			}
		}
		fmt.Printf("evidence: best achievable per-frame errors with <= %d leaves: minA=%d minB=%d\n", *k, minA, minB)
		fmt.Printf("evidence: Pareto frontier of achievable (errA, errB, leaves):\n")
		for _, p := range front {
			fmt.Printf("  errA=%d errB=%d leaves=%d enc=%s\n", p.errA, p.errB, p.leaves, p.enc)
		}
	} else {
		tree, err := parseEnc(best.enc, full)
		if err != nil {
			fmt.Fprintf(os.Stderr, "internal: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("total=%d errA=%d errB=%d leaves=%d (limit %d, caps A<=%d B<=%d)\n",
			best.errA+best.errB, best.errA, best.errB, best.leaves, *k, *capA, *capB)
		fmt.Printf("encoding=%s\n", best.enc)
		fmt.Print(formatTree(tree, a, b))
		fmt.Println("painted frame A:")
		printCanvas(render(tree, a))
		fmt.Println("painted frame B:")
		printCanvas(render(tree, b))
	}

	if *verify {
		dp, dpOK, bfOK, bf, ok2 := verify2(a, b, *k, *capA, *capB)
		if !ok2 {
			fmt.Printf("VERIFY FAILED: DP=(%+v,%v) enumeration=(%+v,%v)\n", dp, dpOK, bf, bfOK)
			os.Exit(1)
		}
		if dpOK {
			fmt.Printf("verify: enumeration over all trees agrees (total=%d errA=%d errB=%d leaves=%d enc=%s)\n",
				bf.errA+bf.errB, bf.errA, bf.errB, bf.leaves, bf.enc)
		} else {
			fmt.Println("verify: enumeration over all trees confirms INFEASIBLE")
		}
	}
	if !ok {
		os.Exit(3)
	}
}

func cmdSelftest(args []string) {
	fs := flag.NewFlagSet("selftest", flag.ExitOnError)
	n := fs.Int("n", 200, "number of random cases")
	maxDim := fs.Int("maxdim", 4, "max image dimension (enumeration is exponential)")
	maxK := fs.Int("maxk", 5, "max leaf budget")
	seed := fs.Int64("seed", 1, "RNG seed")
	fs.Parse(args)
	if err := selftest(*n, *maxDim, *maxK, *seed); err != nil {
		fmt.Fprintf(os.Stderr, "SELFTEST FAILED: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  gsb solve1 -k K -img 0101,1100,... [-verify]
  gsb solve2 -k K -capA A -capB B -imgA ... -imgB ... [-verify]
  gsb selftest [-n N] [-maxdim D] [-maxk K] [-seed S]

Recursive guillotine cuts on integer grid lines; each leaf is painted its
majority color (ties -> 0). solve1 minimizes mis-painted pixels with at most
k leaves (ties: fewer leaves, then smaller public preorder cut encoding).
solve2 shares one cut tree across two same-size frames (per-frame leaf
colors), minimizing total error under per-frame caps; exits 3 when infeasible
and prints the Pareto frontier as evidence.
`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "solve1":
		cmdSolve1(os.Args[2:])
	case "solve2":
		cmdSolve2(os.Args[2:])
	case "selftest":
		cmdSelftest(os.Args[2:])
	default:
		usage()
	}
}
