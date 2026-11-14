package netsec

import (
	"bytes"
	"crypto"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/x509"
	"encoding/asn1"
	"errors"
	"fmt"
	"strings"
	"time"
)

// VerifierConfig controls timestamp trust, time and cache policy.
type VerifierConfig struct {
	Roots                    *x509.CertPool
	Intermediates            *x509.CertPool
	TrustedCertificates      []*x509.Certificate
	TrustedIssuers           []string
	AllowedPolicies          []asn1.ObjectIdentifier
	RevocationChecker        RevocationChecker
	Clock                    func() time.Time
	ClockSkew                time.Duration
	MaxTokenAge              time.Duration
	Cache                    *TokenCache
	CacheTTL                 time.Duration
	SerialRegistry           *SerialRegistry
	AllowAnyExtendedKeyUsage bool
}

// Option mutates a VerifierConfig during construction.
type Option func(*VerifierConfig)

func WithRoots(roots *x509.CertPool) Option {
	return func(config *VerifierConfig) { config.Roots = roots }
}

func WithIntermediates(intermediates *x509.CertPool) Option {
	return func(config *VerifierConfig) { config.Intermediates = intermediates }
}

func WithTrustedIssuers(issuers ...string) Option {
	return func(config *VerifierConfig) { config.TrustedIssuers = append(config.TrustedIssuers, issuers...) }
}

func WithTrustedCertificates(certificates ...*x509.Certificate) Option {
	return func(config *VerifierConfig) {
		config.TrustedCertificates = append(config.TrustedCertificates, certificates...)
	}
}

func WithAllowedPolicies(policies ...asn1.ObjectIdentifier) Option {
	return func(config *VerifierConfig) { config.AllowedPolicies = append(config.AllowedPolicies, policies...) }
}

func WithRevocationChecker(checker RevocationChecker) Option {
	return func(config *VerifierConfig) { config.RevocationChecker = checker }
}

func WithClock(clock func() time.Time) Option {
	return func(config *VerifierConfig) { config.Clock = clock }
}

func WithClockSkew(skew time.Duration) Option {
	return func(config *VerifierConfig) { config.ClockSkew = skew }
}

func WithMaxTokenAge(maxAge time.Duration) Option {
	return func(config *VerifierConfig) { config.MaxTokenAge = maxAge }
}

func WithCache(cache *TokenCache) Option {
	return func(config *VerifierConfig) { config.Cache = cache }
}

func WithCacheTTL(ttl time.Duration) Option {
	return func(config *VerifierConfig) { config.CacheTTL = ttl }
}

func WithSerialRegistry(registry *SerialRegistry) Option {
	return func(config *VerifierConfig) { config.SerialRegistry = registry }
}

// Verifier validates RFC 3161 timestamp tokens and binds them to signatures.
type Verifier struct {
	config          VerifierConfig
	roots           *x509.CertPool
	trustedCerts    []*x509.Certificate
	trustedIssuers  []string
	allowedPolicies []asn1.ObjectIdentifier
	revocations     RevocationChecker
	clock           func() time.Time
	cache           *TokenCache
	registry        *SerialRegistry
}

// TimestampVerifier is an alias retained for importers that prefer the
// explicit timestamp-verifier name.
type TimestampVerifier = Verifier

func NewVerifier(config VerifierConfig, options ...Option) (*Verifier, error) {
	for _, option := range options {
		option(&config)
	}
	if config.Clock == nil {
		config.Clock = time.Now
	}
	if config.ClockSkew == 0 {
		config.ClockSkew = 5 * time.Minute
	}
	if config.CacheTTL == 0 {
		config.CacheTTL = DefaultCacheTTL
	}
	if config.Cache == nil {
		config.Cache = NewTokenCache(config.CacheTTL)
	}
	if config.SerialRegistry == nil {
		config.SerialRegistry = NewSerialRegistry()
	}

	roots := config.Roots
	if roots == nil {
		roots = x509.NewCertPool()
	}
	for _, certificate := range config.TrustedCertificates {
		if certificate != nil {
			roots.AddCert(certificate)
		}
	}
	if config.Roots == nil && len(config.TrustedCertificates) == 0 {
		return nil, ErrNoTrustAnchor
	}

	return &Verifier{
		config:          config,
		roots:           roots,
		trustedCerts:    append([]*x509.Certificate(nil), config.TrustedCertificates...),
		trustedIssuers:  append([]string(nil), config.TrustedIssuers...),
		allowedPolicies: append([]asn1.ObjectIdentifier(nil), config.AllowedPolicies...),
		revocations:     config.RevocationChecker,
		clock:           config.Clock,
		cache:           config.Cache,
		registry:        config.SerialRegistry,
	}, nil
}

func NewTimestampVerifier(config VerifierConfig, options ...Option) (*Verifier, error) {
	return NewVerifier(config, options...)
}

func New(config VerifierConfig, options ...Option) (*Verifier, error) {
	return NewVerifier(config, options...)
}

// Verify binds one RFC 3161 token to the supplied code-signing signature. The
// signing certificate must have been valid at the token's genTime.
func (v *Verifier) Verify(signature []byte, token []byte, signerCertificate *x509.Certificate) (*Binding, error) {
	return v.VerifyRequest(VerificationRequest{
		Token:             token,
		Signature:         signature,
		SignerCertificate: signerCertificate,
	})
}

// VerifyToken is the same operation as Verify with token first.
func (v *Verifier) VerifyToken(token []byte, signature []byte, signerCertificate *x509.Certificate) (*Binding, error) {
	return v.Verify(signature, token, signerCertificate)
}

// Bind is an alias for VerifyToken.
func (v *Verifier) Bind(token []byte, signature []byte, signerCertificate *x509.Certificate) (*Binding, error) {
	return v.VerifyToken(token, signature, signerCertificate)
}

// VerifyDigest binds a token to a digest that the caller computed separately.
func (v *Verifier) VerifyDigest(digest []byte, hash crypto.Hash, token []byte, signerCertificate *x509.Certificate) (*Binding, error) {
	return v.VerifyRequest(VerificationRequest{
		Token:             token,
		Digest:            digest,
		HashAlgorithm:     hash,
		SignerCertificate: signerCertificate,
	})
}

// VerifyTokens enforces the single-token rule explicitly. Multiple timestamp
// tokens for one signature are rejected rather than merged.
func (v *Verifier) VerifyTokens(signature []byte, tokens [][]byte, signerCertificate *x509.Certificate) (*Binding, error) {
	return v.VerifyRequest(VerificationRequest{
		Tokens:            tokens,
		Signature:         signature,
		SignerCertificate: signerCertificate,
	})
}

func (v *Verifier) VerifyRequest(request VerificationRequest) (*Binding, error) {
	if v == nil {
		return nil, errorf(ErrNoTrustAnchor, "verifier is nil")
	}
	tokenBytes, err := selectOneToken(request)
	if err != nil {
		return nil, err
	}
	if request.SignerCertificate == nil {
		return nil, ErrNoSignerCertificate
	}
	token, err := ParseTimestampToken(tokenBytes)
	if err != nil {
		return nil, err
	}
	if len(v.allowedPolicies) > 0 {
		allowed := false
		for _, policy := range v.allowedPolicies {
			if equalOID(policy, token.Policy) {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, errorf(ErrPolicyNotAllowed, "policy %s", token.Policy.String())
		}
	}

	digest, err := artifactDigest(request, token)
	if err != nil {
		return nil, err
	}
	if err := checkImprint(token.MessageImprint, digest); err != nil {
		return nil, err
	}
	if err := v.checkGenerationTime(token, request.SignerCertificate); err != nil {
		return nil, err
	}

	now := v.clock()
	tokenDigest := sha256.Sum256(token.Raw)
	if binding, hit, cacheErr := v.lookupCache(token, tokenDigest, digest, request.SignerCertificate, now); cacheErr != nil {
		return nil, cacheErr
	} else if hit {
		return binding, nil
	}

	signer, chain, err := v.selectTimestampSigner(token, now)
	if err != nil {
		return nil, err
	}
	if err := v.checkRevoked([]*x509.Certificate{request.SignerCertificate}, token.GenTime); err != nil {
		return nil, err
	}
	if err := v.checkRevoked([]*x509.Certificate{request.SignerCertificate}, now); err != nil {
		return nil, err
	}
	if err := v.registry.CheckAndRecord(signer.IssuerIdentity, token.SerialNumber, tokenDigest, now); err != nil {
		return nil, err
	}

	binding := &Binding{
		Timestamp:                  token.GenTime,
		GenTime:                    token.GenTime,
		Policy:                     token.Policy,
		SerialNumber:               cloneBigInt(token.SerialNumber),
		Imprint:                    cloneImprint(token.MessageImprint),
		ArtifactDigest:             bytes.Clone(digest),
		Issuer:                     signer.IssuerIdentity,
		IssuerName:                 signer.IssuerName,
		TimestampSignerCertificate: signer.Certificate,
		SignerCertificate:          request.SignerCertificate,
		Chain:                      chain,
		Token:                      token,
	}
	v.cacheBinding(binding, tokenDigest, now)
	return binding, nil
}

// VerifyAll verifies independently paired signatures and tokens. A token may
// only be reused when it covers the same signature; its time is never applied
// to another signature in the batch.
func (v *Verifier) VerifyAll(requests []VerificationRequest) ([]*Binding, error) {
	if len(requests) == 0 {
		return nil, ErrNoTimestampToken
	}
	tokenArtifacts := make(map[[32]byte]string)
	bindings := make([]*Binding, 0, len(requests))
	for _, request := range requests {
		tokenBytes, err := selectOneToken(request)
		if err != nil {
			return nil, err
		}
		tokenDigest := sha256.Sum256(tokenBytes)
		parsedToken, err := ParseTimestampToken(tokenBytes)
		if err != nil {
			return nil, err
		}
		artifact, err := canonicalArtifactKey(request, parsedToken)
		if err != nil {
			return nil, err
		}
		if previous, ok := tokenArtifacts[tokenDigest]; ok && previous != artifact {
			return nil, errorf(ErrTokenReuse, "one token was supplied for different signatures")
		}
		tokenArtifacts[tokenDigest] = artifact
		binding, err := v.VerifyRequest(request)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

// VerifyTimestamp is a convenience constructor-and-verify entry point.
func VerifyTimestamp(request VerificationRequest, config VerifierConfig, options ...Option) (*Binding, error) {
	verifier, err := NewVerifier(config, options...)
	if err != nil {
		return nil, err
	}
	return verifier.VerifyRequest(request)
}

func selectOneToken(request VerificationRequest) ([]byte, error) {
	hasToken := len(request.Token) > 0
	hasTokens := len(request.Tokens) > 0
	if hasToken && hasTokens {
		return nil, errorf(ErrMultipleTokens, "both Token and Tokens were supplied")
	}
	if hasToken {
		return request.Token, nil
	}
	switch len(request.Tokens) {
	case 0:
		return nil, ErrNoTimestampToken
	case 1:
		if len(request.Tokens[0]) == 0 {
			return nil, ErrNoTimestampToken
		}
		return request.Tokens[0], nil
	default:
		return nil, errorf(ErrMultipleTokens, "got %d tokens", len(request.Tokens))
	}
}

func artifactDigest(request VerificationRequest, token *TimestampToken) ([]byte, error) {
	hasSignature := request.Signature != nil
	hasDigest := request.Digest != nil
	if !hasSignature && !hasDigest {
		return nil, ErrNoSignature
	}
	if request.HashAlgorithm != 0 && request.HashAlgorithm != token.MessageImprint.Hash {
		return nil, errorf(ErrDigestAlgorithmMismatch,
			"requested %v, token uses %s", request.HashAlgorithm, token.MessageImprint.HashName)
	}
	hash := token.MessageImprint.Hash
	var signatureDigest []byte
	if hasSignature {
		if len(request.Signature) == 0 {
			return nil, ErrNoSignature
		}
		var err error
		signatureDigest, err = digestBytes(hash, request.Signature)
		if err != nil {
			return nil, err
		}
	}
	if hasDigest {
		if len(request.Digest) != hash.Size() {
			return nil, errorf(ErrDigestAlgorithmMismatch,
				"digest has %d bytes, token uses %s (%d bytes)", len(request.Digest), token.MessageImprint.HashName, hash.Size())
		}
		if signatureDigest != nil && !equalBytes(signatureDigest, request.Digest) {
			return nil, errorf(ErrDigestAlgorithmMismatch, "supplied digest does not match signature")
		}
		return bytes.Clone(request.Digest), nil
	}
	return signatureDigest, nil
}

func checkImprint(imprint MessageImprint, digest []byte) error {
	if len(imprint.HashedMessage) != len(digest) ||
		subtle.ConstantTimeCompare(imprint.HashedMessage, digest) != 1 {
		return errorf(ErrImprintMismatch, "timestamp is bound to a different signature digest")
	}
	return nil
}

func (v *Verifier) checkGenerationTime(token *TimestampToken, signer *x509.Certificate) error {
	now := v.clock()
	if token.GenTime.After(now.Add(v.config.ClockSkew)) {
		return errorf(ErrTimestampInFuture, "genTime %s is after %s", token.GenTime.UTC().Format(time.RFC3339Nano), now.UTC().Format(time.RFC3339Nano))
	}
	if v.config.MaxTokenAge > 0 && now.Sub(token.GenTime) > v.config.MaxTokenAge {
		return errorf(ErrTokenExpired, "token age exceeds %s", v.config.MaxTokenAge)
	}
	if token.GenTime.Before(signer.NotBefore) || token.GenTime.After(signer.NotAfter) {
		return errorf(ErrSignerCertificateExpired,
			"genTime %s is outside signer validity [%s, %s]",
			token.GenTime.UTC().Format(time.RFC3339Nano),
			signer.NotBefore.UTC().Format(time.RFC3339Nano),
			signer.NotAfter.UTC().Format(time.RFC3339Nano))
	}
	return nil
}

func (v *Verifier) lookupCache(token *TimestampToken, tokenDigest [32]byte, digest []byte, signerCertificate *x509.Certificate, now time.Time) (*Binding, bool, error) {
	for _, signer := range token.Signers {
		if signer.Certificate == nil {
			continue
		}
		key := NewCacheKey(token.MessageImprint, signer.IssuerIdentity)
		entry, ok := v.cache.Get(key, now)
		if !ok {
			continue
		}
		if entry.TokenDigest != tokenDigest || !equalBytes(entry.Imprint.HashedMessage, digest) {
			v.cache.Invalidate(key)
			continue
		}
		if entry.TimestampSigner == nil ||
			!entry.TimestampSigner.Equal(signer.Certificate) ||
			!v.issuerAllowed(signer.Certificate, entry.Chain) {
			v.cache.Invalidate(key)
			continue
		}
		if token.GenTime.Before(entry.TimestampSigner.NotBefore) ||
			token.GenTime.After(entry.TimestampSigner.NotAfter) {
			v.cache.Invalidate(key)
			continue
		}
		if err := v.checkRevoked(entry.Chain, token.GenTime); err != nil {
			v.cache.Invalidate(key)
			return nil, false, err
		}
		if err := v.checkRevoked(entry.Chain, now); err != nil {
			v.cache.Invalidate(key)
			return nil, false, err
		}
		if err := v.checkRevoked([]*x509.Certificate{signerCertificate}, token.GenTime); err != nil {
			v.cache.Invalidate(key)
			return nil, false, err
		}
		if err := v.checkRevoked([]*x509.Certificate{signerCertificate}, now); err != nil {
			v.cache.Invalidate(key)
			return nil, false, err
		}
		if err := v.registry.CheckAndRecord(signer.IssuerIdentity, token.SerialNumber, tokenDigest, now); err != nil {
			return nil, false, err
		}
		bindingCopy := *entry.Binding
		bindingCopy.SignerCertificate = signerCertificate
		bindingCopy.Token = token
		bindingCopy.CacheHit = true
		return &bindingCopy, true, nil
	}
	return nil, false, nil
}

func (v *Verifier) cacheBinding(binding *Binding, tokenDigest [32]byte, now time.Time) {
	hardDeadline := binding.TimestampSignerCertificate.NotAfter
	if binding.SignerCertificate.NotAfter.Before(hardDeadline) {
		hardDeadline = binding.SignerCertificate.NotAfter
	}
	if v.config.MaxTokenAge > 0 {
		maxAgeDeadline := binding.GenTime.Add(v.config.MaxTokenAge)
		if maxAgeDeadline.Before(hardDeadline) {
			hardDeadline = maxAgeDeadline
		}
	}
	if !hardDeadline.IsZero() && !now.Before(hardDeadline) {
		return
	}
	entry := CacheEntry{
		TokenDigest:       tokenDigest,
		Imprint:           cloneImprint(binding.Imprint),
		Issuer:            binding.Issuer,
		SerialNumber:      cloneBigInt(binding.SerialNumber),
		GenTime:           binding.GenTime,
		TimestampSigner:   binding.TimestampSignerCertificate,
		SignerCertificate: binding.SignerCertificate,
		Chain:             append([]*x509.Certificate(nil), binding.Chain...),
		Binding:           binding,
	}
	v.cache.Put(entry, now, hardDeadline)
}

func (v *Verifier) selectTimestampSigner(token *TimestampToken, now time.Time) (*TokenSigner, []*x509.Certificate, error) {
	var chainFailure, trustFailure, revocationFailure error
	validSigner := false
	for index := range token.Signers {
		signer := &token.Signers[index]
		if !signer.SignatureVerified || signer.Certificate == nil {
			continue
		}
		validSigner = true
		chain, err := v.verifyTimestampChain(signer.Certificate, token, token.GenTime)
		if err != nil {
			if chainFailure == nil {
				chainFailure = err
			}
			continue
		}
		if !v.issuerAllowed(signer.Certificate, chain) {
			if trustFailure == nil {
				trustFailure = errorf(ErrIssuerNotTrusted, "issuer %s", signer.IssuerName)
			}
			continue
		}
		if err := v.checkRevoked(chain, token.GenTime); err != nil {
			if revocationFailure == nil {
				revocationFailure = err
			}
			continue
		}
		if err := v.checkRevoked(chain, now); err != nil {
			if revocationFailure == nil {
				revocationFailure = err
			}
			continue
		}
		return signer, chain, nil
	}
	switch {
	case !validSigner:
		return nil, nil, errorf(ErrTokenSignature, "all token signatures are invalid")
	case revocationFailure != nil:
		return nil, nil, revocationFailure
	case chainFailure != nil:
		return nil, nil, chainFailure
	case trustFailure != nil:
		return nil, nil, trustFailure
	default:
		return nil, nil, errorf(ErrCertificateChain, "no usable timestamp signer")
	}
}

func (v *Verifier) verifyTimestampChain(signer *x509.Certificate, token *TimestampToken, at time.Time) ([]*x509.Certificate, error) {
	if signer == nil {
		return nil, errorf(ErrCertificateChain, "timestamp signer certificate is absent")
	}
	if at.Before(signer.NotBefore) || at.After(signer.NotAfter) {
		return nil, errorf(ErrCertificateChain, "timestamp signer certificate is not valid at genTime")
	}
	for _, trusted := range v.trustedCerts {
		if trusted != nil && trusted.Equal(signer) {
			return []*x509.Certificate{signer}, nil
		}
	}

	options := x509.VerifyOptions{
		Roots:       v.roots,
		CurrentTime: at,
	}
	if v.config.AllowAnyExtendedKeyUsage {
		options.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageAny}
	} else {
		options.KeyUsages = []x509.ExtKeyUsage{x509.ExtKeyUsageTimeStamping}
	}

	pools := []*x509.CertPool{nil}
	if embedded := certificatePool(tokenIntermediates(token, signer)); embedded != nil {
		pools = append(pools, embedded)
	}
	if v.config.Intermediates != nil {
		pools = append(pools, v.config.Intermediates)
	}
	var lastErr error
	for _, pool := range pools {
		options.Intermediates = pool
		chains, err := signer.Verify(options)
		if err == nil && len(chains) > 0 {
			return chains[0], nil
		}
		if err != nil {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = errors.New("no verification chain")
	}
	return nil, wrapError(ErrCertificateChain, lastErr)
}

func tokenIntermediates(token *TimestampToken, signer *x509.Certificate) []*x509.Certificate {
	if token == nil || signer == nil {
		return nil
	}
	var certificates []*x509.Certificate
	for _, certificate := range token.Certificates {
		if certificate != nil && !certificate.Equal(signer) {
			certificates = append(certificates, certificate)
		}
	}
	return certificates
}

func certificatePool(certificates []*x509.Certificate) *x509.CertPool {
	if len(certificates) == 0 {
		return nil
	}
	pool := x509.NewCertPool()
	added := false
	for _, certificate := range certificates {
		if certificate != nil {
			pool.AddCert(certificate)
			added = true
		}
	}
	if !added {
		return nil
	}
	return pool
}

func (v *Verifier) issuerAllowed(signer *x509.Certificate, chain []*x509.Certificate) bool {
	if signer == nil {
		return false
	}
	for _, trusted := range v.trustedCerts {
		if trusted != nil && trusted.Equal(signer) {
			return true
		}
	}
	if len(v.trustedIssuers) == 0 {
		return true
	}
	certificates := []*x509.Certificate{signer}
	certificates = append(certificates, chain...)
	for _, expected := range v.trustedIssuers {
		expected = strings.TrimSpace(expected)
		if expected == "" {
			continue
		}
		for _, certificate := range certificates {
			if certificateMatchesName(certificate, expected) {
				return true
			}
		}
	}
	return false
}

func certificateMatchesName(certificate *x509.Certificate, expected string) bool {
	if certificate == nil {
		return false
	}
	normalized := normalizeIdentity(expected)
	identity := certIdentity(certificate)
	candidates := []string{
		certificate.Subject.String(),
		certificate.Subject.CommonName,
		certificate.Issuer.String(),
		certificate.Issuer.CommonName,
		identity.String(),
		hexString(identity.CertificateSHA256[:]),
		hexString(identity.PublicKeySHA256[:]),
	}
	candidates = append(candidates, certificate.Subject.Organization...)
	candidates = append(candidates, certificate.Subject.OrganizationalUnit...)
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if normalizeIdentity(candidate) == normalized {
			return true
		}
	}
	if separator := strings.IndexByte(normalized, '='); separator > 0 {
		attribute := normalized[:separator]
		value := normalized[separator+1:]
		switch attribute {
		case "cn":
			return normalizeIdentity(certificate.Subject.CommonName) == value
		case "o":
			for _, organization := range certificate.Subject.Organization {
				if normalizeIdentity(organization) == value {
					return true
				}
			}
		case "ou":
			for _, unit := range certificate.Subject.OrganizationalUnit {
				if normalizeIdentity(unit) == value {
					return true
				}
			}
		}
	}
	fingerprint := strings.ReplaceAll(strings.ReplaceAll(normalized, ":", ""), " ", "")
	fingerprint = strings.TrimPrefix(fingerprint, "sha256")
	return fingerprint == hexString(identity.CertificateSHA256[:])
}

func (v *Verifier) checkRevoked(certificates []*x509.Certificate, at time.Time) error {
	if v.revocations == nil {
		return nil
	}
	for _, certificate := range certificates {
		if certificate == nil {
			continue
		}
		revoked, err := v.revocations.IsRevoked(certificate, at)
		if err != nil {
			return wrapError(ErrRevocationCheck, err)
		}
		if revoked {
			return errorf(ErrTokenRevoked, "certificate %s is revoked", certificate.Subject.String())
		}
	}
	return nil
}

func cloneImprint(imprint MessageImprint) MessageImprint {
	imprint.HashedMessage = bytes.Clone(imprint.HashedMessage)
	return imprint
}

func canonicalArtifactKey(request VerificationRequest, token *TimestampToken) (string, error) {
	digest, err := artifactDigest(request, token)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%x", token.MessageImprint.HashAlgorithm.String(), digest), nil
}
