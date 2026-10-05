package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("o", "", "write merge manifest to this file (default: stdout)")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: archmerge [-o manifest.json] <source-archive-dir>...")
		fmt.Fprintln(os.Stderr, "merges archives read-only; conflicts exit with status 2")
		os.Exit(64)
	}

	var sources []Source
	used := make(map[string]int)
	for _, dir := range args {
		base := filepath.Base(filepath.Clean(dir))
		used[base]++
		name := base
		if used[base] > 1 {
			name = fmt.Sprintf("%s-%d", base, used[base])
		}
		s, err := LoadSource(name, dir)
		if err != nil {
			fmt.Fprintln(os.Stderr, "archmerge:", err)
			os.Exit(1)
		}
		sources = append(sources, s)
	}

	res := Merge(sources)
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintln(os.Stderr, "archmerge:", err)
		os.Exit(1)
	}
	data = append(data, '\n')

	if *out == "" {
		os.Stdout.Write(data)
	} else if err := writeFileAtomic(*out, data); err != nil {
		fmt.Fprintln(os.Stderr, "archmerge:", err)
		os.Exit(1)
	}

	if len(res.Conflicts) > 0 {
		fmt.Fprintf(os.Stderr, "archmerge: %d conflict(s); see manifest\n", len(res.Conflicts))
		os.Exit(2)
	}
}
