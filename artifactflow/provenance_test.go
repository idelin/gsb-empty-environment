package artifactflow_test

import "testing"
import "prescreen.local/ddd-10563-v3/artifactflow"

// TestMultiRootProvenance verifies that an output derived from two distinct
// root inputs retains both signature sources in its root-source set, and that
// the attribution stays unique across all feasible correspondences.
func TestMultiRootProvenance(t *testing.T) {
	rootA := rootReceipt("ra", "tenant/in/a.bin", "a-content", "source-a", roleVendor)
	rootB := rootReceipt("rb", "tenant/in/b.bin", "b-content", "source-b", roleVendor)
	step := artifactflow.Step{
		ID: "link",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{
				{Name: "a", Path: "tenant/in/a.bin", Digest: digestOf("a-content"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor}},
				{Name: "b", Path: "tenant/in/b.bin", Digest: digestOf("b-content"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor}},
			},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/combined.bin", Digest: digestOf("combined"),
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Rules: []artifactflow.Rule{{Input: "a", Output: "out"}},
		}},
	}
	out := outputReceipt("out", "tenant/out/combined.bin", "combined", "link", roleBuilder)
	problem := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{rootA, rootB, out},
		Steps:      []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/combined.bin", Digest: digestOf("combined"), Sensitive: true,
		}},
	}
	report := findReport(t, problem)
	if report.Status != artifactflow.StatusOK {
		t.Fatalf("deterministic multi-root derivation should verify, got %s", report.Status)
	}
	cert := report.Certificate
	// The assignment roots include both sources.
	rootSet := map[string]bool{}
	for _, id := range cert.Assignment.Roots {
		rootSet[id] = true
	}
	if !rootSet["ra"] || !rootSet["rb"] {
		t.Fatalf("both root receipts must be retained in provenance, got %v", cert.Assignment.Roots)
	}
	if len(cert.Assignment.Steps) != 1 {
		t.Fatalf("exactly one step should be realized, got %d", len(cert.Assignment.Steps))
	}

	// Re-verification standalone keeps the same attribution.
	if err := artifactflow.VerifyCertificate(cert, nil); err != nil {
		t.Fatalf("certificate must re-verify: %v", err)
	}
}

// TestNonSensitiveIgnoredForAmbiguity confirms ambiguity is only judged on
// sensitive entries: two competing attributions on a non-sensitive entry still
// produce an OK verdict as long as every sensitive entry is unambiguous.
func TestNonSensitiveIgnoredForAmbiguity(t *testing.T) {
	rA := rootReceipt("ra", "tenant/in/a.bin", "dup", "source-a", roleVendor)
	rB := rootReceipt("rb", "tenant/in/b.bin", "dup", "source-b", roleVendor)
	step := artifactflow.Step{
		ID: "build",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{
				{Name: "a", Path: "tenant/in/a.bin", Digest: digestOf("dup"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor}},
				{Name: "b", Path: "tenant/in/b.bin", Digest: digestOf("dup"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor}},
			},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/final.bin", Digest: digestOf("final"),
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Rules: []artifactflow.Rule{{Input: "a", Output: "out"}},
		}},
	}
	out := outputReceipt("out", "tenant/out/final.bin", "final", "build", roleBuilder)
	problem := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{rA, rB, out},
		Steps:      []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: false,
		}},
	}
	report := findReport(t, problem)
	if report.Status != artifactflow.StatusOK {
		t.Fatalf("non-sensitive ambiguity must not block release, got %s", report.Status)
	}
}
