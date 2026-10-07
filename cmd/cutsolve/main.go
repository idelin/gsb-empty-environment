// Command cutsolve finds optimal recursive integer-line cut trees for binary
// images, for a single frame or for two frames sharing one cut template.
//
// Usage:
//
//	cutsolve -mode single -k 4 -img0 "0110/1001/0000"
//	cutsolve -mode dual -k 4 -cap 2 -img0 "0110/1001" -img1 "0011/1100" [-verify]
//
// Images are rows of 0/1 separated by '/', up to 8x8. -verify re-derives the
// optimum by exhaustive enumeration of all cut trees (small images only).
package main

import (
	"flag"
	"fmt"
	"os"

	"cutsolve"
)

func main() {
	mode := flag.String("mode", "single", "single | dual")
	k := flag.Int("k", 0, "maximum number of leaves (required)")
	img0s := flag.String("img0", "", "frame 0, rows of 0/1 separated by '/'")
	img1s := flag.String("img1", "", "frame 1 (dual mode)")
	capBoth := flag.Int("cap", -1, "per-frame error cap for both frames (dual)")
	cap0 := flag.Int("cap0", -1, "frame-0 error cap (overrides -cap)")
	cap1 := flag.Int("cap1", -1, "frame-1 error cap (overrides -cap)")
	verify := flag.Bool("verify", false, "exhaustively enumerate all cut trees to verify the optimum")
	flag.Parse()

	if *k < 1 || *img0s == "" {
		fmt.Fprintln(os.Stderr, "required: -k >= 1 and -img0")
		flag.Usage()
		os.Exit(2)
	}
	im0, err := cutsolve.ParseImage(*img0s)
	if err != nil {
		fatal(err)
	}

	switch *mode {
	case "single":
		res := cutsolve.SolveSingle(im0, *k)
		fmt.Printf("mode: single  image: %dx%d  k: %d\n", im0.W, im0.H, *k)
		fmt.Printf("encoding: %s\nleaves: %d\nerror: %d\n", res.Tree.Encoding(), res.Leaves, res.Err)
		fmt.Printf("tree:\n%s", res.Tree.Pretty(cutsolve.Rect{X0: 0, Y0: 0, X1: im0.W, Y1: im0.H}, im0))
		if *verify {
			brute := cutsolve.BruteSingle(im0, *k)
			if brute.Err == res.Err && brute.Leaves == res.Leaves && brute.Tree.Encoding() == res.Tree.Encoding() {
				fmt.Println("verification: OK (exhaustive enumeration agrees)")
			} else {
				fmt.Printf("verification: MISMATCH dp=(err %d, leaves %d, %s) brute=(err %d, leaves %d, %s)\n",
					res.Err, res.Leaves, res.Tree.Encoding(), brute.Err, brute.Leaves, brute.Tree.Encoding())
				os.Exit(1)
			}
		}
	case "dual":
		if *img1s == "" {
			fatal(fmt.Errorf("dual mode requires -img1"))
		}
		im1, err := cutsolve.ParseImage(*img1s)
		if err != nil {
			fatal(err)
		}
		c0, c1 := *cap0, *cap1
		if c0 < 0 {
			c0 = *capBoth
		}
		if c1 < 0 {
			c1 = *capBoth
		}
		if c0 < 0 {
			c0 = im0.W * im0.H
		}
		if c1 < 0 {
			c1 = im0.W * im0.H
		}
		res := cutsolve.SolveDual(im0, im1, *k, c0, c1)
		fmt.Printf("mode: dual  image: %dx%d  k: %d  caps: f0<=%d f1<=%d\n", im0.W, im0.H, *k, c0, c1)
		if !res.Feasible {
			fmt.Print(res.Evidence)
			if *verify {
				brute := cutsolve.BruteDual(im0, im1, *k, c0, c1)
				if brute.Feasible {
					fmt.Println("verification: MISMATCH (enumeration found a feasible tree)")
					os.Exit(1)
				}
				fmt.Println("verification: OK (exhaustive enumeration confirms infeasibility)")
			}
			return
		}
		fmt.Printf("encoding: %s\nleaves: %d\nerror f0: %d\nerror f1: %d\ntotal error: %d\n",
			res.Tree.Encoding(), res.Leaves, res.Err0, res.Err1, res.Err0+res.Err1)
		fmt.Printf("tree:\n%s", res.Tree.Pretty(cutsolve.Rect{X0: 0, Y0: 0, X1: im0.W, Y1: im0.H}, im0, im1))
		if *verify {
			brute := cutsolve.BruteDual(im0, im1, *k, c0, c1)
			if brute.Feasible && brute.Err0 == res.Err0 && brute.Err1 == res.Err1 &&
				brute.Leaves == res.Leaves && brute.Tree.Encoding() == res.Tree.Encoding() {
				fmt.Println("verification: OK (exhaustive enumeration agrees)")
			} else {
				fmt.Printf("verification: MISMATCH dp=(%d,%d leaves %d %s) brute=(%d,%d leaves %d %s)\n",
					res.Err0, res.Err1, res.Leaves, res.Tree.Encoding(),
					brute.Err0, brute.Err1, brute.Leaves, brute.Tree.Encoding())
				os.Exit(1)
			}
		}
	default:
		fatal(fmt.Errorf("unknown -mode %q", *mode))
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}
