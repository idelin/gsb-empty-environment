// Command circlecover reads points, weights and a radius from stdin and
// prints the maximum coverable weight and an attaining center.
//
// Input format (tokens separated by whitespace):
//
//	<radius>        rational, e.g. 5/2 or 3
//	<n>             number of points
//	<x> <y> <w>     n lines: integer coordinates, non-negative weight
//
// Example:
//
//	$ printf '5/2\n3\n0 0 1\n3 0 1\n6 0 5\n' | circlecover
//	weight 6
//	center (3/2, 0)
package main

import (
	"bufio"
	"fmt"
	"math/big"
	"os"

	"circlecover"
)

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "circlecover:", err)
	os.Exit(1)
}

func main() {
	in := bufio.NewReader(os.Stdin)

	var rStr string
	if _, err := fmt.Fscan(in, &rStr); err != nil {
		fatal(err)
	}
	radius, ok := new(big.Rat).SetString(rStr)
	if !ok {
		fatal(fmt.Errorf("invalid radius %q", rStr))
	}

	var n int
	if _, err := fmt.Fscan(in, &n); err != nil {
		fatal(err)
	}
	pts := make([]circlecover.Point, n)
	weights := make([]int64, n)
	for i := 0; i < n; i++ {
		if _, err := fmt.Fscan(in, &pts[i].X, &pts[i].Y, &weights[i]); err != nil {
			fatal(err)
		}
	}

	res, err := circlecover.Solve(pts, weights, radius)
	if err != nil {
		fatal(err)
	}
	fmt.Println("weight", res.Weight)
	fmt.Println("center", res.Center)
}
