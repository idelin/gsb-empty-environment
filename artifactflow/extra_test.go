package artifactflow_test

import (
	"strings"
	"testing"

	"prescreen.local/ddd-10563-v3/artifactflow"
)

// TestNormalizationAndTenantRoot covers the only permitted normalization
// (dropping explicit "." components) and the tenant-root escape ban.
func TestNormalizationAndTenantRoot(t *testing.T) {
	root := rootReceipt("root", "./tenant/./in/seed.bin", "seed", "source-a", roleVendor)
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
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	out := outputReceipt("out", "tenant/./out/final.bin", "final", "build", roleBuilder)
	problem := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{root, out},
		Steps:      []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "./tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	if r := findReport(t, problem); r.Status != artifactflow.StatusOK {
		t.Fatalf("explicit current-directory components must normalize and verify, got %s", r.Status)
	}

	escape := rootReceipt("escape", "tenant/../etc/passwd", "seed", "source-a", roleVendor)
	bad := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{escape, out},
		Steps:      []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"),
		}},
	}
	if _, err := artifactflow.Verify(bad); err == nil {
		t.Fatal("tenant-root escape must be rejected at validation")
	}

	if got, err := artifactflow.NormalizePath("a/./b"); err != nil || got != "a/b" {
		t.Fatalf("dot normalization = %q, %v", got, err)
	}
	if _, err := artifactflow.NormalizePath("a/../b"); err == nil {
		t.Fatal("parent component must be rejected")
	}
	if _, err := artifactflow.NormalizePath("/abs/path"); err == nil {
		t.Fatal("absolute path must be rejected")
	}
	if _, err := artifactflow.NormalizePath("a//b"); err == nil {
		t.Fatal("empty path component must be rejected")
	}
}

// TestLimits checks the structural caps of 16 steps and 3 candidates.
func TestLimits(t *testing.T) {
	base := func() *artifactflow.Problem {
		return &artifactflow.Problem{
			TenantRoot: "tenant",
			Receipts:   []artifactflow.Receipt{rootReceipt("r", "tenant/in/x.bin", "x", "s", roleVendor)},
			Manifest: []artifactflow.ManifestEntry{{
				Path: "tenant/in/x.bin", Digest: digestOf("x"),
			}},
		}
	}

	tooManySteps := base()
	for i := 0; i < artifactflow.MaxSteps+1; i++ {
		tooManySteps.Steps = append(tooManySteps.Steps, artifactflow.Step{ID: "s" + string(rune('a'+i))})
	}
	if _, err := artifactflow.Verify(tooManySteps); err == nil {
		t.Fatal("more than 16 steps must be rejected")
	}

	tooManyCandidates := base()
	var cands []artifactflow.Candidate
	for i := 0; i < artifactflow.MaxCandidates+1; i++ {
		cands = append(cands, artifactflow.Candidate{Name: "c" + string(rune('a'+i))})
	}
	tooManyCandidates.Steps = []artifactflow.Step{{ID: "s", Candidates: cands}}
	if _, err := artifactflow.Verify(tooManyCandidates); err == nil {
		t.Fatal("more than 3 candidates must be rejected")
	}

	atLimit := base()
	for i := 0; i < artifactflow.MaxSteps; i++ {
		atLimit.Steps = append(atLimit.Steps, artifactflow.Step{
			ID: "s" + string(rune('a'+i)),
			Candidates: []artifactflow.Candidate{
				{Name: "a"}, {Name: "b"}, {Name: "c"},
			},
		})
	}
	if _, err := artifactflow.Verify(atLimit); err != nil {
		t.Fatalf("problems at the structural limits must validate: %v", err)
	}
}

type fakeReceiptVerifier struct{ rejectID string }

func (f fakeReceiptVerifier) VerifyReceipt(r artifactflow.Receipt) error {
	if r.ID == f.rejectID {
		return errBadSignature
	}
	return nil
}

var errBadSignature = &textError{"bad signature"}

type textError struct{ text string }

func (e *textError) Error() string { return e.text }

// TestCertificateIndependentVerification covers independent replay, HMAC gate
// signatures and tamper detection without re-running the build.
func TestCertificateIndependentVerification(t *testing.T) {
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
				AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	out := outputReceipt("out", "tenant/out/final.bin", "final", "build", roleBuilder)
	problem := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{root, out},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	report := findReport(t, problem)
	if report.Status != artifactflow.StatusOK || report.Certificate == nil {
		t.Fatalf("expected ok with certificate, got %s", report.Status)
	}
	cert := report.Certificate

	if err := artifactflow.VerifyCertificate(cert, nil); err != nil {
		t.Fatalf("certificate should re-verify: %v", err)
	}

	opts := &artifactflow.VerifyOptions{Receipts: fakeReceiptVerifier{rejectID: "root"}}
	if err := artifactflow.VerifyCertificate(cert, opts); err == nil ||
		!strings.Contains(err.Error(), "bad signature") {
		t.Fatalf("expected receipt signature failure, got %v", err)
	}

	gate := &artifactflow.HMACGate{Secret: []byte("gate-secret")}
	gate.Sign(cert, "release-gate")
	gateOpts := &artifactflow.VerifyOptions{Gate: gate}
	if err := artifactflow.VerifyCertificate(cert, gateOpts); err != nil {
		t.Fatalf("HMAC-signed certificate should verify: %v", err)
	}

	tampered := *cert
	tampered.TenantRoot = "other-tenant"
	if err := artifactflow.VerifyCertificate(&tampered, gateOpts); err == nil {
		t.Fatal("tampered certificate must fail canonical digest verification")
	}

	wrongGate := &artifactflow.HMACGate{Secret: []byte("wrong-secret")}
	if err := artifactflow.VerifyCertificate(cert, &artifactflow.VerifyOptions{Gate: wrongGate}); err == nil {
		t.Fatal("certificate signed with another gate secret must fail")
	}
}

// TestLocallyLegalGloballyInfeasible builds a chain where every individual
// step has a legal-looking receipt set, but no single global correspondence
// covers the required final artifact at once.
func TestLocallyLegalGloballyInfeasible(t *testing.T) {
	root := rootReceipt("root", "tenant/in/seed.bin", "seed", "source-a", roleVendor)
	stage1 := artifactflow.Step{
		ID: "stage1",
		Candidates: []artifactflow.Candidate{
			{
				Name: "left",
				Inputs: []artifactflow.Slot{{
					Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor},
				}},
				Outputs: []artifactflow.Slot{{
					Name: "out", Path: "tenant/mid/left.bin", Digest: digestOf("mid-left"),
					AllowedSigners: []artifactflow.SignerRole{roleBuilder},
				}},
				Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
			},
			{
				Name: "right",
				Inputs: []artifactflow.Slot{{
					Name: "in", Path: "tenant/in/seed.bin", Digest: digestOf("seed"),
					AllowedSigners: []artifactflow.SignerRole{roleVendor},
				}},
				Outputs: []artifactflow.Slot{{
					Name: "out", Path: "tenant/mid/right.bin", Digest: digestOf("mid-right"),
					AllowedSigners: []artifactflow.SignerRole{roleBuilder},
				}},
				Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
			},
		},
	}
	stage2 := artifactflow.Step{
		ID: "stage2",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/mid/left.bin", Digest: digestOf("mid-left"),
				Consumed: true, AllowedSigners: []artifactflow.SignerRole{roleBuilder},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/final.bin", Digest: digestOf("final"),
				AllowedSigners: []artifactflow.SignerRole{rolePackager},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	midLeft := outputReceipt("mid-left", "tenant/mid/left.bin", "mid-left", "stage1", roleBuilder)
	midRight := outputReceipt("mid-right", "tenant/mid/right.bin", "mid-right", "stage1", roleBuilder)
	final := outputReceipt("final", "tenant/out/final.bin", "final", "stage2", rolePackager)

	// Stage1 looks legal in isolation (right intermediate exists) but the
	// left-only final chain has no matching intermediate receipt globally.
	problem := &artifactflow.Problem{
		TenantRoot: "tenant",
		Receipts:   []artifactflow.Receipt{root, midRight, final},
		Steps:      []artifactflow.Step{stage1, stage2},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/final.bin", Digest: digestOf("final"), Sensitive: true,
		}},
	}
	if r := findReport(t, problem); r.Status != artifactflow.StatusInfeasible {
		t.Fatalf("expected global infeasibility, got %s", r.Status)
	}

	complete := *problem
	complete.Receipts = []artifactflow.Receipt{root, midLeft, midRight, final}
	if r := findReport(t, &complete); r.Status != artifactflow.StatusOK {
		t.Fatalf("complete chain should verify, got %s", r.Status)
	}
}

// TestSameNameDifferentContentReplacement ensures a clean artifact's name
// cannot be reused by different content: slot digests pin content even when a
// rename rule keeps the same logical path.
func TestSameNameDifferentContentReplacement(t *testing.T) {
	clean := rootReceipt("clean", "tenant/in/lib.bin", "clean-content", "source-a", roleVendor)
	trojan := rootReceipt("trojan", "tenant/in/lib.bin", "trojan-content", "source-b", roleVendor)
	step := artifactflow.Step{
		ID: "pack",
		Candidates: []artifactflow.Candidate{{
			Name: "c",
			Inputs: []artifactflow.Slot{{
				Name: "in", Path: "tenant/in/lib.bin", Digest: digestOf("clean-content"),
				AllowedSigners: []artifactflow.SignerRole{roleVendor},
			}},
			Outputs: []artifactflow.Slot{{
				Name: "out", Path: "tenant/out/lib.bin", Digest: digestOf("packed-clean"),
				AllowedSigners: []artifactflow.SignerRole{rolePackager},
			}},
			Rules: []artifactflow.Rule{{Input: "in", Output: "out"}},
		}},
	}
	goodOut := outputReceipt("good-out", "tenant/out/lib.bin", "packed-clean", "pack", rolePackager)
	badOut := outputReceipt("bad-out", "tenant/out/lib.bin", "packed-trojan", "pack", rolePackager)

	good := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{clean, trojan, goodOut},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/lib.bin", Digest: digestOf("packed-clean"), Sensitive: true,
		}},
	}
	if r := findReport(t, good); r.Status != artifactflow.StatusOK {
		t.Fatalf("clean flow must verify despite the trojan receipt, got %s", r.Status)
	}
	bad := &artifactflow.Problem{
		TenantRoot: "tenant", Receipts: []artifactflow.Receipt{clean, trojan, badOut},
		Steps: []artifactflow.Step{step},
		Manifest: []artifactflow.ManifestEntry{{
			Path: "tenant/out/lib.bin", Digest: digestOf("packed-trojan"), Sensitive: true,
		}},
	}
	if r := findReport(t, bad); r.Status != artifactflow.StatusInfeasible {
		t.Fatalf("same-name different-content output must be infeasible, got %s", r.Status)
	}
}
