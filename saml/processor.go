package saml

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

// Verifier is the signature callback. Implementations validate the
// cryptographic signature over signed (for example by canonicalizing and
// digesting the referenced subtree and checking SignatureValue against a
// trusted key). The callback receives the exact *Element that the signature
// references inside the parsed document, so a verified result binds trust to
// that node and only that node.
type Verifier interface {
	Verify(signature, signed *Element) error
}

// Claims is the identity data consumed from the verified assertion. Every
// field is read by descending from the signed assertion pointer; elements
// elsewhere in the document are never consulted.
type Claims struct {
	AssertionID  string
	Issuer       string
	Subject      string
	Audience     string
	NotBefore    time.Time
	NotOnOrAfter time.Time
}

// Processor binds verified XML nodes to the SAML claims they authorize and
// enforces single-use assertion IDs.
type Processor struct {
	Verifier Verifier
	Replay   ReplayStore

	// Now returns the current time. Defaults to time.Now when nil, which
	// keeps tests deterministic.
	Now func() time.Time

	// ClockSkew widens the acceptable NotBefore/NotOnOrAfter window.
	ClockSkew time.Duration
}

// Process parses the message, verifies every signature through the callback,
// and extracts claims solely from the single assertion whose own node the
// verifier confirmed.
func (p *Processor) Process(r io.Reader) (*Claims, error) {
	if p.Verifier == nil {
		return nil, errors.New("saml: processor has no verifier")
	}
	if p.Replay == nil {
		return nil, errors.New("saml: processor has no replay store")
	}

	doc, err := Parse(r)
	if err != nil {
		return nil, err
	}

	signatures := doc.Signatures()
	if len(signatures) == 0 {
		return nil, ErrNoSignature
	}

	var verifiedAssertion *Element
	for _, sig := range signatures {
		target, err := resolveReference(doc, sig)
		if err != nil {
			return nil, err
		}
		if err := p.Verifier.Verify(sig, target); err != nil {
			return nil, fmt.Errorf("saml: signature verification failed: %w", err)
		}
		if target.Name.Local != "Assertion" || target.Name.Space != NSAssertion {
			return nil, ErrNotAssertion
		}
		if verifiedAssertion != nil && verifiedAssertion != target {
			return nil, ErrAmbiguousAssertion
		}
		verifiedAssertion = target
	}

	claims, err := extractClaims(verifiedAssertion)
	if err != nil {
		return nil, err
	}

	now := time.Now
	if p.Now != nil {
		now = p.Now
	}
	if err := claims.validate(now(), p.ClockSkew); err != nil {
		return nil, err
	}

	// Single-use check is the last gate: a rejected message never consumes
	// an ID, while an accepted assertion ID can never be accepted again.
	if err := p.Replay.Consume(claims.AssertionID); err != nil {
		return nil, err
	}
	return claims, nil
}

// resolveReference maps a ds:Reference URI attribute to the element it points
// at through the document index. Only same-document fragment references are
// supported, which is all SAML assertions use.
func resolveReference(doc *Document, sig *Element) (*Element, error) {
	var ref *Element
	walk(sig, func(el *Element) bool {
		if el.Name.Local == "Reference" && el.Name.Space == NSSignature {
			ref = el
			return false
		}
		return true
	})
	if ref == nil {
		return nil, fmt.Errorf("%w: missing Reference", ErrReferenceTarget)
	}
	uri := attrValue(ref, "", "URI")
	if uri == "" || !strings.HasPrefix(uri, "#") {
		return nil, fmt.Errorf("%w: unsupported reference %q", ErrReferenceTarget, uri)
	}
	id := uri[1:]
	target, ok := doc.LookupID(id)
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrReferenceTarget, id)
	}
	return target, nil
}

func extractClaims(assertion *Element) (*Claims, error) {
	c := &Claims{}

	if id, ok := elementID(assertion); ok {
		c.AssertionID = id
	}
	if c.AssertionID == "" {
		return nil, fmt.Errorf("%w: assertion has no ID", ErrCondition)
	}

	if issuer := firstChild(assertion, "Issuer", NSAssertion); issuer != nil {
		c.Issuer = strings.TrimSpace(issuer.Text)
	}

	subject := firstChild(assertion, "Subject", NSAssertion)
	if subject == nil {
		return nil, fmt.Errorf("%w: missing Subject", ErrCondition)
	}
	nameID := firstChild(subject, "NameID", NSAssertion)
	if nameID == nil {
		return nil, fmt.Errorf("%w: missing NameID", ErrCondition)
	}
	c.Subject = strings.TrimSpace(nameID.Text)
	if c.Subject == "" {
		return nil, fmt.Errorf("%w: empty NameID", ErrCondition)
	}

	conditions := firstChild(assertion, "Conditions", NSAssertion)
	if conditions == nil {
		return nil, fmt.Errorf("%w: missing Conditions", ErrCondition)
	}
	if v := attrValue(conditions, "", "NotBefore"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid NotBefore: %v", ErrCondition, err)
		}
		c.NotBefore = t
	}
	if v := attrValue(conditions, "", "NotOnOrAfter"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return nil, fmt.Errorf("%w: invalid NotOnOrAfter: %v", ErrCondition, err)
		}
		c.NotOnOrAfter = t
	}

	restriction := firstChild(conditions, "AudienceRestriction", NSAssertion)
	if restriction == nil {
		return nil, fmt.Errorf("%w: missing AudienceRestriction", ErrCondition)
	}
	audience := firstChild(restriction, "Audience", NSAssertion)
	if audience == nil || strings.TrimSpace(audience.Text) == "" {
		return nil, fmt.Errorf("%w: missing Audience", ErrCondition)
	}
	c.Audience = strings.TrimSpace(audience.Text)

	return c, nil
}

func (c *Claims) validate(now time.Time, skew time.Duration) error {
	if !c.NotBefore.IsZero() && now.Add(skew).Before(c.NotBefore) {
		return fmt.Errorf("%w: assertion not yet valid", ErrCondition)
	}
	if !c.NotOnOrAfter.IsZero() && !now.Add(-skew).Before(c.NotOnOrAfter) {
		return fmt.Errorf("%w: assertion expired", ErrCondition)
	}
	return nil
}

func firstChild(el *Element, local, space string) *Element {
	for _, c := range el.Children {
		if c.Name.Local == local && c.Name.Space == space {
			return c
		}
	}
	return nil
}

func attrValue(el *Element, space, local string) string {
	for _, a := range el.Attr {
		if a.Name.Space == space && a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
