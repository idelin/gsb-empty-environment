package artifactflow_test

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"

	"prescreen.local/ddd-10563-v3/artifactflow"
)

const (
	roleBuilder  artifactflow.SignerRole = "builder"
	rolePackager artifactflow.SignerRole = "packager"
	roleVendor   artifactflow.SignerRole = "vendor"
)

func digestOf(content string) artifactflow.Digest {
	sum := sha256.Sum256([]byte(content))
	return artifactflow.Digest("sha256:" + hex.EncodeToString(sum[:]))
}

func rootReceipt(id string, path artifactflow.Path, content, source string, role artifactflow.SignerRole) artifactflow.Receipt {
	return artifactflow.Receipt{
		ID:         id,
		Path:       path,
		Digest:     digestOf(content),
		Signer:     role,
		RootSource: artifactflow.SourceID(source),
	}
}

func outputReceipt(id string, path artifactflow.Path, content, stepID string, role artifactflow.SignerRole) artifactflow.Receipt {
	return artifactflow.Receipt{
		ID:     id,
		Path:   path,
		Digest: digestOf(content),
		Signer: role,
		StepID: stepID,
	}
}

// findReport runs the gate and fails the test on error.
func findReport(t *testing.T, p *artifactflow.Problem) *artifactflow.Report {
	t.Helper()
	report, err := artifactflow.Verify(p)
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	return report
}

// TestSameDigestTwoSources is the headline ambiguity case: the same content
// digest can be rooted at two different sources, so the sensitive final
// artifact has two distinct root-source attributions and the gate must refuse
// ambiguous authorization.
func TestSameDigestTwoSources(t *testing.T) {
	// Two root receipts, identical digest, different source identity and path.
	rA := rootReceipt("ra", "tenant/in/a.bin", "shared-bytes", "source-a", roleVendor)
	rB := rootReceipt("rb", "tenant/in/b.bin", "shared-bytes", "source-b", roleVendor)
	// A build step accepts either path via a capture and emits a fixed output.
	step := artifactflow.Step{
		ID: "build",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/in/{name}.bin", Digest: digestOf("shared-bytes"),
				AllowedSigners: []artifactflow.SignerRole{roleVendor},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/final.tar", Digest: digestOf("final-bytes"),
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	out := outputReceipt("out", "tenant/out/final.tar", "final-bytes", "build", roleBuilder)
	problem := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{rA, rB, out},
		Steps:      []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.tar", Digest: digestOf("final-bytes"), Sensitive: true,
		}},
	}

	report := findReport(t, problem)
	if report.Status != artifactflow.StatusAmbiguous {
		t.Fatalf("expected ambiguous, got %s (reason=%v)", report.Status, report.Reason)
	}
	if report.Ambiguity == nil {
		t.Fatal("ambiguous verdict must carry witness details")
	}
	if report.Ambiguity.FirstSources == nil || report.Ambiguity.SecondSources == nil {
		t.Fatal("ambiguity must carry both source sets")
	}
	if sourceList(report.Ambiguity.FirstSources) == sourceList(report.Ambiguity.SecondSources) {
		t.Fatal("the two attributions must differ")
	}
	if len(report.Ambiguity.First.Steps) == 0 || len(report.Ambiguity.Second.Steps) == 0 {
		t.Fatal("both ambiguity witnesses must contain full assignments")
	}

	// Control case: only source-a exists, so attribution is unique.
	unique := *problem
	unique.Receipts = []artifactflow.Receipt{rA, out}
	okReport := findReport(t, &unique)
	if okReport.Status != artifactflow.StatusOK {
		t.Fatalf("expected ok with a single source, got %s", okReport.Status)
	}
	if okReport.Certificate == nil {
		t.Fatal("ok verdict must carry a certificate")
	}
}

func sourceList(s []artifactflow.SourceID) string {
	parts := make([]string, len(s))
	for i, v := range s {
		parts[i] = string(v)
	}
	return strings.Join(parts, ",")
}

// TestConsumedInputUsedByTwoSteps proves a single-use input cannot be spent on
// two edges of the same build instance, while the equivalent shareable input
// forks freely.
func TestConsumedInputUsedByTwoSteps(t *testing.T) {
	root := rootReceipt("root", "tenant/in/seed.bin", "seed", "source-a", roleVendor)
	mkStep := func(id string, consumed bool, outContent string) (artifactflow.Step, artifactflow.Receipt) {
		st := artifactflow.Step{
			ID: id,
			Candidates: []artifactflow.Candidate{{
				Name: "c",
				Inputs: []artifactflow.Slot{{
					Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
					Consumed: consumed, AllowedSigners: []artifactflow.SignerRole{roleVendor},
				}},
				Outputs: []artifactflow.Slot{{
					Name: "out", Path: artifactflow.Path("tenant/out/" + id + ".bin"),
					Digest: digestOf(outContent), AllowedSigners: []artifactflow.SignerRole{roleBuilder},
				}},
				Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
			}},
		}
		return st, outputReceipt(id+"-out", artifactflow.Path("tenant/out/"+id+".bin"), outContent, id, roleBuilder)
	}
	st1, out1 := mkStep("build1", true, "out1")
	st2, out2 := mkStep("build2", true, "out2")
	problem := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{root, out1, out2},
		Steps:      []artifactflow.Step{st1, st2},
		Manifest: []artifactflow.ManifestEntry{
			{Path: "tenant/out/build1.bin", Digest: digestOf("out1"), Sensitive: true},
			{Path: "tenant/out/build2.bin", Digest: digestOf("out2"), Sensitive: true},
		},
	}
	report := findReport(t, problem)
	if report.Status != artifactflow.StatusInfeasible {
		t.Fatalf("consumed input shared by two steps must be infeasible, got %s", report.Status)
	}

	// Same topology, inputs shareable: the single root receipt forks.
	st3, out3 := mkStep("build1", false, "out1")
	st4, out4 := mkStep("build2", false, "out2")
	shareable := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{root, out3, out4},
		Steps:      []artifactflow.Step{st3, st4},
		Manifest: []artifactflow.ManifestEntry{
			{Path: "tenant/out/build1.bin", Digest: digestOf("out1"), Sensitive: true},
			{Path: "tenant/out/build2.bin", Digest: digestOf("out2"), Sensitive: true},
		},
	}
	okReport := findReport(t, shareable)
	if okReport.Status != artifactflow.StatusOK {
		t.Fatalf("shareable input should fork, got %s", okReport.Status)
	}
}

// TestCapturePathCrossing checks that a capture component binds consistently
// across an input/output rename rule, so crossing names (a->b on the input but
// b->a on the output) cannot satisfy either rule.
func TestCapturePathCrossing(t *testing.T) {
	root := rootReceipt("root", "tenant/in/alpha.bin", "alpha-content", "source-a", roleVendor)
	step := artifactflow.Step{
		ID: "rename",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/in/{name}.bin", Digest: digestOf("alpha-content"),
				AllowedSigners: []artifactflow.SignerRole{roleVendor},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/{name}.bin", Digest: digestOf("packed"),
				AllowedSigners: []artifactflow.SignerRole{rolePackager},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	// Correct output preserves the captured name "alpha".
	good := outputReceipt("good", "tenant/out/alpha.bin", "packed", "rename", rolePackager)
	// Crossed output uses a different name, so the capture constraint rejects it.
	bad := outputReceipt("bad", "tenant/out/beta.bin", "packed", "rename", rolePackager)

	goodProblem := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, good},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/alpha.bin", Digest: digestOf("packed"), Sensitive: true,
		}},
	}
	if r := findReport(t, goodProblem); r.Status != artifactflow.StatusOK {
		t.Fatalf("consistent capture should pass, got %s", r.Status)
	}
	badProblem := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, bad},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/beta.bin", Digest: digestOf("packed"), Sensitive: true,
		}},
	}
	if r := findReport(t, badProblem); r.Status != artifactflow.StatusInfeasible {
		t.Fatalf("crossed capture names must be infeasible, got %s", r.Status)
	}
}

// TestAlternativePartialOutputs ensures outputs from two alternative
// implementations of one step cannot be stitched together into one build.
func TestAlternativePartialOutputs(t *testing.T) {
	root := rootReceipt("root", "tenant/in/seed.bin", "seed", "source-a", roleVendor)
	step := artifactflow.Step{
		ID: "build",
		Candidates: []artifactflow.Candidate{
			{
				Name: "fast",
				Inputs: []artifactflow.Slot{{
					Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor},
				}},
				Outputs: []artifactflow.Slot{{
					Name: "out", Path: "tenant/out/fast.bin", Digest: digestOf("fast-bytes"),
					AllowedSigners: []artifactflow.SignerRole{roleBuilder},
				}},
				Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
			},
			{
				Name: "safe",
				Inputs: []artifactflow.Slot{{
					Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor},
				}},
				Outputs: []artifactflow.Slot{{
					Name: "out", Path: "tenant/out/safe.bin", Digest: digestOf("safe-bytes"),
					AllowedSigners: []artifactflow.SignerRole{roleBuilder},
				}},
				Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
			},
		},
	}
	fastOut := outputReceipt("fast-out", "tenant/out/fast.bin", "fast-bytes", "build", roleBuilder)
	safeOut := outputReceipt("safe-out", "tenant/out/safe.bin", "safe-bytes", "build", roleBuilder)

	// Each single-alternative manifest passes on its own receipt.
	fastOnly := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, fastOut},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/fast.bin", Digest: digestOf("fast-bytes"), Sensitive: true,
		}},
	}
	if r := findReport(t, fastOnly); r.Status != artifactflow.StatusOK {
		t.Fatalf("fast alternative alone should pass, got %s", r.Status)
	}

	// Requiring both outputs at once mixes two alternatives: impossible in a
	// single build instance because only one candidate may be realized.
	mixed := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, fastOut, safeOut},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{
			{Path: "tenant/out/fast.bin", Digest: digestOf("fast-bytes"), Sensitive: true},
			{Path: "tenant/out/safe.bin", Digest: digestOf("safe-bytes"), Sensitive: true},
		},
	}
	if r := findReport(t, mixed); r.Status != artifactflow.StatusInfeasible {
		t.Fatalf("mixing partial outputs of two alternatives must be infeasible, got %s", r.Status)
	}
}
