package configeval

import (
	"reflect"
	"strings"
	"testing"
)

func newTestConfig(t *testing.T, defs map[string]string) *Config {
	t.Helper()
	cfg := NewConfig()
	for name, src := range defs {
		if err := cfg.Set(name, src); err != nil {
			t.Fatalf("Set(%q): %v", name, err)
		}
	}
	return cfg
}

func mustEval(t *testing.T, ev *Evaluator, name string) Result {
	t.Helper()
	res, err := ev.Eval(name)
	if err != nil {
		t.Fatalf("Eval(%q): %v", name, err)
	}
	return res
}

func TestBasicEvaluation(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"port":     "8080",
		"replicas": "${base} * 2",
		"base":     "3",
		"greeting": `"hello " + "world"`,
		"enabled":  "true",
		"url":      `"http://${env.HOST}:${port}/api"`,
	})
	ev := NewEvaluator(cfg, Env{"HOST": "example.com"})

	if got := mustEval(t, ev, "replicas").Value.String(); got != "6" {
		t.Errorf("replicas = %s, want 6", got)
	}
	if got := mustEval(t, ev, "greeting").Value.String(); got != "hello world" {
		t.Errorf("greeting = %s, want hello world", got)
	}
	if got := mustEval(t, ev, "enabled").Value.String(); got != "true" {
		t.Errorf("enabled = %s, want true", got)
	}
	if got := mustEval(t, ev, "url").Value.String(); got != "http://example.com:8080/api" {
		t.Errorf("url = %s", got)
	}
}

func TestMissingVariableChain(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"a": "${b} + 1",
		"b": "${c} + 1",
	})
	ev := NewEvaluator(cfg, nil)
	_, err := ev.Eval("a")
	re, ok := err.(*RefError)
	if !ok {
		t.Fatalf("expected *RefError, got %T: %v", err, err)
	}
	if re.Kind != ErrMissing {
		t.Errorf("kind = %v, want ErrMissing", re.Kind)
	}
	want := []string{"a", "b", "c"}
	if !reflect.DeepEqual(re.Chain, want) {
		t.Errorf("chain = %v, want %v", re.Chain, want)
	}
}

func TestMissingEnvKeyChain(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"addr": `"${env.HOST}:${env.PORT}"`,
	})
	ev := NewEvaluator(cfg, Env{"HOST": "h"})
	_, err := ev.Eval("addr")
	re := err.(*RefError)
	if re.Kind != ErrMissing {
		t.Errorf("kind = %v, want ErrMissing", re.Kind)
	}
	want := []string{"addr", "env.PORT"}
	if !reflect.DeepEqual(re.Chain, want) {
		t.Errorf("chain = %v, want %v", re.Chain, want)
	}
}

func TestCycleChain(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"a": "${b} + 1",
		"b": "${c} + 1",
		"c": "${a} + 1",
	})
	ev := NewEvaluator(cfg, nil)
	_, err := ev.Eval("a")
	re := err.(*RefError)
	if re.Kind != ErrCycle {
		t.Fatalf("kind = %v, want ErrCycle", re.Kind)
	}
	want := []string{"a", "b", "c", "a"}
	if !reflect.DeepEqual(re.Chain, want) {
		t.Errorf("chain = %v, want %v", re.Chain, want)
	}
}

func TestSelfCycle(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{"x": "${x}"})
	ev := NewEvaluator(cfg, nil)
	_, err := ev.Eval("x")
	re := err.(*RefError)
	if re.Kind != ErrCycle || !reflect.DeepEqual(re.Chain, []string{"x", "x"}) {
		t.Errorf("got %v chain %v", re.Kind, re.Chain)
	}
}

func TestTypeErrorChain(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"top":  "${mid} + 1",
		"mid":  `${name} - 1`,
		"name": `"not-a-number"`,
	})
	ev := NewEvaluator(cfg, nil)
	_, err := ev.Eval("top")
	re := err.(*RefError)
	if re.Kind != ErrType {
		t.Fatalf("kind = %v, want ErrType", re.Kind)
	}
	want := []string{"top", "mid"}
	if !reflect.DeepEqual(re.Chain, want) {
		t.Errorf("chain = %v, want %v", re.Chain, want)
	}
	if !strings.Contains(re.Msg, "cannot be applied") {
		t.Errorf("msg = %q", re.Msg)
	}
}

func TestDivisionByZero(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{"r": "1 / (${x} - ${x})", "x": "5"})
	ev := NewEvaluator(cfg, nil)
	_, err := ev.Eval("r")
	if re := err.(*RefError); re.Kind != ErrType || re.Msg != "division by zero" {
		t.Errorf("got %v", err)
	}
}

func TestDigestCoversAllDependencies(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"root":  `"${left}|${right}"`,
		"left":  `"L${env.SUFFIX}"`,
		"right": "${num} + 1",
		"num":   "41",
	})
	ev := NewEvaluator(cfg, Env{"SUFFIX": "x"})
	res := mustEval(t, ev, "root")
	wantDeps := []string{"env.SUFFIX", "left", "num", "right"}
	if !reflect.DeepEqual(res.Deps, wantDeps) {
		t.Errorf("deps = %v, want %v", res.Deps, wantDeps)
	}

	// Same dependencies -> identical digest, even in a fresh evaluator.
	ev2 := NewEvaluator(cfg, Env{"SUFFIX": "x"})
	if d := mustEval(t, ev2, "root").Digest; d != res.Digest {
		t.Errorf("digest not stable across evaluators")
	}

	// Any dependency change -> different digest.
	ev3 := NewEvaluator(cfg, Env{"SUFFIX": "y"})
	if d := mustEval(t, ev3, "root").Digest; d == res.Digest {
		t.Errorf("digest unchanged after env change")
	}
	cfg4 := newTestConfig(t, map[string]string{
		"root": `"${left}|${right}"`, "left": `"L${env.SUFFIX}"`,
		"right": "${num} + 1", "num": "42",
	})
	ev4 := NewEvaluator(cfg4, Env{"SUFFIX": "x"})
	if d := mustEval(t, ev4, "root").Digest; d == res.Digest {
		t.Errorf("digest unchanged after transitive var change")
	}
}

func TestFreezeBoundaryAndIncrementalRecompute(t *testing.T) {
	cfg := newTestConfig(t, map[string]string{
		"root":    `"${dynamic}|${static}"`,
		"dynamic": `"v=${env.VALUE}"`,
		"static":  `"s=${env.STABLE}"`,
	})
	ev := NewEvaluator(cfg, Env{"VALUE": "1", "STABLE": "a"})

	r1 := mustEval(t, ev, "root")
	if r1.Value.String() != "v=1|s=a" {
		t.Fatalf("root = %s", r1.Value)
	}
	base := ev.Stats()

	// Re-eval within the same freeze: pure cache hits, zero computes.
	mustEval(t, ev, "root")
	if s := ev.Stats(); s.Computes != base.Computes || s.Hits == base.Hits {
		t.Errorf("same-freeze eval: %+v -> %+v", base, s)
	}

	// Cross a freeze boundary changing only VALUE.
	ev.Freeze(Env{"VALUE": "2", "STABLE": "a"})
	r2 := mustEval(t, ev, "root")
	if r2.Value.String() != "v=2|s=a" {
		t.Fatalf("after freeze root = %s", r2.Value)
	}
	if r2.Digest == r1.Digest {
		t.Errorf("digest did not change across freeze boundary")
	}
	s := ev.Stats()
	recomputed := s.Computes - base.Computes
	// Only root and dynamic may recompute; static must be reused.
	if recomputed != 2 {
		t.Errorf("recomputed %d expressions, want exactly 2 (root, dynamic)", recomputed)
	}
	staticRes := mustEval(t, ev, "static")
	if staticRes.Value.String() != "s=a" {
		t.Errorf("static = %s", staticRes.Value)
	}

	// Freeze with identical values: nothing recomputes.
	ev.Freeze(Env{"VALUE": "2", "STABLE": "a"})
	before := ev.Stats()
	mustEval(t, ev, "root")
	if s := ev.Stats(); s.Computes != before.Computes {
		t.Errorf("identical freeze recomputed: %+v -> %+v", before, s)
	}

	// Removing a key a cached entry depends on must invalidate it.
	ev.Freeze(Env{"VALUE": "2"})
	if _, err := ev.Eval("static"); err == nil {
		t.Errorf("expected missing-key error after snapshot shrank")
	}
	if got := mustEval(t, ev, "dynamic").Value.String(); got != "v=2" {
		t.Errorf("dynamic = %s, want v=2 (unaffected by STABLE removal)", got)
	}
}

func TestNeverReadsProcessEnvironment(t *testing.T) {
	t.Setenv("CONFIGEVAL_SECRET", "from-process")
	cfg := newTestConfig(t, map[string]string{
		"leak": "${env.CONFIGEVAL_SECRET}",
	})
	ev := NewEvaluator(cfg, Env{})
	_, err := ev.Eval("leak")
	re, ok := err.(*RefError)
	if !ok || re.Kind != ErrMissing {
		t.Fatalf("process env leaked into evaluation: %v", err)
	}

	// Only the explicit snapshot is visible.
	ev2 := NewEvaluator(cfg, Env{"CONFIGEVAL_SECRET": "from-snapshot"})
	if got := mustEval(t, ev2, "leak").Value.String(); got != "from-snapshot" {
		t.Errorf("leak = %s, want from-snapshot", got)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	m := map[string]string{"K": "1"}
	ev := NewEvaluator(newTestConfig(t, map[string]string{"v": "${env.K}"}), Snapshot(m))
	m["K"] = "mutated"
	if got := mustEval(t, ev, "v").Value.String(); got != "1" {
		t.Errorf("snapshot not isolated from caller mutation: %s", got)
	}
}

func TestParseErrors(t *testing.T) {
	cfg := NewConfig()
	for _, bad := range []string{"1 +", "${}", "(1", `"unterminated`, "${env.}", "1 2"} {
		if err := cfg.Set("x", bad); err == nil {
			t.Errorf("Set(%q) unexpectedly succeeded", bad)
		}
	}
}
