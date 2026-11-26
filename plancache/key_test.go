package plancache_test

import (
	"testing"

	"formal.local/w3-bc80ec5616/plancache"
)

// TestKeyHashDistinguishesEveryDimension: any change to an
// authorization-relevant dimension changes the full key hash, while
// reordering-independent structural equality keeps it stable.
func TestKeyHashDistinguishesEveryDimension(t *testing.T) {
	base := baselineInput(alice, "roles:alice:v1")
	baseHash := plancache.KeyOf(base).Hash()

	mutants := map[string]func(plancache.CompileInput) plancache.CompileInput{
		"ast": func(in plancache.CompileInput) plancache.CompileInput {
			in.ASTDigest = "ast:different"
			return in
		},
		"param-types": func(in plancache.CompileInput) plancache.CompileInput {
			in.ParamTypes = []string{"text"}
			return in
		},
		"principal": func(in plancache.CompileInput) plancache.CompileInput {
			in.Caller = mallory
			return in
		},
		"role-closure": func(in plancache.CompileInput) plancache.CompileInput {
			in.RoleClosure = "roles:alice:v2"
			return in
		},
		"view-version": func(in plancache.CompileInput) plancache.CompileInput {
			in.DepGraph[0].View.ViewDefVersion = 2
			return in
		},
		"view-id": func(in plancache.CompileInput) plancache.CompileInput {
			in.DepGraph[0].View.ViewID = outerV2
			return in
		},
		"object-id": func(in plancache.CompileInput) plancache.CompileInput {
			in.DepGraph[2].Table.ObjectID = ordersO2
			return in
		},
		"schema-version": func(in plancache.CompileInput) plancache.CompileInput {
			in.DepGraph[2].Table.SchemaVersion = 2
			return in
		},
		"rls-digest": func(in plancache.CompileInput) plancache.CompileInput {
			in.DepGraph[2].Table.RLSPolicy = rlsV2
			in.RLSPolicies[ordersO] = rlsV2
			return in
		},
		"epoch": func(in plancache.CompileInput) plancache.CompileInput {
			in.PolicyEpoch = 2
			return in
		},
		"ops": func(in plancache.CompileInput) plancache.CompileInput {
			in.RequiredOps = []plancache.Operation{"UPDATE"}
			return in
		},
	}

	for name, mutate := range mutants {
		t.Run(name, func(t *testing.T) {
			got := plancache.KeyOf(mutate(baselineInput(alice, "roles:alice:v1"))).Hash()
			if got == baseHash {
				t.Fatalf("dimension %s did not change the key hash", name)
			}
		})
	}

	// RLS map insertion order must not affect the hash.
	in1 := baselineInput(alice, "roles:alice:v1")
	in2 := baselineInput(alice, "roles:alice:v1")
	in2.RLSPolicies = map[plancache.ObjectID]plancache.Digest{ordersO: rlsV1}
	if plancache.KeyOf(in1).Hash() != plancache.KeyOf(in2).Hash() {
		t.Fatal("equal RLS maps must hash identically regardless of map order")
	}
}

// TestKeyContainsNoTextOrNameSurface: the key type exposes no SQL text or
// relation-name field; identity is AST digest plus object/view IDs.
func TestKeyContainsNoTextOrNameSurface(t *testing.T) {
	key := plancache.KeyOf(baselineInput(alice, "roles:alice:v1"))
	if key.Caller != alice || key.ASTDigest != ast1 {
		t.Fatal("unexpected key contents")
	}
	if len(key.DepGraph) != 3 || key.DepGraph[0].View.ViewID != outerV {
		t.Fatal("dependency graph must carry view identities, not names")
	}
}
