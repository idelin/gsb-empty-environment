package admission

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"strings"
)

// CertificateVersion is the certificate format version.
const CertificateVersion = "admission-cert-v1"

// Certificate is an independently verifiable, content-addressed statement of
// the exact bounds. It deliberately contains no extremal policy or other
// manipulation recipe: only the model digest, the exhaustive-record digest
// chain and the resulting bounds.
type Certificate struct {
	Version        string                     `json:"version"`
	ModelDigest    string                     `json:"model_digest"`
	MaxRetries     int                        `json:"max_retries"`
	PolicyCount    int                        `json:"policy_count"`
	RecordsRoot    string                     `json:"records_root"`
	FailureBound   string                     `json:"failure_bound"`
	MinCompletion  string                     `json:"min_completion"`
	CategoryBounds []CertificateCategoryBound `json:"category_bounds"`
}

// CertificateCategoryBound is the serializable per-category bound.
type CertificateCategoryBound struct {
	Category                  string `json:"category"`
	Ideal                     string `json:"ideal"`
	WorstConditionalDeviation string `json:"worst_conditional_deviation"`
	WorstMassDeviation        string `json:"worst_mass_deviation"`
}

// Certificate builds the verifiable certificate for an exhaustive result.
func (a *Analyzer) Certificate(res *Result) (*Certificate, error) {
	if res == nil {
		return nil, errors.New("admission: nil result")
	}
	cert := &Certificate{
		Version:       CertificateVersion,
		ModelDigest:   a.ModelDigest(),
		MaxRetries:    a.model.MaxRetries,
		PolicyCount:   res.PolicyCount,
		RecordsRoot:   recordsRoot(res.records),
		FailureBound:  res.FailureBound.RatString(),
		MinCompletion: res.MinCompletion.RatString(),
	}
	for _, b := range res.CategoryBounds {
		cert.CategoryBounds = append(cert.CategoryBounds, CertificateCategoryBound{
			Category:                  b.Category,
			Ideal:                     b.Ideal.RatString(),
			WorstConditionalDeviation: b.WorstConditionalDeviation.RatString(),
			WorstMassDeviation:        b.WorstMassDeviation.RatString(),
		})
	}
	return cert, nil
}

// Verify independently re-compiles and re-enumerates the model and checks
// every field and digest in the certificate.
func (c *Certificate) Verify(m *Model) error {
	if c == nil {
		return errors.New("admission: nil certificate")
	}
	if c.Version != CertificateVersion {
		return fmt.Errorf("admission: unsupported certificate version %q", c.Version)
	}
	a, err := m.Compile()
	if err != nil {
		return fmt.Errorf("admission: model failed re-compilation: %w", err)
	}
	if c.ModelDigest != a.ModelDigest() {
		return errors.New("admission: model digest mismatch")
	}
	if c.MaxRetries != m.MaxRetries {
		return errors.New("admission: max retries mismatch")
	}
	res, err := a.Enumerate()
	if err != nil {
		return fmt.Errorf("admission: re-enumeration failed: %w", err)
	}
	if c.PolicyCount != res.PolicyCount {
		return errors.New("admission: policy count mismatch")
	}
	if c.RecordsRoot != recordsRoot(res.records) {
		return errors.New("admission: exhaustive records root mismatch")
	}
	if err := requireSameRat("failure bound", c.FailureBound, res.FailureBound); err != nil {
		return err
	}
	if err := requireSameRat("min completion", c.MinCompletion, res.MinCompletion); err != nil {
		return err
	}
	if len(c.CategoryBounds) != len(res.CategoryBounds) {
		return errors.New("admission: category count mismatch")
	}
	for i, got := range c.CategoryBounds {
		want := res.CategoryBounds[i]
		if got.Category != want.Category {
			return fmt.Errorf("admission: category order mismatch at %d", i)
		}
		if err := requireSameRat("ideal "+got.Category, got.Ideal, want.Ideal); err != nil {
			return err
		}
		if err := requireSameRat("conditional deviation "+got.Category, got.WorstConditionalDeviation, want.WorstConditionalDeviation); err != nil {
			return err
		}
		if err := requireSameRat("mass deviation "+got.Category, got.WorstMassDeviation, want.WorstMassDeviation); err != nil {
			return err
		}
	}
	return nil
}

func requireSameRat(label, got string, want *big.Rat) error {
	r, ok := new(big.Rat).SetString(got)
	if !ok {
		return fmt.Errorf("admission: certificate %s is not a rational number: %q", label, got)
	}
	if r.Cmp(want) != 0 {
		return fmt.Errorf("admission: %s mismatch: certificate %s vs recomputed %s", label, got, want.RatString())
	}
	return nil
}

// ModelDigest returns the hex SHA-256 digest of the canonical model text.
func (a *Analyzer) ModelDigest() string {
	sum := sha256.Sum256([]byte(a.canonicalModel()))
	return hex.EncodeToString(sum[:])
}

func recordsRoot(records []string) string {
	h := sha256.New()
	h.Write([]byte(CertificateVersion + "\nrecords\n"))
	for _, rec := range records {
		line := sha256.Sum256([]byte(rec))
		h.Write(line[:])
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// canonicalModel renders every semantic input in a stable, line-oriented text.
// Only exported, public input fields are rendered, so no private material can
// enter the digest even if a host misuses an opaque field.
func (a *Analyzer) canonicalModel() string {
	m := a.model
	var b strings.Builder
	b.WriteString("version=" + CertificateVersion + "\n")
	b.WriteString("maxRetries=" + fmt.Sprint(m.MaxRetries) + "\n")

	b.WriteString("messages:")
	for _, msg := range m.PublicMessages {
		b.WriteString("|" + string(msg))
	}
	b.WriteString("\n")

	b.WriteString("participants:\n")
	for _, p := range m.Participants {
		b.WriteString(" P " + string(p.Subject) + " domain=" + p.FailureDomain + " sees")
		for _, msg := range p.VisibleMessages {
			b.WriteString("|" + string(msg))
		}
		b.WriteString("\n")
	}

	b.WriteString("hostConditions:\n")
	for _, hc := range m.HostConditions {
		b.WriteString(" H domain=" + hc.Domain + " obs")
		for _, msg := range hc.ObservedMessages {
			b.WriteString("|" + string(msg))
		}
		b.WriteString(" action=" + hc.Action + " rule=" + hc.Rule + "\n")
	}

	b.WriteString("ideal:\n")
	cats := make([]string, 0, len(m.Ideal))
	for cat := range m.Ideal {
		cats = append(cats, cat)
	}
	sortStringsLocal(cats)
	for _, cat := range cats {
		b.WriteString(" I " + cat + "=" + m.Ideal[cat].RatString() + "\n")
	}

	b.WriteString("nodes:\n")
	for _, id := range a.order {
		n := a.nodes[id]
		b.WriteString(" N " + id + " kind=" + kindName(n.Kind) + "\n")
		switch n.Kind {
		case KindDraw:
			b.WriteString("  D name=" + n.Draw.Name + " fixed=" + n.Draw.FixedVariable + " table")
			outcomes := make([]string, 0, len(n.Draw.Table))
			for out := range n.Draw.Table {
				outcomes = append(outcomes, out)
			}
			sortStringsLocal(outcomes)
			for _, out := range outcomes {
				b.WriteString("|" + out + "=" + n.Draw.Table[out].RatString())
			}
			b.WriteString("\n")
		case KindDecision:
			d := n.Decision
			b.WriteString("  C subject=" + string(d.Subject) + " obs")
			for _, msg := range d.ObservedMessages {
				b.WriteString("|" + string(msg))
			}
			b.WriteString(" actions")
			for _, act := range d.Actions {
				b.WriteString("|" + act)
			}
			b.WriteString(" abstain=" + d.AbstainAction + "\n")
		case KindTerminal:
			b.WriteString("  T category=" + n.Terminal.Category + "\n")
		}
		for _, e := range n.Out {
			b.WriteString("  E ->" + e.To)
			if e.OnAction != "" {
				b.WriteString(" action=" + e.OnAction)
			}
			if e.OnOutcome != "" {
				b.WriteString(" outcome=" + e.OnOutcome)
			}
			if e.Retry {
				b.WriteString(" retry=1")
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("root=" + m.Root + "\n")
	return b.String()
}

func kindName(k NodeKind) string {
	switch k {
	case KindEntry:
		return "entry"
	case KindDraw:
		return "draw"
	case KindDecision:
		return "decision"
	case KindTerminal:
		return "terminal"
	default:
		return "unknown"
	}
}

func sortStringsLocal(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
