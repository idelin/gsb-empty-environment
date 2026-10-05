package difftest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestHelperProcess is re-executed as a fake executor under fault injection.
// The fault mode comes from DIFFTEST_FAULT; input is read from stdin.
func TestHelperProcess(t *testing.T) {
	if os.Getenv("DIFFTEST_HELPER") != "1" {
		return
	}
	mode := os.Getenv("DIFFTEST_FAULT")
	switch {
	case mode == "ok":
		fmt.Print(os.Getenv("DIFFTEST_OUTPUT"))
	case strings.HasPrefix(mode, "exit:"):
		code, _ := strconv.Atoi(strings.TrimPrefix(mode, "exit:"))
		fmt.Fprint(os.Stderr, "boom")
		os.Exit(code)
	case strings.HasPrefix(mode, "sleep:"):
		d, _ := time.ParseDuration(strings.TrimPrefix(mode, "sleep:"))
		time.Sleep(d)
		fmt.Print("late")
	case mode == "echo":
		data := make([]byte, 1<<20)
		n, _ := os.Stdin.Read(data)
		os.Stdout.Write(data[:n])
	case mode == "trigger-diff":
		// Emits a divergent value only while the input contains "trigger".
		data := make([]byte, 1<<20)
		n, _ := os.Stdin.Read(data)
		if strings.Contains(string(data[:n]), "trigger") {
			fmt.Print(os.Getenv("DIFFTEST_OUTPUT"))
		} else {
			fmt.Print("same")
		}
	}
	os.Exit(0)
}

// fakeExecutor builds an Executor that re-runs this test binary with the
// given fault mode injected via the environment.
func fakeExecutor(t *testing.T, name, fault, output string, timeout time.Duration) Executor {
	t.Helper()
	return Executor{
		Name:    name,
		Argv:    []string{os.Args[0], "-test.run=TestHelperProcess"},
		Timeout: Duration(timeout),
		ExtraEnv: []string{
			"DIFFTEST_HELPER=1",
			"DIFFTEST_FAULT=" + fault,
			"DIFFTEST_OUTPUT=" + output,
		},
	}
}

func runBoth(t *testing.T, execA, execB Executor, rules *Rules, input []byte) (Outcome, Outcome, Verdict) {
	t.Helper()
	outA := execA.Run(context.Background(), input)
	outB := execB.Run(context.Background(), input)
	return outA, outB, Judge(rules, outA, outB)
}

func TestAgreeIdenticalOutput(t *testing.T) {
	rules := &Rules{TrimSpace: true}
	a := fakeExecutor(t, "A", "ok", "hello\n", time.Second)
	b := fakeExecutor(t, "B", "ok", "hello\n", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictAgree {
		t.Fatalf("want agree, got %v", v)
	}
}

func TestAgreeUnderNormalization(t *testing.T) {
	rules := &Rules{TrimSpace: true, CollapseSpace: true, CaseFold: true}
	a := fakeExecutor(t, "A", "ok", "  Hello   World \n", time.Second)
	b := fakeExecutor(t, "B", "ok", "hello world", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictAgree {
		t.Fatalf("want agree, got %v", v)
	}
}

func TestAgreeFloatTolerance(t *testing.T) {
	rules := &Rules{TrimSpace: true, FloatMode: true, FloatTol: 1e-6}
	a := fakeExecutor(t, "A", "ok", "3.14159265", time.Second)
	b := fakeExecutor(t, "B", "ok", "3.14159266", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictAgree {
		t.Fatalf("want agree, got %v", v)
	}
}

func TestSemanticDiff(t *testing.T) {
	rules := &Rules{TrimSpace: true}
	a := fakeExecutor(t, "A", "ok", "42", time.Second)
	b := fakeExecutor(t, "B", "ok", "43", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictSemanticDiff {
		t.Fatalf("want semantic_diff, got %v", v)
	}
}

// Timeout must never be classified as a value mismatch.
func TestTimeoutIsNotSemanticDiff(t *testing.T) {
	rules := &Rules{TrimSpace: true}
	slow := fakeExecutor(t, "A", "sleep:3s", "", 200*time.Millisecond)
	fast := fakeExecutor(t, "B", "ok", "anything", time.Second)
	outA, _, v := runBoth(t, slow, fast, rules, []byte("x"))
	if outA.Kind != OutcomeTimeout {
		t.Fatalf("want timeout outcome, got %v", outA.Kind)
	}
	if v.Kind != VerdictTimeoutDiff || v.Side != "A" {
		t.Fatalf("want timeout_diff side A, got %v", v)
	}
	if v.Kind == VerdictSemanticDiff {
		t.Fatal("timeout misclassified as semantic diff")
	}
}

func TestBothTimeout(t *testing.T) {
	rules := &Rules{}
	a := fakeExecutor(t, "A", "sleep:3s", "", 150*time.Millisecond)
	b := fakeExecutor(t, "B", "sleep:3s", "", 150*time.Millisecond)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictBothTimeout {
		t.Fatalf("want both_timeout, got %v", v)
	}
}

func TestRunErrorVsValue(t *testing.T) {
	rules := &Rules{TrimSpace: true}
	bad := fakeExecutor(t, "A", "exit:3", "", time.Second)
	good := fakeExecutor(t, "B", "ok", "42", time.Second)
	_, _, v := runBoth(t, bad, good, rules, []byte("x"))
	if v.Kind != VerdictRunErrorDiff || v.Side != "A" {
		t.Fatalf("want run_error_diff side A, got %v", v)
	}
}

func TestBothRunErrorSameCode(t *testing.T) {
	rules := &Rules{}
	a := fakeExecutor(t, "A", "exit:2", "", time.Second)
	b := fakeExecutor(t, "B", "exit:2", "", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictBothRunError {
		t.Fatalf("want both_run_error, got %v", v)
	}
	if v.Kind.Divergent() {
		t.Fatal("identical failures should not count as divergence")
	}
}

func TestBothRunErrorDifferentCodes(t *testing.T) {
	rules := &Rules{}
	a := fakeExecutor(t, "A", "exit:2", "", time.Second)
	b := fakeExecutor(t, "B", "exit:5", "", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictRunErrorDiff {
		t.Fatalf("want run_error_diff, got %v", v)
	}
}

// Parse failure (non-numeric token in strict float mode) must not be
// confused with a semantic difference.
func TestParseErrorIsNotSemanticDiff(t *testing.T) {
	rules := &Rules{TrimSpace: true, FloatMode: true, FloatStrict: true, FloatTol: 1e-9}
	garbage := fakeExecutor(t, "A", "ok", "not-a-number", time.Second)
	numeric := fakeExecutor(t, "B", "ok", "1.0", time.Second)
	_, _, v := runBoth(t, garbage, numeric, rules, []byte("x"))
	if v.Kind != VerdictParseError || v.Side != "A" {
		t.Fatalf("want parse_error side A, got %v", v)
	}
	if v.Kind == VerdictSemanticDiff {
		t.Fatal("parse failure misclassified as semantic diff")
	}
}

// Non-strict float mode falls back to literal token comparison.
func TestFloatModeLenientFallback(t *testing.T) {
	rules := &Rules{TrimSpace: true, FloatMode: true, FloatTol: 1e-9}
	a := fakeExecutor(t, "A", "ok", "nan-ish 1.0", time.Second)
	b := fakeExecutor(t, "B", "ok", "nan-ish 1.0", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictAgree {
		t.Fatalf("want agree, got %v", v)
	}
}

func TestStripPattern(t *testing.T) {
	rules := &Rules{TrimSpace: true, StripPatterns: []string{`pid=\d+`}}
	if err := rules.Compile(); err != nil {
		t.Fatal(err)
	}
	a := fakeExecutor(t, "A", "ok", "started pid=123 ok", time.Second)
	b := fakeExecutor(t, "B", "ok", "started pid=987 ok", time.Second)
	_, _, v := runBoth(t, a, b, rules, []byte("x"))
	if v.Kind != VerdictAgree {
		t.Fatalf("want agree, got %v", v)
	}
}

func TestMinimizeKeepsVerdict(t *testing.T) {
	rules := &Rules{TrimSpace: true}
	a := fakeExecutor(t, "A", "trigger-diff", "AAA", time.Second)
	b := fakeExecutor(t, "B", "trigger-diff", "BBB", time.Second)
	var input []byte
	for i := 0; i < 40; i++ {
		input = append(input, []byte(fmt.Sprintf("filler line %d\n", i))...)
	}
	input = append(input, []byte("trigger\n")...)
	for i := 0; i < 40; i++ {
		input = append(input, []byte(fmt.Sprintf("more filler %d\n", i))...)
	}
	keep := func(candidate []byte) bool {
		_, _, v := runBoth(t, a, b, rules, candidate)
		return v.Kind == VerdictSemanticDiff
	}
	if !keep(input) {
		t.Fatal("setup: full input should reproduce the diff")
	}
	minimized := Minimize(input, keep, 300)
	if !keep(minimized) {
		t.Fatal("minimized input no longer reproduces")
	}
	if len(minimized) >= len(input) {
		t.Fatalf("expected shrink, got %d >= %d bytes", len(minimized), len(input))
	}
	if !strings.Contains(string(minimized), "trigger") {
		t.Fatalf("minimizer dropped the trigger: %q", minimized)
	}
	t.Logf("minimized %d -> %d bytes", len(input), len(minimized))
}

func TestSaveAndReplayCase(t *testing.T) {
	rules := Rules{TrimSpace: true}
	a := fakeExecutor(t, "A", "ok", "1", time.Second)
	b := fakeExecutor(t, "B", "ok", "2", time.Second)
	input := []byte("sample input")
	outA, outB, v := runBoth(t, a, b, &rules, input)
	if v.Kind != VerdictSemanticDiff {
		t.Fatalf("setup: want semantic_diff, got %v", v)
	}
	dir := filepath.Join(t.TempDir(), "case-1")
	if err := SaveCase(dir, input, a, b, rules, [2]Outcome{outA, outB}, v, false); err != nil {
		t.Fatal(err)
	}
	cf, gotInput, err := LoadCase(dir)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotInput) != string(input) {
		t.Fatal("input round-trip mismatch")
	}
	if cf.Verdict.Kind != VerdictSemanticDiff || cf.Env.OS == "" || cf.Tool == "" {
		t.Fatalf("bad case file: %+v", cf)
	}
	// Replay: re-run both executors from the saved spec, expect same verdict.
	reA, reB, reV := runBoth(t, cf.Executors[0], cf.Executors[1], &cf.Rules, gotInput)
	_ = reA
	_ = reB
	if reV.Kind != cf.Verdict.Kind {
		t.Fatalf("replay verdict %v, want %v", reV.Kind, cf.Verdict.Kind)
	}
}
