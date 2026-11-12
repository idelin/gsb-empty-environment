package netsec_test

import (
	"slices"
	"strconv"
	"testing"

	netsec "example.org/netsec/q0481"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected builder error: %v", err)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

func flatten(cs []netsec.Clash) []string {
	var out []string
	for _, c := range cs {
		out = append(out, c.Prov...)
	}
	return out
}

// TestPurposeConfusion reproduces the teaching-protocol bug: an encryption key
// is aliased into an authentication slot and then used for signature
// verification. The solver must flag a purpose clash, and the minimum conflict
// operation set must contain only the offending alias + sign operations.
func TestPurposeConfusion(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-enc", netsec.K("kA"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.AddBindKey("bind-alias", netsec.K("authSlot"), netsec.K("kA")))
	must(t, s.AddSign("sign-wrong", netsec.K("authSlot"), netsec.K("payload")))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if !rep.Confusable {
		t.Fatal("expected purpose confusion to be detected")
	}
	if len(rep.Clashes) != 1 || rep.Clashes[0].Dimension != "purpose" {
		t.Fatalf("expected one purpose clash, got %+v", rep.Clashes)
	}
	if len(rep.ConflictOps) != 1 {
		t.Fatalf("minimum conflict ops = %v, want one removable action", rep.ConflictOps)
	}
	if !slices.Contains([]string{"sign-wrong", "bind-alias"}, rep.ConflictOps[0]) {
		t.Fatalf("minimum conflict op = %q, want sign-wrong or bind-alias", rep.ConflictOps[0])
	}
	if len(rep.MinConflictSets) != 2 {
		t.Fatalf("expected two equivalent minimum sets, got %v", rep.MinConflictSets)
	}
	if kt := rep.KeyTypes["literal:kA"]; kt.Owner != "alice" || kt.SessionID != "s1" {
		t.Fatalf("unconflicted dimensions of kA inferred wrong: %+v", kt)
	}
}

func TestWellTypedSeparateConstructors(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-enc", netsec.K("enc"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.AddNewKey("new-ver", netsec.K("ver"), "bob", netsec.PurposeVerify, "s1"))
	must(t, s.AddNewKey("new-der", netsec.K("der"), "alice", netsec.PurposeDerive, "s1"))
	must(t, s.AddEncrypt("enc-msg", netsec.K("enc")))
	must(t, s.AddSign("verify-sig", netsec.K("ver")))
	must(t, s.AddDerive("derive-child", netsec.K("der")))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if rep.Confusable {
		t.Fatalf("well-typed script reported clashes: %+v", rep.Clashes)
	}
	if !rep.Stats.Exact {
		t.Fatal("exact minimum-set search expected")
	}
	wantTypes := map[string]netsec.KeyType{
		"literal:enc": {Owner: "alice", Purpose: netsec.PurposeEncrypt, SessionID: "s1"},
		"literal:ver": {Owner: "bob", Purpose: netsec.PurposeVerify, SessionID: "s1"},
		"literal:der": {Owner: "alice", Purpose: netsec.PurposeDerive, SessionID: "s1"},
	}
	for label, want := range wantTypes {
		if rep.KeyTypes[label] != want {
			t.Fatalf("KeyTypes[%s] = %+v, want %+v", label, rep.KeyTypes[label], want)
		}
	}
}

// TestPolymorphicHelperConfusion uses one polymorphic helper whose parameter
// flows through both an encryption and a signature constructor. The parameter
// is forced to two incompatible purposes at one call site.
func TestPolymorphicHelperConfusion(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-k", netsec.K("k"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.DeclareFunc(&netsec.Func{
		Name:      "WrapAndAuth",
		KeyParams: []string{"x"},
		Body: []netsec.Node{
			netsec.Encrypt{Key: netsec.P("x")},
			netsec.Sign{Key: netsec.P("x")},
		},
	}))
	must(t, s.CallFunc("call-1", "WrapAndAuth", []netsec.Term{netsec.K("k")}))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if !rep.Confusable {
		t.Fatal("expected polymorphic helper purpose clash")
	}
	if len(rep.ConflictOps) != 1 || rep.ConflictOps[0] != "call-1.n0.enc" {
		t.Fatalf("minimum conflict ops = %v, want [call-1.n0.enc]", rep.ConflictOps)
	}
}

// TestPolymorphicHelperReused verifies that separate call sites of the same
// polymorphic helper receive fresh type variables, so an encryption key and a
// verification key are never identified across calls.
func TestPolymorphicHelperReused(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-e", netsec.K("e"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.AddNewKey("new-v", netsec.K("v"), "bob", netsec.PurposeVerify, "s1"))
	must(t, s.DeclareFunc(&netsec.Func{
		Name:      "Use",
		KeyParams: []string{"x"},
		Body:      []netsec.Node{netsec.Encrypt{Key: netsec.P("x")}},
	}))
	must(t, s.DeclareFunc(&netsec.Func{
		Name:      "Check",
		KeyParams: []string{"x"},
		Body:      []netsec.Node{netsec.Sign{Key: netsec.P("x")}},
	}))
	must(t, s.CallFunc("call-e", "Use", []netsec.Term{netsec.K("e")}))
	must(t, s.CallFunc("call-v", "Check", []netsec.Term{netsec.K("v")}))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if rep.Confusable {
		t.Fatalf("fresh call-site variables leaked across calls: %+v", rep.Clashes)
	}
	if rep.KeyTypes["literal:e"].Purpose != netsec.PurposeEncrypt ||
		rep.KeyTypes["literal:v"].Purpose != netsec.PurposeVerify {
		t.Fatalf("key types merged across call sites: %+v", rep.KeyTypes)
	}
}

// TestRecursiveRoleTemplate unrolls a re-keying role. Each frame declares a
// local key bound to the frame epoch, then signs with it. The correct template
// stays well-typed across all session generations.
func TestRecursiveRoleTemplate(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-root", netsec.K("root"), "server", netsec.PurposeVerify, "0"))
	must(t, s.DeclareRole(&netsec.Role{
		Epoch: "epoch",
		Func: netsec.Func{
			Name:      "Rekey",
			KeyParams: []string{"root"},
			Body: []netsec.Node{
				netsec.NewKey{LocalID: "session", Owner: "server", Purpose: netsec.PurposeVerify, Epoch: "epoch"},
				netsec.Sign{Key: netsec.L("session")},
				netsec.Recurse{Epoch: "epoch"},
			},
		},
	}))
	must(t, s.CallRole("role-1", "Rekey", 3, []netsec.Term{netsec.K("root")}))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	if rep.Confusable {
		t.Fatalf("correct recursive role reported clashes: %+v", rep.Clashes)
	}
	for frame := 0; frame < 3; frame++ {
		label := "role-1.f" + itoa(frame) + ".key.session"
		kt, ok := rep.KeyTypes[label]
		if !ok {
			t.Fatalf("missing inferred type for %s", label)
		}
		if kt.SessionID != itoa(frame) || kt.Purpose != netsec.PurposeVerify || kt.Owner != "server" {
			t.Fatalf("frame %d inferred %+v", frame, kt)
		}
	}
}

// TestRecursiveRoleConfusion injects the protocol bug into the role: the
// per-session verification key is aliased to an encryption root key. Every
// unrolled frame yields a purpose clash, each repaired by one operation.
func TestRecursiveRoleConfusion(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-enc-root", netsec.K("encRoot"), "server", netsec.PurposeEncrypt, "0"))
	must(t, s.DeclareRole(&netsec.Role{
		Epoch: "epoch",
		Func: netsec.Func{
			Name:      "BadRekey",
			KeyParams: []string{"root"},
			Body: []netsec.Node{
				netsec.NewKey{LocalID: "session", Owner: "server", Purpose: netsec.PurposeVerify, Epoch: "epoch"},
				netsec.BindKey{Target: netsec.L("session"), Source: netsec.P("root")},
				netsec.Sign{Key: netsec.L("session")},
				netsec.Recurse{Epoch: "epoch"},
			},
		},
	}))
	must(t, s.CallRole("role-bad", "BadRekey", 3, []netsec.Term{netsec.K("encRoot")}))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	purposeClash := false
	for _, c := range rep.Clashes {
		if c.Dimension == "purpose" &&
			slices.Contains(c.Values, string(netsec.PurposeVerify)) &&
			slices.Contains(c.Values, string(netsec.PurposeEncrypt)) {
			purposeClash = true
			for frame := 0; frame < 3; frame++ {
				bind := "role-bad.f" + itoa(frame) + ".n1.bind"
				if !slices.Contains(c.Prov, bind) {
					t.Fatalf("purpose clash provenance missing frame op %s: %v", bind, c.Prov)
				}
			}
		}
	}
	if !rep.Confusable || !purposeClash {
		t.Fatalf("expected a per-frame purpose clash with all frames in provenance, got %+v", rep.Clashes)
	}
	if len(rep.ConflictOps) != 1 {
		t.Fatalf("one shared frame alias should hit the merged clash classes, got %v", rep.ConflictOps)
	}
	for _, op := range rep.ConflictOps {
		if !slices.Contains(flatten(rep.Clashes), op) {
			t.Fatalf("conflict op %q absent from clash provenance", op)
		}
	}
}

func TestOwnerAndSessionClashes(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-a", netsec.K("a"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.AddNewKey("new-b", netsec.K("b"), "bob", netsec.PurposeEncrypt, "s2"))
	must(t, s.AddBindKey("alias", netsec.K("a"), netsec.K("b")))

	rep := netsec.Solve(s, 0)
	if len(rep.Errors) != 0 {
		t.Fatalf("unexpected errors: %v", rep.Errors)
	}
	dims := map[string]bool{}
	for _, c := range rep.Clashes {
		dims[c.Dimension] = true
	}
	if !dims["owner"] || !dims["session"] {
		t.Fatalf("expected owner and session clashes, got %+v", rep.Clashes)
	}
	if !slices.Equal(rep.ConflictOps, []string{"alias"}) {
		t.Fatalf("single alias must repair both clashes, got %v", rep.ConflictOps)
	}
}

// TestMinimumSetHittingMultipleClashes builds two purpose clashes sharing one
// bad alias operation. A genuine minimum hitting set must pick the shared
// alias rather than one operation per clash.
func TestMinimumSetHittingMultipleClashes(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-enc", netsec.K("enc"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.AddNewKey("new-ver", netsec.K("ver"), "bob", netsec.PurposeVerify, "s1"))
	must(t, s.AddNewKey("new-der", netsec.K("der"), "carol", netsec.PurposeDerive, "s1"))
	must(t, s.AddBindKey("alias-1", netsec.K("ver"), netsec.K("enc")))
	must(t, s.AddBindKey("alias-2", netsec.K("der"), netsec.K("enc")))

	rep := netsec.Solve(s, 0)
	if !rep.Confusable || len(rep.Clashes) < 2 {
		t.Fatalf("expected at least two clashes, got %+v", rep.Clashes)
	}
	if len(rep.ConflictOps) != 1 {
		t.Fatalf("one shared alias must hit both clashes, got %v", rep.ConflictOps)
	}
	allProv := flatten(rep.Clashes)
	for _, set := range rep.MinConflictSets {
		if len(set) != len(rep.ConflictOps) {
			t.Fatalf("non-minimum set returned: %v", set)
		}
		for _, op := range set {
			if !slices.Contains(allProv, op) {
				t.Fatalf("conflict op %q not in any clash provenance", op)
			}
		}
	}
}

func TestDeterministicReports(t *testing.T) {
	build := func() *netsec.Script {
		s := netsec.NewScript()
		_ = s.AddNewKey("new-enc", netsec.K("enc"), "alice", netsec.PurposeEncrypt, "s1")
		_ = s.AddBindKey("alias", netsec.K("slot"), netsec.K("enc"))
		_ = s.AddSign("sign", netsec.K("slot"))
		return s
	}
	first := netsec.Solve(build(), 0)
	second := netsec.Solve(build(), 0)
	if !slices.Equal(first.ConflictOps, second.ConflictOps) {
		t.Fatalf("non-deterministic conflict sets: %v vs %v", first.ConflictOps, second.ConflictOps)
	}
}

func TestBuilderValidation(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("dup", netsec.K("a"), "alice", netsec.PurposeEncrypt, "s1"))
	if err := s.AddSign("dup", netsec.K("a")); err == nil {
		t.Fatal("duplicate operation id must be rejected")
	}
	if err := s.CallFunc("missing", "ghost", nil); err == nil {
		t.Fatal("unknown function call must be rejected")
	}
	if err := s.CallRole("bad-depth", "ghost", 0, nil); err == nil {
		t.Fatal("non-positive role depth must be rejected")
	}
}

func TestNilScript(t *testing.T) {
	rep := netsec.Solve(nil, 0)
	if len(rep.Errors) == 0 || rep.Confusable {
		t.Fatalf("nil script must report an error and no confusion: %+v", rep)
	}
}

func TestSearchBudgetExhausted(t *testing.T) {
	s := netsec.NewScript()
	must(t, s.AddNewKey("new-enc", netsec.K("enc"), "alice", netsec.PurposeEncrypt, "s1"))
	must(t, s.AddBindKey("alias", netsec.K("slot"), netsec.K("enc")))
	must(t, s.AddSign("sign", netsec.K("slot")))

	rep := netsec.Solve(s, 1)
	if !rep.Confusable {
		t.Fatal("expected confusion to be detected regardless of budget")
	}
	if rep.Stats.Exact {
		t.Fatal("a budget of one node must be reported as inexact")
	}
	if len(rep.ConflictOps) == 0 {
		t.Fatal("inexact search must still return a best-effort conflict set")
	}
}
