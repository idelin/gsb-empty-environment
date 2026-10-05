// Command fdtool normalizes functional dependencies, computes attribute
// closures, and checks FD-set equivalence, printing derivation evidence
// for every deletion it performs.
//
// Usage:
//
//	fdtool canonical  [-f file]      canonical cover of the FDs (stdin or file)
//	fdtool closure    [-f file] A,B  attribute closure of the given attributes
//	fdtool equiv      fileA fileB    equivalence check between two FD sets
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"

	fd "fdtool"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "canonical":
		err = cmdCanonical(os.Args[2:])
	case "closure":
		err = cmdClosure(os.Args[2:])
	case "equiv":
		err = cmdEquiv(os.Args[2:])
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
	fmt.Fprintln(os.Stderr, `usage:
  fdtool canonical [-f file]      canonical cover (split RHS, extraneous LHS, redundant FDs)
  fdtool closure   [-f file] A,B  attribute closure with derivation trace
  fdtool equiv     fileA fileB    check whether two FD sets are equivalent
input: one FD per line or ';'-separated, e.g. "AB -> C", "{} -> A", "A,B -> CD"`)
}

func readInput(file string) (string, error) {
	if file == "" || file == "-" {
		b, err := ioutil.ReadAll(os.Stdin)
		return string(b), err
	}
	b, err := ioutil.ReadFile(file)
	return string(b), err
}

func parseFrom(file string) ([]fd.FD, error) {
	text, err := readInput(file)
	if err != nil {
		return nil, err
	}
	return fd.ParseFDs(text)
}

func cmdCanonical(args []string) error {
	fs := flag.NewFlagSet("canonical", flag.ContinueOnError)
	file := fs.String("f", "", "input file (default stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	fds, err := parseFrom(*file)
	if err != nil {
		return err
	}
	rep := fd.Normalize(fds)
	fmt.Println("== normalization log ==")
	for i, e := range rep.Log {
		fmt.Printf("[%d] %s: %s\n", i+1, e.Kind, e.Detail)
		if len(e.Evidence.Steps) > 0 {
			fmt.Println(fd.Indent(e.Evidence.Render()))
		}
	}
	fmt.Println("== canonical cover ==")
	for _, f := range rep.Canonical {
		fmt.Println(f)
	}
	return nil
}

func cmdClosure(args []string) error {
	fs := flag.NewFlagSet("closure", flag.ContinueOnError)
	file := fs.String("f", "", "input file (default stdin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("closure needs exactly one attribute list, e.g. \"A,B\"")
	}
	fds, err := parseFrom(*file)
	if err != nil {
		return err
	}
	attrs, err := fd.ParseAttrs(fs.Arg(0))
	if err != nil {
		return err
	}
	closure, trace := fd.Closure(attrs, fds)
	fmt.Println(trace.Render())
	fmt.Println("closure:", closure)
	return nil
}

func cmdEquiv(args []string) error {
	fs := flag.NewFlagSet("equiv", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 2 {
		return fmt.Errorf("equiv needs exactly two input files")
	}
	f, err := parseFrom(fs.Arg(0))
	if err != nil {
		return err
	}
	g, err := parseFrom(fs.Arg(1))
	if err != nil {
		return err
	}
	rep := fd.Equivalent(f, g)
	for _, p := range rep.Proofs {
		fmt.Println(p)
	}
	for _, fail := range rep.Failures {
		fmt.Println("MISMATCH:", fail)
	}
	if rep.Equivalent {
		fmt.Println("EQUIVALENT")
	} else {
		fmt.Println("NOT EQUIVALENT")
		os.Exit(1)
	}
	return nil
}
