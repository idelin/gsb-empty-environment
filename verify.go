package netsec

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/x509"
	"errors"
	"fmt"
	"time"
)

// Verification failures returned by VerifyDeviceResponse. Use errors.Is to
// distinguish them.
var (
	// ErrMSOSignature means the issuerAuth COSE_Sign1 signature over the
	// MSO did not verify against the issuer key.
	ErrMSOSignature = errors.New("netsec: MSO signature verification failed")
	// ErrDigestMismatch means a disclosed value's digest does not equal the
	// corresponding entry in the MSO valueDigests.
	ErrDigestMismatch = errors.New("netsec: disclosed value digest does not match MSO valueDigests")
	// ErrUnknownDigestID means a disclosed item references a digestID (or
	// namespace) that is not present in the MSO valueDigests.
	ErrUnknownDigestID = errors.New("netsec: digestID not present in MSO valueDigests")
	// ErrNoIssuerKey means neither a pinned issuer key nor a usable
	// x5chain was available to verify the MSO signature.
	ErrNoIssuerKey = errors.New("netsec: no issuer key available")
	// ErrExpired means the MSO validityInfo does not cover the
	// verification time.
	ErrExpired = errors.New("netsec: MSO outside its validity period")
)

// Options controls verification of a DeviceResponse.
type Options struct {
	// IssuerKey, when set, is the pinned issuer public key used to verify
	// the MSO signature. It takes precedence over the x5chain header.
	IssuerKey *ecdsa.PublicKey
	// Roots, when set (and IssuerKey is not), is used to validate the
	// issuer certificate chain carried in the x5chain header; the leaf
	// public key then verifies the MSO signature.
	Roots *x509.CertPool
	// Now is the verification time for the MSO validity period.
	// Zero means time.Now().
	Now time.Time
}

// VerifiedElement is a disclosed data element whose digest was proven equal
// to the corresponding entry in a validly signed MSO.
type VerifiedElement struct {
	Namespace  string
	Identifier string
	Value      any
}

// VerifyDeviceResponse verifies an mdoc DeviceResponse (CBOR) and returns
// the disclosed elements.
//
// A document is accepted only when both hold:
//   - the issuerAuth COSE_Sign1 signature over the MSO verifies, and
//   - for every disclosed IssuerSignedItem, digest(item) equals the digest
//     stored under its (namespace, digestID) in the MSO valueDigests.
//
// Verifying only the MSO signature is not sufficient: an attacker can keep
// the signed MSO and replace a disclosed value. Any such tampering makes
// VerifyDeviceResponse fail with ErrDigestMismatch (or ErrUnknownDigestID).
func VerifyDeviceResponse(response []byte, opts *Options) ([]VerifiedElement, error) {
	if opts == nil {
		opts = &Options{}
	}
	now := opts.Now
	if now.IsZero() {
		now = time.Now()
	}
	top, err := cborMapEntries(response)
	if err != nil {
		return nil, fmt.Errorf("netsec: decoding DeviceResponse: %w", err)
	}
	docsRV, ok := top["documents"]
	if !ok {
		return nil, errors.New("netsec: DeviceResponse missing documents")
	}
	docs, err := cborArrayItems(docsRV.raw)
	if err != nil {
		return nil, fmt.Errorf("netsec: decoding documents: %w", err)
	}
	var out []VerifiedElement
	for i := range docs {
		elements, err := verifyDocument(docs[i].raw, opts, now)
		if err != nil {
			return nil, err
		}
		out = append(out, elements...)
	}
	return out, nil
}

func verifyDocument(docRaw []byte, opts *Options, now time.Time) ([]VerifiedElement, error) {
	doc, err := cborMapEntries(docRaw)
	if err != nil {
		return nil, fmt.Errorf("netsec: decoding Document: %w", err)
	}
	docType, _ := doc["docType"].value.(string)
	issuerSignedRV, ok := doc["issuerSigned"]
	if !ok {
		return nil, errors.New("netsec: Document missing issuerSigned")
	}
	issuerSigned, err := cborMapEntries(issuerSignedRV.raw)
	if err != nil {
		return nil, fmt.Errorf("netsec: decoding IssuerSigned: %w", err)
	}

	// 1. Verify the MSO signature.
	issuerAuthRV, ok := issuerSigned["issuerAuth"]
	if !ok {
		return nil, errors.New("netsec: IssuerSigned missing issuerAuth")
	}
	cose, err := parseCOSESign1(issuerAuthRV.raw)
	if err != nil {
		return nil, err
	}
	key, err := issuerKey(cose, opts, now)
	if err != nil {
		return nil, err
	}
	if err := cose.verify(key); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMSOSignature, err)
	}

	// 2. Parse the MSO from the verified payload.
	mso, err := parseMSO(cose.payload)
	if err != nil {
		return nil, err
	}
	if mso.DocType != docType {
		return nil, fmt.Errorf("netsec: docType %q does not match MSO docType %q", docType, mso.DocType)
	}
	if now.Before(mso.ValidFrom) || now.After(mso.ValidUntil) {
		return nil, fmt.Errorf("%w: valid %s..%s, now %s", ErrExpired, mso.ValidFrom, mso.ValidUntil, now)
	}
	hash, err := digestHash(mso.DigestAlgorithm)
	if err != nil {
		return nil, err
	}

	// 3. Check every disclosed value against the MSO valueDigests.
	nameSpacesRV, ok := issuerSigned["nameSpaces"]
	if !ok {
		return nil, errors.New("netsec: IssuerSigned missing nameSpaces")
	}
	nsValue, err := decodeCBOR(nameSpacesRV.raw)
	if err != nil {
		return nil, fmt.Errorf("netsec: decoding nameSpaces: %w", err)
	}
	nsTag, ok := nsValue.(cborTag)
	if !ok || nsTag.num != 24 {
		return nil, errors.New("netsec: nameSpaces must be tag 24")
	}
	nsBytes, ok := nsTag.value.([]byte)
	if !ok {
		return nil, errors.New("netsec: nameSpaces tag 24 must wrap a bstr")
	}
	namespaces, err := cborMapEntries(nsBytes)
	if err != nil {
		return nil, fmt.Errorf("netsec: decoding nameSpaces map: %w", err)
	}
	var out []VerifiedElement
	for nsKey, itemsRV := range namespaces {
		ns, ok := nsKey.(string)
		if !ok {
			return nil, errors.New("netsec: namespace must be text")
		}
		digests, ok := mso.ValueDigests[ns]
		if !ok {
			return nil, fmt.Errorf("%w: namespace %q", ErrUnknownDigestID, ns)
		}
		items, err := cborArrayItems(itemsRV.raw)
		if err != nil {
			return nil, fmt.Errorf("netsec: decoding items of namespace %q: %w", ns, err)
		}
		for _, itemRV := range items {
			item, err := parseIssuerSignedItem(itemRV.raw)
			if err != nil {
				return nil, err
			}
			expected, ok := digests[item.DigestID]
			if !ok {
				return nil, fmt.Errorf("%w: namespace %q digestID %d", ErrUnknownDigestID, ns, item.DigestID)
			}
			h := hash.New()
			h.Write(item.Raw)
			if !bytes.Equal(h.Sum(nil), expected) {
				return nil, fmt.Errorf("%w: namespace %q element %q (digestID %d)",
					ErrDigestMismatch, ns, item.ElementIdentifier, item.DigestID)
			}
			out = append(out, VerifiedElement{
				Namespace:  ns,
				Identifier: item.ElementIdentifier,
				Value:      item.ElementValue,
			})
		}
	}
	return out, nil
}

// issuerKey resolves the public key used to verify the MSO signature: either
// the pinned key, or the leaf of the x5chain validated against Roots.
func issuerKey(c *coseSign1, opts *Options, now time.Time) (*ecdsa.PublicKey, error) {
	if opts.IssuerKey != nil {
		return opts.IssuerKey, nil
	}
	chain, err := c.x5chain()
	if err != nil {
		return nil, err
	}
	if len(chain) == 0 {
		return nil, ErrNoIssuerKey
	}
	certs := make([]*x509.Certificate, 0, len(chain))
	for _, der := range chain {
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return nil, fmt.Errorf("netsec: parsing x5chain certificate: %w", err)
		}
		certs = append(certs, cert)
	}
	if opts.Roots == nil {
		return nil, fmt.Errorf("%w: x5chain present but no trust roots configured", ErrNoIssuerKey)
	}
	intermediates := x509.NewCertPool()
	for _, cert := range certs[1:] {
		intermediates.AddCert(cert)
	}
	if _, err := certs[0].Verify(x509.VerifyOptions{
		Roots:         opts.Roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err != nil {
		return nil, fmt.Errorf("netsec: issuer certificate chain invalid: %w", err)
	}
	pub, ok := certs[0].PublicKey.(*ecdsa.PublicKey)
	if !ok {
		return nil, errors.New("netsec: issuer certificate public key is not ECDSA")
	}
	return pub, nil
}
