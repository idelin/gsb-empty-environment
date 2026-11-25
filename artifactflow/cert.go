package artifactflow

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

// Certificate is an independently verifiable success verdict. It bundles
// every receipt, step declaration, manifest entry and the exact assignment
// the gate accepted, so a third party can re-verify the whole artifact flow
// without re-running the build.
type Certificate struct {
	// Version is the certificate format version.
	Version int
	// TenantRoot is the normalized tenant root of the flow.
	TenantRoot Path
	// Manifest is the full final manifest, in declared order.
	Manifest []ManifestEntry
	// Receipts are all receipts referenced by the assignment.
	Receipts []Receipt
	// Steps are the full step declarations referenced by the assignment.
	Steps []Step
	// Assignment is the accepted global correspondence.
	Assignment Assignment
	// CanonicalDigest binds the certificate contents:
	// sha256 over the deterministic canonical JSON encoding.
	CanonicalDigest string
	// Signature is an optional gate signature over CanonicalDigest.
	Signature []byte
	// Signer identifies the gate signer when Signature is present.
	Signer string
}

// ReceiptVerifier re-checks an already signed build receipt. A deployment
// that wants fully independent verification supplies one; the gate itself
// treats receipts as pre-verified.
type ReceiptVerifier interface {
	VerifyReceipt(r Receipt) error
}

// GateVerifier verifies the gate signature over the certificate digest.
type GateVerifier interface {
	VerifyGate(signer string, digest string, signature []byte) error
}

// VerifyOptions controls independent certificate verification.
type VerifyOptions struct {
	Receipts ReceiptVerifier
	Gate     GateVerifier
}

// Verify runs the release gate and returns a structured verdict. A malformed
// problem or an exhausted search budget is reported as an error rather than a
// verdict.
func Verify(p *Problem) (*Report, error) {
	indexed, err := prepare(p)
	if err != nil {
		return nil, err
	}
	en, err := enumerate(indexed)
	if err != nil {
		return nil, err
	}
	status, cert, amb, reason, err := indexed.run(en)
	if err != nil {
		return nil, err
	}
	return &Report{Status: status, Certificate: cert, Ambiguity: amb, Reason: reason}, nil
}

// buildCertificate assembles and canonically digests a success certificate.
func buildCertificate(s *solved, en *enumeration, assignment *Assignment) *Certificate {
	used := assignmentReceipts(en, assignment)
	receipts := make([]Receipt, 0, len(used))
	for _, idx := range used {
		receipts = append(receipts, s.receipts[idx].receipt)
	}
	sort.Slice(receipts, func(i, j int) bool { return receipts[i].ID < receipts[j].ID })
	usedSteps := make(map[string]bool)
	for _, rs := range assignment.Steps {
		usedSteps[rs.StepID] = true
	}
	steps := make([]Step, 0, len(usedSteps))
	for _, st := range s.steps {
		if usedSteps[st.step.ID] {
			steps = append(steps, st.step)
		}
	}
	sort.Slice(steps, func(i, j int) bool { return steps[i].ID < steps[j].ID })
	manifest := make([]ManifestEntry, len(s.manifest))
	for i, mi := range s.manifest {
		manifest[i] = mi.entry
	}
	cert := &Certificate{
		Version:    1,
		TenantRoot: s.tenantRoot,
		Manifest:   manifest,
		Receipts:   receipts,
		Steps:      steps,
		Assignment: *assignment,
	}
	cert.CanonicalDigest = cert.digest()
	return cert
}

func assignmentReceipts(en *enumeration, assignment *Assignment) []int {
	used := map[int]bool{}
	for _, rs := range assignment.Steps {
		for _, b := range rs.InputReceipts {
			if idx, ok := en.indexed.byID[b.ReceiptID]; ok {
				used[idx] = true
			}
		}
		for _, b := range rs.OutputReceipts {
			if idx, ok := en.indexed.byID[b.ReceiptID]; ok {
				used[idx] = true
			}
		}
	}
	for _, id := range assignment.Roots {
		for idx, ir := range en.indexed.receipts {
			if ir.receipt.ID == id {
				used[idx] = true
			}
		}
	}
	out := make([]int, 0, len(used))
	for idx := range used {
		out = append(out, idx)
	}
	return out
}

// digest computes sha256 over the deterministic canonical JSON encoding of
// the certificate contents excluding the signature fields.
func (c *Certificate) digest() string {
	copy := *c
	copy.CanonicalDigest = ""
	copy.Signature = nil
	copy.Signer = ""
	raw, err := json.Marshal(canonicalView(&copy))
	if err != nil {
		panic(&budgetPanic{err: err})
	}
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// canonicalView returns a view whose map-free, ordered structure serializes
// deterministically on every Go implementation.
func canonicalView(c *Certificate) any {
	return struct {
		Version    int             `json:"version"`
		TenantRoot string          `json:"tenant_root"`
		Manifest   []ManifestEntry `json:"manifest"`
		Receipts   []Receipt       `json:"receipts"`
		Steps      []Step          `json:"steps"`
		Assignment Assignment      `json:"assignment"`
	}{
		Version:    c.Version,
		TenantRoot: string(c.TenantRoot),
		Manifest:   c.Manifest,
		Receipts:   c.Receipts,
		Steps:      c.Steps,
		Assignment: c.Assignment,
	}
}

// VerifyCertificate independently re-verifies a certificate without re-running
// the build. It re-checks the canonical digest, every referenced receipt
// signature (when a verifier is supplied) and the gate signature (when one is
// present and a verifier is supplied), then replays the whole flow search on
// the embedded evidence and requires an OK verdict with the same assignment.
func VerifyCertificate(c *Certificate, opts *VerifyOptions) error {
	if c == nil {
		return &ValidationError{Field: "certificate", Reason: "certificate is nil"}
	}
	if c.Version != 1 {
		return &ValidationError{Field: "certificate", Reason: "unsupported certificate version"}
	}
	if c.digest() != c.CanonicalDigest {
		return &ValidationError{Field: "certificate", Reason: "canonical digest mismatch; content was tampered"}
	}
	if opts != nil {
		if opts.Receipts != nil {
			for _, r := range c.Receipts {
				if err := opts.Receipts.VerifyReceipt(r); err != nil {
					return &ValidationError{Field: "receipt", Reason: "receipt " + r.ID + " failed verification: " + err.Error()}
				}
			}
		}
		if opts.Gate != nil && c.Signature != nil {
			if err := opts.Gate.VerifyGate(c.Signer, c.CanonicalDigest, c.Signature); err != nil {
				return &ValidationError{Field: "certificate", Reason: "gate signature failed verification: " + err.Error()}
			}
		}
	}
	problem := &Problem{
		TenantRoot: c.TenantRoot,
		Receipts:   c.Receipts,
		Steps:      c.Steps,
		Manifest:   c.Manifest,
	}
	report, err := Verify(problem)
	if err != nil {
		return err
	}
	if report.Status != StatusOK {
		return &ValidationError{Field: "certificate", Reason: "embedded evidence does not re-verify as an unambiguous flow"}
	}
	if !assignmentsEqual(report.Certificate.Assignment, c.Assignment) {
		return &ValidationError{Field: "certificate", Reason: "re-verified assignment differs from the certified assignment"}
	}
	return nil
}

func assignmentsEqual(a, b Assignment) bool {
	if len(a.Steps) != len(b.Steps) || len(a.Roots) != len(b.Roots) {
		return false
	}
	for i := range a.Roots {
		if a.Roots[i] != b.Roots[i] {
			return false
		}
	}
	for i := range a.Steps {
		x, y := a.Steps[i], b.Steps[i]
		if x.StepID != y.StepID || x.CandidateName != y.CandidateName ||
			len(x.InputReceipts) != len(y.InputReceipts) ||
			len(x.OutputReceipts) != len(y.OutputReceipts) {
			return false
		}
		for j := range x.InputReceipts {
			if x.InputReceipts[j] != y.InputReceipts[j] {
				return false
			}
		}
		for j := range x.OutputReceipts {
			if x.OutputReceipts[j] != y.OutputReceipts[j] {
				return false
			}
		}
	}
	return true
}

// HMACGate is a GateVerifier and signing helper using a shared secret. It is
// a batteries-included option for deployments that do not bring asymmetric
// key infrastructure.
type HMACGate struct {
	Secret []byte
}

func (h *HMACGate) mac(digest string) []byte {
	mac := hmac.New(sha256.New, h.Secret)
	mac.Write([]byte(digest))
	return mac.Sum(nil)
}

// Sign attaches an HMAC gate signature to a certificate.
func (h *HMACGate) Sign(c *Certificate, signer string) {
	c.Signer = signer
	c.Signature = h.mac(c.CanonicalDigest)
}

// VerifyGate implements GateVerifier.
func (h *HMACGate) VerifyGate(signer string, digest string, signature []byte) error {
	expected := h.mac(digest)
	if !hmac.Equal(expected, signature) {
		return &ValidationError{Field: "certificate", Reason: "HMAC mismatch for signer " + signer}
	}
	return nil
}
