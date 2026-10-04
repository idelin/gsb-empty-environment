package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
)

func usage() {
	fmt.Fprintf(os.Stderr, `integer script VM - no host calls, gas-metered, pausable

usage:
  vm run        -prog file.vm [-budget N]        run to completion
  vm checkpoint -prog file.vm [-budget N] -steps K -out snap.json
                                                 run K steps, save snapshot
  vm resume     -snap snap.json                  resume a snapshot to completion
  vm verify     -prog file.vm [-budget N]        compare single-run vs resume
                                                 at every breakpoint

budget 0 (default) means unlimited gas.
`)
	os.Exit(2)
}

func loadProgram(path string) *Program {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		fatal(err)
	}
	prog, err := Assemble(string(data))
	if err != nil {
		fatal(fmt.Errorf("%s: %v", path, err))
	}
	return prog
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func printResult(r Result) {
	fmt.Println("output:")
	for _, l := range r.Output {
		fmt.Println(" ", l)
	}
	if r.Err == "" {
		fmt.Println("error: <none>")
	} else {
		fmt.Println("error:", r.Err)
	}
	fmt.Println("gas:", r.Gas)
}

// snapFile is the on-disk checkpoint format; it records the program path so
// `resume` can reload the code in a fresh process.
type snapFile struct {
	Prog  string          `json:"prog"`
	State json.RawMessage `json:"state"`
}

func main() {
	if len(os.Args) < 2 {
		usage()
	}
	switch os.Args[1] {
	case "run":
		fs := flag.NewFlagSet("run", flag.ExitOnError)
		progPath := fs.String("prog", "", "program file")
		budget := fs.Int64("budget", 0, "gas budget (0 = unlimited)")
		fs.Parse(os.Args[2:])
		prog := loadProgram(*progPath)
		printResult(runOnce(prog, *budget))

	case "checkpoint":
		fs := flag.NewFlagSet("checkpoint", flag.ExitOnError)
		progPath := fs.String("prog", "", "program file")
		budget := fs.Int64("budget", 0, "gas budget (0 = unlimited)")
		steps := fs.Int64("steps", 0, "instructions to execute before pausing")
		out := fs.String("out", "", "snapshot output file")
		fs.Parse(os.Args[2:])
		if *out == "" {
			fatal(fmt.Errorf("-out is required"))
		}
		prog := loadProgram(*progPath)
		vm := NewVM(prog, *budget)
		for i := int64(0); i < *steps && !vm.Done(); i++ {
			vm.Step()
		}
		state, err := vm.Snapshot()
		if err != nil {
			fatal(err)
		}
		data, err := json.MarshalIndent(snapFile{Prog: *progPath, State: state}, "", "  ")
		if err != nil {
			fatal(err)
		}
		if err := ioutil.WriteFile(*out, data, 0644); err != nil {
			fatal(err)
		}
		fmt.Printf("paused after %d gas, snapshot written to %s\n", vm.Gas(), *out)

	case "resume":
		fs := flag.NewFlagSet("resume", flag.ExitOnError)
		snapPath := fs.String("snap", "", "snapshot file")
		fs.Parse(os.Args[2:])
		data, err := ioutil.ReadFile(*snapPath)
		if err != nil {
			fatal(err)
		}
		var sf snapFile
		if err := json.Unmarshal(data, &sf); err != nil {
			fatal(err)
		}
		prog := loadProgram(sf.Prog)
		vm, err := RestoreVM(prog, sf.State)
		if err != nil {
			fatal(err)
		}
		vm.Run()
		printResult(vm.Result())

	case "verify":
		fs := flag.NewFlagSet("verify", flag.ExitOnError)
		progPath := fs.String("prog", "", "program file")
		budget := fs.Int64("budget", 0, "gas budget (0 = unlimited)")
		fs.Parse(os.Args[2:])
		prog := loadProgram(*progPath)
		problems := Verify(prog, *budget)
		base := runOnce(prog, *budget)
		if len(problems) > 0 {
			for _, p := range problems {
				fmt.Println("MISMATCH:", p)
			}
			os.Exit(1)
		}
		fmt.Printf("OK: %d breakpoints + chained, output=%v err=%q gas=%d\n",
			base.Gas+1, base.Output, base.Err, base.Gas)

	default:
		usage()
	}
}
