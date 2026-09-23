package negotiate

import (
	"errors"
	"fmt"
)

// Certificate is the complete, verifiable record of a successful
// negotiation: the chosen selection plus the session summary that binds it
// to both parties' profiles.
type Certificate struct {
	Selection Selection
	Summary   [32]byte
}

// NewCertificate builds the certificate for a selection under profiles a, b.
func NewCertificate(a, b *Profile, sel Selection) Certificate {
	return Certificate{Selection: sel, Summary: SummaryOf(a, b, sel)}
}

// Verify recomputes the summary from the profiles and checks that the
// selection is complete and satisfiable under the merged conditions.
func (c Certificate) Verify(a, b *Profile) error {
	if c.Summary != SummaryOf(a, b, c.Selection) {
		return errors.New("negotiate: certificate summary mismatch")
	}
	prob, reasons := buildProblem(a, b)
	if len(reasons) > 0 {
		return fmt.Errorf("negotiate: profiles infeasible: %s", reasons[0])
	}
	return prob.validate(c.Selection)
}
