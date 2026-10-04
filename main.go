package main

import (
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
)

func usage() {
	fmt.Fprintf(os.Stderr, "usage:\n  unarc pack <dir> <archive>\n  unarc unpack <archive> <root>\n")
	os.Exit(2)
}

// packDir walks dir and builds an archive at outPath containing its
// directories, regular files and symlinks (relative to dir).
func packDir(dir, outPath string) error {
	var entries []*entry
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		rel = path.Clean(filepath.ToSlash(rel))
		switch {
		case d.IsDir():
			entries = append(entries, &entry{typ: entDir, path: rel})
		case d.Type()&fs.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			entries = append(entries, &entry{typ: entSymlink, path: rel, target: target})
		case d.Type().IsRegular():
			info, err := d.Info()
			if err != nil {
				return err
			}
			data, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entries = append(entries, &entry{typ: entFile, path: rel, mode: info.Mode(), data: data})
		default:
			return fmt.Errorf("unsupported file type: %s", p)
		}
		return nil
	})
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].path < entries[j].path })
	out, err := os.Create(outPath)
	if err != nil {
		return err
	}
	if err := writeArchive(out, entries); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

func main() {
	if len(os.Args) != 4 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "pack":
		err = packDir(os.Args[2], os.Args[3])
	case "unpack":
		err = extract(os.Args[2], os.Args[3])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "unarc: %v\n", err)
		os.Exit(1)
	}
}
