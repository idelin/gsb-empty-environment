// Command packer incrementally packages a directory tree according to an
// ordered list of include/exclude rules.
//
// Usage:
//
//	packer -root DIR -rules FILE [-manifest FILE] [-default-exclude] [-tar OUT] [-v]
//
// Every packaged entry is printed with the rule that decided it. With
// -manifest, only entries added or changed since the recorded run are
// packaged, deletions are reported, and the manifest is updated.
package main

import (
	"archive/tar"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"packer"
)

func main() {
	var (
		root           = flag.String("root", ".", "directory to package")
		rulesFile      = flag.String("rules", "", "file with ordered '+ pattern' / '- pattern' rules")
		manifestPath   = flag.String("manifest", "", "manifest file enabling incremental packaging")
		defaultExclude = flag.Bool("default-exclude", false, "exclude paths matched by no rule")
		tarOut         = flag.String("tar", "", "also write packaged entries to this tar archive")
		verbose        = flag.Bool("v", false, "print traversal statistics to stderr")
	)
	flag.Parse()

	rs := &packer.RuleSet{DefaultInclude: !*defaultExclude}
	if *rulesFile != "" {
		rules, err := packer.LoadRulesFile(*rulesFile)
		if err != nil {
			fatal(err)
		}
		rs.Rules = rules
	}
	rs.Build()

	info, err := os.Lstat(*root)
	if err != nil {
		fatal(err)
	}
	entries, stats, err := packer.Collect(packer.NewFSNode(*root, info), rs, true)
	if err != nil {
		fatal(err)
	}

	cur, err := packer.BuildManifest(*root, entries)
	if err != nil {
		fatal(err)
	}

	byPath := map[string]packer.Entry{}
	for _, e := range entries {
		byPath[e.Path] = e
	}

	var selected, deleted []string
	incremental := false
	var old packer.Manifest
	if *manifestPath != "" {
		old, err = packer.LoadManifest(*manifestPath)
		switch {
		case err == nil:
			incremental = true
			selected, deleted = old.Diff(cur)
		case os.IsNotExist(err):
			selected = sortedPaths(entries)
		default:
			fatal(err)
		}
	} else {
		selected = sortedPaths(entries)
	}

	for _, path := range selected {
		e := byPath[path]
		action := "INC"
		if incremental {
			action = "ADD"
			if _, ok := old.Entries[path]; ok {
				action = "CHG"
			}
		}
		fmt.Printf("%s\t%s\t%s\t%s\n", action, e.Type, e.Path, rs.Describe(e.Rule))
	}
	for _, path := range deleted {
		fmt.Printf("DEL\t%s\n", path)
	}

	if *tarOut != "" {
		var sel []packer.Entry
		for _, path := range selected {
			sel = append(sel, byPath[path])
		}
		if err := writeTar(*root, sel, *tarOut); err != nil {
			fatal(err)
		}
	}

	if *manifestPath != "" {
		if err := packer.SaveManifest(*manifestPath, cur); err != nil {
			fatal(err)
		}
	}

	if *verbose {
		fmt.Fprintf(os.Stderr, "visited=%d pruned=%d packaged=%d deleted=%d\n",
			stats.Visited, stats.Pruned, len(selected), len(deleted))
	}
}

func sortedPaths(entries []packer.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Path)
	}
	return out // walk order is already deterministic and sorted per directory
}

func writeTar(root string, entries []packer.Entry, outPath string) error {
	f, err := os.Create(outPath)
	if err != nil {
		return err
	}
	tw := tar.NewWriter(f)
	for _, e := range entries {
		full := filepath.Join(root, filepath.FromSlash(e.Path))
		info, err := os.Lstat(full)
		if err != nil {
			f.Close()
			return err
		}
		hdr, err := tar.FileInfoHeader(info, e.LinkTarget)
		if err != nil {
			f.Close()
			return err
		}
		hdr.Name = e.Path
		if err := tw.WriteHeader(hdr); err != nil {
			f.Close()
			return err
		}
		if e.Type == packer.EntryFile {
			src, err := os.Open(full)
			if err != nil {
				f.Close()
				return err
			}
			_, err = io.Copy(tw, src)
			src.Close()
			if err != nil {
				f.Close()
				return err
			}
		}
	}
	if err := tw.Close(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "packer:", err)
	os.Exit(1)
}
