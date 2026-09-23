// Command safearchive packs and unpacks SAR1 archives with strict
// verification and safe extraction semantics.
//
//	safearchive pack   <archive> <file-or-dir>...
//	safearchive unpack <archive> <dest-dir>
//	safearchive verify <archive>
package main

import (
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
)

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "pack":
		err = cmdPack(os.Args[2:])
	case "unpack":
		err = cmdUnpack(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: safearchive pack <archive> <file-or-dir>... | unpack <archive> <dest-dir> | verify <archive>")
	os.Exit(2)
}

func cmdPack(args []string) error {
	if len(args) < 2 {
		usage()
	}
	archivePath := args[0]
	roots := args[1:]

	var entries []Entry
	for _, root := range roots {
		rootInfo, err := os.Lstat(root)
		if err != nil {
			return err
		}
		if rootInfo.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to pack symlink: %q", root)
		}
		base := filepath.Base(filepath.Clean(root))
		err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.Type()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing to pack symlink: %q", p)
			}
			rel, err := filepath.Rel(root, p)
			if err != nil {
				return err
			}
			arcName := filepath.ToSlash(filepath.Join(base, rel))
			if d.IsDir() {
				return nil // only regular files are stored
			}
			if !d.Type().IsRegular() {
				return fmt.Errorf("refusing to pack non-regular file: %q", p)
			}
			content, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			entries = append(entries, Entry{
				Path:    arcName,
				Size:    uint64(len(content)),
				Digest:  sha256.Sum256(content),
				Content: content,
			})
			return nil
		})
		if err != nil {
			return err
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	f, err := os.Create(archivePath)
	if err != nil {
		return err
	}
	if err := Encode(f, entries); err != nil {
		f.Close()
		os.Remove(archivePath)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(archivePath)
		return err
	}
	fmt.Printf("packed %d entries -> %s\n", len(entries), archivePath)
	return nil
}

func cmdUnpack(args []string) error {
	if len(args) != 2 {
		usage()
	}
	entries, err := readArchive(args[0])
	if err != nil {
		return err
	}
	if err := Extract(entries, args[1]); err != nil {
		return err
	}
	fmt.Printf("unpacked %d entries -> %s\n", len(entries), args[1])
	return nil
}

func cmdVerify(args []string) error {
	if len(args) != 1 {
		usage()
	}
	entries, err := readArchive(args[0])
	if err != nil {
		return err
	}
	fmt.Printf("OK: %d entries verified\n", len(entries))
	return nil
}

func readArchive(p string) ([]Entry, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := Decode(io.Reader(f))
	if err != nil {
		return nil, err
	}
	return entries, nil
}
