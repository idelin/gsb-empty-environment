package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `archmerge - merge multiple source archives into one manifest.

Usage: archmerge -o manifest.json SRC [SRC...]

Each SRC is a JSON source manifest or a directory (scanned in place).
Sources are only read, never modified. The merged manifest lists every
entry with the sources it came from. Exit code is 1 if conflicts exist.
`)
	os.Exit(2)
}

func main() {
	out := flag.String("o", "", "write manifest to this file (default: stdout)")
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		usage()
	}

	var sources []Source
	names := make(map[string]bool)
	for _, arg := range args {
		src, err := loadSource(arg)
		if err != nil {
			fmt.Fprintf(os.Stderr, "archmerge: %v\n", err)
			os.Exit(2)
		}
		if names[src.Name] {
			fmt.Fprintf(os.Stderr, "archmerge: duplicate source name %q\n", src.Name)
			os.Exit(2)
		}
		names[src.Name] = true
		sources = append(sources, src)
	}

	res := Merge(sources)
	data, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "archmerge: %v\n", err)
		os.Exit(2)
	}
	data = append(data, '\n')

	if *out == "" {
		os.Stdout.Write(data)
	} else if err := ioutil.WriteFile(*out, data, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "archmerge: %v\n", err)
		os.Exit(2)
	}

	fmt.Fprintf(os.Stderr, "archmerge: %d entries, %d conflicts\n",
		len(res.Entries), len(res.Conflicts))
	if len(res.Conflicts) > 0 {
		os.Exit(1)
	}
}
