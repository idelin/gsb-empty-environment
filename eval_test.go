package configeval

import (
	"os"
	"strings"
	"testing"
)

func testDefs() map[string]Expr {
	return map[string]Expr{
		"host":    Env("DB_HOST"),
		"port":    Env("DB_PORT"),
		"dsn":     Concat(Str("postgres://"), Ref("host"), Str(":"), Ref("port")),
		"retries": Int(3),
		"backoff": Add(Ref("retries"), Int(2)),
	}
}

func testEnv() map[string]string {
	return map[string]string{"DB_HOST": "localhost", "DB_PORT": "5432"}
}

func TestEvalSuccessWithDependencyDigest(t *testing.T) {
	e := New(testDefs(), testEnv())
	res, err := e.Get("dsn")
	if err != nil {
		t.Fatalf("Get(dsn) failed: %v", err)
	}
	if res.Value.Kind != StringKind || res.Value.Str != "postgres://localhost:5432" {
		t.Fatalf("unexpected value: %+v", res.Value)
	}
	if res.Digest == "" {
		t.Fatal("digest is empty")
	}
	wantRefs := []string{"env:DB_HOST", "env:DB_PORT", "var:host", "var:port"}
	if len(res.Deps) != len(wantRefs) {
		t.Fatalf("deps = %+v, want refs %v", res.Deps, wantRefs)
	}
	for i, ref := range wantRefs {
		if res.Deps[i].Ref != ref {
			t.Fatalf("deps[%d].Ref = %q, want %q (deps sorted by ref)", i, res.Deps[i].Ref, ref)
		}
	}
	// Digest is deterministic across evaluators with identical inputs.
	e2 := New(testDefs(), testEnv())
	res2, err := e2.Get("dsn")
	if err != nil {
		t.Fatalf("Get(dsn) on second evaluator failed: %v", err)
	}
	if res.Digest != res2.Digest {
		t.Fatalf("digest not deterministic: %q vs %q", res.Digest, res2.Digest)
	}
}

func TestMissingVariableChain(t *testing.T) {
	defs := map[string]Expr{"a": Ref("b"), "b": Ref("gone")}
	e := New(defs, nil)
	_, err := e.Get("a")
	re, ok := err.(*RefError)
	if !ok || re.Kind != MissingError {
		t.Fatalf("err = %v, want MissingError", err)
	}
	want := []string{"var:a", "var:b", "var:gone"}
	if strings.Join(re.Chain, ",") != strings.Join(want, ",") {
		t.Fatalf("chain = %v, want %v", re.Chain, want)
	}
}

func TestMissingEnvKeyChain(t *testing.T) {
	defs := map[string]Expr{"a": Concat(Ref("b"), Str("!")), "b": Env("NOPE")}
	e := New(defs, nil)
	_, err := e.Get("a")
	re, ok := err.(*RefError)
	if !ok || re.Kind != MissingError {
		t.Fatalf("err = %v, want MissingError", err)
	}
	want := []string{"var:a", "var:b", "env:NOPE"}
	if strings.Join(re.Chain, ",") != strings.Join(want, ",") {
		t.Fatalf("chain = %v, want %v", re.Chain, want)
	}
}

func TestCycleChain(t *testing.T) {
	defs := map[string]Expr{"a": Ref("b"), "b": Ref("c"), "c": Ref("a")}
	e := New(defs, nil)
	_, err := e.Get("a")
	re, ok := err.(*RefError)
	if !ok || re.Kind != CycleError {
		t.Fatalf("err = %v, want CycleError", err)
	}
	want := []string{"var:a", "var:b", "var:c", "var:a"}
	if strings.Join(re.Chain, ",") != strings.Join(want, ",") {
		t.Fatalf("chain = %v, want %v", re.Chain, want)
	}
}

func TestSelfCycleThroughCacheValidation(t *testing.T) {
	// Two-variable cycle must error even when cache entries already exist.
	defs := map[string]Expr{"a": Ref("b"), "b": Ref("a")}
	e := New(defs, nil)
	if _, err := e.Get("a"); err == nil {
		t.Fatal("expected cycle error")
	}
	_, err := e.Get("a")
	if err == nil {
		t.Fatal("expected cycle error on cached re-evaluation")
	}
	re := err.(*RefError)
	if re.Kind != CycleError {
		t.Fatalf("err = %v, want CycleError", err)
	}
}

func TestTypeErrorChain(t *testing.T) {
	defs := map[string]Expr{
		"n":   Int(5),
		"bad": Add(Ref("n"), Ref("s")),
		"s":   Str("oops"),
	}
	e := New(defs, nil)
	_, err := e.Get("bad")
	re, ok := err.(*RefError)
	if !ok || re.Kind != TypeError {
		t.Fatalf("err = %v, want TypeError", err)
	}
	want := []string{"var:bad"}
	if strings.Join(re.Chain, ",") != strings.Join(want, ",") {
		t.Fatalf("chain = %v, want %v", re.Chain, want)
	}
	if !strings.Contains(re.Detail, "oops") {
		t.Fatalf("detail should mention offending value, got %q", re.Detail)
	}
}

func TestNeverReadsProcessEnvironment(t *testing.T) {
	const key = "CONFIGEVAL_TEST_SECRET"
	if err := os.Setenv(key, "from-process-env"); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv(key)

	// Snapshot deliberately omits the key: the process env must not leak in.
	e := New(map[string]Expr{"v": Env(key)}, map[string]string{})
	_, err := e.Get("v")
	re, ok := err.(*RefError)
	if !ok || re.Kind != MissingError {
		t.Fatalf("process environment leaked into evaluation: err = %v", err)
	}

	// And when the snapshot has the key, the snapshot wins over the process env.
	e2 := New(map[string]Expr{"v": Env(key)}, map[string]string{key: "from-snapshot"})
	res, err := e2.Get("v")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	if res.Value.Str != "from-snapshot" {
		t.Fatalf("value = %q, want snapshot value", res.Value.Str)
	}
}

func TestIncrementalRecomputeOnEnvChange(t *testing.T) {
	e := New(testDefs(), testEnv())

	dsn1, err := e.Get("dsn")
	if err != nil {
		t.Fatalf("Get(dsn) failed: %v", err)
	}
	backoff1, err := e.Get("backoff")
	if err != nil {
		t.Fatalf("Get(backoff) failed: %v", err)
	}
	computed, _ := e.Stats()
	if computed != 5 { // host, port, dsn, retries, backoff
		t.Fatalf("computed = %d, want 5", computed)
	}

	// Change only DB_HOST: dsn (via host) must recompute, backoff must not.
	e.SetEnv(map[string]string{"DB_HOST": "db.internal", "DB_PORT": "5432"})

	backoff2, err := e.Get("backoff")
	if err != nil {
		t.Fatalf("Get(backoff) after env change failed: %v", err)
	}
	if backoff2.Digest != backoff1.Digest {
		t.Fatal("backoff digest changed although its dependencies did not")
	}
	computed, reused := e.Stats()
	if computed != 5 {
		t.Fatalf("computed = %d after unrelated env change, want 5 (no recompute)", computed)
	}
	if reused == 0 {
		t.Fatal("expected backoff to be served from cache")
	}

	dsn2, err := e.Get("dsn")
	if err != nil {
		t.Fatalf("Get(dsn) after env change failed: %v", err)
	}
	if dsn2.Value.Str != "postgres://db.internal:5432" {
		t.Fatalf("dsn = %q, want updated host", dsn2.Value.Str)
	}
	if dsn2.Digest == dsn1.Digest {
		t.Fatal("dsn digest did not change after dependency changed")
	}
	computed, _ = e.Stats()
	if computed != 7 { // host + dsn recomputed; port reused
		t.Fatalf("computed = %d, want 7 (only host and dsn recomputed)", computed)
	}
}

func TestTransitiveInvalidation(t *testing.T) {
	defs := map[string]Expr{
		"base":  Env("BASE"),
		"mid":   Concat(Ref("base"), Str("/mid")),
		"top":   Concat(Ref("mid"), Str("/top")),
		"other": Str("static"),
	}
	e := New(defs, map[string]string{"BASE": "v1"})
	if _, err := e.Get("top"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Get("other"); err != nil {
		t.Fatal(err)
	}
	computed, _ := e.Stats()
	if computed != 4 {
		t.Fatalf("computed = %d, want 4", computed)
	}

	e.SetEnv(map[string]string{"BASE": "v2"})
	res, err := e.Get("top")
	if err != nil {
		t.Fatal(err)
	}
	if res.Value.Str != "v2/mid/top" {
		t.Fatalf("top = %q, want v2/mid/top", res.Value.Str)
	}
	computed, _ = e.Stats()
	if computed != 7 { // base, mid, top recomputed
		t.Fatalf("computed = %d, want 7 (transitive recompute)", computed)
	}
	if _, err := e.Get("other"); err != nil {
		t.Fatal(err)
	}
	computed, _ = e.Stats()
	if computed != 7 {
		t.Fatalf("computed = %d, want 7 (unrelated var stays cached)", computed)
	}
}

func TestFreezeBoundary(t *testing.T) {
	e := New(testDefs(), testEnv())
	res1, err := e.Get("dsn")
	if err != nil {
		t.Fatal(err)
	}
	computed, _ := e.Stats()

	// Freeze with an unchanged snapshot: cache must not cross the boundary.
	e.Freeze()
	res2, err := e.Get("dsn")
	if err != nil {
		t.Fatal(err)
	}
	computed2, reused2 := e.Stats()
	if computed2 == computed {
		t.Fatal("no recomputation after Freeze: cache crossed the freeze boundary")
	}
	if reused2 != 0 {
		t.Fatalf("reused = %d after Freeze, want 0", reused2)
	}
	if res1.Digest != res2.Digest {
		t.Fatal("digest changed although inputs did not")
	}

	// Freeze plus a new snapshot: values must come from the new snapshot,
	// never from pre-freeze cache entries.
	e.Freeze()
	e.SetEnv(map[string]string{"DB_HOST": "after-freeze", "DB_PORT": "6432"})
	res3, err := e.Get("dsn")
	if err != nil {
		t.Fatal(err)
	}
	if res3.Value.Str != "postgres://after-freeze:6432" {
		t.Fatalf("dsn = %q, stale value crossed the freeze boundary", res3.Value.Str)
	}
}

func TestRemovedEnvKeyInvalidatesCache(t *testing.T) {
	e := New(testDefs(), testEnv())
	if _, err := e.Get("dsn"); err != nil {
		t.Fatal(err)
	}
	e.SetEnv(map[string]string{"DB_PORT": "5432"}) // DB_HOST removed
	_, err := e.Get("dsn")
	re, ok := err.(*RefError)
	if !ok || re.Kind != MissingError {
		t.Fatalf("err = %v, want MissingError for removed env key", err)
	}
	want := []string{"var:dsn", "var:host", "env:DB_HOST"}
	if strings.Join(re.Chain, ",") != strings.Join(want, ",") {
		t.Fatalf("chain = %v, want %v", re.Chain, want)
	}
}
