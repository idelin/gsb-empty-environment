// fdtool normalizes functional dependencies and checks equivalence.
//
//	fdtool cover              read FDs from stdin, print canonical cover + evidence
//	fdtool closure A,B        read FDs from stdin, print the closure of the given attributes
//	fdtool equiv              read two FD sets separated by a "---" line, check equivalence
//
// Dependency syntax (one per line):
//
//	A,B -> C      comma/space separated attribute names
//	AB -> C       bare run of single uppercase letters (textbook style)
//	{} -> A       empty left-hand side (also "-> A" or "∅ -> A")
package main

import (
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"fdtool/fd"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "cover":
		err = runCover()
	case "closure":
		err = runClosure(os.Args[2:])
	case "equiv":
		err = runEquiv()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `usage:
  fdtool cover            canonical cover of FDs from stdin, with deletion evidence
  fdtool closure A,B      attribute closure of the given attributes
  fdtool equiv            equivalence of two FD sets separated by a "---" line
`)
}

func readStdin() (string, error) {
	data, err := ioutil.ReadAll(os.Stdin)
	return string(data), err
}

func runCover() error {
	input, err := readStdin()
	if err != nil {
		return err
	}
	fds, err := fd.ParseFDs(input)
	if err != nil {
		return err
	}
	res := fd.CanonicalCover(fds)

	fmt.Printf("input: %d dependencies\n", len(fds))
	for _, f := range fds {
		fmt.Println("  ", f)
	}
	fmt.Printf("\ncanonical cover: %d dependencies\n", len(res.Cover))
	for _, f := range res.Cover {
		fmt.Println("  ", f)
	}

	fmt.Printf("\nchange log (%d entries):\n", len(res.Changes))
	for _, c := range res.Changes {
		fmt.Printf("  [%s] %s\n", c.Kind, c.Reason)
		if len(c.Trace) > 0 {
			fmt.Println("    evidence: " + fd.FormatTrace(c.TraceFrom, c.Trace))
		}
	}

	ok := fd.CoverEquivalent(fds, res)
	fmt.Printf("\nself-check: cover equivalent to input: %v\n", ok)
	if !ok {
		return fmt.Errorf("canonical cover is NOT equivalent to the input")
	}
	return nil
}

func runClosure(args []string) error {
	if len(args) != 1 {
		return fmt.Errorf("closure needs exactly one attribute list, e.g. \"fdtool closure A,B\"")
	}
	input, err := readStdin()
	if err != nil {
		return err
	}
	fds, err := fd.ParseFDs(input)
	if err != nil {
		return err
	}
	seed := fd.NewFD(strings.Split(args[0], ","), []string{"_"}).LHS
	closure, trace := fd.ClosureTrace(seed, fds)
	fmt.Printf("{%s}+ = {%s}\n", strings.Join(seed, ","), strings.Join(closure, ","))
	fmt.Println("derivation:")
	fmt.Println("  " + fd.FormatTrace(seed, trace))
	return nil
}

func runEquiv() error {
	input, err := readStdin()
	if err != nil {
		return err
	}
	parts := strings.SplitN(input, "\n---", 2)
	if len(parts) != 2 {
		return fmt.Errorf("separate the two dependency sets with a line containing only ---")
	}
	F, err := fd.ParseFDs(parts[0])
	if err != nil {
		return fmt.Errorf("first set: %v", err)
	}
	G, err := fd.ParseFDs(parts[1])
	if err != nil {
		return fmt.Errorf("second set: %v", err)
	}
	res := fd.CheckEquivalent(F, G)
	fmt.Print(res.Summary())
	if !res.Equivalent {
		os.Exit(1)
	}
	return nil
}
