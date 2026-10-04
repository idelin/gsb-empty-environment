// Command carc creates and extracts custom archives.
//
//   carc create  <archive> <path>...
//   carc extract <archive> <dest>
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"

	"carc"
)

func usage() {
	fmt.Fprintln(os.Stderr, "usage: carc create <archive> <path>... | carc extract <archive> <dest>")
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "create":
		err = cmdCreate(os.Args[2:])
	case "extract":
		err = cmdExtract(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "carc:", err)
		os.Exit(1)
	}
}

func cmdCreate(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: carc create <archive> <path>...")
	}
	out, err := os.Create(args[0])
	if err != nil {
		return err
	}
	w, err := carc.NewWriter(out)
	if err != nil {
		out.Close()
		return err
	}
	for _, p := range args[1:] {
		base := filepath.Dir(p)
		err = filepath.Walk(p, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(base, path)
			if err != nil {
				return err
			}
			name := filepath.ToSlash(rel)
			switch {
			case info.IsDir():
				return w.AddDir(name)
			case info.Mode()&os.ModeSymlink != 0:
				target, err := os.Readlink(path)
				if err != nil {
					return err
				}
				return w.AddSymlink(name, target)
			case info.Mode().IsRegular():
				data, err := ioutil.ReadFile(path)
				if err != nil {
					return err
				}
				return w.AddFile(name, data)
			default:
				return fmt.Errorf("unsupported file type: %s", path)
			}
		})
		if err != nil {
			out.Close()
			return err
		}
	}
	return out.Close()
}

func cmdExtract(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: carc extract <archive> <dest>")
	}
	return carc.Extract(args[0], args[1])
}
