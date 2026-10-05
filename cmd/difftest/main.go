// Command difftest runs two executors on the same input and classifies
// divergence strictly by outcome class and normalization rules.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"difftest/difftest"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = cmdRun(os.Args[2:])
	case "replay":
		err = cmdReplay(os.Args[2:])
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "difftest: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `usage:
  difftest run    --a "cmdA" --b "cmdB" [--input file|-] [--timeout 2s]
                  [--norm trim,collapse,casefold,sortlines,float,float-strict]
                  [--float-tol 1e-9] [--strip REGEX]...
                  [--cases DIR] [--save-all] [--no-minimize] [--max-min-runs 200]
  difftest replay CASE_DIR
`)
}

type stripFlags []string

func (s *stripFlags) String() string { return strings.Join(*s, ",") }
func (s *stripFlags) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	execAStr := fs.String("a", "", "executor A command (required)")
	execBStr := fs.String("b", "", "executor B command (required)")
	inputPath := fs.String("input", "-", "input file, or - for stdin")
	timeout := fs.Duration("timeout", 2*time.Second, "per-executor timeout")
	norm := fs.String("norm", "trim", "comma list: trim,collapse,casefold,sortlines,float,float-strict")
	floatTol := fs.Float64("float-tol", 1e-9, "float comparison tolerance")
	var strips stripFlags
	fs.Var(&strips, "strip", "regex stripped before comparison (repeatable)")
	casesDir := fs.String("cases", "cases", "directory for saved divergent cases")
	saveAll := fs.Bool("save-all", false, "save every case, not only divergent ones")
	noMinimize := fs.Bool("no-minimize", false, "skip input minimization on divergence")
	maxMinRuns := fs.Int("max-min-runs", 200, "max executor runs during minimization")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *execAStr == "" || *execBStr == "" {
		return fmt.Errorf("--a and --b are required")
	}
	argvA, err := splitCommand(*execAStr)
	if err != nil {
		return fmt.Errorf("--a: %w", err)
	}
	argvB, err := splitCommand(*execBStr)
	if err != nil {
		return fmt.Errorf("--b: %w", err)
	}
	rules, err := parseRules(*norm, *floatTol, strips)
	if err != nil {
		return err
	}
	input, err := readInput(*inputPath)
	if err != nil {
		return err
	}

	execA := difftest.Executor{Name: "A", Argv: argvA, Timeout: difftest.Duration(*timeout)}
	execB := difftest.Executor{Name: "B", Argv: argvB, Timeout: difftest.Duration(*timeout)}

	outcomeA, outcomeB, verdict := runPair(execA, execB, rules, input)
	printReport(verdict, outcomeA, outcomeB)

	if !verdict.Kind.Divergent() && !*saveAll {
		return nil
	}
	finalInput := input
	minimized := false
	if verdict.Kind.Divergent() && !*noMinimize {
		finalInput = difftest.Minimize(input, func(candidate []byte) bool {
			_, _, v := runPair(execA, execB, rules, candidate)
			return v.Kind == verdict.Kind && v.Side == verdict.Side
		}, *maxMinRuns)
		minimized = len(finalInput) < len(input)
		if minimized {
			outcomeA, outcomeB, verdict = runPair(execA, execB, rules, finalInput)
			fmt.Printf("minimized input: %d -> %d bytes\n", len(input), len(finalInput))
		}
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	dir := filepath.Join(*casesDir, fmt.Sprintf("%s-%s", stamp, verdict.Kind))
	if err := difftest.SaveCase(dir, finalInput, execA, execB, rules,
		[2]difftest.Outcome{outcomeA, outcomeB}, verdict, minimized); err != nil {
		return err
	}
	fmt.Println("case saved:", dir)
	return nil
}

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("replay requires a case directory")
	}
	cf, input, err := difftest.LoadCase(fs.Arg(0))
	if err != nil {
		return err
	}
	fmt.Printf("replaying case from %s (tool %s, env %s/%s, timeout %s)\n",
		cf.Env.Timestamp.Format(time.RFC3339), cf.Tool, cf.Env.OS, cf.Env.Arch,
		cf.Executors[0].Timeout)
	outcomeA, outcomeB, verdict := runPair(cf.Executors[0], cf.Executors[1], cf.Rules, input)
	printReport(verdict, outcomeA, outcomeB)
	if verdict.Kind == cf.Verdict.Kind && verdict.Side == cf.Verdict.Side {
		fmt.Println("reproduced: verdict matches recorded", cf.Verdict.Kind)
		return nil
	}
	return fmt.Errorf("NOT reproduced: recorded %s, replayed %s", cf.Verdict, verdict)
}

func runPair(execA, execB difftest.Executor, rules difftest.Rules, input []byte) (difftest.Outcome, difftest.Outcome, difftest.Verdict) {
	ctx := context.Background()
	type res struct {
		out difftest.Outcome
	}
	chA, chB := make(chan res, 1), make(chan res, 1)
	go func() { chA <- res{execA.Run(ctx, input)} }()
	go func() { chB <- res{execB.Run(ctx, input)} }()
	outcomeA, outcomeB := (<-chA).out, (<-chB).out
	return outcomeA, outcomeB, difftest.Judge(&rules, outcomeA, outcomeB)
}

func printReport(verdict difftest.Verdict, outcomeA, outcomeB difftest.Outcome) {
	fmt.Printf("verdict: %s\n", verdict)
	fmt.Printf("  A: kind=%s exit=%d duration=%s\n", outcomeA.Kind, outcomeA.ExitCode, outcomeA.Duration)
	fmt.Printf("  B: kind=%s exit=%d duration=%s\n", outcomeB.Kind, outcomeB.ExitCode, outcomeB.Duration)
}

func readInput(path string) ([]byte, error) {
	if path == "-" {
		return os.ReadFile("/dev/stdin")
	}
	return os.ReadFile(path)
}

func parseRules(norm string, floatTol float64, strips []string) (difftest.Rules, error) {
	rules := difftest.Rules{FloatTol: floatTol, StripPatterns: strips}
	for _, name := range strings.Split(norm, ",") {
		switch strings.TrimSpace(name) {
		case "", "none":
		case "trim":
			rules.TrimSpace = true
		case "collapse":
			rules.CollapseSpace = true
		case "casefold":
			rules.CaseFold = true
		case "sortlines":
			rules.SortLines = true
		case "float":
			rules.FloatMode = true
		case "float-strict":
			rules.FloatMode = true
			rules.FloatStrict = true
		default:
			return rules, fmt.Errorf("unknown normalization rule %q", name)
		}
	}
	if err := rules.Compile(); err != nil {
		return rules, err
	}
	return rules, nil
}

// splitCommand splits a command line into argv, honoring quotes and escapes.
func splitCommand(s string) ([]string, error) {
	var argv []string
	var cur strings.Builder
	inToken := false
	var quote rune
	escaped := false
	flush := func() {
		if inToken {
			argv = append(argv, cur.String())
			cur.Reset()
			inToken = false
		}
	}
	for _, r := range s {
		switch {
		case escaped:
			cur.WriteRune(r)
			escaped = false
		case r == '\\' && quote != '\'':
			escaped = true
			inToken = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '\'' || r == '"':
			quote = r
			inToken = true
		case r == ' ' || r == '\t':
			flush()
		default:
			cur.WriteRune(r)
			inToken = true
		}
	}
	if escaped {
		cur.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote in %q", s)
	}
	flush()
	if len(argv) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return argv, nil
}
