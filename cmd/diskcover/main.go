// Command diskcover reads a radius (rational, e.g. "5/2") as its only
// argument and points as "x y weight" lines on stdin, then prints the
// maximum coverable weight and an exact optimal center.
package main

import (
	"bufio"
	"fmt"
	"math/big"
	"os"

	"diskcover"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: diskcover RADIUS < points.txt")
		os.Exit(2)
	}
	radius, ok := new(big.Rat).SetString(os.Args[1])
	if !ok || radius.Sign() < 0 {
		fmt.Fprintln(os.Stderr, "invalid radius:", os.Args[1])
		os.Exit(2)
	}

	var pts []diskcover.Point
	var ws []int64
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		var x, y, w int64
		if _, err := fmt.Sscan(in.Text(), &x, &y, &w); err != nil {
			fmt.Fprintln(os.Stderr, "bad line:", in.Text())
			os.Exit(2)
		}
		pts = append(pts, diskcover.Point{X: x, Y: y})
		ws = append(ws, w)
	}

	best, center, err := diskcover.Solve(pts, ws, radius)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("weight: %d\ncenter: %v\n", best, center)
}
