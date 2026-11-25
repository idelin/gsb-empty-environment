package artifactflow_test

import (
	"testing"

	"prescreen.local/ddd-10563-v3/artifactflow"
)

// TestSignerRoleEnforcement proves a slot only accepts receipts signed by an
// allowed role, and wildcard slots accept any role.
func TestSignerRoleEnforcement(t *testing.T) {
	root := rootReceipt("root", "tenant/in/seed.bin", "seed", "source-a", roleVendor)
	step := artifactflow.Step{
		ID: "build",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
				AllowedSigners: []artifactflow.SignerRole{roleVendor},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/final.bin", Digest: digestOf("final"),
				AllowedSigners: []artifactflow.SignerRole{rolePackager},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	// Output signed by the wrong role (builder instead of packager).
	wrongRole := outputReceipt("wrong", "tenant/out/final.bin", "final", "build", roleBuilder)
	bad := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, wrongRole},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	if r := findReport(t, bad); r.Status != artifactflow.StatusInfeasible {
		t.Fatalf("wrong signer role must be infeasible, got %s", r.Status)
	}

	rightRole := outputReceipt("right", "tenant/out/final.bin", "final", "build", rolePackager)
	good := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, rightRole},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	if r := findReport(t, good); r.Status != artifactflow.StatusOK {
		t.Fatalf("correct signer role should verify, got %s", r.Status)
	}

	// Empty AllowedSigners means wildcard: any role is accepted.
	wildStep := step
	wildStep.Candidates[0].Outputs[0].AllowedSigners = nil
	wild := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, wrongRole},
		Steps: []artifactflow.Step{wildStep},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	if r := findReport(t, wild); r.Status != artifactflow.StatusOK {
		t.Fatalf("wildcard signer slot should accept any role, got %s", r.Status)
	}
}

// TestOutOfOrderReceipts shuffles receipts so the output receipt appears
// before every input/root receipt. The gate is set-based and must be
// insensitive to receipt order.
func TestOutOfOrderReceipts(t *testing.T) {
	root := rootReceipt("root", "tenant/in/seed.bin", "seed", "source-a", roleVendor)
	mid := outputReceipt("mid", "tenant/mid/x.bin", "mid", "stage1", roleBuilder)
	final := outputReceipt("final", "tenant/out/final.bin", "final", "stage2", rolePackager)

	stage1 := artifactflow.Step{
		ID: "stage1",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
				AllowedSigners: []artifactflow.SignerRole{roleVendor},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/mid/x.bin", Digest: digestOf("mid"),
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	stage2 := artifactflow.Step{
		ID: "stage2",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/mid/x.bin", Digest: digestOf("mid"),
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/final.bin", Digest: digestOf("final"),
				AllowedSigners: []artifactflow.SignerRole{rolePackager},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	ordered := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{root, mid, final},
		Steps:      []artifactflow.Step{stage1, stage2},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	shuffled := *ordered
	shuffled.Receipts = []artifactflow.Receipt{final, root, mid}

	r1 := findReport(t, ordered)
	r2 := findReport(t, &shuffled)
	if r1.Status != artifactflow.StatusOK || r2.Status != artifactflow.StatusOK {
		t.Fatalf("both orderings must verify, got %s and %s", r1.Status, r2.Status)
	}
	if r1.Certificate.CanonicalDigest != r2.Certificate.CanonicalDigest {
		t.Fatal("verdict must be independent of receipt ordering")
	}
}

// TestRenameRuleTarget verifies an explicit rule Target renames an output and
// the capture still constrains input and output to the same component.
func TestRenameRuleTarget(t *testing.T) {
	root := rootReceipt("root", "tenant/in/report.txt", "doc", "source-a", roleVendor)
	step := artifactflow.Step{
		ID: "archive",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/in/{name}.txt", Digest: digestOf("doc"),
				AllowedSigners: []artifactflow.SignerRole{roleVendor},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/ignored.bin", Digest: digestOf("archived"),
				AllowedSigners: []artifactflow.SignerRole{rolePackager},
			}},
			Rules: []artifactflow.Rule{{
				Input: "in", Output: "out", Target: "tenant/out/{name}.zip",
			}},
		}},
	}
	good := outputReceipt("good", "tenant/out/report.zip", "archived", "archive", rolePackager)
	problem := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, good},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/report.zip", Digest: digestOf("archived"), Sensitive: true,
		}},
	}
	if r := findReport(t, problem); r.Status != artifactflow.StatusOK {
		t.Fatalf("explicit rename target with shared capture should verify, got %s", r.Status)
	}

	bad := outputReceipt("bad", "tenant/out/other.zip", "archived", "archive", rolePackager)
	problemBad := *problem
	problemBad.Receipts = []artifactflow.Receipt{root, bad}
	problemBad.Manifest = []artifactflow.ManifestEntry{{
		Path: "tenant/out/other.zip", Digest: digestOf("archived"), Sensitive: true,
	}}
	if r := findReport(t, &problemBad); r.Status != artifactflow.StatusInfeasible {
		t.Fatalf("capture mismatch across rename target must be infeasible, got %s", r.Status)
	}
}
