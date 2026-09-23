package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	lcc "layered-container"
)

func main() {
	workDir := flag.String("workdir", "", "demo directory (default: temporary directory)")
	layers := flag.Int("layers", 2, "number of compression layers")
	expansionBasisPoints := flag.Int64("expansion-basis-points", 10000, "allowed decoded/encoded ratio times 100")
	flag.Parse()

	if *workDir == "" {
		dir, err := os.MkdirTemp("", "lcc-demo-")
		must(err)
		*workDir = dir
	} else {
		must(os.MkdirAll(*workDir, 0o755))
	}

	limits := lcc.DefaultLimits()
	limits.MaxExpansionBasisPoints = *expansionBasisPoints

	raw := bytes.Repeat([]byte{'A'}, 10000)
	container, err := lcc.Encode(raw, *layers, limits)
	must(err)

	containerPath := filepath.Join(*workDir, "sample.lcc")
	decodedPath := filepath.Join(*workDir, "sample.out")
	rejectedPath := filepath.Join(*workDir, "rejected.out")
	must(os.WriteFile(containerPath, container, 0o644))
	must(lcc.DecodeFile(containerPath, decodedPath, limits))

	decoded, err := os.ReadFile(decodedPath)
	must(err)
	if !bytes.Equal(raw, decoded) {
		must(fmt.Errorf("round trip mismatch"))
	}

	tight := limits
	tight.MaxOutputBytes = int64(len(raw))
	before := countTempOutputs(*workDir)
	err = lcc.DecodeFile(containerPath, rejectedPath, tight)
	after := countTempOutputs(*workDir)
	if err == nil {
		must(fmt.Errorf("tight shared output budget unexpectedly succeeded"))
	}

	fmt.Printf("workdir: %s\n", *workDir)
	fmt.Printf("raw=%d bytes container=%d bytes layers=%d\n", len(raw), len(container), *layers)
	fmt.Printf("round-trip: ok (%s)\n", decodedPath)
	fmt.Printf("tight shared output budget: rejected: %v\n", err)
	fmt.Printf("temporary outputs before/after rejection: %d/%d\n", before, after)
}

func countTempOutputs(dir string) int {
	matches, err := filepath.Glob(filepath.Join(dir, ".lcc-output-*"))
	if err != nil {
		must(err)
	}
	return len(matches)
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
