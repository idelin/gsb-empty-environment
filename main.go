package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"strconv"
	"strings"
)

func fail(format string, args ...interface{}) {
	fmt.Fprintf(os.Stderr, "muttest: "+format+"\n", args...)
	os.Exit(2)
}

// parseDomain parses "lo..hi" into the inclusive value list.
func parseDomain(spec string) ([]int8, error) {
	parts := strings.Split(spec, "..")
	if len(parts) != 2 {
		return nil, fmt.Errorf("bad domain %q (want lo..hi, e.g. -8..7)", spec)
	}
	lo, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
	hi, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err1 != nil || err2 != nil || lo < -128 || hi > 127 || lo > hi {
		return nil, fmt.Errorf("bad domain %q (bounds must satisfy -128 <= lo <= hi <= 127)", spec)
	}
	vals := make([]int8, 0, hi-lo+1)
	for v := lo; v <= hi; v++ {
		vals = append(vals, int8(v))
	}
	return vals, nil
}

// parseTests reads a test suite: one test per line, either positional
// ("3, -4", in input declaration order) or named ("a=3, b=-4").
func parseTests(src string, inputs []string) ([]map[string]int8, error) {
	var tests []map[string]int8
	for ln, line := range strings.Split(src, "\n") {
		if i := strings.IndexByte(line, 35); i >= 0 { // strip # comments
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Split(line, ",")
		t := make(map[string]int8, len(fields))
		named := strings.Contains(fields[0], "=")
		for i, f := range fields {
			f = strings.TrimSpace(f)
			name, val := "", f
			if named {
				kv := strings.SplitN(f, "=", 2)
				if len(kv) != 2 {
					return nil, fmt.Errorf("tests line %d: bad field %q", ln+1, f)
				}
				name, val = strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
			} else {
				if i >= len(inputs) {
					return nil, fmt.Errorf("tests line %d: more values than inputs", ln+1)
				}
				name = inputs[i]
			}
			n, err := strconv.Atoi(val)
			if err != nil || n < -128 || n > 127 {
				return nil, fmt.Errorf("tests line %d: value %q out of range (-128..127)", ln+1, val)
			}
			t[name] = int8(n)
		}
		for _, in := range inputs {
			if _, ok := t[in]; !ok {
				return nil, fmt.Errorf("tests line %d: missing value for input %q", ln+1, in)
			}
		}
		tests = append(tests, t)
	}
	return tests, nil
}

func fmtInputs(inputs []string, m map[string]int8) string {
	parts := make([]string, len(inputs))
	for i, n := range inputs {
		v, ok := m[n]
		if !ok {
			parts[i] = n + "=?"
		} else {
			parts[i] = fmt.Sprintf("%s=%d", n, v)
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func main() {
	progPath := flag.String("prog", "", "program file (required)")
	testsPath := flag.String("tests", "", "test suite file (optional)")
	domainSpec := flag.String("domain", "-128..127", "input domain per variable, lo..hi within [-128,127]")
	flag.Parse()
	if *progPath == "" {
		fmt.Fprintln(os.Stderr, "usage: muttest -prog FILE [-tests FILE] [-domain lo..hi]")
		os.Exit(2)
	}

	src, err := ioutil.ReadFile(*progPath)
	if err != nil {
		fail("reading program: %v", err)
	}
	prog, err := parse(string(src))
	if err != nil {
		fail("parsing program: %v", err)
	}
	inputs := inputNames(prog)

	var tests []map[string]int8
	if *testsPath != "" {
		tsrc, err := ioutil.ReadFile(*testsPath)
		if err != nil {
			fail("reading tests: %v", err)
		}
		tests, err = parseTests(string(tsrc), inputs)
		if err != nil {
			fail("%v", err)
		}
	}

	values, err := parseDomain(*domainSpec)
	if err != nil {
		fail("%v", err)
	}
	combos := 1
	for range inputs {
		combos *= len(values)
	}
	fmt.Printf("inputs: %s | domain: %d values each (%d combinations) | tests: %d\n",
		strings.Join(inputs, ", "), len(values), combos, len(tests))

	results, err := analyze(prog, tests, values)
	if err != nil {
		fail("%v", err)
	}

	groups := map[Status][]Result{Killed: nil, Survived: nil, Equivalent: nil}
	for _, r := range results {
		groups[r.Status] = append(groups[r.Status], r)
	}
	order := []struct {
		st    Status
		title string
	}{
		{Killed, "KILLED by test suite"},
		{Survived, "SURVIVED (non-equivalent, counterexample given)"},
		{Equivalent, "EQUIVALENT"},
	}
	for _, g := range order {
		rs := groups[g.st]
		fmt.Printf("\n== %s: %d ==\n", g.title, len(rs))
		for _, r := range rs {
			switch g.st {
			case Killed:
				fmt.Printf("  M%-3d %s\n       test %s: original=%d mutant=%d\n",
					r.Mutant.ID, r.Mutant.Desc, fmtInputs(inputs, r.Witness), r.Orig, r.Mut)
			case Survived:
				fmt.Printf("  M%-3d %s\n       counterexample %s: original=%d mutant=%d\n",
					r.Mutant.ID, r.Mutant.Desc, fmtInputs(inputs, r.Witness), r.Orig, r.Mut)
			default:
				fmt.Printf("  M%-3d %s\n", r.Mutant.ID, r.Mutant.Desc)
			}
		}
	}

	total := len(results)
	killed := len(groups[Killed])
	equiv := len(groups[Equivalent])
	fmt.Printf("\nmutants: %d | killed: %d | survived: %d | equivalent: %d\n",
		total, killed, len(groups[Survived]), equiv)
	if total > equiv {
		fmt.Printf("mutation score (excl. equivalent): %d/%d = %.1f%%\n",
			killed, total-equiv, 100*float64(killed)/float64(total-equiv))
	}

	// Exit 1 if any mutant survived: the test suite is insufficient.
	if len(groups[Survived]) > 0 {
		os.Exit(1)
	}
}
