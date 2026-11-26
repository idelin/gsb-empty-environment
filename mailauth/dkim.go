package mailauth

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
	"time"
)

const (
	algRSASHA256   = "rsa-sha256"
	algEd25519SHA  = "ed25519-sha256"
	algEd25519SHA2 = "ed25519-sha256-01"
)

type sigMode int

const (
	modeDKIM sigMode = iota
	modeAMS
	modeAS
)

// coreResult is the shared output of DKIM, AMS and AS verification.
type coreResult struct {
	tags                *sigTags
	valid               bool
	findings            []Finding
	bodyLengthLimit     int64
	canonicalBodyLength int64
	truncated           bool
	fromEntries         int
	fromBound           int
}

// VerifyMessage parses and verifies a raw RFC 5322 message, including all
// DKIM signatures and any ARC chain, then applies the configured anti-spoofing
// policy. It never accesses the network.
func (v *Verifier) VerifyMessage(raw []byte) (*MessageVerdict, error) {
	m, err := ParseMessage(raw)
	if err != nil {
		return nil, err
	}
	return v.verifyParsed(m), nil
}

func (v *Verifier) verifyParsed(m *Message) *MessageVerdict {
	verdict := &MessageVerdict{Accepted: true}
	now := v.policy.Now()

	v.structureChecks(m, verdict)
	v.verifyAllDKIM(m, verdict, now)
	v.verifyARC(m, verdict, now)
	v.applyPolicy(m, verdict)
	return verdict
}

func (v *Verifier) structureChecks(m *Message, verdict *MessageVerdict) {
	switch {
	case m.FromCount == 0:
		verdict.addFinding(newFinding(FindingMissingFrom, SeverityHigh, "message",
			"message has no From header field"))
	case m.FromCount > 1:
		verdict.addFinding(newFinding(FindingMultipleFrom, SeverityCritical, "message",
			fmt.Sprintf("multiple From header fields detected: %d instances", m.FromCount)))
	}
	if m.DKIMSignatureCount == 0 {
		verdict.addFinding(newFinding(FindingNoDKIMSignature, SeverityHigh, "message",
			"message carries no DKIM-Signature header field"))
	}
}

func (v *Verifier) verifyAllDKIM(m *Message, verdict *MessageVerdict, now time.Time) {
	for _, h := range m.Headers {
		if !headerNameIs(h.Name, "dkim-signature") {
			continue
		}
		result := v.verifyOneDKIM(m, h, now)
		verdict.DKIM = append(verdict.DKIM, result)

		location := fmt.Sprintf("dkim[%d]", result.Index)
		if result.FromEntries == 0 {
			verdict.addFinding(newFinding(FindingFromNotCovered, SeverityHigh, location,
				"h= tag list does not cover the From header field"))
		}
		if m.FromCount > result.FromBound {
			verdict.addFinding(newFinding(FindingExtraFromUnsigned, SeverityCritical, location,
				fmt.Sprintf("message has %d From field(s) but only %d is/are signed; "+
					"an injected From header would not be protected",
					m.FromCount, result.FromBound)))
		}
		if result.Truncated {
			verdict.addFinding(newFinding(FindingBodyLengthInjection, SeverityCritical, location,
				fmt.Sprintf("l= limits the signed body to %d bytes but the canonical body is %d bytes; "+
					"trailing content is unsigned and may be injected content",
					result.BodyLengthLimit, result.CanonicalBodyLength)))
		}
	}
}

func (v *Verifier) verifyOneDKIM(m *Message, sigHeader Header, now time.Time) DKIMResult {
	location := fmt.Sprintf("dkim[%d]", sigHeader.Index)
	result := DKIMResult{Index: sigHeader.Index, BodyLengthLimit: -1}

	tags, parseErr := parseSignature(sigHeader.Value)
	if parseErr != nil {
		result.Valid = false
		result.Findings = append(result.Findings,
			newFinding(FindingMalformedSignature, SeverityHigh, location, parseErr.Error()))
		return result
	}
	result.Domain = tags.domain
	result.Selector = tags.selector
	result.Algorithm = tags.algorithm

	core := v.verifyCore(m.Headers, m.Body, sigHeader, sigHeader.Name, tags, modeDKIM, now, location)
	result.BodyLengthLimit = core.bodyLengthLimit
	result.CanonicalBodyLength = core.canonicalBodyLength
	result.Truncated = core.truncated
	result.FromEntries = core.fromEntries
	result.FromBound = core.fromBound
	result.Findings = core.findings
	result.Valid = core.valid

	// DKIM-Signature mandates v=DKIM1 (RFC 6376 section 3.2).
	if tags.version != "1" {
		result.Valid = false
		result.Findings = append(result.Findings, newFinding(FindingMalformedSignature,
			SeverityHigh, location, fmt.Sprintf("invalid DKIM version tag v=%q", tags.version)))
	}
	return result
}

// verifyCore runs the verification steps shared by DKIM-Signature,
// ARC-Message-Signature and ARC-Seal.
func (v *Verifier) verifyCore(headers []Header, body []byte, sigHeader Header,
	sigMatchName string, tags *sigTags, mode sigMode, now time.Time, location string) coreResult {

	r := coreResult{tags: tags, valid: true, bodyLengthLimit: -1}
	invalidate := func(code string, sev Severity, msg string) {
		r.valid = false
		r.findings = append(r.findings, newFinding(code, sev, location, msg))
	}

	if tags.domain == "" || tags.selector == "" {
		invalidate(FindingMalformedSignature, SeverityHigh,
			"signature is missing the required d= or s= tag")
	}
	if tags.algorithm != algRSASHA256 && tags.algorithm != algEd25519SHA &&
		tags.algorithm != algEd25519SHA2 {
		invalidate(FindingUnsupportedAlgorithm, SeverityHigh,
			fmt.Sprintf("unsupported algorithm a=%q", tags.algorithm))
	}
	if len(tags.headerList) == 0 {
		invalidate(FindingMalformedSignature, SeverityHigh,
			"signature has an empty or missing h= tag list")
	}

	// l= is only defined for relaxed body canonicalization (RFC 6376 3.4.5).
	if tags.hasBodyLength && tags.bodyAlgo != canonRelaxed {
		invalidate(FindingLTagSimpleCanon, SeverityHigh,
			"l= body length tag is only valid with relaxed body canonicalization")
	}

	r.fromEntries = countHeaderName(tags.headerList, "from")

	if mode != modeAS {
		r.checkBodyHash(body, tags, invalidate)
	}
	r.checkTime(tags, now, invalidate)

	data, fromBound, err := signedData(headers, sigHeader, sigHeader.Name, tags)
	if err != nil {
		invalidate(FindingMalformedSignature, SeverityHigh, err.Error())
		return r
	}
	r.fromBound = fromBound

	if r.fromEntries > 0 {
		// Oversigning placeholder: the signer bound more From slots than the
		// message currently has, which blocks later From injection.
		if r.fromEntries > countConcreteHeaders(headers, "from") {
			r.findings = append(r.findings, newFinding(FindingFromOversigned,
				SeverityInfo, location,
				fmt.Sprintf("h= oversigns From: %d slot(s) but only %d field(s) exist; "+
					"extra slots are signed as empty placeholders",
					r.fromEntries, countConcreteHeaders(headers, "from"))))
		}
	}

	if tags.algorithm == algRSASHA256 || tags.algorithm == algEd25519SHA ||
		tags.algorithm == algEd25519SHA2 {
		r.checkCryptography(v.keys, data, tags, invalidate)
	}
	return r
}

func (r *coreResult) checkBodyHash(body []byte, tags *sigTags,
	invalidate func(string, Severity, string)) {

	canonLen := int64(canonicalBodyLength(tags.bodyAlgo, body))
	r.canonicalBodyLength = canonLen

	limit := int64(-1)
	if tags.hasBodyLength {
		limit = tags.bodyLength
		r.bodyLengthLimit = limit
		if limit > canonLen {
			// RFC 6376 3.5: l= larger than the body is a verifier permerror.
			invalidate(FindingBodyLengthTooLarge, SeverityHigh,
				fmt.Sprintf("l=%d exceeds canonical body length %d", limit, canonLen))
		}
		if canonLen > limit {
			r.truncated = true
			r.findings = append(r.findings, newFinding(FindingBodyLengthInjection,
				SeverityCritical, "",
				fmt.Sprintf("canonical body is %d bytes but l= only signs the first %d",
					canonLen, limit)))
		}
	}

	expected := bodyHash(tags.bodyAlgo, body, limit)
	provided, err := decodeBase64(tags.bodyHashB64)
	if err != nil {
		invalidate(FindingMalformedSignature, SeverityHigh,
			"bh= tag is not valid base64: "+err.Error())
		return
	}
	want, _ := decodeBase64(expected)
	if !bytes.Equal(provided, want) {
		invalidate(FindingBodyHashMismatch, SeverityCritical,
			"computed body hash does not match bh= tag")
	}
}

func (r *coreResult) checkTime(tags *sigTags, now time.Time,
	invalidate func(string, Severity, string)) {

	if tags.hasCreated && tags.hasExpires && tags.expires <= tags.created {
		invalidate(FindingInvalidTimeRange, SeverityHigh,
			fmt.Sprintf("expiration x=%d must be later than creation t=%d",
				tags.expires, tags.created))
	}
	if tags.hasExpires {
		expiry := time.Unix(tags.expires, 0).UTC()
		if !now.UTC().Before(expiry) {
			invalidate(FindingSignatureExpired, SeverityHigh,
				fmt.Sprintf("signature expired at %s (verification time %s)",
					expiry.Format(time.RFC3339), now.UTC().Format(time.RFC3339)))
		}
	}
}

func (r *coreResult) checkCryptography(keys KeyResolver, signed []byte, tags *sigTags,
	invalidate func(string, Severity, string)) {

	signature, err := decodeBase64(tags.signatureB64)
	if err != nil {
		invalidate(FindingMalformedSignature, SeverityHigh,
			"b= tag is not valid base64: "+err.Error())
		return
	}

	// Key resolution is fully host supplied; there is no DNS or network path.
	keyRecord, ok := keys.LookupKey(tags.domain, tags.selector)
	if !ok {
		invalidate(FindingKeyUnavailable, SeverityHigh,
			fmt.Sprintf("no local public key for %s.%s", tags.selector, tags.domain))
		return
	}

	sum := sha256.Sum256(signed)
	switch key := keyRecord.Key.(type) {
	case *rsa.PublicKey:
		if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, sum[:], signature); err != nil {
			invalidate(FindingSignatureMismatch, SeverityCritical,
				"RSA signature verification failed: "+err.Error())
		}
	case ed25519.PublicKey:
		if !ed25519.Verify(key, signed, signature) {
			invalidate(FindingSignatureMismatch, SeverityCritical,
				"Ed25519 signature verification failed")
		}
	default:
		invalidate(FindingUnsupportedAlgorithm, SeverityHigh,
			fmt.Sprintf("public key type %T is not supported", keyRecord.Key))
	}
}

func countHeaderName(names []string, want string) int {
	n := 0
	for _, name := range names {
		if name == want {
			n++
		}
	}
	return n
}

func countConcreteHeaders(headers []Header, want string) int {
	n := 0
	for _, h := range headers {
		if headerNameIs(h.Name, want) {
			n++
		}
	}
	return n
}

func (v *MessageVerdict) addFinding(f Finding) {
	v.Findings = append(v.Findings, f)
}

// applyPolicy turns findings into the final accept/reject decision.
func (v *Verifier) applyPolicy(m *Message, verdict *MessageVerdict) {
	p := v.policy
	reject := func(code, reason string) {
		verdict.Accepted = false
		if verdict.Reason == "" {
			verdict.Reason = reason
		}
		_ = code
	}

	if p.RejectMultipleFrom && m.FromCount > 1 {
		reject(FindingMultipleFrom, "multiple From header fields are present")
	}

	fromProtected := false
	anyValidDKIM := false
	for _, r := range verdict.DKIM {
		if r.Valid {
			anyValidDKIM = true
			if r.FromBound > 0 {
				fromProtected = true
			}
		}
	}
	if p.RejectUnsignedFrom && m.FromCount > 0 && !fromProtected {
		reject(FindingFromNotCovered, "no valid signature covers the From header field")
	}

	for _, r := range verdict.DKIM {
		if r.Truncated && p.RejectBodyLengthInjection {
			reject(FindingBodyLengthInjection,
				"body length limit hides unsigned trailing content (truncation injection)")
		}
	}

	arcValid := verdict.ARC != nil && verdict.ARC.Valid
	if p.RejectUnauthenticated && !anyValidDKIM && !arcValid {
		reject(FindingNoDKIMSignature, "message has no valid DKIM signature and no valid ARC chain")
	}
}
