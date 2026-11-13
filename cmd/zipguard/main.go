package main

import (
	"fmt"
	"os"

	"example.org/netsec/q0339"
)

func main() {
	if len(os.Args) < 3 {
		usage()
	}

	switch os.Args[1] {
	case "check", "validate":
		if err := netsec.ValidateFile(os.Args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("safe")
	case "list":
		archive, err := netsec.DecodeFile(os.Args[2])
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, entry := range archive {
			fmt.Printf("%s\t%s\n", entry.Path, entry.EffectiveName())
		}
	case "make", "make-malicious":
		target := netsec.DefaultAbsoluteTarget
		if len(os.Args) >= 4 {
			target = os.Args[3]
		}
		archive, err := netsec.BuildAbsoluteLinkArchive(target)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := os.WriteFile(os.Args[2], archive, 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	default:
		usage()
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: zipguard check|list <archive>")
	fmt.Fprintln(os.Stderr, "       zipguard make <archive> [absolute-link-target]")
	os.Exit(2)
}
