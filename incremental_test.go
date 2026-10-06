package resolve

import (
	"math/rand"
	"reflect"
	"testing"
)

// TestIncrementalMatchesDirect applies random declaration/import
// additions, edits and deletions to both models and checks after every
// step that the incremental resolver agrees with the direct model on
// every possible reference.
func TestIncrementalMatchesDirect(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	modules := []string{"m0", "m1", "m2", "m3"}
	names := []string{"a", "b", "c", "d", "e"}

	var keys []Key
	for _, m := range modules {
		for _, n := range names {
			keys = append(keys, k(m, n))
		}
	}
	pick := func() Key { return keys[rng.Intn(len(keys))] }

	inc := NewResolver()
	dir := NewDirect()

	apply := func(op func(*Resolver), dop func(*Direct)) { op(inc); dop(dir) }

	for step := 0; step < 4000; step++ {
		switch rng.Intn(6) {
		case 0:
			key := pick()
			apply(func(r *Resolver) { r.SetLocal(key, Decl{Kind: Def}) },
				func(d *Direct) { d.SetLocal(key, Decl{Kind: Def}) })
		case 1:
			key, tgt := pick(), pick()
			apply(func(r *Resolver) { r.SetLocal(key, Decl{Kind: ReExport, Target: tgt}) },
				func(d *Direct) { d.SetLocal(key, Decl{Kind: ReExport, Target: tgt}) })
		case 2:
			key := pick()
			apply(func(r *Resolver) { r.DeleteLocal(key) },
				func(d *Direct) { d.DeleteLocal(key) })
		case 3:
			from, to := pick(), pick()
			apply(func(r *Resolver) { r.SetImport(from, to) },
				func(d *Direct) { d.SetImport(from, to) })
		case 4:
			from := pick()
			apply(func(r *Resolver) { r.DeleteImport(from) },
				func(d *Direct) { d.DeleteImport(from) })
		case 5:
			// no mutation: exercise cache hits
		}
		for _, key := range keys {
			got := inc.Resolve(key)
			want := dir.Resolve(key)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d, key %v:\nincremental: %v\ndirect:      %v",
					step, key, got.Explain(), want.Explain())
			}
		}
	}
}

// TestDeleteThenReresolve checks a targeted edit sequence: a chain is
// resolved, a middle definition is deleted (becomes unresolved), and a
// re-export edit then creates a cycle.
func TestDeleteThenReresolve(t *testing.T) {
	inc := NewResolver()
	dir := NewDirect()
	setBoth := func(key Key, d Decl) {
		inc.SetLocal(key, d)
		dir.SetLocal(key, d)
	}
	setBoth(k("a", "x"), Decl{Kind: ReExport, Target: k("b", "x")})
	setBoth(k("b", "x"), Decl{Kind: Def})

	if res := inc.Resolve(k("a", "x")); res.Status != Resolved {
		t.Fatalf("precondition: %v", res.Explain())
	}

	inc.DeleteLocal(k("b", "x"))
	dir.DeleteLocal(k("b", "x"))
	got, want := inc.Resolve(k("a", "x")), dir.Resolve(k("a", "x"))
	if got.Status != Unresolved || !reflect.DeepEqual(got, want) {
		t.Fatalf("after delete: got %v want %v", got.Explain(), want.Explain())
	}

	// Edit b.x into a re-export back to a.x: now the chain cycles.
	edit := Decl{Kind: ReExport, Target: k("a", "x")}
	inc.SetLocal(k("b", "x"), edit)
	dir.SetLocal(k("b", "x"), edit)
	got, want = inc.Resolve(k("a", "x")), dir.Resolve(k("a", "x"))
	if got.Status != CycleUnresolved || !reflect.DeepEqual(got, want) {
		t.Fatalf("after edit: got %v want %v", got.Explain(), want.Explain())
	}
}
