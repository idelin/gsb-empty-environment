// Command segx reads integer segments from stdin, one per line as
// "id x1 y1 x2 y2" (blank lines and #-comments are ignored), and prints
// every intersection point and collinear overlap with the participating
// segment IDs. Coordinates are printed as canonical rationals.
package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"segsweep"
)

func main() {
	var segs []segsweep.IntSegment
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var s segsweep.IntSegment
		if _, err := fmt.Sscanf(line, "%d %d %d %d %d",
			&s.ID, &s.X1, &s.Y1, &s.X2, &s.Y2); err != nil {
			fmt.Fprintf(os.Stderr, "segx: bad line %q: %v\n", line, err)
			os.Exit(1)
		}
		segs = append(segs, s)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "segx:", err)
		os.Exit(1)
	}
	res := segsweep.Sweep(segs)
	fmt.Println("intersection points:")
	for _, p := range res.Points {
		fmt.Printf("  %s  segments %v\n", p.P, p.IDs)
	}
	fmt.Println("collinear overlaps:")
	for _, o := range res.Overlaps {
		fmt.Printf("  %s -> %s  segments %v\n", o.A, o.B, o.IDs)
	}
}
