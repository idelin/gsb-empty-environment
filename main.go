package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

var boundarySet = []int8{-128, -127, -2, -1, 0, 1, 2, 126, 127}

func fullSet() []int8 {
	s := make([]int8, 0, 256)
	for i := -128; i <= 127; i++ {
		s = append(s, int8(i))
	}
	return s
}

func loadTests(path string, ninputs int) ([][]int8, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tests [][]int8
	for ln, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Split(line, ",")
		if len(parts) != ninputs {
			return nil, fmt.Errorf("%s:%d: expected %d values, got %d", path, ln+1, ninputs, len(parts))
		}
		row := make([]int8, ninputs)
		for i, p := range parts {
			v, err := strconv.ParseInt(strings.TrimSpace(p), 10, 8)
			if err != nil {
				return nil, fmt.Errorf("%s:%d: value %q is not a signed 8-bit integer", path, ln+1, strings.TrimSpace(p))
			}
			row[i] = int8(v)
		}
		tests = append(tests, row)
	}
	return tests, nil
}

func formatInput(names []string, in []int8) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = fmt.Sprintf("%s=%d", n, in[i])
	}
	return strings.Join(parts, ", ")
}

func main() {
	testsFile := flag.String("tests", "", "file with test inputs, one comma-separated row per line")
	domainKind := flag.String("domain", "boundary", "input domain per variable: boundary | full")
	maxComb := flag.Int64("max", 4000000, "maximum number of input combinations to enumerate")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "usage: mutest [-tests file] [-domain boundary|full] [-max n] program.mut\n")
		flag.PrintDefaults()
	}
	flag.Parse()
	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	data, err := ioutil.ReadFile(flag.Arg(0))
	if err != nil {
		fatal(err)
	}
	prog, err := Parse(string(data))
	if err != nil {
		fatal(err)
	}

	var tests [][]int8
	if *testsFile != "" {
		tests, err = loadTests(*testsFile, len(prog.Inputs))
		if err != nil {
			fatal(err)
		}
	}

	var set []int8
	switch *domainKind {
	case "boundary":
		set = boundarySet
	case "full":
		set = fullSet()
	default:
		fatal(fmt.Errorf("unknown domain %q (want boundary|full)", *domainKind))
	}
	sets := make([][]int8, len(prog.Inputs))
	for i := range sets {
		sets[i] = set
	}
	dom := NewDomain(sets)
	if dom.Total() > *maxComb {
		fatal(fmt.Errorf("input domain has %d combinations (limit %d); use -domain boundary or raise -max", dom.Total(), *maxComb))
	}

	mutants := GenerateMutants(prog)
	rep := Classify(prog, mutants, tests, dom)

	fmt.Printf("program: %s\n", flag.Arg(0))
	fmt.Printf("inputs: %s; domain: %s (%d combinations); tests: %d\n",
		strings.Join(prog.Inputs, ", "), *domainKind, dom.Total(), len(tests))
	fmt.Printf("mutants: %d total, %d killed, %d equivalent, %d surviving\n",
		len(mutants), len(rep.Killed), len(rep.Equivalent), len(rep.Survived))
	if len(tests) > 0 && len(mutants) > len(rep.Equivalent) {
		fmt.Printf("mutation score: %d/%d = %.1f%% (equivalent mutants excluded)\n",
			len(rep.Killed), len(mutants)-len(rep.Equivalent),
			100*float64(len(rep.Killed))/float64(len(mutants)-len(rep.Equivalent)))
	}

	fmt.Printf("\n== killed by tests (%d) ==\n", len(rep.Killed))
	for _, k := range rep.Killed {
		fmt.Printf("  #%d %s %s\n      killed by input: %s\n",
			k.M.ID, k.M.Op, k.M.Desc, formatInput(prog.Inputs, k.Input))
	}

	fmt.Printf("\n== equivalent (%d) ==\n", len(rep.Equivalent))
	for _, m := range rep.Equivalent {
		fmt.Printf("  #%d %s %s\n", m.ID, m.Op, m.Desc)
	}

	fmt.Printf("\n== surviving, not equivalent (%d) ==\n", len(rep.Survived))
	for _, s := range rep.Survived {
		ov, oe := Run(prog, s.Counterexample)
		mv, me := Run(s.M.Prog, s.Counterexample)
		fmt.Printf("  #%d %s %s\n      counterexample: %s\n      original: %s; mutant: %s\n",
			s.M.ID, s.M.Op, s.M.Desc,
			formatInput(prog.Inputs, s.Counterexample),
			formatOutcome(ov, oe), formatOutcome(mv, me))
	}
}

func formatOutcome(v interface{}, err error) string {
	if err != nil {
		return "error: " + err.Error()
	}
	return fmt.Sprintf("%v", v)
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
