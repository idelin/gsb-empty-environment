// Command voxdemo reads voxel coordinates ("x y z" per line, '#' starts a
// comment) from stdin and prints the surface analysis: total and outer
// surface area, cavity volumes, and optionally the per-face evidence.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"voxelsurf"
)

func main() {
	showFaces := flag.Bool("faces", false, "print per-face classification evidence")
	flag.Parse()

	var vs []voxelsurf.Vec3
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		var x, y, z int64
		if _, err := fmt.Sscanf(line, "%d %d %d", &x, &y, &z); err != nil {
			continue
		}
		vs = append(vs, voxelsurf.Vec3{X: x, Y: y, Z: z})
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintln(os.Stderr, "read error:", err)
		os.Exit(1)
	}

	res := voxelsurf.Analyze(vs)
	fmt.Print(res.Summary())
	if *showFaces {
		fmt.Print(res.Evidence())
	}
}
