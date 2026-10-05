package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

type countingOracle struct {
	calls int
}

func (o *countingOracle) Test(cfg []string) (Verdict, error) {
	o.calls++
	return Pass, nil
}

func TestCachingOracleKeysByNormalizedConfig(t *testing.T) {
	inner := &countingOracle{}
	oracle := NewCachingOracle(inner)

	if _, err := oracle.Test([]string{"b", "a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := oracle.Test([]string{"a", "b", "a"}); err != nil {
		t.Fatal(err)
	}
	if inner.calls != 1 {
		t.Fatalf("inner oracle invoked %d times, want 1 (cache miss only)", inner.calls)
	}
	if oracle.Stats.Invocations != 1 || oracle.Stats.CacheHits != 1 {
		t.Fatalf("stats = %+v, want 1 invocation and 1 cache hit", oracle.Stats)
	}
	if oracle.Stats.Passes != 1 {
		t.Fatalf("stats = %+v, want 1 pass", oracle.Stats)
	}
}

func writeScript(t *testing.T, name, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("shell script oracle not supported on windows")
	}
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

const argvOracle = `#!/bin/sh
has_foo=0; has_bar=0; has_boom=0
for a in "$@"; do
  case "$a" in
    foo)  has_foo=1 ;;
    bar)  has_bar=1 ;;
    boom) has_boom=1 ;;
  esac
done
if [ "$has_boom" = 1 ]; then exit 2; fi
if [ "$has_foo" = 1 ] && [ "$has_bar" = 1 ]; then exit 1; fi
exit 0
`

func TestExecOracleArgvVerdicts(t *testing.T) {
	oracle := &ExecOracle{Path: writeScript(t, "oracle.sh", argvOracle), Timeout: 5 * time.Second}

	cases := []struct {
		cfg  []string
		want Verdict
	}{
		{[]string{"foo", "bar"}, TestFail},
		{[]string{"foo"}, Pass},
		{[]string{"bar"}, Pass},
		{[]string{"boom"}, CompileFail},
		{[]string{"foo", "bar", "boom"}, CompileFail}, // compile failure dominates
		{nil, Pass},
	}
	for _, tc := range cases {
		got, err := oracle.Test(tc.cfg)
		if err != nil {
			t.Fatalf("Test(%v): %v", tc.cfg, err)
		}
		if got != tc.want {
			t.Fatalf("Test(%v) = %s, want %s", tc.cfg, got, tc.want)
		}
	}
}

const stdinOracle = `#!/bin/sh
has_foo=0; has_bar=0
while IFS= read -r line; do
  case "$line" in
    foo) has_foo=1 ;;
    bar) has_bar=1 ;;
  esac
done
if [ "$has_foo" = 1 ] && [ "$has_bar" = 1 ]; then exit 1; fi
exit 0
`

func TestExecOracleStdinMode(t *testing.T) {
	oracle := &ExecOracle{
		Path:     writeScript(t, "oracle-stdin.sh", stdinOracle),
		UseStdin: true,
		Timeout:  5 * time.Second,
	}
	if got, _ := oracle.Test([]string{"foo", "bar"}); got != TestFail {
		t.Fatalf("stdin mode: got %s, want %s", got, TestFail)
	}
	if got, _ := oracle.Test([]string{"foo"}); got != Pass {
		t.Fatalf("stdin mode: got %s, want %s", got, Pass)
	}
}

func TestExecOracleUnexpectedExitCode(t *testing.T) {
	oracle := &ExecOracle{
		Path:    writeScript(t, "bad.sh", "#!/bin/sh\nexit 7\n"),
		Timeout: 5 * time.Second,
	}
	if _, err := oracle.Test([]string{"x"}); err == nil {
		t.Fatal("unexpected oracle exit codes must surface as errors")
	}
}

func TestExecOracleMissingProgram(t *testing.T) {
	oracle := &ExecOracle{Path: "/nonexistent/oracle", Timeout: 5 * time.Second}
	if _, err := oracle.Test([]string{"x"}); err == nil {
		t.Fatal("missing oracle program must surface as an error")
	}
}
