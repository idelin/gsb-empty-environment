package netsec

import (
	"context"
	"testing"
)

// TestLoadFamilyAndRequire covers the code-loading sinks that let sandboxed
// code rebuild unrestricted globals.
func TestLoadFamilyAndRequire(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("pkg", BuiltinPackage).
		Function("loadFn", BuiltinLoad).
		Function("loadfileFn", BuiltinLoadFile).
		Function("dofileFn", BuiltinDoFile).
		Function("requireFn", BuiltinRequire).
		Field("sandbox", "load", "loadFn").
		Field("sandbox", "loadfile", "loadfileFn").
		Field("sandbox", "dofile", "dofileFn").
		Field("pkg", "require", "requireFn").
		Upvalue("loadFn", 1, "pkg", "pkg").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	findings := sinkMap(report.Findings)

	for _, want := range []SinkKind{SinkLoad, SinkLoadFile, SinkDoFile, SinkRequire} {
		f, ok := findings[want]
		if !ok {
			t.Fatalf("missing sink %s, got %v", want, report.Findings)
		}
		if f.Sink.String() != want.String() {
			t.Fatalf("sink label mismatch: %s vs %s", f.Sink, want)
		}
		assertPath(t, f, RootSandbox)
	}

	// require is reached through the upvalue captured by load.
	assertPath(t, findings[SinkRequire], RootSandbox, ".load", "[upvalue#1 pkg]", ".require", "requireFn")
}

// TestStructuralFallback verifies classification even when the extractor
// did not mark nodes with builtin identifiers: field names and library
// roles still identify os.execute and string.dump.
func TestStructuralFallback(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("osTable", "").
		Function("executeFn", "").
		Table("stringTable", "").
		Function("dumpFn", "").
		Table("debugTable", "").
		Function("sethookFn", "").
		Field("sandbox", "os", "osTable").
		Field("osTable", "execute", "executeFn").
		Field("sandbox", "string", "stringTable").
		Field("stringTable", "dump", "dumpFn").
		Field("sandbox", "debug", "debugTable").
		Field("debugTable", "sethook", "sethookFn").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	findings := sinkMap(report.Findings)

	if f, ok := findings[SinkOSExecute]; !ok || f.Node != "executeFn" {
		t.Fatalf("os.execute structural fallback failed: %v", report.Findings)
	}
	if f, ok := findings[SinkStringDump]; !ok || f.Node != "dumpFn" {
		t.Fatalf("string.dump structural fallback failed: %v", report.Findings)
	}
	if _, ok := findings[SinkDebug]; !ok {
		t.Fatalf("debug table structural fallback failed: %v", report.Findings)
	}
}

// TestShortestPathAndCycle builds two routes to debug: a short direct one
// and a long cyclic one. BFS must report the short route and terminate
// despite the cycle.
func TestShortestPathAndCycle(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("a", "").
		Table("b", "").
		Table("c", "").
		Table("debugLib", BuiltinDebug).
		Field("sandbox", "debug", "debugLib").
		Field("sandbox", "a", "a").
		Field("a", "b", "b").
		Field("b", "c", "c").
		Field("c", "a", "a"). // back edge: a -> b -> c -> a
		Field("c", "debug", "debugLib").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	findings := sinkMap(report.Findings)

	f, ok := findings[SinkDebug]
	if !ok {
		t.Fatalf("missing debug finding, got %v", report.Findings)
	}
	// Direct path sandbox.debug has depth 1.
	if len(f.Path) != 2 {
		t.Fatalf("shortest debug path length = %d, want 2 (%s)", len(f.Path), f.PathString())
	}
	if f.Path[1].Key != "debug" {
		t.Fatalf("shortest path should use direct debug field, got %s", f.PathString())
	}

	// All cycle nodes are reachable exactly once.
	if len(report.Reachable) != len(g.Nodes) {
		t.Fatalf("reachable = %d, want %d nodes", len(report.Reachable), len(g.Nodes))
	}
}

// TestUnreachableSink ensures a dangerous node disconnected from every
// root produces no finding.
func TestUnreachableSink(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Function("safe", "").
		Table("ioLib", BuiltinIO).
		Function("ioOpen", BuiltinIOOpen).
		Field("ioLib", "open", "ioOpen").
		Field("sandbox", "safe", "safe").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("disconnected io must not be reachable, got %v", report.Findings)
	}
	for _, r := range report.Reachable {
		if r.Node == "ioLib" || r.Node == "ioOpen" {
			t.Fatalf("node %s must not be reachable", r.Node)
		}
	}
}
