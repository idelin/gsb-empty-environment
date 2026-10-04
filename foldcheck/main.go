package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	tablePath := flag.String("table", "", "fold table JSON file, {\"from\": \"to\", ...} (required)")
	pathsFile := flag.String("paths", "", "manifest file: one entry per line, 'F <path>' / 'D <path>', or bare path (trailing '/' = directory)")
	root := flag.String("root", "", "directory to walk instead of -paths")
	asJSON := flag.Bool("json", false, "emit JSON instead of text")
	flag.Parse()

	if *tablePath == "" {
		fmt.Fprintln(os.Stderr, "error: -table is required")
		os.Exit(2)
	}
	tf, err := os.Open(*tablePath)
	if err != nil {
		fatal(err)
	}
	defer tf.Close()
	table, err := LoadTable(tf)
	if err != nil {
		fatal(err)
	}

	var entries []Entry
	switch {
	case *root != "":
		entries, err = walkRoot(*root)
	case *pathsFile != "":
		entries, err = readPaths(*pathsFile)
	default:
		fmt.Fprintln(os.Stderr, "error: one of -paths or -root is required")
		os.Exit(2)
	}
	if err != nil {
		fatal(err)
	}

	groups := FindConflicts(entries, table)
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if groups == nil {
			groups = []Group{}
		}
		enc.Encode(groups)
	} else {
		for _, g := range groups {
			fmt.Printf("CONFLICT %-8s %s\n", g.Reason, g.Norm)
			for _, o := range g.Origins {
				kind := "F"
				if o.Dir {
					kind = "D"
				}
				fmt.Printf("  %s %s\n", kind, o.Path)
			}
		}
		if len(groups) == 0 {
			fmt.Println("OK: no conflicts")
		}
	}
	if len(groups) > 0 {
		os.Exit(1)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(2)
}

func walkRoot(root string) ([]Entry, error) {
	var out []Entry
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		out = append(out, Entry{Path: filepath.ToSlash(rel), Dir: d.IsDir()})
		return nil
	})
	return out, err
}

func readPaths(file string) ([]Entry, error) {
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSuffix(sc.Text(), "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e Entry
		switch {
		case strings.HasPrefix(line, "F "):
			e.Path = line[2:]
		case strings.HasPrefix(line, "D "):
			e.Path, e.Dir = line[2:], true
		case strings.HasSuffix(line, "/"):
			e.Path, e.Dir = strings.TrimSuffix(line, "/"), true
		default:
			e.Path = line
		}
		if e.Path != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}
