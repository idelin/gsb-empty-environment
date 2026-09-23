// Command sar packs, lists and extracts SAR archives.
//
//	sar pack <dir> <out.sar>     create archive (fails if out.sar exists)
//	sar list <archive>           list entries
//	sar unpack <archive> <dir>   extract (fails if dir exists)
package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"safearch"
)

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  sar pack <dir> <out.sar>     create archive (refuses to overwrite)
  sar list <archive>           list entries
  sar unpack <archive> <dir>   extract into new dir (refuses to overwrite)`)
	os.Exit(2)
}

func main() {
	if len(os.Args) < 3 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "pack":
		if len(os.Args) != 4 {
			usage()
		}
		err = safearch.PackFile(os.Args[2], os.Args[3])
	case "list":
		if len(os.Args) != 3 {
			usage()
		}
		err = list(os.Args[2])
	case "unpack":
		if len(os.Args) != 4 {
			usage()
		}
		err = unpack(os.Args[2], os.Args[3])
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sar:", err)
		os.Exit(1)
	}
}

func list(archive string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := safearch.List(f, safearch.Limits{})
	if err != nil {
		return err
	}
	for _, e := range entries {
		kind := "file"
		if e.Dir {
			kind = "dir "
		}
		fmt.Printf("%s %s %8d  %s\n", kind, fs.FileMode(e.Mode).String(), e.Size, e.Name)
	}
	fmt.Printf("%d entries\n", len(entries))
	return nil
}

func unpack(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := safearch.Unpack(f, dest, safearch.Limits{}); err != nil {
		if errors.Is(err, safearch.ErrExists) {
			return fmt.Errorf("destination exists, refusing to overwrite: %w", err)
		}
		return err
	}
	fmt.Println("extracted to", dest)
	return nil
}
