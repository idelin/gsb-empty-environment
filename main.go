package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	aPath := flag.String("a", "", "file describing LTS A")
	bPath := flag.String("b", "", "file describing LTS B")
	flag.Parse()
	if *aPath == "" || *bPath == "" {
		fmt.Fprintln(os.Stderr, "usage: weakbisim -a <fileA> -b <fileB>")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "input format (per file):")
		fmt.Fprintln(os.Stderr, "  <numStates> <startState>     first non-comment line; numStates <= 30")
		fmt.Fprintln(os.Stderr, "  <from> <label> <to>          one transition per line")
		fmt.Fprintln(os.Stderr, "  label 'tau' (or 'τ') is the internal action; anything else is visible")
		os.Exit(2)
	}
	a, err := ParseFile(*aPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading A:", err)
		os.Exit(1)
	}
	b, err := ParseFile(*bPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "reading B:", err)
		os.Exit(1)
	}

	res := Check(a, b)

	fmt.Printf("maximal weak bisimulation (%d pairs):\n", len(res.Pairs))
	byS := make(map[int][]int)
	for _, p := range res.Pairs {
		byS[p.S] = append(byS[p.S], p.T)
	}
	for s := 0; s < a.N; s++ {
		ts, ok := byS[s]
		if !ok {
			continue
		}
		names := make([]string, len(ts))
		for i, t := range ts {
			names[i] = fmt.Sprintf("B%d", t)
		}
		fmt.Printf("  A%-3d <->  { %s }\n", s, strings.Join(names, " "))
	}

	if res.Equivalent {
		fmt.Println("\nverdict: WEAKLY BISIMILAR")
		return
	}

	fmt.Println("\nverdict: NOT weakly bisimilar")
	rounds, err := Extract(a, b, res)
	if err != nil {
		fmt.Fprintln(os.Stderr, "extracting distinguisher:", err)
		os.Exit(1)
	}
	fmt.Println("\ndistinguishing process (attacker/defender game):")
	for i, r := range rounds {
		printRound(i+1, r)
	}
	if err := Replay(a, b, rounds); err != nil {
		fmt.Fprintln(os.Stderr, "\nreplay check FAILED:", err)
		os.Exit(1)
	}
	fmt.Println("\nreplay check: OK (process re-validated against both LTSs)")
}

func printRound(n int, r Round) {
	att, def := "A", "B"
	if r.Side == 1 {
		att, def = "B", "A"
	}
	fmt.Printf("  round %d: attacker %s: %d -%s-> %d\n", n, att, r.From, r.Label, r.To)
	if r.Stuck {
		fmt.Printf("           defender %s: %d is stuck (no weak '%s' move)\n", def, r.DefenderFrom, r.Label)
		return
	}
	fmt.Printf("           defender %s: %d =%s=> %d", def, r.DefenderFrom, r.Label, r.DefenderTo)
	if len(r.DefenderPath) == 0 {
		fmt.Print("  (no steps needed)")
	} else {
		fmt.Print("  via")
		for _, st := range r.DefenderPath {
			fmt.Printf("  %d -%s-> %d", st.From, st.Label, st.To)
		}
	}
	fmt.Println()
}
