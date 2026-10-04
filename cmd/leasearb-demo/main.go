// leasearb-demo replays event scripts against the lease arbiter.
// Usage: go run ./cmd/leasearb-demo [script-or-dir ...]  (default: ./scripts)
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"leasearb/leasearb"
)

func main() {
	paths := os.Args[1:]
	if len(paths) == 0 {
		paths = []string{"scripts"}
	}
	var files []string
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		if info.IsDir() {
			entries, _ := filepath.Glob(filepath.Join(p, "*.script"))
			sort.Strings(entries)
			files = append(files, entries...)
		} else {
			files = append(files, p)
		}
	}
	failed := 0
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			failed++
			continue
		}
		runner := leasearb.NewRunner(os.Stdout)
		name := strings.TrimSuffix(filepath.Base(f), ".script")
		if err := runner.Run(name, string(data)); err != nil {
			fmt.Printf("!!! %s FAILED: %v\n\n", name, err)
			failed++
		}
	}
	if failed > 0 {
		fmt.Printf("%d script(s) failed\n", failed)
		os.Exit(1)
	}
	fmt.Printf("all %d script(s) replayed successfully\n", len(files))
}
