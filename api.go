package netsec

// Additional type aliases matching common archive terminology.
type (
	Header        = WindowHeader
	AuditEntry    = Entry
	ChainAnchor   = Anchor
	WindowSegment = Window
	AuditReport   = Report
)

// Verifier holds a set of immutable external anchors and can verify one or
// more archived window sets against them.
type Verifier struct {
	Anchors []Anchor
}

// NewVerifier returns a verifier bound to anchors. The anchors are copied so
// later mutation by the caller cannot change a verification result.
func NewVerifier(anchors []Anchor) *Verifier {
	return &Verifier{Anchors: cloneAnchors(anchors)}
}

// Verify checks windows against the verifier's anchors.
func (v *Verifier) Verify(windows []Window) error {
	if v == nil {
		return ErrInvalid
	}
	return VerifyChain(windows, v.Anchors)
}

// Check performs a full verification and returns every detected issue.
func (v *Verifier) Check(windows []Window) *Report {
	if v == nil {
		return &Report{Issues: []error{ErrInvalid}}
	}
	return CheckChain(windows, v.Anchors)
}

// Chain groups archived windows with the anchors they must satisfy.
type Chain struct {
	Windows []Window
	Anchors []Anchor
}

// Verify checks the chain.
func (c Chain) Verify() error {
	return VerifyChain(c.Windows, c.Anchors)
}

// Check performs a full verification and returns every detected issue.
func (c Chain) Check() *Report {
	return CheckChain(c.Windows, c.Anchors)
}

func cloneAnchors(anchors []Anchor) []Anchor {
	if anchors == nil {
		return nil
	}
	cloned := make([]Anchor, len(anchors))
	for i, anchor := range anchors {
		cloned[i] = Anchor{Seq: anchor.Seq, Hash: cloneBytes(anchor.Hash)}
	}
	return cloned
}
