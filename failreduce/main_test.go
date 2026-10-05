package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const e2eOracle = `#!/bin/sh
has_foo=0; has_bar=0
for a in "$@"; do
  case "$a" in
    foo) has_foo=1 ;;
    bar) has_bar=1 ;;
  esac
done
if [ "$has_foo" = 1 ] && [ "$has_bar" = 1 ]; then exit 1; fi
exit 0
`

func TestRunEndToEnd(t *testing.T) {
	oraclePath := writeScript(t, "oracle.sh", e2eOracle)
	dir := t.TempDir()
	depsPath := filepath.Join(dir, "deps.txt")
	if err := os.WriteFile(depsPath, []byte("bar: baz\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--oracle", oraclePath,
		"--deps", depsPath,
		"--items", "foo,bar,baz,noise1,noise2",
		"--verify",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"foo", "bar", "baz", "oracle invocations:", "cache hits:", "compile-failures="} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "noise") {
		t.Fatalf("noise items should have been reduced away:\n%s", out)
	}
}

func TestRunJSONOutput(t *testing.T) {
	oraclePath := writeScript(t, "oracle.sh", e2eOracle)
	var stdout, stderr bytes.Buffer
	code := run([]string{
		"--oracle", oraclePath,
		"--items", "foo,bar,x,y",
		"--json",
	}, &stdout, &stderr)
	if code != exitOK {
		t.Fatalf("exit code = %d, stderr = %s", code, stderr.String())
	}
	var res result
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout.String())
	}
	if got := strings.Join(res.Items, ","); got != "bar,foo" {
		t.Fatalf("items = %v, want [bar foo]", res.Items)
	}
	if res.Stats.Invocations == 0 {
		t.Fatal("stats must report actual oracle invocations")
	}
	if !res.OneMinimal {
		t.Fatal("result must be marked 1-minimal")
	}
}

func TestRunNonFailingInitialConfig(t *testing.T) {
	passOracle := writeScript(t, "pass.sh", "#!/bin/sh\nexit 0\n")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--oracle", passOracle, "--items", "a,b"}, &stdout, &stderr)
	if code != exitPrecondition {
		t.Fatalf("exit code = %d, want %d (stderr: %s)", code, exitPrecondition, stderr.String())
	}
}

func TestRunMissingOracleFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"--items", "a"}, &stdout, &stderr); code != exitUsage {
		t.Fatalf("exit code = %d, want %d", code, exitUsage)
	}
}

func TestLoadDepsParsing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "deps.txt")
	content := `# comment
app: libcore libnet
libcore : runtime
plain dep1 dep2
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	deps, err := loadDeps(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := deps.CheckLegal([]string{"app", "libcore", "libnet", "runtime"}); err != nil {
		t.Fatalf("parsed deps wrong: %v", err)
	}
	if err := deps.CheckLegal([]string{"app", "libcore", "libnet"}); err == nil {
		t.Fatal("expected missing transitive dependency error")
	}
	if err := deps.CheckLegal([]string{"plain", "dep1"}); err == nil {
		t.Fatal("expected missing dep2 error")
	}
}
