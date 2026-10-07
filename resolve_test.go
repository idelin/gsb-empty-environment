package resolver

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestLocalDefinition(t *testing.T) {
	d := NewDirect([]Decl{
		{Module: "app", Name: "main", Kind: Definition},
	})
	res := d.Resolve(Ref{ID: 1, Module: "app", Name: "main"})
	if !res.Resolved || res.Cyclic {
		t.Fatalf("expected resolved, got %+v", res)
	}
	if res.Endpoint != (Key{Module: "app", Name: "main"}) {
		t.Fatalf("wrong endpoint: %+v", res.Endpoint)
	}
	if len(res.Chain) != 1 || res.Chain[0].Kind != Definition {
		t.Fatalf("unexpected chain: %+v", res.Chain)
	}
}

func TestReexportChain(t *testing.T) {
	d := NewDirect([]Decl{
		{Module: "app", Name: "handler", Kind: Reexport, TargetModule: "svc", TargetName: "handler"},
		{Module: "svc", Name: "handler", Kind: Reexport, TargetModule: "core", TargetName: "handler"},
		{Module: "core", Name: "handler", Kind: Definition},
	})
	res := d.Resolve(Ref{ID: 1, Module: "app", Name: "handler"})
	if !res.Resolved {
		t.Fatalf("expected resolved, got %+v", res)
	}
	if res.Endpoint != (Key{Module: "core", Name: "handler"}) {
		t.Fatalf("wrong endpoint: %+v", res.Endpoint)
	}
	if len(res.Chain) != 3 {
		t.Fatalf("expected 3-step chain, got %+v", res.Chain)
	}
	for i, want := range []Kind{Reexport, Reexport, Definition} {
		if res.Chain[i].Kind != want {
			t.Fatalf("step %d: want %v, got %+v", i, want, res.Chain[i])
		}
	}
}

func TestCycleWithoutDefinition(t *testing.T) {
	d := NewDirect([]Decl{
		{Module: "a", Name: "x", Kind: Reexport, TargetModule: "b", TargetName: "x"},
		{Module: "b", Name: "x", Kind: Reexport, TargetModule: "a", TargetName: "x"},
	})
	res := d.Resolve(Ref{ID: 1, Module: "a", Name: "x"})
	if res.Resolved || !res.Cyclic {
		t.Fatalf("expected cyclic unresolved, got %+v", res)
	}
	if !strings.Contains(res.Reason, "cyclic") {
		t.Fatalf("unexpected reason: %q", res.Reason)
	}
}

func TestCycleAndDefinitionCoexist(t *testing.T) {
	d := NewDirect([]Decl{
		{Module: "a", Name: "x", Kind: Reexport, TargetModule: "b", TargetName: "x"},
		{Module: "b", Name: "x", Kind: Definition},
		{Module: "a", Name: "y", Kind: Reexport, TargetModule: "b", TargetName: "y"},
		{Module: "b", Name: "y", Kind: Reexport, TargetModule: "a", TargetName: "y"},
	})
	if res := d.Resolve(Ref{ID: 1, Module: "a", Name: "x"}); !res.Resolved {
		t.Fatalf("x should resolve, got %+v", res)
	}
	if res := d.Resolve(Ref{ID: 2, Module: "a", Name: "y"}); res.Resolved || !res.Cyclic {
		t.Fatalf("y should be cyclic unresolved, got %+v", res)
	}
}

func TestLocalShadowsImport(t *testing.T) {
	d := NewDirect([]Decl{
		{Module: "m", Name: "x", Kind: Definition},
		{Module: "m", Name: "x", Kind: Import, TargetModule: "lib", TargetName: "x"},
		{Module: "lib", Name: "x", Kind: Definition},
		{Module: "n", Name: "x", Kind: Import, TargetModule: "lib", TargetName: "x"},
	})
	res := d.Resolve(Ref{ID: 1, Module: "m", Name: "x"})
	if !res.Resolved || res.Endpoint != (Key{Module: "m", Name: "x"}) {
		t.Fatalf("local definition should shadow import, got %+v", res)
	}
	if !strings.Contains(res.Chain[0].Detail, "shadows import") {
		t.Fatalf("chain should mention shadowing, got %+v", res.Chain[0])
	}
	res = d.Resolve(Ref{ID: 2, Module: "n", Name: "x"})
	if !res.Resolved || res.Endpoint != (Key{Module: "lib", Name: "x"}) {
		t.Fatalf("import should be followed, got %+v", res)
	}
}

func TestReexportShadowsImport(t *testing.T) {
	d := NewDirect([]Decl{
		{Module: "m", Name: "x", Kind: Reexport, TargetModule: "a", TargetName: "x"},
		{Module: "m", Name: "x", Kind: Import, TargetModule: "lib", TargetName: "x"},
		{Module: "a", Name: "x", Kind: Definition},
		{Module: "lib", Name: "x", Kind: Definition},
	})
	res := d.Resolve(Ref{ID: 1, Module: "m", Name: "x"})
	if !res.Resolved || res.Endpoint != (Key{Module: "a", Name: "x"}) {
		t.Fatalf("re-export should shadow import, got %+v", res)
	}
}

func TestUnresolved(t *testing.T) {
	d := NewDirect(nil)
	res := d.Resolve(Ref{ID: 1, Module: "m", Name: "ghost"})
	if res.Resolved || res.Cyclic {
		t.Fatalf("expected plain unresolved, got %+v", res)
	}
}

func TestIncrementalInvalidation(t *testing.T) {
	inc := NewIncremental()
	inc.PutDecl(Decl{Module: "a", Name: "x", Kind: Reexport, TargetModule: "b", TargetName: "x"})
	inc.PutDecl(Decl{Module: "b", Name: "x", Kind: Reexport, TargetModule: "a", TargetName: "x"})
	inc.AddRef(Ref{ID: 1, Module: "a", Name: "x"})

	res, ok := inc.Resolve(1)
	if !ok || res.Resolved || !res.Cyclic {
		t.Fatalf("expected cyclic unresolved, got %+v", res)
	}

	// Edit: give b.x a real definition; the cycle becomes resolvable.
	inc.PutDecl(Decl{Module: "b", Name: "x", Kind: Definition})
	inc.DeleteDecl("b", "x", Reexport)
	res, _ = inc.Resolve(1)
	if !res.Resolved || res.Endpoint != (Key{Module: "b", Name: "x"}) {
		t.Fatalf("expected resolution to b.x after edit, got %+v", res)
	}

	// Delete: removing the definition makes the reference unresolved.
	inc.DeleteDecl("b", "x", Definition)
	res, _ = inc.Resolve(1)
	if res.Resolved || res.Cyclic {
		t.Fatalf("expected unresolved after delete, got %+v", res)
	}

	if _, ok := inc.Resolve(999); ok {
		t.Fatal("unknown ref ID should report !ok")
	}
}

// TestIncrementalMatchesDirect applies random declaration edits and
// deletions and checks after every round that the incremental resolver
// agrees with a from-scratch Direct resolution of every reference.
func TestIncrementalMatchesDirect(t *testing.T) {
	seed := int64(1)
	if s := os.Getenv("RESOLVER_SEED"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			seed = v
		}
	}
	rng := rand.New(rand.NewSource(seed))
	inc := NewIncremental()
	golden := make(map[Key]map[Kind]Decl)
	refs := make(map[int]Ref)
	modules := []string{"m0", "m1", "m2", "m3", "m4"}
	names := []string{"a", "b", "c", "d"}
	kinds := []Kind{Definition, Reexport, Import}
	nextRef := 0

	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }

	flatten := func() []Decl {
		var out []Decl
		for _, slot := range golden {
			for _, d := range slot {
				out = append(out, d)
			}
		}
		sort.Slice(out, func(i, j int) bool {
			if out[i].Module != out[j].Module {
				return out[i].Module < out[j].Module
			}
			if out[i].Name != out[j].Name {
				return out[i].Name < out[j].Name
			}
			return out[i].Kind < out[j].Kind
		})
		return out
	}

	for round := 0; round < 300; round++ {
		for op := 1 + rng.Intn(4); op > 0; op-- {
			switch rng.Intn(5) {
			case 0, 1: // add or edit a declaration
				d := Decl{Module: pick(modules), Name: pick(names), Kind: kinds[rng.Intn(len(kinds))]}
				if d.Kind != Definition {
					d.TargetModule = pick(modules)
					d.TargetName = pick(names)
				}
				inc.PutDecl(d)
				k := d.Key()
				if golden[k] == nil {
					golden[k] = make(map[Kind]Decl)
				}
				golden[k][d.Kind] = d
			case 2: // delete a declaration
				if len(golden) == 0 {
					continue
				}
				keys := make([]Key, 0, len(golden))
				for k := range golden {
					keys = append(keys, k)
				}
				k := keys[rng.Intn(len(keys))]
				ks := make([]Kind, 0, len(golden[k]))
				for kind := range golden[k] {
					ks = append(ks, kind)
				}
				kind := ks[rng.Intn(len(ks))]
				inc.DeleteDecl(k.Module, k.Name, kind)
				delete(golden[k], kind)
				if len(golden[k]) == 0 {
					delete(golden, k)
				}
			case 3: // add a reference
				nextRef++
				r := Ref{ID: nextRef, Module: pick(modules), Name: pick(names)}
				inc.AddRef(r)
				refs[r.ID] = r
			case 4: // remove a reference
				if len(refs) == 0 {
					continue
				}
				ids := make([]int, 0, len(refs))
				for id := range refs {
					ids = append(ids, id)
				}
				id := ids[rng.Intn(len(ids))]
				inc.RemoveRef(id)
				delete(refs, id)
			}
		}

		direct := NewDirect(flatten())
		for id, r := range refs {
			got, ok := inc.Resolve(id)
			if !ok {
				t.Fatalf("round %d: ref %d missing from incremental resolver", round, id)
			}
			want := direct.Resolve(r)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("round %d ref %+v mismatch\nincremental: %+v\ndirect:      %+v",
					round, r, formatResult(got), formatResult(want))
			}
		}
	}
}

func formatResult(r Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "resolved=%v cyclic=%v endpoint=%+v reason=%q", r.Resolved, r.Cyclic, r.Endpoint, r.Reason)
	for _, s := range r.Chain {
		fmt.Fprintf(&b, "\n  step %+v %s", s.Key, s.Detail)
	}
	return b.String()
}
