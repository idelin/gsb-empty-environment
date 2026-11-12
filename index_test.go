package netsec

import (
	"context"
	"strings"
	"testing"
)

// TestExplicitIndexEdge uses the semantic EdgeIndex representation of an
// __index forward instead of modeling it as a field edge.
func TestExplicitIndexEdge(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", "").
		Table("stringMeta", "").
		Table("realString", BuiltinString).
		Function("dumpFn", BuiltinStringDump).
		Metatable("stringMeta", "metaMeta").
		Table("metaMeta", "").
		Index("metaMeta", "realString").
		Field("realString", "dump", "dumpFn").
		Root("sandbox", RootSandbox).
		Root("stringMeta", RootStringMetatable).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	findings := sinkMap(report.Findings)

	f, ok := findings[SinkStringDump]
	if !ok {
		t.Fatalf("expected string.dump via explicit __index edge, got %v", report.Findings)
	}
	rendered := f.PathString()
	if !strings.Contains(rendered, "[__index]") {
		t.Fatalf("path must show explicit __index hop: %s", rendered)
	}
	assertPath(t, f, RootStringMetatable, "[metatable]", "[__index]", ".dump", "dumpFn")
}

// TestRootItselfIsSink guards against a root table that is itself a
// dangerous library, for example a sandbox accidentally seeded with io.
func TestRootItselfIsSink(t *testing.T) {
	g := NewBuilder().
		Table("sandbox", BuiltinIO).
		Function("open", BuiltinIOOpen).
		Field("sandbox", "open", "open").
		Root("sandbox", RootSandbox).
		Build()

	report, err := Audit(context.Background(), g)
	if err != nil {
		t.Fatalf("audit failed: %v", err)
	}
	findings := sinkMap(report.Findings)
	f, ok := findings[SinkIO]
	if !ok {
		t.Fatalf("root io table must be flagged, got %v", report.Findings)
	}
	if len(f.Path) != 1 || f.Path[0].Root != RootSandbox {
		t.Fatalf("sink root path malformed: %s", f.PathString())
	}
}
