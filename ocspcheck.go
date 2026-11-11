// Package ocspcheck verifies OCSP responses stapled by a TLS server.
//
// Install [Config.VerifyConnection] on a [crypto/tls.Config] to enforce that
// the server's leaf certificate is backed by a valid, fresh OCSP response
// signed by the issuing CA or by an authorized OCSP delegation certificate.
// Verification rules mirror RFC 6960:
//
//   - The SingleResponse CertID must match the leaf certificate serial number
//     and the issuer name/key hashes.
//   - The signature must be made by the issuing CA itself, or by a delegated
//     responder certificate signed by the issuer, currently valid, not a CA,
//     and carrying the OCSPSigning extended key usage.
//   - thisUpdate must not be in the future and nextUpdate must not be in the
//     past, both allowing a configurable clock skew.
//   - "unknown" certificate status and missing staples are handled according
//     to caller-supplied policies (fail closed by default).
package ocspcheck

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"fmt"
	"time"
)

// UnknownPolicy controls how an OCSP response reporting certificateStatus
// "unknown" is handled.
type UnknownPolicy int

const (
	// UnknownFails rejects handshakes whose OCSP response says "unknown".
	UnknownFails UnknownPolicy = iota
	// UnknownAllowed treats an "unknown" OCSP response as acceptable.
	UnknownAllowed
)

// MissingPolicy controls how a TLS connection without any stapled OCSP
// response is handled.
type MissingPolicy int

const (
	// MissingFails rejects handshakes that carry no stapled OCSP response.
	MissingFails MissingPolicy = iota
	// MissingAllowed lets handshakes without an OCSP staple proceed.
	MissingAllowed
)

const (
	defaultSkew   = time.Minute
	defaultMaxAge = 7 * 24 * time.Hour
)

// Config configures OCSP staple verification.
//
// A zero Skew is replaced with a one minute default and a zero MaxAge is
// replaced with a seven day default. Set MaxAge to a negative duration to
// require every response to carry nextUpdate.
type Config struct {
	// Skew is the maximum tolerated clock difference when comparing
	// thisUpdate and nextUpdate with the current time.
	Skew time.Duration

	// MaxAge bounds the age of responses that omit nextUpdate, measured from
	// thisUpdate. It has no effect when nextUpdate is present. Negative means
	// responses without nextUpdate are rejected outright.
	MaxAge time.Duration

	// UnknownPolicy decides what happens when the responder reports status
	// "unknown". Defaults to UnknownFails.
	UnknownPolicy UnknownPolicy

	// MissingPolicy decides what happens when the server staples no OCSP
	// response. Defaults to MissingFails.
	MissingPolicy MissingPolicy

	// now is overridable in tests; production code uses time.Now.
	now func() time.Time
}

// Sentinel errors wrapped (and thus discoverable via errors.Is) so callers
// can classify verification failures.
var (
	// ErrMissingResponse is returned when no OCSP response is stapled and
	// MissingPolicy is MissingFails.
	ErrMissingResponse = errors.New("ocspcheck: no stapled OCSP response")

	// ErrUnknownStatus is returned when the OCSP responder reports
	// certificateStatus "unknown" and UnknownPolicy is UnknownFails.
	ErrUnknownStatus = errors.New("ocspcheck: OCSP responder returned unknown certificate status")

	// ErrRevoked is returned when the OCSP responder reports the certificate
	// as revoked.
	ErrRevoked = errors.New("ocspcheck: certificate has been revoked")
)

// VerifyConnection implements [crypto/tls.Config.VerifyConnection]. The
// normal certificate-chain verification performed by crypto/tls still runs
// first; this callback adds OCSP staple enforcement on top of it.
func (c *Config) VerifyConnection(cs tls.ConnectionState) error {
	now := time.Now
	if c.now != nil {
		now = c.now
	}
	return c.checkAt(cs, now().UTC())
}

// withClock returns a copy of c whose clock is f. It exists for tests in this
// package so that time-boundary behavior is deterministic.
func (c Config) withClock(f func() time.Time) Config {
	c.now = f
	return c
}

func (c *Config) effectiveSkew() time.Duration {
	if c.Skew > 0 {
		return c.Skew
	}
	return defaultSkew
}

func (c *Config) effectiveMaxAge() time.Duration {
	if c.MaxAge != 0 {
		return c.MaxAge
	}
	return defaultMaxAge
}

// checkAt runs the full verification against an explicit reference time.
func (c *Config) checkAt(cs tls.ConnectionState, now time.Time) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("ocspcheck: connection has no peer certificate")
	}
	leaf := cs.PeerCertificates[0]
	issuer, err := issuerCertificate(cs)
	if err != nil {
		return err
	}

	if len(cs.OCSPResponse) == 0 {
		if c.MissingPolicy == MissingAllowed {
			return nil
		}
		return fmt.Errorf("%w", ErrMissingResponse)
	}

	resp, err := parseBasicResponse(cs.OCSPResponse)
	if err != nil {
		return err
	}

	signer, err := c.resolveSigner(resp.tbs.ResponderID, issuer, resp.certificates, now)
	if err != nil {
		return err
	}
	if err := verifyResponseSignature(resp, signer.PublicKey); err != nil {
		return err
	}

	single, err := findSingleResponse(resp.tbs.Responses, leaf, issuer)
	if err != nil {
		return err
	}
	if err := c.checkTiming(*single, now); err != nil {
		return err
	}
	return checkStatus(*single, c.UnknownPolicy)
}

func issuerCertificate(cs tls.ConnectionState) (*x509.Certificate, error) {
	if len(cs.VerifiedChains) > 0 && len(cs.VerifiedChains[0]) >= 2 {
		return cs.VerifiedChains[0][1], nil
	}
	if len(cs.PeerCertificates) >= 2 {
		return cs.PeerCertificates[1], nil
	}
	return nil, errors.New("ocspcheck: cannot determine issuer certificate from connection state")
}

func (c *Config) checkTiming(sr singleResponse, now time.Time) error {
	skew := c.effectiveSkew()
	// thisUpdate is the earliest moment at which the response is valid.
	if now.Add(skew).Before(sr.ThisUpdate) {
		return fmt.Errorf("ocspcheck: OCSP response is not yet valid (thisUpdate=%s, now=%s)",
			sr.ThisUpdate.UTC().Format(time.RFC3339), now.Format(time.RFC3339))
	}
	if !sr.NextUpdate.IsZero() {
		if now.Add(-skew).After(sr.NextUpdate) {
			return fmt.Errorf("ocspcheck: OCSP response has expired (nextUpdate=%s, now=%s)",
				sr.NextUpdate.UTC().Format(time.RFC3339), now.Format(time.RFC3339))
		}
		return nil
	}
	maxAge := c.effectiveMaxAge()
	if maxAge < 0 {
		return errors.New("ocspcheck: OCSP response omits nextUpdate, which is required by policy")
	}
	if age := now.Sub(sr.ThisUpdate); age > maxAge {
		return fmt.Errorf("ocspcheck: OCSP response without nextUpdate is stale (age=%s, max=%s)",
			age, maxAge)
	}
	return nil
}

func checkStatus(sr singleResponse, policy UnknownPolicy) error {
	switch {
	case sr.Status.Tag == 0 && sr.Status.Class == asn1.ClassContextSpecific:
		return nil
	case sr.Status.Tag == 1 && sr.Status.Class == asn1.ClassContextSpecific:
		revocationTime, err := parseRevocationTime(sr.Status)
		if err != nil {
			return err
		}
		return fmt.Errorf("%w (revocation time=%s)", ErrRevoked,
			revocationTime.UTC().Format(time.RFC3339))
	case sr.Status.Tag == 2 && sr.Status.Class == asn1.ClassContextSpecific:
		if policy == UnknownAllowed {
			return nil
		}
		return fmt.Errorf("%w (serial=%s)", ErrUnknownStatus, sr.CertID.SerialNumber)
	default:
		return errors.New("ocspcheck: OCSP response carries no recognizable certificate status")
	}
}

// parseRevocationTime extracts the GeneralizedTime that is the first element
// inside the revoked [1] choice (the optional CRLReason [0] follows it).
func parseRevocationTime(status asn1.RawValue) (time.Time, error) {
	var revocationTime time.Time
	if rest, err := asn1.Unmarshal(status.Bytes, &revocationTime); err != nil {
		return time.Time{}, fmt.Errorf("ocspcheck: cannot parse revocation time: %w", err)
	} else if len(rest) > 0 {
		// Remaining bytes may be the optional CRLReason extension; ignore it.
	}
	return revocationTime, nil
}

// resolveSigner picks the certificate that signed the response and, for
// delegated responders, fully validates the delegation against the issuer.
func (c *Config) resolveSigner(
	responderID asn1.RawValue,
	issuer *x509.Certificate,
	embedded []*x509.Certificate,
	now time.Time,
) (*x509.Certificate, error) {
	matches, err := responderIDMatches(responderID, issuer)
	if err != nil {
		return nil, err
	}
	if matches {
		// The issuing CA signed the response directly.
		return issuer, nil
	}
	for _, cert := range embedded {
		matches, err := responderIDMatches(responderID, cert)
		if err != nil {
			return nil, err
		}
		if !matches {
			continue
		}
		if err := validateDelegatedResponder(cert, issuer, now); err != nil {
			return nil, err
		}
		return cert, nil
	}
	return nil, errors.New("ocspcheck: OCSP response is not signed by the issuer and no valid delegated responder certificate is present")
}

// validateDelegatedResponder enforces RFC 6960 section 4.2.2.2.1: a delegated
// responder must be directly signed by the issuing CA, currently valid, not a
// CA itself, and hold the id-kp-OCSPSigning extended key usage.
func validateDelegatedResponder(cert, issuer *x509.Certificate, now time.Time) error {
	if cert.IsCA {
		return errors.New("ocspcheck: delegated OCSP responder certificate must not be a CA")
	}
	if err := cert.CheckSignatureFrom(issuer); err != nil {
		return fmt.Errorf("ocspcheck: delegated OCSP responder is not signed by the issuing CA: %w", err)
	}
	if now.Before(cert.NotBefore) || now.After(cert.NotAfter) {
		return fmt.Errorf("ocspcheck: delegated OCSP responder certificate is not valid at %s (validity %s..%s)",
			now.UTC().Format(time.RFC3339),
			cert.NotBefore.UTC().Format(time.RFC3339),
			cert.NotAfter.UTC().Format(time.RFC3339))
	}
	if cert.KeyUsage != 0 && cert.KeyUsage&x509.KeyUsageDigitalSignature == 0 {
		return errors.New("ocspcheck: delegated OCSP responder certificate lacks digitalSignature key usage")
	}
	if !hasOCSPSigningEKU(cert) {
		return errors.New("ocspcheck: delegated OCSP responder certificate lacks id-kp-OCSPSigning extended key usage")
	}
	return nil
}

func hasOCSPSigningEKU(cert *x509.Certificate) bool {
	for _, eku := range cert.ExtKeyUsage {
		if eku == x509.ExtKeyUsageOCSPSigning {
			return true
		}
	}
	for _, oid := range cert.UnknownExtKeyUsage {
		if oid.Equal(oidExtKeyOCSPSigning) {
			return true
		}
	}
	return false
}

// responderIDMatches implements both ResponderID choices from RFC 6960:
// [1] byName (the responder's distinguished name) and [2] byKey (SHA-1 of
// the responder's public key).
func responderIDMatches(rid asn1.RawValue, cert *x509.Certificate) (bool, error) {
	if rid.Class != asn1.ClassContextSpecific {
		return false, fmt.Errorf("ocspcheck: unsupported ResponderID class %d", rid.Class)
	}
	switch rid.Tag {
	case 1:
		var name asn1.RawValue
		if _, err := asn1.Unmarshal(cert.RawSubject, &name); err != nil {
			return false, fmt.Errorf("ocspcheck: cannot parse certificate subject: %w", err)
		}
		return bytes.Equal(rid.Bytes, name.Bytes), nil
	case 2:
		keyBytes, err := subjectPublicKeyBytes(cert)
		if err != nil {
			return false, err
		}
		sum := sha1.Sum(keyBytes)
		return bytes.Equal(rid.Bytes, sum[:]), nil
	default:
		return false, fmt.Errorf("ocspcheck: unsupported ResponderID tag %d", rid.Tag)
	}
}

func subjectPublicKeyBytes(cert *x509.Certificate) ([]byte, error) {
	var spki struct {
		Algorithm        pkix.AlgorithmIdentifier
		SubjectPublicKey asn1.BitString
	}
	if _, err := asn1.Unmarshal(cert.RawSubjectPublicKeyInfo, &spki); err != nil {
		return nil, fmt.Errorf("ocspcheck: cannot parse subject public key info: %w", err)
	}
	return spki.SubjectPublicKey.Bytes, nil
}

func findSingleResponse(
	responses []singleResponse,
	leaf, issuer *x509.Certificate,
) (*singleResponse, error) {
	for i := range responses {
		sr := &responses[i]
		ok, err := certIDMatches(sr.CertID, leaf, issuer)
		if err != nil {
			return nil, err
		}
		if ok {
			return sr, nil
		}
	}
	return nil, errors.New("ocspcheck: OCSP response contains no SingleResponse matching the leaf certificate")
}

// certIDMatches enforces the two CertID hashes (issuer name and issuer public
// key) together with the requested certificate serial number.
func certIDMatches(cid certID, leaf, issuer *x509.Certificate) (bool, error) {
	hashFunc, ok := oidToHash[cid.HashAlgorithm.Algorithm.String()]
	if !ok {
		return false, fmt.Errorf("ocspcheck: unsupported CertID hash algorithm %s", cid.HashAlgorithm.Algorithm)
	}

	nameHasher := hashFunc.New()
	nameHasher.Write(issuer.RawSubject)
	if !bytes.Equal(nameHasher.Sum(nil), cid.NameHash) {
		return false, nil
	}

	keyBytes, err := subjectPublicKeyBytes(issuer)
	if err != nil {
		return false, err
	}
	keyHasher := hashFunc.New()
	keyHasher.Write(keyBytes)
	if !bytes.Equal(keyHasher.Sum(nil), cid.KeyHash) {
		return false, nil
	}

	if leaf.SerialNumber == nil || leaf.SerialNumber.Cmp(cid.SerialNumber) != 0 {
		return false, nil
	}
	return true, nil
}

// verifyResponseSignature validates the BasicOCSPResponse signature against
// the resolved signer's public key using the algorithm OID in the response.
func verifyResponseSignature(resp *basicOCSPResponse, public crypto.PublicKey) error {
	alg, ok := sigAlgorithms[resp.tbs.SignatureAlgorithm.Algorithm.String()]
	if !ok {
		return fmt.Errorf("ocspcheck: unsupported OCSP signature algorithm %s", resp.tbs.SignatureAlgorithm.Algorithm)
	}
	signature := resp.signature.Bytes
	tbs := resp.tbs.RawTBSCertificate.FullBytes

	switch key := public.(type) {
	case *rsa.PublicKey:
		hashed := alg.hash.New()
		hashed.Write(tbs)
		digest := hashed.Sum(nil)
		if alg.pss {
			if err := rsa.VerifyPSS(key, alg.hash, digest, signature, nil); err != nil {
				return fmt.Errorf("ocspcheck: OCSP signature verification failed: %w", err)
			}
			return nil
		}
		if err := rsa.VerifyPKCS1v15(key, alg.hash, digest, signature); err != nil {
			return fmt.Errorf("ocspcheck: OCSP signature verification failed: %w", err)
		}
		return nil
	case *ecdsa.PublicKey:
		hashed := alg.hash.New()
		hashed.Write(tbs)
		if !ecdsa.VerifyASN1(key, hashed.Sum(nil), signature) {
			return errors.New("ocspcheck: OCSP ECDSA signature verification failed")
		}
		return nil
	case ed25519.PublicKey:
		if !ed25519.Verify(key, tbs, signature) {
			return errors.New("ocspcheck: OCSP Ed25519 signature verification failed")
		}
		return nil
	default:
		return fmt.Errorf("ocspcheck: unsupported OCSP signer public key type %T", public)
	}
}
