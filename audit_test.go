package netsec

import (
	"context"
	"strings"
	"testing"
)

func sinkMap(findings []Finding) map[SinkKind]Finding {
	m := make(map[SinkKind]Finding, len(findings))
	for _, f := range findings {
		m[f.Sink] = f
	}
	return m
}

// assertPath renders the finding path and requires all fragments to appear
// in order.
func assertPath(t *testing.T, f Finding, fragments ...string) {
	t.Helper()
	rendered := f.PathString()
	pos := 0
	for _, frag := range fragments {
		idx := strings.Index(rendered[pos:], frag)
		if idx < 0 {
			t.Fatalf("path %q missing fragment %q (want order %v)", rendered, frag, fragments)
		}
		pos += idx + len(frag)
	}
}

// TestMetatableIndexBacktrack models the classic string-metatable escape:
// sandbox code reaches a method through the shared string metatable, whose
// __index chain forwards into the real string and debug tables even though
// those globals were removed from the sandbox environment.
func TestMetatableIndexBacktrack(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("stringMeta", "").
		Table("stringLib", BuiltinString).
		Function("stringDump", BuiltinStringDump).
		Table("indexFallback", "").
		Table("debugLib", BuiltinDebug).
		Function("debugTraceback", BuiltinDebugTrace).
		Field("sandbox", "format", "formatFn").
		Function("formatFn", "string.format").
		Metatable("formatFn", "stringMeta").
		Metatable("stringMeta", "stringMetaMeta").
		Table("stringMetaMeta", "").
		Field("stringMetaMeta", "__index", "indexFallback").
		Field("indexFallback", "debug", "debugLib").
		Field("stringMeta", "dump", "stringDump").
		Root("sandbox", RootSandbox).
		Root("stringMeta", RootStringMetatable).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}

	findings := sinkMap(report.Findings)

	dump, ok := findings[SinkStringDump]
	if !ok {
		t.Fatalf("expected string.dump finding, got %v", report.Findings)
	}
	if dump.Node != "stringDump" {
		t.Fatalf("string.dump points at %q, want stringDump", dump.Node)
	}
	// Shortest route starts at the shared string metatable root.
	if dump.Path[0].Root != RootStringMetatable {
		t.Fatalf("string.dump path root = %q, want %q", dump.Path[0].Root, RootStringMetatable)
	}
	assertPath(t, dump, RootStringMetatable, ".dump", "stringDump")

	// The metatable's own metatable forwards __index into a fallback table
	// that still references debug.
	dbg, ok := findings[SinkDebug]
	if !ok {
		t.Fatalf("expected debug.* finding via __index backtrack, got %v", report.Findings)
	}
	assertPath(t, dbg, RootStringMetatable, "[metatable]", ".__index", ".debug", "debugLib")

	if report.Safe() {
		t.Fatal("report must not be safe")
	}
}

// TestUpvalueLeak checks that a sandbox-exposed closure can re-capture a
// removed global through its upvalues, and that upvalue edges render with
// their slot and name.
func TestUpvalueLeak(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Function("helper", "").
		Table("realGlobals", "").
		Table("ioLib", BuiltinIO).
		Function("ioOpen", BuiltinIOOpen).
		Function("loader", "").
		Table("osLib", BuiltinOS).
		Function("osExecute", BuiltinOSExecute).
		Field("sandbox", "helper", "helper").
		Upvalue("helper", 1, "env", "realGlobals").
		Field("realGlobals", "io", "ioLib").
		Field("ioLib", "open", "ioOpen").
		Field("sandbox", "loader", "loader").
		Upvalue("loader", 2, "", "osLib").
		Field("osLib", "execute", "osExecute").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}

	findings := sinkMap(report.Findings)

	ioOpen, ok := findings[SinkIO]
	if !ok {
		t.Fatalf("expected io.* finding, got %v", report.Findings)
	}
	assertPath(t, ioOpen, RootSandbox, ".helper", "[upvalue#1 env]", ".io", "ioLib")

	exec, ok := findings[SinkOSExecute]
	if !ok {
		t.Fatalf("expected os.execute finding, got %v", report.Findings)
	}
	assertPath(t, exec, RootSandbox, ".loader", "[upvalue#2]", ".execute", "osExecute")
	if !strings.Contains(exec.PathString(), "[upvalue#2]") {
		t.Fatalf("anonymous upvalue rendering wrong: %s", exec.PathString())
	}
}

// TestWhitelistEnvironment is a sealed sandbox: only a vetted allowlist of
// safe functions and libraries is reachable, so no finding is produced.
func TestWhitelistEnvironment(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("mathLib", "math").
		Function("mathAdd", "").
		Table("stringLib", BuiltinString).
		Function("safeFormat", "string.format").
		Function("safePcall", "pcall").
		Function("safePairs", "pairs").
		Field("sandbox", "math", "mathLib").
		Field("mathLib", "add", "mathAdd").
		Field("sandbox", "format", "safeFormat").
		Field("sandbox", "pcall", "safePcall").
		Field("sandbox", "pairs", "safePairs").
		Field("sandbox", "string", "stringLib").
		Field("stringLib", "format", "safeFormat").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("whitelist sandbox must be safe, found %v", report.Findings)
	}
	// Every node must still be accounted as reachable.
	if len(report.Reachable) != len(g.Nodes) {
		t.Fatalf("reachable = %d nodes, want %d", len(report.Reachable), len(g.Nodes))
	}
}

// TestHarmlessMathLibrary confirms that a math library, including one
// exposed via a shared metatable with __index, carries no dangerous sink.
func TestHarmlessMathLibrary(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("numberMeta", "").
		Table("mathLib", "math").
		Function("mathFloor", "math.floor").
		Function("mathRandom", "math.random").
		Table("safeIndex", "").
		Metatable("numberMeta", "numberMetaMeta").
		Table("numberMetaMeta", "").
		Field("numberMetaMeta", "__index", "safeIndex").
		Field("safeIndex", "floor", "mathFloor").
		Field("safeIndex", "random", "mathRandom").
		Field("sandbox", "math", "mathLib").
		Root("sandbox", RootSandbox).
		Root("numberMeta", RootNumberMetatable).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	if !report.Safe() {
		t.Fatalf("math-only environment must be safe, found %v", report.Findings)
	}

	// The shared number metatable root must reach the math functions even
	// though the sandbox table does not expose them directly.
	byNode := make(map[string]Reachable, len(report.Reachable))
	for _, r := range report.Reachable {
		byNode[r.Node] = r
	}
	floor, ok := byNode["mathFloor"]
	if !ok {
		t.Fatal("math.floor not reported as reachable")
	}
	if floor.Root != RootNumberMetatable {
		t.Fatalf("math.floor root = %q, want %q", floor.Root, RootNumberMetatable)
	}
	if floor.Depth != 3 {
		t.Fatalf("math.floor depth = %d, want 3", floor.Depth)
	}
}
