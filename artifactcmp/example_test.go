package artifactcmp_test

import (
	"fmt"
	"time"

	"example.org/netsec/q0084/artifactcmp"
)

// ExampleCompare shows the gate host comparing two parsed trees. The closure
// callback reports every path an executable actually reads.
func ExampleCompare() {
	t0 := time.Date(2026, 11, 13, 0, 0, 0, 0, time.UTC)
	left := artifactcmp.Tree{
		Builder: "builder-a",
		Nodes: []artifactcmp.Node{
			{Path: "bin", Type: artifactcmp.TypeDir, Mode: 0o755, ModTime: t0},
			{Path: "bin/app", Type: artifactcmp.TypeFile, Mode: 0o755, Content: []byte("#!/bin/sh\nload ../etc/cfg\n"), ModTime: t0},
			{Path: "etc", Type: artifactcmp.TypeDir, Mode: 0o755, ModTime: t0},
			{Path: "etc/cfg", Type: artifactcmp.TypeFile, Mode: 0o644, Content: []byte("v=1\n"), ModTime: t0},
		},
	}
	right := artifactcmp.Tree{
		Builder: "builder-b",
		Nodes: []artifactcmp.Node{
			{Path: "bin", Type: artifactcmp.TypeDir, Mode: 0o755, ModTime: t0.Add(time.Hour)},
			{Path: "bin/app", Type: artifactcmp.TypeFile, Mode: 0o755, Content: []byte("#!/bin/sh\nload ../etc/cfg\n"), ModTime: t0.Add(time.Hour)},
			{Path: "etc", Type: artifactcmp.TypeDir, Mode: 0o755, ModTime: t0.Add(time.Hour)},
			{Path: "etc/cfg", Type: artifactcmp.TypeFile, Mode: 0o644, Content: []byte("v=1\n"), ModTime: t0.Add(time.Hour)},
		},
	}

	closure := func(entries []string) ([]string, error) {
		// A real implementation traces loader syscalls; here the executable
		// reads its script plus the config it references.
		return append(append([]string{}, entries...), "etc/cfg"), nil
	}

	report, receipt, err := artifactcmp.Compare(
		left, right,
		"commit-deadbeef",
		artifactcmp.Rules{IgnoreModTime: true},
		closure,
	)
	if err != nil {
		fmt.Println("error:", err)
		return
	}
	fmt.Println("equal:", report.Equal)
	fmt.Println("receipt issued:", receipt != nil)
	fmt.Println("bound commit:", receipt.SourceCommit)
	// Output:
	// equal: true
	// receipt issued: true
	// bound commit: commit-deadbeef
}
