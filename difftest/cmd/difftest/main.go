// difftest is a dual-executor differential testing tool. It feeds the same
// input to two executors, records value/error/timeout for each, judges the
// pair strictly by the problem-given normalization rules, and saves a
// minimal replayable sample plus an environment summary.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"difftest/internal/casestore"
	"difftest/internal/faultinj"
	"difftest/internal/judge"
	"difftest/internal/normalize"
	"difftest/internal/runner"
	"difftest/internal/shrink"
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
	case "shrink":
		err = cmdShrink(os.Args[2:])
	case "selftest":
		err = cmdSelftest()
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
  difftest run    --a "cmdA" --b "cmdB" --input file [--timeout 2s] [--rules rules.json] [--save dir]
  difftest replay --case dir
  difftest shrink --a "cmdA" --b "cmdB" --input file [--timeout 2s] [--rules rules.json] --save dir
  difftest selftest
`)
}

type commonFlags struct {
	a, b    string
	input   string
	timeout time.Duration
	rules   string
	save    string
}

func bindCommon(fs *flag.FlagSet, c *commonFlags) {
	fs.StringVar(&c.a, "a", "", "executor A command")
	fs.StringVar(&c.b, "b", "", "executor B command")
	fs.StringVar(&c.input, "input", "", "input file")
	fs.DurationVar(&c.timeout, "timeout", 2*time.Second, "per-executor timeout")
	fs.StringVar(&c.rules, "rules", "", "normalization rules JSON file")
	fs.StringVar(&c.save, "save", "", "directory to save the replayable case")
}

func loadRules(path string) (normalize.Rules, error) {
	// Default: the usual competitive-programming normalization.
	rules := normalize.Rules{
		TrimLineTrailingSpace:    true,
		IgnoreTrailingBlankLines: true,
	}
	if path == "" {
		return rules, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return rules, err
	}
	if err := json.Unmarshal(data, &rules); err != nil {
		return rules, fmt.Errorf("parse rules: %w", err)
	}
	return rules, nil
}

func splitCommand(s string) ([]string, error) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return nil, fmt.Errorf("empty command")
	}
	return fields, nil
}

func runPair(ctx context.Context, execA, execB runner.Executor, input []byte) (runner.Outcome, runner.Outcome) {
	return execA.Run(ctx, input), execB.Run(ctx, input)
}

func printVerdict(v judge.Result) {
	fmt.Printf("verdict: %s", v.Verdict)
	if len(v.Sides) > 0 {
		fmt.Printf(" (sides: %s)", strings.Join(v.Sides, ","))
	}
	fmt.Println()
	if v.Detail != "" {
		fmt.Println("detail:", v.Detail)
	}
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	var c commonFlags
	bindCommon(fs, &c)
	if err := fs.Parse(args); err != nil {
		return err
	}
	rules, err := loadRules(c.rules)
	if err != nil {
		return err
	}
	input, err := os.ReadFile(c.input)
	if err != nil {
		return err
	}
	argvA, err := splitCommand(c.a)
	if err != nil {
		return err
	}
	argvB, err := splitCommand(c.b)
	if err != nil {
		return err
	}
	execA := &runner.CommandExecutor{Argv: argvA, Timeout: c.timeout}
	execB := &runner.CommandExecutor{Argv: argvB, Timeout: c.timeout}

	outA, outB := runPair(context.Background(), execA, execB, input)
	verdict := judge.Judge(outA, outB, rules)
	printVerdict(verdict)

	if c.save != "" {
		kase := casestore.New(execA.Name(), execB.Name(), rules, input, outA, outB, verdict)
		if err := casestore.Save(c.save, kase, input); err != nil {
			return err
		}
		fmt.Println("case saved to", c.save)
	}
	return nil
}

func cmdReplay(args []string) error {
	fs := flag.NewFlagSet("replay", flag.ContinueOnError)
	caseDir := fs.String("case", "", "saved case directory")
	timeout := fs.Duration("timeout", 2*time.Second, "per-executor timeout")
	if err := fs.Parse(args); err != nil {
		return err
	}
	kase, input, err := casestore.Load(*caseDir)
	if err != nil {
		return err
	}
	fmt.Printf("replaying case from %s (recorded %s on %s/%s, host %s)\n",
		*caseDir, kase.Env.TimeUTC.Format(time.RFC3339),
		kase.Env.OS, kase.Env.Arch, kase.Env.Hostname)
	fmt.Printf("recorded verdict: %s\n", kase.Verdict.Verdict)

	argvA, err := splitCommand(kase.ExecA)
	if err != nil {
		return err
	}
	argvB, err := splitCommand(kase.ExecB)
	if err != nil {
		return err
	}
	execA := &runner.CommandExecutor{Argv: argvA, Timeout: *timeout}
	execB := &runner.CommandExecutor{Argv: argvB, Timeout: *timeout}
	outA, outB := runPair(context.Background(), execA, execB, input)
	verdict := judge.Judge(outA, outB, kase.Rules)
	fmt.Print("replayed ")
	printVerdict(verdict)
	if verdict.Verdict != kase.Verdict.Verdict {
		return fmt.Errorf("replay verdict %q differs from recorded %q",
			verdict.Verdict, kase.Verdict.Verdict)
	}
	fmt.Println("replay matches recorded verdict")
	return nil
}

func cmdShrink(args []string) error {
	fs := flag.NewFlagSet("shrink", flag.ContinueOnError)
	var c commonFlags
	bindCommon(fs, &c)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if c.save == "" {
		return fmt.Errorf("--save is required for shrink")
	}
	rules, err := loadRules(c.rules)
	if err != nil {
		return err
	}
	input, err := os.ReadFile(c.input)
	if err != nil {
		return err
	}
	argvA, err := splitCommand(c.a)
	if err != nil {
		return err
	}
	argvB, err := splitCommand(c.b)
	if err != nil {
		return err
	}
	execA := &runner.CommandExecutor{Argv: argvA, Timeout: c.timeout}
	execB := &runner.CommandExecutor{Argv: argvB, Timeout: c.timeout}

	// Interesting = reproduces a semantic difference (the only verdict
	// worth minimizing; infra verdicts are not value bugs).
	interesting := func(candidate []byte) bool {
		outA, outB := runPair(context.Background(), execA, execB, candidate)
		return judge.Judge(outA, outB, rules).Verdict == judge.SemanticDiff
	}
	if !interesting(input) {
		return fmt.Errorf("input does not reproduce a semantic difference; nothing to shrink")
	}
	minimal := shrink.Shrink(input, interesting)
	fmt.Printf("shrunk input: %d -> %d bytes\n", len(input), len(minimal))

	outA, outB := runPair(context.Background(), execA, execB, minimal)
	verdict := judge.Judge(outA, outB, rules)
	printVerdict(verdict)

	kase := casestore.New(execA.Name(), execB.Name(), rules, minimal, outA, outB, verdict)
	if err := casestore.Save(c.save, kase, minimal); err != nil {
		return err
	}
	fmt.Println("minimal case saved to", c.save)
	return nil
}

// cmdSelftest injects controlled faults and verifies the judge keeps
// parse failure, run failure, timeout, and semantic difference distinct.
func cmdSelftest() error {
	rules := normalize.Rules{
		TrimLineTrailingSpace:    true,
		IgnoreTrailingBlankLines: true,
		NumericTokens:            true,
		FloatTolerance:           1e-9,
	}
	mk := func(label string, b faultinj.Behavior, out string) *faultinj.Executor {
		return &faultinj.Executor{Label: label, Behavior: b, Output: out, Timeout: 50 * time.Millisecond}
	}
	good := func() *faultinj.Executor { return mk("good", faultinj.Value, "42") }

	type row struct {
		name     string
		a, b     *faultinj.Executor
		expected judge.Verdict
	}
	rows := []row{
		{"equal values", good(), good(), judge.Equal},
		{"semantic diff", good(), mk("other", faultinj.Value, "43"), judge.SemanticDiff},
		{"parse failure B", good(), mk("garbage", faultinj.ParseGarbage, "not-a-number"), judge.ParseError},
		{"run failure B", good(), mk("crash", faultinj.RunFail, ""), judge.RunError},
		{"timeout B", good(), mk("hang", faultinj.Hang, ""), judge.Timeout},
		{"timeout vs diff value", mk("hang", faultinj.Hang, ""), mk("other", faultinj.Value, "43"), judge.Timeout},
		{"timeout vs parse garbage", mk("hang", faultinj.Hang, ""), mk("garbage", faultinj.ParseGarbage, "junk"), judge.Timeout},
		{"run failure vs diff value", mk("crash", faultinj.RunFail, ""), mk("other", faultinj.Value, "43"), judge.RunError},
	}
	failed := 0
	for _, r := range rows {
		outA, outB := runPair(context.Background(), r.a, r.b, []byte("input"))
		got := judge.Judge(outA, outB, rules).Verdict
		status := "ok"
		if got != r.expected {
			status = "MISMATCH"
			failed++
		}
		fmt.Printf("%-28s expected=%-14s got=%-14s %s\n", r.name, r.expected, got, status)
	}
	if failed > 0 {
		return fmt.Errorf("%d selftest rows failed", failed)
	}
	fmt.Println("selftest passed: parse failure, run failure, timeout and semantic diff are distinct")
	return nil
}
