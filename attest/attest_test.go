package attest

import (
	"errors"
	"reflect"
	"testing"
)

func sha(v string) Digest { return Digest{Algorithm: "sha256", Value: v} }

const (
	testCtx     = "release/2026-11-11"
	testBuilder = "builder://github-actions/go-1.24"
	testCommit  = "c0ffee123"
)

var (
	srcMain   = Resource{Name: "src/main.go", Digest: sha("a1")}
	srcGoMod  = Resource{Name: "src/go.mod", Digest: sha("a2")}
	depsLock  = Resource{Name: "deps.lock", Digest: sha("a3")}
	compileTo = Resource{Name: "bin/server", Digest: sha("b1")}
	packageTo = Resource{Name: "server.tar.gz", Digest: sha("c1")}
)

// twoStepBuild returns a compile -> package chain for one build.
func twoStepBuild() []Statement {
	return []Statement{
		{
			ID:        "stmt-compile",
			Builder:   testBuilder,
			Commit:    testCommit,
			Context:   testCtx,
			Materials: []Resource{srcMain, srcGoMod, depsLock},
			Outputs:   []Resource{compileTo},
		},
		{
			ID:        "stmt-package",
			Builder:   testBuilder,
			Commit:    testCommit,
			Context:   testCtx,
			Materials: []Resource{compileTo},
			Outputs:   []Resource{packageTo},
		},
	}
}

func testPolicy() Policy {
	return Policy{
		Artifact:  packageTo.Digest,
		Commit:    testCommit,
		Builder:   testBuilder,
		Context:   testCtx,
		Materials: []Resource{srcMain, srcGoMod, depsLock},
	}
}

func TestVerifyMultiStepChain(t *testing.T) {
	res, err := Verify(twoStepBuild(), testPolicy())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	for _, req := range []string{ReqArtifactFromCommit, ReqPinnedBuilder, ReqCompleteMaterials} {
		if _, ok := res.Proofs[req]; !ok {
			t.Errorf("missing proof path for %q", req)
		}
	}
	// Chain order: sources first, artifact-producing statement last.
	want := []string{"stmt-compile", "stmt-package"}
	if got := res.Proofs[ReqArtifactFromCommit].Statements; !reflect.DeepEqual(got, want) {
		t.Errorf("artifact-from-commit path = %v, want %v", got, want)
	}
	if got := res.Proofs[ReqPinnedBuilder].Statements; !reflect.DeepEqual(got, want) {
		t.Errorf("pinned-builder path = %v, want %v", got, want)
	}
	if got := res.Proofs[ReqCompleteMaterials].Statements; !reflect.DeepEqual(got, []string{"stmt-compile"}) {
		t.Errorf("complete-materials path = %v, want [stmt-compile]", got)
	}
}

func TestVerifyMixedStatementsFromTwoBuilds(t *testing.T) {
	// A second, unrelated build of a different commit.
	otherBuild := []Statement{
		{
			ID:      "stmt-other-compile",
			Builder: testBuilder,
			Commit:  "deadbeef999",
			Context: testCtx,
			Materials: []Resource{
				{Name: "src/main.go", Digest: sha("x1")},
			},
			Outputs: []Resource{{Name: "bin/server", Digest: sha("y1")}},
		},
		{
			ID:        "stmt-other-package",
			Builder:   testBuilder,
			Commit:    "deadbeef999",
			Context:   testCtx,
			Materials: []Resource{{Name: "bin/server", Digest: sha("y1")}},
			Outputs:   []Resource{{Name: "server.tar.gz", Digest: sha("z1")}},
		},
	}
	mixed := append(twoStepBuild(), otherBuild...)

	// Releasing the other build's artifact against our commit must fail.
	p := testPolicy()
	p.Artifact = sha("z1")
	if _, err := Verify(mixed, p); !errors.Is(err, ErrViolation) {
		t.Fatalf("mixed builds: err = %v, want ErrViolation", err)
	}

	// Mixing materials across builds does not link: the other build's
	// intermediate digest never appears in our chain, so its statements
	// cannot satisfy our material requirements either.
	p = testPolicy()
	p.Materials = append(p.Materials, Resource{Name: "bin/server", Digest: sha("y1")})
	if _, err := Verify(mixed, p); !errors.Is(err, ErrMissingProof) {
		t.Fatalf("cross-build material: err = %v, want ErrMissingProof", err)
	}
}

func TestVerifySameNameDifferentDigest(t *testing.T) {
	stmts := twoStepBuild()
	// Statement carries deps.lock with a different digest than required.
	stmts[0].Materials[2] = Resource{Name: "deps.lock", Digest: sha("ffff")}

	if _, err := Verify(stmts, testPolicy()); !errors.Is(err, ErrMissingProof) {
		t.Fatalf("same name different digest: err = %v, want ErrMissingProof", err)
	}
}

func TestVerifyRejectsWrongBuilder(t *testing.T) {
	stmts := twoStepBuild()
	stmts[1].Builder = "builder://unpinned/local-laptop"

	if _, err := Verify(stmts, testPolicy()); !errors.Is(err, ErrViolation) {
		t.Fatalf("wrong builder: err = %v, want ErrViolation", err)
	}
}

func TestVerifyReplayedOldReleaseContext(t *testing.T) {
	stmts := twoStepBuild()
	// Attacker replays statements from a previous release.
	for i := range stmts {
		stmts[i].Context = "release/2026-10-01"
	}

	if _, err := Verify(stmts, testPolicy()); !errors.Is(err, ErrStaleContext) {
		t.Fatalf("replayed context: err = %v, want ErrStaleContext", err)
	}
}

func TestVerifyDetectsCycle(t *testing.T) {
	stmts := []Statement{
		{
			ID:        "stmt-a",
			Builder:   testBuilder,
			Commit:    testCommit,
			Context:   testCtx,
			Materials: []Resource{{Name: "b.out", Digest: sha("db")}},
			Outputs:   []Resource{{Name: "a.out", Digest: sha("da")}},
		},
		{
			ID:        "stmt-b",
			Builder:   testBuilder,
			Commit:    testCommit,
			Context:   testCtx,
			Materials: []Resource{{Name: "a.out", Digest: sha("da")}},
			Outputs:   []Resource{{Name: "b.out", Digest: sha("db")}},
		},
	}
	p := testPolicy()
	p.Artifact = sha("da")
	p.Materials = nil

	if _, err := Verify(stmts, p); !errors.Is(err, ErrCycle) {
		t.Fatalf("cycle: err = %v, want ErrCycle", err)
	}
}

func TestVerifyMissingProofNotGuessed(t *testing.T) {
	// No statement produces the released artifact.
	p := testPolicy()
	p.Artifact = sha("nonexistent")
	if _, err := Verify(twoStepBuild(), p); !errors.Is(err, ErrMissingProof) {
		t.Fatalf("unknown artifact: err = %v, want ErrMissingProof", err)
	}

	// Artifact exists but a required material is nowhere declared.
	p = testPolicy()
	p.Materials = append(p.Materials, Resource{Name: "toolchain", Digest: sha("t1")})
	if _, err := Verify(twoStepBuild(), p); !errors.Is(err, ErrMissingProof) {
		t.Fatalf("undeclared material: err = %v, want ErrMissingProof", err)
	}
}
