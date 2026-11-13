package rfc3161

import (
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"errors"
	"math/big"
	"time"
)

// Verify parses and verifies an RFC 3161 TimeStampToken (the DER-encoded
// ContentInfo produced by a TSA) against the request values and trust policy
// in opts. On success it returns the verified token details.
//
// The checks performed are:
//
//   - The token is a structurally valid CMS SignedData wrapping a TSTInfo and
//     signed by exactly one signer whose certificate is embedded in the token.
//   - The TSTInfo messageImprint uses opts.HashAlgorithm and its digest equals
//     opts.HashedMessage byte for byte.
//   - The TSTInfo nonce equals opts.Nonce (both absent is also a match).
//   - The TSTInfo policy OID is present in opts.PolicyWhitelist.
//   - The signer certificate has a single critical EKU extension containing
//     only id-kp-timeStamping (1.3.6.1.5.5.7.3.8).
//   - genTime lies inside the signer certificate validity period and
//     genTime+accuracy is not after the verification instant.
//   - The CMS signature, including the message-digest attribute, verifies
//     against the signer public key.
//   - When opts.Roots is non-nil, the signer certificate chains to a trusted
//     root for the timeStamping EKU; otherwise chain validation is skipped.
func Verify(tokenDER []byte, opts Options) (*Info, error) {
	policies, err := opts.validate()
	if err != nil {
		return nil, err
	}

	token, err := parseToken(tokenDER)
	if err != nil {
		return nil, err
	}

	if token.info.Version != 1 {
		return nil, errors.Join(ErrInvalidToken, errors.New("unexpected TSTInfo version"))
	}

	if err := verifyMessageImprint(token.info.MessageImprint, opts); err != nil {
		return nil, err
	}
	if err := verifyNonce(token.info.Nonce, opts.Nonce); err != nil {
		return nil, err
	}
	if err := verifyPolicy(token.info.Policy, policies); err != nil {
		return nil, err
	}
	if err := verifyTimeStampingEKU(token.signer); err != nil {
		return nil, err
	}
	if err := token.verifySignature(); err != nil {
		return nil, err
	}

	accuracy, err := parseAccuracy(token.info.Accuracy)
	if err != nil {
		return nil, err
	}
	now := opts.CurrentTime
	if now.IsZero() {
		now = time.Now()
	}
	if err := verifyTimeWindow(token.info.GenTime, accuracy, token.signer, now); err != nil {
		return nil, err
	}

	if opts.Roots != nil {
		if err := verifyChain(token, opts.Roots, now); err != nil {
			return nil, err
		}
	}

	return &Info{
		GenTime:      token.info.GenTime,
		Accuracy:     accuracy,
		GenTimeUpper: token.info.GenTime.Add(accuracy),
		Policy:       token.info.Policy.String(),
		SerialNumber: token.info.SerialNumber,
		Signer:       token.signer,
	}, nil
}

// verifyMessageImprint enforces both the algorithm OID and the digest value of
// the messageImprint against the digest of the data being time stamped.
func verifyMessageImprint(imprint messageImprint, opts Options) error {
	wantOID := hashOIDs[opts.HashAlgorithm]
	if !imprint.HashAlgorithm.Algorithm.Equal(wantOID) {
		return errors.Join(ErrMessageImprint,
			errors.New("messageImprint algorithm OID does not match the requested hash algorithm"))
	}
	if !algorithmParamsAbsent(imprint.HashAlgorithm) {
		return errors.Join(ErrMessageImprint,
			errors.New("messageImprint algorithm must not carry parameters"))
	}
	if !equalRawBytes(imprint.HashedMessage, opts.HashedMessage) {
		return errors.Join(ErrMessageImprint,
			errors.New("messageImprint digest does not match the digest of the signed data"))
	}
	if len(imprint.HashedMessage) != opts.HashAlgorithm.Size() {
		return errors.Join(ErrMessageImprint,
			errors.New("messageImprint digest length does not match the hash algorithm"))
	}
	return nil
}

func algorithmParamsAbsent(alg pkix.AlgorithmIdentifier) bool {
	return len(alg.Parameters.FullBytes) == 0 ||
		(alg.Parameters.Tag == asn1.TagNull && len(alg.Parameters.Bytes) == 0)
}

// verifyNonce requires token and request nonces to be identical. A nil
// request nonce requires the token to omit the nonce, so a token that
// introduces one unprompted cannot pass verification.
func verifyNonce(tokenNonce, requestNonce *big.Int) error {
	switch {
	case tokenNonce == nil && requestNonce == nil:
		return nil
	case tokenNonce == nil || requestNonce == nil:
		return errors.Join(ErrNonce, errors.New("nonce present on only one of request and token"))
	case tokenNonce.Cmp(requestNonce) != 0:
		return errors.Join(ErrNonce, errors.New("token nonce differs from request nonce"))
	}
	return nil
}

func verifyPolicy(tokenPolicy asn1.ObjectIdentifier, allowed map[string]asn1.ObjectIdentifier) error {
	if _, ok := allowed[tokenPolicy.String()]; !ok {
		return errors.Join(ErrPolicy,
			errors.New("token policy OID is not in the whitelist: "+tokenPolicy.String()))
	}
	return nil
}

// verifyTimeStampingEKU inspects the raw extensions so that the EKU must be
// explicitly present, marked critical, and contain exactly one purpose OID,
// id-kp-timeStamping. Anything else (missing, non-critical, additional EKUs)
// fails.
func verifyTimeStampingEKU(cert *x509.Certificate) error {
	for _, ext := range cert.Extensions {
		if !ext.Id.Equal(oidExtKeyUsage) {
			continue
		}
		if !ext.Critical {
			return errors.Join(ErrEKU, errors.New("extKeyUsage extension is not critical"))
		}
		var purposes []asn1.ObjectIdentifier
		if _, err := asn1.Unmarshal(ext.Value, &purposes); err != nil {
			return errors.Join(ErrEKU, err)
		}
		if len(purposes) != 1 || !purposes[0].Equal(oidKPTimeStamping) {
			return errors.Join(ErrEKU,
				errors.New("extKeyUsage must contain only id-kp-timeStamping"))
		}
		return nil
	}
	return errors.Join(ErrEKU, errors.New("certificate has no extKeyUsage extension"))
}

// parseAccuracy converts an RFC 3161 Accuracy into a Duration, rejecting the
// out-of-range millis/micros values that DER alone cannot exclude.
func parseAccuracy(acc accuracy) (time.Duration, error) {
	if acc.Millis < 0 || acc.Millis > 999 || acc.Micros < 0 || acc.Micros > 999 {
		return 0, errors.Join(ErrInvalidToken, errors.New("accuracy millis/micros must be in [1,999]"))
	}
	if acc.Seconds < 0 {
		return 0, errors.Join(ErrInvalidToken, errors.New("accuracy seconds must not be negative"))
	}
	d := time.Duration(acc.Seconds) * time.Second
	d += time.Duration(acc.Millis) * time.Millisecond
	d += time.Duration(acc.Micros) * time.Microsecond
	return d, nil
}

// verifyTimeWindow enforces both certificate-validity and the "not in the
// future" condition. genTime alone must be inside the certificate lifetime;
// the upper bound genTime+accuracy must not be later than the verification
// instant, otherwise the claimed earliest time could still be in the future.
func verifyTimeWindow(genTime time.Time, accuracy time.Duration, cert *x509.Certificate, now time.Time) error {
	if genTime.Before(cert.NotBefore) {
		return errors.Join(ErrTime, errors.New("genTime is before the signer certificate NotBefore"))
	}
	if genTime.After(cert.NotAfter) {
		return errors.Join(ErrTime, errors.New("genTime is after the signer certificate NotAfter"))
	}
	upper := genTime.Add(accuracy)
	if upper.After(now) {
		return errors.Join(ErrTime, errors.New("genTime plus accuracy is later than the verification time"))
	}
	return nil
}

func verifyChain(token *parsedToken, roots *x509.CertPool, now time.Time) error {
	intermediates := x509.NewCertPool()
	// Every other certificate in the bag is a candidate intermediate.
	rest := token.signedData.Certificates.Bytes
	for len(rest) > 0 {
		var raw asn1.RawValue
		var err error
		rest, err = asn1.Unmarshal(rest, &raw)
		if err != nil {
			return errors.Join(ErrChain, err)
		}
		if raw.Tag != asn1.TagSequence || raw.Class != asn1.ClassUniversal {
			continue
		}
		cert, err := x509.ParseCertificate(raw.FullBytes)
		if err != nil {
			return errors.Join(ErrChain, err)
		}
		if !equalRawBytes(cert.Raw, token.signer.Raw) {
			intermediates.AddCert(cert)
		}
	}

	_, err := token.signer.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   now,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping},
	})
	if err != nil {
		return errors.Join(ErrChain, err)
	}
	return nil
}
