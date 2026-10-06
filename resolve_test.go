package resolve

import (
	"strings"
	"testing"
)

func k(module, name string) Key { return Key{Module: module, Name: name} }

func TestLocalDefinition(t *testing.T) {
	d := NewDirect()
	d.SetLocal(k("a", "x"), Decl{Kind: Def})
	res := d.Resolve(k("a", "x"))
	if res.Status != Resolved || res.Endpoint != k("a", "x") {
		t.Fatalf("got %v", res)
	}
	if len(res.Chain) != 1 || res.Chain[0].Via != "definition" {
		t.Fatalf("unexpected chain: %v", res.Chain)
	}
}

func TestReExportChain(t *testing.T) {
	d := NewDirect()
	d.SetLocal(k("a", "x"), Decl{Kind: ReExport, Target: k("b", "x")})
	d.SetLocal(k("b", "x"), Decl{Kind: ReExport, Target: k("c", "y")})
	d.SetLocal(k("c", "y"), Decl{Kind: Def})
	res := d.Resolve(k("a", "x"))
	if res.Status != Resolved || res.Endpoint != k("c", "y") {
		t.Fatalf("got %v", res)
	}
	want := []string{"re-export", "re-export", "definition"}
	if len(res.Chain) != len(want) {
		t.Fatalf("chain: %v", res.Chain)
	}
	for i, w := range want {
		if res.Chain[i].Via != w {
			t.Fatalf("step %d: got %q want %q", i, res.Chain[i].Via, w)
		}
	}
}

func TestImportHop(t *testing.T) {
	d := NewDirect()
	d.SetImport(k("a", "x"), k("b", "x"))
	d.SetLocal(k("b", "x"), Decl{Kind: Def})
	res := d.Resolve(k("a", "x"))
	if res.Status != Resolved || res.Endpoint != k("b", "x") {
		t.Fatalf("got %v", res)
	}
	if res.Chain[0].Via != "import" {
		t.Fatalf("first hop should be import: %v", res.Chain)
	}
}

func TestLocalShadowsImport(t *testing.T) {
	d := NewDirect()
	d.SetImport(k("a", "x"), k("b", "x"))
	d.SetLocal(k("b", "x"), Decl{Kind: Def})
	d.SetLocal(k("a", "x"), Decl{Kind: Def}) // shadows the import
	res := d.Resolve(k("a", "x"))
	if res.Status != Resolved || res.Endpoint != k("a", "x") {
		t.Fatalf("local declaration should shadow import: %v", res)
	}
	if !strings.Contains(res.Chain[0].Note, "shadows import") {
		t.Fatalf("expected shadowing note, got %q", res.Chain[0].Note)
	}
}

func TestUnresolved(t *testing.T) {
	d := NewDirect()
	res := d.Resolve(k("a", "missing"))
	if res.Status != Unresolved {
		t.Fatalf("got %v", res)
	}
	// Chain through a re-export into nowhere is also unresolved.
	d.SetLocal(k("a", "x"), Decl{Kind: ReExport, Target: k("b", "x")})
	res = d.Resolve(k("a", "x"))
	if res.Status != Unresolved || len(res.Chain) != 1 {
		t.Fatalf("got %v", res)
	}
}

func TestCycleUnresolved(t *testing.T) {
	d := NewDirect()
	d.SetLocal(k("a", "x"), Decl{Kind: ReExport, Target: k("b", "x")})
	d.SetLocal(k("b", "x"), Decl{Kind: ReExport, Target: k("a", "x")})
	res := d.Resolve(k("a", "x"))
	if res.Status != CycleUnresolved {
		t.Fatalf("got %v", res)
	}
	if len(res.Cycle) != 2 || res.Cycle[0] != k("a", "x") || res.Cycle[1] != k("b", "x") {
		t.Fatalf("cycle: %v", res.Cycle)
	}
}

func TestCycleThroughImports(t *testing.T) {
	d := NewDirect()
	d.SetImport(k("a", "x"), k("b", "x"))
	d.SetImport(k("b", "x"), k("a", "x"))
	res := d.Resolve(k("a", "x"))
	if res.Status != CycleUnresolved {
		t.Fatalf("got %v", res)
	}
}

func TestCycleBrokenByDefinition(t *testing.T) {
	d := NewDirect()
	d.SetLocal(k("a", "x"), Decl{Kind: ReExport, Target: k("b", "x")})
	d.SetLocal(k("b", "x"), Decl{Kind: ReExport, Target: k("a", "x")})
	// Introducing a real definition breaks the cycle for keys that
	// can reach it: retarget b.x at a real definition.
	d.SetLocal(k("b", "x"), Decl{Kind: ReExport, Target: k("c", "x")})
	d.SetLocal(k("c", "x"), Decl{Kind: Def})
	res := d.Resolve(k("a", "x"))
	if res.Status != Resolved || res.Endpoint != k("c", "x") {
		t.Fatalf("got %v", res)
	}
}

func TestExplain(t *testing.T) {
	d := NewDirect()
	d.SetImport(k("a", "x"), k("b", "x"))
	d.SetLocal(k("b", "x"), Decl{Kind: ReExport, Target: k("c", "x")})
	d.SetLocal(k("c", "x"), Decl{Kind: Def})
	s := d.Resolve(k("a", "x")).Explain()
	if !strings.Contains(s, "a.x (import)") || !strings.Contains(s, "b.x (re-export)") ||
		!strings.Contains(s, "c.x (definition)") || !strings.Contains(s, "=> resolved at c.x") {
		t.Fatalf("explain: %s", s)
	}
}
