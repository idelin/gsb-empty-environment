// Command pubtool is a small CLI around the publish package.
//
//	pubtool -root <dir> publish <dir-or-file>...
//	pubtool -root <dir> current
//	pubtool -root <dir> list
//	pubtool -root <dir> cat <file>
//	pubtool -root <dir> report
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	publish "pubtool"
)

func main() {
	root := flag.String("root", "data", "store root directory")
	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pubtool [-root dir] <publish|current|list|cat|report> [args]")
		os.Exit(2)
	}

	store, err := publish.Open(*root)
	if err != nil {
		fatal(err)
	}
	warnRecovery(store)

	switch args[0] {
	case "publish":
		if len(args) < 2 {
			fatal(fmt.Errorf("publish needs at least one file or directory"))
		}
		files, err := collect(args[1:])
		if err != nil {
			fatal(err)
		}
		id, err := store.Publish(files)
		if err != nil {
			fatal(err)
		}
		fmt.Printf("published %s (%d files)\n", id, len(files))
	case "current":
		id := store.Current()
		if id == "" {
			fmt.Println("(no committed version)")
		} else {
			fmt.Println(id)
		}
	case "list":
		sn, err := store.Acquire()
		if err != nil {
			fatal(err)
		}
		fmt.Println(sn.Version)
		for _, name := range sn.Files() {
			fmt.Printf("  %s\n", name)
		}
	case "cat":
		if len(args) != 2 {
			fatal(fmt.Errorf("cat needs exactly one file name"))
		}
		sn, err := store.Acquire()
		if err != nil {
			fatal(err)
		}
		data, err := sn.ReadFile(args[1])
		if err != nil {
			fatal(err)
		}
		os.Stdout.Write(data)
	case "report":
		rep := store.Report()
		fmt.Printf("current:            %q\n", rep.Recovered)
		fmt.Printf("bad pointer:        %v (target %q)\n", rep.BadPointer, rep.PointerTarget)
		fmt.Printf("uncommitted staged: %v\n", rep.UncommittedStaging)
		fmt.Printf("quarantined:        %v\n", rep.Quarantined)
		fmt.Printf("unknown (untouched): %v\n", rep.UnknownEntries)
	default:
		fatal(fmt.Errorf("unknown command %q", args[0]))
	}
}

func collect(paths []string) (map[string][]byte, error) {
	files := make(map[string][]byte)
	add := func(path string) error {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.Base(path)] = data
		return nil
	}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if !info.IsDir() {
			if err := add(p); err != nil {
				return nil, err
			}
			continue
		}
		entries, err := os.ReadDir(p)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if err := add(filepath.Join(p, e.Name())); err != nil {
				return nil, err
			}
		}
	}
	return files, nil
}

func warnRecovery(s *publish.Store) {
	rep := s.Report()
	if rep.BadPointer {
		fmt.Fprintf(os.Stderr, "warning: bad pointer (target %q), recovered to %q\n", rep.PointerTarget, rep.Recovered)
	}
	if len(rep.UncommittedStaging) > 0 {
		fmt.Fprintf(os.Stderr, "warning: quarantined uncommitted staging dirs: %v\n", rep.UncommittedStaging)
	}
	if len(rep.UnknownEntries) > 0 {
		fmt.Fprintf(os.Stderr, "warning: unknown entries left untouched: %v\n", rep.UnknownEntries)
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "pubtool:", err)
	os.Exit(1)
}
