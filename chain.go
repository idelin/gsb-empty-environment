package netsec

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	chainVersion           = 1
	generationDomain       = "netsec/q0490/timestamp-generation/v1"
	generationDigestDomain = "netsec/q0490/generation-digest/v1"
)

// Header is the immutable part of a chain. Re-signing cannot replace either
// field; both are covered by every generation signature.
type Header struct {
	EvidenceDigest Digest    `json:"evidence_digest"`
	CapturedAt     time.Time `json:"captured_at"`
}

// NewHeader hashes the original evidence and records its original collection
// time. CapturedAt is normalized to UTC and stripped of the monotonic clock
// reading so it is stable across serialization.
func NewHeader(evidence []byte, capturedAt time.Time) Header {
	return Header{
		EvidenceDigest: DigestOf(evidence),
		CapturedAt:     capturedAt.UTC().Round(0),
	}
}

func (h Header) normalized() Header {
	h.CapturedAt = h.CapturedAt.UTC().Round(0)
	return h
}

// GenerationDigest returns the digest that a following generation must use as
// its ParentDigest.
func (h Header) GenerationDigest(g Generation) Digest {
	return generationDigest(h.normalized(), g)
}

// Generation is one link in the append-only chain. It contains only the
// values added by a re-signing operation: the parent digest, issuance time,
// issuer certificate, and signature.
type Generation struct {
	ParentDigest Digest    `json:"parent_digest"`
	IssuedAt     time.Time `json:"issued_at"`
	IssuerDER    []byte    `json:"issuer_der"`
	Signature    []byte    `json:"signature"`
}

// IssuerCertificate parses the certificate embedded in the generation.
func (g Generation) IssuerCertificate() (*x509.Certificate, error) {
	cert, err := x509.ParseCertificate(g.IssuerDER)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrIssuerInvalid, err)
	}
	return cert, nil
}

func (g Generation) clone() Generation {
	g.IssuerDER = append([]byte(nil), g.IssuerDER...)
	g.Signature = append([]byte(nil), g.Signature...)
	return g
}

type signedFields struct {
	Domain            string `json:"domain"`
	EvidenceDigest    string `json:"evidence_digest"`
	CapturedAt        string `json:"captured_at"`
	ParentDigest      string `json:"parent_digest"`
	IssuedAt          string `json:"issued_at"`
	IssuerFingerprint string `json:"issuer_fingerprint"`
}

func generationPayload(h Header, g Generation) []byte {
	h = h.normalized()
	fingerprint := DigestOf(g.IssuerDER)
	fields := signedFields{
		Domain:            generationDomain,
		EvidenceDigest:    h.EvidenceDigest.String(),
		CapturedAt:        h.CapturedAt.Format(time.RFC3339Nano),
		ParentDigest:      g.ParentDigest.String(),
		IssuedAt:          g.IssuedAt.UTC().Round(0).Format(time.RFC3339Nano),
		IssuerFingerprint: fingerprint.String(),
	}
	// The struct contains only strings, so Marshal cannot fail.
	payload, _ := json.Marshal(fields)
	return payload
}

func generationDigest(h Header, g Generation) Digest {
	hash := sha256.New()
	hash.Write([]byte(generationDigestDomain))
	hash.Write(generationPayload(h, g))
	hash.Write(g.Signature)
	var digest Digest
	copy(digest[:], hash.Sum(nil))
	return digest
}

// SignGeneration signs a single detached generation. Hosts normally call
// Chain.Append; this function exists so records restored from external storage
// and fork audits can be signed and verified without mutating a Chain.
func SignGeneration(h Header, parent Digest, issuedAt time.Time, issuer Issuer) (Generation, error) {
	h = h.normalized()
	issuedAt = issuedAt.UTC().Round(0)
	if issuedAt.Before(h.CapturedAt) {
		return Generation{}, fmt.Errorf("%w: issued at %s, captured at %s",
			ErrTimeBeforeCapture,
			issuedAt.Format(time.RFC3339Nano), h.CapturedAt.Format(time.RFC3339Nano))
	}
	cert, err := issuer.validate(issuedAt)
	if err != nil {
		return Generation{}, err
	}

	g := Generation{
		ParentDigest: parent,
		IssuedAt:     issuedAt,
		IssuerDER:    append([]byte(nil), cert.Raw...),
	}
	g.Signature, err = signPayload(cert, issuer.Signer, generationPayload(h, g))
	if err != nil {
		return Generation{}, fmt.Errorf("%w: %v", ErrIssuerSignature, err)
	}
	return g, nil
}

// Chain is an immutable signed timestamp chain. Append returns a new Chain;
// it never modifies the receiver or its existing generations.
type Chain struct {
	header      Header
	generations []Generation
}

// NewChain seals evidence at capturedAt with the issuer's signature at the
// same time. The first generation's parent is the evidence digest.
func NewChain(evidence []byte, capturedAt time.Time, issuer Issuer) (*Chain, error) {
	return NewChainAt(evidence, capturedAt, capturedAt, issuer)
}

// NewChainAt seals evidence captured at capturedAt with a first generation
// issued at issuedAt. This supports ingesting old evidence without replacing
// its original collection time.
func NewChainAt(evidence []byte, capturedAt, issuedAt time.Time, issuer Issuer) (*Chain, error) {
	header := NewHeader(evidence, capturedAt)
	first, err := SignGeneration(header, header.EvidenceDigest, issuedAt, issuer)
	if err != nil {
		return nil, err
	}
	return &Chain{
		header:      header,
		generations: []Generation{first},
	}, nil
}

// Header returns the immutable chain header.
func (c *Chain) Header() Header {
	if c == nil {
		return Header{}
	}
	return c.header
}

// EvidenceDigest returns the immutable SHA-256 digest of the original evidence.
func (c *Chain) EvidenceDigest() Digest {
	if c == nil {
		return Digest{}
	}
	return c.header.EvidenceDigest
}

// CapturedAt returns the immutable original collection time.
func (c *Chain) CapturedAt() time.Time {
	if c == nil {
		return time.Time{}
	}
	return c.header.CapturedAt
}

// Len returns the number of generations.
func (c *Chain) Len() int {
	if c == nil {
		return 0
	}
	return len(c.generations)
}

// Generations returns a deep copy of every generation, including generations
// that fail verification.
func (c *Chain) Generations() []Generation {
	if c == nil {
		return nil
	}
	out := make([]Generation, len(c.generations))
	for i, g := range c.generations {
		out[i] = g.clone()
	}
	return out
}

// Generation returns a deep copy of generation i.
func (c *Chain) Generation(i int) (Generation, bool) {
	if c == nil || i < 0 || i >= len(c.generations) {
		return Generation{}, false
	}
	return c.generations[i].clone(), true
}

// Tip returns the newest generation.
func (c *Chain) Tip() (Generation, bool) {
	return c.Generation(c.Len() - 1)
}

// GenerationDigest returns the digest of generation i.
func (c *Chain) GenerationDigest(i int) (Digest, error) {
	if c == nil || i < 0 || i >= len(c.generations) {
		return Digest{}, fmt.Errorf("netsec: generation index %d out of range", i)
	}
	return generationDigest(c.header, c.generations[i]), nil
}

// TipDigest returns the digest that the next generation must reference.
func (c *Chain) TipDigest() (Digest, bool) {
	if c == nil || len(c.generations) == 0 {
		return Digest{}, false
	}
	return generationDigest(c.header, c.generations[len(c.generations)-1]), true
}

// Append adds one generation. It returns ErrTimeRegression if issuedAt is
// earlier than the tip, and ErrTimeBeforeCapture if it predates the original
// collection time. Equal timestamps are allowed on a linear chain; forked
// siblings sharing a parent (and especially siblings sharing a time) are
// rejected by Verify. The receiver is never modified.
func (c *Chain) Append(issuedAt time.Time, issuer Issuer) (*Chain, error) {
	if c == nil || len(c.generations) == 0 {
		return nil, ErrEmptyChain
	}
	issuedAt = issuedAt.UTC().Round(0)
	last := c.generations[len(c.generations)-1]

	var timeErrs []error
	if issuedAt.Before(c.header.CapturedAt) {
		timeErrs = append(timeErrs, fmt.Errorf("%w: issued at %s, captured at %s",
			ErrTimeBeforeCapture,
			issuedAt.Format(time.RFC3339Nano), c.header.CapturedAt.Format(time.RFC3339Nano)))
	}
	if issuedAt.Before(last.IssuedAt) {
		timeErrs = append(timeErrs, fmt.Errorf("%w: issued at %s, parent issued at %s",
			ErrTimeRegression,
			issuedAt.Format(time.RFC3339Nano), last.IssuedAt.Format(time.RFC3339Nano)))
	}
	if len(timeErrs) > 0 {
		return nil, errors.Join(timeErrs...)
	}

	parent := generationDigest(c.header, last)
	g, err := SignGeneration(c.header, parent, issuedAt, issuer)
	if err != nil {
		return nil, err
	}

	next := &Chain{
		header:      c.header,
		generations: make([]Generation, len(c.generations), len(c.generations)+1),
	}
	for i, existing := range c.generations {
		next.generations[i] = existing.clone()
	}
	next.generations = append(next.generations, g.clone())
	return next, nil
}

// Reseal is an alias for Append: it appends a new trusted time proof without
// changing the original capture time or evidence digest.
func (c *Chain) Reseal(issuedAt time.Time, issuer Issuer) (*Chain, error) {
	return c.Append(issuedAt, issuer)
}

// VerifyOptions controls optional trust anchors and externally anchored facts.
type VerifyOptions struct {
	// Roots enables X.509 path validation at each historical issuance time.
	Roots *x509.CertPool
	// Intermediates supplies intermediate certificates for path validation.
	Intermediates *x509.CertPool
	// RequireTrustedIssuer rejects chains when Roots is nil.
	RequireTrustedIssuer bool
	// ExpectedCapturedAt, when non-nil, must equal the chain header's original
	// collection time. It lets a host detect a wholly replaced chain.
	ExpectedCapturedAt *time.Time
}

// Verify checks the evidence binding, complete parent chain, time ordering,
// every signature, and each issuer's validity at its generation's issuance
// time. It returns a *VerificationError containing every violation; no
// generation is truncated or modified on failure.
func (c *Chain) Verify(evidence []byte, opts ...VerifyOptions) error {
	var options VerifyOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	if c == nil || len(c.generations) == 0 {
		return &VerificationError{Violations: []Violation{{Generation: -1, Err: ErrEmptyChain}}}
	}

	header := c.header.normalized()
	var violations []Violation
	if want := DigestOf(evidence); want != header.EvidenceDigest {
		violations = append(violations, Violation{
			Generation: -1,
			Err: fmt.Errorf("%w: got %s, chain expects %s",
				ErrEvidenceMismatch, want.String(), header.EvidenceDigest.String()),
		})
	}
	if options.ExpectedCapturedAt != nil &&
		!header.CapturedAt.Equal((*options.ExpectedCapturedAt).UTC().Round(0)) {
		violations = append(violations, Violation{
			Generation: -1,
			Err: fmt.Errorf("%w: got %s, chain expects %s",
				ErrCaptureTimeMismatch,
				(*options.ExpectedCapturedAt).UTC().Round(0).Format(time.RFC3339Nano),
				header.CapturedAt.Format(time.RFC3339Nano)),
		})
	}
	violations = append(violations, verifyGenerations(header, c.generations, options)...)
	if len(violations) > 0 {
		return &VerificationError{Violations: violations}
	}
	return nil
}

// VerifyGenerations checks a detached chain against evidence and its original
// collection time. It is intended for fork audits and for records restored
// from external storage.
func VerifyGenerations(evidence []byte, capturedAt time.Time, generations []Generation, opts ...VerifyOptions) error {
	var options VerifyOptions
	if len(opts) > 0 {
		options = opts[0]
	}
	if len(generations) == 0 {
		return &VerificationError{Violations: []Violation{{Generation: -1, Err: ErrEmptyChain}}}
	}
	header := NewHeader(evidence, capturedAt)
	violations := verifyGenerations(header, generations, options)
	if len(violations) > 0 {
		return &VerificationError{Violations: violations}
	}
	return nil
}

func verifyGenerations(header Header, generations []Generation, opts VerifyOptions) []Violation {
	header = header.normalized()
	var violations []Violation
	add := func(generation int, err error) {
		if err != nil {
			violations = append(violations, Violation{Generation: generation, Err: err})
		}
	}

	parentUses := make(map[Digest][]int)
	digestUses := make(map[Digest][]int)
	var previousIssued time.Time

	for i, g := range generations {
		if g.IssuedAt.Before(header.CapturedAt) {
			add(i, fmt.Errorf("%w: issued at %s, captured at %s",
				ErrTimeBeforeCapture,
				g.IssuedAt.UTC().Round(0).Format(time.RFC3339Nano),
				header.CapturedAt.Format(time.RFC3339Nano)))
		}
		if i > 0 && g.IssuedAt.Before(previousIssued) {
			add(i, fmt.Errorf("%w: issued at %s, previous generation issued at %s",
				ErrTimeRegression,
				g.IssuedAt.UTC().Round(0).Format(time.RFC3339Nano),
				previousIssued.UTC().Round(0).Format(time.RFC3339Nano)))
		}

		expectedParent := header.EvidenceDigest
		if i > 0 {
			expectedParent = generationDigest(header, generations[i-1])
		}
		if g.ParentDigest != expectedParent {
			add(i, fmt.Errorf("%w: got %s, expected %s",
				ErrParentMismatch, g.ParentDigest.String(), expectedParent.String()))
		}

		parentUses[g.ParentDigest] = append(parentUses[g.ParentDigest], i)
		if uses := parentUses[g.ParentDigest]; len(uses) > 1 {
			first := generations[uses[0]]
			switch {
			case g.IssuedAt.Equal(first.IssuedAt):
				add(i, errors.Join(
					fmt.Errorf("%w: parent %s shared by generations %d and %d",
						ErrFork, g.ParentDigest.String(), uses[0], i),
					fmt.Errorf("%w: both issued at %s",
						ErrSameTimeFork, g.IssuedAt.UTC().Round(0).Format(time.RFC3339Nano)),
				))
			default:
				add(i, fmt.Errorf("%w: parent %s shared by generations %d and %d",
					ErrFork, g.ParentDigest.String(), uses[0], i))
			}
		}

		digest := generationDigest(header, g)
		digestUses[digest] = append(digestUses[digest], i)
		if uses := digestUses[digest]; len(uses) > 1 {
			add(i, fmt.Errorf("%w: digest %s used by generations %d and %d",
				ErrDuplicateGeneration, digest.String(), uses[0], i))
		}

		cert, err := x509.ParseCertificate(g.IssuerDER)
		if err != nil {
			add(i, fmt.Errorf("%w: %v", ErrIssuerInvalid, err))
		} else {
			add(i, checkCertificateTime(cert, g.IssuedAt))
			add(i, verifySignature(cert, generationPayload(header, g), g.Signature))
			add(i, verifyCertificateTrust(cert, g.IssuedAt, opts))
		}

		previousIssued = g.IssuedAt
	}
	return violations
}
