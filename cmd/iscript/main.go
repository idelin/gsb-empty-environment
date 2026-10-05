// Command iscript assembles and runs an integer script.
//
// Usage:
//
//	iscript [-budget N] [-quantum N] file.is
//
// With -quantum > 0 the VM runs in slices of at most N instructions and
// its full state (call stack, locals, output, cost) is serialized to JSON
// and restored at every slice boundary, demonstrating pause/resume.
package main

import (
	"flag"
	"fmt"
	"os"

	"iscript/interp"
)

func main() {
	budget := flag.Int64("budget", -1, "cost budget (-1 = unlimited)")
	quantum := flag.Int64("quantum", 0, "max instructions per slice; 0 = run to completion")
	flag.Parse()
	if flag.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: iscript [-budget N] [-quantum N] file.is")
		os.Exit(2)
	}
	src, err := os.ReadFile(flag.Arg(0))
	if err != nil {
		fmt.Fprintln(os.Stderr, "read:", err)
		os.Exit(1)
	}
	prog, err := interp.Assemble(string(src))
	if err != nil {
		fmt.Fprintln(os.Stderr, "assemble:", err)
		os.Exit(1)
	}

	vm := interp.NewVM(prog, *budget)
	slices := 0
	for vm.Status() == interp.StatusReady || vm.Status() == interp.StatusPaused {
		if *quantum > 0 {
			vm.RunQuantum(*quantum)
		} else {
			vm.Run()
		}
		if vm.Status() != interp.StatusPaused {
			break
		}
		data, err := vm.Snapshot()
		if err != nil {
			fmt.Fprintln(os.Stderr, "snapshot:", err)
			os.Exit(1)
		}
		vm, err = interp.Restore(prog, data)
		if err != nil {
			fmt.Fprintln(os.Stderr, "restore:", err)
			os.Exit(1)
		}
		slices++
	}

	for _, v := range vm.Output() {
		fmt.Println(v)
	}
	fmt.Fprintf(os.Stderr, "status=%s cost=%d slices=%d", vm.Status(), vm.Cost(), slices)
	if fe := vm.FirstError(); fe != nil {
		fmt.Fprintf(os.Stderr, " firstError=%v", fe)
	}
	fmt.Fprintln(os.Stderr)
	if vm.Status() == interp.StatusFailed {
		os.Exit(1)
	}
}
